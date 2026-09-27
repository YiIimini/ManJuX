package project_test

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/director"
	"github.com/YiIimini/ManJuX/internal/engine/h3api"
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
// TestViewVocabularyReachesBothSides 视角/尾帧/场景卡三套词表必须**同时**出现在两侧:
// 契约门按 project 的常量判,而产出侧是 director 的提示词文案 —— 只改一边的后果是
// "门按 A 判、LLM 按 B 写",两边都觉得自己对(2026-09-27 技能侧整改单 §1.1–§1.3)。
//
// 按**产出什么**分组守:写 shots 的两条路径守视角与尾帧状态,写 scenes 的两条路径守场景卡。
// 混着要求"每条提示词都有全部规则"不是事实(分批分镜轮不产出场景卡),也就守不住真缺口 ——
// 2026-09-27 这条守卫就抓到过一次:规划轮(真正写 scenes[] 的地方)漏了 sysSceneRules。
func TestViewVocabularyReachesBothSides(t *testing.T) {
	for name, sp := range director.ShotPrompts() {
		for _, v := range project.PlanViews {
			if !strings.Contains(sp, v) {
				t.Errorf("%s 缺视角词表项 %q —— LLM 写不出契约门认得的机位", name, v)
			}
		}
		if !strings.Contains(sp, project.PlanEstablishingExtra) {
			t.Errorf("%s 缺建立镜头串 %q", name, project.PlanEstablishingExtra)
		}
		for _, want := range []string{"end_state", "尾帧状态", "view", "hook_type", "HOOK DENSITY", "NO CARD, NO FACE"} {
			if !strings.Contains(sp, want) {
				t.Errorf("%s 缺 %q —— 产出侧不写这个字段,契约门就永远在报同一件事", name, want)
			}
		}
		for _, h := range project.PlanHookTypes {
			if !strings.Contains(sp, h) {
				t.Errorf("%s 缺官方钩子词表项 %q", name, h)
			}
		}
	}
	for name, sp := range director.ScenePrompts() {
		if !strings.Contains(sp, project.PlanScenePreEventClause) {
			t.Errorf("%s 缺场景事件隔离串 %q", name, project.PlanScenePreEventClause)
		}
		for _, want := range []string{"landmarks", "light_state", "EVENT ISOLATION", "PROP BUDGET"} {
			if !strings.Contains(sp, want) {
				t.Errorf("%s 缺场景卡纪律 %q", name, want)
			}
		}
	}
}

func TestContractConstantsMatchDirector(t *testing.T) {
	if project.PlanCharsPerSecond != director.CharsPerSecond {
		t.Fatalf("口播速率走散:contract=%v director=%v",
			project.PlanCharsPerSecond, director.CharsPerSecond)
	}
	if project.PlanMinShotSeconds != director.MinShotSeconds {
		t.Fatalf("单镜渲染窗口下限走散:contract=%d director=%d",
			project.PlanMinShotSeconds, director.MinShotSeconds)
	}
	if project.PlanMaxShotSeconds != director.MaxShotSeconds {
		t.Fatalf("单镜上限走散:contract=%d director=%d",
			project.PlanMaxShotSeconds, director.MaxShotSeconds)
	}
}

// TestSystemPromptShotDurationRangeMatchesDirector 提示词里的时长数字必须由常量渲染。
//
// 为什么要单钉这一条(2026-09-26 平台侧整改清单 §1.3):SystemPrompt 里曾**写死**
// "Each shot 4-8 seconds" 与 "min 4, max 8",而计划契约是 4-10、云端引擎的下限是
// 5 秒(h3api.MinDurationSeconds)—— 三处口径各自漂,后果是:
//
//	① LLM 直出路径稳定产出 4 秒镜 → 云端 clampDuration 静默抬成 5 秒(成片比计划长);
//	② 4-8 比契约的 4-10 更窄 → 长台词镜被迫拆得比需要的更碎。
//
// 常量改对了没用:文案里写死的数字照样会漂,而它才是模型真正读到的东西 ——
// 所以这里**直接扫文案**,并把它钉回常量。
func TestSystemPromptShotDurationRangeMatchesDirector(t *testing.T) {
	// ① 生成下限不得低于云端硬下限:低于它 = 生成出来的镜必然被云端改写。
	if director.GenMinShotSeconds < h3api.MinDurationSeconds {
		t.Fatalf("生成下限 %d 秒低于云端下限 %d 秒(h3api.MinDurationSeconds):"+
			"LLM 直出会产出被云端静默抬长的镜头,计划与成片时长不一致",
			director.GenMinShotSeconds, h3api.MinDurationSeconds)
	}
	// ② 生成区间必须落在契约的渲染窗口内,否则生成出来的方案会被契约拒收。
	if director.GenMinShotSeconds < project.PlanMinShotSeconds ||
		director.GenMinShotSeconds > project.PlanMaxShotSeconds {
		t.Fatalf("生成下限 %d 秒不在契约渲染窗口 %d–%d 秒内",
			director.GenMinShotSeconds, project.PlanMinShotSeconds, project.PlanMaxShotSeconds)
	}
	if director.MaxShotSeconds != project.PlanMaxShotSeconds {
		t.Fatalf("生成上限 %d 秒与契约上限 %d 秒不一致:提示词会与契约打架",
			director.MaxShotSeconds, project.PlanMaxShotSeconds)
	}

	// ③ 文案里必须出现由常量渲染出来的那两句(缺一句就说明它又写死了别的数)。
	wantRange := fmt.Sprintf("Each shot %d-%d seconds", director.GenMinShotSeconds, director.MaxShotSeconds)
	wantClamp := fmt.Sprintf("min %d, max %d", director.GenMinShotSeconds, director.MaxShotSeconds)
	wantRate := fmt.Sprintf("runs about %s characters per second",
		strconv.FormatFloat(float64(director.CharsPerSecond), 'g', -1, 64))
	for _, want := range []string{wantRange, wantClamp, wantRate} {
		if !strings.Contains(director.SystemPrompt, want) {
			t.Fatalf("SystemPrompt 里没有由常量渲染的 %q —— 时长文案又漂了\n生成区间应为 %d-%d 秒",
				want, director.GenMinShotSeconds, director.MaxShotSeconds)
		}
	}

	// ④ 反向闸门:文案里**任何** "N-M seconds" / "min N, max M" 都必须等于常量。
	// 只查③不够 —— 有人补一句 "(6-9 seconds for close-ups)" 也一样是第二个口径。
	for _, m := range reSecondRange.FindAllStringSubmatch(director.SystemPrompt, -1) {
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[2])
		if lo != director.GenMinShotSeconds || hi != director.MaxShotSeconds {
			t.Fatalf("SystemPrompt 里的时长区间 %q 与常量 %d-%d 不符(第二个口径)",
				m[0], director.GenMinShotSeconds, director.MaxShotSeconds)
		}
	}
	for _, m := range reMinMaxClamp.FindAllStringSubmatch(director.SystemPrompt, -1) {
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[2])
		if lo != director.GenMinShotSeconds || hi != director.MaxShotSeconds {
			t.Fatalf("SystemPrompt 里的台词时长钳制 %q 与常量 %d-%d 不符(第二个口径)",
				m[0], director.GenMinShotSeconds, director.MaxShotSeconds)
		}
	}

	// ⑤ 旧的写死值必须彻底消失:留着"4-8"就等于这一条整改没做。
	for _, stale := range []string{"4-8 seconds", "min 4, max 8", "in a 4-second shot"} {
		if strings.Contains(director.SystemPrompt, stale) {
			t.Fatalf("SystemPrompt 里还留着写死的旧口径 %q(下限已是 %d 秒)",
				stale, director.GenMinShotSeconds)
		}
	}

	// ⑥ 占位符必须被渲染干净:漏掉一个 {{…}} 会把模板记号直接喂给模型。
	if i := strings.Index(director.SystemPrompt, "{{"); i >= 0 {
		t.Fatalf("SystemPrompt 里残留未渲染的占位符:%q",
			director.SystemPrompt[i:min(i+20, len(director.SystemPrompt))])
	}
}

// 扫文案用的两条判据。刻意放宽到"任何 N-M seconds",而不是只找已知的那一句。
var (
	reSecondRange = regexp.MustCompile(`(\d+)-(\d+) seconds`)
	reMinMaxClamp = regexp.MustCompile(`min (\d+), max (\d+)`)
)

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
