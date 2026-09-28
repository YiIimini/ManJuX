package qc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 人脸一致性测量（2026-09-28）。第一版**只测量、不拦人** ——
// 阈值 0.85 来自开源参考实现，但在本平台画风上还没有跨镜标定数据（详见 face.go 的说明）。
//
// 这组测试要钉住三件事：
//  1. 环境不齐时必须报"没验过"，**绝不能**沉默或当成通过（本项目反复修的那类"报成功其实没做"）；
//  2. 结果解析要稳（脚本会先打告警，真正结果在最后一行 JSON）；
//  3. 有模型时**真跑一次**（没模型则跳过，不假装验过）。
func TestFaceConfigNotReadySaysWhy(t *testing.T) {
	// 空的配置：三件事一件都不齐
	rep := MeasureFaceConsistency(context.Background(), FaceConfig{}, "ref.png", []string{"a.png"})
	if rep.OK || !rep.Skipped {
		t.Fatalf("环境不齐时必须标为跳过，实际 %+v", rep)
	}
	if strings.TrimSpace(rep.Note) == "" {
		t.Error("跳过必须给出原因（否则界面上只是一片安静）")
	}
	sum := FaceSummary(rep, "林昭")
	if !strings.Contains(sum, "未验") || !strings.Contains(sum, "不当作通过") {
		t.Errorf("摘要里必须写清「未验、不当作通过」，实际 %q", sum)
	}
	// 没有帧
	if r2 := MeasureFaceConsistency(context.Background(), FaceConfig{}, "ref.png", nil); !r2.Skipped {
		t.Error("没有帧时也应标为跳过")
	}
}

func TestFaceConfigFromComfyRoot(t *testing.T) {
	dir := t.TempDir()
	// 造出 comfy.root_dir 的标准结构：standalone-env/python.exe 与 ComfyUI/models/insightface/models/buffalo_l
	if err := os.MkdirAll(filepath.Join(dir, "standalone-env"), 0o755); err != nil {
		t.Fatal(err)
	}
	py := filepath.Join(dir, "standalone-env", "python.exe")
	if err := os.WriteFile(py, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "ComfyUI", "models", "insightface", "models", "buffalo_l"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := FaceConfigFromComfyRoot(dir)
	if c.Python != py {
		t.Errorf("python 应从环境根推导，实际 %q", c.Python)
	}
	if !strings.HasSuffix(filepath.ToSlash(c.ModelRoot), "ComfyUI/models/insightface") {
		t.Errorf("模型根应从环境根推导，实际 %q", c.ModelRoot)
	}
	if ok, why := c.Ready(); !ok {
		t.Errorf("标准结构应判定为就绪，实际 %q", why)
	}
	// 缺 buffalo_l 时要说不齐
	empty := t.TempDir()
	c2 := FaceConfigFromComfyRoot(empty)
	if ok, _ := c2.Ready(); ok {
		t.Error("空目录不该判定为就绪")
	}
}

func TestLastJSONLine(t *testing.T) {
	in := "FutureWarning: xxx\n  tform.estimate(...)\n{\"ok\": true, \"min\": 0.9}\n"
	if got := lastJSONLine(in); got != `{"ok": true, "min": 0.9}` {
		t.Errorf("应取最后一行 JSON，实际 %q", got)
	}
	if got := lastJSONLine("no json here"); got != "" {
		t.Errorf("没有 JSON 时应返回空串，实际 %q", got)
	}
}

func TestFaceSummaryIsHonestWhenLow(t *testing.T) {
	lo := 0.51
	rep := &FaceReport{
		OK: true, Measured: 3, Below: 1, Min: lo, Mean: 0.88, Max: 0.97, Thresh: 0.85,
		Items: []FaceItem{{Frame: "1", Faces: 1, Sim: &lo}, {Frame: "2", Faces: 1}, {Frame: "3", Faces: 0}},
	}
	s := FaceSummary(rep, "林昭")
	for _, want := range []string{"测到 3 帧", "最低 0.510", "低于 0.85 的 1 帧", "尚未在本画风上跨镜标定", "先记录不拦人"} {
		if !strings.Contains(s, want) {
			t.Errorf("摘要里要有 %q，实际 %q", want, s)
		}
	}
}

// TestFaceMeasureRealModel 真跑一次（本机装了 buffalo_l 才跑；没装就跳过 —— 不假装验过）。
func TestFaceMeasureRealModel(t *testing.T) {
	cfg := FaceConfigFromComfyRoot(`D:\Ai\ComfyUI`)
	if ok, why := cfg.Ready(); !ok {
		t.Skipf("本机没有可用的 buffalo_l，跳过真实测量:%s", why)
	}
	// 找一张真实的项目图当参考与帧（同一张 ⇒ 相似度应接近 1）
	var img string
	filepath.Walk(`D:\Ai\Datas\ManJuX\data`, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || img != "" {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(p), "cover.png") {
			img = p
		}
		return nil
	})
	if img == "" {
		t.Skip("没有可用的真实图片")
	}
	rep := MeasureFaceConsistency(context.Background(), cfg, img, []string{img})
	if !rep.OK {
		t.Fatalf("真实测量应成功，实际 note=%q raw=%q", rep.Note, rep.Raw)
	}
	if rep.Measured != 1 || rep.Min < 0.99 {
		t.Errorf("同一张图自比应接近 1，实际 measured=%d min=%.4f", rep.Measured, rep.Min)
	}
	t.Logf("真实测量: %s", FaceSummary(rep, filepath.Base(img)))
}
