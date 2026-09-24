// Package h3api 实现 MiniMax H3 云端 API 引擎。
// 协议(官方文档):POST /v2/video_generation 创建任务(异步),
// GET /v2/query/video_generation/{task_id} 轮询(建议 10s),
// 成功后 task.content.url 下载成片。
package h3api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/textutil"
)

// defaultAPITimeout 云端任务轮询兜底上限(作业未指定 Timeout 时使用)。
const defaultAPITimeout = 20 * time.Minute

// Engine 云端 H3 引擎。
type Engine struct {
	cfg func() config.MiniMaxSettings
}

// New 创建引擎。
func New(cfg func() config.MiniMaxSettings) *Engine { return &Engine{cfg: cfg} }

// Name 引擎名。
func (e *Engine) Name() string { return "api" }

// Available 检查 API Key 已配置(轻量,不真调 API)。
func (e *Engine) Available(ctx context.Context) error {
	c := e.cfg()
	if c.APIKey == "" {
		return fmt.Errorf("MiniMax API Key 未配置:请在工作台「设置」中填写")
	}
	return nil
}

// Render 提交任务 → 轮询 → 下载。
// ── interface.Engine 的通用可选能力:云端引擎没有本地显存/队列概念 ──

// FreeVRAM 云端无本地显存可释放。
func (e *Engine) FreeVRAM(ctx context.Context) error { return nil }

// ClearQueue 云端任务由服务端排队,本地无队列可清。
func (e *Engine) ClearQueue(ctx context.Context) {}

// Interrupt 云端任务的取消在 Render 内部尽力完成,此处无需额外动作。
func (e *Engine) Interrupt(ctx context.Context) {}

// GenerateImage 云端引擎不提供本地图像生成:定妆照走本地 Krea-2。
func (e *Engine) GenerateImage(ctx context.Context, prompt, neg string, seed int64, w, h int, outPath string) (string, error) {
	return "", fmt.Errorf("云端引擎不支持本地图像生成,请改用本地 ComfyUI 引擎")
}

// Upscale 2× 放大是本地后处理链路(依赖 ComfyUI 的放大节点与权重),
// 云端引擎不实现 —— 与 GenerateImage 同理,给出可执行的指引而不是静默失败。
func (e *Engine) Upscale(ctx context.Context, srcVideoPath string, opt engine.UpscaleOptions, length int, fps float64, outName, outPath string, timeout time.Duration) (string, error) {
	return "", fmt.Errorf("2× 放大是本地后处理,请改用本地 ComfyUI 引擎")
}

func (e *Engine) Render(ctx context.Context, job engine.Job) (*engine.Result, error) {
	c := e.cfg()
	if err := e.Available(ctx); err != nil {
		return nil, err
	}
	_ = os.MkdirAll(job.OutputDir, 0o755)
	// 注:产物复用/断点续跑由管线层按输入指纹(manifest)权威判定,
	// 引擎层不做本地缓存,避免绕过指纹校验复用旧文件
	final := filepath.Join(job.OutputDir, job.OutputName+".mp4")

	content := []map[string]any{
		{"type": "text", "text": job.Prompt},
	}
	for _, ref := range job.RefImages {
		dataURI, err := fileToDataURI(ref.Path)
		if err != nil {
			return nil, fmt.Errorf("参考图读取失败(%s): %w", filepath.Base(ref.Path), err)
		}
		role := "reference_image"
		if ref.Kind == "first_frame" {
			role = "first_frame"
		}
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": dataURI},
			"role":      role,
		})
	}
	body := map[string]any{
		"model":      c.Model,
		"content":    content,
		"duration":   clampDuration(job.Length / 24),
		"resolution": orDefault(c.Resolution, "768P"),
		// ratio 始终显式传:ref2va 下云端不按参考图自动取竖横,缺省会落 16:9
		"ratio": ratioFor(job.Width, job.Height),
	}

	taskID, err := e.create(ctx, c, body)
	if err != nil {
		return nil, err
	}
	if job.OnProgress != nil {
		job.OnProgress(3, "云端任务已提交 "+taskID)
	}

	// 轮询(官方建议 10s 间隔;上限由作业超时决定,未指定时 defaultAPITimeout)
	timeout := job.Timeout
	if timeout <= 0 {
		timeout = defaultAPITimeout
	}
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			// 尽力取消云端任务,避免空跑计费
			go e.cancel(c, taskID)
			return nil, ctx.Err()
		case <-tick.C:
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w(云端任务 %s 超过 %v)", engine.ErrRenderTimeout, taskID, timeout)
		}
		st, url, err := e.query(ctx, c, taskID)
		if err != nil {
			continue // 网络抖动容忍
		}
		switch st {
		case "succeed", "succeeded", "success":
			if url == "" {
				return nil, fmt.Errorf("任务成功但缺少成片 url: %s", taskID)
			}
			if job.OnProgress != nil {
				job.OnProgress(90, "下载成片")
			}
			var dlErr error
			for attempt := 0; attempt < 2; attempt++ {
				if dlErr = download(ctx, url, final); dlErr == nil {
					break
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(3 * time.Second):
				}
			}
			if dlErr != nil {
				return nil, fmt.Errorf("下载成片失败(已重试): %w", dlErr)
			}
			return &engine.Result{VideoPath: final, TaskID: taskID, Engine: e.Name()}, nil
		case "failed", "cancelled":
			return nil, fmt.Errorf("云端任务失败(%s): %s", st, taskID)
		default: // queued / running / processing
			if job.OnProgress != nil {
				job.OnProgress(40, "云端生成中("+st+")")
			}
		}
	}
}

func (e *Engine) create(ctx context.Context, c config.MiniMaxSettings, body map[string]any) (string, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.BaseURL, "/")+"/v2/video_generation", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("提交云端任务失败: %w", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("提交失败 HTTP %d: %s", resp.StatusCode, textutil.Truncate(string(out), 400))
	}
	var r struct {
		TaskID   string `json:"task_id"`
		BaseResp *struct {
			StatusMsg string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", err
	}
	if r.TaskID == "" {
		msg := ""
		if r.BaseResp != nil {
			msg = r.BaseResp.StatusMsg
		}
		return "", fmt.Errorf("响应缺少 task_id: %s %s", textutil.Truncate(string(out), 200), msg)
	}
	return r.TaskID, nil
}

func (e *Engine) query(ctx context.Context, c config.MiniMaxSettings, taskID string) (status, url string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(c.BaseURL, "/")+"/v2/query/video_generation/"+taskID, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("查询失败 HTTP %d", resp.StatusCode)
	}
	var r struct {
		Task struct {
			Status  string `json:"status"`
			Content struct {
				URL string `json:"url"`
			} `json:"content"`
		} `json:"task"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", "", err
	}
	return strings.ToLower(r.Task.Status), r.Task.Content.URL, nil
}

func (e *Engine) cancel(c config.MiniMaxSettings, taskID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		strings.TrimRight(c.BaseURL, "/")+"/v2/video_generation/"+taskID, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	if resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req); err == nil {
		resp.Body.Close()
	}
}

func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载成片失败 HTTP %d", resp.StatusCode)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

func fileToDataURI(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(raw) > 30<<20 {
		return "", fmt.Errorf("图片超过 30MB 上限")
	}
	mime := "image/png"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".webp":
		mime = "image/webp"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func clampDuration(sec int) int {
	if sec < 5 {
		return 5
	}
	if sec > 15 {
		return 15
	}
	return sec
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// ratioFor 由宽高推导 ratio(t2va 必填,不能为 adaptive)。
func ratioFor(w, h int) string {
	if w <= 0 || h <= 0 {
		return "9:16"
	}
	r := float64(w) / float64(h)
	switch {
	case r > 1.6:
		return "16:9"
	case r > 1.1:
		return "4:3"
	case r > 0.9:
		return "1:1"
	case r > 0.62:
		return "3:4"
	default:
		return "9:16"
	}
}
