package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zz_probe_empty_body_live_test.go 临时探针：拿**盘上那份故障方案**走一遍唯一读取边界，
// 看镜 12 的正文是不是真的补上了、补出来的东西能不能过全部格式判据。
//
// 为什么要这个探针：夹具是我按真机形态抄的，抄得像不等于**那一份**真方案能过关
// （2026-09-28 的教训：夹具形态写错，门一直绿而生产从来没被咬到）。
// 没有这份真机项目时自动跳过（别的机器上不报 fail）。
func TestZZProbeEmptyBodyOnRealPlan(t *testing.T) {
	root := filepath.Join("..", "..", "data", "projects")
	projID := "我捡的团子，暖了三千人的船·第01集·霜里刨出个活物"
	st, err := NewStore(root)
	if err != nil {
		t.Skip(err)
	}
	// 路径按 Store 自己的口径（`data/projects/<书>/<集>/plan/plan.json`），**不许手拼**：
	// 手拼漏了"集目录嵌在书目录下"这一层，探针就静默跳过、拿不到任何真机证据。
	if _, err := os.Stat(st.PlanPath(projID)); err != nil {
		t.Skip("没有这份真机方案，跳过探针")
	}
	plan, err := st.LoadPlan(projID)
	if err != nil {
		t.Fatalf("读方案失败：%v", err)
	}
	t.Logf("读取边界之后：补过的空正文 = %v", plan.FilledBodyShots)
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		if sh.ID != 12 {
			continue
		}
		if strings.TrimSpace(sh.H3Prompt) == "" {
			t.Fatalf("镜 12 的正文仍是空的（渲染入口的致命门会照旧拦住整条产线）")
		}
		if v := H3FormatViolations(sh.H3Prompt); len(v) > 0 {
			t.Fatalf("补出来的正文没过格式判据：%v", v)
		}
		t.Logf("镜 12 时长=%d 内切=%v 违规=0 <d> 条数=%d 字数=%d",
			sh.Duration, sh.Cuts, strings.Count(sh.H3Prompt, "<d>"), H3DescriptionWords(sh.H3Prompt))
		// 把补出来的正文落一份到 scratch 供人眼复核（不进仓库）。
		if out := os.Getenv("PI_SCRATCH_DIR"); out != "" {
			_ = os.WriteFile(filepath.Join(out, "shot12_filled.txt"), []byte(sh.H3Prompt), 0o644)
			t.Logf("已写出：%s", filepath.Join(out, "shot12_filled.txt"))
		}
	}
	if ids := EmptyPromptShotIDs(plan); len(ids) > 0 {
		t.Logf("读取边界之后仍为空的镜（没材料、交给致命门）：%v", ids)
	}
}
