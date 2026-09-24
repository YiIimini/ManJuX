package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// TestRelatedSuffixName 库里关联图 → 项目侧命名的映射。
//
// 回归 2026-09-12:渲染时 turnaroundPath() 是按**主图主名**拼
// `<base>_turnaround.png` 找第二张参考的,而库里的角度图文件名来自最初入库
// 那个项目的命名(characters_char_张三_view_side.png)。库复用若照抄文件名,
// 项目里明明有转台图却永远匹配不上 —— 角色在 H3 里依旧只有一张正面照。
func TestRelatedSuffixName(t *testing.T) {
	const base = "characters_char_c1"
	cases := []struct{ in, want string }{
		{"characters_char_c1_view_side.png", base + "_view_side.png"},
		{"characters_char_c1_view_threequarter.png", base + "_view_threequarter.png"},
		{"characters_char_c1_view_back.png", base + "_view_back.png"},
		{"characters_char_c1_turnaround.png", base + "_turnaround.png"},
		// 库内自己生成的(主名 portrait)同样要能被认出来
		{"portrait_view_side.png", base + "_view_side.png"},
		{"portrait_turnaround.png", base + "_turnaround.png"},
		// 认不出的关联图原样保留,宁可多一个文件也不能丢角度
		{"weird_extra.png", "weird_extra.png"},
	}
	for _, c := range cases {
		if got := RelatedSuffixName(base, c.in); got != c.want {
			t.Errorf("RelatedSuffixName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCopyAssetRelatedKeepsTurnaroundName 库复用拷贝后,项目里必须存在
// turnaroundPath() 能推导出来的那个文件名 —— 这是「渲染能直接调用多视角」的
// 唯一判据。
func TestCopyAssetRelatedKeepsTurnaroundName(t *testing.T) {
	libDir := t.TempDir()
	destDir := t.TempDir()
	const base = "characters_char_c1"
	related := []string{
		filepath.Join(libDir, "characters_char_c1_view_side.png"),
		filepath.Join(libDir, "characters_char_c1_turnaround.png"),
	}
	for _, f := range related {
		if err := os.WriteFile(f, []byte("img:"+filepath.Base(f)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	related = append(related, filepath.Join(libDir, "missing.png")) // 读不到的文件必须被跳过而不是中断
	if n := copyAssetRelated(related, destDir, base); n != 2 {
		t.Fatalf("应复制 2 张,实际 %d 张", n)
	}
	// 项目侧主图(仅占位,用于让 turnaroundPath 推导相对路径)
	master := filepath.Join(destDir, base+".png")
	if err := os.WriteFile(master, []byte("portrait"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(destDir, master)
	if got := turnaroundPath(destDir, filepath.ToSlash(rel)); got == "" {
		t.Fatal("拷贝进来的转台图必须能被 turnaroundPath 找到(否则渲染用不上多视角)")
	}
}

// TestGenerateTurnaroundValidatesMaster 未给/给定不可读的定妆照时必须立刻返回
// 错误,而不是把 nil 引擎带进渲染流程(调用方要据此告警)。
func TestGenerateTurnaroundValidatesMaster(t *testing.T) {
	if _, err := GenerateTurnaround(context.Background(), nil, nil, TurnaroundOptions{Name: "空路径"}); err == nil {
		t.Fatal("缺定妆照路径应报错")
	}
	if _, err := GenerateTurnaround(context.Background(), nil, nil, TurnaroundOptions{
		MasterPath: filepath.Join(t.TempDir(), "不存在.png"), Name: "坏路径",
	}); err == nil {
		t.Fatal("定妆照不可读应报错")
	}
}

// TestAssetViewsDefaultOn 多视角补齐全开关的语义:老项目 json 里没有这个键
// (nil)必须按**开**处理 —— 存量项目里角色只有正面照正是用户报的问题,
// 默认关掉等于新能力对存量项目完全不可见。
func TestAssetViewsDefaultOn(t *testing.T) {
	var a project.AssetsParams
	if !a.ViewsOn() {
		t.Error("未表态(nil)应视为开启多视角补齐")
	}
	off := false
	a.Views = &off
	if a.ViewsOn() {
		t.Error("显式关闭时应视为关闭")
	}
	if !project.DefaultStageParams().Assets.ViewsOn() {
		t.Error("新建项目默认应开启多视角补齐")
	}
}

// TestEnsureChineseDialogue 台词语言标记兜底。
//
// 回归 2026-09-12(用户报「配音不能有听不懂的鸟语混杂进去」):提示词模板要求
// <d>[Chinese]台词</d>,但 LLM 直出时经常漏标记(实测该书的旁白全是 <d>断的、折的…</d>),
// H3 拿不到语言线索就自己猜语言,成片里就出现听不懂的杂音。代码必须兜住。
func TestEnsureChineseDialogue(t *testing.T) {
	in := "The narrator (S1) says: <d>断的、折的、锈的，全记在这本子上。</d>\n" +
		"<Subject 1> (S1) says in a rough voice: <d>English line here.</d>"
	out := EnsureChineseDialogue(in)
	if strings.Contains(out, "<d>断的") {
		t.Errorf("缺标记的台词必须补 [Chinese]:%s", out)
	}
	if strings.Contains(out, "[English]") {
		t.Errorf("非中文语言标记必须被替换:%s", out)
	}
	if n := strings.Count(out, "<d>[Chinese]"); n != 2 {
		t.Errorf("两段台词都应带标记,实际 %d 处:%s", n, out)
	}
	if !strings.Contains(out, "MANDARIN ONLY") {
		t.Error("有台词时必须点明只许普通话")
	}
	// 幂等:重复处理不得叠加标记或重复追加语言约束
	again := EnsureChineseDialogue(out)
	if strings.Count(again, "[Chinese][Chinese]") > 0 {
		t.Errorf("重复处理不得叠加标记:%s", again)
	}
	if strings.Count(again, "MANDARIN ONLY") != 1 {
		t.Errorf("语言约束只应出现一次,实际 %d 次", strings.Count(again, "MANDARIN ONLY"))
	}
	// 无台词的提示词原样返回(不该被塞语言约束)
	silent := "integrated_multimodal_description:\nno dialogue here"
	if EnsureChineseDialogue(silent) != silent {
		t.Error("无台词镜头不应被追加语言约束")
	}
}

// TestAnchorPromptLocksIdentityToPicture 锚定块必须把身份锁在参考图上。
//
// 回归 2026-09-12:旧锚定写「外貌描述优先级最高,任何画面细节与之冲突时以此为准」,
// 等于让模型忘掉参考图按文字重画 —— 于是同一批角色的脸趋同、文字里的痣/红鼻头
// 出现在成片里而定妆照没有(用户报「脸部相似仍存在」「定妆照没有痣视频里却有」
// 「定妆照与多位图不一致」)。
func TestAnchorPromptLocksIdentityToPicture(t *testing.T) {
	plan := &project.Plan{
		Style: "next-gen 3D CG",
		Characters: []project.Character{
			{ID: "c1", Name: "张屠", Description: "圆胖脸,嘴角边一颗黑痣,褐色短褂"},
		},
		Scenes: []project.Scene{{ID: "s1", Name: "废件库", Description: "青砖大院"}},
	}
	shot := &project.Shot{ID: 1, Characters: []string{"c1"}, Scene: "s1", H3Prompt: "detailed_description:\n[Shot 1] ..."}
	got := anchorPrompt(plan, shot, true)
	if strings.Contains(got, "外貌描述优先级最高") {
		t.Error("旧文案必须删掉:它让模型按文字重画脸")
	}
	for _, want := range []string{"身份锁", "照抄参考图", "禁止添加参考图里没有的面部标记", "张屠", "废件库"} {
		if !strings.Contains(got, want) {
			t.Errorf("锚定块缺少「%s」:\n%s", want, got)
		}
	}
	// 2026-09-24:角色动漫化 + 哑光(去"油腻")硬约束必须进视频侧提示词。
	// 只压定妆照不够 —— H3 会照着强光描述把哑光角色重新画成油光脸。
	for _, want := range []string{"MATTE ANIMATED LOOK", "oily or greasy skin", "resemblance to a real person"} {
		if !strings.Contains(got, want) {
			t.Errorf("视频侧画风硬约束缺少「%s」:\n%s", want, got)
		}
	}
	// 关掉该开关时不得注入(真人剧是合法路线,不能强加画风)
	if off := anchorPrompt(plan, shot, false); strings.Contains(off, "MATTE ANIMATED LOOK") {
		t.Error("关闭角色动漫化后不应再注入动画/哑光硬约束")
	}
	// 文字仍要传给模型(供服装/道具/姿态用),但必须标明用途受限
	if !strings.Contains(got, "嘴角边一颗黑痣") {
		t.Error("原描述应保留(服装/道具/状态仍需要它)")
	}
	if !strings.Contains(got, "不得用来改写长相") {
		t.Error("必须显式限定文字不得改写长相")
	}
}

// TestCompactIdentityStripsFacialWords 转身视频的身份锚点不得携带面部/皮肤形容词。
//
// 回归 2026-09-12(用户报「多位图有小红鼻和小红脸」):旧版把定妆照提示词整段塞进
// 转身提示词,描述里的"小红鼻子"被模型当成绘图指令补了出来,而定妆照本身并没有;
// 同时"圆钝""大眼睛"这类词把成年角色拉成 Q 版娃娃。
func TestCompactIdentityStripsFacialWords(t *testing.T) {
	in := "chubby face, thick puffy eyelids over two squinting small eyes, broad flat nose, a distinct black mole at the corner of his mouth, rosy cheeks, greasy dark hair up in a small topknot, brown short jacket, in next-gen 3D CG style, plain background, upper-body framing, front-facing"
	got := compactIdentity(in)
	low := strings.ToLower(got)
	for _, bad := range []string{"mole", "rosy", "blush", "nose", "eyes", "eyelid", "cheek", "face", "skin", "upper-body", "front-facing", "plain background"} {
		if strings.Contains(low, bad) {
			t.Errorf("身份锚点不应包含「%s」:%s", bad, got)
		}
	}
	for _, keep := range []string{"brown short jacket", "topknot"} {
		if !strings.Contains(low, keep) {
			t.Errorf("服装/发型锚点应保留「%s」:%s", keep, got)
		}
	}
	if len([]rune(got)) > 220 {
		t.Errorf("锚点应短小,实际 %d 字", len([]rune(got)))
	}
}

// TestCompactIdentityDropsTrailingOrphans 整词删除后**句尾**不能留下孤儿修饰语。
//
// 回归 2026-09-24:剥掉 "chin" 后句子变成 "a chinese young man with and a long" ——
// 悬空的 "long" 会被模型接到别处(把脸画长),正好制造我们要消除的身份漂移;
// 中部内容一个词都不许动(服装/发型/族群都是身份锚)。
func TestCompactIdentityDropsTrailingOrphans(t *testing.T) {
	got := compactIdentity("a Chinese young man with rosy cheeks and a long chin")
	if got != "a chinese young man" {
		t.Errorf("句尾孤儿词应被剥净,期望 %q,实际 %q", "a chinese young man", got)
	}
	// 中部锚点不受影响。
	mid := compactIdentity("a Chinese young man with a distinct black mole, greasy dark hair up in a small topknot, brown short jacket")
	low := strings.ToLower(mid)
	for _, keep := range []string{"brown short jacket", "topknot"} {
		if !strings.Contains(low, keep) {
			t.Errorf("中部锚点应保留「%s」:%s", keep, mid)
		}
	}
	if strings.HasSuffix(strings.TrimSpace(low), "with") {
		t.Errorf("句尾不该留下连接词:%s", mid)
	}
}

// TestUniformCivilianClarify 民用制服"去警用化"说明只在相关角色上追加。
//
// 回归 2026-09-12(用户实证「保安制服不要渲染的和警察服一样啊」):中式保安服与警服
// 相似,模型先验直接把"黑制服 + 对讲机"画成警服(实测出了肩章星徽与带字胸牌)。
// Krea-2 是蒸馏模型(CFG 1.0)、负面词不生效,所以用正面具体的描述把民用工装讲清楚,
// 并且**只对命中关键词的角色追加**,不干扰其它角色。
func TestUniformCivilianClarify(t *testing.T) {
	cases := []struct {
		desc string
		want bool
	}{
		{"三十五六岁,壮实方脸…黑保安制服外挂一只对讲机", true},
		{"小区门卫,灰制服", true},
		{"夜班看守,深色工装", true},
		{"a plain black security uniform with a walkie-talkie", true},
		{"二十七八岁,清瘦,长脸窄下巴,细长单眼皮…灰蓝短袖工装衬衫", false},
		{"六十多岁,佝偻瘦削…灰夹克、深灰裤子、旧胶鞋", false},
	}
	for _, c := range cases {
		got := uniformCivilianClarify(c.desc)
		if (got != "") != c.want {
			t.Errorf("desc=%q 命中判定错误(got 长度 %d, want 命中=%v)", c.desc, len(got), c.want)
		}
	}
	// 命中时必须是可执行的正向描述,且**不含**否定式列举(那会把警服元素画出来)
	got := uniformCivilianClarify("黑保安制服外挂一只对讲机")
	for _, bad := range []string{"no epaulettes", "no metal", "no badge"} {
		if strings.Contains(got, bad) {
			t.Errorf("去警用化说明不得使用否定式列举(%s):会把该元素画出来", bad)
		}
	}
	// 必须是**正面**表述:不得出现"不要肩章/警徽"这类否定列举(蒸馏模型会照着词画)
	for _, want := range []string{"CIVILIAN WORKWEAR", "work shirt", "lanyard", "shop-floor", "普通民用工装"} {
		if !strings.Contains(got, want) {
			t.Errorf("去警用化说明缺少「%s」", want)
		}
	}
}

// TestNormalizeUniformWording 名词短语层面的去警用化。
//
// 只加"这是民用工装"的补充段不够(实测画面照旧画出肩章与胸徽)—— 触发警服先验的是
// "black security uniform / 黑保安制服"这个名词短语本身,必须把它换成具体民用描述。
func TestNormalizeUniformWording(t *testing.T) {
	cn := normalizeUniformWording("三十五六岁…黑保安制服外挂一只对讲机")
	if strings.Contains(cn, "保安制服") {
		t.Errorf("中文名词短语应被替换:%s", cn)
	}
	if !strings.Contains(cn, "民用保安工装") || !strings.Contains(cn, "布质名牌") {
		t.Errorf("替换后应给出具体民用工装描述:%s", cn)
	}
	en := normalizeUniformWording("burly man, black security uniform with a walkie-talkie")
	if strings.Contains(strings.ToLower(en), "security uniform") {
		t.Errorf("英文名词短语应被替换:%s", en)
	}
	if !strings.Contains(en, "civilian work shirt") {
		t.Errorf("替换后应给出具体民用描述:%s", en)
	}
	// 与制服无关的角色描述不得被改动
	plain := "二十七八岁,清瘦,长脸窄下巴,灰蓝短袖工装衬衫"
	if normalizeUniformWording(plain) != plain {
		t.Errorf("无关描述不应被改动:%s", normalizeUniformWording(plain))
	}
}

// TestCompactIdentityKeepsEthnicity 剥离面部词必须**整词**匹配,不得削掉族群词。
//
// 回归 2026-09-12(用户「转台图和多位图咋生成外国人了,还与正面图不一样啊」):
// 上一版用 strings.ReplaceAll 扣面部词,`chin` 把 `Chinese man` 削成 `ese man` ——
// 族群信息一丢,转身视频立刻把角色画成外国人(用户当场发现)。整词边界同时避免
// chin→machine / lip→clip / face→surface 这类误伤。
func TestCompactIdentityKeepsEthnicity(t *testing.T) {
	in := "Chinese man in his late twenties, slim build, long narrow face with a sharp chin, " +
		"high brow ridge, narrow single-lidded cold calm eyes, slightly messy short black hair, " +
		"faded grey-blue short-sleeve work shirt, upper-body framing, front-facing"
	got := compactIdentity(in)
	low := strings.ToLower(got)
	if !strings.Contains(low, "chinese") {
		t.Errorf("族群词必须保留(否则会画成外国人):%q", got)
	}
	// 注意:不能简单查 "ese man" —— "chinese man" 本身就包含它;只查"开头被削"这种真错法
	if strings.HasPrefix(low, "ese ") {
		t.Errorf("族群词被削掉了(chinese→ese):%q", got)
	}
	for _, bad := range []string{" chin ", " chin,", "eyes", " face ", "lips", "nose"} {
		if strings.Contains(low, bad) {
			t.Errorf("面部词未被整词剥掉(残留 %q):%q", bad, got)
		}
	}
	// 服装/发型/年龄锚点要留住,残句要清干净
	for _, keep := range []string{"work shirt", "black hair", "late twenties"} {
		if !strings.Contains(low, keep) {
			t.Errorf("身份锚点应保留 %q:%q", keep, got)
		}
	}
	if strings.Contains(got, " ,") || strings.Contains(got, ",,") {
		t.Errorf("删除后面部词后不得留下残句标点:%q", got)
	}
}
