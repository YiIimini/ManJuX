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
			Landmarks: "门槛与东侧立柱", LightState: "黄昏，主光从门洞斜射",
		}},
		Shots: []Shot{{
			ID: 1, Scene: "scene_room", Characters: []string{"char_a"},
			ShotSize: "中景", Camera: "slow push-in", Action: "他站起来",
			View:     ViewAerial45,
			EndState: "他站在供桌前，右手抬起指向门外，视线朝左前方，地面水渍未干",
			Dialogue: []Line{{Speaker: "阿甲", Text: "我准了。"}},
			Duration: 5, Mode: "ref2va",
			H3Prompt: compliantH3("subject_definitions:\n<Subject 1> is 阿甲 ...\n\ndetailed_description:\n" +
				SampleStyleSentence + "\n[Shot 1] he stands up under " + ViewAerial45 + "; " +
				PlanEstablishingExtra + ".<d>[Chinese] 我准了。</d>"),
		}},
	}
}

// SampleStyleSentence 合规样本的风格句（写在第一个 [Shot] 之前，官方硬规则）。
const SampleStyleSentence = "A cinematic GUOFENG period film, real materials, soft motivated key light, natural film grain."

// compliantH3 把一段提示词补成"格式合规"的骨架：风格句位置正确、主视角串与建立镜头串都在。
//
// 为什么需要一个基线：契约里有一批**覆盖率类**判据（主视角、建立镜头、H3 格式），
// 任何"最小样本"都会命中它们 —— 而每个用例真正要测的只是自己那一维。给样本一个合规基线，
// 用例就只剩一个变量（本包的纪律：一条规则一个击穿用例，别让别的规则混进来搅浑判据）。
func compliantH3(body string) string {
	if !strings.Contains(body, "detailed_description:") && !strings.Contains(body, "integrated_multimodal_description:") {
		body += "\n\ndetailed_description:\n" + SampleStyleSentence + "\n[Shot 1] " +
			ViewAerial45 + "; " + PlanEstablishingExtra + "."
	}
	if _, ok := PlanViewIn(body); !ok {
		body += "\n" + ViewAerial45 + "."
	}
	if !strings.Contains(body, PlanEstablishingExtra) {
		body += " " + PlanEstablishingExtra + "."
	}
	return body
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

// 语音层（契约 v2）：画外音与画内对白必须分得开。
//
// 用户报障原话：「系统旁白,主角动嘴?」—— 技能端 889 条台词里 543 条是旁白,
// 却与画内对白写成同一个样子,模型只能找画面里的人来张嘴。下面这几条把这些
// "可判的形态"钉死:光靠提示词措辞约束不住。
func TestValidatePlanVoiceLayer(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(p *Plan)
		level   string
		wantSub string
	}{
		{
			name: "旁白标签却没标 os",
			mutate: func(p *Plan) {
				p.Shots[0].Dialogue = []Line{{Speaker: "旁白", Text: "雨一直在下。"}}
				p.Shots[0].Narration = ""
			},
			level: "fail", wantSub: "os=true",
		},
		{
			name:    "系统音没标 os",
			mutate:  func(p *Plan) { p.Shots[0].Dialogue = []Line{{Speaker: "系统", Text: "任务完成。"}} },
			level:   "fail",
			wantSub: "os=true",
		},
		{
			name: "说话人不在本镜画面里",
			mutate: func(p *Plan) {
				p.Characters = append(p.Characters, Character{ID: "char_b", Name: "阿乙",
					Kind: "human", Description: "另一个人", ImagePrompt: "p"})
				p.Shots[0].Dialogue = []Line{{Speaker: "阿乙", Text: "我也不在画面里。"}}
			},
			level: "fail", wantSub: "不在本镜 characters",
		},
		{
			name: "画外音镜提示词缺「嘴唇闭合」",
			mutate: func(p *Plan) {
				p.Shots[0].Dialogue = []Line{{Speaker: "旁白", Text: "雨一直在下。", OS: true}}
				p.Shots[0].H3Prompt = "subject_definitions:\n<d>[Chinese]雨一直在下。</d>"
			},
			level: "warn", wantSub: "lips remain completely closed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := samplePlan()
			tc.mutate(p)
			items := PlanIssuesText(ValidatePlan(p))
			for _, it := range items {
				if strings.Contains(it, tc.level) && strings.Contains(it, tc.wantSub) {
					return
				}
			}
			t.Fatalf("期望 %s 级命中 %q,实际:%v", tc.level, tc.wantSub, items)
		})
	}
}

// TestValidatePlanAcceptsProperVoiceover 合法的画外音不许被拦（门要证明它会拦，也要证明它不乱拦）。
func TestValidatePlanAcceptsProperVoiceover(t *testing.T) {
	p := samplePlan()
	p.Shots[0].Dialogue = []Line{{Speaker: "旁白", Text: "雨一直在下。", OS: true}}
	p.Shots[0].H3Prompt = "subject_definitions:\nOff-screen narration: the narrator says: " +
		"<d>[Chinese]雨一直在下。</d> while the on-screen characters' lips remain completely closed"
	issues := ValidatePlan(p)
	for _, it := range issues {
		if it.Level == "fail" {
			t.Fatalf("合规画外音不该 fail：%s", it)
		}
	}
	// 内心独白：人就在画面里，但那句话是心里说的 —— 合法，且不算"说话人不在画面"
	p2 := samplePlan()
	p2.Shots[0].Dialogue = []Line{{Speaker: "阿甲", Text: "行。", OS: true}}
	for _, it := range PlanFails(ValidatePlan(p2)) {
		t.Fatalf("内心独白不该 fail：%s", it)
	}
}

// TestVoiceoverHelpers HasVoiceover / IsVoiceoverSpeaker 是两侧共用的判据。
func TestVoiceoverHelpers(t *testing.T) {
	if !IsVoiceoverSpeaker("旁白") || !IsVoiceoverSpeaker(" 系统 ") || IsVoiceoverSpeaker("阿甲") {
		t.Fatal("画外音标签判据不对 —— 它会决定平台是否给镜头补「嘴唇闭合」")
	}
	sh := Shot{Dialogue: []Line{{Speaker: "阿甲", Text: "x", OS: true}}}
	if !HasVoiceover(sh) {
		t.Fatal("os=true 的镜次必须算画外音")
	}
	if HasVoiceover(Shot{Dialogue: []Line{{Speaker: "阿甲", Text: "x"}}}) {
		t.Fatal("普通画内对白不该算画外音")
	}
	if !HasVoiceover(Shot{Narration: "旁白一句"}) {
		t.Fatal("narration 非空即画外音")
	}
}

// TestValidatePlanNotTooStrict 反向用例:几条"看起来可疑但其实合法"的写法不能被拦。
//
// 都是真实会出现的形态:纯空镜(无角色无台词)、t2va 纯文生镜、中文景别带修饰词。
// 注意第 2 镜:它有旁白,所以提示词里**必须**有 <d> —— 否则那句旁白只是字幕,
// H3 根本不会把它念出来(这正是 fail 规则要拦的,不是误拦)。
func TestValidatePlanNotTooStrict(t *testing.T) {
	p := samplePlan()
	p.Shots = []Shot{
		{ID: 1, Scene: "scene_room", ShotSize: "远景", Camera: "static",
			Action: "空庙的雨", Duration: 4, Mode: "t2va", H3Prompt: "empty temple in the rain"},
		{ID: 2, Scene: "scene_room", Characters: []string{"char_a"}, ShotSize: "中近景",
			Camera: "handheld", Action: "他抬头", Duration: 6, Mode: "ref2va",
			Narration: "旁白一句。",
			H3Prompt: "subject_definitions: ...\nOff-screen narration: the narrator says in an " +
				"off-screen voiceover: <d>[Chinese]旁白一句。</d> while the on-screen characters' " +
				"lips remain completely closed"},
	}
	if fails := PlanFails(ValidatePlan(p)); len(fails) != 0 {
		t.Fatalf("合法的空镜/纯文生镜/t2va 不该被拒: %v", PlanIssuesText(fails))
	}
}

// TestValidatePlanVoiceoverWithoutTag → fail:有旁白却没有任何 <d> 标记。
//
// 这是"配音丢了"的形态:平台后处理补不了(它不知道该往哪插 <d>),字幕有、声音没有。
func TestValidatePlanVoiceoverWithoutTag(t *testing.T) {
	p := samplePlan()
	p.Shots[0].Dialogue = nil
	p.Shots[0].Narration = "香炉是空的。"
	p.Shots[0].H3Prompt = "subject_definitions:\n[Shot 1] the censer is empty."
	if fails := PlanFails(ValidatePlan(p)); len(fails) == 0 {
		t.Fatal("有旁白却没有任何 <d> 标记必须 fail —— 那句旁白不会被念出来")
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
