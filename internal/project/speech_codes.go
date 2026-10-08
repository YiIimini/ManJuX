package project

import (
	"fmt"
	"regexp"
	"strings"
)

// speech_codes.go 台词里的**字母编号**（`U-80001` 这种）在提示词里换成中文读法。
//
// 【为什么（2026-10-08 真机实锤，用户「配音第一个分镜就不对」，他拍板按此优化）】
// H3 只念 `<d>[Chinese]…</d>` 里的内容。把 `U-80001` 直接写进 `<d>`，模型念出来是
// 一团糊 —— 真机第01集镜头 1 的 ASR（faster-whisper medium）转写是
// 「硬件剛印U-C缠Hi-001」：字母与数字混排的读法它稳不住（同一集镜 6/8/12 各有
// `U-80001`/`U-80000`，风险一样）。平台侧能**确定性地**做掉这件事：把读法写成汉字，
// 让它只需要念中文。
//
//	`U-80001` → 「尤八零零零一」（字母按中文字母表读音，数字逐位念）
//
// 两条通道各归各的（本仓既有设计）：**声音**念 `<d>`（现在是中文读法）、
// **字幕**烧 `dialogue[].text`（仍是 `U-80001` 原样）—— 观众听到的是读音、看到的是编号，
// 这是正常的做法，不是"声音与字幕不一致"（对账两侧都走 normSpokenText，见下）。
//
// 边界（照实说）：
//	· 只处理**含字母**的编号（`U-80001`、`H3`）。**纯数字不动**：`80000` 当编号要逐位念、
//	  当数量要念"八万"、当日期读法又不同 —— 猜错比不读更糟（剧本里 `80000` 本来就写成了「八万」）。
//	· 数字**逐位**念（编号口径），不做数词换算（`U-80001` 不是"八万零一"）。
//	· 只在 `<d>` 里替换；`dialogue`（字幕源）一个字不动。

// 字母的中文读音（字母表口径）。
var codeLetterReading = map[rune]string{
	'A': "诶", 'B': "比", 'C': "西", 'D': "迪", 'E': "伊", 'F': "艾弗",
	'G': "吉", 'H': "艾尺", 'I': "艾", 'J': "杰", 'K': "开", 'L': "艾勒",
	'M': "艾姆", 'N': "恩", 'O': "欧", 'P': "皮", 'Q': "丘", 'R': "阿尔",
	'S': "艾斯", 'T': "提", 'U': "尤", 'V': "维", 'W': "达不溜", 'X': "艾克斯",
	'Y': "歪", 'Z': "贼德",
}

// 数字逐位读音（编号口径，不做数词换算）。
var codeDigitReading = map[rune]string{
	'0': "零", '1': "一", '2': "二", '3': "三", '4': "四",
	'5': "五", '6': "六", '7': "七", '8': "八", '9': "九",
}

// reCodeToken 字母编号：至少一个字母 + 数字（连字符/全角破折号/空格可有可无）。
var reCodeToken = regexp.MustCompile(`[A-Za-z]+[ \t]*[-–—]?[ \t]*[0-9]+`)

// PronounceCodeToken 把一个编号 token 念成中文（`U-80001` → `尤八零零零一`）。
//
// 表里没有的字母/数字原样保留（宁可留一个原字符，也不要编一个读音）。
func PronounceCodeToken(tok string) string {
	var b strings.Builder
	for _, r := range tok {
		if up := r; up >= 'a' && up <= 'z' {
			b.WriteString(codeLetterReading[up-'a'+'A'])
			continue
		}
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteString(codeLetterReading[r])
		case r >= '0' && r <= '9':
			b.WriteString(codeDigitReading[r])
		default:
			// 连字符、空格这类分隔符不进读音（念"尤-八..."会让模型念出"杠"）
		}
	}
	return b.String()
}

// PronounceCodesInText 把一句话里所有**含字母的编号**换成中文读法；没有编号就原样返回。
//
// 幂等：读法结果是纯汉字，再跑一遍不会命中 reCodeToken。
func PronounceCodesInText(s string) string {
	if s == "" || !reCodeToken.MatchString(s) {
		return s
	}
	return reCodeToken.ReplaceAllStringFunc(s, PronounceCodeToken)
}

// hasSpokenCode 这句话里有没有需要定读音的**字母编号**。
func hasSpokenCode(s string) bool { return reCodeToken.MatchString(s) }

// NormalizeSpokenCodes 把每一镜 `<d>` 标签里的字母编号换成中文读法（幂等）。
//
// 只动 `<d>…</d>` 里的正文：语言标记（`[Chinese]`）保留，标签外的一个字不改。
// 返回改动的镜数与改了哪几镜（调用方如实播报）。
func NormalizeSpokenCodes(plan *Plan) (int, []string, int) {
	if plan == nil || len(plan.Shots) == 0 {
		return 0, nil, 0
	}
	shots, codes := 0, 0
	var ids []string
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		p := sh.H3Prompt
		if p == "" || !hasSpokenCode(p) {
			continue
		}
		out, n := pronounceCodesInTags(p)
		if n == 0 {
			continue
		}
		sh.H3Prompt = out
		shots++
		codes += n
		ids = append(ids, fmt.Sprintf("#%d", sh.ID))
	}
	return shots, ids, codes
}

// pronounceCodesInTags 逐条 `<d>…</d>` 处理（标签外不动），返回新串与改动条数。
func pronounceCodesInTags(h3 string) (string, int) {
	var b strings.Builder
	rest := h3
	n := 0
	for {
		i := strings.Index(rest, "<d>")
		if i < 0 {
			b.WriteString(rest)
			break
		}
		j := strings.Index(rest[i:], "</d>")
		if j < 0 {
			b.WriteString(rest) // 没有闭合标签：不碰（另有门在处理缺标签）
			break
		}
		body := rest[i+len("<d>") : i+j]
		b.WriteString(rest[:i+len("<d>")])
		if newBody := PronounceCodesInText(body); newBody != body {
			n++
			b.WriteString(newBody)
		} else {
			b.WriteString(body)
		}
		b.WriteString("</d>")
		rest = rest[i+j+len("</d>"):]
	}
	return b.String(), n
}

// CodeGaps 点名**还没有中文读法**的字母编号所在镜头（渲染前/预检的"看得见"用）。
//
// 正常情况 HealPlan 已经补过，这里为空；非空说明这份方案绕过了读取边界。
func CodeGaps(plan *Plan) []string {
	if plan == nil {
		return nil
	}
	var out []string
	for i := range plan.Shots {
		sh := &plan.Shots[i]
		for _, body := range h3TagBodies(sh.H3Prompt) {
			if hasSpokenCode(body) {
				out = append(out, fmt.Sprintf("#%d", sh.ID))
				break
			}
		}
	}
	return out
}

// h3TagBodies 列出 `h3` 里每一条 `<d>…</d>` 的正文（含语言标记，调用方自行剥）。
func h3TagBodies(h3 string) []string {
	var out []string
	for _, m := range h3DialogueRe.FindAllStringSubmatch(h3, -1) {
		out = append(out, m[1])
	}
	return out
}

// CodeSummary 给日志用的一句话（哪几镜的编号定了读音）。
func CodeSummary(shots []string, codes int) string {
	if len(shots) == 0 {
		return ""
	}
	return fmt.Sprintf("%d 镜、%d 处字母编号（`U-80001` 这种）定了中文读法（念「尤八零零零一」，字幕仍是原编号）：%s",
		len(shots), codes, strings.Join(shots, "、"))
}
