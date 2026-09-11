// novels.go 小说库:扫描设置目录中的小说(书=子文件夹,优先「全本」,次选合并「正文」
// 分卷章节;封面取「封面」文件夹内图片),支持弹窗阅读详情与一键视频创作。
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
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
)

type novelItem struct {
	Name     string `json:"name"`     // 书名(=文件夹名/文件主干)
	Path     string `json:"path"`     // 书目录(或文件)绝对路径
	Type     string `json:"type"`     // dir / file
	Chars    int    `json:"chars"`    // 正文字数估算
	Chapters int    `json:"chapters"` // 章节文件数
	Cover    string `json:"cover"`    // 封面绝对路径(空=无)
	Produced bool   `json:"produced"` // 是否已创建过系列项目
}

// novelRoot 小说库根目录(设置项;空=未配置)。
func (s *Server) novelRoot() string { return strings.TrimSpace(s.cfg.Get().NovelDir) }

// inNovelRoot 校验路径在小说库内(防穿越)。
func (s *Server) inNovelRoot(p string) bool {
	root := s.novelRoot()
	if root == "" {
		return false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	rAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	return strings.HasPrefix(abs, rAbs+string(os.PathSeparator)) || abs == rAbs
}

// handleNovels GET /api/novels 扫描小说库。
func (s *Server) handleNovels(w http.ResponseWriter, r *http.Request) {
	root := s.novelRoot()
	if root == "" {
		writeJSON(w, 200, map[string]any{"root": "", "novels": []novelItem{}, "hint": "未配置小说目录,请在设置中填写"})
		return
	}
	if _, err := os.Stat(root); err != nil {
		writeJSON(w, 200, map[string]any{"root": root, "novels": []novelItem{}, "hint": "目录不存在:" + root})
		return
	}
	var out []novelItem
	ents, _ := os.ReadDir(root)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(root, e.Name())
		if e.IsDir() {
			out = append(out, s.scanNovelDir(full, e.Name()))
		} else if strings.HasSuffix(strings.ToLower(e.Name()), ".md") || strings.HasSuffix(strings.ToLower(e.Name()), ".txt") {
			st, _ := e.Info()
			out = append(out, novelItem{
				Name: strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())),
				Path: full, Type: "file",
				Chars: int(st.Size() / 3), Chapters: 1,
				Produced: s.novelProduced(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, 200, map[string]any{"root": root, "novels": out})
}

// scanNovelDir 一本书(子文件夹):全本优先,正文分卷为章节;封面取「封面」内首图。
func (s *Server) scanNovelDir(dir, name string) novelItem {
	it := novelItem{Name: name, Path: dir, Type: "dir"}
	// 全本
	var fullbook string
	if fbs, err := filepath.Glob(filepath.Join(dir, "全本", "*")); err == nil {
		for _, f := range fbs {
			if hasTxtExt(f) {
				fullbook = f
				break
			}
		}
	}
	// 章节文件(正文/** 的 md/txt;无正文目录时全目录扫描,忽略素材/设定)
	chapDirs := []string{filepath.Join(dir, "正文")}
	walk := func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if hasTxtExt(p) {
			it.Chapters++
			if st, e := d.Info(); e == nil {
				it.Chars += int(st.Size() / 3)
			}
		}
		return nil
	}
	if fi, err := os.Stat(chapDirs[0]); err == nil && fi.IsDir() {
		_ = filepath.WalkDir(chapDirs[0], walk)
	} else {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			rel := strings.ToLower(p)
			if d.IsDir() && (strings.Contains(rel, "素材") || strings.Contains(rel, "设定") || strings.Contains(rel, "docs")) {
				return filepath.SkipDir
			}
			return walk(p, d, err)
		})
	}
	if fullbook != "" {
		if st, err := os.Stat(fullbook); err == nil {
			it.Chars = int(st.Size() / 3) // 全本字数覆盖章节累加
		}
	}
	// 封面:封面/ 内第一张图
	if cs, err := os.ReadDir(filepath.Join(dir, "封面")); err == nil {
		for _, c := range cs {
			if c.IsDir() {
				continue
			}
			n := strings.ToLower(c.Name())
			for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp"} {
				if strings.HasSuffix(n, ext) {
					it.Cover = filepath.Join(dir, "封面", c.Name())
					break
				}
			}
			if it.Cover != "" {
				break
			}
		}
	}
	it.Produced = s.novelProduced(name)
	return it
}

// safeSeries 书名 → 项目名安全形态:项目 ID 白名单外的字符(全角逗号/空格/
// 感叹号等)统一替换为间隔号「·」,再压缩连续与首尾。书名「天庭把我裁员，
// 我在人间爆红」→「天庭把我裁员·我在人间爆红」。查重与拆集共用,保证一致。
func safeSeries(name string) string {
	var b []rune
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b = append(b, r)
		case r >= 0x4e00 && r <= 0x9fff:
			b = append(b, r)
		case r == '_', r == '-', r == '·':
			b = append(b, r)
		default:
			b = append(b, '·')
		}
	}
	out := strings.Trim(string(b), "·")
	for strings.Contains(out, "··") {
		out = strings.ReplaceAll(out, "··", "·")
	}
	if out == "" {
		out = "未命名系列"
	}
	return out
}

// seriesExistingEp 系列下按集号找已有分集项目名(前缀匹配,无则空串)。
// 补缺重拆时 LLM 重新措辞的标题不再催生重复集。
func (s *Server) seriesExistingEp(series string, ep int) string {
	prefix := fmt.Sprintf("%s·第%02d集·", series, ep)
	for _, p := range s.store.List() {
		if strings.HasPrefix(p.ID, prefix) {
			return p.ID
		}
	}
	return ""
}

// novelProduced 是否已为该书创建过系列/同名项目。
func (s *Server) novelProduced(name string) bool {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(safeSeries(name)) + `(·第\d+集|$)`)
	for _, p := range s.store.List() {
		if re.MatchString(p.ID) {
			return true
		}
	}
	return false
}

// handleNovelCover GET /api/novels/cover?path= 输出封面图。
func (s *Server) handleNovelCover(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !s.inNovelRoot(p) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
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

// novelChapter 阅读详情条目。
type novelChapter struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	KB    int64  `json:"kb"`
	Chars int    `json:"chars"`
}

// handleNovelDetail GET /api/novels/detail?path= 弹窗数据:章节文件列表(+全本)。
func (s *Server) handleNovelDetail(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !s.inNovelRoot(p) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
		return
	}
	fi, err := os.Stat(p)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	d := novelItem{Name: filepath.Base(p), Path: p}
	if fi.IsDir() {
		d = s.scanNovelDir(p, filepath.Base(p))
	} else {
		d.Type, d.Chapters, d.Chars = "file", 1, int(fi.Size()/3)
		d.Name = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	}
	// 章节清单
	var chapters []novelChapter
	if fi.IsDir() {
		if fbs, _ := filepath.Glob(filepath.Join(p, "全本", "*")); len(fbs) > 0 && hasTxtExt(fbs[0]) {
			st, _ := os.Stat(fbs[0])
			chapters = append(chapters, novelChapter{Path: fbs[0], Name: "📖 全本 · " + filepath.Base(fbs[0]), KB: st.Size() / 1024, Chars: int(st.Size() / 3)})
		}
		roots := []string{filepath.Join(p, "正文")}
		if _, err := os.Stat(roots[0]); err != nil {
			roots[0] = p
		}
		_ = filepath.WalkDir(roots[0], func(fp string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() || !hasTxtExt(fp) {
				return nil
			}
			st, _ := e.Info()
			rel, _ := filepath.Rel(p, fp)
			rel = filepath.ToSlash(rel) // 前端按 / 分层建目录树(Windows 反斜杠会整条平铺)
			chapters = append(chapters, novelChapter{Path: fp, Name: rel, KB: st.Size() / 1024, Chars: int(st.Size() / 3)})
			return nil
		})
		sort.Slice(chapters, func(i, j int) bool { return chapters[i].Path < chapters[j].Path })
	} else {
		chapters = append(chapters, novelChapter{Path: p, Name: filepath.Base(p), KB: fi.Size() / 1024, Chars: int(fi.Size() / 3)})
	}
	writeJSON(w, 200, map[string]any{"novel": d, "chapters": chapters})
}

// handleNovelRead GET /api/novels/read?path= 读章节正文(截断 400KB 防巨文件)。
func (s *Server) handleNovelRead(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !s.inNovelRoot(p) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	const limit = 400 << 10
	text := string(raw)
	truncated := false
	if len(raw) > limit {
		// 按 rune 截断避免切坏中文
		runes := []rune(text)
		maxRunes := limit / 3
		if len(runes) > maxRunes {
			text = string(runes[:maxRunes])
			truncated = true
		}
	}
	writeJSON(w, 200, map[string]any{"text": text, "truncated": truncated, "chars": len([]rune(string(raw)))})
}

// handleNovelProduce POST /api/novels/produce {path, max_episodes?}
// 一键视频创作:读全本 → 查重 → 拆集建项目 → 封面复制 → 自动启动批量产线。
func (s *Server) handleNovelProduce(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
		Max  int    `json:"max_episodes"`
	}
	if err := readBody(r, &in); err != nil || strings.TrimSpace(in.Path) == "" {
		writeErr(w, 400, fmt.Errorf("请提供小说路径"))
		return
	}
	if !s.inNovelRoot(in.Path) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
		return
	}
	fi, err := os.Stat(in.Path)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	// 书名与正文
	var name, text string
	var coverSrc string
	if fi.IsDir() {
		it := s.scanNovelDir(in.Path, filepath.Base(in.Path))
		name, coverSrc = it.Name, it.Cover
		// 合并正文章节(分册文件);内容无章头的册从文件名注入章头,
		// 保证章界(切块/CHAPTERS/【本集原文】附着全依赖章头)
		readMerged := func() (string, int) {
			var paths []string
			_ = filepath.WalkDir(in.Path, func(p string, e os.DirEntry, err error) error {
				if err != nil || e.IsDir() || !hasTxtExt(p) {
					return nil
				}
				rel := strings.ToLower(p)
				if strings.Contains(rel, "素材") || strings.Contains(rel, "设定") || strings.Contains(rel, "docs") || strings.Contains(rel, "封面") || strings.Contains(rel, "全本") {
					return nil
				}
				paths = append(paths, p)
				return nil
			})
			sort.Strings(paths)
			var sb strings.Builder
			for _, p := range paths {
				b, _ := os.ReadFile(p)
				sb.WriteString(ensureChapterHead(filepath.Base(p), string(b)))
				sb.WriteString("\n\n")
			}
			return sb.String(), len(paths)
		}
		if fbs, _ := filepath.Glob(filepath.Join(in.Path, "全本", "*")); len(fbs) > 0 && hasTxtExt(fbs[0]) {
			b, err := os.ReadFile(fbs[0])
			if err != nil {
				writeErr(w, 500, err)
				return
			}
			full := NormalizeNovelText(b)
			merged, nFiles := readMerged()
			// 全本无有效章结构而正文/有足够分章文件时,弃全本用分章合并
			// (2026-09-11 实证:全本 md 正文无章头仅目录,章界识别失败→
			// 硬切大段→CHAPTERS 失准→原文附着断链→拆集粒度失控)
			if countChapterHeads(full) >= 8 || nFiles < 8 {
				text = full
			} else {
				text = NormalizeNovelText([]byte(merged))
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
					Message: fmt.Sprintf("📖 全本文件无章节结构,改用正文分章合并(%d 册,已按文件名注入章头)", nFiles)})
			}
		} else {
			text, _ = readMerged()
		}
	} else {
		name = strings.TrimSuffix(filepath.Base(in.Path), filepath.Ext(in.Path))
		b, err := os.ReadFile(in.Path)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		text = string(b)
	}
	// 文本预处理:不管源文件什么编码/格式,统一转干净 UTF-8 纯文本
	// (编码检测/BOM/不可见字符/智能引号/Markdown 装饰/空行压缩)
	text = NormalizeNovelText([]byte(text))
	if len([]rune(text)) < 200 {
		writeErr(w, 400, fmt.Errorf("小说正文过短(%d 字,预处理后)", len([]rune(text))))
		return
	}
	// 读取立项.json 的 art_style 作为视频渲染风格(2026-09-11 用户定调)
	artStyle := ""
	if fi.IsDir() {
		if raw, e := os.ReadFile(filepath.Join(in.Path, "立项.json")); e == nil {
			var proj json.RawMessage
			var m map[string]any
			if json.Unmarshal(raw, &proj) == nil && json.Unmarshal(raw, &m) == nil {
				if v, ok := m["art_style"].(string); ok && strings.TrimSpace(v) != "" {
					artStyle = strings.TrimSpace(v)
				}
			}
		}
	}
	// 查重:同书系列/同名项目已存在
	if s.novelProduced(name) {
		// 已有系列:放行进入补缺模式——重拆按集号去重,已有分集不动,只建缺失
		// (2026-09-10:分集 47 因标题符号创建失败后无补建通道,用户被查重挡死)
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
			Message: "《" + name + "》已有系列,进入补缺模式:已有分集保持不动,仅补建缺失的集"})
	}
	// 后台异步:拆集→建项目→封面→自动批量。立即返回,弹窗不等待 LLM(2-6 分钟);
	// 进度看全局日志(30 秒心跳),分集项目创建后首页实时出现。
	cover := coverSrc
	maxEp := in.Max
	go func() {
		// 注册键必须与拆集内部键一致:splitNovelToProjects 会做 safeSeries 规整
		// (全角逗号等→「·」),原始书名注册的占位卡会因键不一致永远停在 running
		// (2026-09-09 实证:「天庭把我裁员，我在人间爆红」占位卡不退场)。
		name = safeSeries(name)
		jobSet(name, "running", 0, "AI 拆集通读中…(提交即注册,首页占位卡立即可见)")
		// 可取消上下文:停止按钮可中断 LLM 拆集调用(2026-09-11 用户定调)
		splitCtx, splitCancel := context.WithCancel(context.Background())
		defer splitCancel()
		// 流水线化(2026-09-10 用户定调):首段拆集就绪即启动批量产线,
		// LLM 静默持续分段解析补集,产线消费到哪追到哪,日志双流同步。
		var started bool
		var seriesFinal string
		var prodP *novelProducer
		applyCover := func(newEps []string) {
			if cover == "" {
				return
			}
			ext := strings.ToLower(filepath.Ext(cover))
			for _, ep := range newEps {
				dst := filepath.Join(s.store.Dir(ep), "assets", "cover"+ext)
				if b, e := os.ReadFile(cover); e == nil {
					_ = os.MkdirAll(filepath.Dir(dst), 0o755)
					_ = os.WriteFile(dst, b, 0o644)
				}
			}
		}
		_, _, err := s.splitNovelToProjects(splitCtx, name, text, maxEp, artStyle, func(series string, newEps []string) {
			seriesFinal = series
			if prodP == nil {
				prodP = prodStart(series, splitCancel) // 注册生产者(含 cancel:停止按钮可中断拆集)
			}
			applyCover(newEps)
			if !started {
				started = true
				if s.startBatch(series, newEps) {
					s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
						Message: "🚀 首段拆集就绪,批量产线提前启动(拆集继续后台补集,产线实时消费)"})
				} else {
					s.batchAppend(series, newEps) // 已有产线在跑:直接入队
				}
			} else {
				s.batchAppend(series, newEps)
			}
		})
		if prodP != nil {
			prodDone(seriesFinal, prodP) // 拆集结束:产线跑完已知集自然收尾
		}
		if err != nil {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "error",
				Message: "《" + name + "》拆集失败:" + err.Error()})
			jobSet(name, "failed", 0, "拆集失败:"+err.Error())
			return
		}
	}()
	writeJSON(w, 200, map[string]any{"ok": true, "submitted": true,
		"message": "《" + name + "》已提交后台创作:拆集(2-6 分钟)→分集项目自动出现→自动开始批量产线,全程日志可观测"})
}

// ---------- 流式拆集生产者注册表(产线据此判断还有没有新分集要来) ----------
var novelProducers = struct {
	sync.Mutex
	m map[string]*novelProducer // series → 生产者(done+cancel)
}{m: map[string]*novelProducer{}}

// novelProducer 流式拆集生产者:done=结束信号,cancel=取消拆集 LLM 调用。
type novelProducer struct {
	done   chan struct{}
	cancel context.CancelFunc
}

func prodStart(series string, cancel context.CancelFunc) *novelProducer {
	p := &novelProducer{done: make(chan struct{}), cancel: cancel}
	novelProducers.Lock()
	novelProducers.m[series] = p
	novelProducers.Unlock()
	return p
}
func prodDone(series string, p *novelProducer) {
	novelProducers.Lock()
	delete(novelProducers.m, series)
	novelProducers.Unlock()
	select {
	case <-p.done:
	default:
		close(p.done)
	}
}
func prodAlive(series string) bool {
	novelProducers.Lock()
	defer novelProducers.Unlock()
	_, ok := novelProducers.m[series]
	return ok
}

// prodCancelAll 取消所有拆集生产者(停止按钮调用,终止 LLM 拆集)。
func prodCancelAll() {
	novelProducers.Lock()
	defer novelProducers.Unlock()
	for _, p := range novelProducers.m {
		if p.cancel != nil {
			p.cancel()
		}
	}
}

// batchAppend 向运行中的批量产线追加新分集(流式拆集逐段推送)。
func (s *Server) batchAppend(prefix string, newEps []string) {
	batch.mu.Lock()
	defer batch.mu.Unlock()
	if !batch.running || batch.prefix != prefix {
		return
	}
	batch.projects = append(batch.projects, newEps...)
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
		Message: fmt.Sprintf("📥 拆集新段就绪 +%d 集,已加入批量产线队列(当前共 %d 集)", len(newEps), len(batch.projects))})
}

// ---------- 进行中拆集任务注册表(首页占位卡数据源) ----------

type novelJob struct {
	Series  string `json:"series"`
	Created int    `json:"created"` // 已建分集数
	State   string `json:"state"`   // running / done / failed
	Message string `json:"message"`
	At      int64  `json:"at"`
}

var novelJobs = struct {
	sync.Mutex
	m map[string]*novelJob
}{m: map[string]*novelJob{}}

func jobSet(series, state string, created int, msg string) {
	novelJobs.Lock()
	defer novelJobs.Unlock()
	if state == "" { // 终态清理
		delete(novelJobs.m, series)
		return
	}
	novelJobs.m[series] = &novelJob{Series: series, Created: created, State: state, Message: msg, At: time.Now().Unix()}
}

// handleNovelDelete DELETE /api/novels?path= 删除小说库条目(仅允许一级:书目录或单文件)。
func (s *Server) handleNovelDelete(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		writeErr(w, 400, fmt.Errorf("请提供小说路径"))
		return
	}
	if !s.inNovelRoot(p) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
		return
	}
	root := filepath.Clean(s.novelRoot())
	if filepath.Clean(p) == root {
		writeErr(w, 403, fmt.Errorf("不能删除小说库根目录"))
		return
	}
	if filepath.Clean(filepath.Dir(p)) != root {
		writeErr(w, 400, fmt.Errorf("仅支持删除小说库中的一级条目(整本书或单文件)"))
		return
	}
	fi, err := os.Stat(p)
	if err != nil {
		writeErr(w, 404, fmt.Errorf("小说不存在"))
		return
	}
	if fi.IsDir() {
		err = os.RemoveAll(p)
	} else {
		err = os.Remove(p)
	}
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	s.bus.Publish(pipeline.Event{Phase: "system", Level: "info",
		Message: "🗑 已从小说库删除《" + filepath.Base(p) + "》(磁盘文件)"})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleNovelJobsDelete DELETE /api/novel-jobs?series= 移除拆集任务占位(首页失败占位卡清理)。
func (s *Server) handleNovelJobsDelete(w http.ResponseWriter, r *http.Request) {
	series := r.URL.Query().Get("series")
	if series == "" {
		writeErr(w, 400, fmt.Errorf("请提供 series"))
		return
	}
	novelJobs.Lock()
	_, ok := novelJobs.m[series]
	delete(novelJobs.m, series)
	novelJobs.Unlock()
	if !ok {
		writeErr(w, 404, fmt.Errorf("任务不存在或已结束"))
		return
	}
	s.bus.Publish(pipeline.Event{Phase: "system", Level: "info",
		Message: "🗑 已移除拆集失败占位卡《" + series + "》"})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleNovelJobs GET /api/novel-jobs:进行中/刚完成的拆集任务(失败保留 10 分钟)。
func (s *Server) handleNovelJobs(w http.ResponseWriter, r *http.Request) {
	novelJobs.Lock()
	defer novelJobs.Unlock()
	now := time.Now().Unix()
	out := []*novelJob{}
	for k, j := range novelJobs.m {
		if j.State == "failed" && now-j.At > 600 {
			delete(novelJobs.m, k)
			continue
		}
		// running 兜底:拆集 LLM 通常 2-6 分钟;20 分钟无任何推进(键错位/
		// 进程重启遗留等)按孤儿清掉,占位卡不再永久滞留
		if j.State == "running" && now-j.At > 1200 {
			delete(novelJobs.m, k)
			continue
		}
		if j.State == "done" && now-j.At > 120 { // 完成态短留(真卡已就位)
			delete(novelJobs.m, k)
			continue
		}
		out = append(out, j)
	}
	writeJSON(w, 200, map[string]any{"jobs": out})
}

// startBatch 启动批量产线(供 produce 与手动接口共用)。
func (s *Server) startBatch(prefix string, eps []string) bool {
	if len(eps) == 0 {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "error",
			Message: "✕ 批量产线未启动:《" + prefix + "》没有可处理的分集(拆集 0 集,见上方日志)"})
		return false
	}
	batch.mu.Lock()
	if batch.running {
		batch.mu.Unlock()
		return false
	}
	batch.prefix, batch.projects, batch.cur, batch.stop, batch.running = prefix, eps, 0, false, true
	batch.mu.Unlock()
	go s.runNovelBatch(prefix, eps)
	return true
}

// splitNovelToProjects 拆集核心(与 /api/novel-split 共用):返回(系列名, 子项目列表)。
// artStyle 来自立项.json 的 art_style 字段(空=由 LLM 自定风格)。
func (s *Server) splitNovelToProjects(ctx context.Context, name, text string, max int, artStyle string, onEps func(series string, newEps []string)) (string, []string, error) {
	g := s.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	// 拆集参数从设置读取(0=默认值;2026-09-11 用户定调:灵活配置)
	splitCfg := g.Split
	dflt := func(v, d int) int { if v <= 0 { return d }; return v }
	chunkSize := dflt(splitCfg.ChunkSize, 20000)
	maxOrig := dflt(splitCfg.MaxOrigChars, 9000)
	conc := dflt(splitCfg.Concurrency, 3)
	prevSum := dflt(splitCfg.PrevSummary, 400)
	briefMin := dflt(splitCfg.BriefMin, 400)
	briefMax := dflt(splitCfg.BriefMax, 700)
	if !client.Configured() {
		return "", nil, fmt.Errorf("LLM 未配置")
	}
	heartbeatCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		start := time.Now()
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-heartbeatCtx.Done():
				return
			case <-t.C:
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
					Message: fmt.Sprintf("⏳ 《%s》拆集解析中…已等待 %d 秒(分层拆解逐段进行)", name, int(time.Since(start).Seconds()))})
			}
		}
	}()
	maxLine := ""
	if max > 0 {
		maxLine = fmt.Sprintf("\n- 最多拆 %d 集。", max)
	}
	splitOnce := func(userText string) (string, error) {
		cctx, cancel := context.WithTimeout(ctx, time.Duration(g.LLM.Timeout)*time.Second+2*time.Minute)
		defer cancel()
		// json_object 模式必须关:v0.33.16 起拆集走分节定界协议(非 JSON),
		// json 模式会强制模型输出 JSON 与协议打架
		return client.Chat(cctx, []llm.Message{
			{Role: "system", Content: novelSplitSystem},
			{Role: "user", Content: userText + maxLine + "\n按 ===EP=== 分节格式输出拆集结果(纯文本,不要 JSON)。"},
		}, false)
	}
	parsePlan := func(raw string) ([]epItem, string, string, error) {
		// 第一优先:分节定界协议(v0.33.16 起提示词要求的输出格式,
		// 对引号/括号/截断等一切文本层错误免疫)
		if eps, book, style, ok := parseEpBlocks(raw); ok {
			return eps, book, style, nil
		}
		// 兜底:JSON 协议(模型偶发按旧习惯输出时仍能吃下)
		// 第一优先:整体 JSON 解析(最快,正常情况下走这条路)
		var pl struct {
			Book     string   `json:"book"`
			Style    string   `json:"style"`
			Episodes []epItem `json:"episodes"`
		}
		if err := jsonUnmarshalStrict(trimJSON(repairJSONStrings(raw)), &pl); err == nil && len(pl.Episodes) > 0 {
			return pl.Episodes, pl.Book, pl.Style, nil
		}
		// 整体解析失败 → 逐集正则提取(终极兜底,免疫一切结构错误:
		// å/è 乱字、截断、缺括号、多余文本,只要单个集对象完整就能提取)
		eps := extractEpisodes(raw)
		if len(eps) == 0 {
			return nil, "", "", fmt.Errorf("整体解析和逐集提取均失败")
		}
		// 从提取的 map 转回 epItem
		out := make([]epItem, 0, len(eps))
		for _, m := range eps {
			ep := epItem{}
			if v, ok := m["ep"].(float64); ok {
				ep.Ep = int(v)
			}
			if v, ok := m["title"].(string); ok {
				ep.Title = v
			}
			if v, ok := m["chapters"].(string); ok {
				ep.Chapters = v
			}
			if v, ok := m["brief"].(string); ok {
				ep.Brief = v
			}
			if ep.Brief != "" {
				out = append(out, ep)
			}
		}
		// 提取 book/style(从原始文本找)
		book, style := "", ""
		if m := regexp.MustCompile(`"book"\s*:\s*"([^"]*)"`).FindStringSubmatch(raw); len(m) > 1 {
			book = m[1]
		}
		if m := regexp.MustCompile(`"style"\s*:\s*"([^"]*)"`).FindStringSubmatch(raw); len(m) > 1 {
			style = m[1]
		}
		return out, book, style, nil
	}

	// ---- 分层拆解 + 流水线化 ----
	// 全本(如 240 章 39 万字)单次调用输出上限装不下完整拆集;按章节切块
	// 逐段拆解,且**逐段建项**:onEps 回调让产线在首段就绪时即启动,后续段
	// 静默补集(生产者-消费者,2026-09-10 用户定调)。
	type chunk struct{ label, body string }
	var chunks []chunk
	// 阈值 4 万(原 12 万):8-10 万字的书单段拆解时 brief+chapters+思考链
	// 仍会撞输出上限截断(2026-09-10 实证 ep12 处 JSON 腰斩);分段输出才可控
	if len([]rune(text)) > chunkSize {
		// 4 万字/段:更大的段输出(30-45 集大纲+思考链)会撞 completion 上限
		// (2026-09-10 实证第 1/8 段 JSON 截断);缩小段=缩输出。
		for _, ck := range chunkNovelText(text, chunkSize) {
			chunks = append(chunks, chunk(ck))
		}
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
			Message: fmt.Sprintf("📚 全本 %d 字,分层拆解:共 %d 段(逐段拆集,首段即开产线,后续段静默补集)", len([]rune(text)), len(chunks))})
	} else {
		chunks = []chunk{{label: "全书", body: text}}
	}

	series := name
	seriesFixed := false
	fixSeries := func(book string) {
		if seriesFixed {
			return
		}
		seriesFixed = true
		if series == "连载" && strings.TrimSpace(book) != "" {
			series = strings.TrimSpace(book)
		}
		if safe := safeSeries(series); safe != series {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
				Message: "书名含项目名不支持的字符号(如全角逗号/空格),已自动规整为「" + safe + "」"})
			series = safe
		}
		jobSet(series, "running", 0, "AI 拆集通读中…")
	}
	globalStyle := ""
	var epsAll []string // 全部集(含补缺命中的已有集)
	var lastEp epItem   // 跨段前情(上一集概要,供 SaveSource 前情提要)
	createFail := 0
	createdTotal := 0
	createSeg := func(segEps []epItem, ck chunk) {
		fixSeries(series)
		var created []string
		for _, ep := range segEps {
			epName := fmt.Sprintf("%s·第%02d集·%s", series, ep.Ep, safeSeries(ep.Title))
			if ex := s.seriesExistingEp(series, ep.Ep); ex != "" {
				epsAll = append(epsAll, ex)
				lastEp = ep
				continue
			}
			if _, ok := s.store.Get(epName); ok {
				epsAll = append(epsAll, epName)
				lastEp = ep
				continue
			}
			if _, err := s.store.Create(epName, s.projectConfigDefault()); err != nil {
				createFail++
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
					Message: fmt.Sprintf("⚠ 分集 %d 创建失败(%v),已跳过", ep.Ep, err)})
				continue
			}
			var prev string
			if lastEp.Ep > 0 {
				prev = fmt.Sprintf("\n【前情提要】第%02d集《%s》:%s\n", lastEp.Ep, lastEp.Title, clampRunes(lastEp.Brief, prevSum))
			}
			src := fmt.Sprintf("《%s》系列短视频 第%02d 集《%s》\n全局风格:%s%s\n\n【本集剧情】%s",
				series, ep.Ep, ep.Title, globalStyle, prev, ep.Brief)
			// 附本集覆盖章节的小说原文:分镜 LLM 直接看原文逐段改编,不再是大纲
			// 的摘要——跳段/断章取义的根治(2026-09-10 用户定调)
			if orig := extractEpOriginal(ck.body, ep.Chapters, maxOrig); orig != "" {
				src += "\n\n【本集原文】(分镜必须沿此原文节拍逐段改编,覆盖全部关键情节)\n" + orig
			}
			_ = s.store.SaveSource(epName, src)
			epsAll = append(epsAll, epName)
			created = append(created, epName)
			lastEp = ep
			createdTotal++
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success", ProjectID: epName,
				Message: fmt.Sprintf("已创建分集 %d:《%s》", ep.Ep, epName)})
		}
		if onEps != nil && len(created) > 0 {
			onEps(series, created)
		}
	}
	// ===== 并行拆段(2026-09-10 用户定调:字数多就拆多任务跑,互不影响)=====
	// 各段章界不重叠、集号由系统按段序统一编排,段间零依赖 → 3 路并发
	// 调 LLM,总耗时≈单段时长(原逐段串行是段数×单段)。
	type segResult struct {
		eps   []epItem
		book  string
		style string
		err   error
	}
	results := make([]segResult, len(chunks))
	if len(chunks) > 1 {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
			Message: fmt.Sprintf("📚 全本 %d 字,分层拆解 %d 段,3 路并行解析(互不影响)", len([]rune(text)), len(chunks))})
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for ci, ck := range chunks {
		wg.Add(1)
		go func(ci int, ck chunk) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var user string
			if len(chunks) > 1 {
				// 集数密度按段内章数动态定(2026-09-11 用户定调「细中细」:
				// 原固定「最多 8 集/段」把 56 章压成 49 集,节拍被强制合并)。
				// 上限=章数×1.5+2,钳制 [8,24](输出预算:24 集×700 字×1.5
				// token+思考 ≈ 55K < 65K,24 为安全顶)
				nCh := countChapterHeads(ck.body)
				maxEps := nCh + nCh/2 + 2
				if maxEps > 24 {
					maxEps = 24
				}
				if maxEps < 8 {
					maxEps = 8
				}
				epsHint := "本段通常 5-8 集(最多 8 集,超过则合并节拍;输出预算有限,宁精勿多)"
				if nCh > 0 {
					epsHint = fmt.Sprintf("本段覆盖 %d 章:每 1-2 章拆 1 集,信息密集的单章拆 2-3 集,禁止把不同剧情节拍合并进同一集(细拆:不注水也不压缩);本段最多 %d 集", nCh, maxEps)
				}
				user = fmt.Sprintf("【全书分段】本段是全书 %d 段中的第 %d 段:各段章界不重叠、剧情独立,集号由系统统一编排,无需关心全局编号;%s,每集 brief "+fmt.Sprint(briefMin)+"-"+fmt.Sprint(briefMax)+" 字逐拍列节拍\n【本段正文·%s 起】\n\"\"\"\n%s\n\"\"\"\n", len(chunks), ci+1, epsHint, ck.label, ck.body)
			} else {
				user = "小说正文:\n\"\"\"\n" + text + "\n\"\"\"\n"
			}
			// 段级自动重试:思考模型大 JSON 偶发截断/串格式/空集,重试多可过
			var eps []epItem
			var book, style string
			var err error
			for attempt := 1; attempt <= 2; attempt++ {
				var raw string
				raw, err = splitOnce(user)
				if err == nil {
					eps, book, style, err = parsePlan(raw)
					if err == nil {
						if len(eps) > 0 {
							break // 正常:有集
						}
						// 合法 JSON 但 episodes 为空——LLM 认为该段无独立剧情节拍
						// 不是错误,跳过该段(比报错重试更合理:内容太少确实拆不出)
						err = nil
						break
					}
					// 解析失败:带具体错误(不再是 nil)
					err = fmt.Errorf("JSON 解析失败:%v(输出尾部:…%s)", err, tailRunes(raw, 120))
				}
				if attempt == 1 && err != nil {
					s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
						Message: fmt.Sprintf("⚠ 第 %d/%d 段拆集结果异常,自动重试一次:%v", ci+1, len(chunks), err)})
				}
			}
			results[ci] = segResult{eps: eps, book: book, style: style, err: err}
		}(ci, ck)
	}
	wg.Wait()
	// 按段序统一建项(集号全局连续;建项顺序 = 段顺序,产线流式推送不变)
	planned := 0
	var plannedEps []epItem // 全部已规划集(覆盖核对用,含创建失败的)
	for ci, ck := range chunks {
		res := results[ci]
		if res.err != nil {
			return series, epsAll, fmt.Errorf("拆集失败(第 %d/%d 段):%v", ci+1, len(chunks), res.err)
		}
		if len(res.eps) == 0 {
			// 该段内容太少 LLM 拆不出独立集(合法空结果),跳过并记录
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
				Message: fmt.Sprintf("📚 第 %d/%d 段内容较少,无独立剧情节拍,跳过", ci+1, len(chunks))})
			continue
		}
		if ci == 0 {
			fixSeries(res.book)
			// art_style 优先(立项.json 指定的渲染风格);无则用 LLM 首段生成的风格
			if artStyle != "" {
				globalStyle = artStyle
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
					Message: "🎨 视频渲染风格锁定(立项.json art_style):" + clampRunes(artStyle, 80)})
			} else {
				globalStyle = res.style
			}
		}
		for j := range res.eps {
			planned++
			res.eps[j].Ep = planned
		}
		plannedEps = append(plannedEps, res.eps...)
		createSeg(res.eps, ck)
		jobSet(series, "running", createdTotal, fmt.Sprintf("已建 %d 集(拆解进行中)", createdTotal))
		if len(chunks) > 1 {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
				Message: fmt.Sprintf("📚 分层拆解 %d/%d 段入队完成,累计已建 %d 集", ci+1, len(chunks), createdTotal)})
		}
	}
	if len(epsAll) == 0 {
		err := fmt.Errorf("拆集结果 %d 集全部创建失败,请查看日志告警", planned)
		jobSet(series, "failed", 0, err.Error())
		return "", nil, err
	}
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
		Message: fmt.Sprintf("✔ 拆集完成:《%s》共 %d 集,已创建 %d 个子项目%s", series, planned, createdTotal,
			failNote(createFail))})
	// 章节覆盖核对(细拆质检):各集 chapters 覆盖的章号 vs 全书章头集合,
	// 缺章即报——LLM 跳章不再静默(2026-09-11 用户定调「细中细是否有做到」)
	if headNums := chapterNumSet(text); len(headNums) > 0 {
		cov := map[int]bool{}
		numRe := regexp.MustCompile(`第\s*0*([0-9]{1,4})\s*[章回节]`)
		for _, ep := range plannedEps {
			var vals []int
			for _, m := range numRe.FindAllStringSubmatch(ep.Chapters, -1) {
				if v, e := strconv.Atoi(m[1]); e == nil {
					vals = append(vals, v)
				}
			}
			if len(vals) == 0 {
				continue
			}
			lo, hi := vals[0], vals[0]
			for _, v := range vals {
				if v < lo {
					lo = v
				}
				if v > hi {
					hi = v
				}
			}
			if hi-lo <= 30 { // 区间异常大视为脏数据,只记出现的号
				for c := lo; c <= hi; c++ {
					cov[c] = true
				}
			} else {
				for _, v := range vals {
					cov[v] = true
				}
			}
		}
		var missing []int
		for c := range headNums {
			if !cov[c] {
				missing = append(missing, c)
			}
		}
		sort.Ints(missing)
		switch {
		case len(missing) == 0:
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
				Message: fmt.Sprintf("✔ 章节覆盖核对:%d/%d 章全部有对应分集", len(headNums), len(headNums))})
		case len(missing) <= 20:
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
				Message: fmt.Sprintf("⚠ 章节覆盖核对:缺 %d 章未拆入任何分集(第 %v 章),可重拆补缺", len(missing), missing)})
		default:
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
				Message: fmt.Sprintf("⚠ 章节覆盖核对:仅覆盖 %d/%d 章,chapters 字段可能失准,建议重拆", len(headNums)-len(missing), len(headNums))})
		}
	}
	jobSet(series, "done", createdTotal, fmt.Sprintf("拆集完成:%d 集", createdTotal))
	return series, epsAll, nil
}

// chapterNumSet 从文本章头行提取全书章号集合(目录已剥,正文章头为准;
// 不直接全文找「第N章」避免正文引用误收)。
func chapterNumSet(text string) map[int]bool {
	numRe := regexp.MustCompile(`第\s*0*([0-9]{1,4})\s*[章回节]`)
	out := map[int]bool{}
	for _, line := range chapterHeadRe.FindAllString(text, -1) {
		if m := numRe.FindStringSubmatch(line); m != nil {
			if v, e := strconv.Atoi(m[1]); e == nil {
				out[v] = true
			}
		}
	}
	return out
}

// failNote 创建失败数附注。
func failNote(n int) string {
	if n > 0 {
		return fmt.Sprintf("(另有 %d 集创建失败已跳过)", n)
	}
	return ""
}

func hasTxtExt(p string) bool {
	n := strings.ToLower(filepath.Ext(p))
	return n == ".md" || n == ".txt"
}

// projectConfigDefault 新项目默认参数(与创建项目接口一致:全局默认引擎优先)。
func (s *Server) projectConfigDefault() project.ProjectConfig {
	cfg := project.ProjectConfig{Params: project.DefaultStageParams()}
	switch s.cfg.Get().Render.DefaultEngine {
	case "auto", "comfy", "api":
		cfg.Params.Render.Engine = s.cfg.Get().Render.DefaultEngine
	}
	return cfg
}

// chunkNovelText 按章节标题把全书切成 ≤limit 字的段(无章节标题则按尺寸硬切)。
// 供分层拆解:每段一次 LLM 调用,输出体量可控,整本不丢内容。
func chunkNovelText(text string, limit int) []struct{ label, body string } {
	type piece struct{ head, body string }
	re := regexp.MustCompile(`(?m)^\s{0,4}(第[0-9零〇一二三四五六七八九十百千两]+[章回节])[^\n]{0,50}\s*$`)
	locs := re.FindAllStringSubmatchIndex(text, -1)
	var pieces []piece
	if len(locs) >= 8 { // 章节结构可信
		for i, loc := range locs {
			start := loc[0]
			end := len(text)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			pieces = append(pieces, piece{head: strings.TrimSpace(text[loc[0]:loc[1]]), body: text[start:end]})
		}
		if locs[0][0] > 0 { // 楔子/简介并入首段
			pre := text[:locs[0][0]]
			if strings.TrimSpace(pre) != "" && len(pieces) > 0 {
				pieces[0].body = pre + pieces[0].body
			}
		}
	} else { // 无章节结构:按尺寸硬切,段落边界优先
		runes := []rune(text)
		for start := 0; start < len(runes); {
			end := start + limit
			if end >= len(runes) {
				end = len(runes)
			} else {
				for end > start && end < len(runes) && runes[end] != '\n' {
					end--
				}
				if end == start {
					end = start + limit
				}
			}
			pieces = append(pieces, piece{head: fmt.Sprintf("第 %d 块", len(pieces)+1), body: string(runes[start:end])})
			start = end
		}
	}
	// 贪心合并相邻章成 ≤limit 的段
	var out []struct{ label, body string }
	var curLabel, curBody string
	flush := func() {
		if strings.TrimSpace(curBody) != "" {
			out = append(out, struct{ label, body string }{curLabel, curBody})
		}
		curLabel, curBody = "", ""
	}
	for _, pc := range pieces {
		switch {
		case curBody == "":
			curLabel, curBody = pc.head, pc.body
		case len([]rune(curBody))+len([]rune(pc.body)) <= limit:
			curBody += pc.body
		default:
			flush()
			curLabel, curBody = pc.head, pc.body
		}
	}
	flush()
	return out
}

// extractEpOriginal 从分段正文中按章节范围("第X章 标题~第Y章 标题")提取
// 本集覆盖的原文;匹配失败返回空(降级为仅大纲)。先整词前缀匹配,再退回
// "第X章"序号词,容忍 LLM 措辞偏差。
func extractEpOriginal(body, chapters string, maxOrig int) string {
	if maxOrig <= 0 {
		maxOrig = 9000
	}
	if strings.TrimSpace(chapters) == "" {
		return ""
	}
	parts := strings.Split(chapters, "~")
	startTok := strings.TrimSpace(parts[0])
	endTok := strings.TrimSpace(parts[len(parts)-1])
	re := regexp.MustCompile(`(?m)^\s{0,4}(第[0-9零〇一二三四五六七八九十百千两]+[章回节])[^\n]{0,50}`)
	locs := re.FindAllStringSubmatchIndex(body, -1)
	if len(locs) == 0 {
		return ""
	}
	head := func(i int) string { return strings.TrimSpace(body[locs[i][2]:locs[i][3]]) }
	find := func(tok string) int {
		if tok == "" {
			return -1
		}
		for i := range locs {
			if strings.HasPrefix(head(i), tok) || strings.HasPrefix(tok, head(i)) {
				return i
			}
		}
		if m := regexp.MustCompile(`第[0-9零〇一二三四五六七八九十百千两]+[章回节]`).FindString(tok); m != "" {
			for i := range locs {
				if strings.HasPrefix(head(i), m) {
					return i
				}
			}
		}
		return -1
	}
	si := find(startTok)
	if si < 0 {
		return ""
	}
	ei := find(endTok)
	if ei < si {
		ei = si
	}
	startAt, endAt := locs[si][0], len(body)
	if ei+1 < len(locs) {
		endAt = locs[ei+1][0]
	}
	orig := strings.TrimSpace(body[startAt:endAt])
	if r := []rune(orig); len(r) > maxOrig {
		orig = string(r[:maxOrig]) + "\n…(原文超长截断)"
	}
	return orig
}
