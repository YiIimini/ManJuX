// novels.go 小说库:扫描设置目录中的小说(书=子文件夹,优先「全本」,次选合并「正文」
// 分卷章节;封面取「封面」文件夹内图片),支持弹窗阅读详情与一键视频创作。
package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

// novelProduced 是否已为该书创建过系列/同名项目。
func (s *Server) novelProduced(name string) bool {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `(·第\d+集|$)`)
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
		var src string
		if fbs, _ := filepath.Glob(filepath.Join(in.Path, "全本", "*")); len(fbs) > 0 && hasTxtExt(fbs[0]) {
			src = fbs[0]
		} else {
			// 合并正文章节
			var paths []string
			_ = filepath.WalkDir(in.Path, func(p string, e os.DirEntry, err error) error {
				if err != nil || e.IsDir() || !hasTxtExt(p) {
					return nil
				}
				rel := strings.ToLower(p)
				if strings.Contains(rel, "素材") || strings.Contains(rel, "设定") || strings.Contains(rel, "docs") || strings.Contains(rel, "封面") {
					return nil
				}
				paths = append(paths, p)
				return nil
			})
			sort.Strings(paths)
			var sb strings.Builder
			for _, p := range paths {
				b, _ := os.ReadFile(p)
				sb.WriteString(string(b))
				sb.WriteString("\n\n")
			}
			text = sb.String()
		}
		if src != "" {
			b, err := os.ReadFile(src)
			if err != nil {
				writeErr(w, 500, err)
				return
			}
			text = string(b)
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
	if len([]rune(text)) < 200 {
		writeErr(w, 400, fmt.Errorf("小说正文过短(%d 字)", len([]rune(text))))
		return
	}
	// 查重:同书系列/同名项目已存在
	if s.novelProduced(name) {
		writeErr(w, 409, fmt.Errorf("《%s》已创建过视频项目(首页「剧集合集」卡片可查看/续跑),请勿重复创建", name))
		return
	}
	// 拆集+建项目(复用 novel-split 核心)
	series, eps, err := s.splitNovelToProjects(r.Context(), name, text, in.Max)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// 封面复制到每集
	if coverSrc != "" {
		ext := strings.ToLower(filepath.Ext(coverSrc))
		for _, ep := range eps {
			dst := filepath.Join(s.store.Dir(ep), "assets", "cover"+ext)
			if b, e := os.ReadFile(coverSrc); e == nil {
				_ = os.MkdirAll(filepath.Dir(dst), 0o755)
				_ = os.WriteFile(dst, b, 0o644)
			}
		}
	}
	// 自动启动批量产线
	s.startBatch(series, eps)
	writeJSON(w, 200, map[string]any{"ok": true, "book": series, "episodes": len(eps),
		"message": fmt.Sprintf("《%s》已拆 %d 集并自动开始批量产线", series, len(eps))})
}

// startBatch 启动批量产线(供 produce 与手动接口共用)。
func (s *Server) startBatch(prefix string, eps []string) bool {
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
func (s *Server) splitNovelToProjects(ctx context.Context, name, text string, max int) (string, []string, error) {
	g := s.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	if !client.Configured() {
		return "", nil, fmt.Errorf("LLM 未配置")
	}
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
			case <-t.C:
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
					Message: fmt.Sprintf("⏳ 《%s》拆集通读中…已等待 %d 秒(整本拆解通常 2-6 分钟)", name, int(time.Since(start).Seconds()))})
			}
		}
	}()
	maxLine := ""
	if max > 0 {
		maxLine = fmt.Sprintf("\n- 最多拆 %d 集。", max)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(g.LLM.Timeout)*time.Second+2*time.Minute)
	defer cancel()
	raw, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: novelSplitSystem},
		{Role: "user", Content: "小说正文:\n\"\"\"\n" + text + "\n\"\"\"" + maxLine + "\n输出拆集 JSON。"},
	}, true)
	if err != nil {
		return "", nil, fmt.Errorf("拆集失败: %w", err)
	}
	var plan struct {
		Book     string `json:"book"`
		Style    string `json:"style"`
		Episodes []struct {
			Ep    int    `json:"ep"`
			Title string `json:"title"`
			Brief string `json:"brief"`
		} `json:"episodes"`
	}
	if err := jsonUnmarshalStrict(trimJSON(raw), &plan); err != nil || len(plan.Episodes) == 0 {
		return "", nil, fmt.Errorf("拆集结果解析失败,请重试")
	}
	sort.Slice(plan.Episodes, func(i, j int) bool { return plan.Episodes[i].Ep < plan.Episodes[j].Ep })
	series := name
	if series == "连载" && strings.TrimSpace(plan.Book) != "" {
		series = strings.TrimSpace(plan.Book)
	}
	var eps []string
	for i, ep := range plan.Episodes {
		epName := fmt.Sprintf("%s·第%02d集·%s", series, ep.Ep, ep.Title)
		if _, ok := s.store.Get(epName); ok {
			eps = append(eps, epName)
			continue
		}
		if _, err := s.store.Create(epName, s.projectConfigDefault()); err != nil {
			continue
		}
		var prev string
		if i > 0 {
			p := plan.Episodes[i-1]
			prev = fmt.Sprintf("\n【前情提要】第%02d集《%s》:%s\n", p.Ep, p.Title, clampRunes(p.Brief, 120))
		}
		src := fmt.Sprintf("《%s》系列短视频 第%02d 集《%s》\n全局风格:%s%s\n\n【本集剧情】%s",
			series, ep.Ep, ep.Title, plan.Style, prev, ep.Brief)
		_ = s.store.SaveSource(epName, src)
		eps = append(eps, epName)
	}
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
		Message: fmt.Sprintf("✔ 拆集完成:《%s》共 %d 集,已创建 %d 个子项目", series, len(plan.Episodes), len(eps))})
	return series, eps, nil
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
