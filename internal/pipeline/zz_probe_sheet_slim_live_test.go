// zz_probe_sheet_slim_live_test.go 真机探针（用户「定妆照提示词瘦身」，2026-10-10）。
//
// 拿**盘上那份真实成品**（项目 `assets/asset_map.json` 里存着的定妆照提示词 = 当时真机提交给
// ComfyUI 的原文）走一遍**现在的装配点**，量出三件事：
//
//	① 取景口径（chest-up portrait / ONE QUARTER / 裁切口径 / BUST PORTRAIT / 收尾纪律）装配前后的份数
//	   —— 用来回答"盘上那批到底还能不能更瘦"；
//	② 提示词字符数变化；
//	③ CAST DISTINCTNESS 那六条摘要的长度（旧实现 = 160 字符硬切 + 省略号，见 git 里那版；
//	   新实现 = 完整从句）。旧值在这里**就地按老规则重算**，免得拿记忆里的数字播报。
//
// 角色一律从 `plan/plan.json` 取（**带真实的 kind**：用裸 Character 会让 kind 被名字猜错 ——
// 探针第一版就把灵宠「吞吞」当成人类、套上了人像取景，log 里的数字是假的）。
//
// 跑法：$env:MANJUX_LIVE_PROBE=1; go test ./internal/pipeline/ -run TestZZProbeSheetSlim -v
package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// oldDistinctnessEntry 旧实现那一步（`git show HEAD~1:internal/pipeline/prompts.go` 里的原样）：
// 160 字符硬切 + 省略号。探针里重算一次，只为把"瘦了多少"量出来。
func oldDistinctnessEntry(d string) string {
	if r := []rune(d); len(r) > 160 {
		return string(r[:160]) + "…"
	}
	return d
}

func TestZZProbeSheetSlim(t *testing.T) {
	if os.Getenv("MANJUX_LIVE_PROBE") != "1" {
		t.Skip("设 MANJUX_LIVE_PROBE=1 才跑（读盘上真机成品卡 + 写 scratch）")
	}
	type card struct {
		id, name, desc, kind, prompt string
	}
	var cards []card
	plans, _ := filepath.Glob(filepath.Join("..", "..", "data", "projects", "*", "*", "plan", "plan.json"))
	for _, pp := range plans {
		pb, err := os.ReadFile(pp)
		if err != nil {
			continue
		}
		var pl struct {
			Characters []struct {
				ID, Name, Kind, Description string
			} `json:"characters"`
		}
		if err := json.Unmarshal(pb, &pl); err != nil || len(pl.Characters) == 0 {
			continue
		}
		ab, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(pp)), "assets", "asset_map.json"))
		if err != nil {
			continue
		}
		var am map[string]struct {
			Name        string `json:"name"`
			ImagePrompt string `json:"image_prompt"`
		}
		if err := json.Unmarshal(ab, &am); err != nil {
			continue
		}
		for _, c := range pl.Characters {
			v, ok := am["char:"+c.ID]
			if !ok || len(v.ImagePrompt) < 800 {
				continue
			}
			cards = append(cards, card{c.ID, c.Name, c.Description, c.Kind, v.ImagePrompt})
		}
		break // 一份项目就够
	}
	if len(cards) == 0 {
		t.Skip("盘上没有可用的真机成品卡（plan.json + asset_map.json 不在或没对上）")
	}
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{Style: "国风电影级"}
	scratch := os.Getenv("PI_SCRATCH_DIR")
	sum := func(s string) string {
		return "chest-up portrait×" + itoa(strings.Count(strings.ToLower(s), "chest-up portrait")) +
			" ONE QUARTER×" + itoa(strings.Count(strings.ToUpper(s), "ONE QUARTER")) +
			" 裁切口径×" + itoa(strings.Count(s, portraitCropCue)) +
			" BUST PORTRAIT×" + itoa(strings.Count(s, "BUST PORTRAIT")) +
			" 收尾纪律×" + itoa(strings.Count(s, "FRAMING (mandatory)"))
	}
	for _, c := range cards {
		ch := project.Character{ID: c.id, Name: c.name, Description: c.desc, ImagePrompt: c.prompt}
		got := PortraitPromptForProject(proj, plan, ch)
		kind := NormalizeEntityKind(string(ResolveEntityKind(ch)))
		same := "（逐字节相同：盘上这份已经是「一处收口」的形态）"
		if got != c.prompt {
			same = "（有变化）"
		}
		t.Logf("【char:%s】%s / kind=%s\n  装配前 %d 字：%s\n  装配后 %d 字：%s %s",
			c.id, c.name, kind, len(c.prompt), sum(c.prompt), len(got), sum(got), same)
		if again := PortraitPromptForProject(proj, plan, project.Character{ID: ch.ID, Name: ch.Name, Description: ch.Description, ImagePrompt: got}); again != got {
			t.Errorf("【char:%s】收口不幂等：%d 字 → %d 字", c.id, len(got), len(again))
		}
		if scratch != "" && kind == KindHuman {
			out := filepath.Join(scratch, "sheet_slim_"+c.id+".txt")
			if err := os.WriteFile(out, []byte(got), 0o644); err != nil {
				t.Logf("写 scratch 失败: %v", err)
			}
		}
	}
	// CAST DISTINCTNESS：同一条"别人的外形摘要"，新旧两种写法各占多少字符。
	cplan := &project.Plan{}
	for _, c := range cards {
		cplan.Characters = append(cplan.Characters, project.Character{
			ID: c.id, Name: c.name, Description: c.desc, ImagePrompt: c.prompt,
		})
	}
	blk := castContrastBlock(cplan, "", "", KindHuman)
	n, newLen, oldLen, midWord := 0, 0, 0, 0
	for i, c := range cards {
		if i == 0 { // 第一条当"自己"，对比块里不列它
			continue
		}
		d := englishIdentitySummary(project.Character{ID: c.id, Name: c.name, Description: c.desc, ImagePrompt: c.prompt})
		if d == "" {
			continue
		}
		old := oldDistinctnessEntry(d)
		new := distinctnessNote(d)
		n++
		newLen += len([]rune(new))
		oldLen += len([]rune(old))
		if strings.HasSuffix(old, "…") {
			midWord++ // 旧写法截断处前面那个字符若仍是字母/数字，就是切在词中间
		}
	}
	t.Logf("CAST DISTINCTNESS：%d 条摘要；新写法合计 %d 字符，旧写法（160 硬切+省略号）合计 %d 字符 ⇒ 变化 %+d；旧写法被截断 %d 条",
		n, newLen, oldLen, newLen-oldLen, midWord)
	t.Logf("装配出来的对比块末尾带省略号：%d 处（新写法应当一处都没有）；块长度 %d 字符",
		strings.Count(blk, "…"), len([]rune(blk)))
}
