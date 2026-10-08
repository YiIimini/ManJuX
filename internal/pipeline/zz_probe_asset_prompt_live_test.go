// zz_probe_asset_prompt_live_test.go 真机探针（用户「角色渲染的很丑，你看一下资产库」，2026-10-08）。
//
// 目的：拿**盘上那份真实记录**（资产库 asset.json 里存着的定妆照提示词 = 当时真机提交给 ComfyUI 的原文），
// 与**平台现在装配出来的**提示词逐处对照，证明三处硬伤真的被改掉了；并把新提示词写到 scratch，
// 供 `cmd/imagecheck` 用**生产同一条出图路径**再出一张，与盘上那张旧图做 A/B。
//
// 为什么要这样取证：这个项目的 plan.json 已被一次"界面上的删除项目"清空（见台账），
// 但**资产库记录还在** —— 旧提示词原文就在里面。卡正文（平台装配块之前的部分）用平台自己的
// 边界判据 `appearanceExcerpt` 还原，不手写启发式。
//
// 跑法：$env:MANJUX_LIVE_PROBE=1; go test ./internal/pipeline/ -run TestZZProbeAssetPromptAB -v
package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 三处硬伤的**特征串**（逐字取自盘上 asset.json 的旧提示词）
var assetBugs = []struct {
	name  string
	probe string
}{
	{"手办材质(BJD)", "BJD-inspired sculpted forms"},
	{"手办材质(哑光粉质)", "matte powdery skin shading with a dull even finish"},
	{"手办材质(收藏人偶)", "collectible virtual figure look"},
	{"空摘要条目", "other cast members of this series: ( …); ( …)"},
	{"空 NOT 行", "NOT: …"},
	{"胸像里又要求头身比", "keeps its stated head-to-body ratio"},
	{"胸像里又要求长腿", "stays long-limbed"},
}

func TestZZProbeAssetPromptAB(t *testing.T) {
	if os.Getenv("MANJUX_LIVE_PROBE") != "1" {
		t.Skip("设 MANJUX_LIVE_PROBE=1 才跑（读盘上资产库 + 写 scratch）")
	}
	root := filepath.Join("..", "..", "data", "assets_library", "characters")
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("读资产库失败: %v", err)
	}
	type rec struct {
		name, desc, stored, body string
	}
	var recs []rec
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, e.Name(), "asset.json"))
		if err != nil {
			t.Logf("跳过 %s: %v", e.Name(), err)
			continue
		}
		var a struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			ImagePrompt string `json:"image_prompt"`
		}
		if err := json.Unmarshal(b, &a); err != nil {
			t.Logf("跳过 %s（json）: %v", e.Name(), err)
			continue
		}
		stored := strings.TrimSpace(a.ImagePrompt)
		// 卡正文 = 剥掉平台取景抬头 + 只取平台装配标记之前的部分（平台自己的判据）
		body := appearanceExcerpt(StripPortraitLeadFraming(stored))
		recs = append(recs, rec{name: a.Name, desc: a.Description, stored: stored, body: body})
	}
	if len(recs) == 0 {
		t.Fatal("资产库里没有可读的角色卡")
	}

	// 真机 plan.style（逐字）：本项目风格里带「3D CG」但不含手办/虚拟人
	const realStyle = "半写实绘画感的东方星海国风 3D CG"
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{Style: realStyle}
	for i, r := range recs {
		plan.Characters = append(plan.Characters, project.Character{
			ID:          fmt.Sprintf("char_real_%d", i),
			Name:        r.name,
			Description: r.desc,
			ImagePrompt: r.body,
		})
	}

	scratch := os.Getenv("PI_SCRATCH_DIR")
	for i := range recs {
		ch := plan.Characters[i]
		sum := englishIdentitySummary(ch)
		t.Logf("【%s】卡正文 %d 字 / description %d 字（中文） ⇒ 英文外形摘要 %d 字",
			recs[i].name, len([]rune(recs[i].body)), len([]rune(recs[i].desc)), len([]rune(sum)))
		if sum == "" {
			t.Errorf("【%s】取不到英文外形摘要（那这一张在对比块/FACE ID 里就是空白）", recs[i].name)
		} else {
			t.Logf("   摘要首 120 字: %s", headRunes(sum, 120))
		}
		oldHits, newHits := assetBugHits(recs[i].stored), assetBugHits(characterImagePrompt(proj, plan, ch))
		t.Logf("   旧提示词命中硬伤 %d 处: %v", len(oldHits), oldHits)
		t.Logf("   新提示词命中硬伤 %d 处: %v", len(newHits), newHits)
		if len(newHits) != 0 {
			t.Errorf("【%s】新装配出来的提示词仍带硬伤: %v", recs[i].name, newHits)
		}
		// 新提示词必须**真的列出**别的角色（真机形态的英文摘要来自 image_prompt 正文）
		if n := len(recs); n > 1 {
			if block := characterImagePrompt(proj, plan, ch); !strings.Contains(block, "CAST DISTINCTNESS") {
				t.Errorf("【%s】新提示词里没有同剧对比块（那\"别和别人撞脸\"就落空了）", recs[i].name)
			} else if strings.Contains(block, "( …)") {
				t.Errorf("【%s】对比块里还有空条目", recs[i].name)
			}
		}
		if scratch != "" {
			out := filepath.Join(scratch, fmt.Sprintf("new_prompt_%d_%s.txt", i, recs[i].name))
			if err := os.WriteFile(out, []byte(characterImagePrompt(proj, plan, ch)), 0o644); err != nil {
				t.Errorf("写新提示词失败: %v", err)
			} else {
				t.Logf("   新提示词已写出: %s", out)
			}
		}
	}
}

// assetBugHits 返回一段提示词命中的硬伤特征串名字（按固定顺序）。
func assetBugHits(prompt string) []string {
	var out []string
	for _, b := range assetBugs {
		if strings.Contains(prompt, b.probe) {
			out = append(out, b.name)
		}
	}
	return out
}

// headRunes 截前 n 个字符（探针用；包内已有 firstRunes，避免重名）。
func headRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
