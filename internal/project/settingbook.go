package project

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// settingbook.go 技能侧**设定集**的结构化读取（`data/小说/<作品>/设定集/`）。
//
// ## 为什么要读它（升级队列 §I，2026-09-28 读盘实测）
//
// 技能侧已经有平台正需要的数据，而且是结构化的：
//
//	人物外形锚表.md    外形**唯一权威**，要求"逐项照抄"；含比例锚（7.5 / 8 头身两档）
//	重点道具表.md      名称/外形/功能边界/来历/标志性细节/首次出现/锚定章（含 `锚定章_机器`）
//	数字锚表.md        全书数字口径（防"两处都对、合起来错"）
//	视觉风格卡-*.md    画风定义（本片该长什么样）
//
// **而平台从来不读它们**（当时全仓搜索无引用）—— 只把正文喂给分镜 LLM。后果三类：
//
//	① 分镜里写了器物动作却没写 `prop_ids`（器物漂移的高发区）
//	② 角色卡缺比例锚 ⇒ 同集内头身比互相打架
//	③ 器物/人物的 id 与命名靠模型现编，跨集不稳定
//
// ## 与「五个专职会话」的关系（用户 2026-09-28 定的架构）
//
// ①小说转剧本跑完之后，②人物道具 / ④场景 / ⑤分镜要**并行**且互不影响。
// 三条独立会话既要各跑各的、又要**用同一套名字与设定**，唯一可行的办法是
// **各自从同一份真源取数** —— 这份设定集就是那个真源，而不是让三个会话各自去正文里猜。
//
// 本文件只负责**读**（解析成结构），不负责"怎么用"；注入策略在各自的会话里定。

// SettingField 锚表里的一行 `- 键：值`。
type SettingField struct {
	Key   string
	Value string
}

// SettingChar 人物外形锚表里的一个角色。
type SettingChar struct {
	Name   string         // 祁七
	Note   string         // 标题括号里的定位说明（"主角。机器人／机甲形态……"）
	Fields []SettingField // 按**原文顺序**（拼提示词时要保序，别打乱作者的排列）
	Raw    string         // 原始小节文本（"逐项照抄"用）
	// Cut 这一节是被**剧本正文的起点标记**截断的（而不是被下一个角色标题正常结束）。
	//
	// 【为什么值得单独记一位】（2026-10-07 真机）《名册之外》第01集 source.txt 里「断桅」那一节
	// 只写到 `- 身形：瘦，背驼，走路` 就直接接上了【本集剧情】…，随后是整集剧本 ——
	// 不设边界时 Raw 一路吞到文末（4846 字，大半是剧本正文）。截断本身是**源文本的问题**，
	// 平台能做的只有两件事：不把正文当成外形描述用，以及**如实点名**告诉用户这一节不全。
	Cut bool
}

// Field 按键取值（找不到返回空串）。
func (c SettingChar) Field(key string) string {
	for _, f := range c.Fields {
		if strings.TrimSpace(f.Key) == key {
			return strings.TrimSpace(f.Value)
		}
	}
	return ""
}

// SettingProp 重点道具表里的一件道具。
type SettingProp struct {
	Index  int
	Name   string
	Fields []SettingField
	Raw    string
}

// Field 按键取值。
func (p SettingProp) Field(key string) string {
	for _, f := range p.Fields {
		if strings.TrimSpace(f.Key) == key {
			return strings.TrimSpace(f.Value)
		}
	}
	return ""
}

// AnchorChapters 解析 `锚定章_机器`（"1,4,8,16"）→ []int。
//
// 这一条是**给机器看的**：平台据此把该章的 `prop_ids` 确定性列进分镜，
// 而不是让模型自己判断"这件器物该不该出现在这一章"。
func (p SettingProp) AnchorChapters() []int {
	raw := strings.TrimSpace(p.Field("锚定章_机器"))
	if raw == "" {
		return nil
	}
	var out []int
	for _, seg := range strings.Split(raw, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		n := 0
		ok := true
		for _, r := range seg {
			if r < '0' || r > '9' {
				ok = false
				break
			}
			n = n*10 + int(r-'0')
		}
		if ok && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// SettingAnchor 数字锚表里的一条锚。
type SettingAnchor struct {
	Name  string // 锚名（"现在是哪一年"）
	Value string // 定值（"灰纪元第八万年"）
	Note  string // 备注（易错点）
}

// SettingBook 一个作品的设定集（缺哪份就是空，不是错误）。
type SettingBook struct {
	Dir        string
	Characters []SettingChar
	Props      []SettingProp
	Numbers    []SettingAnchor
	StyleCard  string // 视觉风格卡原文（画风定义）
	StyleName  string // 从文件名取的风格名
}

// SettingFiles 设定集里平台要读的四类文件（文件名前缀 → 用途）。
//
// 用前缀匹配而不是写死全名：技能侧给风格卡起的名字带画风（`视觉风格卡-3D写实动漫脸.md`），
// 换一部戏就换名字 —— 写死全名会在下一部戏上静默读不到。
var (
	settingCharFile   = "人物外形锚表"
	settingPropFile   = "重点道具表"
	settingNumberFile = "数字锚表"
	settingStyleFile  = "视觉风格卡"
)

// LoadSettingBook 读一个作品的设定集目录。
//
// 纪律：**目录不存在 / 单个文件不存在都不算错误**（返回 nil 或跳过该部分）——
// 平台要能处理"这部戏没有设定集"（老包、纯原创、技能侧还没产出的中间态），
// 那种情况下退回"只喂正文"的旧行为，而不是让整条流水线失败。
func LoadSettingBook(settingDir string) (*SettingBook, error) {
	st, err := os.Stat(settingDir)
	if err != nil || !st.IsDir() {
		return nil, nil
	}
	b := &SettingBook{Dir: settingDir}
	if raw := readSettingFile(settingDir, settingCharFile); raw != "" {
		b.Characters = ParseCharAnchor(raw)
	}
	if raw := readSettingFile(settingDir, settingPropFile); raw != "" {
		b.Props = ParsePropTable(raw)
	}
	if raw := readSettingFile(settingDir, settingNumberFile); raw != "" {
		b.Numbers = ParseNumberAnchor(raw)
	}
	if name, raw := readSettingFileAny(settingDir, settingStyleFile); raw != "" {
		b.StyleCard = raw
		b.StyleName = name
	}
	if len(b.Characters) == 0 && len(b.Props) == 0 && len(b.Numbers) == 0 && b.StyleCard == "" {
		return nil, nil // 目录在但一份都没读到：按"没有设定集"处理
	}
	return b, nil
}

// readSettingFile 按**前缀**找一个 .md 文件并读回内容（找不到返回空串）。
func readSettingFile(dir, prefix string) string {
	_, raw := readSettingFileAny(dir, prefix)
	return raw
}

func readSettingFileAny(dir, prefix string) (string, string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".md", ".txt", ".markdown":
		default:
			continue
		}
		if raw, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			return e.Name(), string(raw)
		}
	}
	return "", ""
}

var (
	// reCharHead 人物外形锚表的小节标题。
	//
	// 同一张表有**两种出现形态**，都必须认（2026-09-28 统一解析器时踩到）：
	//   · 设定集文件里带井号          → `### 祁七（定位：主角。……）`
	//   · 被作者整段塞进 source.txt 时通常不带 → `祁七（定位：主角。……）`
	// 所以井号写成"0–6 个"；并且**括号里的定位说明要一起收进匹配** —— 它是 `Note` 的来源
	// （选角色 kind 时要用："机器人／机甲形态" 与 "工具人" 不是一类）。
	reCharHead = regexp.MustCompile(`(?m)^#{0,6}\s*([^\r\n（(#]{1,14}（定位[:：][^\r\n]*）?)\s*$`)
	// reField 锚表里的 `- 键：值`（半角/全角冒号都要认）。
	reField = regexp.MustCompile(`(?m)^\s*[-*]\s*([^:：]+)[:：]\s*(.*)$`)
	// rePropHead 道具小节标题：`### 1 记名链` 或 `### 记名链`。
	rePropHead = regexp.MustCompile(`(?m)^###\s*(?:(\d+)\s+)?(.+?)\s*$`)
	// reTableRow markdown 表格行 `| 项 | 内容 |`。
	reTableRow = regexp.MustCompile(`(?m)^\|\s*([^|]+?)\s*\|\s*([^|]*?)\s*\|\s*$`)
)

// ParseCharAnchor 解析《人物外形锚表》。
//
// 认的是**作者的写法**：小节标题 `### 名字（定位：…）` + 若干 `- 字段：值`。
// 标题里的括号内容拆成 Note（它是"这个人在片里是什么身份"，对选角色 kind 有用）。
func ParseCharAnchor(md string) []SettingChar {
	var out []SettingChar
	for _, sec := range splitSections(md, reCharHead) {
		name, note := splitNameNote(sec.Title)
		if name == "" {
			continue
		}
		// **正文边界**：锚表被整段塞进 source.txt 时，最后一个角色的"小节"会一路吃到文末，
		// 把整集剧本吞进来（真机：断桅 Raw 4846 字，里面大半是【本集剧情】与逐场正文）。
		body, cut := cutAtScriptBody(sec.Body)
		c := SettingChar{Name: name, Note: note, Raw: strings.TrimSpace(body), Cut: cut}
		for _, m := range reField.FindAllStringSubmatch(body, -1) {
			k := strings.TrimSpace(m[1])
			v := strings.TrimSpace(m[2])
			if k == "" {
				continue
			}
			c.Fields = append(c.Fields, SettingField{Key: k, Value: v})
		}
		// 一个字段都没有的"小节"多半是正文里的三级标题，不是角色
		if len(c.Fields) == 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

// scriptBodyMarkers 剧本正文 / 平台节的起点标记：锚表小节吃到这些标记就说明**这一节被正文截断了**。
//
// 与 `internal/server` 的 briefEndMarkers 同一口径（那里治的是"【本集剧情】段的读写边界"）：锚表与剧本
// 被拼进同一个 source.txt 时，只有平台自己的节标记能可靠地划出边界 —— 空行、缩进、井号都不行
// （作者在每一项之间就爱空一行）。
var scriptBodyMarkers = []string{"【本集剧情】", "【本集原文】", "【前情提要】", "【开场爆点】", "【场 "}

// cutAtScriptBody 在**最早出现的**正文标记处截断；没有标记就原样返回（cut=false）。
func cutAtScriptBody(md string) (string, bool) {
	cut := -1
	for _, m := range scriptBodyMarkers {
		if i := strings.Index(md, m); i >= 0 && (cut < 0 || i < cut) {
			cut = i
		}
	}
	if cut < 0 {
		return md, false
	}
	return md[:cut], true
}

// ParsePropTable 解析《重点道具表》。
//
// 认的是**表格**写法：`### N 道具名` + `| 项 | 内容 |` 行（表头与分隔行自动跳过）。
func ParsePropTable(md string) []SettingProp {
	var out []SettingProp
	for _, sec := range splitSections(md, rePropHead) {
		name := strings.TrimSpace(sec.Title)
		if name == "" {
			continue
		}
		p := SettingProp{Index: sec.Index, Name: name, Raw: strings.TrimSpace(sec.Body)}
		for _, m := range reTableRow.FindAllStringSubmatch(sec.Body, -1) {
			k := strings.TrimSpace(m[1])
			v := strings.TrimSpace(m[2])
			if k == "" || k == "项" || strings.HasPrefix(k, "---") {
				continue // 表头与分隔行
			}
			p.Fields = append(p.Fields, SettingField{Key: k, Value: v})
		}
		// 名称以表格里的"名称"字段为准（标题里的可能带序号）
		if n := p.Field("名称"); n != "" {
			p.Name = n
		}
		if len(p.Fields) == 0 {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ParseNumberAnchor 解析《数字锚表》的锚清单表格。
//
// 表头形如：`| 锚名 | 定值（唯一） | 依据 | 已出现过此数的章 | 备注（易错点） |`
// —— 列数可能变（作者会加列），所以按"前三列"取，多的并进备注。
func ParseNumberAnchor(md string) []SettingAnchor {
	var out []SettingAnchor
	for _, m := range splitTableRows(md) {
		if m[0] == "锚名" || strings.HasPrefix(m[0], "---") {
			continue
		}
		a := SettingAnchor{Name: m[0], Value: cell(m, 1)}
		rest := []string{}
		for _, c := range m[2:] {
			if c != "" {
				rest = append(rest, c)
			}
		}
		a.Note = strings.Join(rest, "；")
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type settingSection struct {
	Title string
	Body  string
	Index int
}

// splitSections 按标题正则切小节：每段从标题行到下一个标题之前。
func splitSections(md string, head *regexp.Regexp) []settingSection {
	idx := head.FindAllStringSubmatchIndex(md, -1)
	if len(idx) == 0 {
		return nil
	}
	var out []settingSection
	for i, m := range idx {
		end := len(md)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		body := md[m[1]:end]
		sec := settingSection{Body: body}
		// 标题组可能有两组（道具表：序号 + 名字）
		if len(m) >= 4 && m[2] >= 0 && m[3] > m[2] {
			sec.Index = atoiSafe(strings.TrimSpace(md[m[2]:m[3]]))
		}
		gi := 4
		if len(m) >= 6 && m[4] >= 0 && m[5] > m[4] {
			sec.Title = strings.TrimSpace(md[m[4]:m[5]])
		} else if gi == 4 && m[2] >= 0 && m[3] > m[2] {
			// 单组标题（人物锚表）
			sec.Title = strings.TrimSpace(md[m[2]:m[3]])
		}
		out = append(out, sec)
	}
	return out
}

// splitTableRows 把 markdown 表格切成行 × 列。
func splitTableRows(md string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		raw := strings.Split(strings.Trim(line, "|"), "|")
		cells := make([]string, 0, len(raw))
		for _, c := range raw {
			cells = append(cells, strings.TrimSpace(c))
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}
	return rows
}

func cell(row []string, i int) string {
	if i < len(row) {
		return row[i]
	}
	return ""
}

// splitNameNote `祁七（定位：主角。机器人／机甲形态）` → ("祁七", "定位：主角。…")。
func splitNameNote(s string) (string, string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "（("); i > 0 {
		name := strings.TrimSpace(s[:i])
		note := strings.TrimSpace(strings.TrimRight(s[i:], "）)"))
		note = strings.TrimLeft(note, "（(")
		return name, note
	}
	return s, ""
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
