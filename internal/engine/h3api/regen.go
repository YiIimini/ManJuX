package h3api

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/ff"
)

// Regenerate2K 云端"视频再生成":以本地 768P 产物为 base_video + 原始提示词/参考,
// In-context 重生成 2K(细节重新条件化,非简单放大)。
// 官方约束:源视频须符合 H3 768P 规格且 ≤50MB;Model 须为 MiniMax-H3。
func (e *Engine) Regenerate2K(ctx context.Context, baseVideo string, prompt string, refs []engine.RefImage, outPath string, onProgress func(pct int, note string)) (string, error) {
	c := e.cfg()
	if err := e.Available(ctx); err != nil {
		return "", err
	}
	if c.Model == "MiniMax-H3-Max" {
		return "", fmt.Errorf("2K 再生成仅支持 MiniMax-H3 模型(当前 %s 不含 2K),请在设置中切换模型", c.Model)
	}
	if st, err := os.Stat(baseVideo); err != nil {
		return "", fmt.Errorf("源视频不存在: %s", baseVideo)
	} else if st.Size() > 50<<20 {
		return "", fmt.Errorf("源视频 %.1fMB 超过云端 50MB 上限", float64(st.Size())/(1<<20))
	}

	content := []map[string]any{
		{"type": "text", "text": prompt},
	}
	for _, ref := range refs {
		dataURI, err := fileToDataURI(ref.Path)
		if err != nil {
			return "", fmt.Errorf("参考图读取失败: %w", err)
		}
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": dataURI},
			"role":      "reference_image",
		})
	}
	videoURI, err := fileToVideoDataURI(baseVideo)
	if err != nil {
		return "", fmt.Errorf("源视频读取失败: %w", err)
	}
	content = append(content, map[string]any{
		"type":      "video_url",
		"video_url": map[string]any{"url": videoURI},
		"role":      "base_video",
	})

	body := map[string]any{
		"model":      "MiniMax-H3",
		"task_type":  "regeneration",
		"content":    content,
		"resolution": "2K",
	}
	taskID, err := e.create(ctx, c, body)
	if err != nil {
		return "", err
	}
	if onProgress != nil {
		onProgress(5, "2K 再生任务已提交 "+taskID)
	}
	// 与 Render 同一条纪律:提交之后任何非成功路径都要取消远端任务,避免空跑计费。
	settled := false
	defer func() {
		if !settled {
			e.cancel(c, taskID)
		}
	}()

	url, err := e.awaitTask(ctx, c, taskID, 30*time.Minute, onProgress)
	if err != nil {
		return "", err
	}
	if onProgress != nil {
		onProgress(90, "下载 2K 成片")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	if err := download(ctx, url, outPath); err != nil {
		return "", err
	}
	settled = true
	return taskID, nil
}

// Validate2KBase 预校验源视频是否符合 regeneration 规格(fail-loud)。
func Validate2KBase(f *ff.FF, path string, wantPortrait bool) error {
	info, err := f.Probe(path)
	if err != nil {
		return fmt.Errorf("无法探测源视频: %w", err)
	}
	short, long := info.Width, info.Height
	if short > long {
		short, long = long, short
	}
	if short != 768 {
		return fmt.Errorf("短边须为 768(当前 %d×%d,云端再生成仅接受 H3 768P 规格)", info.Width, info.Height)
	}
	if long > 1344 {
		return fmt.Errorf("长边超限 %d(上限 1344)", long)
	}
	if !info.HasAudio {
		return fmt.Errorf("源视频无音轨(H3 产物应自带音轨,再生成需保留)")
	}
	return nil
}

func fileToVideoDataURI(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(raw), nil
}
