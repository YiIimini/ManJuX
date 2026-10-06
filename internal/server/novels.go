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

	"github.com/YiIimini/ManJuX/internal/project"
)

type novelItem struct {
	Name     string      `json:"name"`             // 书名(=文件夹名/文件主干)
	Path     string      `json:"path"`             // 书目录(或文件)绝对路径
	Type     string      `json:"type"`             // dir / file
	Chars    int         `json:"chars"`            // 正文字数估算
	Chapters int         `json:"chapters"`         // 章节文件数
	Cover    string      `json:"cover"`            // 封面绝对路径(空=无)
	Produced bool        `json:"produced"`         // 是否已创建过系列项目
	Hidden   bool        `json:"hidden"`           // 已隐藏(界面状态,见 novels_hidden.go)
	Script   *bookScript `json:"script,omitempty"` // 目录里发现的技能端脚本包(nil=没有,见 novels_script.go)

	// Detail 技能侧的**结构化明细**（`分镜/分镜明细.json`）。2026-09-27 起它是「脚本创作」
	// 的首选来源 —— 有它时入口可用、且**不**标「历史包」（那个标签只对旧渲染包有意义）。
	Detail *novelDetailBrief `json:"detail,omitempty"`
}

// novelDetailBrief 书卡上要显示的结构化明细概况（只够判断"有没有、多少集"）。
type novelDetailBrief struct {
	Episodes int    `json:"episodes"`
	Shots    int    `json:"shots"`
	Source   string `json:"source"`
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
		writeJSON(w, 200, map[string]any{"root": "", "novels": []novelItem{}, "hidden_count": 0, "hint": "未配置小说目录,请在设置中填写"})
		return
	}
	if _, err := os.Stat(root); err != nil {
		writeJSON(w, 200, map[string]any{"root": root, "novels": []novelItem{}, "hidden_count": 0, "hint": "目录不存在:" + root})
		return
	}
	// 隐藏是界面状态(见 novels_hidden.go):默认**不列出**已隐藏的作品 —— 用户点"隐藏"
	// 就是不想再看见它;要在库页重新找到它,前端用 ?hidden=1 再拉一次(顺带拿到计数)。
	hiddenSet := s.loadHiddenNovels()
	withHidden := r.URL.Query().Get("hidden") == "1"
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
	// 标记"这本书目录里有没有技能端脚本包"(见 novels_script.go):有的话界面上给第二个
	// 入口「脚本创作」—— 直接导包,不再跑 LLM 拆集。检测放在排序后,批量读盘的顺序更顺。
	for i := range out {
		out[i].Script = s.findBookScript(out[i].Path)
		if d := loadNovelDetail(out[i].Path); d != nil {
			out[i].Detail = &novelDetailBrief{
				Episodes: len(detailEpKeys(d.Shots)), Shots: len(d.Shots), Source: novelShotDetailJSON,
			}
		}
	}
	// 打隐藏标记 + 过滤(打标记在排序之后,顺序不受影响)
	visible := out[:0]
	hiddenCount := 0
	for i := range out {
		if hiddenSet[s.novelHiddenKey(out[i].Path)] {
			out[i].Hidden = true
			hiddenCount++
			if !withHidden {
				continue
			}
		}
		visible = append(visible, out[i])
	}
	out = visible
	writeJSON(w, 200, map[string]any{
		"root": root, "novels": out, "hidden_count": hiddenCount, "with_hidden": withHidden,
	})
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
		case project.IsFullWidthPunct(r):
			// 技能侧 2026-09-28 规定：章名/书名**只准全角标点**（禁 ASCII 标点与空格）。
			// 盘上因此出现过平台替他们改字的情况：书名《补碑人改命，守钟英逆时》被改成
			// `补碑人改命·守钟女逆时`、章名「她替他挡账（二）」被改成「她替他挡账·二」——
			// **静默改字**，而技能侧明确要求不要这么做（章名就是集名，改字会让成片与集号对不上）。
			// 他们的字符集既然已保证合规，平台就照原样保留。
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
	// **空切片必须是 []，不能是 null**（2026-09-30 深度审计）：一本书的目录里没有
	// .md/.txt（只有 epub / 图片 / 空目录）时 `chapters` 是 nil，Go 把它 marshal 成 `null`；
	// 而详情弹窗直接读 `dlg.chapters.length`（三处）⇒ 点开就整页炸
	// `Cannot read properties of null (reading 'length')`，弹窗内容一个字都不显示。
	// 前端不必给每个字段都加 `?.`（新界面不会再踩）。
	// 前端不必给每个字段都加 `?.`（新界面不会再踩）。
	if chapters == nil {
		chapters = []novelChapter{}
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
// seriesNameProblem 名字里第一个**平台不接受**的字符（"" = 合规）。
//
// 【为什么要这条判据】技能侧 2026-09-28 通知统一了符号规定（章名/书名只准全角标点，
// 禁一切 ASCII 标点与空格），并明确要求平台 **不要静默替换** —— 原话："旧作品若出现这类字符，
// 请先报给我们改名，不要静默替换（章名是集名，静默替换会让成片与集号记录不一致）"。
// 而平台此前是把非法字**换成 `·`** 再建项目的：盘上因此出现过 `坏书·带·尖括号` 这种
// 谁也对不上的目录名。现在改成"**先报，不改**"：判到非法字就拒绝这一集/这一批，
// 把原文与字符一起报出来，让技能侧改名后重跑。
//
// 判据用 project.ValidProjectName（**唯一出处**），不在这里另写一套字表 —— 两套字表
// 迟早一处松一处紧，表现就是"这条入口拦了那条入口没拦"。
func seriesNameProblem(name string) string {
	for _, r := range name {
		if !project.IsAllowedNameRune(r) {
			return string(r)
		}
	}
	return ""
}
