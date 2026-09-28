package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 锚表补出来的卡必须是**人**（2026-09-29 真机实测）。
//
// 现场：锚表补进的 5 个角色没有 kind 字段，出图时按整段描述推断 ——
// 而《名册之外，八万零一号》的锚表里，第二主角「霜见」的小节写着
// 「**颈侧一道义体接缝纹**」「**颈部黑色机械护颈项圈**」「黑色哑光**机能**外套」，
// 命中 `objectStrongWords` 里的「机械」⇒ **人被判成器物**：
// 定妆照走器物那套取景（"a clean three-quarter product view of the object"、
// 没有脸的身份条款），实测出图是一张全身立绘，与人设完全不是一回事。
//
// 判据：锚表是《**人物**外形锚表》，表里的人是"人" —— 除非它自己写着别的
// （定位行或名字里出现器物/灵体/妖兽/灵宠/无实体）。夹具照真实条目抄。
func TestAnchorCardKindIsHumanForPeople(t *testing.T) {
	// 真实条目（《名册之外，八万零一号》设定集/人物外形锚表.md 的霜见小节）
	shuangjian := project.SettingChar{
		Name: "霜见",
		Note: "定位：第二主角／通信手。拾荒船团的读数员，全书唯一的「只信数字的人」",
		Raw: "### 霜见（定位：第二主角／通信手）\n\n- 年龄：少年，17–19 岁\n" +
			"- 脸型：瘦长脸，下颌线清晰\n- 独特记号：**颈侧一道义体接缝纹**＋**颈部黑色机械护颈项圈**\n" +
			"- 首套服装：黑色高领针织 ＋ 哑光机能外套\n",
	}
	if got := anchorCardKind(shuangjian); got != string(KindHuman) {
		t.Fatalf("第二主角「霜见」必须是 human（描述里的「机械护颈项圈」不该把它判成器物），实得 %q", got)
	}
	// 反面：锚表里真写着器物/灵体的，不能被硬判成人
	for _, c := range []project.SettingChar{
		{Name: "铜雀", Note: "定位：器物。主角随身的机关鸟"},
		{Name: "阿墟", Note: "定位：灵体。只以回声出现"},
	} {
		if got := anchorCardKind(c); got == string(KindHuman) {
			t.Errorf("「%s」的定位写着非人（%s），不该被硬判成 human", c.Name, c.Note)
		}
	}
	// 接线：补进方案的那张卡要真的带上 kind（只写函数不接线等于没修）
	plan := &project.Plan{}
	src := "## 人物外形锚表\n\n### 霜见（定位：第二主角／通信手）\n\n- 脸型：瘦长脸\n- 独特记号：颈部黑色机械护颈项圈\n"
	if added := EnsureAnchorCast(plan, src); len(added) != 1 {
		t.Fatalf("应补进 1 个角色，实得 %v", added)
	}
	if len(plan.Characters) != 1 || strings.TrimSpace(plan.Characters[0].Kind) != string(KindHuman) {
		t.Fatalf("补进的角色卡必须带 kind=human，实得 %+v", plan.Characters)
	}
}
