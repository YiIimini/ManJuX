package pipeline

import (
	"strings"
	"testing"
)

// 双侧共组的核心承诺:技能端已经交上 plan.json 时,平台**不许**再跑一次「LLM 直出分镜」。
//
// 这条不是性能优化,是**防毁活**:重新生成会改写每镜提示词 → 指纹全变 →
// 已渲的镜头全部作废。所以它必须有门钉着 —— 谁把 PhaseScript 加回来,这里就红。
func TestPipelineStagesSkipsScriptWhenPlanExists(t *testing.T) {
	withPlan := strings.Join(pipelineStages(PhaseAll, true), "→")
	if strings.Contains(withPlan, PhaseScript) {
		t.Fatalf("已有方案时全流程竟包含 %s 阶段(%s)—— 会烧 2–6 分钟并把已渲镜头全部作废",
			PhaseScript, withPlan)
	}
	if !strings.HasPrefix(withPlan, PhaseAssets) {
		t.Fatalf("跳过 script 后应从 assets 开工,实际 %s", withPlan)
	}
	// 后续阶段一个都不能少(跳过的是分镜,不是质检或合成)
	for _, want := range []string{PhaseAssets, PhaseRender, PhaseQC, PhaseAssemble} {
		if !strings.Contains(withPlan, want) {
			t.Fatalf("跳过 script 时丢了 %s 阶段:%s", want, withPlan)
		}
	}
}

// 没有方案时必须照旧全跑(不许"反着乱拦":空项目也得能一键出片)。
func TestPipelineStagesRunsScriptWhenNoPlan(t *testing.T) {
	stages := strings.Join(pipelineStages(PhaseAll, false), "→")
	if !strings.HasPrefix(stages, PhaseScript) {
		t.Fatalf("没有方案时必须以 %s 开头,实际 %s", PhaseScript, stages)
	}
}

// 显式点某个阶段 = 显式意图,不许替他省。
//
// 特别是:用户在剧本页点「生成分镜方案」时必须真的重新生成 ——
// 这条路是作者推翻方案、重新创作的唯一入口,被"优化"掉就没法重做了。
func TestPipelineStagesHonorsExplicitPhase(t *testing.T) {
	for _, ph := range []string{PhaseScript, PhaseAssets, PhaseRender, PhaseQC, PhaseAssemble} {
		for _, hasPlan := range []bool{false, true} {
			got := pipelineStages(ph, hasPlan)
			if len(got) != 1 || got[0] != ph {
				t.Fatalf("显式阶段 %s(hasPlan=%v) 应只跑它自己,实际 %v", ph, hasPlan, got)
			}
		}
	}
}
