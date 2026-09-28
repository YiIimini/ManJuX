package director

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// prompt_duration_test.go 提示词里的单镜时长口径(包内闸门)。
//
// 背景(2026-09-26 平台侧整改清单 §1.3):单镜下限曾有三处口径 —— 提示词写死 4-8 秒、
// 契约 4-10 秒、云端引擎 ≥5 秒。后果是 LLM 直出稳定产出 4 秒镜(云端静默抬成 5 秒,
// 成片比计划长),而 4-8 又比契约更窄,长台词镜被迫多拆。
//
// 跨包的"文案 == 常量"断言在 internal/project/plan_contract_ext_test.go(那里能同时
// import 契约与引擎);这里补两条包内的事:模板渲染本身是纯的/干净的,以及**分批路径
// 与单轮路径共用同一段规则**(各写一份就又是一处会漂的口径)。

// TestRenderShotRulesUsesDurationConstants 渲染结果必须是常量算出来的那一句。
func TestRenderShotRulesUsesDurationConstants(t *testing.T) {
	rules := renderShotRules()
	for _, want := range []string{
		fmt.Sprintf("Each shot %d-%d seconds", GenMinShotSeconds, MaxShotSeconds),
		fmt.Sprintf("min %d, max %d", GenMinShotSeconds, MaxShotSeconds),
		fmt.Sprintf("in a %d-second shot", GenMinShotSeconds),
		fmt.Sprintf("If a line cannot fit %d seconds", MaxShotSeconds),
		fmt.Sprintf("runs about %s characters per second",
			strconv.FormatFloat(CharsPerSecond, 'g', -1, 64)),
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("分镜规则段缺 %q —— 文案与常量走散", want)
		}
	}
	if strings.Contains(rules, "{{") {
		t.Error("渲染后仍残留占位符:模板记号的替换没做干净")
	}
	// 纯函数:同样的输入必须给出同样的输出(渲染里不许掺状态/时间)。
	if rules != renderShotRules() {
		t.Error("renderShotRules 不是纯函数:两次调用结果不同")
	}
}

// TestBothPromptPathsShareShotRules 单轮与分批**共用同一段**规则。
//
// 这一条才是"不会继续漂"的关键:两条路径各写一份,改一处漏一处 ——
// 分批路径(长文默认走的那条)会继续按旧口径出镜。
func TestBothPromptPathsShareShotRules(t *testing.T) {
	for name, prompt := range map[string]string{
		"SystemPrompt":                         SystemPrompt,
		"batchSystemPrompt__OUTPUT_THIS_BATCH": batchSystemPrompt,
	} {
		if !strings.Contains(prompt, sysShotRules) {
			t.Errorf("%s 没有内嵌渲染后的分镜规则段(时长文案可能各写了一份)", name)
		}
		if !strings.Contains(prompt, fmt.Sprintf("Each shot %d-%d seconds", GenMinShotSeconds, MaxShotSeconds)) {
			t.Errorf("%s 里的时长区间不是常量渲染的 %d-%d 秒",
				name, GenMinShotSeconds, MaxShotSeconds)
		}
	}
	// 规划轮提示词不写单镜时长(它只切段),但也不许偷偷写死一个别的区间。
	if strings.Contains(planSystemPrompt, "Each shot") {
		t.Error("规划轮提示词不该出现单镜时长规则")
	}
}

// TestValidateClampsToGenerationFloor LLM 直出的兜底必须按**生成下限**钳,
// 不再产出 4 秒镜(与云端 ≥GenMinShotSeconds 对齐,避免计划与成片时长不一致)。
func TestValidateClampsToGenerationFloor(t *testing.T) {
	long := strings.Repeat("x", 400) // validate 要求 h3_prompt ≥400 字符
	p := &project.Plan{Title: "测试", Shots: []project.Shot{
		{ID: 1, Duration: 0, H3Prompt: long},  // LLM 漏写时长
		{ID: 2, Duration: 4, H3Prompt: long},  // 旧口径会原样放行的 4 秒镜
		{ID: 3, Duration: 12, H3Prompt: long}, // 超过上限
		{ID: 4, Duration: 7, H3Prompt: long},  // 区间内,原样不动
	}}
	if err := validate(p, 0); err != nil {
		t.Fatalf("validate 不应报错:%v", err)
	}
	want := []int{GenMinShotSeconds, GenMinShotSeconds, MaxShotSeconds, 7}
	for i, w := range want {
		if p.Shots[i].Duration != w {
			t.Errorf("镜 %d 时长 = %d,期望 %d(生成区间 %d-%d)",
				i+1, p.Shots[i].Duration, w, GenMinShotSeconds, MaxShotSeconds)
		}
	}
}
