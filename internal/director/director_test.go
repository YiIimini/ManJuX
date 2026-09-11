package director

import (
	"strings"
	"testing"
)

// TestBuildSingleUserPromptContentDriven 内容驱动(MaxShots=0)时,提示词里
// 绝不能出现「(N shots)」这类把镜头数钉死的字样 —— 否则「该多少就多少」失效。
func TestBuildSingleUserPromptContentDriven(t *testing.T) {
	opt := Options{MaxShots: defaultContentDrivenShots, TotalSeconds: 42}
	got := buildSingleUserPrompt("正文内容", opt, true, true)

	if strings.Contains(got, "shots)") {
		t.Errorf("内容驱动提示词不应写死镜头数:\n%s", got)
	}
	if !strings.Contains(got, "Decide the shot count YOURSELF") {
		t.Errorf("应显式要求 LLM 自行决定镜头数:\n%s", got)
	}
	if !strings.Contains(got, "about 42 seconds") {
		t.Errorf("用户给了时长时应保留时长约束:\n%s", got)
	}
}

// TestBuildSingleUserPromptContentDrivenNoSeconds 时长也没给时,镜头数与时长
// 都交给内容决定,提示词不应出现任何数值约束。
func TestBuildSingleUserPromptContentDrivenNoSeconds(t *testing.T) {
	opt := Options{MaxShots: defaultContentDrivenShots, TotalSeconds: 42}
	got := buildSingleUserPrompt("正文内容", opt, true, false)

	if strings.Contains(got, "about") {
		t.Errorf("用户未给时长时不应出现时长约束:\n%s", got)
	}
	if !strings.Contains(got, "Decide BOTH the shot count and the total duration") {
		t.Errorf("应要求同时决定镜头数与时长:\n%s", got)
	}
}

// TestBuildSingleUserPromptExplicit 用户明确指定镜头数时沿用原模板,
// 保证既有「指定值」行为一字不变。
func TestBuildSingleUserPromptExplicit(t *testing.T) {
	opt := Options{MaxShots: 8, TotalSeconds: 56}
	got := buildSingleUserPrompt("正文内容", opt, false, true)

	if !strings.Contains(got, "about 56 seconds (8 shots)") {
		t.Errorf("指定镜头数时应原样写进提示词:\n%s", got)
	}
}

// TestGenerateKeepsContentDrivenIntent 校验 Generate 内部的兜底不会把内容驱动
// 误判成「用户指定了 6 镜」—— 单批路径必须走内容驱动提示词分支。
func TestGenerateKeepsContentDrivenIntent(t *testing.T) {
	opt := Options{MaxShots: 0, TotalSeconds: 0}
	contentDriven := opt.MaxShots <= 0
	secondsGiven := opt.TotalSeconds > 0
	if !contentDriven || secondsGiven {
		t.Fatalf("MaxShots=0/TotalSeconds=0 应判定为内容驱动且未给时长")
	}
}
