package project

import (
	"fmt"
	"strings"
)

// required_fields.go 每镜的**必填字段完整性**（用户《平台侧生成规范》§6.2）。
//
// ## 口径（逐字来自规范）
//
//	「每镜把场地/人物/光/时间/道具状态全写出来；**禁止"同上""如上"、禁止留空**
//	 （外库原话："即梦无记忆…所有字段必须完整写出"）」
//
// ## 为什么单列一道门
//
// 留给模型的"自觉"是不可靠的（本仓实测：写了"片段内必须切 2-4 个小镜头"，35/35 镜全都没切）。
// 而"留空"这件事平台此前**只查了 `action` 一个字段** —— 一个 5 秒的片段可以在
// `end_state`、`shot_size`、`camera`、`hook_type` 全空的情况下过门，而渲染时模型
// 只能自己补：补出来的站位/景别/运镜**每一版都不一样**，几集下来就是"同一个故事两种拍法"。
//
// 这道门把审计里那句"只查一个字段，其余放过"收口成一份**清单**：列在
// `RequiredShotFields` 里的每一项都必须有值。以后规范加了字段，往清单里加一行即可
// （而不是到处补 if）。

// RequiredShotFields 必填字段清单（字段名 → 人话说明 → 取值器）。
//
// ⚠️ **只列"还没有专门门"的字段** —— 重复造门的下场是同一件事报两遍，用户看两次烦两次。
// 已经有门的（本节不再重复）：
//
//	`scene`      → G012（未指定场景）/ G013（引用不存在的场景）
//	`action`     → 「画面动作为空」那条
//	`shot_size`  → G018（景别不在白名单）
//	`camera`     → 运镜三条门（没写 / 术语 / 连续重复）
//	`angle_zh`   → 「没写机位角度词」那条
//	`end_state`  → G037（缺尾帧状态）
//	`h3_prompt`  → H3 格式门 + 汉字卫生门 + 词数门
//
// 所以这里只剩**规范要求、而平台一直没有判据**的那一项：钩子类型。
//
// ⚠️ 另有几项**故意不列**（列了就是误报）：
//
//	· `Dialogue` / `Narration`：空镜与纯动作镜本来就没有台词 —— 规范说的是
//	  "如无则写无"，平台侧的等价物是**允许两者都空**（`vo_kind` 决定这一镜说不说话），
//	  下面只对 `vo_kind=speech` 的镜单独判。
//	· `PropIDs`：不需要器物的镜头本来就该空（"这一镜没有关键道具"是合法状态）。
//	· `View`：官方九视角表没有的取景（平视中景等）**留空是对的**，填错词比留空更糟
//	  （见 view_vocab.go 的说明）。
var RequiredShotFields = []struct {
	Key  string
	What string
	Get  func(Shot) string
}{
	{"hook_type", "钩子类型（规范 §6.2 的必填项，平台一直没有判据）", func(s Shot) string { return s.HookType }},
}

// RequiredFieldIssues 找出必填字段留空的镜（返回人话，空 = 没问题）。
//
// 按**字段**聚合（不是一个镜头报一条）：一种字段空一片时，用户要看到的是
// "有 7 镜没写钩子类型"，而不是 7 条各说一遍。
func RequiredFieldIssues(p *Plan) []string {
	if p == nil || len(p.Shots) == 0 {
		return nil
	}
	var out []string
	for _, f := range RequiredShotFields {
		var ids []string
		for _, sh := range p.Shots {
			if strings.TrimSpace(f.Get(sh)) == "" {
				ids = append(ids, fmt.Sprintf("#%d", sh.ID))
			}
		}
		if len(ids) > 0 {
			out = append(out, fmt.Sprintf("%d 镜没写 %s（%s）：%s",
				len(ids), f.What, f.Key, strings.Join(ids, " ")))
		}
	}
	// ⚠️ **台词那一条不在这里判** —— `voiceover.go` 的 `voIssues` 已经有一份
	// （"画面里有人在说却没有台词 ⇒ 哑剧"），再写一遍就是同一件事报两遍。
	// 这是本仓的纪律：**先查有没有现成的门**，别重复造（当天就栽过一次）。
	return out
}
