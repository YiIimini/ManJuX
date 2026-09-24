// novels.go 小说库:扫描设置目录中的小说(书=子文件夹,优先「全本」,次选合并「正文」
// 分卷章节;封面取「封面」文件夹内图片),支持弹窗阅读详情与一键视频创作。
package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
	// 默认**倒序**(用户 2026-09-24 定调):书名降序 —— 列表新加的书名排在前面。
	sortNovels(out, novelSortDesc(r))
	writeJSON(w, 200, map[string]any{"root": root, "novels": out})
}

// novelSortDesc 解析排序方向:只有显式 ?sort=asc 才升序,其余一律默认**倒序**。
// 单独成函数是为了让"默认倒序"这条约定可被测试钉住(见 novels_sort_test.go):
// 口径散在 handler 里时,谁都能顺手改回去而没人发现。
func novelSortDesc(r *http.Request) bool {
	return !strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("sort")), "asc")
}

// sortNovels 小说库排序。desc=true 为默认的倒序。
func sortNovels(items []novelItem, desc bool) {
	sort.Slice(items, func(i, j int) bool {
		if desc {
			return items[i].Name > items[j].Name
		}
		return items[i].Name < items[j].Name
	})
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
