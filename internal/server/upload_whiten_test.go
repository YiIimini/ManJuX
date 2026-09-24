package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 上传白底化的**降级契约**(用户 2026-09-24:上传图也走 Qwen-Image-Edit 生成白底角色)。
//
// 关键纪律:白底化失败(编辑权重没下齐 / ComfyUI 没起 / 没有本地引擎)**绝不能阻断上传**
// —— 用户上传一张图,我们要么给他白底版,要么给他原图 + 一句实话,
// 而不是报错拒收,更不能偷偷用 Krea-2 文生图把人重画一遍(那是"改内容"不是"换背景")。
func TestUploadWhitenDegradesGracefully(t *testing.T) {
	s := newTestServer(t) // 空引擎注册表:模拟"没有本地 ComfyUI"

	dir := t.TempDir()
	img := filepath.Join(dir, "upload.png")
	// 1×1 PNG(内容不重要,被测的是流程与降级行为)
	pngBytes := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	if err := os.WriteFile(img, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"name":        "白底化降级测试",
		"description": "灰布长衫的中年人",
		"image_path":  img,
		// 不传 whiten → 默认白底化
	}
	code, out := doReq(t, s, "POST", "/api/library/characters", body)
	if code != 200 {
		t.Fatalf("白底化失败时上传仍应成功(200),实际 %d %v", code, out)
	}
	note, _ := out["note"].(string)
	if note == "" {
		t.Fatal("没做成白底化就必须如实告知(note 为空等于假装做过了)")
	}
	if !strings.Contains(note, "原图") {
		t.Errorf("note 应说清「按原图入库」,实际 %q", note)
	}
	// 入库条目必须存在,且来源是 upload(不是 upload-whitebg)
	entry, ok := out["entry"].(map[string]any)
	if !ok {
		t.Fatalf("应返回库条目,实际 %v", out)
	}
	if src, _ := entry["source"].(string); src != "upload" {
		t.Errorf("白底化未完成时来源应仍是 upload,实际 %q", src)
	}

	// 显式 whiten=false:不该产生任何 note(用户自己选的原图)
	body2 := map[string]any{
		"name": "白底化跳过测试", "image_path": img, "whiten": false,
	}
	code2, out2 := doReq(t, s, "POST", "/api/library/characters", body2)
	if code2 != 200 {
		t.Fatalf("跳过白底化应成功,实际 %d %v", code2, out2)
	}
	if n2, _ := out2["note"].(string); n2 != "" {
		t.Errorf("显式跳过时不该有 note,实际 %q", n2)
	}
}

// TestWhitenRequiresEditModel 白底化必须走**图像编辑**链路;缺件时要给出可执行的说明。
func TestWhitenRequiresEditModel(t *testing.T) {
	s := newTestServer(t)
	if _, err := s.whitenUploaded(httptest.NewRequest("POST", "/", nil).Context(),
		"甲", "描述", filepath.Join(t.TempDir(), "x.png")); err == nil {
		t.Fatal("没有本地引擎时应返回错误(调用方据此保留原图)")
	} else if !strings.Contains(err.Error(), "ComfyUI") {
		t.Errorf("错误提示应指出需要本地 ComfyUI,实际 %v", err)
	}
}
