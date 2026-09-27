// Package llm 提供 OpenAI 兼容(/chat/completions)大模型客户端。
// 适配 DeepSeek / GLM / Qwen / Kimi / 本地 vLLM-Ollama 等一切兼容端点。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/textutil"
)

// Message 一条对话消息。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client LLM 客户端(不可变,并发安全)。
type Client struct {
	baseURL string
	apiKey  string
	model   string
	timeout time.Duration
	http    *http.Client
}

// New 创建客户端。超时默认 600s:深度推理模型(deepseek-v4-flash 等)分镜
// 直出任务实测思考+输出可长达 4-6 分钟,300s 会误杀。
func New(baseURL, apiKey, model string, timeoutSec int) *Client {
	if timeoutSec <= 0 {
		timeoutSec = 600
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		timeout: time.Duration(timeoutSec) * time.Second,
		http:    &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}
}

// Configured 是否已配置可用。
func (c *Client) Configured() bool {
	return c.baseURL != "" && c.apiKey != "" && c.model != ""
}

type chatRequest struct {
	Model          string    `json:"model"`
	Messages       []Message `json:"messages"`
	Temperature    float64   `json:"temperature"`
	MaxTokens      int       `json:"max_tokens,omitempty"`
	ResponseFormat *struct {
		Type string `json:"type"`
	} `json:"response_format,omitempty"`
	Stream bool `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatRespMessage `json:"message"`
		FinishReason string          `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Usage 本次调用的 token 用量(截断排查用:completion 顶到上限即被截断)。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Result 一次对话的正文与结束信息。
//
// 为什么要把 finish_reason / usage 带出来:分镜规划轮失败过一类极难查的
// "输出解析失败(缺少 title/segments)"(2026-09-24 用户报障,48 集产线卡在第 1 集)。
// 根因往往就是**被 max_tokens 截断**(推理 token 也算在 completion 里),
// 而旧接口只把正文交出去,调用方分不清"截断"和"模型没按格式答",只能给一句
// 无从下手的错误。带上 finish/usage 后:截断能指名道姓,并能据此触发更小的重问。
type Result struct {
	Content string
	Finish  string
	Usage   Usage
	Model   string
}

type chatRespMessage struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
}

// Chat 发起一次对话。jsonMode=true 时请求 response_format=json_object;
// 思考型模型对 JSON mode 兼容不稳,重试时自动降级普通模式(解析层有容错)。
// 深度推理模型单次响应可长达数分钟:仅对可重试错误(网络/429/5xx)重试 1 次,
// 鉴权/参数类(401/403/404/400)立即失败,避免把用户晾在十几分钟里。
func (c *Client) Chat(ctx context.Context, msgs []Message, jsonMode bool) (string, error) {
	res, err := c.ChatEx(ctx, msgs, jsonMode)
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

// ChatEx 同 Chat,但把 finish_reason / token 用量一并交回调用方(见 Result 注释)。
func (c *Client) ChatEx(ctx context.Context, msgs []Message, jsonMode bool) (Result, error) {
	if !c.Configured() {
		return Result{}, fmt.Errorf("LLM 未配置:请在工作台「设置」中填写 BaseURL / API Key / Model")
	}
	var lastErr error
	useJSON := jsonMode
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-time.After(time.Duration(attempt*3) * time.Second):
			}
		}
		req := chatRequest{
			Model:       c.model,
			Messages:    msgs,
			Temperature: 0.7,
			MaxTokens:   65536, // 推理模型的思考 token 也计入;实测 6 镜方案 completion 可达 3.2 万,留足余量防截断
			Stream:      false,
		}
		if useJSON {
			req.ResponseFormat = &struct {
				Type string `json:"type"`
			}{Type: "json_object"}
		}
		body, _ := json.Marshal(req)
		out, err := c.do(ctx, body)
		if err == nil {
			return out, nil
		}
		lastErr = err
		// 用户取消不重试
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		// 鉴权/参数/模型名错误:重试无意义,立即失败
		if isFatalLLMError(err) {
			return Result{}, fmt.Errorf("LLM 调用失败: %w", err)
		}
		useJSON = false // 下一次降级普通模式
	}
	return Result{}, fmt.Errorf("LLM 调用失败(已重试): %w", lastErr)
}

// isFatalLLMError 鉴权/参数/资源不存在类错误。
func isFatalLLMError(err error) bool {
	if se, ok := err.(*statusError); ok {
		return se.code == 400 || se.code == 401 || se.code == 403 || se.code == 404
	}
	return false
}

// statusError HTTP 非 200 错误(携带状态码,供重试策略分类)。
type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.code, e.body) }

func (c *Client) do(ctx context.Context, body []byte) (Result, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, &statusError{code: resp.StatusCode, body: textutil.Truncate(string(raw), 500)}
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return Result{}, fmt.Errorf("响应解析失败: %w", err)
	}
	if cr.Error != nil {
		return Result{}, fmt.Errorf("API 错误: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return Result{}, fmt.Errorf("API 返回空 choices")
	}
	ch := cr.Choices[0]
	content := strings.TrimSpace(ch.Message.Content)
	res := Result{Content: stripReasoning(content), Finish: ch.FinishReason, Model: c.model}
	if cr.Usage != nil {
		res.Usage = *cr.Usage
	}
	if content == "" {
		if ch.FinishReason == "length" {
			return res, fmt.Errorf("输出被 max_tokens 截断(思考模型正文未生成):请在项目参数中减少镜头数,或更换非推理模型")
		}
		return res, fmt.Errorf("API 返回空内容(finish=%s)", ch.FinishReason)
	}
	return res, nil
}

// stripReasoning 剥离部分模型(如 DeepSeek-R1/GLM 思考模式)的 <think> 块。
func stripReasoning(s string) string {
	if i := strings.Index(s, "</think>"); i >= 0 {
		s = s[i+len("</think>"):]
	}
	return strings.TrimSpace(s)
}

// ---------- 视觉多模态 ----------

// VisionContent 一段多模态消息。
type VisionContent struct {
	Role   string   `json:"role"`
	Text   string   `json:"text"`
	Images []string `json:"images"` // data URI(data:image/png;base64,…)
}

// ChatVision 多模态对话(OpenAI 兼容 image_url 协议)。
//
// max_tokens 从 2048 提到 8192(2026-09-27 实测):GLM 一类**思考型**视觉模型的推理 token
// 也计入 completion,2048 会被思考吃干净、正文一个字没出来就报
// 「VLM 返回空内容(finish=length)」—— 而定妆照自检正好是"逐项回答九项 + 中文读数"的长输出。
// 症状是**静默降级**:那次自检被判成"未判定",于是"朝向偏离就换种子重出"根本不触发,
// 而事件流里只有一句无从下手的自检失败。上限给足之后,失败才真的是失败,不是被截断。
func (c *Client) ChatVision(ctx context.Context, system string, turns []VisionContent, jsonMode bool) (string, error) {
	if !c.Configured() {
		return "", fmt.Errorf("视觉模型未配置:请在设置中填写 VLM BaseURL / API Key / Model")
	}
	msgs := []map[string]any{}
	if system != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": system})
	}
	for _, t := range turns {
		content := []map[string]any{}
		if t.Text != "" {
			content = append(content, map[string]any{"type": "text", "text": t.Text})
		}
		for _, img := range t.Images {
			content = append(content, map[string]any{
				"type": "image_url", "image_url": map[string]any{"url": img},
			})
		}
		msgs = append(msgs, map[string]any{"role": t.Role, "content": content})
	}
	body := map[string]any{
		"model":       c.model,
		"messages":    msgs,
		"max_tokens":  8192,
		"temperature": 0.2,
	}
	if jsonMode {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	raw, _ := json.Marshal(body)
	var out []byte
	var resp *http.Response
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		resp, err = c.http.Do(req)
		if err != nil {
			return "", err
		}
		out, _ = io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if resp.StatusCode == http.StatusOK {
			break
		}
		// 免费档限流(429)退避重试
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 2 {
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 6 * time.Second):
			}
			continue
		}
		defer resp.Body.Close()
		return "", fmt.Errorf("VLM HTTP %d: %s", resp.StatusCode, textutil.Truncate(string(out), 300))
	}
	defer resp.Body.Close()
	var cr chatResponse
	if err := json.Unmarshal(out, &cr); err != nil {
		return "", err
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("VLM 返回空 choices")
	}
	content := strings.TrimSpace(cr.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("VLM 返回空内容(finish=%s)", cr.Choices[0].FinishReason)
	}
	return stripReasoning(content), nil
}
