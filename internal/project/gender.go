package project

import (
	"fmt"
	"strings"
)

// gender.go 角色卡的**性别**（2026-09-29 用户：「钟离渲染成男的？」）。
//
// ## 为什么要有它
//
// 卡面上没有性别字段时，出图模型**自己猜**。实测「钟离」（守钟女，卡面只写"外表 17–18、
// 清瘦挺直、八头身"）被画成了男性 —— 名字对模型偏男性先验（同名的另一位知名角色也是男的），
// 而提示词里没有任何一句说这是女人。
//
// 与"国风的脸必须是中国人的脸"是同一类错：**身份属性没被点名，模型就按默认先验走**。
//
// ## 口径
//
//   - 锚表/卡面写了 `性别：女`（技能侧 2026-09-29 起补这个字段）→ 直接采信；
//   - 没写字段但文中有**强**性别词（少女/女子/妇人/汉子/老汉…）→ 推断；
//   - 两者都没有 → **报出来**（`GenderIssues`），让补字段，而不是让模型猜。
//
// 只认"人形"三类（人/人形机械/Q 版）：器物、灵宠、妖兽、无实体没有性别这回事。

// genderFemale / genderMale 强性别词（**弱词不收**：「少年」「青年」在不同文本里男女都用过，
// 拿它推断会把女性角色判成男性 —— 宁可报"没写"，也不许猜错）。
var (
	genderFemale = []string{"少女", "女子", "女性", "姑娘", "妇人", "妇人", "少妇", "老妇", "婆婆", "大妈", "女人", "女孩", "女童", "她"}
	genderMale   = []string{"男子", "男性", "汉子", "老汉", "老头", "大爷", "少年郎", "男人", "男孩", "男童", "他"}
)

// GenderOf 取这张卡的性别（"女"/"男"；取不到返回空串与 false）。
//
// 顺序：显式字段 `性别：X` → 强性别词。字段优先，因为那是作者写的。
func GenderOf(desc, imagePrompt string) (string, bool) {
	hay := desc + "\n" + imagePrompt
	for _, ln := range strings.Split(hay, "\n") {
		k, v, ok := splitAnchorKV(ln)
		if !ok || !strings.Contains(k, "性别") {
			continue
		}
		switch {
		case strings.Contains(v, "女"):
			return "女", true
		case strings.Contains(v, "男"):
			return "男", true
		}
	}
	for _, w := range genderFemale {
		if strings.Contains(hay, w) {
			return "女", true
		}
	}
	for _, w := range genderMale {
		if strings.Contains(hay, w) {
			return "男", true
		}
	}
	return "", false
}

// GenderClause 定妆照/视频提示词里的**性别声明**（取不到性别时返回空串）。
//
// 措辞正向为主 + 一句排除：模型猜错过一次（钟离 → 男），所以要写得没有余地。
func GenderClause(gender string) string {
	switch gender {
	case "女":
		return "\n- GENDER (mandatory): this character is a WOMAN — the face, build, hair styling and costume all " +
			"read as a woman of the stated age; do not render this character as male.\n"
	case "男":
		return "\n- GENDER (mandatory): this character is a MAN — the face, build, hair styling and costume all " +
			"read as a man of the stated age; do not render this character as female.\n"
	}
	return ""
}

// GenderIssues 方案里**没写性别**的人形角色（门用；返回人能读懂的一句句）。
//
// 只报人形三类：器物/灵宠/妖兽/无实体没有性别。
func GenderIssues(p *Plan) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, c := range p.Characters {
		if !isHumanlikeKind(c) {
			continue
		}
		if _, ok := GenderOf(c.Description, c.ImagePrompt); ok {
			continue
		}
		out = append(out, fmt.Sprintf("角色「%s」没写性别（卡面与锚表里都没有）—— 出图模型会自己猜，实测「钟离」就被画成了男性；"+
			"请在锚表这一节补一行 `性别：女`（或 `性别：男`）", c.Name))
	}
	return out
}

// isHumanlikeKind 会不会有性别（人 / 人形机械 / Q 版）。
//
// 用**字符串词表**判（`EntityKind` 那套类型在 pipeline 包里，project 不能反向依赖它 ——
// 依赖单向是仓库纪律）。未知取值按人算：宁可多问一句"没写性别"，也别漏掉真人角色。
func isHumanlikeKind(c Character) bool {
	switch strings.ToLower(strings.TrimSpace(c.Kind)) {
	case "", "human", "android", "chibi", "人", "人形机械", "q版", "q 版":
		return true
	case "object", "spirit_pet", "beast", "incorporeal", "器物", "道具", "灵宠", "妖兽", "无实体", "灵体":
		return false
	}
	return true
}
