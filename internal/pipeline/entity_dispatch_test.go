package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 每种角色类型必须**挑到自己的渲染方法**（2026-09-29 用户：
// 「不同的角色类型需要分好选择对应的渲染函数方法」）。
//
// 起因是一串真机事故：白瓷装甲的女主角按人的身份锁画成古装女子（android 缺档）；
// 巴掌大的全息虚影画成真人大小的半透明女人（incorporeal 的取景丢尺度、还用了黑底）。
// 根因都是同一个形状：**某个类型在某个分派点上没有分支，悄悄落进 default** ——
// 而 default 往往是"人"那一套。构建与运行都不报错，只有用户看得见。
//
// 这条测试把"类型 → 渲染方法"的选择**逐项钉死**：加了新类型却没接上分派点，它会红。
// 表里的每一列都是一个真实存在、且会出像素（或决定出什么像素）的入口。
func TestEveryEntityKindPicksItsOwnRenderingMethod(t *testing.T) {
	// humanLike：走人形取景（半身正脸）+ 发型条款 + 人形三视图板
	// **不含 Q 版的人脸介质块**：Q 版是手办动漫质感（2026-09-29 用户「不是写实的那种，不可爱」），
	// 挂写实人脸条款会把它画成写实小人 —— 见 §人脸介质块那一组断言。
	humanLike := map[EntityKind]bool{KindHuman: true, KindAndroid: true, KindChibi: true}
	// faceClauseKinds：**允许**挂写实人脸介质条款的类型（Q 版不在内）
	faceClauseKinds := map[EntityKind]bool{KindHuman: true, KindAndroid: true}
	turnVerb := map[EntityKind]string{
		KindHuman: "原地转身", KindAndroid: "原地转身", KindChibi: "原地转身",
		KindObject: "转台自转", KindIncorporeal: "原地旋动",
		KindSpiritPet: "转身环视", KindBeast: "转身环视",
	}
	markers := map[string]EntityKind{}
	for _, k := range EntityKinds {
		prof := EntityProfileOf(k)

		// ① 取景：人形那一档才有前导句与构图纪律；其余一律**不许**套人像
		lead := PortraitLeadFraming(k, 768, 1344)
		clause := PortraitFramingClause(k, 768, 1344)
		if humanLike[k] {
			if lead == "" || clause == "" {
				t.Errorf("%s 缺人形取景（前导句/构图纪律）—— 定妆照会退化成裸提示词", EntityKindLabel(k))
			}
			if !strings.Contains(clause, "chest-up") {
				t.Errorf("%s 的取景要写明上半身（用户口径：上半身正脸）", EntityKindLabel(k))
			}
		} else {
			if lead != "" || clause != "" {
				t.Errorf("%s 不是人形，却拿到了人像取景 —— 器物/灵体被按人画就是这么来的", EntityKindLabel(k))
			}
		}
		// ② 发型条款：只有长头发的类型谈发型
		hair := PortraitHairClause(k)
		if humanLike[k] && hair == "" {
			t.Errorf("%s 缺发型条款（发长/额前状态是漂移高发项）", EntityKindLabel(k))
		}
		if !humanLike[k] && hair != "" {
			t.Errorf("%s 不该有发型条款（它没有头发）", EntityKindLabel(k))
		}
		// ③ 三视图板：官方那张"3 全身 + 6 头"的板只对人形有意义
		if got := HumanoidBoardKind(k); got != humanLike[k] {
			t.Errorf("%s 的三视图板判据错了（实得 %v）—— 给器物/灵宠套头身板只会画出三只鸟排排站",
				EntityKindLabel(k), got)
		}
		// ④ 介质块：人形给"人脸"块，其余给"通用介质"块（给器物写肤色哑光＝逼它长脸）
		medium := TurnaroundMediumBlock(k, false)
		// humanFaceRenderClause() 是"人脸条款"的唯一实现（人形给脸，其余不给）
		face := humanFaceRenderClause()
		has := strings.Contains(medium, face)
		if faceClauseKinds[k] && !has {
			t.Errorf("%s 的介质块缺人脸条款", EntityKindLabel(k))
		}
		if !faceClauseKinds[k] && has {
			t.Errorf("%s 拿到了人脸介质块（给器物写肤色哑光＝逼它长脸）", EntityKindLabel(k))
		}
		// ⑤ 多视角动作：每种类型一个动作词
		if want := turnVerb[k]; want != "" && k.TurnVerb() != want {
			t.Errorf("%s 的多视角动作应是 %q，实得 %q", EntityKindLabel(k), want, k.TurnVerb())
		}
		// ⑥ 定妆照标记必须**一型一标**：老定妆照据此判"是不是按当前类型渲染的"
		if m := prof.SheetMarker; m == "" {
			t.Errorf("%s 没有定妆照标记", EntityKindLabel(k))
		} else if prev, dup := markers[m]; dup {
			t.Errorf("%s 与 %s 的定妆照标记相同（%q）—— 换类型后旧图不会被判为过时",
				EntityKindLabel(k), EntityKindLabel(prev), m)
		} else {
			markers[m] = k
		}
		// ⑦ 取景重写：除了人（LLM 那句本来就对），其余类型都要自己收口
		if k != KindHuman && k != KindChibi && !prof.RewriteSheetFraming {
			t.Errorf("%s 没有重写取景尾巴 —— 会沿用 LLM 写的人像取景", EntityKindLabel(k))
		}
		// ⑧ 每一类都要有反错误形态条款（不得拟人化/不得实体化/不得缩水/不得退化成普通人）
		if strings.TrimSpace(prof.AntiForm) == "" && k != KindHuman && k != KindChibi {
			t.Errorf("%s 缺反错误形态条款", EntityKindLabel(k))
		}
		// ⑨ 判据（人审/VLM）也要按类型给
		if strings.TrimSpace(prof.JudgePoints) == "" {
			t.Errorf("%s 缺人审判据", EntityKindLabel(k))
		}
	}

	// ⑩ 定妆照装配的端到端：每一类的成品提示词里必须带**它自己那个标记**，
	// 且不带人形专属的取景句（器物/灵体/妖兽尤其不能）
	for _, k := range EntityKinds {
		prompt := PortraitPrompt(PortraitSubject{
			Kind: k, Name: "测试主体", Description: "描述", ImagePrompt: "a test subject",
			FacesOn: true, FrameW: 768, FrameH: 1344,
		}, &project.Plan{Style: "guofeng_cinematic"})
		if !strings.Contains(prompt, EntityProfileOf(k).SheetMarker) {
			t.Errorf("%s 的定妆照提示词里没有本类型的标记 —— 界面上判不出「这张图是按哪套方法渲染的」", EntityKindLabel(k))
		}
		if !humanLike[k] && strings.Contains(prompt, "chest-up portrait") {
			t.Errorf("%s 的定妆照被套上了人像取景（上半身正脸）", EntityKindLabel(k))
		}
	}
}
