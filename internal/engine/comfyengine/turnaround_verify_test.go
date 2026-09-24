package comfyengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// TestTurnaroundRender 真机验证:用 H3 Ref2V 生成角色转身视频 —— 这是多位图
// 方案的核心链路(以定妆照为参考生成转身视频,再抽帧拼成转台图)。
//
// 默认跳过:它会真实占用 GPU 数分钟,不适合当常规回归。启用方式:
//
//	MANJUX_VERIFY_TURNAROUND=1 go test ./internal/engine/comfyengine/ -run Turnaround -v -timeout 30m
func TestTurnaroundRender(t *testing.T) {
	if os.Getenv("MANJUX_VERIFY_TURNAROUND") != "1" {
		t.Skip("需要真实 GPU 渲染;设 MANJUX_VERIFY_TURNAROUND=1 启用")
	}
	const root = "D:/Ai/ManJuX"
	raw, err := os.ReadFile(filepath.Join(root, "data", "settings.json"))
	if err != nil {
		t.Fatalf("读取真实设置失败(本测试需要它来拿模型名): %v", err)
	}
	var s struct {
		Comfy config.ComfySettings `json:"comfy"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("解析设置失败: %v", err)
	}
	e := New(func() config.ComfySettings { return s.Comfy })

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	outDir := filepath.Join(root, "data", "_verify_turnaround")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	master := filepath.Join(root, "comfyui", "ComfyUI", "input", "img2img_test.png")
	if _, err := os.Stat(master); err != nil {
		t.Fatalf("缺少参考图 %s: %v", master, err)
	}

	_, err = e.Render(ctx, engine.Job{
		ProjectID: "verify", ShotID: 0,
		Prompt:    verifyTurnaroundPrompt(),
		Mode:      "ref2va",
		RefImages: []engine.RefImage{{Kind: "character", Path: master}},
		Width:     832, Height: 1248,
		Length: 124,
		Seed:   12345, Steps: 8, Accel: "turbo",
		OutputDir: outDir, OutputName: "turnaround",
		OnProgress: func(p int, note string) { t.Logf("  [%d%%] %s", p, note) },
	})
	if err != nil {
		t.Fatalf("转台视频渲染失败: %v", err)
	}

	// 产物应是一个非空视频
	matches, _ := filepath.Glob(filepath.Join(outDir, "turnaround*"))
	if len(matches) == 0 {
		t.Fatal("渲染返回成功但没有产物文件")
	}
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.Size() > 0 {
			t.Logf("✓ 转台视频已生成: %s (%.1f MB)", filepath.Base(m), float64(st.Size())/1024/1024)
			return
		}
	}
	t.Fatal("产物存在但为空")
}

// verifyTurnaroundPrompt 与 pipeline.turnaroundPrompt 同构(此处内联以免测试
// 依赖 pipeline 包造成循环)。
func verifyTurnaroundPrompt() string {
	return strings.Join([]string{
		"subject_definitions:",
		"<Subject 1> is a character in <Picture 1>.",
		"",
		"summary:",
		"[reference generation]",
		"A character turnaround reference: the character stands still in the centre of frame and slowly rotates in place, showing the front view first, then turning through three-quarter and full profile to the back view. Plain seamless background, even studio lighting, the character never leaves the frame and never changes appearance.",
		"",
		"retention_analysis:",
		"- <Picture 1> : fully_preserved - character identity (face shape, hairstyle, hair colour, costume, accessories, body proportions) must stay identical to the reference image in every frame. FACE LOCK: copy the facial features, bone structure and skin details from <Picture 1> exactly; do NOT blend, average, or substitute any other face.",
		"",
		"detailed_description:",
		"Character turnaround on a plain seamless background, soft even studio lighting, no props, no text.",
		"[Shot 1] The character stands centred, facing the camera directly, arms relaxed at the sides, neutral expression; the camera is static and the framing is an upper-body medium shot.",
		"[Shot 2] At 00:01.300 The character has rotated about forty-five degrees, now seen from a three-quarter angle; facial features, hair and costume remain unchanged.",
		"[Shot 3] At 00:02.600 The character has rotated about ninety degrees, now in full profile; the silhouette of the hairstyle and costume stays identical.",
		"[Shot 4] At 00:03.900 The character has rotated about one hundred and eighty degrees, now seen from behind; the back of the hairstyle and the costume are visible, still identical.",
		"The whole clip is one continuous slow rotation with no cuts, no camera movement and no background change. No on-screen text, no subtitles, no watermarks, no logos. Subjects must remain clearly visible and adequately lit throughout the video; avoid rendering any frame nearly black or overly dark.",
		"",
		"overall_soundscape:",
		"Quiet room tone, no speech, no music, no sound effects.",
		"",
		"non_diegetic_music:",
		"N/A",
	}, "\n")
}
