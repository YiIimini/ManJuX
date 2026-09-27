package project

import (
	"strings"
	"testing"
)

// char_boards_test.go 三视图板串的解析（技能侧《平台侧生成规范》§3.4/§3.5）。
//
// 平台此前只读卡片里的「① 定妆」，出板用的是平台自己那份 15 格列表 —— 与规范的 15 格内容
// **对不上**（头角度与局部细节两项不同）。现在以卡片的 `board_prompt` 为准，平台那份只作兜底。

const cardMDWithBoard = `# 角色卡 · 某书

### 沈砚（主角 · 灾星）
**① 定妆（身份源 · 正面半身 · 纯白底）**
` + "```text" + `
Shen Yan — human, nineteen. IDENTITY LOCK (do not change): thin long face.
GUOFENG ANIMATED-FEATURE ART DIRECTION (period Chinese): celadon palette.
` + "```" + `

**② 三视图细节板（一致性校对用 · 一张板）**
` + "```text" + `
Shen Yan — character turnaround board, one sheet.
LEFT (main area): three full-body standing views — front, left profile, back — the SAME character in all three.
UPPER RIGHT: six head angles — front face, top-down view of the crown, back of the head, left silhouette.
LOWER RIGHT: six detail tiles — upper-garment fabric, footwear as a single item.
LABELS (for the design sheet): the character name, role tag, main 3/4 view, expression, material notes.
plain pure white seamless background, no text, no letters, no watermark.
` + "```" + `

### 阿婆（真情锚）
**① 定妆（身份源 · 正面半身 · 纯白底）**
` + "```text" + `
A Po — human, seventy. IDENTITY LOCK (do not change): round face.
` + "```" + `
`

// TestParseCharacterCardBoards 板串与定妆串**分别解析**，互不串味。
func TestParseCharacterCardBoards(t *testing.T) {
	boards := ParseCharacterCardBoards(cardMDWithBoard)
	portraits := ParseCharacterCards(cardMDWithBoard)

	if len(boards) != 1 {
		t.Fatalf("只有沈砚有 ② 段，应解析出 1 条，实际 %d 条：%v", len(boards), boards)
	}
	bp := boards["沈砚"]
	if !strings.Contains(bp, "character turnaround board") {
		t.Errorf("板串内容不对：%s", bp)
	}
	// 定妆串与板串不能互相污染（此前只有一个解析器时最容易出这种错）
	if strings.Contains(portraits["沈砚"], "turnaround board") {
		t.Error("定妆串里混进了板串 —— 两个解析器必须各取各的段")
	}
	if strings.Contains(bp, "IDENTITY LOCK") {
		t.Error("板串里混进了定妆段的内容")
	}
}

// TestBoardPromptStripsLabels 板串里"要求画标签"的那一行必须被掐掉。
//
// 依据：《平台侧生成规范》§3.4 明说板上"不出现文字说明、不出现 LOGO/水印/UI 元素"，
// 而技能侧板串自己写着 `LABELS (for the design sheet): the character name, …` —— 自相矛盾。
// 按 §3.4 与平台"画面文字禁止"的口径掐掉（该由技能侧自己删，已回报）。
func TestBoardPromptStripsLabels(t *testing.T) {
	bp := ParseCharacterCardBoards(cardMDWithBoard)["沈砚"]
	if strings.Contains(bp, "LABELS") || strings.Contains(bp, "role tag") {
		t.Errorf("LABELS 行没被掐掉：%s", bp)
	}
	// 同一条板串自己写的 no text 要留着（那是正确的约束）
	if !strings.Contains(bp, "no text") {
		t.Error("别把正确的 no text 约束一起删了")
	}
	// 风格句在这里**不剥**：卡片板串自带项目风格串，平台不再注入第二份
	if !strings.Contains(bp, "plain pure white seamless background") {
		t.Error("板串的正文被误删")
	}
}

func TestStripBoardLabels(t *testing.T) {
	in := "LEFT: three views.\nLABELS (for the design sheet): name, tag.\nLOWER RIGHT: six tiles."
	got := StripBoardLabels(in)
	if strings.Contains(got, "LABELS") {
		t.Errorf("LABELS 行应删掉：%s", got)
	}
	if !strings.Contains(got, "three views") || !strings.Contains(got, "six tiles") {
		t.Errorf("其余行必须原样保留：%s", got)
	}
}
