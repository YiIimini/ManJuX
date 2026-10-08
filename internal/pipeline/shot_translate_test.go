package pipeline

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// shot_translate_test.go 分镜动作/走位翻英文的判据（2026-10-08 用户：「还没做/边界，给我留着吗？」）。
//
// 夹具照真机《我捡的团子，暖了三千人的船》第01集镜 12 的形态：正文为空、动作空、
// 走位是**中文**（键是中文名字）。

// realShot12ForTranslate 真机镜 12（走位键=中文名字，动作是空的）。
func realShot12ForTranslate() project.Shot {
	return project.Shot{
		ID:         12,
		Duration:   4,
		Scene:      "scene_old_hold",
		Characters: []string{"char_jiangwanzhou", "char_xiaodeng", "char_atang"},
		Positions: map[string]string{
			"姜晚舟": "画面左三分之一、前景、面朝右下，双手圈着怀里的白绒团",
			"小灯":  "画面右三分之一、前景、面朝左，双手举着木鸟",
			"阿糖":  "画面中央、前景，白绒团在姜晚舟臂弯里按着木鸟",
		},
		ShotSize: "近景",
		Camera:   "Static Shot with small amplitude at slow speed",
		Action:   "",
		Dialogue: []project.Line{
			{Speaker: "小灯", Text: "师姐，她叫什么？", OS: true},
		},
		H3Prompt: "",
	}
}

// TestNeedsStagingTranslation 花不花这次 LLM 调用，判据要两条都满足。
func TestNeedsStagingTranslation(t *testing.T) {
	cases := []struct {
		name string
		sh   project.Shot
		want bool
	}{
		{"真机镜 12（空正文 + 中文走位）", realShot12ForTranslate(), true},
		{"正文在 ⇒ 不必翻（模型自己写了英文）", func() project.Shot {
			s := realShot12ForTranslate()
			s.H3Prompt = "subject_definitions:\n<Subject 1> is 姜晚舟.\n"
			return s
		}(), false},
		{"已经翻过 ⇒ 幂等", func() project.Shot {
			s := realShot12ForTranslate()
			s.PositionsEN = map[string]string{"姜晚舟": "left third of frame"}
			return s
		}(), false},
		{"材料本来就是英文 ⇒ 不必翻", func() project.Shot {
			s := realShot12ForTranslate()
			s.Positions = map[string]string{"姜晚舟": "left third of frame, foreground"}
			return s
		}(), false},
		{"动作是中文（没有走位）⇒ 要翻", func() project.Shot {
			s := realShot12ForTranslate()
			s.Positions = nil
			s.Action = "小灯把木鸟举到炉口"
			return s
		}(), true},
		{"正文空但完全没有材料 ⇒ 也不值得翻（没东西可翻）", project.Shot{ID: 13, Duration: 6}, false},
	}
	for _, c := range cases {
		if got := NeedsStagingTranslation(c.sh); got != c.want {
			t.Errorf("%s：NeedsStagingTranslation = %v，期望 %v", c.name, got, c.want)
		}
	}
}

// TestStagingTodoOnlyAsksForShotsThatNeedIt 待翻清单只放"正文为空 + 中文材料"的镜。
func TestStagingTodoOnlyAsksForShotsThatNeedIt(t *testing.T) {
	ok := realShot12ForTranslate()
	done := realShot12ForTranslate()
	done.ID = 11
	done.H3Prompt = "subject_definitions:\n<Subject 1> is 姜晚舟.\n"
	plan := &project.Plan{Shots: []project.Shot{done, ok}}
	todo := stagingTodo(plan)
	if len(todo) != 1 || todo[0].ID != 12 {
		t.Fatalf("只该把镜 12 交给翻译会话，实得 %d 镜：%+v", len(todo), todo)
	}
}

// TestStagingRequestCarriesTheRealMaterial 请求里必须把这一镜的中文走位**逐条**给模型，
// 并说清"动作是空的就别编"。缺了材料，翻出来的东西就是编的。
func TestStagingRequestCarriesTheRealMaterial(t *testing.T) {
	req := stagingRequest([]project.Shot{realShot12ForTranslate()})
	for _, want := range []string{
		"### 镜 12",
		"画面左三分之一、前景、面朝右下，双手圈着怀里的白绒团",
		"画面右三分之一、前景、面朝左，双手举着木鸟",
		"画面中央、前景，白绒团在姜晚舟臂弯里按着木鸟",
		"分镜台没写动作",
	} {
		if !strings.Contains(req, want) {
			t.Errorf("请求里缺 %q：\n%s", want, req)
		}
	}
	// 系统提示的硬要求（只写英文 / 不许增删）不能被改掉
	for _, want := range []string{"一个汉字都不许有", "不许新增动作", "严格的 JSON"} {
		if !strings.Contains(stagingTranslateSys, want) {
			t.Errorf("翻译会话的系统提示里缺硬要求 %q", want)
		}
	}
}

// TestParseStagingMapToleratesFencesAndShotPrefixes 模型会给围栏、也会把键写成「镜 12」；
// `positions` 两种形态（要的数组 / 旧的对象）都要能解析。
func TestParseStagingMapToleratesFencesAndShotPrefixes(t *testing.T) {
	raw := "好的，结果如下：\n```json\n{\"镜 12\": {\"action\": \"the bird is pinned\", " +
		"\"positions\": [{\"who\": \"姜晚舟\", \"where\": \"left third of frame\"}]}}\n```\n"
	m := parseStagingMap(raw)
	got, ok := m[12]
	if !ok || got.Action != "the bird is pinned" {
		t.Fatalf("解析失败/解析错了：%+v", m)
	}
	pos := parseStagingPositions(got.Positions)
	if len(pos) != 1 || pos[0].Who != "姜晚舟" || pos[0].Where != "left third of frame" {
		t.Fatalf("数组形态没解析对：%+v", pos)
	}

	// 旧的对象形态也要能读（别的模型/别的提示词版本会给这种）
	old := parseStagingPositions(json.RawMessage(`{"姜晚舟":"left third of frame"}`))
	if len(old) != 1 || old[0].Where != "left third of frame" {
		t.Fatalf("对象形态没解析对：%+v", old)
	}
	if parseStagingMap("模型今天不想干活") != nil {
		t.Error("没有 JSON 时应返回 nil（调用方据此放弃，而不是写半截英文进方案）")
	}
}

// TestAlignStagingPositionsByKeyAndOrder 键对不上的时候：
//
//	① `who` 逐字/宽松相同 ⇒ 用它；
//	② 都对不上但**条数相等** ⇒ 按输入顺序配（模型把键译成拼音时的正解）；
//	③ 条数不等 ⇒ **整镜不算**（不许猜人、不许静默少人）。
//
// 【为什么这条重要】真机实测：模型把 `{"姜晚舟": …}` 的**键也翻成拼音**（`Jiang Wanzhou`），
// 兜底正文按方案里的名字查不到 ⇒ 英文走位翻出来了却**一条都没进正文**。
func TestAlignStagingPositionsByKeyAndOrder(t *testing.T) {
	sh := realShot12ForTranslate()
	keys := positionKeys(sh) // 字典序（请求与写回同一条判据）
	// 主体名集合（角色+场景+道具）：翻译结果里**允许**出现这些汉字名（真机实测模型会写
	// `in 姜晚舟's arms`），但别的汉字一律不许 —— 判据与入口的汉字卫生门同源。
	names := []string{"姜晚舟", "小灯", "阿糖", "冰星老舱·青铜玉炉厅"}
	if len(keys) != 3 {
		t.Fatalf("夹具前提：镜 12 有 3 条走位，实得 %d", len(keys))
	}

	// ① 键逐字相同
	exact := json.RawMessage(`[{"who":"` + keys[0] + `","where":"left third"},
		{"who":"` + keys[1] + `","where":"right third"},
		{"who":"` + keys[2] + `","where":"centre"}]`)
	pn, ok := alignStagingPositions(keys, exact, names)
	if !ok || pn[keys[0]] != "left third" || pn[keys[2]] != "centre" {
		t.Fatalf("键逐字相同时该对上：ok=%v %+v", ok, pn)
	}

	// ② 键被模型译走（拼音）但条数相等 ⇒ 按输入顺序配（真机现场就是这个形态）
	translated := json.RawMessage(`[{"who":"Jiang Wanzhou","where":"left third"},
		{"who":"Xiao Deng","where":"right third"},
		{"who":"A Tang","where":"centre"}]`)
	pn2, ok2 := alignStagingPositions(keys, translated, names)
	if !ok2 {
		t.Fatal("键被译走但条数相等时应当按输入顺序配上（否则英文走位永远进不了正文）")
	}
	if pn2[keys[0]] != "left third" || pn2[keys[1]] != "right third" || pn2[keys[2]] != "centre" {
		t.Fatalf("按顺序配错了：%+v（键序 %v）", pn2, keys)
	}

	// ③ 条数不等 ⇒ 不猜
	short := json.RawMessage(`[{"who":"Jiang Wanzhou","where":"left third"}]`)
	if _, ok3 := alignStagingPositions(keys, short, names); ok3 {
		t.Error("模型少给了一个人的走位时**整镜不算**（不许猜人、不许静默少人）")
	}

	// 含汉字的值一律不要
	cjk := json.RawMessage(`[{"who":"` + keys[0] + `","where":"画面左三分之一"},
		{"who":"` + keys[1] + `","where":"right third"},
		{"who":"` + keys[2] + `","where":"centre"}]`)
	if _, ok4 := alignStagingPositions(keys, cjk, names); ok4 {
		t.Error("含汉字的走位不许采用（入口会拒收）")
	}
}

// TestApplyTranslatedStagingRejectsCJK **带汉字的结果一律不采用**（与卡片翻译同一条纪律）：
// 宁可这一镜少一份材料（兜底正文会如实播报），也不把中文塞进正文（入口会拒收）。
// 同时：没翻出来的镜要**逐条点名**，不许静默当成功。
func TestApplyTranslatedStagingRejectsCJK(t *testing.T) {
	plan := &project.Plan{Shots: []project.Shot{realShot12ForTranslate()}}
	todo := stagingTodo(plan)

	// ① 模型给了中文（等于没翻）：一个字段都不该写进去，且镜 12 要进 missing。
	n, missing := applyTranslatedStaging(plan, map[int]stagingEN{
		12: {Action: "小灯把木鸟举到炉口", Positions: json.RawMessage(
			`[{"who":"姜晚舟","where":"画面左三分之一"}]`)},
	}, todo)
	if n != 0 || len(missing) != 1 || missing[0] != 12 {
		t.Fatalf("带汉字的结果不该被采用、且要点名 #12，实得 n=%d missing=%v", n, missing)
	}
	if plan.Shots[0].ActionEN != "" || len(plan.Shots[0].PositionsEN) != 0 {
		t.Error("带汉字的翻译结果不许写进方案")
	}

	// ② 正常英文结果：写进去、不进 missing（键照抄输入的键）。
	plan2 := &project.Plan{Shots: []project.Shot{realShot12ForTranslate()}}
	keys := positionKeys(plan2.Shots[0])
	n2, missing2 := applyTranslatedStaging(plan2, map[int]stagingEN{
		12: {Action: "the bird is pinned between them", Positions: json.RawMessage(
			`[{"who":"` + keys[0] + `","where":"left third of frame, foreground"},
			  {"who":"` + keys[1] + `","where":"right third of frame, foreground"},
			  {"who":"` + keys[2] + `","where":"centre of frame, foreground"}]`)},
	}, stagingTodo(plan2))
	if n2 != 1 || len(missing2) != 0 {
		t.Fatalf("英文结果该被采用，实得 n=%d missing=%v", n2, missing2)
	}
	if plan2.Shots[0].ActionEN != "the bird is pinned between them" ||
		plan2.Shots[0].PositionsEN[keys[1]] != "right third of frame, foreground" {
		t.Error("英文结果没写进方案")
	}
}

// TestTranslateShotStagingWithoutClientIsNoOp 没配 LLM 时**不打断流水线**（返回 0 且不报错）。
func TestTranslateShotStagingWithoutClientIsNoOp(t *testing.T) {
	plan := &project.Plan{Shots: []project.Shot{realShot12ForTranslate()}}
	n, err := TranslateShotStaging(t.Context(), nil, plan)
	if n != 0 || err != nil {
		t.Fatalf("client 为 nil 时应静默跳过，实得 n=%d err=%v", n, err)
	}
	if plan.Shots[0].ActionEN != "" {
		t.Error("没配 LLM 时不该改动方案")
	}
}

// TestAlignKeepsSubjectNamesButRejectsOtherHan 汉字判据必须与**入口那道门同源**：
// 值里可以出现主体名（`the white plush ball in 姜晚舟's arms` —— 门自己就扣主体名），
// 但不许有别的汉字（真机第一次就是被"一个汉字都不许有"误杀了整镜）。
func TestAlignKeepsSubjectNamesButRejectsOtherHan(t *testing.T) {
	sh := realShot12ForTranslate()
	keys := positionKeys(sh)
	names := []string{"姜晚舟", "小灯", "阿糖"}

	// 值里带**主体名** ⇒ 合法（真机实测模型就会这么写）
	withName := json.RawMessage(`[{"who":"` + keys[0] + `","where":"foreground, the plush ball in 姜晚舟's arms"},
		{"who":"` + keys[1] + `","where":"right third, holding up the bird"},
		{"who":"` + keys[2] + `","where":"centre, pressing the bird"}]`)
	if _, ok := alignStagingPositions(keys, withName, names); !ok {
		t.Error("值里只有**主体名**（豁免）时必须接受 —— 否则真机那一镜会被判成没翻出来")
	}

	// 值里带**别的汉字**（中文散文）⇒ 不许采用（会在画面上变成乱码字形）
	withProse := json.RawMessage(`[{"who":"` + keys[0] + `","where":"画面左三分之一、前景"},
		{"who":"` + keys[1] + `","where":"right third"},
		{"who":"` + keys[2] + `","where":"centre"}]`)
	if _, ok := alignStagingPositions(keys, withProse, names); ok {
		t.Error("值里有主体名之外的汉字时必须拒绝（那是入口拒收级问题）")
	}
}

// TestRefillTargetsOnlyTouchesPlatformBodies 重拼只动**平台自己拼过正文**的那几镜：
// 依据是这一轮读取时记下的账 `plan.FilledBodyShots`（模型自己写的正文一个字都不许碰），
// 而且只挑"材料还是中文、还没英文版"的镜（翻过的不再花 LLM）。
func TestRefillTargetsOnlyTouchesPlatformBodies(t *testing.T) {
	// ① 没记过账（模型自己写的）⇒ 什么都不动
	model := realShot12ForTranslate()
	model.H3Prompt = "subject_definitions:\n<Subject 1> is 姜晚舟.\n"
	if got := refillTargets(&project.Plan{Shots: []project.Shot{model}}); len(got) != 0 {
		t.Errorf("模型自己写的正文不该被碰，实得 %v", got)
	}

	// ② 记过账、材料还是中文 ⇒ 要补翻 + 重拼
	p := &project.Plan{Shots: []project.Shot{realShot12ForTranslate()}}
	p.FilledBodyShots = []int{12}
	if got := refillTargets(p); len(got) != 1 || got[0] != 12 {
		t.Errorf("平台拼过的那一镜要补翻+重拼，实得 %v", got)
	}

	// ③ 已经有英文版 ⇒ 不再花 LLM
	p2 := &project.Plan{Shots: []project.Shot{realShot12ForTranslate()}}
	p2.FilledBodyShots = []int{12}
	p2.Shots[0].PositionsEN = map[string]string{"姜晚舟": "left third of frame"}
	if got := refillTargets(p2); len(got) != 0 {
		t.Errorf("已经有英文走位的镜不该再翻，实得 %v", got)
	}
}
