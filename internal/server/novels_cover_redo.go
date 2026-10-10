package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/pipeline"
)

// novels_cover_redo.go 小说库的「封面重做」——**可配置**的那一版。
//
// 用户 2026-09-26:先是「小说库中的封面菜单需要新增一个封面重做的功能」,试过之后紧接着
// 「封面重做做成一个用户自定义配置弹窗啊,重做需要点改变的啊」。
//
// 这条反馈是对的:**只换 seed 不叫重做**。同一个正向词重抽,构图与内容基本不变,用户看到的
// 是"跟原来几乎一样的一张";能"改变"的旋钮必须交到用户手里:
//
//	正向词(可改可追加) · 出图尺寸/张数 · seed(可复现上次那张) · 是否把新词存回技能端留档
//
// 三段式,不做"点一下直接换掉封面"的黑盒:
//   ① GET  /api/novels/cover-config   读正向词与上一次参数(配置弹窗的初始值)
//   ② POST /api/novels/cover-redo     按参数出 **N 张候选**(只写候选,不动现封面)
//   ③ POST /api/novels/cover-apply    用户挑定之后才落盘(旧封面先备份 + 刷新集项目封面)
//
// 为什么必须分三步:出图是不可逆的创作动作,而"候选"与"定稿"混在一起做,用户就没有比较的余地
// —— 那正是"重做需要点改变"的另一半意思:给我几张不一样的挑。

// coverCandidatesDir 候选图目录(在封面目录下的隐藏子目录里)。
// 刻意不放在 `封面/` 根下:小说库的封面识别取的是该目录里名字序第一张图,
// 候选图混进去会被当成"这本书的封面"(甚至把书卡封面换成一张候选)。
const coverCandidatesDir = ".候选"

// coverSizePreset 配置弹窗里的尺寸预设。
type coverSizePreset struct {
	Label string `json:"label"`
	W     int    `json:"w"`
	H     int    `json:"h"`
}

// coverSizes 常用封面比例:竖版(书卡/项目卡就是 3:4)、方版、横版。
// 默认竖版 —— projectCover 与书卡都是竖着显示的,横版会被裁掉两边。
func coverSizes() []coverSizePreset {
	return []coverSizePreset{
		{"竖版 896×1344(默认,技能端口径)", 896, 1344},
		{"竖版 768×1152", 768, 1152},
		{"方版 1024×1024", 1024, 1024},
		{"横版 1344×896", 1344, 896},
	}
}

// coverPromptPath 技能端留的正向词(权威来源)。
func coverPromptPath(bookDir string) string {
	return filepath.Join(bookDir, "封面", coverRedoPromptFile)
}

// coverSeedPrompt 这本书还没有留档正向词时，给弹窗/一键入口**一份能直接用的默认封面词**
// —— 也就是"**以小说名称直接创作封面**"那句词（2026-10-10 用户原话：不能以小说名称直接创作封面吗？）。
//
// 组成部分全部来自这本书**自己声明过**的东西（唯一出处是 scanNovelStyle，见 novels_style.go），
// 不调模型、不猜剧情：
//   - 书名 —— 而且**放进英文引号里**：官方 ON-SCREEN TEXT 的判据是「**没被英文引号括起来的文字，
//     就是模型会画成字形的来源**」（见 project/prompt_cjk.go 顶上那段），所以"想让它出现在封面上"
//     的字只有一种写法：放进引号。封面上有书名才像封面；
//   - 立项.json 的 art_style / 渲染风格串 / 画风，或 设定集/视觉风格卡-*.md 里的风格串（有则用 ——
//     封面与正片必须同一口径，否则封面跟片子看起来不像一套）；
//   - 一条固定的竖版封面构图口径（下三分之一留标题位）。
//
// **正文必须是英文**（照平台自己的图像侧口径：出图装配只吃英文提示词，汉字注解会被当成
// "要画的字"照抄 —— 见 pipeline/artstyle.go 里那条 StripCJK 注释）。上一版把整句写成中文，
// 与这条口径冲突：那等于让模型把"下三分之一留出标题位置"这类**说明**也当字形画到封面上去。
// 于是判据很干脆：**这份词里引号之外不许出现任何汉字**（引号内只有书名）。
func coverSeedPrompt(bookDir, bookName string) string {
	name := strings.TrimSpace(bookName)
	var b strings.Builder
	fmt.Fprintf(&b, "Vertical 3:4 poster key visual for the Chinese short drama \"《%s》\". ", name)
	if st := strings.TrimSpace(scanNovelStyle(bookDir).Style); st != "" {
		b.WriteString(st + ". ")
	}
	fmt.Fprintf(&b, "Single hero subject, centered slightly above the middle, lower third kept clean for the title; "+
		"dramatic cinematic lighting, shallow depth of field, highly detailed. "+
		"The cover title text \"《%s》\" typeset clearly on the poster. "+
		"No other text, no subtitles, no watermark, no signature.", name)
	return b.String()
}

// handleNovelCoverConfig GET /api/novels/cover-config?path=<书目录>
//
// 配置弹窗的初始值:现状正词、上次用的参数、可选尺寸、现封面尺寸与最近候选。
//
// 【2026-10-10 改】这本书**没有留档正向词**时，原来回 **400** —— 于是弹窗根本打不开，
// 而弹窗本身支持"直接写一份词再生成"（cover-redo 认 in.Prompt），候选对比视图里也专门写着
// 「这本书还没有封面」（CoverCompare.vue）：也就是说 **既没有封面、又没有留档词** 恰恰是
// 封面重做最该顶用的场合，却成了唯一走不通的场合（用户被卡在"先让技能端生成一次"那一步）。
// 现在一律 200：留档词没有就**按这本书自己声明过的东西推一份默认词**（coverSeedPrompt，
// 不猜内容、不调模型），并把状态用 has_prompt / hint 明说，由用户在弹窗里改。
func (s *Server) handleNovelCoverConfig(w http.ResponseWriter, r *http.Request) {
	bookDir := strings.TrimSpace(r.URL.Query().Get("path"))
	if !s.inNovelRoot(bookDir) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
		return
	}
	bookName := filepath.Base(bookDir)
	// 书必须是**目录**：`封面/`（留档词、候选图、meta 都在这儿）只在目录下成立。
	// 单个文件的书做不了封面重做 —— 如实标 applicable=false 并说清原因（前端据此置灰），
	// 而不是让用户点进去、出图时再撞一个 500。
	var applicable bool
	if fi, err := os.Stat(bookDir); err == nil && fi.IsDir() {
		applicable = true
	}
	archived := ""
	if raw, err := os.ReadFile(coverPromptPath(bookDir)); err == nil {
		archived = strings.TrimSpace(string(raw))
	}
	// 弹窗初始值走**模板**（毫秒级、离线可判），**不在这里调 LLM**：config 是打开弹窗那一下，
	// 让它等模型几秒不值当。想要 AI 写的词，弹窗里有「AI 按书名写词」按钮（POST /cover-prompt）。
	prompt, hint := archived, ""
	switch {
	case !applicable:
		hint = "这本书在小说库里是**单个文件**（或目录已不在了）——封面重做只对**书目录**可用；" +
			"单个文件的书请到集项目里换封面"
	case archived == "":
		prompt = coverSeedPrompt(bookDir, bookName)
		hint = "这本书还没有留档正向词（封面/" + coverRedoPromptFile + "）——下面是按书名推的**模板词**，" +
			"可以直接改也可以直接出图；想要更像封面的词，点下面的「AI 按书名写词」让平台 LLM 按这本书写一份"
	}
	llmReady := s.llmReady()
	cw, ch := 896, 1344
	if p := coverInDir(bookDir); p != "" {
		if w, h, ok := s.imageSize(p); ok {
			cw, ch = w, h
		}
	}
	writeJSON(w, 200, map[string]any{
		"prompt":     prompt,
		"has_prompt": archived != "",
		"applicable": applicable,
		"hint":       hint,
		"llm_ready":  llmReady, // 「AI 按书名写词」按钮能不能用（LLM 没配置时前端置灰，别让用户点了才报错）
		"neg":        strings.TrimSpace(readTextOr(filepath.Join(bookDir, "封面", "neg-qwen21.txt"), "")),
		"sizes":      coverSizes(),
		"width":      cw, "height": ch,
		"last_seed":  lastCoverSeed(bookDir),
		"candidates": listCoverCandidates(bookDir),
	})
}

// handleNovelCoverRedo POST /api/novels/cover-redo {path,prompt,append,width,height,seed,count,save_prompt}
//
// 出 N 张候选,只写候选目录,**不动现封面**。返回每张的文件路径 + seed(用户挑定后交给 cover-apply)。
func (s *Server) handleNovelCoverRedo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path       string `json:"path"`
		Prompt     string `json:"prompt"`
		Append     string `json:"append"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		Seed       int64  `json:"seed"`
		Count      int    `json:"count"`
		SavePrompt bool   `json:"save_prompt"`
	}
	if err := readBody(r, &in); err != nil || strings.TrimSpace(in.Path) == "" {
		writeErr(w, 400, fmt.Errorf("需要 path(小说库里的书目录)"))
		return
	}
	bookDir := strings.TrimSpace(in.Path)
	if !s.inNovelRoot(bookDir) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
		return
	}
	fi, err := os.Stat(bookDir)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("书目录不存在:%s", bookDir))
		return
	}
	if !fi.IsDir() {
		// 散文件小说（小说库根下直接放一个 .md/.docx）：没有书目录、也就没有 `封面/` 目录，
		// 候选图无处可放。如实说清**是哪一类书不行** —— 原来只报"书目录不存在"，
		// 而它明明就在那儿（同一个 400 里两件事，用户不知道该改哪一件）。
		writeErr(w, 400, fmt.Errorf("这本书在小说库里是**单个文件**（%s）：封面重做只对**书目录**可用；"+
			"单个文件的书请到集项目里换封面", filepath.Base(bookDir)))
		return
	}
	// 正向词两档：**弹窗里给的** > `coverPromptFor`（留档 `封面/pos-qwen21.txt` > **平台 LLM 按书名写**
	// > 模板兜底）。三档优先级与降级说明都在 novel_cover_prompt.go 里（唯一出处）。
	//
	// 【2026-10-10 用户两问：不能以小说名称直接创作封面吗？／平台有LLM功能啊】
	// 这里原来是"没有留档词就 400，让技能端先出一版" —— 也就是**只给书名出不了封面**；
	// 而补上的那一版又只是我拼的模板句（用户当场指出平台有 LLM）。现在：书名就够，
	// **词由平台 LLM 按书名 + 本书风格 + 正文开头写**，模板只在她不可用时兜底、且如实标注。
	prompt := strings.TrimSpace(in.Prompt)
	src := "**用户给的正向词**"
	if prompt == "" {
		cp := s.coverPromptFor(bookDir)
		prompt, src = cp.Prompt, cp.From
		if cp.Note != "" {
			src += "（" + cp.Note + "）"
		}
	}
	if extra := strings.TrimSpace(in.Append); extra != "" {
		prompt = strings.TrimRight(prompt, " ,，。.") + ". " + extra
	}

	w0, h0 := normCoverSize(in.Width, in.Height)
	count := in.Count
	if count <= 0 {
		count = 1
	}
	if count > 4 {
		count = 4
	}
	eng, err := s.reg.Get("comfy")
	if err != nil {
		writeErr(w, 503, fmt.Errorf("本地 ComfyUI 引擎不可用:%w", err))
		return
	}
	candDir := filepath.Join(bookDir, "封面", coverCandidatesDir)
	if err := os.MkdirAll(candDir, 0o755); err != nil {
		writeErr(w, 500, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(count)*15*time.Minute)
	defer cancel()

	// 正向词是哪来的必须**如实播报**（用户要能一眼看出这行词是 AI 写的、技能端留档的，还是兜底模板）
	s.opLog("🎨 封面候选:《%s》%d 张 %d×%d(正向词来自:%s%s)",
		filepath.Base(bookDir), count, w0, h0, src,
		func() string {
			if strings.TrimSpace(in.Append) != "" {
				return ",附加要求:" + strings.TrimSpace(in.Append)
			}
			return ""
		}())

	type cand struct {
		File string `json:"file"`
		Seed int64  `json:"seed"`
	}
	out := make([]cand, 0, count)
	seeds := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		seed := in.Seed
		if seed == 0 {
			seed = pipeline.RandomSeed()
		} else if i > 0 {
			seed += int64(i) * 7919 // 一次多张:同一起点往外散,便于"同一想法换姿势"
		}
		dst := filepath.Join(candDir, fmt.Sprintf("c%d-%d.png", seed, time.Now().UnixNano()%1000))
		if _, err := eng.GenerateImage(ctx, prompt, "", seed, w0, h0, dst); err != nil {
			if len(out) == 0 {
				writeErr(w, 500, fmt.Errorf("封面生成失败:%w", err))
				return
			}
			break // 出到一半:把已经出来的交出去,别让用户白等
		}
		out = append(out, cand{File: dst, Seed: seed})
		seeds = append(seeds, seed)
	}
	if len(out) == 0 {
		writeErr(w, 500, fmt.Errorf("封面生成失败:没有产出任何候选"))
		return
	}
	// 记下参数(不写正向词):下次打开弹窗能"用上次的 seed / 尺寸"复现
	writeCoverMeta(bookDir, coverMeta{Prompt: prompt, W: w0, H: h0, Seed: seeds[len(seeds)-1],
		PromptSaved: in.SavePrompt, At: time.Now()})
	if in.SavePrompt {
		// 用户勾了「把新词存回封面留档」:写回 pos-qwen21.txt(旧词备份成 .bak)
		p := coverPromptPath(bookDir)
		if old, err := os.ReadFile(p); err == nil {
			_ = os.WriteFile(p+".bak", old, 0o644)
		}
		if err := os.WriteFile(p, []byte(prompt), 0o644); err != nil {
			s.opLog("⚠ 封面正向词写回失败:%v", err)
		} else {
			s.opLog("💾 封面正向词已更新(旧词备份 %s.bak)", filepath.Base(p))
		}
	}
	// from 现在是一个可读的来源串（"留档…"/"平台 LLM 按书名写"/"模板兜底…"/"**用户给的正向词**"），
	// 前端据此显示"这行词是谁写的"；`from_file` 保留旧字段名会误导（它早就不止"文件"一种来源）。
	writeJSON(w, 200, map[string]any{"ok": true, "candidates": out,
		"width": w0, "height": h0, "count": len(out), "prompt": prompt, "from": src})
}

// handleNovelCoverApply POST /api/novels/cover-apply {path,file}
//
// 用户挑定某张候选 → 落盘为本书封面(旧封面先备份),并刷新这本书的集项目封面。
func (s *Server) handleNovelCoverApply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
		File string `json:"file"`
	}
	if err := readBody(r, &in); err != nil || strings.TrimSpace(in.Path) == "" || strings.TrimSpace(in.File) == "" {
		writeErr(w, 400, fmt.Errorf("需要 path(书目录)与 file(候选图路径)"))
		return
	}
	bookDir, cand := strings.TrimSpace(in.Path), strings.TrimSpace(in.File)
	if !s.inNovelRoot(bookDir) || !s.inNovelRoot(cand) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
		return
	}
	if _, err := os.Stat(cand); err != nil {
		writeErr(w, 400, fmt.Errorf("候选图不存在(可能已被清理):%s", cand))
		return
	}
	// 只接受本书封面目录下的候选:否则就成了"把任意图片设成封面"的洞
	if !strings.HasPrefix(filepath.Clean(cand), filepath.Clean(filepath.Join(bookDir, "封面"))+string(os.PathSeparator)) {
		writeErr(w, 400, fmt.Errorf("候选图必须来自这本书的封面目录"))
		return
	}
	backup, refreshed, err := s.applyCoverCandidate(bookDir, cand)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	dst := filepath.Join(bookDir, "封面", "封面"+strings.ToLower(filepath.Ext(cand)))
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
		Message: fmt.Sprintf("🎨 《%s》封面已更新%s", filepath.Base(bookDir),
			map[bool]string{true: fmt.Sprintf(",并刷新了 %d 个集项目封面", refreshed), false: ""}[refreshed > 0])})
	writeJSON(w, 200, map[string]any{"ok": true, "cover": filepath.ToSlash(filepath.Join("封面", filepath.Base(dst))),
		"backup": backup, "projects": refreshed})
}

// applyCoverCandidate 把一张候选图落盘为本书封面（旧封面先备份）+ 刷新这本书的集项目封面。
// 返回（备份路径, 刷新了几个集, 错误）。
//
// 【为什么单独成函数·2026-10-10】"一键按书名出封面"（`seedCoverFromTitle`）与"用户挑定候选"
// （`handleNovelCoverApply`）必须走**同一段落盘逻辑** —— 备份、按后缀落盘、刷新集项目封面这三件事
// 一旦两处各写一遍，迟早出现"自动那张不备份"/"挑的那张不刷新集项目"这种只在一半路径上发生的缺陷。
func (s *Server) applyCoverCandidate(bookDir, cand string) (string, int, error) {
	coverDir := filepath.Join(bookDir, "封面")
	b, err := os.ReadFile(cand)
	if err != nil {
		return "", 0, err
	}
	// 旧封面先备份(重做是创作行为,随时可能反悔)
	backup := ""
	if old := coverInDir(bookDir); old != "" {
		ext := strings.ToLower(filepath.Ext(old))
		backup = filepath.Join(coverDir, "封面.旧-"+time.Now().Format("20060102-150405")+ext)
		if ob, e := os.ReadFile(old); e == nil {
			if e2 := os.WriteFile(backup, ob, 0o644); e2 != nil {
				backup = ""
			}
		}
	}
	dst := filepath.Join(coverDir, "封面"+strings.ToLower(filepath.Ext(cand)))
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		return backup, 0, err
	}
	return backup, s.refreshProjectsCover(filepath.Base(bookDir), dst, backup), nil
}

// handleNovelCoverPrompt POST /api/novels/cover-prompt {path}
//
// 「让**平台 LLM** 按这本书写一份封面正向词」—— 弹窗里那个「AI 按书名写词」按钮走这条。
// 与「一键出封面」的区别只有一处：这里**强制走 LLM**（哪怕技能端留过档），因为用户的意图
// 就是"要一份新的、AI 写的"；LLM 给不出可用结果时回**模板 + 如实说明**（不报 500 ——
// 用户要的是"一行能用的词"，拿不到 LLM 也该给他能改的起点）。
func (s *Server) handleNovelCoverPrompt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if err := readBody(r, &in); err != nil || strings.TrimSpace(in.Path) == "" {
		writeErr(w, 400, fmt.Errorf("需要 path(小说库里的书目录)"))
		return
	}
	bookDir := strings.TrimSpace(in.Path)
	if !s.inNovelRoot(bookDir) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
		return
	}
	if fi, err := os.Stat(bookDir); err != nil || !fi.IsDir() {
		writeErr(w, 400, fmt.Errorf("封面词只对**书目录**可用（这本是单个文件/目录不在）"))
		return
	}
	name := filepath.Base(bookDir)
	prompt, note, err := s.llmCoverPrompt(bookDir)
	from := "llm"
	if err != nil {
		prompt = coverSeedPrompt(bookDir, name)
		from = "template"
		note = "LLM 没用上：" + err.Error()
	}
	s.opLog("🎨 封面正向词:《%s》来自 %s（%d 字%s）", name,
		map[string]string{"llm": "**平台 LLM**", "template": "模板兜底"}[from],
		len([]rune(prompt)), map[bool]string{true: "；" + note, false: ""}[note != ""])
	writeJSON(w, 200, map[string]any{"prompt": prompt, "from": from, "note": note})
}

// seedCoverFromTitle 「**以小说名称直接创作一张封面**」：拿词（coverPromptFor：留档 > LLM > 模板）
// → 出 1 张 → 走与「挑定候选」**同一段落盘逻辑**（旧封面先备份 + 刷新这本书的集项目封面）。
// 返回落盘后的封面路径。
//
// 【2026-10-10 用户两问：不能以小说名称直接创作封面吗？／平台有LLM功能啊】
// 一键入口（小说库菜单的「按书名出封面」、视频制作里"书没有封面时先出一张"）都走这里：
// 书名就是足够的输入，词由**平台 LLM** 写（模板只在她不可用时兜底，且如实标注来源）。
//
// 拿不到引擎/出图失败/落盘失败 → 返回 error，由调用方**如实播报**（不许静默吞掉）。
func (s *Server) seedCoverFromTitle(bookDir string) (string, error) {
	eng, err := s.reg.Get("comfy")
	if err != nil {
		return "", fmt.Errorf("本地 ComfyUI 引擎不可用:%w", err)
	}
	cp := s.coverPromptFor(bookDir)
	w0, h0 := normCoverSize(0, 0) // 技能端口径 896×1344（竖版）
	candDir := filepath.Join(bookDir, "封面", coverCandidatesDir)
	if err := os.MkdirAll(candDir, 0o755); err != nil {
		return "", err
	}
	seed := pipeline.RandomSeed()
	cand := filepath.Join(candDir, fmt.Sprintf("c%d-%d.png", seed, time.Now().UnixNano()%1000))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if _, err := eng.GenerateImage(ctx, cp.Prompt, "", seed, w0, h0, cand); err != nil {
		return "", fmt.Errorf("出图失败:%w", err)
	}
	if _, _, err := s.applyCoverCandidate(bookDir, cand); err != nil {
		return "", fmt.Errorf("落盘失败:%w", err)
	}
	note := ""
	if cp.Note != "" {
		note = "；" + cp.Note
	}
	s.opLog("🎨 已按书名生成封面:《%s》%d×%d（正向词来自:%s%s）",
		filepath.Base(bookDir), w0, h0, cp.From, note)
	return filepath.Join(bookDir, "封面", "封面.png"), nil
}

// normCoverSize 规整尺寸:夹到 [512,2048] 并对齐到 16 的倍数(图像侧对齐要求),
// 缺省/非法回落到技能端口径 896×1344。
func normCoverSize(w, h int) (int, int) {
	clamp := func(v int) int {
		if v < 512 {
			v = 512
		}
		if v > 2048 {
			v = 2048
		}
		return (v / 16) * 16
	}
	if w <= 0 || h <= 0 {
		return 896, 1344
	}
	return clamp(w), clamp(h)
}

// coverMeta 上一次出图的参数(给弹窗做"复现上次"用)。
type coverMeta struct {
	Prompt      string    `json:"prompt"`
	W           int       `json:"w"`
	H           int       `json:"h"`
	Seed        int64     `json:"seed"`
	PromptSaved bool      `json:"prompt_saved"`
	At          time.Time `json:"at"`
}

func coverMetaPath(bookDir string) string {
	return filepath.Join(bookDir, "封面", ".cover-meta.json")
}

func writeCoverMeta(bookDir string, m coverMeta) {
	b, err := jsonMarshalIndent(m)
	if err != nil {
		return
	}
	_ = os.WriteFile(coverMetaPath(bookDir), b, 0o644)
}

func lastCoverSeed(bookDir string) int64 {
	b, err := os.ReadFile(coverMetaPath(bookDir))
	if err != nil {
		return 0
	}
	var m coverMeta
	if jsonUnmarshal(b, &m) != nil {
		return 0
	}
	return m.Seed
}

// listCoverCandidates 已有候选(弹窗重开时能接着挑)。
//
// seed 从文件名解析回来:候选是"重开弹窗接着挑"的对象,而挑的时候第一眼要看的就是
// seed(哪张是哪次抽的、能不能复现);缺了它前端只能显示空,或者再猜一遍文件名 —— 那是
// 同一套命名规则的两份实现,迟早对不上。解析只此一处。
func listCoverCandidates(bookDir string) []map[string]any {
	dir := filepath.Join(bookDir, "封面", coverCandidatesDir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && coverImageExts[strings.ToLower(filepath.Ext(e.Name()))] {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{"file": filepath.Join(dir, n), "name": n, "seed": seedFromCandName(n)})
	}
	return out
}

// seedFromCandName 从候选文件名取 seed(命名规则见出图那段:`c<seed>-<毫秒>.png`)。
// 名字不合规则就返回 0 —— 前端据此显示「seed —」,不假装知道。
func seedFromCandName(name string) int64 {
	s := strings.TrimPrefix(name, "c")
	i := strings.IndexByte(s, '-')
	if i <= 0 {
		return 0
	}
	v, err := strconv.ParseInt(s[:i], 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func readTextOr(path, def string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return def
	}
	return string(b)
}

// coverRedoPromptFile 技能端留的正向词文件名(权威来源)。
const coverRedoPromptFile = "pos-qwen21.txt"

// refreshProjectsCover 把新封面写进这本书名下的所有集项目。
//
// oldBackup = 旧封面的备份路径(换封面之前那一版)。集项目里的 `cover.*` 是导入时从**旧封面**
// 拷的副本,所以判据是**逐字节比对**:与旧封面相同的 = 我们拷的副本,可以覆盖;
// 不相同的 = 用户自己换过的封面,一律不碰("我明明换过封面,它又变回去了"是最难查的一类)。
func (s *Server) refreshProjectsCover(book, src, oldBackup string) int {
	ext := strings.ToLower(filepath.Ext(src))
	b, err := os.ReadFile(src)
	if err != nil {
		return 0
	}
	var old []byte
	if oldBackup != "" {
		old, _ = os.ReadFile(oldBackup)
	}
	n := 0
	for _, p := range s.store.List() {
		if !strings.HasPrefix(p.ID, book+"·第") {
			continue
		}
		dir := filepath.Join(s.store.Dir(p.ID), "assets")
		cur := coverFileIn(dir)
		if cur != "" {
			if len(old) == 0 {
				continue // 有封面但不知道旧封面长什么样:宁可不覆盖
			}
			cb, err := os.ReadFile(cur)
			if err != nil || len(cb) != len(old) || string(cb) != string(old) {
				continue // 用户自己换过的封面
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, "cover"+ext), b, 0o644); err != nil {
			continue
		}
		if cur != "" && !strings.EqualFold(cur, filepath.Join(dir, "cover"+ext)) {
			_ = os.Remove(cur) // 换了后缀(旧 jpg → 新 png)时清掉旧文件
		}
		n++
	}
	return n
}

// coverFileIn 项目 assets 目录里的封面文件(没有则空)。
func coverFileIn(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n := strings.ToLower(e.Name())
		if strings.HasPrefix(n, "cover.") && coverImageExts[filepath.Ext(n)] {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// imageSize 读图片像素尺寸(用 ffprobe,平台已有这条依赖;读不到返回 ok=false)。
func (s *Server) imageSize(path string) (int, int, bool) {
	if s.ff == nil {
		return 0, 0, false
	}
	info, err := s.ff.Probe(path)
	if err != nil || info == nil || info.Width <= 0 || info.Height <= 0 {
		return 0, 0, false
	}
	return info.Width, info.Height, true
}

// jsonMarshalIndent / jsonUnmarshal 小工具(本文件只用于 meta 的读写)。
func jsonMarshalIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
