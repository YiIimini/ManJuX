package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// —— 配音侧：把每句台词的声线绑成**权威文本**（2026-10-07 用户「配音存在乱配音」）——
//
// 夹具＝**真机第01集逐句抄下来的引述**，一字不改（下面这些字符串就是仓库里那份 plan.json 的原话）。
// 为什么必须照抄：这条修复要判的是"模型自己写的改写版能不能被换掉"，
// 自己编一句"in a clear voice"测不到真机那 11 种措辞。

// cardVoiceQiqi 真机角色卡上祁七的 voice（逐字）。
const cardVoiceQiqi = "A cool, even, slightly hollow young female voice, measured and clipped, " +
	"each word read out flatly like a calibration readout rather than a feeling."

func realVoicePlan() *project.Plan {
	return &project.Plan{
		Characters: []project.Character{
			{ID: "char_qiqi", Name: "祁七", Kind: "android", Voice: cardVoiceQiqi},
		},
	}
}

// TestBindLineVoicesRebindsTheModelsRewording 模型每镜改写的声线必须被换掉。
func TestBindLineVoicesRebindsTheModelsRewording(t *testing.T) {
	plan := realVoicePlan()
	shot := &project.Shot{
		ID: 2, VoKind: "speech", Characters: []string{"char_qiqi"},
		Dialogue: []project.Line{
			{Speaker: "舱内AI", Text: "该单元不在名册上。", OS: true},
			{Speaker: "祁七", Text: "名册最后一行是多少。"},
		},
		H3Prompt: "detailed_description:\n" +
			"[Shot 1] The bay's overhead speaker voice, a flat, sexless synthetic voice, " +
			"even-paced and breathless, off-screen (S1) says: <d>[Chinese]该单元不在名册上。</d>\n" +
			"[Shot 2] Her lights move once, jaw level. She asks, in a clear, cool young voice " +
			"with a slight mechanical edge (S2) says: <d>[Chinese]名册最后一行是多少。</d>",
	}
	out, bound, unresolved := BindLineVoices(shot.H3Prompt, plan, shot)
	if bound != 2 || unresolved != 0 {
		t.Fatalf("应重绑 2 句、认不出 0 句，实得 bound=%d unresolved=%d\n%s", bound, unresolved, out)
	}
	// ① 角色卡上的原文（逐字）必须出现在引述里；卡片值末尾的句号会被吞（免得与语法的冒号撞成 ".："）。
	if !strings.Contains(normLoose(out), normLoose(strings.TrimRight(cardVoiceQiqi, "."))) {
		t.Errorf("祁七那句没绑上角色卡原文：\n%s", out)
	}
	// ② 无卡说话人（舱内AI）用平台默认合成声，并保留"画外"与它自己的 (Sx)。
	if !strings.Contains(out, "舱内AI (S1) says off-screen in "+syntheticVoiceDefault+": ") {
		t.Errorf("舱内AI 那句没绑上平台默认合成声：\n%s", out)
	}
	if !strings.Contains(out, "祁七 (S2) says in ") {
		t.Errorf("祁七那句的引述形状不对：\n%s", out)
	}
	// ③ 模型自己写的那几种措辞**一个字都不该留下**（H3 拿它当音频条件，留着音色就跟着它走）。
	for _, leftover := range []string{
		"a clear, cool young voice",
		"slight mechanical edge",
		"a flat, sexless synthetic voice, even-paced and breathless, off-screen (S1)",
	} {
		if strings.Contains(out, leftover) {
			t.Errorf("模型改写的声线还留在提示词里：%q\n%s", leftover, out)
		}
	}
	// ④ 幂等：再跑一次不再改（否则每装配一次就把引述重写一遍、指纹乱跳）。
	again, bound2, _ := BindLineVoices(out, plan, shot)
	if bound2 != 0 || again != out {
		t.Errorf("重绑不幂等：第二次 bound=%d、文本%s", bound2, map[bool]string{true: "变了", false: "没变"}[again != out])
	}
}

// TestBindLineVoicesRealInnerShots 真机三种内心/Q版镜的写法都要能吃掉（画面描述要留下）。
func TestBindLineVoicesRealInnerShots(t *testing.T) {
	plan := realVoicePlan()
	cases := []struct{ name, prompt, keep string }{
		{
			"shot5 Q版在肩上说话（画面描述必须留下）",
			"on her left shoulder the palm-sized Q-version figure stands, arms folded, chin lifted, " +
				"and speaks with 祁七's own voice, off-screen: the android girl with a clear, cool young voice " +
				"and a slight mechanical edge (S2) says: <d>[Chinese]下一行是空的。连一道格线都没留。</d>",
			"the palm-sized Q-version figure stands, arms folded, chin lifted",
		},
		{
			"shot11 闭嘴句必须留下",
			"the Q-version figure rises out of the still air, arms folded, chin lifted, speaking in " +
				"祁七's own voice while her lips stay completely closed: the android girl with a clear, " +
				"cool young voice and a slight mechanical edge (S2) says: <d>[Chinese]她不知道自己为什么要挂上它。</d>",
			"her lips stay completely closed",
		},
		{
			"shot13 短句写法",
			"The palm-sized Q-version self on her left shoulder crouches, arms folded, chin up, " +
				"speaking in 祁七's own voice, cool, slightly mechanical (S2) says: <d>[Chinese]谁烧的门。外面还有活物吗。</d>",
			"The palm-sized Q-version self on her left shoulder crouches",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sh := &project.Shot{ID: 5, VoKind: "inner", Characters: []string{"char_qiqi"},
				Dialogue: []project.Line{{Speaker: "祁七", Text: textOf(tc.prompt)}}}
			out, bound, unresolved := BindLineVoices(tc.prompt, plan, sh)
			if bound != 1 || unresolved != 0 {
				t.Fatalf("应重绑 1 句，实得 bound=%d unresolved=%d\n%s", bound, unresolved, out)
			}
			if !strings.Contains(out, tc.keep) {
				t.Errorf("画面描述被误删：%q\n%s", tc.keep, out)
			}
			if !strings.Contains(out, "says off-screen in ") {
				t.Errorf("内心镜的声线没写成画外：\n%s", out)
			}
			if strings.Contains(out, "cool young voice") || strings.Contains(out, "slightly mechanical") {
				t.Errorf("模型改写的声线还留着：\n%s", out)
			}
		})
	}
}

// TestBindLineVoicesLeavesWhatItCannotResolve 认不出来的一律不动（宁可让模型发挥，也不塞错声线）。
func TestBindLineVoicesLeavesWhatItCannotResolve(t *testing.T) {
	plan := realVoicePlan()
	// ① **数不一致**（正文 2 句 / dialogue 只有 1 条）⇒ 一个字都不动。
	// 【2026-10-10 语义收窄】旧版本用"正文 1 句 + dialogue 1 条、只是文本被模型改写"当反例 ——
	// 那个形态现在**要绑**（数一致 ⇒ 按顺序认人，见 TestBindLineVoicesOrderFallbackWhenPlanTextIsEmpty），
	// 因为平台的 dialogue 是有序的、"第 i 句 = 第 i 条"是确定性信息。真正"认不出"的情形收窄成
	// **数对不上**与**没有说话人**两种 —— 那两种才是一个人都不许猜。
	sh := &project.Shot{ID: 1, VoKind: "speech", Characters: []string{"char_qiqi"},
		Dialogue: []project.Line{{Speaker: "祁七", Text: "名册最后一行是多少。"}},
		H3Prompt: "… she says in a clear, cool young voice (S2) says: <d>[Chinese]台词被模型改成了别的字。</d> " +
			"<d>[Chinese]凭空多出来的一句。</d>"}
	out, bound, unresolved := BindLineVoices(sh.H3Prompt, plan, sh)
	if bound != 0 || unresolved != 2 {
		t.Fatalf("数对不上时一个字都不许动：bound=%d unresolved=%d\n%s", bound, unresolved, out)
	}
	if out != sh.H3Prompt {
		t.Errorf("认不出却改了文本：\n%s", out)
	}
	// ② 数一致、但 dialogue 里**没有说话人** ⇒ 同样不动（不猜）。
	noSpk := &project.Shot{ID: 2, VoKind: "speech", Characters: []string{"char_qiqi"},
		Dialogue: []project.Line{{Text: "名册最后一行是多少。"}},
		H3Prompt: "… says in a clear, cool young voice: <d>[Chinese]名册最后一行是多少。</d>"}
	if _, b, u := BindLineVoices(noSpk.H3Prompt, plan, noSpk); b != 0 || u != 1 {
		t.Errorf("没有说话人时不许猜：bound=%d unresolved=%d", b, u)
	}
	// 规则文本里的示例标签（`<d>…</d>`、`<d></d>`、`<d>（无）</d>`）不是台词，别当句子数。
	rules := "MANDARIN ONLY: matching the [Chinese] tag inside each <d>…</d>; never write <d></d> or <d>（无）</d>"
	if got, _, _ := BindLineVoices(rules, plan, sh); got != rules {
		t.Errorf("规则文本里的示例标签被当成台词改了：\n%s", got)
	}
}

// TestBindLineVoicesOrderFallbackWhenPlanTextIsEmpty 真机那份坏方案（`dialogue[].text` 全是空串）也要认得出人。
//
// 【真机取证（2026-10-10）】归档第01集 **19 处 `dialogue[].text` 是空串**（ComfyUI 侧交付文档 §10.2 记的
// 正是这份方案）：文本对不上 ⇒ 旧实现认不出 ⇒ 那些句子只能保留模型自己写的引述（**没有权威声线**），
// 音色逐句漂移 —— 用户听到的「配音不对人」。而对话条目的**先后顺序**是平台自己的数据，
// 正文里的台词也是按序写的 ⇒ 数一致时"第 i 句 = dialogue 第 i 条"是确定性信息。
func TestBindLineVoicesOrderFallbackWhenPlanTextIsEmpty(t *testing.T) {
	plan := realVoicePlan() // 只有祁七有卡；白手套没有卡（走平台派生的默认声线）
	sh := &project.Shot{ID: 3, VoKind: "speech", Characters: []string{"char_qiqi"},
		Dialogue: []project.Line{{Speaker: "祁七"}, {Speaker: "白手套"}, {Speaker: "祁七"}}, // 词全空＝真机那份坏方案
		// 夹具照真机形态：**每句台词各占一行**（真机提交版提示词就是这样，一行一句带缩进）——
		// 挤在一行会让"引述头"跨句（`voiceClauseHead` 取的是从上一个换行到 `<d>` 的那段），
		// 于是重绑会把前一句的 `</d>` 一起吃掉。
		H3Prompt: "subject_definitions:\n<Subject 1> is 祁七, the android maintenance unit.\n" +
			"detailed_description:\n[Shot 1] 祁七 (S2) says: <d>[Chinese]别碰那条链。</d>\n" +
			"白手套 (S1) says: <d>[Chinese]一台散件，凭什么说话。</d>\n" +
			"祁七 (S2) says: <d>[Chinese]凭它还在响。</d>\n"}
	out, bound, unresolved := BindLineVoices(sh.H3Prompt, plan, sh)
	if bound != 3 || unresolved != 0 {
		t.Fatalf("三句都该按顺序认出人并绑上声线：bound=%d unresolved=%d\n%s", bound, unresolved, out)
	}
	// 第 1/3 句是祁七：必须出现卡上的权威声线（逐字特征串）。
	if n := strings.Count(out, "A cool, even, slightly hollow young female voice, measured and clipped"); n != 2 {
		t.Errorf("祁七的两句都要绑上卡上声线，实得 %d 处\n%s", n, out)
	}
	// 第 2 句是白手套（无卡）：也要有一条平台给的声线描述，且说话人名要写对。
	if !strings.Contains(out, "白手套") {
		t.Errorf("第二句的说话人应当是白手套\n%s", out)
	}
	// 台词文本一个字都不许被改写（只换引述里的声线）。
	for _, line := range []string{"别碰那条链。", "一台散件，凭什么说话。", "凭它还在响。"} {
		if !strings.Contains(out, "<d>[Chinese]"+line+"</d>") {
			t.Errorf("台词被改写了：缺少 %q\n%s", line, out)
		}
	}
}

// TestShotCastingIncludesSpeakersWithoutCards 真机 shot 1：唯一说话人「舱内AI」没有角色卡 ⇒
// 原来那份 casting 块**整段缺席**，一个正在说话的人没有任何音色约束。
func TestShotCastingIncludesSpeakersWithoutCards(t *testing.T) {
	plan := realVoicePlan()
	sh := &project.Shot{ID: 1, Characters: []string{"char_qiqi"},
		Dialogue: []project.Line{{Speaker: "舱内AI", Text: "重新检测到生命反应。", OS: true}}}
	got := ShotCasting(plan, sh)
	if len(got) != 1 || got[0].Name != "舱内AI" {
		t.Fatalf("无卡说话人必须进 casting 清单，实得 %+v", got)
	}
	if got[0].Voice != syntheticVoiceDefault {
		t.Errorf("系统音该用平台默认合成声，实得 %q", got[0].Voice)
	}
	// 没台词的镜：一份 casting 都不该有（2026-09-27「小孩子乱吵」那条的回归判据）。
	if silent := ShotCasting(plan, &project.Shot{ID: 12, Characters: []string{"char_qiqi"}}); len(silent) != 0 {
		t.Errorf("没有台词的镜不该有 casting 清单：%+v", silent)
	}
}

// TestDefaultVoiceForSeparatesSystemFromNarrator 「舱内AI」同时命中 IsVoiceoverSpeaker（名字里有 AI）——
// 若先走旁白那条，一个舱内电子音会被配成"warm, low-pitched adult male narrator"（第一次重绑的实测结果）。
func TestDefaultVoiceForSeparatesSystemFromNarrator(t *testing.T) {
	if got := defaultVoiceFor("舱内AI"); got != syntheticVoiceDefault {
		t.Errorf("系统音该用合成声默认，实得 %q", got)
	}
	if got := defaultVoiceFor("旁白"); got != narratorVoiceDefault {
		t.Errorf("旁白该用旁白默认，实得 %q", got)
	}
	// 认不出的说话人：**按名字派生**池子里的声线（2026-10-07 改口径）——
	// 此前是所有人共用常数 genericVoiceDefault ⇒ 家丁甲/家丁乙/路人丙 一个嗓子。
	// 判据两条：同名的值必须与派生一致（跨镜稳定），**两个不同名不许相同**。
	if got, want := defaultVoiceFor("家丁甲"), project.DerivedVoice("家丁甲", ""); got != want {
		t.Errorf("无卡说话人该按名字派生声线，实得 %q，期望 %q", got, want)
	}
	if defaultVoiceFor("家丁甲") == defaultVoiceFor("家丁乙") {
		t.Error("家丁甲与家丁乙不该共用一句声线（无卡说话人撞声）")
	}
}

// TestHealedPlanGivesEachCharacterHisOwnVoice 读取边界补出来的声线**必须真到渲染侧**。
//
// 2026-10-07：`EnsureAnchorCast` 补出来的卡（苔生/拾叁/…)与②会话漏写 voice 的卡，
// 渲染侧要么什么都绑不上、要么全落到同一句默认声线 ⇒ 成片里"好几个人一个嗓子"。
// 这一条把链路串起来测：**HealPlan（唯一读取边界）→ voiceFor（渲染侧取声线）**，
// 判据是两个不同角色不许拿到同一条，且三个角色都得有声线。
func TestHealedPlanGivesEachCharacterHisOwnVoice(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{
		{Name: "苔生", Kind: "human", Description: "- 性别：女"},
		{Name: "拾叁", Kind: "human", Description: "- 性别：女"},
		{Name: "邵舷", Kind: "human", Description: "- 性别：男"},
	}}
	// 没有走读取边界时：声线只能"按名字从**中性池**派生"（模板不知道性别）——
	// 这正是这张卡缺 voice 的代价：女角色会拿到中性嗓，而且没有"同一份方案内不撞声"这层保障。
	before, ok := voiceFor(plan, "苔生")
	if !ok || before != project.DerivedVoice("苔生", "") {
		t.Fatalf("未经自愈时应落到按名字派生的中性默认，实得 %q（ok=%v）", before, ok)
	}
	project.HealPlan(plan)
	got := map[string]string{}
	for i, n := range []string{"苔生", "拾叁", "邵舷"} {
		v, ok := voiceFor(plan, n)
		if !ok || strings.TrimSpace(v) == "" {
			t.Fatalf("自愈之后「%s」还是没有声线 —— 渲染时只能让模型现编", n)
		}
		if card := strings.TrimSpace(plan.Characters[i].Voice); v != card {
			t.Errorf("渲染侧没读到卡片上的声线：「%s」卡上是 %q，voiceFor 给的是 %q", n, card, v)
		}
		got[n] = v
	}
	if got["苔生"] == before {
		t.Error("自愈该把锚表里的性别带上（女声池），否则女角色一直在中性嗓里")
	}
	if got["苔生"] != project.DerivedVoice("苔生", "female") {
		t.Errorf("苔生该走女声池，实得 %q", got["苔生"])
	}
	if got["苔生"] == got["拾叁"] {
		t.Errorf("苔生与拾叁拿到同一条声线（%q）—— 成片里是同一个嗓子", got["苔生"])
	}
	if got["邵舷"] == got["苔生"] || got["邵舷"] == got["拾叁"] {
		t.Errorf("男声池与女声池撞了：邵舷 %q", got["邵舷"])
	}
}

// textOf 从夹具提示词里取 `<d>` 内容（测试辅助：dialogue 必须与它逐字一致）。
func textOf(prompt string) string {
	i := strings.Index(prompt, "<d>[Chinese]")
	if i < 0 {
		return ""
	}
	rest := prompt[i+len("<d>[Chinese]"):]
	j := strings.Index(rest, "</d>")
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// —— 2026-10-10：用户「配音还是存在问题，声不对人，重复配音」——
//
// 真机（《离婚后，千亿前夫跪求复合》第01集，11 镜 / 15 句）取证：装配把**卡上权威声线**
// 追加在**模型自编声线**的右边，而左扩只在"每个词都在声线词表里"时才吃 —— 表里缺一个词
// （真机命中的是 older / rasping / drawling / smiling）就整段留下，于是音频条件里同时有两句
// **互相矛盾**的声线（H3 把这段文字当音频条件，不是契约）。下面的夹具就是那 3 条原话，一字不改。

// cardVoiceGuMu / cardVoiceSuYa 角色卡上的 voice（逐字，与 plan.characters 一致）。
const (
	cardVoiceGuMu = "An elderly, sharp, nagging female voice with a nasal edge and a dry rasp; " +
		"she talks over people and never lowers her volume."
	cardVoiceSuYa = "A sweet, breathy, sing-song young female voice that turns shrill and whining " +
		"the instant she loses her footing."
)

func TestBindLineVoicesEatsModelVoiceLeftovers(t *testing.T) {
	cases := []struct {
		name     string
		speaker  string
		card     string
		line     string
		raw      string   // 模型自己写的那句引述（真机原话）
		mustGone []string // 一个字都不许留下（否则与卡上声线同时进音频条件）
	}{
		{
			name: "顾母（older/rasping 不在词表里）", speaker: "顾母", card: cardVoiceGuMu,
			line:     "赶紧签！别耽误我抱孙子。",
			raw:      `<Subject 1> (S3), an older woman in her sixties with a sharp, rasping, impatient voice, says: `,
			mustGone: []string{"older woman", "sharp, rasping", "impatient voice"},
		},
		{
			name: "苏雅（twenties/soft/drawling）", speaker: "苏雅", card: cardVoiceSuYa,
			line:     "姐姐，五万块，够你买房了。",
			raw:      `<Subject 2> (S2), twenties, soft, drawling, smiling voice, says: `,
			mustGone: []string{"twenties", "drawling", "smiling voice"},
		},
		{
			name: "苏雅（a young woman … smiling voice，动词还带尾巴）", speaker: "苏雅", card: cardVoiceSuYa,
			line:     "你们……知道她是谁吗？",
			raw:      `<Subject 2> (S2), a young woman in her twenties with a soft, drawling, smiling voice, says with the smile gone out of it: `,
			mustGone: []string{"young woman", "drawling", "smiling voice"},
		},
	}
	for _, c := range cases {
		plan := &project.Plan{Characters: []project.Character{
			{ID: "char_x", Name: c.speaker, Kind: "human", Voice: c.card},
		}}
		shot := &project.Shot{
			ID: 1, VoKind: "speech", Characters: []string{"char_x"},
			Dialogue: []project.Line{{Speaker: c.speaker, Text: c.line}},
			H3Prompt: c.raw + "<d>[Chinese]" + c.line + "</d>",
		}
		out, bound, unresolved := BindLineVoices(shot.H3Prompt, plan, shot)
		if bound != 1 || unresolved != 0 {
			t.Errorf("%s：应重绑 1 句、认不出 0 句，实得 bound=%d unresolved=%d\n%s", c.name, bound, unresolved, out)
		}
		// ① 卡上声线必须**逐字**在里面（末尾句号被 embedVoice 吞掉，与既有口径一致）
		// ① 卡上声线必须**逐字**在里面（末尾句号被 embedVoice 吞掉，与既有口径一致）
		if !strings.Contains(out, c.speaker+" says in ") ||
			!strings.Contains(normLoose(out), normLoose(strings.TrimRight(c.card, "."))) {
			t.Errorf("%s：没绑上卡上权威声线：\n%s", c.name, out)
		}
		for _, bad := range c.mustGone {
			if strings.Contains(strings.ToLower(out), strings.ToLower(bad)) {
				t.Errorf("%s：模型自编的声线还留在提示词里（%q）—— 这正是「声不对人」的来源：\n%s", c.name, bad, out)
			}
		}
		// ③ 一句台词只许有一个声线子句（两个 says in 就是把两句声线都喂给了模型）
		if n := strings.Count(out, " says in "); n != 1 {
			t.Errorf("%s：这句台词出现了 %d 个声线子句（应恰好 1 个）：\n%s", c.name, n, out)
		}
	}
}

// TestStripGenderTailKeepsPictureWords 反面针：**画面描述一个字都不许删**。
//
// 判据刻意保守 —— 性别证据右边只要还有"画面段"（含画面名词/主体标记），就整句不动：
// 宁可漏删一处声线残留（用户顶多听到一次音色漂移），也不能删掉画面内容（那是不可逆的创作损失）。
func TestStripGenderTailKeepsPictureWords(t *testing.T) {
	// 性别证据右边跟着画面段（one hand / table）⇒ 一个字都不动（尾部多余逗号会被修掉，内容必须原样）
	head := `<Subject 1> (S3), an older woman, one hand resting on the table, `
	got, hit := stripGenderTail(head)
	if hit || !strings.Contains(got, "an older woman") || !strings.Contains(got, "one hand resting on the table") {
		t.Errorf("右边还有画面描述时不许动它：\n得到 %q（hit=%v）", got, hit)
	}
	// 画面名词在**被吃的段**里 ⇒ 同样不许吃
	head2 := `<Subject 2> (S2), a young woman with her hand on the frame, `
	if got2, _ := stripGenderTail(head2); !strings.Contains(got2, "a young woman") ||
		!strings.Contains(got2, "hand on the frame") {
		t.Errorf("被吃的段里含画面名词时不许吃：\n得到 %q", got2)
	}
	// 反面：纯声线残段必须吃掉（真机那 3 条就是这一类）
	got3, hit3 := stripGenderTail(`<Subject 1> (S3), an older woman in her sixties with a sharp, rasping, `)
	if !hit3 || strings.Contains(got3, "older woman") || strings.Contains(got3, "rasping") {
		t.Errorf("词表缺词的残段必须吃掉：得到 %q（hit=%v）", got3, hit3)
	}
}

// TestSubjectMarkerSpeakerBindsCardVoice 引述里只写 `<Subject N>` 时也要绑上**卡上权威声线**。
//
// 【真机取证（2026-10-10 深度排查，用户「配音老是有问题」）】归档第01集镜 3 装配后的原话：
//
//	<Subject 1> (S2) says: <d>[Chinese]凭它还在响。</d>
//
// 说话人写成了主体标记，卡上的名字（祁七）不在那一行 ⇒ 旧实现认不出 ⇒ 这句**没有权威声线**
// （只能让模型自由发挥），用户听到的就是"这句换了个嗓子"。而"第 1 个主体是祁七"这件事就写在
// **同一条提示词的第一节**（`subject_definitions: <Subject 1> is 祁七, …`）——
// 平台自己能读到的确定性信息，不该放着不用。
func TestSubjectMarkerSpeakerBindsCardVoice(t *testing.T) {
	plan := realVoicePlan()
	const defs = "subject_definitions:\n<Subject 1> is 祁七, the android maintenance unit " +
		"(machine stamp U-80001): porcelain-white skin, waist-length straight hair.\n"
	sh := &project.Shot{ID: 3, VoKind: "speech", Characters: []string{"char_qiqi"},
		H3Prompt: defs + "detailed_description:\n[Shot 1] <Subject 1> (S2) says: <d>[Chinese]凭它还在响。</d>\n"}
	out, bound, unresolved := BindLineVoices(sh.H3Prompt, plan, sh)
	if bound != 1 || unresolved != 0 {
		t.Fatalf("主体标记应当被映射回角色名并绑上卡上声线：bound=%d unresolved=%d\n%s", bound, unresolved, out)
	}
	// 声线原文可能被收尾处理过（`voiceClause` 会把句子末尾的句号换成冒号），所以断**开头那段特征串**。
	if !strings.Contains(out, "A cool, even, slightly hollow young female voice, measured and clipped") {
		t.Errorf("绑定后必须出现卡片上的权威声线（逐字）\n%s", out)
	}
	if strings.Contains(out, "<Subject 1> (S2) says:") {
		t.Errorf("引述里的主体标记应当已被角色名+声线替换掉\n%s", out)
	}
	// 反面针：主体定义映射到的名字**没有角色卡** ⇒ 一个字都不许改（宁可认不出，也不塞错人）。
	noCard := &project.Shot{ID: 4, VoKind: "speech", Characters: []string{"char_qiqi"},
		H3Prompt: "subject_definitions:\n<Subject 1> is 路人甲, a passer-by.\n" +
			"detailed_description:\n[Shot 1] <Subject 1> (S2) says: <d>[Chinese]让一让。</d>\n"}
	if got, b, u := BindLineVoices(noCard.H3Prompt, plan, noCard); got != noCard.H3Prompt || b != 0 || u != 1 {
		t.Errorf("映射到的名字没有角色卡 ⇒ 不许绑：bound=%d unresolved=%d\n%s", b, u, got)
	}
}
