package project

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// zz_probe_cuts_live_test.go 真机探针：拿**真机方案**跑平台自己的小分镜判据（只播报、不判定）。
//
// 【为什么留着（本仓惯例：真机探针都保留，见 zz_probe_*_live_test.go）】用户那句
// 「分镜脚本中的小分镜到底有没有做好？」的证据就是它跑出来的：结构字段 `shot.cuts` 有刀、
// 正文 `[Shot N] At` 也有刀、`CutIssues`/`CutCoverageIssues` 全过 —— **但渲染出来的片段里
// 兑现不了**（要把关节交给 `internal/filmaudit/cuts.go` 去量产物）。下次有人质疑"到底有没有做"，
// 跑它 + `go run ./cmd/auditfilm` 就能当场复现。
//
// 判据：没给 `MJX_PLAN` 就跳过（不假装验过）。
func TestZZProbeRealCuts(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("MJX_PLAN"))
	if path == "" {
		t.Skip("没给 MJX_PLAN")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	HealPlan(&p)

	perCut, perAt, noCuts, fromDoc := 0, 0, 0, 0
	for i := range p.Shots {
		sh := &p.Shots[i]
		cuts := sh.Cuts
		docCuts := ParseCutsFromPrompt(sh.H3Prompt)
		perCut += len(cuts)
		perAt += len(docCuts)
		if len(cuts) == 0 {
			noCuts++
		}
		if len(cuts) > 0 && len(docCuts) > 0 {
			fromDoc++
		}
		// 逐刀时长
		var spans []string
		for k := range cuts {
			spans = append(spans, fmt.Sprintf("%.2fs", sh.CutSeconds(k)))
		}
		acts := make([]string, 0, len(cuts))
		for _, c := range cuts {
			a := strings.TrimSpace(c.Action)
			if len([]rune(a)) > 22 {
				a = string([]rune(a)[:22]) + "…"
			}
			acts = append(acts, a)
		}
		t.Logf("镜 %-3d 时长=%2ds 结构刀=%d 正文刀=%d 景别=%s 运镜=%s",
			sh.ID, sh.Duration, len(cuts), len(docCuts), sh.ShotSize, sh.Camera)
		if len(cuts) > 0 {
			t.Logf("      逐刀时长: %s", strings.Join(spans, " / "))
			t.Logf("      逐刀内容: %s", strings.Join(acts, " | "))
		}
	}
	t.Logf("== 合计：%d 镜；结构字段刀数=%d（均 %.1f）；正文 [Shot N] 数=%d（均 %.1f）；一刀都没有的镜=%d；两边都有=%d",
		len(p.Shots), perCut, float64(perCut)/float64(len(p.Shots)),
		perAt, float64(perAt)/float64(len(p.Shots)), noCuts, fromDoc)

	// 平台自己的判据（用户的问题="到底有没有做好"，就用这两条答）
	t.Logf("---- CutIssues（每刀 1.5-4s / 首刀为 0 / 各刀之和=片段时长）----")
	for _, s := range CutIssues(&p) {
		t.Logf("   %s", s)
	}
	t.Logf("---- CutCoverageIssues（4 秒以上必须切）----")
	for _, s := range CutCoverageIssues(&p) {
		t.Logf("   %s", s)
	}
	// 六段式里的小镜头到底写没写进正文
	t.Logf("---- 正文里 [Shot N] At 的原文（前 6 镜）----")
	for i := 0; i < len(p.Shots) && i < 6; i++ {
		if d := ParseCutsFromPrompt(p.Shots[i].H3Prompt); len(d) > 0 {
			t.Logf("   镜 %d: %+v", p.Shots[i].ID, d)
		}
	}
}
