package director

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// realEmptyShot 真机《外卖员写了七千封回信》第01集镜头 8 的形态（2026-10-08 现场，照 plan.json 抄）：
// **结构化字段全在、只有 `h3_prompt` 是空的**，而 `dialogue` 里有两句 —— 也就是说
// "把整镜写出来"的修复**必然**要从 0 补到 2 条 `<d>`。
var realEmptyShot = project.Shot{
	ID:       8,
	Duration: 10,
	Scene:    "scene_station_office_door",
	H3Prompt: "",
	Action:   "何仕把工牌塞进兜里撂下狠话转身要走；陈声掏出手机点了三下，走廊里三十多部手机同时响，他问站长投诉的时候为什么没人看那条备注。",
	Dialogue: []project.Line{
		{Speaker: "何仕", Text: "要是热搜再来第二波，你就不用来站里了。", OS: true},
		{Speaker: "陈声", Text: "这条备注，投诉的时候你们不看？", OS: true},
	},
	VoKind: "speech",
}

// repairedPrompt 一份"把这一镜写出来"的合理产物：官方式六段 + 两刀 + 两句台词。
const repairedPrompt = `subject_definitions:
<Subject 1> is 陈声, a delivery rider in a worn blue uniform.
<Subject 2> is the corridor outside the station office.

summary:
[reference generation] Cold 3D CG in the corridor: 陈声 raises his phone.

retention_analysis:
- <Subject 1> front sheet 陈声 (used in this shot): fully_preserved - face, hair and uniform copied exactly.

detailed_description:
Cinematic, 3D CG, realistic: low-saturation cold palette, fluorescent corridor light.

[Shot 1] Medium shot down the corridor: 何仕 turns away with the badge pushed into his pocket, 陈声 lifts his phone. Static Shot; air-conditioner hum.

[Shot 2] At 00:03.500 the shot cuts closer on 陈声 as he taps three times; thirty phones ring along the corridor. The camera Push In slowly. 何仕 (S1) says off-screen: <d>[Chinese]要是热搜再来第二波，你就不用来站里了。</d> 陈声 (S2) says off-screen: <d>[Chinese]这条备注，投诉的时候你们不看？</d> No watermarks, no logos, no captions, no subtitles, no added lettering on clothing or props. Subjects must remain clearly visible and adequately lit throughout the video; avoid rendering any frame nearly black or overly dark.

overall_soundscape:
fluorescent hum, thirty ringtones starting one after another, footsteps.

non_diegetic_music:
N/A
`

// TestAdoptRepairedFillsAnEmptyPrompt 空提示词的正确修复必须被采纳 ——
// 这正是短篇那一本卡死的地方：安全阀按"台词条数不许变"（0）把补上台词的修复判成"改坏了"，
// 于是那一镜永远是空的 ⇒ 渲染被致命门拦住 ⇒ 整条产线中止。
func TestAdoptRepairedFillsAnEmptyPrompt(t *testing.T) {
	if v := project.H3FormatViolations(repairedPrompt); len(v) > 0 {
		t.Fatalf("夹具本身必须是一份合规的提示词，实得违规：%v", v)
	}
	sh := realEmptyShot
	if len(project.EmptyPromptShotIDs(&project.Plan{Shots: []project.Shot{sh}})) != 1 {
		t.Fatal("夹具前提：这一镜的 h3_prompt 应该是空的")
	}
	if !adoptRepaired(&sh, repairedPrompt) {
		t.Fatal("空提示词补上台词（0 → 计划里那两句）是**正确修复**，不该被安全阀退回")
	}
	if strings.TrimSpace(sh.H3Prompt) == "" {
		t.Fatal("采纳之后正文不该还是空的")
	}
	if n := strings.Count(sh.H3Prompt, "<d>"); n != 2 {
		t.Errorf("修好之后应有两句台词，实得 %d", n)
	}
	// 安全阀的本意不能丢：**计划外的台词**照样拒。
	extra := strings.Replace(repairedPrompt, "non_diegetic_music:", "<d>[Chinese]我没说过这句。</d>\n\nnon_diegetic_music:", 1)
	sh2 := realEmptyShot
	if adoptRepaired(&sh2, extra) {
		t.Error("凭空多出计划外的台词必须拒绝（台词是内容，不是格式）")
	}
	// 一个字都不补（还是没台词）也拒：那不是修复。
	sh3 := realEmptyShot
	if adoptRepaired(&sh3, strings.ReplaceAll(repairedPrompt, "<d>", "(")) {
		t.Error("把两句台词丢了（<d> 没了）必须拒绝")
	}
}

// TestAdoptRepairedStillGuardsExistingTags 原有那半边行为不许被改坏：
// **原文有 `<d>` 时**，条数仍然不许增删（repair_test.go 的那条守卫要照旧成立）。
func TestAdoptRepairedStillGuardsExistingTags(t *testing.T) {
	base := "summary:\nx\n\nretention_analysis:\n- <Subject 1> (appears in [Shot 1]): fully_preserved\n\n" +
		"detailed_description:\nThe style is cinematic.\n[Shot 1] he walks.<d>[Chinese]走。</d>\n\n" +
		"overall_soundscape:\nwind.\n\nnon_diegetic_music:\nN/A."
	sh := &project.Shot{ID: 1, Duration: 6, H3Prompt: base}
	withTwo := strings.Replace(base, "<d>[Chinese]走。</d>", "<d>[Chinese]走。</d> <d>[Chinese]别走。</d>", 1)
	if adoptRepaired(sh, withTwo) {
		t.Error("原文只有一句台词时，改出两句必须拒绝")
	}
	if sh.H3Prompt != base {
		t.Error("拒绝时不该改动原文")
	}
}

// TestCollectRepairableNamesEmptyShot 空提示词的镜必须进自纠名单（且在"写整镜"的指令里带上
// 时长与动作 —— 模型手上没有原文可改，只有这些线索）。
func TestCollectRepairableNamesEmptyShot(t *testing.T) {
	plan := &project.Plan{Shots: []project.Shot{realEmptyShot}}
	got := collectRepairable(plan)
	if len(got) != 1 || got[0].shotID != 8 {
		t.Fatalf("该点名 #8，实际 %+v", got)
	}
	if !strings.Contains(got[0].note, "提示词是空的") {
		t.Errorf("点名要说清是「空的」，实得 %q", got[0].note)
	}
	for _, want := range []string{"NO prompt text", "six-section", "陈声掏出手机"} {
		if !strings.Contains(got[0].fix, want) { // 动作原样带过去，模型才有内容可写
			t.Errorf("修法里缺 %q：%s", want, got[0].fix)
		}
	}
}

// TestEmptyPromptShotIDsPointsAtTheRealShot 剧本阶段的点名判据（存盘前播报用）。
func TestEmptyPromptShotIDsPointsAtTheRealShot(t *testing.T) {
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, H3Prompt: "summary:\nx"},
		realEmptyShot,
		{ID: 9, H3Prompt: "   \n "}, // 只有空白也算空（引擎收到跟空串一样）
	}}
	ids := project.EmptyPromptShotIDs(plan)
	if len(ids) != 2 || ids[0] != 8 || ids[1] != 9 {
		t.Errorf("该点名 [#8 #9]，实得 %v", ids)
	}
}

// realShot12 真机《我捡的团子，暖了三千人的船》第01集镜 12（2026-10-08 现场，照 plan.json 抄）：
// **材料齐全、只有 `action` 与 `h3_prompt` 是空的** —— 用户第二次报「还是报一样的错误」就是这一镜。
// 与上面那个夹具的区别正在 action：那一个写着动作，这一个**连动作都是空的**，
// 而旧指令只把「时长 + 动作」给模型 ⇒ 模型手上没有任何具体内容（哪几句要说都不知道）。
var realShot12 = project.Shot{
	ID:         12,
	Duration:   4,
	Scene:      "scene_old_hold",
	ShotSize:   "近景",
	Characters: []string{"char_jiangwanzhou", "char_xiaodeng", "char_atang"},
	Positions: map[string]string{
		"姜晚舟": "画面左三分之一、前景、面朝右下，双手圈着怀里的白绒团",
		"小灯":  "画面右三分之一、前景、面朝左，双手举着木鸟",
	},
	Camera: "Static Shot with small amplitude at slow speed; a slight push in as the bird is pinned",
	Action: "",
	Dialogue: []project.Line{
		{Speaker: "小灯", Text: "师姐，她叫什么？", OS: true},
		{Speaker: "姜晚舟", Text: "没名。", OS: true},
	},
	VoKind: "speech",
}

// realShot12Plan 与镜 12 同集的人物/场景卡（照 plan.json 抄，名字与 id 都写上：
// 指令里要能认出来，走位表的键是名字、characters 里是 id）。
func realShot12Plan() *project.Plan {
	return &project.Plan{
		Characters: []project.Character{
			{ID: "char_jiangwanzhou", Name: "姜晚舟"},
			{ID: "char_xiaodeng", Name: "小灯"},
			{ID: "char_atang", Name: "阿糖"},
		},
		Scenes: []project.Scene{{ID: "scene_old_hold", Name: "冰星老舱·青铜玉炉厅"}},
		Shots:  []project.Shot{realShot12},
	}
}

// TestEmptyShotFixCarriesTheShotMaterial 【本轮根因】空提示词的修复指令必须把这一镜**自己的材料**
// 全给模型：要说哪几句（逐字）、谁在说、在哪个场景、什么景别机位、走位在哪。
//
// 旧指令只写「时长 + 动作」，而真机镜 12 的动作**本身也是空的** ⇒ 模型没有内容可写 ⇒
// 它写出来的正文里不会有计划里那两句 `<d>` ⇒ 采纳闸门（台词条数必须等于计划里的句数）
// 把正确修复判成改坏了、退回原文（原文是空的）⇒ 这一镜永远补不上
// ⇒ 渲染入口的致命门「h3_prompt 为空」拦住整条产线（48 集停在第 1 集）。
func TestEmptyShotFixCarriesTheShotMaterial(t *testing.T) {
	plan := realShot12Plan()
	got := collectRepairable(plan)
	if len(got) != 1 || got[0].shotID != 12 {
		t.Fatalf("该点名 #12，实际 %+v", got)
	}
	fix := got[0].fix
	for _, want := range []string{
		"EXACTLY 2 line(s)", // 句数（采纳闸门就按它数）
		"师姐，她叫什么？",          // 台词逐字（原文）
		"没名。",               // 第二句
		"小灯",                // 谁在说
		"姜晚舟",               // 谁在说
		"off-screen",        // os:true ⇒ 画外（画面里的人不许动嘴）
		"scene_old_hold",    // 场次 id
		"冰星老舱·青铜玉炉厅",        // 场次名
		"近景",                // 景别
		"Static Shot",       // 机位
		"画面左三分之一、前景、面朝右下，双手圈着怀里的白绒团", // 走位原文
		"NO other <d> line",             // 不许凭空多出台词
		"not written in the storyboard", // 动作是空的这件事要说出来，不许让模型自己编
	} {
		if !strings.Contains(fix, want) {
			t.Errorf("修复指令里缺 %q —— 模型手上没有它，就写不出计划里那两句：\n%s", want, fix)
		}
	}
}
