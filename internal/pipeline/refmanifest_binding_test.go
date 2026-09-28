package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// subject ↔ picture 绑定的宽窄两难(审计:绑定判据依赖「名字紧跟 is」,绑不上就静默退回)。
//
// 官方全参考模式要求 `<Subject N> is <角色名> in <Picture K>`;而句子是 LLM 写的,
// 完全可能写成 "…is a slim seventeen-year-old boy named 沈砚, …"。旧实现把 is 与第一个
// 逗号之间整段当名字去精确比对 → 必然 miss → 原样返回、不绑图、不打日志,于是
// "补官方句式"这条修复(2026-09-26 真机根因修复)整条静默失效,回到
// 「模型按文字重画、同一个沈砚每镜不同人」。
func TestSubjectBindingAcceptsNaturalSentences(t *testing.T) {
	roles := []RefRole{
		{Picture: 1, Kind: "character_front", Name: "沈砚", ID: "char_01"},
		{Picture: 2, Kind: "scene", Name: "临水城", ID: "scene_01"},
	}
	cases := []struct {
		name     string
		prompt   string
		wantLine string
	}{
		{
			name:     "官方句式(名字紧跟 is)",
			prompt:   "subject_definitions:\n<Subject 1> is 沈砚（human）— 十八九岁、瘦 —— 以附图为准。\n",
			wantLine: "<Subject 1> is 沈砚 in <Picture 1>（human）— 十八九岁、瘦 —— 以附图为准。",
		},
		{
			name:     "自然句(名字在描述中间)",
			prompt:   "subject_definitions:\n<Subject 1> is a slim seventeen-year-old boy named 沈砚, with a short low ponytail.\n",
			wantLine: "named 沈砚 in <Picture 1>, with a short low ponytail.",
		},
		{
			name:     "全角括号紧跟名字",
			prompt:   "subject_definitions:\n<Subject 2> is 临水城（the town）— 河港小镇。\n",
			wantLine: "<Subject 2> is 临水城 in <Picture 2>（the town）— 河港小镇。",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bindSubjectsToPictures(tc.prompt, roles)
			if !strings.Contains(got, tc.wantLine) {
				t.Fatalf("应绑成 %q,实际:\n%s", tc.wantLine, got)
			}
			// 幂等:再绑一次不得叠出第二个 in <Picture
			again := bindSubjectsToPictures(got, roles)
			if n := strings.Count(again, "in <Picture"); n != strings.Count(got, "in <Picture") {
				t.Errorf("绑定必须幂等,实际 %d → %d", strings.Count(got, "in <Picture"), n)
			}
		})
	}
}

// 名字真的对不上时不许硬塞编号(那比不绑更坏:会把别人的脸绑到这一行上)。
func TestSubjectBindingRefusesUnknownName(t *testing.T) {
	roles := []RefRole{{Picture: 1, Kind: "character_front", Name: "沈砚", ID: "char_01"}}
	in := "subject_definitions:\n<Subject 1> is 路人甲 — 无参考图。\n"
	if got := bindSubjectsToPictures(in, roles); strings.Contains(got, "in <Picture") {
		t.Errorf("名字对不上时不许猜编号:\n%s", got)
	}
	// 名字出现在很后面(超出窗口)也不绑 —— 那不是"这一行讲的是谁"
	long := "subject_definitions:\n<Subject 1> is " + strings.Repeat("很长的描述文字。", 12) + "沈砚。\n"
	if got := bindSubjectsToPictures(long, roles); strings.Contains(got, "in <Picture") {
		t.Errorf("名字远在描述尾巴上时不该认定为这一行的主体:\n%s", got)
	}
}

// 命中多个名字时取最长者(短名是长名的子串时不能抢)。
func TestSubjectBindingPrefersLongestName(t *testing.T) {
	roles := []RefRole{
		{Picture: 1, Kind: "character_front", Name: "苏晚", ID: "char_01"},
		{Picture: 2, Kind: "character_front", Name: "苏晚晚", ID: "char_02"},
	}
	got := bindSubjectsToPictures("subject_definitions:\n<Subject 1> is 苏晚晚（human）— 妹妹。\n", roles)
	if !strings.Contains(got, "苏晚晚 in <Picture 2>") {
		t.Fatalf("应命中更长的名字(苏晚晚 → Picture 2),实际:\n%s", got)
	}
}

// subjectBindStats:一行都没绑上时必须能量出来(调用方据此告警,不让它静默)。
func TestSubjectBindStats(t *testing.T) {
	roles := []RefRole{
		{Picture: 1, Kind: "character_front", Name: "沈砚", ID: "char_01"},
		{Picture: 2, Kind: "character_front", Name: "苏晚", ID: "char_02"},
	}
	bound, total := subjectBindStats("subject_definitions:\n<Subject 1> is 沈砚（human）\n<Subject 2> is 苏晚（human）\n", roles)
	if bound != 2 || total != 2 {
		t.Errorf("两行都能绑上,实际 %d/%d", bound, total)
	}
	bound, total = subjectBindStats("subject_definitions:\n<Subject 1> is 沈砚（human）\n<Subject 2> is the tall man in grey\n", roles)
	if bound != 1 || total != 2 {
		t.Errorf("应量出 1/2,实际 %d/%d", bound, total)
	}
	bound, total = subjectBindStats("subject_definitions:\n<Subject 1> is the woman\n", roles)
	if bound != 0 || total != 1 {
		t.Errorf("一行都没绑上时应是 0/1,实际 %d/%d", bound, total)
	}
	if b, tt := subjectBindStats("subject_definitions:\n无此行\n", roles); b != 0 || tt != 0 {
		t.Errorf("没有 subject 行时应是 0/0,实际 %d/%d", b, tt)
	}
}

// 端到端:EnsureRefManifest 仍要附清单,并且绑定句式真的落进提示词。
func TestEnsureRefManifestBindsAndKeepsManifest(t *testing.T) {
	roles := []RefRole{{Picture: 1, Kind: "character_front", Name: "沈砚", ID: "char_01"}}
	in := "subject_definitions:\n<Subject 1> is a slim boy named 沈砚, with a low ponytail.\n"
	shot := project.Shot{ID: 6}
	out := EnsureRefManifest(in, shot, roles)
	if !strings.Contains(out, "named 沈砚 in <Picture 1>") {
		t.Errorf("自然句也要绑上图:\n%s", out)
	}
	if !strings.Contains(out, refManifestKey) {
		t.Errorf("权威清单必须仍在:\n%s", out)
	}
	if !strings.HasPrefix(out, "subject_definitions:") {
		t.Error("原文不得被吃掉(清单是并列其后的一段结构性声明)")
	}
}

// 【2026-09-28 用户报 · 照真实数据写的回归】角色卡名带括号注解，而 LLM 只写其中一半。
//
// 实测：角色卡名 `碑生（丙十七）`，LLM 在 subject_definitions 里写的是
// `<Subject 1> is 丙十七, a twenty-year-old stele-quarry youth: …`。
// 整名对不上 ⇒ authorityPictures 里没有一个 key 命中 ⇒ **一行都没能绑到参考图**
// （渲染阶段那条告警）⇒ 模型按文字重画 ⇒ 同一角色逐镜不同人。
func TestSubjectBindingMatchesNameAnnotations(t *testing.T) {
	roles := []RefRole{
		{Picture: 1, Kind: "character_front", Name: "碑生（丙十七）", ID: "char_bingsheng"},
		{Picture: 2, Kind: "character_front", Name: "邢五", ID: "char_xingwu"},
		{Picture: 3, Kind: "scene", Name: "西坡碑林", ID: "scene_beilin"},
	}
	cases := []struct{ name, prompt, want string }{
		{
			name:   "提示词只写括号里的注解",
			prompt: "subject_definitions:\n<Subject 1> is 丙十七, a twenty-year-old stele-quarry youth.\n",
			want:   "<Subject 1> is 丙十七 in <Picture 1>,",
		},
		{
			name:   "提示词只写括号前的主名",
			prompt: "subject_definitions:\n<Subject 1> is 碑生, a stele-quarry youth.\n",
			want:   "<Subject 1> is 碑生 in <Picture 1>,",
		},
		{
			name:   "整名照旧（且按最长者胜绑整名）",
			prompt: "subject_definitions:\n<Subject 1> is 碑生（丙十七）— 石场少年。\n",
			want:   "<Subject 1> is 碑生（丙十七） in <Picture 1>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bindSubjectsToPictures(tc.prompt, roles); !strings.Contains(got, tc.want) {
				t.Fatalf("应绑成含 %q，实际:\n%s", tc.want, got)
			}
		})
	}
	// 反向：名字真不在这一行里时不许硬塞编号（把碑生的脸绑到别人行上比不绑更坏）
	miss := bindSubjectsToPictures("subject_definitions:\n<Subject 2> is 一个没登记的路人, walking.\n", roles)
	if strings.Contains(miss, "in <Picture") {
		t.Errorf("对不上的行不许硬塞编号：%s", miss)
	}
	// 别名展开只做去括号这一种，别把真不同的角色混成一个
	if aliases := nameAliases("碑生（丙十七）"); len(aliases) != 3 {
		t.Errorf("`碑生（丙十七）` 应展开为 3 个写法（整名/主名/注解），实际 %v", aliases)
	}
	if aliases := nameAliases("邢五"); len(aliases) != 1 {
		t.Errorf("没有括号时只该有整名，实际 %v", aliases)
	}
	if aliases := nameAliases("小乔（甲）"); len(aliases) != 2 {
		t.Errorf("单字注解太容易误伤，不该当别名，实际 %v", aliases)
	}
}
