package qc

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/llm"
)

// zz_probe_assets_live_test.go 临时探针：让**平台自己的 VLM** 逐张读资产库里的角色定妆照，
// 把"丑在哪"变成可引用的原话（不许凭记忆或推断说"丑"）。
//
// 默认跳过（要打真接口）：MANJUX_LIVE_PROBE=1 才跑。
func TestZZProbeAssetLook(t *testing.T) {
	if os.Getenv("MANJUX_LIVE_PROBE") == "" {
		t.Skip("真机 VLM 探针：设 MANJUX_LIVE_PROBE=1 才跑")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "data", "settings.json"))
	if err != nil {
		t.Skip(err)
	}
	var s config.Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Skip(err)
	}
	if strings.TrimSpace(s.VLM.APIKey) == "" {
		t.Skip("VLM 凭据为空")
	}
	client := llm.New(s.VLM.BaseURL, s.VLM.APIKey, s.VLM.Model, 300)
	if !client.Configured() {
		t.Skip("VLM 未配置齐")
	}

	root := filepath.Join("..", "..", "data", "assets_library", "characters")
	var files []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(strings.ToLower(p), ".png") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	if len(files) == 0 {
		t.Skip("资产库里没有角色图")
	}

	sys := "你是影视美术指导，正在评审 AI 短剧的**角色定妆照**（它们会作为视频生成的参考图）。\n" +
		"只说你**在图上看到的**，不要猜、不要客套。逐条答：\n" +
		"1) 构图：头占画面高度约几分之一？身体被裁到哪里？画面是竖幅还是横幅？\n" +
		"2) 五官清晰度：这个分辨率下，眼型/眉形/唇形能不能分辨？脸约占多少像素宽？\n" +
		"3) 观感属于哪一类：写实照片 / 半写实 / 3D 塑料娃娃（BJD）/ 卡通？皮肤是蜡感还是有材质？\n" +
		"4) 明显缺陷（有就说，没有就说没有）：多余或畸形的手/指、四肢错位、背景残影或色块、\n" +
		"   画面里出现文字或空白括号、左右脸不对称、五官糊成一团、衣物穿模。\n" +
		"5) 与「电影级角色定妆照」相比，最丑的三点是什么（按严重度排序）。\n" +
		"每行不超过 60 字，中文。"

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
		out, err := client.ChatVision(t.Context(), sys, []llm.VisionContent{
			{Role: "user", Text: "这张图是角色定妆照，请按 1)-5) 评审。", Images: []string{uri}},
		}, false)
		rel := strings.TrimPrefix(f, filepath.Join("..", "..", "data", "assets_library")+string(os.PathSeparator))
		if err != nil {
			t.Logf("=== %s ===\n  VLM 失败：%v", rel, err)
			continue
		}
		t.Logf("=== %s ===\n%s", rel, strings.TrimSpace(out))
	}
}
