package project

import (
	"strings"
	"testing"
)

// empty_body_test.go 空正文装配点兜底的判据（2026-10-08）。
//
// 夹具**照真机形态抄**：`data/projects/我捡的团子，暖了三千人的船/…第01集…/plan/plan.json`
// 的镜 12 —— 场次 / 人物 / 走位 / 景别 / 机位 / 台词（两句，`os:true`）/ 时长全在，
// `action` 与 `h3_prompt` 是空的；渲染入口的致命门就是被这一镜拦住的
// （`[render][error] ⛔ [fatal] shots[11](id=12): h3_prompt 为空`）。
// 走位表的键是**中文名字**、`characters` 里是 **id** —— 这个形态也照抄（`posOf` 两种都要认）。

const realStyleSentence = "Cinematic, 3D CG, semi-realistic Guofeng oriental star-sea look: " +
	"shaded brow bones, a bright glint at the eye corners, deep shadow on skin, " +
	"frost-white and bronze-jade palette struck through with warm amber where the flame bursts."

func realBodyPlan() *Plan {
	return &Plan{
		Title: "我捡的团子，暖了三千人的船·第01集·霜里刨出个活物",
		Style: "半写实绘画感的东方星海国风3D CG：眉骨带阴影、眼尾有光、皮肤留暗部，" +
			"霜白与青铜玉的豆青作冷调主色，只有火起处转暖橙。",
		Characters: []Character{
			{ID: "char_jiangwanzhou", Name: "姜晚舟",
				Description: "二十出头，中等身量，手劲比同龄人大，右肩比左肩低一点。鹅蛋脸，下颌线利落；" +
					"杏眼，瞳色深褐，眼尾有光，柳叶眉，抬眉时眉峰挑得高；鼻梁直、鼻头圆。",
				Voice: "A low, slightly hoarse young female voice, clipped and practical, " +
					"that warms a half-step only when she speaks to the small creature."},
			{ID: "char_xiaodeng", Name: "小灯",
				Description: "七岁男童，瘦小，肩膀窄，跑起来往前倾。圆润苹果脸，额宽下巴圆；圆眼，瞳色黑，" +
					"眼白亮，野生平眉；鼻梁低平，嘴唇薄，笑起来缺一颗门牙。",
				Voice: "A bright, fast, unbroken boy's voice that tumbles over its own words."},
			{ID: "char_atang", Name: "阿糖",
				Description: "兽形青玉玉兽，母，兽龄幼崽。巴掌大，四足短腿，圆滚一团，抱起来比看上去更沉。",
				Voice:       "No speech — tiny high chirring breaths and a soft frost-crackle purr."},
		},
		Scenes: []Scene{
			{ID: "scene_old_hold", Name: "冰星老舱·青铜玉炉厅",
				Description: "三千年古船老舱内部，弧形的橡木船肋与舱顶被厚霜覆住，舱壁嵌着压了三千年的老玉板。" +
					"舱尾立着一人高的青铜玉炉：圆腹三足，腹板是整片老玉，炉沿旧铜边被熏得发黑。",
				LightState: "黎明前至清晨，主光为舱盖破缝漏进的冷蓝侧光（画面左上方）。"},
		},
		Shots: []Shot{
			// 镜 11：正常的兄弟镜（合规六段式）—— 兜底正文的风格句从它这儿取。
			{ID: 11, Scene: "scene_old_hold", ShotSize: "中景", Duration: 8,
				Camera: "the camera tilts up slowly with small amplitude as the flame bursts, then holds",
				H3Prompt: "subject_definitions:\n<Subject 1> is 曹锦, the forty-four-year-old quartermaster.\n" +
					"The scene is the LAST attached picture: 冰星老舱·青铜玉炉厅.\n\n" +
					"summary:\n[reference generation] The furnace hall erupts.\n\n" +
					"retention_analysis:\n<Subject 1> (appears in [Shot 1]): fully_preserved.\n\n" +
					"detailed_description:\n" + realStyleSentence + "\n\n" +
					"[Shot 1] Medium shot past the furnace base.\n\n" +
					EmptyBodyExclusion + " " + EmptyBodyGuard + "\n\n" +
					"overall_soundscape:\nA heavy whoomph then a wide steady roar.\n\n" +
					"non_diegetic_music:\nN/A\n"},
			// 镜 12：**故障现场**（照 plan.json 抄；h3_prompt 与 action 都是空的）。
			{ID: 12, Scene: "scene_old_hold",
				Characters: []string{"char_jiangwanzhou", "char_xiaodeng", "char_atang"},
				Positions: map[string]string{
					"姜晚舟": "画面左三分之一、前景、面朝右下，双手圈着怀里的白绒团",
					"小灯":  "画面右三分之一、前景、面朝左，双手举着木鸟",
					"阿糖":  "画面中央、前景，白绒团在姜晚舟臂弯里按着木鸟",
				},
				Offscreen: map[string]string{"曹锦": "退到舱门门槛外，与两名队员在画外翻册子"},
				ShotSize:  "近景",
				Camera:    "Static Shot with small amplitude at slow speed; a slight push in as the bird is pinned",
				Action:    "",
				Dialogue: []Line{
					{Speaker: "小灯", Text: "师姐，她叫什么？", OS: true},
					{Speaker: "姜晚舟", Text: "没名。", OS: true},
				},
				Duration: 4,
				VoKind:   "speech",
			},
			// 镜 13：**什么材料都没有**（没场次、没人物、没台词）—— 不许硬编，留给致命门如实拦。
			{ID: 13, Duration: 6},
		},
	}
}

// TestFillEmptyShotBodiesFixesTheRealShot12 真机镜 12 必须被补上，且补出来的正文要过**全部**格式判据。
func TestFillEmptyShotBodiesFixesTheRealShot12(t *testing.T) {
	p := realBodyPlan()
	if ids := EmptyPromptShotIDs(p); len(ids) != 2 {
		t.Fatalf("夹具前提：空正文的应该是镜 12 与 13，实得 %v", ids)
	}
	if v := H3FormatViolations(p.Shots[0].H3Prompt); len(v) > 0 {
		t.Fatalf("夹具前提：兄弟镜的正文本身要合规，实得 %v", v)
	}
	n, ids := FillEmptyShotBodies(p)
	if n != 1 || len(ids) != 1 || ids[0] != 12 {
		t.Fatalf("只该补镜 12（镜 13 没材料不该硬编），实得 n=%d ids=%v", n, ids)
	}
	if got := p.FilledBodyShots; len(got) != 1 || got[0] != 12 {
		t.Errorf("补过的镜号要记在 Plan.FilledBodyShots 上（阶段据此如实播报），实得 %v", got)
	}
	sh := p.Shots[1]
	if strings.TrimSpace(sh.H3Prompt) == "" {
		t.Fatal("镜 12 的正文还是空的 —— 渲染入口的致命门会照旧拦住整条产线")
	}
	// ① 格式判据（六段/风格句位置/首镜无时间戳/严格递增/守卫句/排除句/保留标记/编号连号…）
	if v := H3FormatViolations(sh.H3Prompt); len(v) > 0 {
		t.Errorf("补出来的正文必须过全部格式判据，实得违规：%v", v)
	}
	// ② 那句致命门判据本身（空提示词门与渲染入口同一条）
	if ids := EmptyPromptShotIDs(p); len(ids) != 1 || ids[0] != 13 {
		t.Errorf("补完之后只该剩镜 13 是空的，实得 %v", ids)
	}
	// ③ 台词逐句、逐字在 <d> 里各一次；`<d>` 条数 = 这一镜该说的句数（adoptRepaired 用的同一条判据）
	want := PlanSpokenTexts(&sh)
	if len(want) != 2 {
		t.Fatalf("夹具前提：镜 12 计划里有两句，实得 %d", len(want))
	}
	if got := strings.Count(sh.H3Prompt, "<d>"); got != len(want) {
		t.Errorf("<d> 条数要与计划里的句数一致（%d），实得 %d", len(want), got)
	}
	for _, line := range want {
		if got := strings.Count(sh.H3Prompt, "<d>[Chinese]"+line+"</d>"); got != 1 {
			t.Errorf("台词必须逐字进 <d>[Chinese]…</d> 且只一次：%q 实得 %d 次", line, got)
		}
	}
	// ④ 人物/场景/走位/机位都要写上（材料是已知量，缺一项就等于让模型自己编）
	for _, want := range []string{"姜晚舟", "小灯", "阿糖", "冰星老舱·青铜玉炉厅", "近景",
		"画面左三分之一、前景、面朝右下，双手圈着怀里的白绒团", "Static Shot with small amplitude"} {
		if !strings.Contains(sh.H3Prompt, want) {
			t.Errorf("补出来的正文里缺 %q —— 这一项是方案自己记着的材料，不该丢", want)
		}
	}
	// ⑤ 风格句取自兄弟镜（同一套画风），而不是另编一句
	if !strings.Contains(sh.H3Prompt, realStyleSentence) {
		t.Error("风格句必须取自同集兄弟镜（否则这一镜的画风与其它镜不是一套）")
	}
	// ⑥ 内切结构与正文一致：`CutIssues` 那几条（每刀 1.5–4 秒/首刀为 0/之和 = 时长）不许报它
	if cut := ParseCutsFromPrompt(sh.H3Prompt); len(cut) != 2 ||
		cut[0].At != 0 || cut[1].At != 2 {
		t.Errorf("正文里的内切要与时长对得上（4 秒 ⇒ 0 / 2.0），实得 %+v", cut)
	}
	if len(sh.Cuts) != 2 || sh.Cuts[0].At != 0 || sh.Cuts[1].At != 2 {
		t.Errorf("结构字段要一并回填（界面据此显示切了几刀），实得 %+v", sh.Cuts)
	}
	for _, issue := range CutIssues(p) {
		if strings.Contains(issue, "#12") {
			t.Errorf("补出来的内切结构必须自己合规，实得：%s", issue)
		}
	}
	// ⑦ 幂等：再跑一次不再动它（正文非空的镜一个字都不碰）
	body := sh.H3Prompt
	if n2, _ := FillEmptyShotBodies(p); n2 != 0 {
		t.Errorf("幂等：第二次不该再补，实得 %d", n2)
	}
	if p.Shots[1].H3Prompt != body {
		t.Error("幂等：补好的正文不该被改写")
	}
}

// TestFillEmptyShotBodiesSkipsShotsWithoutMaterial 材料真的什么都没有时不硬编
// （那等于替编剧新编一镜，是创作不是兜底）—— 留给渲染入口的致命门如实拦。
func TestFillEmptyShotBodiesSkipsShotsWithoutMaterial(t *testing.T) {
	p := realBodyPlan()
	p.Shots = []Shot{{ID: 13, Duration: 6}}
	n, ids := FillEmptyShotBodies(p)
	if n != 0 || len(ids) != 0 {
		t.Fatalf("没有场次/人物/台词的镜不该被拼正文，实得 n=%d ids=%v", n, ids)
	}
	if p.Shots[0].H3Prompt != "" {
		t.Error("没材料的镜必须保持空 —— 由致命门拦住并如实说")
	}
}

// TestFillEmptyShotBodiesRunsAtTheReadBoundary 兜底必须挂在**唯一读取边界**上：
// 预检 / 渲染 / 人审都走 `LoadPlan → HealPlan`，只挂生成链会让三个消费方看到不同的方案
// （2026-09-28 的教训：预检报 5 条 fail 而渲染器看到 0 条）。
func TestFillEmptyShotBodiesRunsAtTheReadBoundary(t *testing.T) {
	p := realBodyPlan()
	res := HealPlan(p)
	if res.Bodies != 1 || len(res.BodyIDs) != 1 || res.BodyIDs[0] != 12 {
		t.Fatalf("HealPlan 必须补上镜 12 并如实计数，实得 Bodies=%d BodyIDs=%v", res.Bodies, res.BodyIDs)
	}
	if strings.TrimSpace(p.Shots[1].H3Prompt) == "" {
		t.Fatal("HealPlan 走完之后镜 12 的正文不该还是空的")
	}
}

// TestMinimalCutsAreCompliantForEveryRealLength 兜底用的内切结构，对**每一种真实片段时长**
// 都要满足用户文件那几条（每刀 1.5–4 秒、首刀为 0、严格递增、各刀之和 = 片段时长、
// 4 秒以上至少 2 刀、最多 4 刀）—— 它自己不合规就等于把问题挪到下一道门。
func TestMinimalCutsAreCompliantForEveryRealLength(t *testing.T) {
	for d := PlanMinShotSeconds; d <= PlanMaxShotSeconds; d++ {
		p := &Plan{Shots: []Shot{{ID: 1, Duration: d, Cuts: minimalCuts(d)}}}
		if issues := CutIssues(p); len(issues) > 0 {
			t.Errorf("%d 秒的兜底内切不合规：%v", d, issues)
		}
		if issues := CutCoverageIssues(p); len(issues) > 0 {
			t.Errorf("%d 秒的兜底内切覆盖不合规：%v", d, issues)
		}
		cut := ParseCutsFromPrompt(CutsToPromptSection(p.Shots[0].Cuts))
		if len(cut) != len(p.Shots[0].Cuts) {
			t.Errorf("%d 秒：时间戳写回正文再解析回来必须一致（写/读同一条格式），实得 %+v", d, cut)
		}
	}
}
