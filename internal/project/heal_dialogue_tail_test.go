package project

import (
	"os"
	"strings"
	"testing"
)

// heal_dialogue_tail_test.go **补出来的台词标签不许在同一趟 heal 里被抹掉**（2026-10-14 真机事故）。
//
// 【用户原话】「卧槽，现在角色完全没有配音了」
//
// 【真机取证】`人间收件箱·第01集·雨夜叩门`（12 镜里 **11 镜**有台词）：
//
//   - 盘上 `plan.json` 的 `h3_prompt` **一个 `<d>` 都没有**；
//   - 而 `HealPlan` 的播报写着「给 11 镜补上了缺失的台词标签」⇒ **补了又被删**；
//   - 提交版提示词因此**同时**出现「正文里有 `<d>`」与「this shot has NO spoken dialogue at all」，
//     模型照后者办 ⇒ 成片整集没有人声（三个已渲镜实测 mean_volume −21.6 / −27.7 / −22.9 dB，
//     有环境声、无一句台词）。
//
// 【机制】`FillMissingDialogueTags` 把补出来的 `<d>` **追加到正文末尾**，而 `non_diegetic_music:`
// 正是六段式的**最后一段** ⇒ 补出来的那几句落进了「配乐段」的**段体**里；紧接着
// `EnsureMusicNA`（当时写在 `HealResult{...}` 字面量里，Go 按源码顺序求值 ⇒ 排在补台词**之后**）
// 把配乐段**整段**换成 `N/A`（`musicSectionToNA`：段尾 = 下一个段名或正文结束），
// 于是刚补的台词连同尾部一起被删掉。
//
// 修法：把配乐段改写提到补台词**之前**（先改配乐、再补台词 ⇒ 尾巴落在最后一段之后，没人再动它）。
// 下面三条门分别钉：① 端到端结果；② 源码里的顺序；③ 危险顺序本身确实会吞尾巴（说明为什么不能反过来）。

// realShapedBodyWithoutDialogue 真机形态的六段式正文：**句尾就是 `non_diegetic_music: N/A`**，
// 台词一句都没写进正文（真机那份 plan 的正文全长 3099 字，这里截取可辨识的头尾）。
func realShapedBodyWithoutDialogue(music string) string {
	return "subject_definitions:\n" +
		"<Subject 1> is 周砚, the young locksmith of 铜锣巷七号, with the face, short black hair and " +
		"dark-blue work jacket shown in his character sheet.\n\n" +
		"summary:\n[reference generation] Close on 周砚 with his ear pressed to the old mailbox.\n\n" +
		"retention_analysis:\n- <Subject 1> (appears in [Shot 1]): fully_preserved - face shape, hair, jacket.\n\n" +
		"detailed_description:\nCinematic, 2D-animated, Chinese donghua style with clean linework.\n" +
		"[Shot 1] At 00:00.000 a close shot holds on 周砚, rain running down his hair. " +
		"He speaks quietly to himself without lifting his head.\n\n" +
		"overall_soundscape:\nRain hissing into steam on warm metal, a dull knock from inside the box.\n\n" +
		"non_diegetic_music:\n" + music + "\n"
}

func healFixtureShot(id int, body, line string) Shot {
	sh := Shot{
		ID:       id,
		Duration: 8,
		Scene:    "scene_lane",
		H3Prompt: body,
	}
	if line != "" {
		sh.Dialogue = []Line{{Speaker: "周砚", Text: line}}
	}
	return sh
}

// ① 端到端：heal 之后台词必须真的在正文里（**闸门本体**，不是下游过滤过的数据）。
func TestHealPlanKeepsFilledDialogueTags(t *testing.T) {
	const line = "箱子里……有东西在响。"
	cases := []struct {
		name  string
		music string
	}{
		// 真机那份：配乐本来就是 N/A（仍然会吞尾巴 —— 因为段体里混进了台词，isNA 判成 false）。
		{"配乐已是 N/A（真机形态）", "N/A"},
		// 另一类真机形态（「19 镜写着配乐」）：配乐非 N/A，`EnsureMusicNA` 必然要改写这一整段。
		{"配乐非 N/A（19 镜那种）", "A single low guzheng note, very slow tempo, sparse rhythm."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Plan{
				Characters: []Character{{ID: "char_zhou", Name: "周砚", Voice: "A low clear baritone."}},
				Scenes:     []Scene{{ID: "scene_lane", Name: "铜锣巷七号门口"}},
				Shots:      []Shot{healFixtureShot(3, realShapedBodyWithoutDialogue(c.music), line)},
			}
			if n := strings.Count(p.Shots[0].H3Prompt, "<d>"); n != 0 {
				t.Fatalf("夹具要先有\"正文里没写台词\"这个形态，实际 <d>=%d", n)
			}
			hr := HealPlan(&p)
			if hr.Dialogue != 1 {
				t.Fatalf("这一镜该被补上 1 处台词标签，实际 hr.Dialogue=%d", hr.Dialogue)
			}
			got := p.Shots[0].H3Prompt
			if !strings.Contains(got, "<d>[Chinese]"+line+"</d>") {
				t.Errorf("**补出来的台词又不见了** —— 这正是「角色完全没有配音」的成因。\n"+
					"正文尾部：%s", tailOfStr(got, 200))
			}
			// 配乐那一段该被改写成 N/A（这条政策本身没变）。
			if !strings.Contains(got, "non_diegetic_music:\nN/A") {
				t.Errorf("配乐段该被改写成 N/A；正文尾部：%s", tailOfStr(got, 200))
			}
			// 反面：整镜静音条款不该被写进这一镜（写了就是"有人在说却让他闭嘴"）。
			if IsHealedSilentShot(got) {
				t.Errorf("正文里有台词，却像静音镜一样什么都不说：%s", tailOfStr(got, 200))
			}
		})
	}
}

// IsHealedSilentShot 只给本门用的薄判据：正文里 `<d>` 之外没有任何真台词。
func IsHealedSilentShot(h3 string) bool {
	return len(spokenInPrompt(h3)) == 0
}

// ② 源码顺序门：配乐段改写必须排在补台词之前。
//
// 为什么用**顺序**而不是只测结果：这条事故的成因就是求值顺序，而 `HealResult{...}` 字面量里的
// 字段**按源码顺序求值** —— 只要有人把 `EnsureMusicNA(plan)` 挪回字面量里，端到端结果立刻回到 0 个 `<d>`。
// 顺序门与结果门一起才关得死（结果门在真机夹具上已经咬过一次）。
func TestMusicNARewriteRunsBeforeDialogueFill(t *testing.T) {
	src := mustReadProjectFile(t, "prop_refs.go")
	music := strings.Index(src, "musicNA := EnsureMusicNA(plan)")
	fill := strings.Index(src, "dialogueFilled := FillMissingDialogueTags(plan)")
	if music < 0 || fill < 0 {
		t.Fatalf("源码里找不到这两步（musicNA=%d fill=%d）—— 门失效", music, fill)
	}
	if music > fill {
		t.Errorf("`EnsureMusicNA` 必须排在 `FillMissingDialogueTags` **之前**：" +
			"补出来的 `<d>` 追加在正文末尾（= 最后一段「配乐段」里），" +
			"配乐段改写会把它连同尾部一起整段换掉 ⇒ 整集没人声（2026-10-14 真机事故）")
	}
	// 反面：这一条不许被塞回 `HealResult{...}` 字面量（那里按源码顺序求值，紧跟着补台词之后）。
	if strings.Contains(src, "MusicNA:           EnsureMusicNA(plan),") {
		t.Error("`EnsureMusicNA` 不许再写在 `HealResult{...}` 字面量里 —— 那会排到补台词之后")
	}
}

// ③ 危险顺序本身确实会吞尾巴：把「补台词 → 改配乐」这个顺序**直接跑一遍**，钉住它必须吞。
//
// 这条门不是"测 bug"，而是**记录机理**：它一旦不再吞（例如有人把 `musicSectionToNA` 的段边界改了），
// 说明口径变了，那时的结论（"顺序不能反"）必须重新审 —— 免得下一个人照着过期的理由做决定。
func TestDangerousOrderSwallowsTheDialogueTail(t *testing.T) {
	p := Plan{
		Characters: []Character{{ID: "char_zhou", Name: "周砚", Voice: "A low clear baritone."}},
		Shots:      []Shot{healFixtureShot(3, realShapedBodyWithoutDialogue("N/A"), "箱子里……有东西在响。")},
	}
	// ⚠ 故意用**错误**的顺序：先补台词、再改配乐。
	FillMissingDialogueTags(&p)
	if n := strings.Count(p.Shots[0].H3Prompt, "<d>"); n == 0 {
		t.Fatal("前置条件不成立：先补台词之后正文里应当有 <d>")
	}
	EnsureMusicNA(&p)
	if n := strings.Count(p.Shots[0].H3Prompt, "<d>"); n != 0 {
		t.Fatalf("这个顺序**本该**吞掉台词尾部（段体边界 = 下一段名或正文结束）；"+
			"现在没吞（<d>=%d）⇒ 机理变了，请重新审 `musicSectionToNA` 的段边界与上面那条顺序门", n)
	}
}

func tailOfStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}

func mustReadProjectFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读不到 %s：%v", name, err)
	}
	return string(b)
}
