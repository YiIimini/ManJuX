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
// 归一化是**双向**的（2026-10-09 改成）：这一位由**平台**按方案的出场名单定 ——
//
//	· 说话人在本镜 `characters` 里、且**不在** `offscreen`（已出画）里 ⇒ `os=false`（画内对口型）
//	· 说话人不在本镜（或是本镜已出画的角色）⇒ `os=true`（画外音）
//
// 【为什么必须双向（真机取证）】原版是单向的（只 false→true，从不改回），理由是"不覆盖模型
// 的判断" —— 真机《我捡的团子·第01集》**19 句台词全被模型标成 os=true**（而 schema 里写的
// 是 `os:false`）：于是①平台自己的"画外说话人"判据被这些 os 全部跳过（见 plan_contract.go
// 的 OffScreenSpeakers 注释），镜 5 那种**真画外音**反而没人管 ⇒ 画内的人被模型安排去动嘴；
// ②"人在画面里亲口说"的镜头全被写成画外音。⇒ "说话人在不在这一镜"是**方案自己的事实**
// （characters + offscreen 两个字段），不该让模型的自述把它改掉。不动的只有三种：画外音标签
// 说话人（旁白/系统/内心，整镜画外，由 vo_kind 管）、空说话人、**不是本方案角色**的名字
// （那多半是打错字，交给契约失败暴露）。
//
// 返回值：补标处数、**纠正**（把画内台词从 os=true 改回 false）处数。两件事分开报 ——
// 纠正数是"模型写错了、平台替你改了"，用户有权知道。
func markOffscreenSpeakers(p *Plan) (marked, cleared int) {
	if p == nil {
		return 0, 0
	}
	byID := map[string]bool{}
	byName := map[string]bool{}
	for _, ch := range p.Characters {
		byID[strings.TrimSpace(ch.ID)] = true
		byName[strings.TrimSpace(ch.Name)] = true
	}
	// 统计口径：marked 补标（画外音没标）、cleared 纠正（人在画面里却被标成画外音）。
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
		// 本镜**已出画**的角色（`offscreen` 字段）：人在场、但这一镜不在画面里 —— 他的台词仍算画外音。
		offScreen := map[string]bool{}
		for k := range sh.Offscreen {
			key := strings.ToLower(strings.TrimSpace(k))
			if key == "" {
				continue
			}
			offScreen[key] = true
			for _, ch := range p.Characters {
				if ch.ID == k || ch.Name == k {
					offScreen[strings.ToLower(strings.TrimSpace(ch.Name))] = true
					offScreen[strings.ToLower(strings.TrimSpace(ch.ID))] = true
				}
			}
		}
		for j := range sh.Dialogue {
			line := &sh.Dialogue[j]
			sp := strings.TrimSpace(line.Speaker)
			if sp == "" || IsVoiceoverSpeaker(sp) {
				continue
			}
			if !byID[sp] && !byName[sp] {
				continue // 不是本方案的角色：可能是写错了名字，让它去失败
			}
			if !onScreen[sp] || offScreen[strings.ToLower(sp)] {
				if !line.OS {
					line.OS = true
					marked++
				}
				continue
			}
			if line.OS {
				line.OS = false
				cleared++
			}
		}
	}
	// 装配期诊断量：纠正了几条被误标成画外音的台词（供两阶段如实播报，不进 JSON）。
	p.SpeakerOSFixed += cleared
	return marked, cleared
}

// MarkOffscreenSpeakers 兼容入口：返回**改动总处数**（补标 + 纠正）。
// 需要区分"补标/纠正"两件事时用 markOffscreenSpeakers（播报用得到）。
func MarkOffscreenSpeakers(p *Plan) int {
	marked, cleared := markOffscreenSpeakers(p)
	return marked + cleared
}
