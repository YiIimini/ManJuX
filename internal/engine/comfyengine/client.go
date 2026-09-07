// Package comfyengine 实现本地 ComfyUI 渲染引擎。
// 节点结构与参数对齐 ComfyUI 内置 MiniMax-H3 节点
// (comfy_extras/nodes_minimax_h3.py)与 ComfyUI-H3-ConditioningCache 插件。
package comfyengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client ComfyUI HTTP API 客户端。
type Client struct {
	BaseURL  string
	ClientID string
	http     *http.Client
}

// NewClient 创建客户端。
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		ClientID: "manjux-" + fmt.Sprintf("%d", time.Now().UnixNano()%1e6),
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// Alive 探测服务是否存活。
func (c *Client) Alive(ctx context.Context) bool {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/system_stats", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// Version 返回 ComfyUI 版本摘要(探测页)。
func (c *Client) Version(ctx context.Context) (string, error) {
	raw, err := c.get(ctx, "/system_stats")
	if err != nil {
		return "", err
	}
	var out struct {
		System struct {
			ComfyUIVersion string `json:"comfyui_version"`
			PythonVersion  string `json:"python_version"`
			OS             string `json:"os"`
		} `json:"system"`
		Devices []struct {
			Name  string `json:"name"`
			VRAMTotal float64 `json:"vram_total"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	ver := "ComfyUI " + out.System.ComfyUIVersion
	if len(out.Devices) > 0 {
		ver += " | GPU " + out.Devices[0].Name
	}
	return ver, nil
}

// HasNode 检查节点类是否存在(object_info)。
func (c *Client) HasNode(ctx context.Context, classType string) bool {
	raw, err := c.get(ctx, "/object_info/"+url.PathEscape(classType))
	if err != nil {
		return false
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return false
	}
	_, ok := out[classType]
	return ok
}

// UploadImage 上传图片到 ComfyUI input,返回服务器端文件名。
func (c *Client) UploadImage(ctx context.Context, localPath string) (name string, err error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("image", filepath.Base(localPath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	w.WriteField("overwrite", "true")
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/upload/image", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("上传失败 HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Name     string `json:"name"`
		Subfolder string `json:"subfolder"`
		Type     string `json:"type"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.Name, nil
}

// Submit 提交工作流,返回 prompt_id。
func (c *Client) Submit(ctx context.Context, graph map[string]any) (string, error) {
	payload := map[string]any{"prompt": graph, "client_id": c.ClientID}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/prompt", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("提交失败 HTTP %d: %s", resp.StatusCode, extractNodeErrors(string(raw)))
	}
	var out struct {
		PromptID string `json:"prompt_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.PromptID == "" {
		return "", fmt.Errorf("提交响应缺少 prompt_id")
	}
	return out.PromptID, nil
}

// extractNodeErrors 从 /prompt 422 响应中提取节点校验错误。
func extractNodeErrors(body string) string {
	var out struct {
		Error  map[string]any `json:"error"`
		NodeErrors map[string]struct {
			Errors []struct {
				Message string `json:"message"`
				Details string `json:"details"`
			} `json:"errors"`
		} `json:"node_errors"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return truncate(body, 400)
	}
	var msgs []string
	if e, ok := out.Error["message"]; ok {
		msgs = append(msgs, fmt.Sprint(e))
	}
	for id, ne := range out.NodeErrors {
		for _, e := range ne.Errors {
			msgs = append(msgs, fmt.Sprintf("节点%s: %s", id, e.Message))
		}
	}
	if len(msgs) == 0 {
		return truncate(body, 400)
	}
	return strings.Join(msgs, "; ")
}

// HistoryEntry 一次执行的产物与状态。
type HistoryEntry struct {
	Status struct {
		Completed  bool   `json:"completed"`
		StatusStr  string `json:"status_str"`
		Messages   []json.RawMessage `json:"messages"`
	} `json:"status"`
	Outputs map[string]map[string]json.RawMessage `json:"outputs"`
}

// History 查询执行历史。
func (c *Client) History(ctx context.Context, promptID string) (*HistoryEntry, error) {
	raw, err := c.get(ctx, "/history/"+url.PathEscape(promptID))
	if err != nil {
		return nil, err
	}
	var out map[string]HistoryEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	e, ok := out[promptID]
	if !ok {
		return nil, nil // 尚在队列/执行中
	}
	return &e, nil
}

// OutputFile 从 history 输出中提取产物文件描述。
type OutputFile struct {
	Filename  string
	Subfolder string
	Type      string
	Animated  bool
}

// ExtractFiles 遍历 outputs 收集全部文件对象(SaveVideo/SaveImage 等)。
func (h *HistoryEntry) ExtractFiles() []OutputFile {
	var files []OutputFile
	for _, nodeOut := range h.Outputs {
		for key, arrRaw := range nodeOut {
			if key == "ui" {
				continue
			}
			var arr []struct {
				Filename  string `json:"filename"`
				Subfolder string `json:"subfolder"`
				Type      string `json:"type"`
				Format    string `json:"format"`
				Animated  bool   `json:"animated"`
			}
			if err := json.Unmarshal(arrRaw, &arr); err != nil {
				continue
			}
			for _, it := range arr {
				if it.Filename == "" {
					continue
				}
				animated := it.Animated || strings.Contains(it.Format, "video")
				files = append(files, OutputFile{it.Filename, it.Subfolder, it.Type, animated})
			}
		}
	}
	return files
}

// Download 下载 output/view 文件到本地。
func (c *Client) Download(ctx context.Context, f OutputFile, destPath string) error {
	q := url.Values{}
	q.Set("filename", f.Filename)
	if f.Subfolder != "" {
		q.Set("subfolder", f.Subfolder)
	}
	if f.Type != "" {
		q.Set("type", f.Type)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/view?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败 HTTP %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	tmp := destPath + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, destPath)
}

// Free 释放显存。
func (c *Client) Free(ctx context.Context) {
	body := bytes.NewReader([]byte(`{"unload_models":true,"free_memory":true}`))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/free", body)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := c.http.Do(req); err == nil {
		resp.Body.Close()
	}
}

// Interrupt 中断当前执行。
func (c *Client) Interrupt(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/interrupt", nil)
	if err != nil {
		return
	}
	if resp, err := c.http.Do(req); err == nil {
		resp.Body.Close()
	}
}

// WaitForDone 轮询直到完成/出错。onProgress 收到 0-100 进度。
func (c *Client) WaitForDone(ctx context.Context, promptID string, onProgress func(pct int, note string)) (*HistoryEntry, error) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	deadline := time.Now().Add(45 * time.Minute) // 单镜兜底上限
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("渲染超时(45 分钟)")
			}
			h, err := c.History(ctx, promptID)
			if err != nil {
				continue // 网络抖动容忍
			}
			if h == nil {
				if onProgress != nil {
					onProgress(2, "排队中")
				}
				continue
			}
			if h.Status.Completed || h.Status.StatusStr == "success" {
				return h, nil
			}
			if h.Status.StatusStr == "error" {
				return h, fmt.Errorf("执行出错: %s", execError(h))
			}
			if onProgress != nil {
				pct := progressFromMessages(h)
				onProgress(pct, fmt.Sprintf("渲染中 %d%%", pct))
			}
		}
	}
}

// progressFromMessages 从 status.messages 提取采样进度。
func progressFromMessages(h *HistoryEntry) int {
	for _, raw := range h.Status.Messages {
		var m []json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil || len(m) < 2 {
			continue
		}
		var ev string
		json.Unmarshal(m[0], &ev)
		if ev != "progress" {
			continue
		}
		var data struct {
			Value int `json:"value"`
			Max   int `json:"max"`
		}
		if err := json.Unmarshal(m[1], &data); err == nil && data.Max > 0 {
			return data.Value * 100 / data.Max
		}
	}
	return 5
}

// execError 从错误消息中提取人话。
func execError(h *HistoryEntry) string {
	for _, raw := range h.Status.Messages {
		var m []json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil || len(m) < 2 {
			continue
		}
		var ev string
		json.Unmarshal(m[0], &ev)
		if ev != "execution_error" && ev != "execution_interrupted" {
			continue
		}
		var data struct {
			NodeMessage string `json:"node_message"`
			ExceptionMessage string `json:"exception_message"`
			NodeType    string `json:"node_type"`
		}
		if err := json.Unmarshal(m[1], &data); err == nil {
			msg := data.ExceptionMessage
			if msg == "" {
				msg = data.NodeMessage
			}
			if data.NodeType != "" {
				msg = "[" + data.NodeType + "] " + msg
			}
			if msg != "" {
				return truncate(msg, 500)
			}
		}
	}
	return "未知执行错误(详见 ComfyUI 控制台)"
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
