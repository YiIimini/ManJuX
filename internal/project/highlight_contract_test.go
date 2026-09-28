package project

import (
	"strings"
	"testing"
)

// highlight_contract_test.go 抓重点纪律的两道机器门（用户《一、网文小说转剧本提示词指令》）。
//
// 2026-09-28 用户：「分镜脚本 LLM 解析是否按这个来啊，抓重点啊，咋那么纯啊」。
// 那份文件里能量化的两条：
//
//	· 开篇 3 秒爆点 = 第一句极致冲突/绝境/反转/悬念，**无任何铺垫、无多余旁白**；
//	· 每 80 字 1 个小爽点、每 200 字 1 个大爽点（按台词字数累计，超阈即"这段是平的"）。
func TestHighlightOpeningNoNarration(t *testing.T) {
	mkPlan := func(nar string, vk string) *Plan {
		p := samplePlan()
		p.Shots[0].Narration = nar
		p.Shots[0].VoKind = vk
		return p
	}
	if !hasIssue(ValidatePlan(mkPlan("西坡碑林，十万块碑。", "")), "warn", "开篇镜 #1 带了旁白") {
		t.Error("首镜带旁白要报（那份文件第一条就是「无任何铺垫、无多余旁白」）")
	}
	if !hasIssue(ValidatePlan(mkPlan("", VoKindNarration)), "warn", "开篇镜 #1 带了旁白") {
		t.Error("vo_kind=narration 也算旁白，同样要报")
	}
	if hasIssue(ValidatePlan(mkPlan("", "")), "warn", "开篇镜 #1 带了旁白") {
		t.Error("首镜没有旁白时不该报（误拦比漏拦更糟）")
	}
}

func TestHighlightPayoffDensity(t *testing.T) {
	// 造一段 3 镜、台词堆到 200 字以上、且没有 reveal/reversal/climax 的"流水账"
	p := samplePlan()
	long := strings.Repeat("我记着这笔账。", 15) // 每句 7 字 × 15 ≈ 105 字
	p.Shots = []Shot{
		mkShot(1, "中景", 6, "setup", long),
		mkShot(2, "中景", 6, "expression-beat", long),
		mkShot(3, "中景", 6, "tender", long),
	}
	if !hasIssue(ValidatePlan(p), "warn", "没有强节拍") {
		t.Error("连续 200 字以上没有强节拍要报（每 200 字必须有一个大爽点）")
	}
	// 中间插一个 reversal 就该把累计清零、不再报
	p.Shots[1].HookType = "reversal"
	if hasIssue(ValidatePlan(p), "warn", "没有强节拍") {
		t.Error("有 reversal 之后不该报 —— 强节拍就是用来清零的")
	}
}

func mkShot(id int, size string, dur int, hook, line string) Shot {
	return Shot{
		ID: id, Scene: "scene_room", Characters: []string{"char_a"},
		ShotSize: size, Duration: dur, Mode: "ref2va", HookType: hook,
		Action:   "他站着",
		EndState: "他站在供桌前，视线朝左前方",
		Dialogue: []Line{{Speaker: "阿甲", Text: line}},
		H3Prompt: compliantH3("detailed_description:\n" + SampleStyleSentence + "\n[Shot " +
			"1] he stands."),
	}
}

// TestHighlightOneTakeGate 片段内切镜门：超过单镜上限（PlanOneTakeSeconds）还只有一个 [Shot 1] ⇒ 报"一镜到底"。
//
// 依据（用户 2026-09-28：「难怪渲染的视频总感觉拖沓，都没抓重点」）：
// 实测每镜正文只有一个 [Shot 1]，一个 6-8 秒片段从头到尾演一个连续动作 = 全程慢动作。
// 用户自己的《【额外】仿真人10S分镜头指令》要求片段内切 2-4 个小镜头、每镜 1.5-4 秒。
func TestHighlightOneTakeGate(t *testing.T) {
	p := samplePlan()
	if hasIssue(ValidatePlan(p), "warn", "一镜到底") {
		t.Fatal("夹具本身就该是切过镜的，不该先报自己")
	}
	// 把第二刀拿掉 → 立刻变成"一镜到底"
	p.Shots[0].H3Prompt = strings.Replace(p.Shots[0].H3Prompt, "\n[Shot 2] At 00:02.500", "", 1)
	if !hasIssue(ValidatePlan(p), "warn", "一镜到底") {
		t.Error("超过单镜上限还只有一个镜头要报（切镜是节奏，一镜到底就是拖）")
	}
	// **恰好在分界上**也要报：用户文件《【额外】仿真人10S分镜头指令》写的是
	// 「每镜 1.5–4 秒」—— 4 秒时还只有一刀，单镜就已经到上限了，再长就越界。
	// （2026-09-28 校正：此前分界是 5 秒，比用户文件松，已按出处改成 4。）
	p2 := samplePlan()
	p2.Shots[0].H3Prompt = strings.Replace(p2.Shots[0].H3Prompt, "\n[Shot 2] At 00:02.500", "", 1)
	p2.Shots[0].Duration = PlanOneTakeSeconds
	if !hasIssue(ValidatePlan(p2), "warn", "一镜到底") {
		t.Errorf("时长正好等于分界(%d 秒)且只有一刀时要报", PlanOneTakeSeconds)
	}
	// 短于分界的单动作片段一镜到底不算拖
	p3 := samplePlan()
	p3.Shots[0].H3Prompt = strings.Replace(p3.Shots[0].H3Prompt, "\n[Shot 2] At 00:02.500", "", 1)
	p3.Shots[0].Duration = PlanOneTakeSeconds - 1
	if hasIssue(ValidatePlan(p3), "warn", "一镜到底") {
		t.Errorf("短于分界(%d 秒)的单动作片段不该报", PlanOneTakeSeconds)
	}
}

// TestH3ClipCuts 内部切镜的计数口径：只在 detailed_description 段内数。
//
// 第一版按全文数 `[Shot` 得出"48/48 镜多段"的假结论（retention_analysis 里的
// "appears in [Shot 1]" 被算了进去），当场被自己推翻 —— 这里把口径钉死。
func TestH3ClipCuts(t *testing.T) {
	one := "subject_definitions:\n- <Subject 1> (appears in [Shot 1]): x\n\ndetailed_description:\n[Shot 1] he stands."
	if got := H3ClipCuts(one); got != 1 {
		t.Errorf("只有 [Shot 1] 时应为 1（retention 里的引用不算），实际 %d", got)
	}
	two := strings.Replace(one, "[Shot 1] he stands.", "[Shot 1] he stands.\n[Shot 2] At 00:02.500, closer.", 1)
	if got := H3ClipCuts(two); got != 2 {
		t.Errorf("两刀应为 2，实际 %d", got)
	}
}
