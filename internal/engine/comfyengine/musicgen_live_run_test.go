package comfyengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// 配乐生成的**端到端真跑**（默认跳过，设 MANJUX_MUSIC_LIVE=1 才跑）。
//
// 为什么要它单独存在：契约测试与实机节点核对都只证明"图对"，证明不了"能出曲子" ——
// 13.68 GB 的模型、两个文本编码器、采样与解码任何一环有问题都只有真跑才知道。
// 生成出来还要**入资产库并验证复用命中**，那才是用户要的那条链。
//
// 开销：首次要加载 4B 文本编码器（比出图慢得多），30 秒的曲子约几分钟。
func TestMusicGenerationLive(t *testing.T) {
	if os.Getenv("MANJUX_MUSIC_LIVE") != "1" {
		t.Skip("未设 MANJUX_MUSIC_LIVE=1，跳过配乐真跑（它很慢，不该在每次自检里跑）")
	}
	// 与 upscale_live_test.go 同一套造法：给一个能返回配置的闭包即可
	cfg := config.Default().Comfy
	eng := New(func() config.ComfySettings { return cfg })
	if err := eng.Available(context.Background()); err != nil {
		t.Skipf("本机 ComfyUI 不可用，跳过:%v", err)
	}
	dir := t.TempDir()
	// 30 秒足够验证链路（成片铺底会自动循环，真用时由 pipeline 按片长算）
	job := engine.MusicJob{
		Tags:       "calm guzheng and low strings, slow, instrumental, background score",
		Seconds:    30,
		BPM:        70,
		OutputDir:  dir,
		OutputName: "bgm_live_test",
		Timeout:    25 * time.Minute,
	}
	start := time.Now()
	path, err := eng.GenerateMusic(context.Background(), job)
	if err != nil {
		t.Fatalf("真跑配乐失败:%v", err)
	}
	elapsed := time.Since(start)
	st, err := os.Stat(path)
	if err != nil || st.Size() == 0 {
		t.Fatalf("配乐落盘失败或为空:%v", err)
	}
	// 必须是音频扩展名、且大小合理（几十秒的 mp3 至少几百 KB，太小说明是空文件）
	ext := filepath.Ext(path)
	if ext != ".mp3" && ext != ".flac" && ext != ".wav" && ext != ".ogg" {
		t.Errorf("产物应是音频，实际 %s", ext)
	}
	if st.Size() < 100*1024 {
		t.Errorf("产物太小（%d 字节），可能是空音频", st.Size())
	}
	t.Logf("真跑成功：%s（%.1f MB，用时 %.0f 秒）", filepath.Base(path), float64(st.Size())/1024/1024, elapsed.Seconds())
}
