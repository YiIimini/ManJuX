package project

import (
	"strings"
	"testing"
	"unicode/utf8"
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
		// 核心提炼：真实方案必有（用户口径"抓重点"），夹具也一样 —— 没有会被 plan.core 门抓住。
		CoreBeats: []string{
			"核心冲突：他被当作死人写进了碑册",
			"爽点①：他当着碑奴的面把名字从碑上刮掉（reveal）",
			"爽点②：他逼监工认账（reversal）",
		},
		Style: "PHOTOREAL 3D WORLD, SEMI-STYLISED FACES",
		Characters: []Character{{
			ID: "char_a", Name: "阿甲", Kind: "human",
			Description: "二十岁男子", Voice: "young male voice",
			// #21/#24 的门要求：角色卡带**年龄锚**、三视图板写**纯白无缝底**（用户《三》
			// 「白底」，以及"中年人渲染成小年轻"那条）。夹具照真实形态写。
			Age:         "20",
			BoardPrompt: "a character production reference sheet on a pure white seamless backdrop: three full-body views, six head angles, six detail tiles",
			// 夹具照真实形态写：规范要求角色卡带比例锚点（8.5 头身），真实卡会有；
			// 没有就会被"卡完备性"门抓住（不是门太严，是夹具不真）。
			ImagePrompt: "IDENTITY LOCK (do not change): a young man, eight and a half heads tall, lean, " +
				"straight-backed; narrow eyes, a small scar over the left brow.",
		}},
		Scenes: []Scene{{
			ID: "scene_room", Name: "荒山破庙·雨夜", Description: "潮湿的破庙，空气里有股压抑的静",
			// 夹具照真实形态写：用户《四》:63「四大要素：环境类型、具体时间、空间氛围、视觉主要特征」
			// —— 场景卡缺了「空间氛围」时新门会（正确地）报它，所以这里照实写进去。
			ImagePrompt: "ULTRA-WIDE 45-DEGREE AERIAL VIEW of an abandoned hill temple courtyard at dead-gold dusk: ultra-wide angle, grand scale, epic wide shot, vast space, distant horizon, cinematic establishing shot; wet flagstones, a fallen ridge beam, distant blue-grey mountain ridges beyond the courtyard wall; a hush of damp air and an atmosphere of quiet dread; the last sun lies in long bars across the wet stone, dusk running down the rough wall, cold light talking to damp moss; two groups of fixed furnishings — a stone censer and a leaning pillar base in the midground, scattered tiles and a dead cypress at the edge; pre-event timeless location, no blood, no corpses, no fresh aftermath, no dropped props, no event aftermath, no humans, empty, landscape only, UE5 render, 8k, ultimate detail",
			Landmarks:   "门槛与东侧立柱", LightState: "黄昏，主光从门洞斜射",
		}},
		// 关键道具：2026-09-28 起"一件道具都没有"会报警（实测那一集 25 镜提到器物、0 张卡 ⇒ 漂移）。
		// 夹具照**真实形态**写：一部戏总有几件反复出现的器物，样本里也得有。
		Props: []Prop{{
			ID: "prop_knife", Name: "旧柴刀", EN: "a short worn chopping knife with a hemp-wrapped handle",
			// 用户《二》要求**八项**，「功能作用」与「独有特征」是其中两项 ——
			// 合规样本必须写全，否则新的"道具八项"门会（正确地）报它不完整。
			Description: "磨薄的旧柴刀，柄缠麻布；功能作用：劈柴与防身，砍不动硬木；独有特征：刀背有一道豁口",
			// #23：道具图是拿卡当提示词单独出的，材质单薄就是"一块塑料" —— 夹具写够 3 种材质
			ImagePrompt: "a short worn chopping knife: aged iron blade with a chipped spine, hemp-wrapped wooden handle, " +
				"worn leather cord binding, 1:1 square, isolated on plain white background",
		}},
		Shots: []Shot{{
			ID: 1, Scene: "scene_room", Characters: []string{"char_a"}, PropIDs: []string{"prop_knife"},
			// 夹具照真实形态写：官方分镜表要求每个出场角色都有画面位置（Character Positions）
			Positions: map[string]string{"char_a": "画面右三分之一、前景、面朝门外"},
			ShotSize:  "中景", Camera: "slow push-in", Action: "他把柴刀搁在供桌上，站起来",
			// 角度词必填（用户《五》：「角度**必填**」）—— 夹具照真实形态写，
			// 缺这一栏时新的那条门会（正确地）报它。
			AngleZH:  "平视，镜头缓缓推进",
			View:     ViewAerial45,
			EndState: "他站在供桌前，右手抬起指向门外，视线朝左前方，地面水渍未干",
			HookType: "reveal",
			Dialogue: []Line{{Speaker: "阿甲", Text: "我准了。"}},
			Duration: 5, Mode: "ref2va",
			H3Prompt: compliantH3("subject_definitions:\n<Subject 1> is 阿甲 ...\n\ndetailed_description:\n" +
				SampleStyleSentence + "\n[Shot 1] he stands up under " + ViewAerial45 + "; " +
				PlanEstablishingExtra + ".\n[Shot 2] At 00:02.500, the camera pushes in tighter on the blade " +
				"as he sets it down and straightens up; his hands stay readable..<d>[Chinese] 我准了。</d>"),
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
// compliantH3 把夹具的提示词补成**真实形态**的官方格式（六段齐、亮度守卫、排除句、保留标记…）。
//
// 为什么必须补全（2026-09-27）：输出门扩到"六段齐与顺序／亮度守卫／排除句／保留标记／声音段句数"
// 之后，原先那些只有一两段的缩水夹具**全都会被抓**——这不是门太严，是夹具不像真东西
// （本仓的老规矩：夹具必须照真实形态写，干净的夹具会给出假的绿灯）。
func compliantH3(body string) string {
	// **内部切镜必须无条件补**：原来这段只在"body 里还没有 detailed_description"时才生效，
	// 而 `samplePlan` 恰好含 `detailed_description:` —— 于是那个 5 秒的夹具**一直是"一镜到底"**，
	// 直到「片段 → 小镜头」两层结构的门把它咬出来（`CutCoverageIssues` 报「5 秒 0 刀」）。
	// 夹具必须照真实形态写：用户文件要求 4 秒以上的片段切 2–4 刀、每刀 1.5–4 秒。
	// 缺 `detailed_description` 时**插在第一个 `[Shot]` 之前**（官方六段顺序固定，
	// 追加到末尾就是段序错）。这一项**独立判断**，与"有没有 [Shot 1]"无关 ——
	// 嵌套进下面那个分支会让"自带 [Shot 1] 但缺段"的夹具漏补（当天就栽过）。
	if !strings.Contains(body, "detailed_description:") && !strings.Contains(body, "integrated_multimodal_description:") {
		if i := strings.Index(body, "[Shot "); i >= 0 {
			body = body[:i] + "detailed_description:\n" + body[i:]
		} else {
			body += "\n\ndetailed_description:"
		}
	}
	// **内部切镜要分别检查两镜**：
	//   · body 里一个 `[Shot]` 都没有 → 补两镜；
	//   · body 已有 `[Shot 1]`（多数夹具就是）→ **只补 `[Shot 2]`**。
	//
	// 原来这段只检查"有没有 `detailed_description`"，于是 `samplePlan`（自带 detailed_description
	// 与 [Shot 1]）从不补切镜，**一直是"一镜到底"的假夹具**；而按"只看 `[Shot 2]` 在不在"补的
	// 第一版又会在已有 `[Shot 1]` 的夹具里补出**第二个 `[Shot 1]`**（两个都从 0 开始）——
	// 两层结构的解析器判定"时间点不递增"，整份正文被弃，表现为所有夹具都读不出小镜头。
	// 教训仍是同一条：夹具要照真实形态写，"补丁式的夹具"最容易在这上面骗过所有人。
	if !strings.Contains(body, "[Shot 1]") {
		// 风格句必须在第一个 `[Shot]` **之前**（官方 §5.2：写进首镜等于只有那一镜是这部片的画风）
		if !strings.Contains(body, SampleStyleSentence) {
			body += "\n" + SampleStyleSentence
		}
		body += "\n[Shot 1] " + ViewAerial45 + "; " + PlanEstablishingExtra + "."
	}
	if !strings.Contains(body, "[Shot 2]") {
		// 基线也照官方 §4.2 写：只改距离/景别用**运镜**，不用 cut
		// （规范原话：if only the distance or a slight angle needs to change, prefer CAMERA
		//  MOTION over a cut）。原来写的 `the camera cuts closer` 不在五个切镜词里，
		// 加了切镜白名单门之后当场被咬——咬得对。
		body += "\n[Shot 2] At 00:02.000, the camera pushes in closer as he lifts the blade; " +
			"a tighter frame on his hands."
	}
	if _, ok := PlanViewIn(body); !ok {
		body += "\n" + ViewAerial45 + "."
	}
	if !strings.Contains(body, PlanEstablishingExtra) {
		body += " " + PlanEstablishingExtra + "."
	}
	// 六段齐（Ref2VA）：subject_definitions / summary / retention_analysis / detailed_description
	// / overall_soundscape / non_diegetic_music，**按官方顺序**。
	if !strings.Contains(body, "summary:") {
		body = strings.Replace(body, "detailed_description:",
			"summary:\n[reference generation] A short drama beat built from the attached reference pictures.\n\n"+
				"retention_analysis:\n- <Subject 1> (appears in [Shot 1]): fully_preserved - identity, costume and marks.\n\n"+
				"detailed_description:", 1)
	}
	if !strings.Contains(body, "subject_definitions:") {
		body = "subject_definitions:\n<Subject 1> is the first listed character; its front sheet is the first attached picture.\n\n" + body
	}
	if !strings.Contains(body, "overall_soundscape:") {
		body += "\n\noverall_soundscape:\nQuiet room tone with distant rain and soft footsteps."
	}
	if !strings.Contains(body, "non_diegetic_music:") {
		body += "\n\nnon_diegetic_music:\nN/A"
	}
	// 官方强制收尾：排除句 + 亮度守卫句
	if !containsAny(strings.ToLower(body), "no watermark", "no logo", "no captions") {
		body += " No watermarks, no logos, no captions, no added lettering on clothing or props."
	}
	if !containsAny(body, "clearly visible and adequately lit") {
		body += " Subjects must remain clearly visible and adequately lit throughout the video; " +
			"avoid rendering any frame nearly black or overly dark."
	}
	// 官方 §5.2 要求正文 350–500 英文词，而"单镜也不能因此写短" ⇒ 夹具的正文也必须**像真东西**：
	// 真实的单镜正文是几百词，两三句的玩具夹具会被词数门抓住（那不是门太严，是夹具不真）。
	// 填充句**只能进描述段**（第一次写时直接 append，把配乐段顶到 13 句，被句数门抓住）；
	// 它不参与任何断言，只负责把"形状"撑对。
	filler := " The camera holds on the scene as the light shifts slowly across the stone and the dust drifts through it."
	for H3DescriptionWords(body) < H3WordsFloor+20 {
		if i := strings.Index(body, "\n\noverall_soundscape:"); i >= 0 {
			body = body[:i] + filler + body[i:]
		} else {
			body += filler
		}
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

// 角度词**必填**（用户《五》：「运镜：[运动方式] + [角度词]（**角度必填**）」）。
//
// 【为什么要这条门】`AngleWordOK` 对空值直接放行（注释写"空值合法"），于是"角度必填"
// 这条**从来没有机检** —— 分镜里这一栏大面积为空，契约一句话都不说。
// 规范那句"按 WARN，不拒收"说的是**级别**（不判 fail），不是"不报"。
func TestValidatePlanRequiresAngleWord(t *testing.T) {
	p := samplePlan()
	p.Shots[0].AngleZH = "" // 真实事故形态：这一栏空着
	issues := ValidatePlan(p)
	found := false
	for _, is := range issues {
		if strings.Contains(strings.Join(PlanIssuesText([]Issue{is}), " "), "没写机位角度词") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("镜头没写角度词时必须报出来（用户《五》要求必填），实际 issues：%v",
			PlanIssuesText(issues))
	}
	// 反过来：写了合法角度词就不该报这一条
	p2 := samplePlan()
	p2.Shots[0].AngleZH = "低角度仰拍，镜头跟随"
	for _, is := range ValidatePlan(p2) {
		if strings.Contains(strings.Join(PlanIssuesText([]Issue{is}), " "), "没写机位角度词") {
			t.Error("写了角度词还被报「没写」，是判据写反了")
		}
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
			// 60 字 ÷ 4.1 + 0.3 ≈ 14.9 秒 > 单镜上限 10 秒
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
			// 级别是 **info**：平台会确定性补齐这一句 ⇒ 用户无事可做（2026-09-28 降噪）
			level: "info", wantSub: "lips remain completely closed",
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

// TestNeededShotSeconds 台词时长预算:与 director 同规则(PlanCharsPerSecond 字/秒 + 0.3 收尾,向上取整)。
//
// 【2026-09-28 改成从常量推导】此前这里写死了 4.5 字/秒下的结果 —— 语速一改(4.5 → 4.1,
// 依据 H3 公开契约文档)测试就假红。测的应该是**公式**,不是某个常数下的数字。
func TestNeededShotSeconds(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", PlanMinShotSeconds},      // 无台词 → 下限 4
		{"我准了。", PlanMinShotSeconds},  // 4 字 ≈ 1.2 秒 → 抬到下限
		{strings.Repeat("字", 18), 5},  // 18/4.1+0.3 ≈ 4.7 → 5
		{strings.Repeat("字", 40), 11}, // 40/4.1+0.3 ≈ 10.06 → 11（不夹上限：一镜装不下要说出来）
	}
	for _, c := range cases {
		sh := Shot{Dialogue: []Line{{Speaker: "x", Text: c.text}}}
		got := NeededShotSeconds(sh)
		// 公式断言（与语速常量同源）：秒数 = 字数/语速 + 收尾，向上取整后再夹到 [下限, 上限]
		if c.text != "" {
			n := utf8.RuneCountInString(c.text)
			raw := float64(n)/PlanCharsPerSecond + PlanSpeechPadSeconds
			wantF := int(raw)
			if raw > float64(wantF) {
				wantF++ // 向上取整
			}
			if wantF < PlanMinShotSeconds {
				wantF = PlanMinShotSeconds
			}
			// **故意不夹上限**：算出 11 秒就是在告诉上层"这句台词一镜装不下"，
			// 由契约门（台词超单镜上限）去报，而不是在这里悄悄压回 10 秒把问题藏起来。
			if got != wantF {
				t.Fatalf("%q：公式应得 %d 秒，实际 %d（语速 %.1f 字/秒）", c.text, wantF, got, PlanCharsPerSecond)
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
