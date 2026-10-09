package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
)

// novels_style.go 渲染风格的**裁决**与**平台自有行的同步**（2026-10-09）。
//
// 用户原话：「小说库导入视频制作时，用户需要选择对应的渲染风格，目前所有视频渲染风格都是一样的」。
//
// 实测链路（本轮排查）：
//   - 导入制作时风格是**写死**的：`handleNovelProduce` 只读小说目录 `立项.json` 的 `art_style`，
//     用户在「视频制作」那一步**没有任何选择入口**（前端只有一个 ElMessageBox 确认框）。
//   - 这个串由 `splitNovelToProjects` 写进每集输入的平台自有行「全局风格:」，
//     再由 `director.extractGlobalStyle` 读回、`enforceStyleLock` **逐镜覆写 plan.Style**
//     —— 所以"风格由谁定"是确定的（好事），但"只能由立项定"就是这次要改的。
//   - 项目参数抽屉里那个「渲染视频风格」多选（`Script.Styles`）**只喂给分镜会话当提示**，
//     最终仍被 `enforceStyleLock` 用输入里的旧串覆写 ⇒ 用户在那里改了也看不出区别
//     （这正是"所有风格都一样"的另一半原因）。
//
// 本文件只做两件确定性的事：
//  1. 导入制作时把用户选的预设拼成**权威风格串**（而不是交给模型）；
//  2. 项目参数里改了风格 ⇒ **同步更新平台自己写的那行「全局风格:」**（唯一真源，幂等，原子写）。

// importStyle 一次导入制作的风格裁决结果。
type importStyle struct {
	Style   string   // 最终权威风格串（空 = 由 LLM 自定，之后仍被「全局风格:」锁定）
	From    string   // "user"（导入时选定）/ "brief"（立项 art_style）/ "llm"（都没有）
	Unknown []string // 用户勾了但平台不认的 key（如实点名，不静默吞）
	// Conflicts 勾选的预设里**介质互相矛盾**的组合说明（空 = 没有矛盾）。
	// 平台**照用户选的做**（用户拍板优先），但必须如实点名 —— 见 pipeline.StyleMediumConflicts。
	Conflicts []string
	Styles    []string // 用户勾选的 key：写进每集项目参数（项目参数抽屉看得见、重跑分镜 hint 一致）
	Hint      string   // 自由文本附加要求
}

// presetNames 把用户勾选的 key 译成中文显示名（播报用，如「国漫二维动画 + 水墨国风」）。
func (s importStyle) presetNames() string {
	return strings.Join(pipeline.StylePresetNames(s.Styles), " + ")
}

// resolveImportStyle 导入制作时的**风格裁决**（纯函数）。
//
// 优先级（写死，不交给模型）：
//  1. 用户在导入时勾了预设 / 写了附加要求 ⇒ 用它（From=user）；
//  2. 否则用小说自带的 `立项.json art_style`（From=brief）—— 这是改动前的老行为，保持不变；
//  3. 都没有 ⇒ 空（From=llm）：由分镜会话自定，之后照旧被「全局风格:」锁定。
func resolveImportStyle(briefStyle string, styles []string, freeHint string) importStyle {
	st := importStyle{
		Styles:    append([]string(nil), styles...),
		Hint:      strings.TrimSpace(freeHint),
		Unknown:   pipeline.UnknownStyleKeys(styles),
		Conflicts: pipeline.StyleMediumConflicts(styles),
	}
	if chosen := pipeline.ComposeStyleHint(styles, freeHint); chosen != "" {
		st.Style, st.From = chosen, "user"
		return st
	}
	if brief := strings.TrimSpace(briefStyle); brief != "" {
		st.Style, st.From = brief, "brief"
		return st
	}
	st.From = "llm"
	return st
}

// globalStyleLabel 平台写进每集输入的**自有行**标签（`novels_split.go` 的 createSeg 写它，
// `director.extractGlobalStyle` 读它）。改这里必须同步改那两处 —— 有测试钉着。
const globalStyleLabel = "全局风格:"

// setGlobalStyleLine 把 source 里的「全局风格:」行改成 style（**幂等**）。
//
// 行为（尽量不碰用户的字）：
//   - 已经有这一行 ⇒ 只替换这一行的值；
//   - 没有这一行（技能侧导入的历史包 / 手改过的输入）⇒ 在**第一行之后**插入一行，其余原样保留；
//   - style 为空 ⇒ 原样返回、changed=false（不把行删掉，也不写空值）。
//
// 返回 (新文本, 是否改动)。
func setGlobalStyleLine(src, style string) (string, bool) {
	style = strings.TrimSpace(style)
	if style == "" {
		return src, false
	}
	want := globalStyleLabel + style
	lines := strings.Split(src, "\n")
	for i, ln := range lines {
		if strings.Contains(ln, globalStyleLabel) {
			if strings.TrimSpace(ln) == want {
				return src, false // 已经是这个值：幂等，不产生无意义写入
			}
			lines[i] = want
			return strings.Join(lines, "\n"), true
		}
	}
	// 没有这一行：插在第一行（抬头）之后；整份为空时直接给一行。
	if len(lines) == 0 || strings.TrimSpace(src) == "" {
		return want + "\n", true
	}
	out := []string{lines[0], want}
	out = append(out, lines[1:]...)
	return strings.Join(out, "\n"), true
}

// globalStyleValue 取 source 里「全局风格:」当前的值（只为播报"原值是什么"；取不到就空）。
func globalStyleValue(src string) string {
	for _, ln := range strings.Split(src, "\n") {
		if i := strings.Index(ln, globalStyleLabel); i >= 0 {
			return strings.TrimSpace(ln[i+len(globalStyleLabel):])
		}
	}
	return ""
}

// syncGlobalStyleFromParams 项目参数里**显式选了**渲染风格 ⇒ 同步平台自有的「全局风格:」行。
//
// 【为什么必须同步（2026-10-09 排查）】`director.enforceStyleLock` 每一轮都会按输入里的这一行
// **代码级覆写** plan.Style（跨集一致的最终保障）。只把选择存进参数、不同步这一行 ⇒
// 分镜里用户选的风格会被旧串压回去，用户看到的就是「改了风格但成片没变」
// （用户原话：「目前所有视频渲染风格都是一样的」）。
//
// 边界（有意为之，别当 bug 修）：
//   - `styles` 为空 ⇒ **什么都不做**。把"取消勾选"解释成"换回原书风格"会覆盖用户在输入里
//     手写/技能侧给的风格串 —— 宁可不动。
//   - 读不到 source / 写失败 ⇒ **如实播报**（"参数存了但输入没跟上"），不假装成功。
//   - 这一行是**平台自己写的**（novels_split 的 createSeg），不是用户的正文/剧本。
func (s *Server) syncGlobalStyleFromParams(id string, params project.StageParams) {
	styles := params.Script.Styles
	if len(styles) == 0 {
		return
	}
	want := pipeline.ComposeStyleHint(styles, params.Script.StyleHint)
	if want == "" {
		return
	}
	src, err := s.store.LoadSource(id)
	if err != nil || strings.TrimSpace(src) == "" {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", ProjectID: id,
			Message: fmt.Sprintf("⚠ 渲染风格已存进参数,但读不到本集输入(%v)——「全局风格:」行没同步,"+
				"分镜仍会按输入里的旧串锁定", err)})
		return
	}
	old := globalStyleValue(src)
	next, changed := setGlobalStyleLine(src, want)
	if !changed {
		return // 幂等：这一行已经是用户选的那个串
	}
	if err := s.store.SaveSource(id, next); err != nil {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", ProjectID: id,
			Message: fmt.Sprintf("⚠ 渲染风格已存进参数,但写回本集输入失败(%v)——「全局风格:」行没同步,"+
				"分镜仍会按输入里的旧串锁定", err)})
		return
	}
	msg := "🎨 已把本集输入的「全局风格:」更新为你选的渲染风格"
	if names := strings.Join(pipeline.StylePresetNames(styles), " + "); names != "" {
		msg += "（" + names + "）"
	}
	if old != "" && old != want {
		msg += ";原值:" + clampRunes(old, 60)
	}
	msg += " —— 下一轮「剧本分镜 / 渲染」都按它（这一行是平台写的,不动你的正文与剧本）"
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info", ProjectID: id, Message: msg})
}

// novelStyleScan 小说自带的"视频风格"字段扫描结果（2026-10-09 用户：
// 「另外导入的小说也需要解析是否存在视频风格相关字段」）。
//
// 【真机取证】《我捡的团子，暖了三千人的船》的 立项.json 里同时有四个相关字段：
//
//	画风       = guofeng-starsea-painterly（东方星海 · 绘画感电影光：霜蓝 × 月白 × 旧金 …）中文，人读
//	渲染风格串 = PAINTERLY CINEMATIC EASTERN COSMIC FANTASY: …（英文，权威串）
//	art_style  = 与「渲染风格串」同值（英文，权威串）
//	风格       = { 主/次/与上一本的差异 } ← **文学风格，不是画风**，绝不能当画风串用
//
// 另外 设定集/视觉风格卡-鬼刀式东方星海.md 里第一段代码块**就是**那句风格串，卡自己也写着
// 「一句话风格串（立项.json 的 渲染风格串 / art_style 直接用）」。
// 平台此前只读 art_style：字段改名/缺失时就当"这本书没有风格"，风格卡更是从来没读过。
type novelStyleScan struct {
	Style string // 权威风格串（进「全局风格:」行 → director.enforceStyleLock → 每一镜）
	Field string // 取自哪里（播报用，必须说清，不许含糊成"读到了"）
	Card  string // 风格卡文件名（若有；只播报存在与字数，不把整张卡塞进风格串）
	Chars int    // 风格卡字数
	Hint  string // 人读的旁注（例如「画风」字段的中文名）——仅供界面展示
}

// Found 有没有扫到可用的风格串。
func (s novelStyleScan) Found() bool { return strings.TrimSpace(s.Style) != "" }

// scanNovelStyle 从小说目录里扫"视频风格"相关字段（**纯函数**，便于机检）。
//
// 优先级（写死，先精准后兜底）：
//  1. 立项.json 的 art_style（老行为，保持最高优先）
//  2. 立项.json 的 渲染风格串（同义字段；真机里两者同值，改名时互为兜底）
//  3. 立项.json 的 画风（中文画风名：**只在没有英文串时**兜底，比"没有"强）
//  4. 设定集/视觉风格卡-*.md 里**第一段代码块**（真机那张卡自己写明"这就是风格串"）
func scanNovelStyle(dir string) novelStyleScan {
	var out novelStyleScan
	brief := map[string]any{}
	if raw, err := os.ReadFile(filepath.Join(dir, "立项.json")); err == nil {
		_ = json.Unmarshal(raw, &brief)
	}
	pick := func(keys ...string) (string, string) {
		for _, k := range keys {
			if v, ok := brief[k].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v), k
			}
		}
		return "", ""
	}
	if s, k := pick("art_style", "渲染风格串"); s != "" {
		out.Style, out.Field = s, "立项.json 的 "+k
	}
	if h, _ := pick("画风"); h != "" {
		out.Hint = h
		if out.Style == "" {
			out.Style, out.Field = h, "立项.json 的画风（只有中文名，兜底）"
		}
	}
	if name, card := findStyleCard(dir); card != "" {
		out.Card, out.Chars = name, len([]rune(card))
		if out.Style == "" {
			if s := firstFencedBlock(card); s != "" {
				out.Style, out.Field = s, "设定集/"+name+" 的风格串代码块"
			}
		}
	}
	return out
}

// findStyleCard 在 设定集/ 里找「视觉风格卡*.md」，返回（文件名, 内容）。
//
// 多张卡时取**文件名排序最后**的一张：真机里 视觉风格卡-3D写实动漫脸.md 是技能自带的快照，
// 而 视觉风格卡-鬼刀式东方星海.md 是**本书专用**（按用户当次点名生成）—— 后者才是本书口径。
func findStyleCard(dir string) (string, string) {
	names, _ := filepath.Glob(filepath.Join(dir, "设定集", "*风格卡*.md"))
	if len(names) == 0 {
		names, _ = filepath.Glob(filepath.Join(dir, "*", "*风格卡*.md"))
	}
	if len(names) == 0 {
		return "", ""
	}
	sort.Strings(names)
	last := names[len(names)-1]
	raw, err := os.ReadFile(last)
	if err != nil {
		return "", ""
	}
	return filepath.Base(last), string(raw)
}

// firstFencedBlock 取 markdown 里的**第一段** 三反引号 代码块（多行拼成一行；没有就空）。
func firstFencedBlock(md string) string {
	fence := "`" + "`" + "`"
	i := strings.Index(md, fence)
	if i < 0 {
		return ""
	}
	rest := md[i+len(fence):]
	if j := strings.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[j+1:] // 跳掉语言标记那一行
	}
	j := strings.Index(rest, fence)
	if j < 0 {
		return ""
	}
	var parts []string
	for _, ln := range strings.Split(rest[:j], "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// novelStyle GET /api/novels/style?path=… —— 给「视频制作」对话框显示**原书风格**：
// 扫到的是什么串、来自哪个字段、有没有风格卡。找不到就 found=false（前端据此提示"自己选一个"）。
//
// 【为什么要有这条接口】用户 2026-10-09：「另外导入的小说也需要解析是否存在视频风格相关字段」——
// 解析结果必须**看得见**：只写在日志里，用户在导入那一刻仍然不知道这本书有没有风格、用的是哪个。
func (s *Server) novelStyle(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" || !s.inNovelRoot(p) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
		return
	}
	scan := scanNovelStyle(p)
	writeJSON(w, 200, map[string]any{
		"found":      scan.Found(),
		"style":      scan.Style,
		"field":      scan.Field,
		"card":       scan.Card,
		"card_chars": scan.Chars,
		"hint":       scan.Hint,
	})
}
