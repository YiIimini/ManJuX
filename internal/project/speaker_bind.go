package project

import "strings"

// speaker_bind.go 说话人与本镜画面的**绑定归一化**（2026-09-27 实测驱动）。
//
// ## 为什么要有
//
// 真跑一次 LLM 直出（40 镜）实测：契约自检 **fail=5，全部是同一条** ——
//
//	说话人「卖菜婆」不在本镜 characters 里 —— 画面里没有他，模型会随便找个人动嘴
//	（要么把他加进本镜，要么标 os=true 当画外音）
//
// 这不是模型"不听话"，而是**语义上本来就该是画外音**：那一镜里卖菜婆确实不在画面中
// （把菜篮砸过来的人只给了一只手臂或干脆在画外喊），她的话是**画面外发出来的**。
// 契约自己那句提示里就写着正确处置（"要么标 os=true"），只是没人去做 —— 于是每一集
// 都会稳定带几条 fail，而 fail 是会拦住导入的。
//
// ## 判据与边界
//
// 只处理"说话人**是**本方案的角色、但**不在本镜** characters 里"这一种：
//   - 说话人是画外音标签（旁白/系统/内心…）→ 本来就不该在图里，不动；
//   - 说话人在本镜里 → 正常，不动；
//   - 说话人根本不是本方案的角色（打字错、临时角色）→ **不动**，交给契约失败暴露
//     （自动标 os 会把"写错名字"这类真问题盖掉）。
//
// 归一化是**单向**的：只把 os 从 false 改成 true，从不改回 false，也不动台词文本。
func MarkOffscreenSpeakers(p *Plan) int {
	if p == nil {
		return 0
	}
	byID := map[string]bool{}
	byName := map[string]bool{}
	for _, ch := range p.Characters {
		byID[strings.TrimSpace(ch.ID)] = true
		byName[strings.TrimSpace(ch.Name)] = true
	}
	n := 0
	for i := range p.Shots {
		sh := &p.Shots[i]
		if len(sh.Dialogue) == 0 {
			continue
		}
		inShot := map[string]bool{}
		for _, id := range sh.Characters {
			inShot[strings.TrimSpace(id)] = true
		}
		// 本镜在场角色的**名字**集合：说话人字段用名字写（技能侧/直出都是名字）
		onScreen := map[string]bool{}
		for _, ch := range p.Characters {
			if inShot[strings.TrimSpace(ch.ID)] {
				onScreen[strings.TrimSpace(ch.Name)] = true
			}
			// 角色名常带造型档后缀（沈砚·一品），点名部分也算在场
			if inShot[strings.TrimSpace(ch.ID)] {
				if base := CharFamilyBaseName(ch.Name); base != "" {
					onScreen[base] = true
				}
			}
		}
		for j := range sh.Dialogue {
			line := &sh.Dialogue[j]
			if line.OS {
				continue
			}
			sp := strings.TrimSpace(line.Speaker)
			if sp == "" || IsVoiceoverSpeaker(sp) || onScreen[sp] {
				continue
			}
			if !byID[sp] && !byName[sp] {
				continue // 不是本方案的角色：可能是写错了名字，让它去失败
			}
			line.OS = true
			n++
		}
	}
	return n
}
