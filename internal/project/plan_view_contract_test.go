package project

import (
	"strings"
	"testing"
)

// plan_view_contract_test.go 镜头语言三件套的击穿用例
// （2026-09-27 技能侧整改单 §1.1 主视角 / §1.2 尾帧状态 / §1.5 H3 官方格式）。
//
// 与其余契约用例同一条纪律：每条规则都要有一个专门打它的用例，且**合规样本零命中**
// （见 TestValidatePlanCleanSample）—— 误拦的规则比没有规则更糟。
//
// 依据：技能侧 `references/分镜脚本-多引擎输出规范.md` §二/§六（源：官方
// `skills/h3-prompt-writing`）与 `references/漫剧指令库-外部对标.md`。

// hasIssue 问题表里是否存在 level 级、消息含 sub 的条目。
func hasIssue(issues []Issue, level, sub string) bool {
	for _, it := range issues {
		if it.Level == level && strings.Contains(it.Msg, sub) {
			return true
		}
	}
	return false
}

// viewShotPlan n 镜同场景方案：每镜都带主视角与建立镜头串（可选），只为测视角/尾帧那几维。
//
// 场景固定同一处，所以第 1 镜是建立镜头、其余不是 —— 正好覆盖两个分支。
func viewShotPlan(n int, withView bool) *Plan {
	p := samplePlan()
	p.Shots = nil
	for i := 1; i <= n; i++ {
		h3 := "subject_definitions:\n<Subject 1> is 阿甲 ...\n\ndetailed_description:\n" +
			SampleStyleSentence + "\n[Shot 1] he walks. "
		if withView {
			h3 += ViewAerial45 + ". "
		}
		h3 += "<d>[Chinese]我准了。</d>"
		sh := Shot{
			ID: i, Scene: "scene_room", Characters: []string{"char_a"},
			ShotSize: "中景", Camera: "slow push-in", Action: "他往前走",
			Dialogue: []Line{{Speaker: "阿甲", Text: "我准了。"}},
			Duration: 5, Mode: "ref2va",
			EndState: "他站在门槛前，右手扶门框，视线朝院中",
			H3Prompt: h3,
		}
		if i == 1 {
			sh.H3Prompt += " " + PlanEstablishingExtra + "."
		}
		p.Shots = append(p.Shots, sh)
	}
	return p
}

// TestValidatePlanCatchesMissingViewpoint 每镜的提示词里必须有官方 9 视角词表里的串。
func TestValidatePlanCatchesMissingViewpoint(t *testing.T) {
	// 反向：合规（每镜都有视角串）→ 不该报
	if issues := ValidatePlan(viewShotPlan(3, true)); hasIssue(issues, "warn", "找不到机位视角串") {
		t.Fatalf("每镜都有视角串时不该报缺视角：%v", PlanIssuesText(issues))
	}
	// 正向：全都没有 → 一条聚合 warn，且带镜号与词表提示
	issues := ValidatePlan(viewShotPlan(3, false))
	if !hasIssue(issues, "warn", "找不到机位视角串") {
		t.Fatalf("三镜都没有视角串必须报，实际：%v", PlanIssuesText(issues))
	}
	if !hasIssue(issues, "warn", ViewAerial45) {
		t.Error("报错文案里必须贴出官方词表（否则用户不知道该写什么）")
	}
}

// TestValidatePlanCatchesMonotoneViewpoint 视角单调：≥10 镜却只有 <6 种机位。
func TestValidatePlanCatchesMonotoneViewpoint(t *testing.T) {
	p := viewShotPlan(12, true) // 12 镜全是同一种视角
	if !hasIssue(ValidatePlan(p), "warn", "只用了") {
		t.Fatalf("12 镜只用 1 种视角必须报视角单调：%v", PlanIssuesText(ValidatePlan(p)))
	}
	// 铺开到 6 种 → 不该报
	views := PlanViews[:6]
	for i := range p.Shots {
		p.Shots[i].H3Prompt = strings.Replace(p.Shots[i].H3Prompt, ViewAerial45, views[i%len(views)], 1)
	}
	if hasIssue(ValidatePlan(p), "warn", "只用了") {
		t.Error("铺开到 6 种视角后不该再报单调")
	}
}

// TestValidatePlanViewFieldMustMatchPrompt view 字段与提示词必须一致 —— 否则自检面板在骗人。
func TestValidatePlanViewFieldMustMatchPrompt(t *testing.T) {
	p := viewShotPlan(2, true)
	p.Shots[0].View = ViewAerial45 // 与提示词一致
	if hasIssue(ValidatePlan(p), "warn", "view 字段写的是") {
		t.Fatal("字段与提示词一致时不该报")
	}
	p.Shots[0].View = ViewTelephoto // 提示词里仍是 ViewAerial45
	if !hasIssue(ValidatePlan(p), "warn", "view 字段写的是") {
		t.Fatalf("字段与提示词不一致必须报：%v", PlanIssuesText(ValidatePlan(p)))
	}
	p.Shots[0].View = "bird eye view" // 不在官方词表里
	if !hasIssue(ValidatePlan(p), "warn", "不是官方 9 视角词表里的串") {
		t.Fatalf("自造视角串必须报：%v", PlanIssuesText(ValidatePlan(p)))
	}
}

// TestValidatePlanEstablishingShot 建立镜头：必须有建立镜头串，且不许用特写。
func TestValidatePlanEstablishingShot(t *testing.T) {
	// 正向：首镜没有建立镜头串
	p := viewShotPlan(3, true)
	p.Shots[0].H3Prompt = strings.Replace(p.Shots[0].H3Prompt, PlanEstablishingExtra, "", 1)
	if !hasIssue(ValidatePlan(p), "warn", "建立镜头串") {
		t.Fatalf("建立镜头缺串必须报：%v", PlanIssuesText(ValidatePlan(p)))
	}
	// 正向：建立镜头用了特写
	p = viewShotPlan(3, true)
	p.Shots[0].ShotSize = "特写"
	if !hasIssue(ValidatePlan(p), "warn", "建立镜头用了特写") {
		t.Fatalf("特写当建立镜头必须报：%v", PlanIssuesText(ValidatePlan(p)))
	}
	// 反向：同一场景的第二镜不是建立镜头，不必带串、也不必是远景
	p = viewShotPlan(3, true)
	p.Shots[1].ShotSize = "特写"
	p.Shots[1].H3Prompt = strings.Replace(p.Shots[1].H3Prompt, PlanEstablishingExtra, "", 1)
	if hasIssue(ValidatePlan(p), "warn", "建立镜头") {
		t.Fatalf("非建立镜头不该被建立镜头规则命中：%v", PlanIssuesText(ValidatePlan(p)))
	}
	// 场景变化处重新算建立镜头
	p = viewShotPlan(3, true)
	p.Scenes = append(p.Scenes, Scene{ID: "scene_road", Name: "官道", ImagePrompt: "a dirt road ..."})
	p.Shots[2].Scene = "scene_road"
	p.Shots[2].H3Prompt = strings.Replace(p.Shots[2].H3Prompt, PlanEstablishingExtra, "", 1)
	if !hasIssue(ValidatePlan(p), "warn", "建立镜头串") {
		t.Fatalf("换场景后的首镜也是建立镜头，必须报：%v", PlanIssuesText(ValidatePlan(p)))
	}
}

// TestValidatePlanEndState 尾帧状态：同场景有后继的镜必须写，且不许写听觉/氛围词。
func TestValidatePlanEndState(t *testing.T) {
	// 正向：中间那镜缺尾帧状态
	p := viewShotPlan(3, true)
	p.Shots[1].EndState = ""
	if !hasIssue(ValidatePlan(p), "warn", "缺 end_state") {
		t.Fatalf("同场景有后继却缺尾帧状态必须报：%v", PlanIssuesText(ValidatePlan(p)))
	}
	// 反向：每段场景的末镜（后面换了场景）允许不写
	p = viewShotPlan(2, true)
	p.Scenes = append(p.Scenes, Scene{ID: "scene_road", Name: "官道", ImagePrompt: "a dirt road ..."})
	p.Shots[0].EndState = ""
	p.Shots[1].Scene = "scene_road"
	p.Shots[1].EndState = ""
	if hasIssue(ValidatePlan(p), "warn", "缺 end_state") {
		t.Fatalf("跨场景处不要求尾帧状态，不该报：%v", PlanIssuesText(ValidatePlan(p)))
	}
	// 正向：听觉/氛围词
	p = viewShotPlan(2, true)
	p.Shots[0].EndState = "他站在门口，周围一片寂静，气氛压抑"
	issues := ValidatePlan(p)
	if !hasIssue(issues, "warn", "听觉/氛围") {
		t.Fatalf("尾帧状态里的氛围词必须报：%v", PlanIssuesText(issues))
	}
	if !hasIssue(issues, "warn", "寂静") {
		t.Error("报错文案里要点出命中的词（否则不知道该删哪几个）")
	}
}

// TestValidatePlanConsecutiveShotSize 连续同景别上限（整改单 §1.1 验收：≤6 镜）。
func TestValidatePlanConsecutiveShotSize(t *testing.T) {
	if hasIssue(ValidatePlan(viewShotPlan(6, true)), "warn", "连续") {
		t.Fatal("连续 6 镜同景别正好在上限内，不该报")
	}
	issues := ValidatePlan(viewShotPlan(7, true))
	if !hasIssue(issues, "warn", "连续 7 镜同一个景别") {
		t.Fatalf("连续 7 镜必须报：%v", PlanIssuesText(issues))
	}
	// 中间断开就不算连续
	p := viewShotPlan(7, true)
	p.Shots[3].ShotSize = "近景"
	if hasIssue(ValidatePlan(p), "warn", "连续") {
		t.Fatalf("中间断开后不该再报连续：%v", PlanIssuesText(ValidatePlan(p)))
	}
}

// TestStripCJK 场景/画面提示词的汉字清洗：整句丢弃优先，整段中文退回逐字删除。
func TestStripCJK(t *testing.T) {
	cases := []struct {
		name, in   string
		noHan      bool
		allowEmpty bool
		wantSub    string
	}{
		{
			"中文名开头的卡片（技能侧实测形态）",
			"临水城前街（口径锁定正名表里的地点）. Photorealistic 3D cinematic environment plate: a wet street.",
			true, false, "Photorealistic 3D cinematic environment plate",
		},
		{
			// 整段中文：清完什么都不剩 → 返回空串（调用方的取景/风格块照常拼上），
			// **绝不**退回原文 —— 留着汉字等于让 T2I 把乱码字形画到环境板上，而这块板每镜都带着。
			"整段中文（清空，不退原文）",
			"雨后的青石长街，木牌楼歪着，灯笼还亮着。",
			true, true, "",
		},
		{
			"全英文卡片原样返回",
			"a ruined temple at dusk, moss on the eaves, plain background",
			false, false, "a ruined temple at dusk",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StripCJK(tc.in)
			if tc.noHan && CountCJK(got) != 0 {
				t.Errorf("应清到 0 个汉字，实际 %d：%q", CountCJK(got), got)
			}
			if tc.wantSub != "" && !strings.Contains(got, tc.wantSub) {
				t.Errorf("英文正文应保留（含 %q），实际 %q", tc.wantSub, got)
			}
			if !tc.allowEmpty && strings.TrimSpace(got) == "" && strings.TrimSpace(tc.in) != "" {
				t.Error("不该把提示词清成空串")
			}
			if again := StripCJK(got); again != got {
				t.Errorf("清洗必须幂等：一次 %q / 二次 %q", got, again)
			}
		})
	}
	// 英文正文里的破折号/花引号是正常标点，不许被当成中文标点删掉
	in := "the same layout — the same architecture, “as written”"
	if got := StripCJK(in); got != in {
		t.Errorf("全英文（含破折号与花引号）应原样返回，实际 %q", got)
	}
}

// TestValidatePlanSceneCardCJK 场景板（T2I）汉字门：出现任何汉字即提示，**不扣主体名**。
//
// 与 h3_prompt 那条门的关键差别就在"扣不扣名字"：场景卡的 image_prompt 里没有
// "必须写中文名"的理由（渲染期点名靠英文结构与 PICTURE MANIFEST），所以一个汉字都不该有。
func TestValidatePlanSceneCardCJK(t *testing.T) {
	p := samplePlan()
	if hasIssue(ValidatePlan(p), "warn", "场景板是 T2I") {
		t.Fatalf("全英文场景卡不该报：%v", PlanIssuesText(ValidatePlan(p)))
	}
	p.Scenes[0].ImagePrompt = "临水城（口径锁定正名表里的地点）. Photorealistic 3D cinematic environment plate."
	issues := ValidatePlan(p)
	if !hasIssue(issues, "warn", "场景板是 T2I") {
		t.Fatalf("场景卡带汉字必须报：%v", PlanIssuesText(issues))
	}
	if !hasIssue(issues, "warn", "14 个汉字") {
		t.Errorf("要报出汉字个数（临水城 3 + 括号里 11 = 14），实际：%v", PlanIssuesText(issues))
	}
}

// TestH3FormatViolations 官方格式的三条可机检判据（§1.5）。
func TestH3FormatViolations(t *testing.T) {
	ok := "subject_definitions:\n<Subject 1> is 阿甲 ...\n\nretention_analysis:\n- <Picture 1>: fully_preserved - 阿甲 ...\n\n" +
		"detailed_description:\n" + SampleStyleSentence + "\n[Shot 1] he stands. [Shot 2] At 00:06.00 he walks.\n\n" +
		"overall_soundscape:\nquiet room tone\n\nnon_diegetic_music:\nN/A"
	if got := H3FormatViolations(ok); len(got) != 0 {
		t.Fatalf("合规提示词不该报，实际：%v", got)
	}
	cases := []struct {
		name, h3, wantSub string
	}{
		{
			"retention_analysis 里写了 (Sx)",
			strings.Replace(ok, "- <Picture 1>:", "- <Subject 1> (S1) <Picture 1>:", 1),
			"说话人标记 (S1)",
		},
		{
			"首镜带时间戳",
			strings.Replace(ok, "[Shot 1] he stands.", "[Shot 1] At 00:00.00 he stands.", 1),
			"首镜无时间戳",
		},
		{
			"时间戳形态不对",
			strings.Replace(ok, "At 00:06.00", "At 6s", 1),
			"形态不对",
		},
		{
			"风格句写在首镜里（前面是空的）",
			strings.Replace(ok, SampleStyleSentence+"\n", "", 1),
			"之前是空的",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := H3FormatViolations(tc.h3)
			hit := false
			for _, s := range got {
				if strings.Contains(s, tc.wantSub) {
					hit = true
				}
			}
			if !hit {
				t.Fatalf("应命中 %q，实际：%v", tc.wantSub, got)
			}
		})
	}
}

// TestValidatePlanAggregatesH3Format 契约把格式问题**聚合**成一条 warn（不逐镜刷屏）。
func TestValidatePlanAggregatesH3Format(t *testing.T) {
	p := viewShotPlan(3, true)
	for i := range p.Shots {
		p.Shots[i].H3Prompt = strings.Replace(p.Shots[i].H3Prompt,
			"detailed_description:", "retention_analysis:\n- <Subject 1> (S1) <Picture 1>: fully_preserved\ndetailed_description:", 1)
	}
	issues := ValidatePlan(p)
	if !hasIssue(issues, "warn", "不符合官方格式") {
		t.Fatalf("三镜都有格式问题必须报：%v", PlanIssuesText(issues))
	}
	if !hasIssue(issues, "warn", "3 镜的 h3_prompt 不符合官方格式") {
		t.Error("聚合条要报出命中镜数")
	}
}
