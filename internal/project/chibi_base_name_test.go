package project

import "testing"

// Q 版卡的**命名写法**要一次认全（2026-09-29 用户第二次报同一件事：
// 「你看一下下面的判断逻辑咋还没有改好？」——真机报的是
// 「Q版碑生（卡片 char_q_bisheng）」与「碑生（丙十七）（卡片 char_bisheng）」像同一个人）。
//
// 那次技能侧用的是 **`char_q_<名字>` + 名字「Q版<名字>」前缀**，而当时的判据只认
// `chibi_<本体>` / `<本体>_chibi` 与「<本体>·Q版」后缀 ⇒ 判据全部落空。
// 教训：**判据认的写法比实际写法少**。三种写法现在都收在 ChibiBaseName 里。
func TestChibiBaseNameRecognisesEveryNamingForm(t *testing.T) {
	cases := []struct {
		id, name, want string
		why            string
	}{
		{"char_q_bisheng", "Q版碑生", "碑生", "真机形态：id 前缀 q_ + 名字前缀「Q版」"},
		{"chibi_char_bisheng", "碑生·Q版", "碑生", "技能侧老写法：id chibi_ 前缀 + 名字 ·Q版 后缀"},
		{"char_bisheng_chibi", "碑生", "bisheng", "id 后缀 _chibi（名字没标记 ⇒ 只能从 id 取到 slug）"},
		{"char_x", "碑生·Q版（肩上巴掌大的自己）", "碑生", "括注要先剥掉"},
		{"char_x", "Q版 钟离", "钟离", "名字里带空格"},
		{"char_bisheng", "碑生（丙十七）", "", "普通角色不是 Q 版"},
		{"char_shenyan", "沈砚·少年", "", "造型档不是 Q 版"},
	}
	for _, c := range cases {
		if got := ChibiBaseName(c.id, c.name); got != c.want {
			t.Errorf("ChibiBaseName(%q, %q) = %q；期望 %q（%s）", c.id, c.name, got, c.want, c.why)
		}
	}
}
