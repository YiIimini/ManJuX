// novel_formats.go 小说库「正文文件」的**唯一**格式判据与取正文入口。
//
// 【为什么要这个文件】盘上真机那本书《离婚后，千亿前夫跪求复合》的正文就是
// `第一章 签字，滚出顾家.docx` … `第八章 ….docx` 共 8 个 Word 文档，而平台原来只认
// `.md`/`.txt`：书卡列 **0 章 / 0 字**，点「视频制作」得到的是
// 「小说正文过短(0 字,预处理后)」—— 文件明明在，却一个字都读不到。
//
// 支持哪些（见 novelTextExts，**唯一出处**）：
// `.txt` `.text` `.md` `.markdown` 直接取字节，编码与装饰交给 NormalizeNovelText；
// `.docx` 取 zip 里 word/document.xml 的段落文本（Word 把一句话切成多个 run 是常态，
// 同一段内的 run **直接拼**，否则读出来的「第一章」会断成「第」+「一章」，章头就认不出）；
// `.epub` 走 META-INF/container.xml → OPF → **按 spine 顺序**逐个 xhtml 抽文本。
//
// 只用标准库（archive/zip + encoding/xml）⇒ 不引新依赖、不要求用户装外部工具。
//
// 永久边界（**本次不读，但一定要逐条点名**，见 novelUnsupportedReport）：
// `.pdf`（要版式解析器）、`.doc`/`.rtf`（老式二进制与控制字，要真解析器）、`.wps`、
// `.mobi`/`.azw3`（Kindle 私有容器）、`.html`/`.htm`（网上扒来的 HTML 几乎都不是严格 XML，
// 「宽松分词器」是新依赖）。不做就如实说：把**文件名和数量报出来**，
// 用户才不会对着「小说正文过短(0 字)」猜原因。
package server

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// novelTextExts 平台**读作正文**的扩展名（唯一出处：识别、取正文、报错文案都看它）。
var novelTextExts = []string{".txt", ".text", ".md", ".markdown", ".docx", ".epub"}

// novelDocExts 平台**明确不读**的文档格式（要逐条点名的那份清单，不含图片/配置等无关文件）。
var novelDocExts = []string{".pdf", ".doc", ".rtf", ".wps", ".mobi", ".azw3", ".html", ".htm"}

// novelContainerExts 容器格式：**盘上字节数与正文字数不成比例**的两种 ——
// zip 里还有 XML 标签、样式表、图片。实测真机那 8 个 .docx：合计 120405 B，
// 按"3 字节/汉字"估出 40135 字，而真实正文只有 17577 字（虚高 2.3 倍）。
var novelContainerExts = []string{".docx", ".epub"}

// isNovelContainer 判是否是"要解压才知道正文有多长"的容器格式。
func isNovelContainer(p string) bool {
	ext := novelExt(p)
	for _, e := range novelContainerExts {
		if e == ext {
			return true
		}
	}
	return false
}

// novelFileChars 一册正文的**字数**（列表页用）：
// 纯文本按"3 字节/汉字"估；容器格式（.docx/.epub）**真解一遍正文再数** ——
// 否则书卡会把 1.7 万字的书写成"4 万字"（见 novelContainerExts 的实测）。
// 解不开的容器退回字节估算：列表页不能因为一个坏文件整页失败
// （该文件在"一键创作"那条链上会**逐条点名**报出来，见 novels_produce.go）。
func novelFileChars(p string, size int64) int {
	if isNovelContainer(p) {
		if text, err := readNovelTextFile(p); err == nil {
			return len([]rune(text))
		}
	}
	return int(size / 3)
}

// novelZipEntryMax 单个 zip 条目（docx/epub 里的 XML/HTML）的解压上限。
// 正文 XML 不会有这么大；这是防"压缩包炸弹"的**确定性**上限，不是性能调优。
const novelZipEntryMax = 64 << 20

// novelTextExtsHint 支持格式的一句话（错误与播报共用，避免两处措辞漂移）。
func novelTextExtsHint() string { return strings.Join(novelTextExts, " / ") }

// novelExt 取小写扩展名（含点；无扩展名返回空串）。
func novelExt(p string) string { return strings.ToLower(filepath.Ext(p)) }

// isNovelTextFile 判「这个文件是不是小说正文册」——**全仓唯一判据**。
// 原来这个判据叫 hasTxtExt（只认 .md/.txt），名字与实现都只覆盖纯文本；
// 现在连 .docx/.epub 一起认，名字改成说人话的 isNovelTextFile，免得下一个调用方
// 又按"只有纯文本"去用。
func isNovelTextFile(p string) bool {
	ext := novelExt(p)
	if ext == "" {
		return false
	}
	for _, e := range novelTextExts {
		if e == ext {
			return true
		}
	}
	return false
}

// isNovelDocExt 判「这个扩展名是平台不读的文档格式」（用于如实点名）。
func isNovelDocExt(ext string) bool {
	for _, e := range novelDocExts {
		if e == ext {
			return true
		}
	}
	return false
}

// readNovelTextFile 按扩展名取正文（**所有读小说正文的地方都走这里**）。
// 不支持的格式返回明确错误（带支持列表），绝不把二进制当文本返回 ——
// 旧实现是裸 os.ReadFile：给 .docx 就返回 zip 二进制，再经 NormalizeNovelText
// 变成一串乱码喂给拆集 LLM，谁也查不出问题出在哪。
func readNovelTextFile(p string) (string, error) {
	ext := novelExt(p)
	switch ext {
	case ".txt", ".text", ".md", ".markdown":
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		return string(b), nil
	case ".docx":
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		return docxPlainText(b)
	case ".epub":
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		return epubPlainText(b)
	}
	shown := ext
	if shown == "" {
		shown = "（无扩展名）"
	}
	return "", fmt.Errorf("不支持的文本格式 %s（平台可读：%s）", shown, novelTextExtsHint())
}

// fullBookFile 取「全本」目录里的**第一个可读正文文件**（没有则空串）。
//
// 旧实现三处各写一遍 `filepath.Glob(全本/*)` 然后只看 `fbs[0]`：Glob 是**按名字排序**的，
// 目录里只要先排着一张封面图或一个 README，整份「全本」就被跳过 —— 与"全本优先"的口径
// 恰好相反（真机那本书的 全本/ 里就同时躺着 .md 与几张图）。收在这里一处。
func fullBookFile(bookDir string) string {
	ents, err := os.ReadDir(filepath.Join(bookDir, "全本"))
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		if isNovelTextFile(n) {
			return filepath.Join(bookDir, "全本", n)
		}
	}
	return ""
}

// novelUnsupportedReport 扫一本书目录里「平台不读的文档格式」，返回一句能直接播报的文案
// （没有这类文件则返回空串）。maxExamples 是举例的文件名条数上限。
//
// 这条是"不许静默跳过"的落地：格式不支持时用户看到的必须是
// 「这本书里有 .pdf ×6（例：…），平台只读 …」，而不是猜"为什么 0 字"。
func novelUnsupportedReport(bookDir string, maxExamples int) string {
	counts := map[string]int{}
	var examples []string
	_ = filepath.WalkDir(bookDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch strings.ToLower(d.Name()) {
			// 封面图目录/素材目录/依赖目录里的文档与"这本书的正文"无关，别拿来吓用户。
			case "封面", "素材", "素材包", "docs", "node_modules", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		ext := novelExt(p)
		if !isNovelDocExt(ext) {
			return nil
		}
		counts[ext]++
		if len(examples) < maxExamples {
			examples = append(examples, d.Name())
		}
		return nil
	})
	if len(counts) == 0 {
		return ""
	}
	exts := make([]string, 0, len(counts))
	for e := range counts {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	parts := make([]string, 0, len(exts))
	for _, e := range exts {
		parts = append(parts, fmt.Sprintf("%s ×%d", e, counts[e]))
	}
	return fmt.Sprintf("⚠ 这本书里有平台**不读**的文档格式:%s（例:%s）—— 平台可读的文本格式是 %s；"+
		"这些文件**本次没有读入**，需要的话请先转成 .txt 或 .docx 再导入",
		strings.Join(parts, "、"), strings.Join(examples, "、"), novelTextExtsHint())
}

// ---------- .docx ----------

// docxPlainText 抽 .docx 正文：按 XML token 流走 word/document.xml。
//
// 规则（都要，缺一条真机就读不对）：
//   - 同一段（w:p）里的多个 w:t **直接拼**：Word 为拼写检查/修订会把一句话切成多个 run，
//     中间插分隔符会把「第一章 签字，滚出顾家」断成几截，章头正则就认不出来了；
//   - 段落收尾换行；w:tab → 空格；w:br / w:cr → 换行；
//   - w:del（修订删除的内容）与 w:instrText / w:fldChar（域代码，如页码）**不算正文**。
func docxPlainText(raw []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", fmt.Errorf("不是有效的 .docx（压缩包打不开）：%w", err)
	}
	data, err := zipEntryBytes(zr, "word/document.xml")
	if err != nil {
		return "", fmt.Errorf("不是有效的 .docx（缺 word/document.xml）：%w", err)
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Entity = xml.HTMLEntity
	var sb strings.Builder
	inText := false
	skip := 0 // >0 = 在 w:del / w:instrText 里
	for {
		tok, terr := dec.Token()
		if terr == io.EOF {
			break
		}
		if terr != nil {
			return "", fmt.Errorf("解析 word/document.xml 失败：%w", terr)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "del", "instrText", "fldChar":
				skip++
			case "t":
				if skip == 0 {
					inText = true
				}
			case "tab":
				if skip == 0 {
					sb.WriteByte(' ')
				}
			case "br", "cr":
				if skip == 0 {
					sb.WriteByte('\n')
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "del", "instrText", "fldChar":
				if skip > 0 {
					skip--
				}
			case "t":
				inText = false
			case "p":
				if skip == 0 {
					sb.WriteByte('\n')
				}
			}
		case xml.CharData:
			if inText && skip == 0 {
				sb.Write(t)
			}
		}
	}
	return sb.String(), nil
}

// ---------- .epub ----------

// epubPlainText 抽 .epub 正文：container.xml → OPF → 按 spine 顺序逐个 xhtml。
// 每份文档之间留空行（章界），SPINE 之外（封面页/版权页在 manifest 里但不在 spine 上）不读。
func epubPlainText(raw []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", fmt.Errorf("不是有效的 .epub（压缩包打不开）：%w", err)
	}
	opfPath := epubOPFPath(zr)
	if opfPath == "" {
		return "", fmt.Errorf("不是有效的 .epub（找不到 OPF 清单）")
	}
	opf, err := zipEntryBytes(zr, opfPath)
	if err != nil {
		return "", fmt.Errorf("读 .epub 的 OPF 失败（%s）：%w", opfPath, err)
	}
	hrefs := epubChapterHrefs(opf)
	if len(hrefs) == 0 {
		return "", fmt.Errorf("不是有效的 .epub（OPF 里没有可读章节）")
	}
	base := path.Dir(strings.ReplaceAll(opfPath, "\\", "/"))
	var sb strings.Builder
	read := 0
	for _, href := range hrefs {
		name := strings.TrimPrefix(path.Join(base, href), "./")
		data, rerr := zipEntryBytes(zr, name)
		if rerr != nil {
			return "", fmt.Errorf("读 .epub 章节失败（%s）：%w", name, rerr)
		}
		ch, perr := xhtmlPlainText(data)
		if perr != nil {
			// 严格 XML 解析失败就说清楚是**哪个文档** —— 不跳过、不硬凑：
			// 悄悄少读一章正是本仓最怕的那种事故（章号对不上但没人知道）。
			return "", fmt.Errorf(".epub 章节不是严格 XML（%s）：%w", name, perr)
		}
		if strings.TrimSpace(ch) == "" {
			continue
		}
		sb.WriteString(strings.TrimSpace(ch))
		sb.WriteString("\n\n")
		read++
	}
	if read == 0 {
		return "", fmt.Errorf("不是有效的 .epub（章节里没有文本）")
	}
	return sb.String(), nil
}

// epubOPFPath 从 META-INF/container.xml 找 OPF 路径；没有就退回"第一个 .opf"。
func epubOPFPath(zr *zip.Reader) string {
	if data, err := zipEntryBytes(zr, "META-INF/container.xml"); err == nil {
		dec := xml.NewDecoder(bytes.NewReader(data))
		dec.Entity = xml.HTMLEntity
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			if se, ok := tok.(xml.StartElement); ok && strings.EqualFold(se.Name.Local, "rootfile") {
				if p := attrVal(se, "full-path"); p != "" {
					return p
				}
			}
		}
	}
	return zipEntryBySuffix(zr, ".opf")
}

// epubChapterHrefs 取章节 href：**按 spine 顺序**；OPF 没有 spine（规范要求有，但野生的有）
// 才退回 manifest 顺序。不改写、不猜测，只决定读哪些、按什么顺序读。
func epubChapterHrefs(opf []byte) []string {
	type item struct{ id, href, media string }
	dec := xml.NewDecoder(bytes.NewReader(opf))
	dec.Entity = xml.HTMLEntity
	var items []item
	byID := map[string]string{}
	var spine []string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "item":
			it := item{id: attrVal(se, "id"), href: attrVal(se, "href"), media: attrVal(se, "media-type")}
			if it.href != "" {
				items = append(items, it)
				byID[strings.ToLower(it.id)] = it.href
			}
		case "itemref":
			if id := attrVal(se, "idref"); id != "" {
				spine = append(spine, strings.ToLower(id))
			}
		}
	}
	var out []string
	seen := map[string]bool{}
	add := func(h string) {
		h = strings.TrimSpace(h)
		if i := strings.IndexAny(h, "#?"); i >= 0 {
			h = h[:i] // 锚点/查询串不是文件名的一部分
		}
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	for _, id := range spine {
		if h, ok := byID[id]; ok {
			add(h)
		}
	}
	if len(out) == 0 {
		for _, it := range items {
			m := strings.ToLower(it.media)
			if m == "" || strings.Contains(m, "xhtml") || strings.HasSuffix(strings.ToLower(it.href), ".html") {
				add(it.href)
			}
		}
	}
	return out
}

// xhtmlPlainText 从 xhtml/XML 抽纯文本：块级元素收尾换行（章头因此自成一行），
// script/style/head 整段丢弃。严格 XML：解析不了就报错，不返回半截文本。
func xhtmlPlainText(raw []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Entity = xml.HTMLEntity // xhtml 里的 &nbsp; 等实体不在 XML 内置表里
	var sb strings.Builder
	quiet := 0
	block := func(local string) bool {
		switch local {
		case "p", "div", "br", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6",
			"blockquote", "section", "article", "figcaption", "hr", "table":
			return true
		}
		return false
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			local := strings.ToLower(t.Name.Local)
			switch local {
			case "script", "style", "head":
				quiet++
			default:
				if quiet == 0 && block(local) {
					sb.WriteByte('\n')
				}
			}
		case xml.EndElement:
			local := strings.ToLower(t.Name.Local)
			switch local {
			case "script", "style", "head":
				if quiet > 0 {
					quiet--
				}
			default:
				if quiet == 0 && block(local) {
					sb.WriteByte('\n')
				}
			}
		case xml.CharData:
			if quiet == 0 {
				sb.Write(t)
			}
		}
	}
	return sb.String(), nil
}

// ---------- zip 小工具 ----------

// attrVal 取元素属性值（忽略大小写与命名空间前缀；找不到返回空串）。
func attrVal(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if strings.EqualFold(a.Name.Local, name) {
			return a.Value
		}
	}
	return ""
}

// zipEntryBytes 读出 zip 里的一个条目（忽略大小写、忽略开头的 ./；href 里的 %20 也会试一次）。
func zipEntryBytes(zr *zip.Reader, name string) ([]byte, error) {
	want := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(name, "./"), "\\"))
	var f *zip.File
	for _, e := range zr.File {
		got := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(e.Name, "./"), "\\"))
		if got == want {
			f = e
			break
		}
	}
	if f == nil {
		if un, err := url.PathUnescape(name); err == nil && un != name {
			return zipEntryBytes(zr, un)
		}
		return nil, fmt.Errorf("压缩包里没有 %s", name)
	}
	if f.UncompressedSize64 > novelZipEntryMax {
		return nil, fmt.Errorf("条目 %s 过大（%d 字节 > 上限 %d）", name, f.UncompressedSize64, novelZipEntryMax)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(rc, novelZipEntryMax)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// zipEntryBySuffix 找第一个以 suffix 结尾的条目（忽略大小写；多个取名字最小的，保证稳定）。
func zipEntryBySuffix(zr *zip.Reader, suffix string) string {
	best := ""
	for _, e := range zr.File {
		if !strings.HasSuffix(strings.ToLower(e.Name), suffix) {
			continue
		}
		if best == "" || e.Name < best {
			best = e.Name
		}
	}
	return best
}
