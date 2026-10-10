// wallpapers.go 壁纸库：**手机壁纸**与**桌面壁纸**两分类（2026-10-10 用户：
// 「新增"壁纸库"，分类"手机壁纸"和"桌面壁纸"，新增用户描述然后结合LLM生成提示词后编辑进行文生图
// （分辨率配置、是否同时生成移动端和桌面端等相关参数配置）」）。
//
// 链路：**用户描述 → 平台 LLM 写成一段英文正向提示词 → 用户可编辑 → 文生图（本机 ComfyUI）→ 落盘进壁纸库**。
//
// ## 为什么提示词必须由平台收口（不能把用户那句话直接送进引擎）
//
// 平台自己的图像侧口径（`internal/pipeline/artstyle.go` 顶部）：出图只吃英文，**汉字注解会被模型
// 当成"要画的字"照抄**；而 2026-10-10 那一轮又把另一半钉死了 —— 出图侧 **cfg=1.0：否定词不生效，
// 且提示词里提到的名词会被当成要画的东西**（真机证据：词里写着 no other text，画面里照样多出一行
// 乱码伪字）。壁纸最怕的正是这个：**画面上出现乱码字**，还天天挂在桌面上。
//
// 所以收口三条（与门一一对应）：
//
//	① 整段**不许有汉字**（用户的中文描述不进提示词）；
//	② **不许出现文字类名词**（text/lettering/title/caption/subtitle/watermark/logo/signature/sign/number）
//	   —— 一个都不提，模型就没理由画字；
//	③ **不许写否定句**（no/without/avoid/free of）：要"没有"什么，就只描述**要**的画面。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 两分类的**唯一出处**（前端、存储目录、尺寸预设、提示词口径都按它分）。
const (
	wallpaperKindMobile  = "mobile"
	wallpaperKindDesktop = "desktop"
)

// wallpaperRoot 壁纸库根目录（数据根下，与小说库/项目库同级）。
func (s *Server) wallpaperRoot() string {
	return filepath.Join(strings.TrimSpace(s.cfg.Get().DataDir), "wallpapers")
}

// 分类目录与路径守卫的**唯一出处**在 wallpaper_dirs.go（输出目录可由用户改）：
// `wallpaperDirOf(kind)` / `inWallpaperDir(p, kind)` / `wallpaperKindOfPath(p)`。
// 这里只留"配置/默认根"（wallpaperRoot）与分类归一化，避免两处各判一套。

// normWallpaperKind 只认两个分类；其余（含空）归到手机壁纸（默认值写死在一处）。
func normWallpaperKind(kind string) string {
	if strings.EqualFold(strings.TrimSpace(kind), wallpaperKindDesktop) {
		return wallpaperKindDesktop
	}
	return wallpaperKindMobile
}

// wallpaperSize 一个可选分辨率。**16 对齐**是图像侧的硬要求（见 normWallpaperSize）。
type wallpaperSize struct {
	W     int    `json:"w"`
	H     int    `json:"h"`
	Label string `json:"label"`
}

// wallpaperSizes 分类的分辨率档（2026-10-10 用户：「壁纸分辨率参数多增加些」）。
//
// 两条硬约束（不是我的偏好，是图像侧的）：**每条边 16 对齐**、**夹在 [512,2048]**。
// 所以这里**不可能**有 2K/4K 档（2560/3440/3840/7680 全都超过上限 2048）——
// 想要"和屏幕一样大"的观感，用 `sizeForScreen`（保持屏幕比例、把最长边压到 2048）。
// 门 `TestWallpaperSizesAre16AlignedAndBounded` 直接喂这个函数，并且要求**标签开头的尺寸 == 实际像素**
// （不许标签写 1080、实际出 1072 这种"写着好看"的档）。
func wallpaperSizes(kind string) []wallpaperSize {
	if normWallpaperKind(kind) == wallpaperKindDesktop {
		return []wallpaperSize{
			{W: 2048, H: 1152, Label: "2048×1152（16:9 最大档 · 出图上限 2048）"},
			{W: 1920, H: 1088, Label: "1920×1088（16:9 主流桌面）"},
			{W: 1920, H: 1200, Label: "1920×1200（16:10 桌面）"},
			{W: 2048, H: 864, Label: "2048×864（21:9 带鱼屏 · 3440×1440 一类）"},
			{W: 1600, H: 896, Label: "1600×896（16:9 小桌面/笔记本）"},
			{W: 1280, H: 720, Label: "1280×720（省显存 · 快速试稿）"},
		}
	}
	return []wallpaperSize{
		{W: 1088, H: 1920, Label: "1088×1920（9:16 主流手机）"},
		{W: 1200, H: 1920, Label: "1200×1920（5:8 大屏手机）"},
		{W: 896, H: 1600, Label: "896×1600（9:16 小屏手机）"},
		{W: 768, H: 1360, Label: "768×1360（9:15 竖版小档）"},
		{W: 720, H: 1280, Label: "720×1280（省显存 · 快速试稿）"},
	}
}

// normWallpaperSize 规整分辨率：缺省/非法回落到该分类的第一档；否则夹到 [512,2048] 并对齐 16。
func normWallpaperSize(kind string, w, h int) (int, int) {
	def := wallpaperSizes(kind)[0]
	if w <= 0 || h <= 0 {
		return def.W, def.H
	}
	clamp := func(v int) int {
		if v < wallpaperMinSide {
			v = wallpaperMinSide
		}
		if v > wallpaperMaxSide {
			v = wallpaperMaxSide
		}
		return v - v%wallpaperAlign
	}
	return clamp(w), clamp(h)
}

// wallpaperSizeNote 给界面显示"**实际会出成多少**"。
//
// 为什么必须有它：用户填 3440×1440 时，平台会夹到 2048、再对齐 16 ⇒ 实际出的是别的尺寸。
// 界面若照用户填的显示，就是"写着 3440、出的是 2048"——那是骗人。改了什么一律写出来。
func wallpaperSizeNote(kind string, w, h int) string {
	first := wallpaperSizes(kind)[0]
	if w <= 0 || h <= 0 {
		return fmt.Sprintf("没填尺寸 ⇒ 用本分类第一档 %d×%d", first.W, first.H)
	}
	aw, ah := normWallpaperSize(kind, w, h)
	if aw == w && ah == h {
		return ""
	}
	why := make([]string, 0, 2)
	if w > wallpaperMaxSide || h > wallpaperMaxSide {
		why = append(why, fmt.Sprintf("单边上限 %d", wallpaperMaxSide))
	}
	if w < wallpaperMinSide || h < wallpaperMinSide {
		why = append(why, fmt.Sprintf("单边下限 %d", wallpaperMinSide))
	}
	if w%wallpaperAlign != 0 || h%wallpaperAlign != 0 {
		why = append(why, fmt.Sprintf("%d 对齐", wallpaperAlign))
	}
	return fmt.Sprintf("你填的是 %d×%d，按平台口径（%s）**实际出 %d×%d**", w, h, strings.Join(why, "、"), aw, ah)
}

// ── 本机屏幕分辨率（"按我的屏幕出一张"） ──────────────────────────────────

// screenSize 本机主屏分辨率（生产走 GetSystemMetrics；非 Windows / 读不到 ⇒ 0,0）。
func (s *Server) screenSize() (int, int) {
	if f := s.wallpaperScreen; f != nil {
		return f()
	}
	return desktopScreenSize()
}

// handleWallpaperSize GET /api/wallpapers/size?kind=&w=&h= —— 问一句"这个尺寸实际会出成多少"。
//
// 为什么要有它：用户在界面里填自定义宽高时，平台会**夹上限 + 对齐 16**，实际尺寸与填的不一样。
// 界面自己算一遍就会与 Go 那份口径**各写一套**（迟早分叉）⇒ 一律回来问这一处。
func (s *Server) handleWallpaperSize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := normWallpaperKind(q.Get("kind"))
	want, _ := strconv.Atoi(strings.TrimSpace(q.Get("w")))
	hant, _ := strconv.Atoi(strings.TrimSpace(q.Get("h")))
	aw, ah := normWallpaperSize(kind, want, hant)
	writeJSON(w, 200, map[string]any{
		"w":    aw,
		"h":    ah,
		"note": wallpaperSizeNote(kind, want, hant),
	})
}

// sizeForScreen 把屏幕分辨率映射成**平台能出的**壁纸尺寸：保持屏幕宽高比、最长边压到上限、
// 两边对齐 16、并夹在 [512,2048] 内。返回 (宽, 高, 人话说明)；屏幕读不到 ⇒ (0,0,"") 由调用方兜。
//
// 例（真机 3440×1440 带鱼屏）：出 2048×848 —— 说明里必须写清"屏幕是 3440×1440、上限 2048 ⇒ 出 2048×848"，
// 别让用户以为出的是原始尺寸。纯函数 ⇒ 门直接喂（不需要真屏幕）。
func sizeForScreen(sw, sh int) (int, int, string) {
	if sw <= 0 || sh <= 0 {
		return 0, 0, ""
	}
	w, h := sw, sh
	if w > wallpaperMaxSide || h > wallpaperMaxSide {
		if w >= h {
			h = h * wallpaperMaxSide / w
			w = wallpaperMaxSide
		} else {
			w = w * wallpaperMaxSide / h
			h = wallpaperMaxSide
		}
	}
	align := func(v int) int {
		v = v - v%wallpaperAlign
		if v < wallpaperMinSide {
			v = wallpaperMinSide
		}
		return v
	}
	w, h = align(w), align(h)
	note := fmt.Sprintf("本机屏幕 %d×%d", sw, sh)
	if sw > wallpaperMaxSide || sh > wallpaperMaxSide {
		note += fmt.Sprintf("；平台出图单边上限 %d ⇒ 按屏幕比例出 %d×%d", wallpaperMaxSide, w, h)
	} else {
		note += fmt.Sprintf(" ⇒ 按屏幕比例出 %d×%d（已对齐 %d）", w, h, wallpaperAlign)
	}
	return w, h, note
}

// ── 提示词口径（唯一出处） ────────────────────────────────────────────────

// wallpaperPromptSys 壁纸提示词撰稿人的系统提示词。
const wallpaperPromptSys = `你是壁纸画面的提示词撰稿人。给你一句用户的中文描述，你只输出**一段英文正向提示词**（供 Qwen-Image 出壁纸图）。

硬要求（逐条都得满足）：
1. 只输出那段英文提示词本身：不要解释、不要 markdown 围栏、不要编号、不要用引号把整段包起来。
2. **整段不许出现任何汉字**（汉字会被出图模型当成"要画的字"照抄到画面上）。
3. 只描述**画面本身**：主体、构图、光线、色彩、材质、氛围、留白。
   **不许出现任何文字类名词** —— text / lettering / title / caption / subtitle / watermark / logo /
   signature / sign / number 这些词一个都不要写。原因：出图侧 cfg=1.0，**否定词不生效**，而**提到的
   名词会被当成要画的东西**（真机实测：写了 no other text 之后，画面里照样多出一行乱码伪字）。
4. **不要写否定句**（no / without / avoid / free of）：要"没有"什么，就只描述你**要**的画面。
5. 手机壁纸是**竖版**、桌面壁纸是**横版**；构图要给图标与时钟留出**大片安静的留白**。
6. 6-10 个英文短语，逗号分隔；不要写成一段散文。

直接给出那段英文提示词本身。`

// wallpaperBannedRe 文字类名词与否定式：**出现即摘掉那个短语**（判据见文件头 ②③）。
//
// 为什么按短语摘而不是"整句重写"：提示词是逗号分隔的短语清单，摘掉一个短语不会把句子打断；
// 而重写要再调一次模型（又一轮不可控）。摘掉了什么写进 Note，如实播报。
var wallpaperBannedRe = regexp.MustCompile(`(?i)\b(no|not|without|avoid|free of|` +
	`texts?|lettering|titles?|captions?|subtitles?|watermarks?|logos?|signatures?|signs?|numbers?|` +
	`letters?|words?|typeface|fonts?)\b`)

// wallpaperSeedPrompt 拿不到 LLM 时的**兜底**：只用**平台自己的通用构图句**，一个字都不猜用户的内容。
//
// 刻意**不用**用户那句中文描述：提示词里不许有汉字（口径①），而把中文描述翻成英文这件事
// 正是 LLM 的活；翻不了就如实说"LLM 没用上，这是通用构图词"，让用户自己改 —— 不许假装是他描述的。
func wallpaperSeedPrompt(kind string) string {
	if normWallpaperKind(kind) == wallpaperKindDesktop {
		return "Horizontal desktop wallpaper, wide cinematic landscape composition, " +
			"large calm negative space, soft gradient sky, distant layered mountains, " +
			"gentle mist, muted teal and warm grey palette, smooth matte texture, " +
			"even ambient light, minimalist and uncluttered"
	}
	return "Vertical phone wallpaper, tall portrait composition, " +
		"large calm negative space in the upper half, soft gradient sky, " +
		"a single small silhouette on the horizon, gentle mist, muted indigo and warm sand palette, " +
		"smooth matte texture, even ambient light, minimalist and uncluttered"
}

// wallpaperPromptUsable 收口后还成不成句：≥40 字符、≥5 个 ASCII 单词、不含汉字、不含被禁词。
func wallpaperPromptUsable(p string) bool {
	if len([]rune(p)) < 40 {
		return false
	}
	if project.CountCJK(p) > 0 {
		return false
	}
	if wallpaperBannedRe.MatchString(p) {
		return false
	}
	words := 0
	for _, w := range strings.FieldsFunc(p, func(r rune) bool { return r < 'A' || (r > 'Z' && r < 'a') || r > 'z' }) {
		if len(w) >= 2 {
			words++
		}
	}
	return words >= 5
}

// cleanWallpaperPrompt 平台收口：剥围栏/前缀 → 压成一行 → 清汉字 → 摘掉文字类名词与否定式短语。
// 返回 (词, 附加说明, 可用)；不可用时调用方走兜底（并如实标注）。
func cleanWallpaperPrompt(raw string) (prompt, note string, ok bool) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "```"); i >= 0 {
		parts := strings.Split(s, "```")
		best := ""
		for _, p := range parts {
			p = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "text"))
			if len([]rune(p)) > len([]rune(best)) {
				best = p
			}
		}
		s = best
	}
	for _, pre := range []string{"Prompt:", "Prompt：", "提示词：", "提示词:", "英文提示词：", "英文提示词:"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, pre))
	}
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	cleaned, removed := stripCJK(s)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if removed > 0 {
		note = fmt.Sprintf("清掉了 %d 个汉字（汉字会被当字形画到画面上）", removed)
	}
	// 逐短语摘掉"文字类名词/否定式"（逗号分隔的清单，摘一个不会打断句子）
	kept := make([]string, 0, 12)
	dropped := 0
	for _, ph := range strings.Split(cleaned, ",") {
		p := strings.TrimSpace(ph)
		if p == "" {
			continue
		}
		if wallpaperBannedRe.MatchString(p) {
			dropped++
			continue
		}
		kept = append(kept, p)
	}
	if dropped > 0 {
		note = appendNote(note, fmt.Sprintf("摘掉 %d 个「文字/否定」短语（cfg=1.0 下否定词不生效，提到的名词会被画出来）", dropped))
	}
	cleaned = strings.Join(kept, ", ")
	if !wallpaperPromptUsable(cleaned) {
		return "", note, false
	}
	return cleaned, note, true
}

// llmWallpaperPrompt 让平台 LLM 按用户描述写一份壁纸词，并做**平台侧收口**。
func (s *Server) llmWallpaperPrompt(desc, kind string) (string, string, error) {
	g := s.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	if !client.Configured() {
		return "", "", fmt.Errorf("平台 LLM 未配置（设置里填 LLM 地址/密钥/模型）")
	}
	timeout := g.LLM.Timeout
	if timeout <= 0 {
		timeout = 120
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	orient := "竖版手机壁纸"
	if normWallpaperKind(kind) == wallpaperKindDesktop {
		orient = "横版桌面壁纸"
	}
	raw, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: wallpaperPromptSys},
		{Role: "user", Content: "用户描述：" + strings.TrimSpace(desc) + "\n要出的是：" + orient},
	}, false)
	if err != nil {
		return "", "", fmt.Errorf("LLM 调用失败：%w", err)
	}
	p, note, ok := cleanWallpaperPrompt(raw)
	if !ok {
		return "", "", fmt.Errorf("LLM 给的词清洗后不成句（整段像中文说明，或只剩被禁的词）")
	}
	return p, note, nil
}

// wallpaperPrompts 一条提示词 + 它的来源（**必须能播报**：用户要能看出这行词是谁写的）。
type wallpaperPrompts struct {
	Prompt string `json:"prompt"`
	From   string `json:"from"`
	Note   string `json:"note,omitempty"`
}

// wallpaperPromptFor 用户描述 → 一份可用提示词（LLM 优先，兜底通用构图词 + 如实标注来源）。
func (s *Server) wallpaperPromptFor(desc, kind string) wallpaperPrompts {
	desc = strings.TrimSpace(desc)
	if desc != "" {
		p, note, err := s.llmWallpaperPrompt(desc, kind)
		if err == nil {
			return wallpaperPrompts{Prompt: p, From: "平台 LLM 按你的描述写", Note: note}
		}
		seed := wallpaperSeedPrompt(kind)
		return wallpaperPrompts{Prompt: seed, From: "通用构图词（LLM 没用上）",
			Note: appendNote("LLM 没用上："+err.Error(), "这是平台通用构图词，**不含你的描述**，请自行编辑")}
	}
	return wallpaperPrompts{Prompt: wallpaperSeedPrompt(kind), From: "通用构图词（没给你描述）",
		Note: "想让它像你描述的那样，先在描述框里写一句，或点「AI 写词」"}
}

// ── 存储 ────────────────────────────────────────────────────────────────

// wallpaperItem 壁纸库里的一张图（**清单从盘上算**，不维护索引文件：少一处会失同步的状态）。
type wallpaperItem struct {
	File    string `json:"file"`    // 绝对路径（前端走 /api/wallpapers/image?path= 读）
	Kind    string `json:"kind"`    // mobile / desktop
	W       int    `json:"w"`       //
	H       int    `json:"h"`       //
	Seed    int64  `json:"seed"`    //
	Prompt  string `json:"prompt"`  // 出这张图用的正向词
	From    string `json:"from"`    // 这行词是谁写的
	Created string `json:"created"` // RFC3339
	KB      int64  `json:"kb"`      //
}

// wallpaperMetaPath 一张壁纸的伴随文件（正向词、参数、来源）。
func wallpaperMetaPath(img string) string {
	return strings.TrimSuffix(img, filepath.Ext(img)) + ".json"
}

// writeWallpaperMeta 落一张壁纸的伴随文件（正向词/参数/来源）。
//
// 失败**不致命**（图还在，只是清单里那一项缺了词），但**不许静默**：审计门
// `TestNoSwallowedPersistenceErrors` 抓的就是"流程照跑、日志照报成功" —— 写失败要如实播报。
func (s *Server) writeWallpaperMeta(img string, it wallpaperItem) {
	b, err := json.MarshalIndent(it, "", "  ")
	if err != nil {
		s.opLog("⚠ 壁纸伴随信息序列化失败(%s):%v", filepath.Base(img), err)
		return
	}
	if err := os.WriteFile(wallpaperMetaPath(img), b, 0o644); err != nil {
		s.opLog("⚠ 壁纸伴随信息写失败(%s):%v —— 图还在，但这一张记不住正向词与来源", filepath.Base(img), err)
	}
}

// readWallpaperMeta 读伴随文件（没有就用文件信息兜一份最小值，不假装知道词）。
func readWallpaperMeta(img, kind string) wallpaperItem {
	it := wallpaperItem{File: img, Kind: kind}
	if b, err := os.ReadFile(wallpaperMetaPath(img)); err == nil {
		_ = json.Unmarshal(b, &it)
	}
	if it.File == "" {
		it.File = img
	}
	if it.Kind == "" {
		it.Kind = kind
	}
	if fi, err := os.Stat(img); err == nil {
		it.KB = fi.Size() / 1024
		if it.Created == "" {
			it.Created = fi.ModTime().Format(time.RFC3339)
		}
	}
	return it
}

// listWallpapers 扫盘列出某分类的壁纸（按创建时间倒序）。只认图片扩展名。
//
// 目录**不给参数**：从 `wallpaperDirOf(kind)` 取（用户可能把输出目录改到别处）——
// 让调用方自己拼目录，就是给"清单读 A 处、生成写 B 处"留门。
func (s *Server) listWallpapers(kind string) []wallpaperItem {
	kind = normWallpaperKind(kind)
	dir := s.wallpaperDirOf(kind)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]wallpaperItem, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !coverImageExts[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		out = append(out, readWallpaperMeta(filepath.Join(dir, e.Name()), kind))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out
}

// ── HTTP ───────────────────────────────────────────────────────────────

// handleWallpapers GET /api/wallpapers —— 壁纸库现状（两分类 + 目录 + 尺寸档 + LLM 就绪）。
func (s *Server) handleWallpapers(w http.ResponseWriter, r *http.Request) {
	sw, sh := s.screenSize()
	fitW, fitH, screenNote := sizeForScreen(sw, sh)
	writeJSON(w, 200, map[string]any{
		"root":    s.wallpaperRoot(), // 配置/默认根（输出目录可能被用户改到别处，见 dirs）
		"mobile":  s.listWallpapers(wallpaperKindMobile),
		"desktop": s.listWallpapers(wallpaperKindDesktop),
		"dirs":    s.wallpaperDirInfo(), // 每个分类当前的输出目录 + 是不是改过 + 存不存在
		"sizes":   map[string]any{"mobile": wallpaperSizes(wallpaperKindMobile), "desktop": wallpaperSizes(wallpaperKindDesktop)},
		// 图像侧硬口径给界面显示（用户要自定义尺寸时必须能看见"为什么 4K 出不了"）
		"limits": map[string]any{"max_side": wallpaperMaxSide, "min_side": wallpaperMinSide, "align": wallpaperAlign},
		// 本机屏幕（"按我的屏幕出一张"）：读不到就 0/空，界面据此置灰
		"screen":    map[string]any{"w": sw, "h": sh, "fit_w": fitW, "fit_h": fitH, "note": screenNote},
		"llm_ready": s.llmReady(), // 「AI 写词」能不能用（没配 LLM 时前端置灰，别让用户点了才报错）
		// 提示词口径给前端显示（用户改词时看得见规矩）：只说**要**什么，不列禁用词清单
		"hint": "提示词用英文；画面里不要出现任何文字（提了文字类名词，模型就会画字）",
	})
}

// handleWallpaperImage GET /api/wallpapers/image?path= —— 只读输出壁纸图
// （路径必须落在**该分类当前的输出目录**里；分类不认识就 403）。
func (s *Server) handleWallpaperImage(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if s.wallpaperKindOfPath(p) == "" {
		writeErr(w, 403, fmt.Errorf("路径不在壁纸的输出目录里（若你改过输出目录，老目录里的图就不再属于这一类）"))
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	defer f.Close()
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".webp":
		w.Header().Set("Content-Type", "image/webp")
	default:
		w.Header().Set("Content-Type", "image/jpeg")
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, f)
}

// handleWallpaperPrompt POST /api/wallpapers/prompt {desc,kind}
//
// 「AI 写词」按钮走这条：**强制走 LLM**（用户点它就是要一份按自己描述写的词）；
// LLM 拿不到就回通用构图词 + 如实说明（200，不报 500 —— 用户要的是"一行能改的词"）。
func (s *Server) handleWallpaperPrompt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Desc string `json:"desc"`
		Kind string `json:"kind"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败：%w", err))
		return
	}
	kind := normWallpaperKind(in.Kind)
	desc := strings.TrimSpace(in.Desc)
	if desc == "" {
		writeErr(w, 400, fmt.Errorf("请先写一句描述（AI 要按它写词）"))
		return
	}
	g := s.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	if !client.Configured() {
		s.opLog("🎨 壁纸提示词:LLM 未配置 ⇒ 回通用构图词")
		writeJSON(w, 200, wallpaperPrompts{Prompt: wallpaperSeedPrompt(kind), From: "通用构图词（LLM 没配置）",
			Note: "设置里填了 LLM 之后，这条会按你的描述写"})
		return
	}
	p, note, err := s.llmWallpaperPrompt(desc, kind)
	if err != nil {
		s.opLog("⚠ 壁纸提示词:LLM 没用上(%v) ⇒ 回通用构图词", err)
		writeJSON(w, 200, wallpaperPrompts{Prompt: wallpaperSeedPrompt(kind), From: "通用构图词（LLM 没用上）",
			Note: appendNote("LLM 没用上："+err.Error(), "这是平台通用构图词，**不含你的描述**，请自行编辑")})
		return
	}
	s.opLog("🎨 壁纸提示词:《%s》来自平台 LLM（%d 字%s）", descCut(desc),
		len([]rune(p)), map[bool]string{true: "；" + note, false: ""}[note != ""])
	writeJSON(w, 200, wallpaperPrompts{Prompt: p, From: "平台 LLM 按你的描述写", Note: note})
}

// handleWallpaperGenerate POST /api/wallpapers/generate
// {desc,prompt,kind,width,height,seed,count,both} —— 文生图并**落盘进壁纸库**。
//
// 参数口径（用户点名要的几项）：
//   - **分辨率**：给 width/height 就用（夹到 [512,2048] 并对齐 16）；不给就用该分类第一档；
//   - **是否同时出两端**：both=true ⇒ 手机 + 桌面各出一套（同一条词，各按自己的画幅）；
//   - 张数 1..4、seed（0=随机）。
//
// 失败纪律（与封面那轮同一条教训）：**引擎检查在造目录之前** —— 拿不到引擎就如实报错、
// **一个目录都不建、一张图都不落**（否则盘上留下一堆空目录，看起来像"生过但失败了"）。
func (s *Server) handleWallpaperGenerate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Desc   string `json:"desc"`
		Prompt string `json:"prompt"`
		Kind   string `json:"kind"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Seed   int64  `json:"seed"`
		Count  int    `json:"count"`
		Both   bool   `json:"both"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败：%w", err))
		return
	}
	eng, err := s.reg.Get("comfy")
	if err != nil {
		writeErr(w, 503, fmt.Errorf("本地 ComfyUI 引擎不可用:%w", err))
		return
	}
	count := in.Count
	if count <= 0 {
		count = 1
	}
	if count > 4 {
		count = 4
	}
	kinds := []string{normWallpaperKind(in.Kind)}
	if in.Both {
		kinds = []string{wallpaperKindMobile, wallpaperKindDesktop}
	}
	// 词：用户给的就用；没给就按描述（或通用构图词）现取一份，并把来源带回去
	prompts := map[string]wallpaperPrompts{}
	for _, k := range kinds {
		if p := strings.TrimSpace(in.Prompt); p != "" {
			prompts[k] = wallpaperPrompts{Prompt: p, From: "**用户给的词**"}
			continue
		}
		prompts[k] = s.wallpaperPromptFor(in.Desc, k)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(len(kinds)*count)*10*time.Minute)
	defer cancel()
	made := make([]wallpaperItem, 0, len(kinds)*count)
	for _, k := range kinds {
		w0, h0 := normWallpaperSize(k, in.Width, in.Height)
		dir := s.wallpaperDirOf(k) // 输出目录由用户定（见 wallpaper_dirs.go）；一行口径，别再各处拼
		if err := os.MkdirAll(dir, 0o755); err != nil {
			writeErr(w, 500, err)
			return
		}
		pr := prompts[k]
		s.opLog("🎨 壁纸出图:%s %d×%d ×%d（词来自:%s%s）", wallpaperKindLabel(k), w0, h0, count,
			pr.From, map[bool]string{true: "；" + pr.Note, false: ""}[pr.Note != ""])
		for i := 0; i < count; i++ {
			seed := in.Seed
			if seed == 0 {
				seed = pipeline.RandomSeed()
			} else if i > 0 {
				seed += int64(i) * 7919 // 一次多张：同一起点往外散（与封面同一手法）
			}
			name := fmt.Sprintf("wp-%s-%s-%d.png", k, time.Now().Format("20060102-150405"), seed)
			dst := filepath.Join(dir, name)
			if _, err := eng.GenerateImage(ctx, pr.Prompt, "", seed, w0, h0, dst); err != nil {
				if len(made) == 0 {
					writeErr(w, 500, fmt.Errorf("壁纸生成失败:%w", err))
					return
				}
				break // 出到一半：把已经出来的交出去，别让用户白等
			}
			it := wallpaperItem{File: dst, Kind: k, W: w0, H: h0, Seed: seed,
				Prompt: pr.Prompt, From: pr.From, Created: time.Now().Format(time.RFC3339)}
			s.writeWallpaperMeta(dst, it)
			if fi, err := os.Stat(dst); err == nil {
				it.KB = fi.Size() / 1024
			}
			made = append(made, it)
		}
	}
	if len(made) == 0 {
		writeErr(w, 500, fmt.Errorf("壁纸生成失败:没有产出任何图"))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": made, "count": len(made)})
}

// handleWallpaperDelete DELETE /api/wallpapers/item?file= —— 删壁纸（图 + 伴随文件）。
func (s *Server) handleWallpaperDelete(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("file"))
	if s.wallpaperKindOfPath(p) == "" {
		writeErr(w, 403, fmt.Errorf("路径不在壁纸的输出目录里（若你改过输出目录，老目录里的图就不再属于这一类）"))
		return
	}
	if err := os.Remove(p); err != nil {
		writeErr(w, 404, err)
		return
	}
	_ = os.Remove(wallpaperMetaPath(p))
	s.opLog("🗑 已删除壁纸:%s", filepath.Base(p))
	writeJSON(w, 200, map[string]any{"ok": true})
}

// wallpaperKindLabel 分类的人话名（播报与前端同一处）。
func wallpaperKindLabel(kind string) string {
	if normWallpaperKind(kind) == wallpaperKindDesktop {
		return "桌面壁纸"
	}
	return "手机壁纸"
}

// descCut 描述缩到日志里能放下的长度。
func descCut(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= 18 {
		return string(r)
	}
	return string(r[:18]) + "…"
}
