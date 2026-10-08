package qc

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/llm"
)

// zz_probe_asset_ab_live_test.go 临时探针：对**旧图 / 新图**用同一套问题问平台自己的 VLM，
// 把"改完到底有没有变好"变成可引用的原话（2026-10-08 用户「角色渲染的很丑，你看一下资产库」）。
//
// 旧图 = 盘上资产库那张（当时那版提示词出的）；
// 新图 = 用**修好的提示词**走生产出图路径重出的（`cmd/imagecheck`，同 ComfyUI 同模型）。
// 诚实交代：旧图的 seed 未知 ⇒ 不是严格同 seed 对照，只比"提示词驱动的那部分观感"。
//
// 跑法：$env:MANJUX_LIVE_PROBE=1; $env:MANJUX_AB_OLD=<旧图>; $env:MANJUX_AB_NEW=<新图>;
//
//	go test ./internal/qc/ -run TestZZProbeAssetAB -v
func TestZZProbeAssetAB(t *testing.T) {
	if os.Getenv("MANJUX_LIVE_PROBE") == "" {
		t.Skip("真机 VLM 探针：设 MANJUX_LIVE_PROBE=1 才跑")
	}
	oldPath := os.Getenv("MANJUX_AB_OLD")
	newPath := os.Getenv("MANJUX_AB_NEW")
	if oldPath == "" || newPath == "" {
		t.Skip("需要 MANJUX_AB_OLD / MANJUX_AB_NEW")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "data", "settings.json"))
	if err != nil {
		t.Skip(err)
	}
	var s config.Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Skip(err)
	}
	client := llm.New(s.VLM.BaseURL, s.VLM.APIKey, s.VLM.Model, 300)
	if !client.Configured() {
		t.Skip("VLM 未配置齐")
	}
	sys := "你是影视美术指导，正在评审 AI 短剧的**角色定妆照**（它们会作为视频生成的参考图）。\n" +
		"只说你**在图上看到的**，不要猜、不要客套，也不要因为看到两张图就做对比评价。逐条答：\n" +
		"1) 构图：头占画面高度约几分之一？身体被裁到哪里？有没有整身（含靴子/脚）入镜？\n" +
		"2) 比例：这个人看起来几头身？像成年人还是像童装立绘？\n" +
		"3) 皮肤与材质：蜡感/塑料感还是真实材质？有毛孔或织物纹理吗？\n" +
		"4) 光：平光还是有塑形光（鼻影、颧骨结构、下颌阴影）？\n" +
		"5) 最丑的三点（按严重度排序）。\n" +
		"每行不超过 60 字，中文。"
	for _, item := range []struct{ label, path string }{{"旧图（旧提示词出的）", oldPath}, {"新图（修好的提示词出的）", newPath}} {
		b, err := os.ReadFile(item.path)
		if err != nil {
			t.Errorf("%s 读不到: %v", item.label, err)
			continue
		}
		uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
		out, err := client.ChatVision(t.Context(), sys, []llm.VisionContent{
			{Role: "user", Text: "这张图是角色定妆照，请按 1)-5) 评审。", Images: []string{uri}},
		}, false)
		if err != nil {
			t.Errorf("%s VLM 失败: %v", item.label, err)
			continue
		}
		t.Logf("=== %s ===\n%s", item.label, strings.TrimSpace(out))
	}
}
