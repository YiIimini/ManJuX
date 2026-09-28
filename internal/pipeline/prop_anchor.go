package pipeline

import (
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// prop_anchor.go 关键道具的**跨镜文字锚定**（2026-09-28 用户：关键物品生成道具、前后镜漂移）。
//
// ## 为什么必须有这一层
//
// 官方规则是 **Props never get a picture**（道具不进 H3 参考图集合），所以道具的跨镜一致性
// **只能靠文字**：同一件东西在第 3 镜和第 20 镜必须被**同一段话**描述，模型才画得出同一件器物。
// 而实测那一集是这样的：plan 里**连 props 字段都没有** ✗、25 镜提到刀/烙铁/名册、`prop_ids` 全空 ——
// 于是每镜都由模型**自己发明**那把碑刀：形状、长短、缠柄、朱砂线一镜一个样，观众看到的就是漂移。
//
// 这一层做一件事：渲染前，把该镜 `prop_ids` 里每一件道具的**卡片原文**（外观/材质/尺寸/记号）
// 逐字追加进提示词。**一处实现**（装配点），不依赖 LLM 每镜自觉复述。
//
// ## 边界
//
//	① 只锚 `prop_ids` 里点到的道具 —— 不点名就不塞（避免把全片道具堆进每一镜）；
//	② 卡片文本走与道具出图**同一份**清洗（汉字卫生），而且是**逐字原样**引用，不做改写；
//	③ 幂等：已含锚块原样返回（渲染前补齐与单镜重渲都会走到这里）；
//	④ 卡片不存在 / 描述为空 ⇒ 跳过并留给契约门报（这里不猜）。
const propAnchorMarker = "PROP ANCHORS (canonical, written by the renderer from the prop cards)"

// EnsurePropAnchors 把本镜引用到的道具卡片原文锚进提示词（幂等）。
//
// 返回原串表示不需要锚定（没有 prop_ids / 卡片缺失 / 描述为空）。
func EnsurePropAnchors(prompt string, plan *project.Plan, shot project.Shot) string {
	if plan == nil || len(shot.PropIDs) == 0 || strings.TrimSpace(prompt) == "" {
		return prompt
	}
	if strings.Contains(prompt, propAnchorMarker) {
		return prompt // 幂等
	}
	byID := map[string]project.Prop{}
	for _, pr := range plan.Props {
		byID[strings.TrimSpace(pr.ID)] = pr
	}
	var lines []string
	for _, id := range shot.PropIDs {
		pr, ok := byID[strings.TrimSpace(id)]
		if !ok {
			continue
		}
		// 用**英文外观短语**优先（那是给图模型看的那一句），没有才退中文描述 ——
		// 与道具出图（PropImagePrompt 的兜底）同一条优先级。
		text := strings.TrimSpace(pr.EN)
		if text == "" {
			text = strings.TrimSpace(project.StripCJK(pr.Description))
		}
		if text == "" {
			text = strings.TrimSpace(project.StripCJK(pr.ImagePrompt))
		}
		if text == "" {
			continue
		}
		// 名字用中文名 + 卡片 id：中文名给模型认物，id 给日志/人审对得上
		lines = append(lines, "- "+strings.TrimSpace(pr.Name)+" ("+strings.TrimSpace(pr.ID)+"): "+text)
	}
	if len(lines) == 0 {
		return prompt
	}
	var sb strings.Builder
	sb.WriteString("\n\n" + propAnchorMarker + " — the SAME object must look identical in every shot that names it; " +
		"copy its form, material, colour, size and signature detail exactly as written here, and never redesign it:\n")
	sb.WriteString(strings.Join(lines, "\n"))
	sb.WriteString("\n")
	return prompt + sb.String()
}
