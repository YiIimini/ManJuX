package project

import (
	"strings"
)

// settingbook_brief.go 把设定集渲染成**可注入提示词的一段**（各专职会话共用）。
//
// ## 由来（2026-09-28 用户架构：五步专职会话）
//
// 读取器 `LoadSettingBook` 早就写好了（实测一部戏能读出 14 角色 / 13 道具 / 67 数字锚 /
// 12K 风格卡），但**没有任何会话消费它** —— 那些锚白白躺在磁盘上，模型每次自己编：
//
//	· 「现在是哪一年」集集不同（数字锚表里明明写着"灰纪元第八万年"）；
//	· 角色的年龄/疤痕/衣裳颜色每集漂（外形锚表里逐项写着）；
//	· 道具的"功能边界"被忽略（重点道具表里写着"托不起人"，而情节正靠这个限制成立）。
//
// 这一段就是**接线的落点**：谁开新会话（编剧 / 人物道具 / 分镜），谁把它拼进 system 或 user 段。
//
// ## 为什么**不带 id**
//
// 锚表是技能侧按**名字**写的（`SettingChar` / `SettingProp` 只有 Name/Note/Fields/Raw），
// 里面根本没有 id。所以这里只写名字 —— **不编 id**：编出来的 id 会指向不存在的卡，
// 而"卡片 id 由谁产生"这件事有唯一的答案（卡片自己，见 `Entry.ID`）。
// 名字 → id 的映射发生在**装配侧的对号**（`HealPlan`），不在提示词里。
// 用户口径"程序里都以数据 ID 关联"说的是**程序内部**，而提示词是给模型读的散文。

// SettingBriefChars 角色锚段最多写几个角色（超出的角色不写，避免提示词爆掉）。
const SettingBriefChars = 24

// SettingBrief 把设定集渲染成一段可注入提示词的中文摘要。
//
// `withStyle=false` 时跳过风格卡原文（它很长，而分镜会话另外有画风块要拼）。
// 返回空串表示这部戏没有设定集（调用方照旧走"只喂正文"的老路）。
func (b *SettingBook) SettingBrief(withStyle bool) string {
	if b == nil {
		return ""
	}
	var sb strings.Builder
	if len(b.Characters) > 0 {
		sb.WriteString("【本作人物外形锚（**必须逐项沿用**，不得自己另写一套）】\n")
		for i, c := range b.Characters {
			if i >= SettingBriefChars {
				sb.WriteString("（其余角色从略）\n")
				break
			}
			line := "- " + strings.TrimSpace(c.Name)
			if d := strings.TrimSpace(c.Raw); d != "" {
				line += "：" + clampRunesOneLine(d, 160)
			}
			sb.WriteString(line + "\n")
		}
	}
	if len(b.Props) > 0 {
		sb.WriteString("【本作重点道具（功能边界往往是情节的钥匙，不得改写）】\n")
		for i, p := range b.Props {
			if i >= SettingBriefChars {
				break
			}
			line := "- " + strings.TrimSpace(p.Name)
			if d := strings.TrimSpace(p.Raw); d != "" {
				line += "：" + clampRunesOneLine(d, 160)
			}
			sb.WriteString(line + "\n")
		}
	}
	if len(b.Numbers) > 0 {
		// 数字锚是**最容易集集不同**的一类（年份/距离/人数/金额），单列出来
		sb.WriteString("【本作数字锚（出现相关话题时必须用这里的定值，不得另编一个数）】\n")
		for i, a := range b.Numbers {
			if i >= 40 {
				sb.WriteString("（其余从略）\n")
				break
			}
			line := "- " + strings.TrimSpace(a.Name) + "：" + strings.TrimSpace(a.Value)
			if n := strings.TrimSpace(a.Note); n != "" {
				line += "（" + clampRunesOneLine(n, 60) + "）"
			}
			sb.WriteString(line + "\n")
		}
	}
	if withStyle {
		if s := strings.TrimSpace(b.StyleCard); s != "" {
			sb.WriteString("【本作视觉风格卡（画风以此为准）】\n" + clampRunesOneLine(s, 800) + "\n")
		}
	}
	return sb.String()
}

// HasContent 设定集里有没有任何可用内容（没有就别往提示词里拼一段空标题）。
func (b *SettingBook) HasContent() bool {
	return b != nil && (len(b.Characters) > 0 || len(b.Props) > 0 || len(b.Numbers) > 0 ||
		strings.TrimSpace(b.StyleCard) != "")
}

// clampRunesOneLine 截到 n 个字符并把换行压成空格（提示词里的一行）。
func clampRunesOneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
