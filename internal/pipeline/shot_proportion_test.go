package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 镜头侧「人体比例锚点」(shot_proportion.go)的判据。
//
// 这一条链的价值全在**分发**上,而它两种失效方式都是静默的:
//   - 发多了(把「七头半」塞进特写)→ 提示词与画面对不上,模型折中,脸被画小;
//   - 发少了或发错档 → 回到「每一镜各按各的审美发挥比例」的老毛病,没有任何报错。
//
// 所以这里把四件事钉死:①各景别(含中英写法/同族词/空值/大小写/带空格)分到哪一档;
// ②正文是**技能侧口径的逐字子集**(不另编比值、不引「黄金比例」);③幂等(跑两次一样、
// 已带锚点不再叠加);④端到端装配按景别真的注入了,而且不越界(器物镜/Q 版镜/定妆照链)。

// shotSizeCases 穷举景别写法 → 期望档位。
var shotSizeCases = []struct {
	size string
	want shotProportionLevel
}{
	// —— 脸档:画面里只有脸 ——
	{"特写", shotProportionFace},
	{"大特写", shotProportionFace}, // 同族词(project 侧校验合法)
	{"面部特写", shotProportionFace},
	{"近景", shotProportionFace},
	{"close-up", shotProportionFace},
	{"close up", shotProportionFace},
	{"CLOSE-UP", shotProportionFace},     // 大小写
	{"  close-up  ", shotProportionFace}, // 带空格
	{"extreme close-up", shotProportionFace},
	{"head shot", shotProportionFace},
	// —— 中景档:看得见颈与肩 ——
	{"中景", shotProportionMedium},
	{"中近景", shotProportionMedium}, // 字面里带「近景」,必须先于它判
	{"中景偏全景", shotProportionMedium},
	{"medium shot", shotProportionMedium},
	{"medium close-up", shotProportionMedium}, // 字面里带 close,必须先于它判
	{"mid shot", shotProportionMedium},
	{"MEDIUM", shotProportionMedium},
	// —— 全身档:人整个在画面里 ——
	{"全景", shotProportionFull},
	{"远景", shotProportionFull},
	{"全身", shotProportionFull},
	{"wide shot", shotProportionFull},
	{"wide", shotProportionFull},
	{"full body", shotProportionFull},
	{"long shot", shotProportionFull},
	{"establishing shot", shotProportionFull},
	// —— 认不出:不注入 ——
	{"", shotProportionNone},
	{"   ", shotProportionNone},
	{"\n\t ", shotProportionNone},
	{"over-the-shoulder", shotProportionNone},
	{"过肩镜头", shotProportionNone},
	{"插入镜头", shotProportionNone},
	{"？？？", shotProportionNone},
}

// TestShotProportionClauseDispatch 穷举:各景别(含中英写法/同族词/空值/大小写/带空格)
// 分到哪一档,以及那一档**只写画面上看得见的那几条**。
func TestShotProportionClauseDispatch(t *testing.T) {
	for _, tc := range shotSizeCases {
		got := shotProportionClause(tc.size)
		if lvl := shotProportionLevelOf(tc.size); lvl != tc.want {
			t.Errorf("景别 %q:档位应为 %d,实际 %d", tc.size, tc.want, lvl)
		}
		if tc.want == shotProportionNone {
			if got != "" {
				t.Errorf("景别 %q 认不出来,不该注入(宁可不加,也不要给错误景别的锚点),实际:%q", tc.size, got)
			}
			continue
		}
		if got == "" {
			t.Errorf("景别 %q 应注入,实际空", tc.size)
			continue
		}
		if !strings.HasPrefix(got, shotProportionMarker) {
			t.Errorf("景别 %q 的注入块缺抬头(抬头同时是幂等判据串):%q", tc.size, got)
		}
		// 一套「画面上看不看得见」的判据:脸档不得提颈肩腰腿,中景档不得提头身比,
		// 全身档八条齐发。
		body := strings.TrimPrefix(got, shotProportionMarker)
		has := func(sub string) bool { return strings.Contains(body, sub) }
		switch tc.want {
		case shotProportionFace:
			for _, want := range []string{
				"the eye line sits at the vertical middle of the head",
				"the eyes are one eye-width apart",
				"the nose is about one third of the face length and the mouth about one fifth of it",
				"the chin is about two thirds as wide as the cheekbones",
			} {
				if !has(want) {
					t.Errorf("景别 %q(脸档)缺「%s」:%q", tc.size, want, got)
				}
			}
			if has("the neck is slim") || has("shoulders are about two head-widths") || has("seven and a half heads") {
				t.Errorf("景别 %q(脸档)看不见颈/肩/腰腿,不该写那些比例:%q", tc.size, got)
			}
		case shotProportionMedium:
			for _, want := range []string{
				"the eyes are one eye-width apart",
				"the nose is about one third of the face length and the mouth about one fifth of it", // 三庭
				"the neck is slim and long",
				"the shoulders are about two head-widths wide",
			} {
				if !has(want) {
					t.Errorf("景别 %q(中景档)缺「%s」:%q", tc.size, want, got)
				}
			}
			if has("seven and a half heads") {
				t.Errorf("景别 %q(中景档)腰腿还看不见,不该写头身比:%q", tc.size, got)
			}
		case shotProportionFull:
			for _, want := range []string{
				"the eye line sits at the vertical middle of the head",
				"the eyes are one eye-width apart",
				"the nose is about one third of the face length and the mouth about one fifth of it",
				"the chin is about two thirds as wide as the cheekbones",
				"the neck is slim and long",
				"the shoulders are about two head-widths wide",
				"the figure is seven and a half heads tall with a slim waist and long legs",
			} {
				if !has(want) {
					t.Errorf("景别 %q(全身档)缺「%s」:%q", tc.size, want, got)
				}
			}
		}
		// 禁令:每一档都要有防「美人先验」的那一半。只有比值而没有禁令时,模型完全可以在
		// 「符合比例」的同时把整张脸往网红审美上挪(拉长脸、放大眼睛)。
		if !strings.Contains(strings.ToLower(body), "never lengthen the face and never enlarge the eyes") {
			t.Errorf("景别 %q 缺禁令(拉长脸/放大眼睛):%q", tc.size, got)
		}
	}
}

// TestShotProportionClauseReusesSkillWording 三档正文必须是**技能侧口径的逐字子集**,
// 而且是**纯英文短句**:不新增比值、不搬中文对照、不引技能侧没用过的说法。
//
// 为什么要钉:平台自己再编一套比值就等于第二个口径 —— 同一段提示词里两套比例时模型只会
// 取中值(见 proportion_clause.go 记的病根)。而「黄金比例」是用户抱怨时的原话,
// 技能侧从没用过它(它不是可执行的比值),写进提示词只会让模型自由发挥。
func TestShotProportionClauseReusesSkillWording(t *testing.T) {
	// 归一化:小写 + 去标点 + 压空白,用来做「逐字子集」判定
	norm := func(s string) string {
		s = strings.ToLower(s)
		s = strings.NewReplacer(".", " ", ";", " ", ",", " ", ":", " ").Replace(s)
		return strings.Join(strings.Fields(s), " ")
	}
	skillNorm := norm(skillProportionClause)

	for _, size := range []string{"特写", "近景", "中景", "中近景", "全景", "远景", "全身"} {
		c := shotProportionClause(size)
		if c == "" {
			t.Fatalf("景别 %q 应注入", size)
		}
		body := strings.TrimPrefix(c, shotProportionMarker)
		// ① **每一条**都能在技能侧比例句里逐字找到 —— 没另编比值
		pieces := strings.FieldsFunc(body, func(r rune) bool { return r == ';' || r == '.' })
		if len(pieces) < 5 {
			t.Errorf("景别 %q 的注入块条数太少(%d 条):%q", size, len(pieces), c)
		}
		for _, p := range pieces {
			if !strings.Contains(skillNorm, norm(p)) {
				t.Errorf("景别 %q 的这一条在技能侧比例句里找不到(平台另编了一套口径):%q", size, p)
			}
		}
		// ② 注入的正文必须能被「已有技能侧比例句」的判据认出来 —— 这正是本条的幂等来源
		if !HumanProportionClausePresent(c) {
			t.Errorf("景别 %q 注入的正文自己都不算「技能侧比例句」,判据与口径已经分叉:%q", size, c)
		}
		// ③ 纯英文、两句话、够短
		if i := strings.IndexFunc(c, func(r rune) bool { return r > 127 }); i >= 0 {
			t.Errorf("景别 %q 的注入块必须纯英文(不搬技能侧的中文对照):%q", size, c)
		}
		if n := strings.Count(c, "."); n != 2 {
			t.Errorf("景别 %q 应是「一句比例 + 一句禁令」两句,实际 %d 句:%q", size, n, c)
		}
		if len(c) > 620 {
			t.Errorf("景别 %q 的注入块太长(%d 字节)—— 镜头提示词已是 4k 量级,锚点必须短:%q", size, len(c), c)
		}
		// ④ 技能侧没用过的说法一律拒绝
		low := strings.ToLower(c)
		for _, bad := range []string{"golden ratio", "golden mean", "golden proportion", "黄金比例", "完美比例"} {
			if strings.Contains(low, bad) {
				t.Errorf("景别 %q 引用了技能侧没用过的说法 %q(那不是可执行的比值):%q", size, bad, c)
			}
		}
	}

	// 全身档必须含全部八条 —— 它才是「七头半」该出现的地方
	full := shotProportionClause("全景")
	for _, want := range []string{
		"the eye line sits at the vertical middle of the head",
		"the eyes are one eye-width apart",
		"the nose is about one third of the face length and the mouth about one fifth of it",
		"the chin is about two thirds as wide as the cheekbones",
		"the neck is slim and long",
		"the shoulders are about two head-widths wide",
		"the figure is seven and a half heads tall with a slim waist and long legs",
		"never lengthen the face and never enlarge the eyes",
	} {
		if !strings.Contains(full, want) {
			t.Errorf("全身档缺这一条「%s」:%q", want, full)
		}
	}
	// 三档必须互不相同,且全身档最长(它是唯一该写头身比的那一档)。
	// 注意**不**断言「脸档比中景短」:脸的五条里含下巴/颧骨与眼线,中景的简版反而更短 ——
	// 档位是「看得见什么」的分档,不是长度分档。
	face, mid := shotProportionClause("特写"), shotProportionClause("中景")
	if face == mid || face == full || mid == full {
		t.Error("三档必须是三份不同的正文")
	}
	if len(full) <= len(face) || len(full) <= len(mid) {
		t.Error("全身档最长(八条齐发)")
	}
}

// proportionShotFixture 一个「人类角色 + 场景」的镜头夹具(端到端用)。
func proportionShotFixture(size, h3 string) (*project.Project, *project.Plan, *project.Shot) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{
		Style:      "国漫二维动画",
		Characters: []project.Character{{ID: "c1", Name: "沈砚", Kind: "human", Description: "十八九岁的少年,短低马尾"}},
		Scenes:     []project.Scene{{ID: "s1", Name: "废件库", Description: "青砖大院"}},
	}
	shot := &project.Shot{ID: 1, Characters: []string{"c1"}, Scene: "s1", ShotSize: size, H3Prompt: h3}
	return proj, plan, shot
}

// TestShotRenderPromptInjectsProportionByShotSize 端到端:装配点按景别注入,**只注入一次**。
func TestShotRenderPromptInjectsProportionByShotSize(t *testing.T) {
	cases := []struct {
		size      string
		wantBody  string // 该景别正文里必须出现的一条
		forbidden string // 画面上看不见、不该出现在该档的一条
	}{
		{"特写", "the chin is about two thirds as wide as the cheekbones", "seven and a half heads"},
		{"近景", "the chin is about two thirds as wide as the cheekbones", "seven and a half heads"},
		{"中景", "the shoulders are about two head-widths wide", "seven and a half heads"},
		{"中近景", "the shoulders are about two head-widths wide", "seven and a half heads"},
		{"全景", "the figure is seven and a half heads tall with a slim waist and long legs", ""},
		{"远景", "the figure is seven and a half heads tall with a slim waist and long legs", ""},
		{"全身", "the figure is seven and a half heads tall with a slim waist and long legs", ""},
	}
	for _, tc := range cases {
		proj, plan, shot := proportionShotFixture(tc.size, "detailed_description:\n[Shot 1] …")
		got := ShotRenderPrompt(proj, plan, shot)
		if n := strings.Count(got, shotProportionMarker); n != 1 {
			t.Errorf("景别 %q:比例锚点应恰好注入一次,实际 %d 次:\n%s", tc.size, n, got)
		}
		if !strings.Contains(got, tc.wantBody) {
			t.Errorf("景别 %q 缺该档的比例条 %q:\n%s", tc.size, tc.wantBody, got)
		}
		if tc.forbidden != "" && strings.Contains(got, tc.forbidden) {
			t.Errorf("景别 %q 不该出现 %q(那一条画面上看不见):\n%s", tc.size, tc.forbidden, got)
		}
		// 锚点必须落在 shot.H3Prompt **之前**(它贴着画面,取最近注意力),且不得挤掉原有条款
		if strings.Index(got, shotProportionMarker) > strings.Index(got, "detailed_description:") {
			t.Errorf("景别 %q:比例锚点应贴在镜头正文之前:\n%s", tc.size, got)
		}
		for _, want := range []string{"【身份锚定", "STYLE AUTHORITY", "外貌不变项"} {
			if !strings.Contains(got, want) {
				t.Errorf("景别 %q:注入不得挤掉「%s」:\n%s", tc.size, want, got)
			}
		}
	}
}

// TestShotProportionInjectionGates 三道闸:认不出的景别不加、器物/空镜不加、Q 版不加。
//
// Q 版那一条尤其重要:它的比例**就是它这个类型本身**(二到三头身),同镜再写「七个半头」
// 就是两套比例,模型取中值 —— 既不 Q 版也不是真人比例,正是技能侧清单 §5.1 记的病根。
func TestShotProportionInjectionGates(t *testing.T) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()

	// ① 认不出的景别:不注入(宁可不加,也不要给一个错误景别的锚点)
	for _, size := range []string{"", "over-the-shoulder", "插入镜头"} {
		p, pl, shot := proportionShotFixture(size, "body")
		if got := ShotRenderPrompt(p, pl, shot); strings.Contains(got, shotProportionMarker) {
			t.Errorf("景别 %q 认不出来,不该注入:\n%s", size, got)
		}
	}
	// ② 纯器物镜头:比例写的是人体,注入等于把器物往人形上拽
	objPlan := &project.Plan{
		Style:      "国漫二维动画",
		Characters: []project.Character{{ID: "c2", Name: "阿凿", Kind: "object", Description: "一只插画铜雀,铆钉与玻璃眼"}},
		Scenes:     []project.Scene{{ID: "s1", Name: "废件库", Description: "青砖大院"}},
	}
	objShot := &project.Shot{ID: 2, Characters: []string{"c2"}, Scene: "s1", ShotSize: "全景", H3Prompt: "body"}
	if got := ShotRenderPrompt(proj, objPlan, objShot); strings.Contains(got, shotProportionMarker) {
		t.Errorf("纯器物镜头不得注入人体比例锚点(会把铜雀往人形上拽):\n%s", got)
	}
	// ③ 无人空镜:同理
	emptyShot := &project.Shot{ID: 3, Scene: "s1", ShotSize: "全景", H3Prompt: "an empty courtyard"}
	if got := ShotRenderPrompt(proj, objPlan, emptyShot); strings.Contains(got, shotProportionMarker) {
		t.Errorf("空镜不得注入人体比例锚点:\n%s", got)
	}
	// ④ Q 版角色:它自己的比例就是二到三头身
	chibiPlan := &project.Plan{
		Style:      "国漫二维动画",
		Characters: []project.Character{{ID: "c3", Name: "迷你沈砚", Kind: "chibi", Description: "沈砚的 Q 版化身"}},
		Scenes:     []project.Scene{{ID: "s1", Name: "废件库", Description: "青砖大院"}},
	}
	chibiShot := &project.Shot{ID: 4, Characters: []string{"c3"}, Scene: "s1", ShotSize: "全景", H3Prompt: "body"}
	if got := ShotRenderPrompt(proj, chibiPlan, chibiShot); strings.Contains(got, shotProportionMarker) {
		t.Errorf("Q 版镜头不得注入人体比例锚点(二到三头身与七个半头同现＝模型取中值):\n%s", got)
	}
	// ⑤ 人 + Q 版同镜:同样不发(两套比例的风险与纯 Q 版相同)
	mixedPlan := &project.Plan{
		Style: "国漫二维动画",
		Characters: []project.Character{
			{ID: "c1", Name: "沈砚", Kind: "human", Description: "十八九岁的少年"},
			{ID: "c3", Name: "迷你沈砚", Kind: "chibi", Description: "Q 版化身"},
		},
		Scenes: []project.Scene{{ID: "s1", Name: "废件库", Description: "青砖大院"}},
	}
	mixedShot := &project.Shot{ID: 5, Characters: []string{"c1", "c3"}, Scene: "s1", ShotSize: "全景", H3Prompt: "body"}
	if got := ShotRenderPrompt(proj, mixedPlan, mixedShot); strings.Contains(got, shotProportionMarker) {
		t.Errorf("Q 版同镜时不得注入人体比例锚点:\n%s", got)
	}
	// ⑥ 在册但**没有描述**的人类角色:比例是关于人体的,与平台手上有没有描述无关
	bare := &project.Plan{
		Style:      "国漫二维动画",
		Characters: []project.Character{{ID: "c9", Name: "无名", Kind: "human"}},
		Scenes:     []project.Scene{{ID: "s1", Name: "废件库", Description: "青砖大院"}},
	}
	bareShot := &project.Shot{ID: 6, Characters: []string{"c9"}, Scene: "s1", ShotSize: "中景", H3Prompt: "body"}
	if got := ShotRenderPrompt(proj, bare, bareShot); !strings.Contains(got, shotProportionMarker) {
		t.Errorf("在册的人类角色(哪怕没有描述)也该拿到比例锚点:\n%s", got)
	}
}

// TestShotProportionIsIdempotent 幂等 + 不重复。
//
// 两种「跑两次」的形状都要钉:
//   - 同一个 shot 连跑两次,结果逐字相同;
//   - **已带锚点的提示词**再进这条路(把上一次的装配结果当 H3Prompt),不再叠加第二份。
//
// 第二条正是「复用 proportion_clause.go 判据」的直接后果:注入的正文自己带着技能侧的
// 特征短语,所以它会被判成「已有技能侧比例句」。技能侧若已把比例句写进 h3_prompt,
// 走的也是这条路 —— 平台不加,原句逐字保留(平台不删、不改它的定义权)。
func TestShotProportionIsIdempotent(t *testing.T) {
	proj, plan, shot := proportionShotFixture("全景", "detailed_description:\n[Shot 1] …")

	once := ShotRenderPrompt(proj, plan, shot)
	twice := ShotRenderPrompt(proj, plan, shot)
	if once != twice {
		t.Errorf("同一 shot 连跑两次结果必须逐字相同:\n%s\n---\n%s", once, twice)
	}
	if n := strings.Count(once, shotProportionMarker); n != 1 {
		t.Errorf("比例锚点必须恰好一份,实际 %d 份", n)
	}
	// 把上一次的装配结果当正文再装配一次:判据应认出「已有比例句」,不再叠加
	again := ShotRenderPrompt(proj, plan, &project.Shot{ID: 1, Characters: []string{"c1"}, Scene: "s1",
		ShotSize: "全景", H3Prompt: once})
	if n := strings.Count(again, shotProportionMarker); n != 1 {
		t.Errorf("已带锚点的提示词再装配不得叠加第二份,实际 %d 份", n)
	}

	// 技能侧自己把比例句写进 h3_prompt:平台不加,原句逐字保留
	skillShot := &project.Shot{ID: 1, Characters: []string{"c1"}, Scene: "s1", ShotSize: "全景",
		H3Prompt: "detailed_description:\n[Shot 1] …\n" + skillProportionClause}
	got := ShotRenderPrompt(proj, plan, skillShot)
	if strings.Contains(got, shotProportionMarker) {
		t.Errorf("h3_prompt 里已有技能侧比例句时,平台不得再注入自己那份:\n%s", got)
	}
	for _, want := range []string{
		"the eye line sits at the vertical middle of the head",
		"the eyes are one eye-width apart",
		"the figure is seven and a half heads tall",
		"Never distort these ratios",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("技能侧比例句必须逐字保留(平台不重写它的定义权),缺「%s」:\n%s", want, got)
		}
	}
}

// TestShotProportionDoesNotTouchPortraitChain 越界守卫:定妆照那条链**不得**拿到镜头锚点。
//
// 镜头锚点带着「identical in every shot」(它是逐镜一致性的约束),跑到定妆照上就成了一条
// 管不着的东西;而定妆照那条链的比例逻辑上一轮刚做过「让位」(卡里已有技能侧比例句时
// 平台自己的比例措辞退让),不能被这里的注入搅乱。
func TestShotProportionDoesNotTouchPortraitChain(t *testing.T) {
	proj, plan := humanProj("Q版呆萌, 国漫二维")
	ch := project.Character{ID: "c1", Name: "沈砚",
		ImagePrompt: "Clean anime-style face. plain pure white seamless background, upper-body framing"}
	sheet := PortraitPromptForProject(proj, plan, ch)
	if strings.Contains(sheet, shotProportionMarker) || strings.Contains(sheet, "seven and a half heads") {
		t.Errorf("定妆照提示词不得出现镜头侧的比例锚点(那是逐镜约束):\n%s", sheet)
	}
	// 上一轮的不变量:卡里已有技能侧比例句时,平台自己的比例措辞让位
	ch2 := project.Character{ID: "c2", Name: "沈砚",
		ImagePrompt: "Clean anime-style face. " + skillProportionClause}
	sheet2 := PortraitPromptForProject(proj, plan, ch2)
	if strings.Contains(sheet2, "cute chibi proportions") {
		t.Errorf("让位行为被搅乱:卡里已有技能侧比例句时平台仍追加了自己的比例措辞:\n%s", sheet2)
	}
}
