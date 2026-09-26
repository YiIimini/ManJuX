package project_test

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/project"
)

// plan_contract_ext_test.go 「双侧同源」的机械保证。
//
// 契约的常量与算式在两侧各有一份实现:
//   - 平台侧 project.NeededShotSeconds / PlanMinShotSeconds …(渲染入口用)
//   - 对话侧 director.NeededDuration / CharsPerSecond …(分镜阶段用)
//
// 为什么是两份:project 是更底层的包,director 反过来 import project,真共享常量会成环。
// 复制常量的风险是**两边悄悄走散** —— 那就等于技能端按 A 规则产出、平台端按 B 规则验收。
// 所以这个外部测试包(可以同时 import 两边)把"必须相等"钉死:谁改了一边,这里立刻红。
func TestContractConstantsMatchDirector(t *testing.T) {
	if project.PlanCharsPerSecond != director.CharsPerSecond {
		t.Fatalf("口播速率走散:contract=%v director=%v",
			project.PlanCharsPerSecond, director.CharsPerSecond)
	}
	if project.PlanMinShotSeconds != director.MinShotSeconds {
		t.Fatalf("单镜下限走散:contract=%d director=%d",
			project.PlanMinShotSeconds, director.MinShotSeconds)
	}
	if project.PlanMaxShotSeconds != director.MaxShotSeconds {
		t.Fatalf("单镜上限走散:contract=%d director=%d",
			project.PlanMaxShotSeconds, director.MaxShotSeconds)
	}
}

// TestNeededSecondsMatchesDirector 两个算式必须逐例给出同一个数。
//
// 只比常量不够:算式里的"加 0.3 收尾""向上取整""下限夹紧"任何一处改了,
// 都会让技能端的自检通过、平台端的验收不通过(或反过来),而常量比对看不出来。
func TestNeededSecondsMatchesDirector(t *testing.T) {
	cases := []project.Shot{
		{ID: 1, Duration: 6},
		{ID: 2, Duration: 4, Dialogue: []project.Line{{Speaker: "a", Text: "我准了。"}}},
		{ID: 3, Duration: 5, Dialogue: []project.Line{{Speaker: "a", Text: strings.Repeat("字", 18)}}},
		{ID: 4, Duration: 9, Dialogue: []project.Line{
			{Speaker: "a", Text: strings.Repeat("字", 20)},
			{Speaker: "b", Text: strings.Repeat("字", 12)}}},
		{ID: 5, Duration: 8, Narration: strings.Repeat("字", 30)},
		{ID: 6, Duration: 10, Dialogue: []project.Line{{Speaker: "a", Text: strings.Repeat("字", 60)}}},
	}
	for _, sh := range cases {
		want := director.NeededDuration(sh)
		got := project.NeededShotSeconds(sh)
		if got != want {
			t.Fatalf("镜 %d 需求时长走散:契约侧 %d 秒 / 分镜侧 %d 秒(台词=%d 字,旁白=%d 字)",
				sh.ID, got, want, len([]rune(strings.Join(lineTexts(sh), ""))), len([]rune(sh.Narration)))
		}
	}
}

func lineTexts(sh project.Shot) []string {
	out := make([]string, 0, len(sh.Dialogue))
	for _, l := range sh.Dialogue {
		out = append(out, l.Text)
	}
	return out
}
