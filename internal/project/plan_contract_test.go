package project

import (
	"strings"
	"testing"
)

// plan_contract_test.go 契约校验的**击穿用例**。
//
// 门必须证明两件事(与技能侧 scripts/qa_selftest.py 同一条纪律):
//  1. 它真的会拦 —— 每条规则都有一个专门打它的用例;
//  2. 它不乱拦 —— 合规样本必须零 issue(误拦的规则比没有规则更糟,因为报告没人看)。
//
// 契约的消费者是**渲染**:漏放一条悬空引用,成片里就是"角色没有定妆照、每镜换脸"
// 这种没人知道该怪谁的故障。所以这里宁可把每条规则都钉死。

func samplePlan() *Plan {
	return &Plan{
		Title:   "测试集",
		Logline: "一句话梗概",
		Style:   "PHOTOREAL 3D WORLD, SEMI-STYLISED FACES",
		Characters: []Character{{
			ID: "char_a", Name: "阿甲", Kind: "human",
			Description: "二十岁男子", ImagePrompt: "a young man ...", Voice: "young male voice",
		}},
		Scenes: []Scene{{
			ID: "scene_room", Name: "破庙", Description: "潮湿的破庙", ImagePrompt: "a ruined temple ...",
		}},
		Shots: []Shot{{
			ID: 1, Scene: "scene_room", Characters: []string{"char_a"},
			ShotSize: "中景", Camera: "slow push-in", Action: "他站起来",
			Dialogue: []Line{{Speaker: "阿甲", Text: "我准了。"}},
			Duration: 5, Mode: "ref2va",
			H3Prompt: "subject_definitions:\n<Subject 1> is 阿甲 ...\n<d>[Chinese] 我准了。</d>",
		}},
	}
}

func TestValidatePlanCleanSample(t *testing.T) {
	issues := ValidatePlan(samplePlan())
	if len(issues) != 0 {
		t.Fatalf("合规样本不该有任何 issue(误拦比漏拦更糟),实际 %d 条:%v",
			len(issues), PlanIssuesText(issues))
	}
}

func TestValidatePlanCatches(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Plan)
		level   string
		wantSub string // 命中 issues 里的 where 或 msg 子串
	}{
		{"空方案", func(p *Plan) {}, "fail", "方案为空"}, // nil 由下面单独构造
		{"没有镜头", func(p *Plan) { p.Shots = nil }, "fail", "没有 shots"},
		{"风格为空", func(p *Plan) { p.Style = "" }, "warn", "风格为空"},
		{"角色 id 为空", func(p *Plan) { p.Characters[0].ID = "" }, "fail", "角色 id 为空"},
		{"场景 id 为空", func(p *Plan) { p.Scenes[0].ID = "" }, "fail", "场景 id 为空"},
		{"镜头 id 为 0", func(p *Plan) { p.Shots[0].ID = 0 }, "fail", "必须为正整数"},
		{"镜头 id 重复", func(p *Plan) {
			p.Shots = append(p.Shots, p.Shots[0])
		}, "fail", "镜头 id 重复"},
		{"场景悬空引用", func(p *Plan) { p.Shots[0].Scene = "scene_nope" }, "fail", "不存在的场景"},
		{"角色悬空引用", func(p *Plan) { p.Shots[0].Characters = []string{"char_nope"} }, "fail", "不存在的角色"},
		{"时长过短", func(p *Plan) { p.Shots[0].Duration = 3 }, "fail", "超出 H3 合法区间"},
		{"时长过长", func(p *Plan) { p.Shots[0].Duration = 12 }, "fail", "超出 H3 合法区间"},
		{"提示词为空", func(p *Plan) { p.Shots[0].H3Prompt = "  " }, "fail", "h3_prompt 为空"},
		{"mode 非法", func(p *Plan) { p.Shots[0].Mode = "i2v" }, "warn", "不是 ref2va/t2va"},
		// 注意:判定与平台一致用 Contains —— "大特写""中近景"这类同族词**合法**
		// (pipeline.wantsAnglesStrip 也是 Contains 判的);只有真的不在族里的才 warn,
		// 这里用"全身"(真实景别但不在中文五档写法里)。
		{"景别不在白名单", func(p *Plan) { p.Shots[0].ShotSize = "全身" }, "warn", "不在白名单"},
		{"未指定场景", func(p *Plan) { p.Shots[0].Scene = "" }, "warn", "未指定 scene"},
		{"台词没有说话人", func(p *Plan) { p.Shots[0].Dialogue[0].Speaker = "" }, "warn", "没有说话人"},
		{"镜头号不连续", func(p *Plan) { p.Shots[0].ID = 2 }, "warn", "镜头号不连续"},
		{"台词装不下", func(p *Plan) {
			// 60 字 ÷ 4.5 + 0.3 ≈ 13.6 秒 > 单镜上限 10 秒
			p.Shots[0].Dialogue = []Line{{Speaker: "阿甲", Text: strings.Repeat("字", 60)}}
			p.Shots[0].Duration = 10
		}, "warn", "超过单镜上限"},
		{"角色缺定妆提示词", func(p *Plan) { p.Characters[0].ImagePrompt = "" }, "warn", "缺 image_prompt"},
		{"场景缺提示词", func(p *Plan) { p.Scenes[0].ImagePrompt = "" }, "warn", "缺 image_prompt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := samplePlan()
			if tc.name != "空方案" {
				tc.mutate(p)
			} else {
				issues := ValidatePlan(nil)
				if len(PlanFails(issues)) == 0 {
					t.Fatal("nil plan 必须报 fail")
				}
				return
			}
			items := PlanIssuesText(ValidatePlan(p))
			found := false
			for _, it := range items {
				if strings.Contains(it, tc.level) && strings.Contains(it, tc.wantSub) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("期望 %s 级命中 %q,实际 issues:%v", tc.level, tc.wantSub, items)
			}
		})
	}
}

// TestValidatePlanNotTooStrict 反向用例:几条"看起来可疑但其实合法"的写法不能被拦。
//
// 都是真实会出现的形态:纯空镜(无角色无台词)、t2va 纯文生镜、中文景别带修饰词。
func TestValidatePlanNotTooStrict(t *testing.T) {
	p := samplePlan()
	p.Shots = []Shot{
		{ID: 1, Scene: "scene_room", ShotSize: "远景", Camera: "static",
			Action: "空庙的雨", Duration: 4, Mode: "t2va", H3Prompt: "empty temple in the rain"},
		{ID: 2, Scene: "scene_room", Characters: []string{"char_a"}, ShotSize: "中近景",
			Camera: "handheld", Action: "他抬头", Duration: 6, Mode: "ref2va",
			Narration: "旁白一句。", H3Prompt: "subject_definitions: ..."},
	}
	if fails := PlanFails(ValidatePlan(p)); len(fails) != 0 {
		t.Fatalf("合法的空镜/纯文生镜/t2va 不该被拒: %v", PlanIssuesText(fails))
	}
}

// TestValidatePlanShotSizeFamily 景别同族词不 warn:判定口径必须与平台一致(Contains)。
//
// 为什么单独钉一条:"大特写""中近景"是创作者真会写的写法,而平台自己的
// pipeline.wantsAnglesStrip 也是 Contains 判的 —— 若契约侧改成精确匹配,
// 就会把合法写法报成 warn,报告立刻失去可信度(误拦的规则比没有规则更糟)。
func TestValidatePlanShotSizeFamily(t *testing.T) {
	for _, s := range []string{"远景", "全景", "中景", "中近景", "近景", "特写", "大特写", "中景偏全景"} {
		p := samplePlan()
		p.Shots[0].ShotSize = s
		for _, it := range ValidatePlan(p) {
			if strings.Contains(it.Msg, "不在白名单") {
				t.Fatalf("景别 %q 属同族词,不该 warn:%s", s, it)
			}
		}
	}
}

// TestNeededShotSeconds 台词时长预算:与 director 同规则(4.5 字/秒 + 0.3 收尾,向上取整)。
func TestNeededShotSeconds(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", PlanMinShotSeconds},     // 无台词 → 下限 4
		{"我准了。", PlanMinShotSeconds}, // 4 字 ≈ 1.2 秒 → 抬到下限
		{strings.Repeat("字", 18), 5}, // 18/4.5+0.3 = 4.3 → 5
		{strings.Repeat("字", 40), 9}, // 40/4.5+0.3 = 9.19 → 10? 见下断言
	}
	for _, c := range cases {
		sh := Shot{Dialogue: []Line{{Speaker: "x", Text: c.text}}}
		got := NeededShotSeconds(sh)
		if c.text == strings.Repeat("字", 40) {
			if got != 10 {
				t.Fatalf("40 字应需 10 秒(9.19 向上取整),实际 %d", got)
			}
			continue
		}
		if got != c.want {
			t.Fatalf("%q 期望 %d 秒,实际 %d", c.text, c.want, got)
		}
	}
	// 旁白与台词一起计入
	sh := Shot{Dialogue: []Line{{Speaker: "x", Text: strings.Repeat("字", 9)}},
		Narration: strings.Repeat("字", 9)}
	if got := NeededShotSeconds(sh); got != 5 {
		t.Fatalf("台词+旁白合计 18 字应需 5 秒,实际 %d", got)
	}
}
