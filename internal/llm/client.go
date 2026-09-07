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

// New 创建客户端。
func New(baseURL, apiKey, model string, timeoutSec int) *Client {
	if timeoutSec <= 0 {
		timeoutSec = 300
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
	Model           string    `json:"model"`
	Messages        []Message `json:"messages"`
	Temperature     float64   `json:"temperature"`
	MaxTokens       int       `json:"max_tokens,omitempty"`
	ResponseFormat  *struct {
		Type string `json:"type"`
	} `json:"response_format,omitempty"`
	Stream bool `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat 发起一次对话。jsonMode=true 时请求 response_format=json_object。
func (c *Client) Chat(ctx context.Context, msgs []Message, jsonMode bool) (string, error) {
	if !c.Configured() {
		return "", fmt.Errorf("LLM 未配置:请在工作台「设置」中填写 BaseURL / API Key / Model")
	}
	req := chatRequest{
		Model:       c.model,
		Messages:    msgs,
		Temperature: 0.7,
		MaxTokens:   16384,
		Stream:      false,
	}
	if jsonMode {
		req.ResponseFormat = &struct {
			Type string `json:"type"`
		}{Type: "json_object"}
	}
	body, _ := json.Marshal(req)

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt*3) * time.Second):
			}
		}
		out, err := c.do(ctx, body)
		if err == nil {
			return out, nil
		}
		lastErr = err
		// 用户取消不重试
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	return "", fmt.Errorf("LLM 调用失败(已重试 3 次): %w", lastErr)
}

func (c *Client) do(ctx context.Context, body []byte) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 500))
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", fmt.Errorf("响应解析失败: %w", err)
	}
	if cr.Error != nil {
		return "", fmt.Errorf("API 错误: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("API 返回空 choices")
	}
	content := strings.TrimSpace(cr.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("API 返回空内容")
	}
	return stripReasoning(content), nil
}

// stripReasoning 剥离部分模型(如 DeepSeek-R1/GLM 思考模式)的 <think> 块。
func stripReasoning(s string) string {
	if i := strings.Index(s, "</think>"); i >= 0 {
		s = s[i+len("</think>"):]
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
