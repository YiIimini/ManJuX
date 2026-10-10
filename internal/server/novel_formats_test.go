// novel_formats_test.go 小说库**新增文本格式**（.docx / .epub）的门。
//
// 【为什么是这几条】真机那本书《离婚后，千亿前夫跪求复合》的正文是 8 个 .docx ——
// 平台原来只认 .md/.txt，于是书卡列 **0 章 / 0 字**、一键创作报「小说正文过短(0 字)」。
// 所以判据不能停在"函数认识 .docx"这一层，要一路钉到**消费方**：
// ① 格式判据（isNovelTextFile）认哪些、不认哪些；
// ② 抽出来的正文**能不能被章头识别/切章**（SplitChapters）—— 抽成「第」+「一章」就等于没抽；
// ③ 章节册清单（collectManuscript）要把 .docx 收进来并**按章号排**（一章一集的地基）；
// ④ 书卡/详情/阅读三个 HTTP 面要真的看得见（列 8 章、读出来是正文而不是 zip 二进制）；
// ⑤ 不支持的格式**逐条点名**，不许静默跳过；
// ⑥ 判据只有一个出处（谁再写第二份扩展名表就会红）。
package server

import (
	"archive/zip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ---------- 源码级判据的小工具 ----------

// codeLines 去掉**整行注释**（判据只许落在代码行上）。
// 第 43 轮栽过一次：源码扫描被我自己注释里那句 `comfyGen.GenerateImage(...)` 判红 ——
// "断言命中注释"与"断言命中代码"必须能分开。
func codeLines(src string) string {
	var out []string
	for _, ln := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

func mustServerSrc(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读 %s 失败：%v（测试的 cwd 应当是 internal/server）", name, err)
	}
	return string(b)
}

// mustRead 读一册正文（走唯一入口 readNovelTextFile）。
func mustRead(t *testing.T, path string) string {
	t.Helper()
	text, err := readNovelTextFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", path, err)
	}
	return text
}

// ---------- 夹具：按真实形态现造 .docx / .epub ----------

func writeZipFixture(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names) // map 遍历无序，固定写入顺序才能让夹具字节稳定
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entries[n])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

const docxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/></Types>`

// docxBody 照真机那份 word/document.xml 的关键形态写：
// 章头被切成多个 run（Word 的常态）、w:tab、w:br、`<w:del>` 里的修订删除文本、域代码。
const docxBody = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t>第</w:t></w:r><w:r><w:t>一章 签字，滚出顾家</w:t></w:r></w:p>
<w:p><w:r><w:t>林晚</w:t><w:tab/><w:t>把手里的笔放下。</w:t></w:r><w:r><w:br/><w:t>第二行。</w:t></w:r></w:p>
<w:p><w:del><w:r><w:delText>这句是修订删掉的</w:delText></w:r></w:del></w:p>
<w:p><w:r><w:instrText>PAGE</w:instrText></w:r><w:r><w:t>正文末段。</w:t></w:r></w:p>
</w:body></w:document>`

func docxFixture(t *testing.T, path, documentXML string) {
	t.Helper()
	writeZipFixture(t, path, map[string]string{
		"[Content_Types].xml": docxContentTypes,
		"word/document.xml":   documentXML,
	})
}

// epubFixture 造一个 epub：manifest 里**第 2 章声明在前**，而 spine 说第 1 章在前 ——
// 读出来必须是 spine 顺序（这是 EPUB 的正文顺序，不是 zip 顺序、也不是 manifest 顺序）。
func epubFixture(t *testing.T, path string) {
	t.Helper()
	writeZipFixture(t, path, map[string]string{
		"META-INF/container.xml": `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OEBPS/content.opf": `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>测试书</dc:title></metadata>
<manifest>
<item id="cover" href="cover.xhtml" media-type="application/xhtml+xml"/>
<item id="c2" href="text/ch2.xhtml" media-type="application/xhtml+xml"/>
<item id="c1" href="text/ch1.xhtml" media-type="application/xhtml+xml"/>
</manifest>
<spine><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`,
		"OEBPS/text/ch1.xhtml": `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>内页标题不该被当成正文</title><style>p{color:red}</style></head>
<body><h1>第一章 网线那头的百亿合同</h1><p>林晚把剧本摔在桌上。</p>
<script>var x = "脚本不该出现";</script>
<p>她&nbsp;没有回头。</p></body></html>`,
		"OEBPS/text/ch2.xhtml": `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body><h1>第二章 骗子的百亿身家</h1><p>顾城推门进来。</p></body></html>`,
		"OEBPS/cover.xhtml": `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body><p>封面页不在 spine 上，不该读进正文。</p></body></html>`,
	})
}

// ---------- ① 格式判据 ----------

func TestNovelTextFormatRecognition(t *testing.T) {
	for _, p := range []string{"a.txt", "a.text", "a.md", "a.markdown", "a.docx", "a.epub",
		"第一章 签字，滚出顾家.docx", "A.DOCX", "a.Epub"} {
		if !isNovelTextFile(p) {
			t.Errorf("%s 应当被认作正文册（.docx/.epub 是本轮新增的格式）", p)
		}
	}
	for _, p := range []string{"a.pdf", "a.doc", "a.rtf", "a.wps", "a.mobi", "a.azw3", "a.html", "a.htm",
		"README", "cover.png", "a.docx.bak", "全本.epub.txt.bak"} {
		if isNovelTextFile(p) {
			t.Errorf("%s 不该被认作正文册（.pdf/.doc 等是**永久边界**，见 novel_formats.go 的注释）", p)
		}
	}
}

// ---------- ② .docx 抽取：run 合并 + 段落 + 消费方可用 ----------

func TestDocxPlainTextMergesRunsAndKeepsParagraphs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "第一章 签字，滚出顾家.docx")
	docxFixture(t, p, docxBody)

	text, err := readNovelTextFile(p)
	if err != nil {
		t.Fatalf("读 .docx 失败：%v", err)
	}
	if !strings.HasPrefix(text, "第一章 签字，滚出顾家\n") {
		t.Errorf("章头必须**拼回一行**（Word 把一句话切成多个 run 是常态，中间插分隔符就认不出章头）：\n%q", firstLines(text, 2))
	}
	if !strings.Contains(text, "林晚 把手里的笔放下。") {
		t.Errorf("w:tab 应当是空格：%q", text)
	}
	if !strings.Contains(text, "放下。\n第二行。") {
		t.Errorf("w:br 应当是换行：%q", text)
	}
	if strings.Contains(text, "这句是修订删掉的") || strings.Contains(text, "PAGE") {
		t.Errorf("修订删除的文本与域代码（w:instrText）都不算正文：%q", text)
	}
	if strings.Contains(text, "<w:") || strings.Contains(text, "</w:") {
		t.Errorf("抽出来还必须是没有标签的纯文本：%q", text)
	}
	// 判据钉到**消费方**：抽出来的东西要能被章头识别/切章用（"像章头"不算数）。
	if got := countChapterHeads(text); got != 1 {
		t.Errorf("抽出的正文应有 1 个章头行，实得 %d（切块/覆盖核对/【本集原文】全依赖它）", got)
	}
	chaps := SplitChapters(text)
	if len(chaps) != 1 || chaps[0].Num != 1 || chaps[0].Title != "签字，滚出顾家" {
		t.Errorf("切章结果不对：%+v", chaps)
	}
}

// ---------- ③ .epub 抽取：spine 顺序 + 实体 + 剔 script/style/head ----------

func TestEpubPlainTextFollowsSpineOrder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "全本.epub")
	epubFixture(t, p)

	text, err := readNovelTextFile(p)
	if err != nil {
		t.Fatalf("读 .epub 失败：%v", err)
	}
	i1 := strings.Index(text, "第一章 网线那头的百亿合同")
	i2 := strings.Index(text, "第二章 骗子的百亿身家")
	if i1 < 0 || i2 < 0 {
		t.Fatalf("两章正文都该读出来：%q", text)
	}
	if i1 > i2 {
		t.Errorf("第 1 章必须排在第 2 章之前（按 OPF 的 spine 顺序，不是 manifest 声明顺序）：\n%q", text)
	}
	if strings.Contains(text, "封面页不在 spine 上") {
		t.Errorf("不在 spine 上的文档（封面/版权页）不该读进正文：%q", text)
	}
	if strings.Contains(text, "脚本不该出现") || strings.Contains(text, "color:red") {
		t.Errorf("script/style 里的内容不是正文：%q", text)
	}
	if strings.Contains(text, "内页标题不该被当成正文") {
		t.Errorf("<head> 里的 title 不该读进正文：%q", text)
	}
	if strings.Contains(text, "&nbsp;") || strings.Contains(text, "&") {
		t.Errorf("html 实体必须转成字符（&nbsp; → 不换行空格），不能原样漏出来：%q", text)
	}
	if !strings.Contains(text, "没有回头") {
		t.Errorf("实体所在的那句话也应当在：%q", text)
	}
	if strings.Contains(text, "<h1>") || strings.Contains(text, "<p>") {
		t.Errorf("不该残留标签：%q", text)
	}
	chaps := SplitChapters(text)
	if len(chaps) != 2 || chaps[0].Num != 1 || chaps[1].Num != 2 {
		t.Errorf("epub 也要能切出两章：%+v", chaps)
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "⏎")
}

// ---------- ④ 不支持的格式：报错要点名 ----------

func TestReadNovelTextFileRejectsUnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "全本.pdf")
	if err := os.WriteFile(p, []byte("%PDF-1.7 假装是 pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, err := readNovelTextFile(p)
	if err == nil {
		t.Fatalf(".pdf 现在读不了，必须返回错误（而不是把字节当正文回给调用方）：%q", text)
	}
	if !strings.Contains(err.Error(), ".pdf") || !strings.Contains(err.Error(), "不支持的文本格式") {
		t.Errorf("错误里要点名扩展名：%v", err)
	}
	for _, ext := range novelTextExts {
		if !strings.Contains(err.Error(), ext) {
			t.Errorf("错误里要带上**支持列表**（缺 %s）：%v", ext, err)
		}
	}
	if !isNovelDocExt(".pdf") {
		t.Error(".pdf 应当落在「平台不读的文档格式」清单里（它要被逐条点名）")
	}
}

// ---------- ⑤ 章节册清单：收 .docx + 按章号排 ----------

func TestCollectManuscriptAcceptsAndOrdersDocxChapters(t *testing.T) {
	book := t.TempDir()
	// 真机同形：没有「正文」目录，8 个中文数字章号的 .docx 平铺在书根；故意乱序创建。
	names := []string{
		"第八章 神秘代号S与带血的手表.docx", "第三章 全网直播与苏雅的真面目.docx",
		"第一章 签字，滚出顾家.docx", "第六章 废弃码头与恶魔契约.docx",
		"第二章 天价违约金与跪地求饶.docx", "第七章 慈善晚宴与致命陷阱.docx",
		"第五章 落魄重逢与那碗猪脚饭.docx", "第四章 豪门竹马与顾城的崩溃.docx",
	}
	for _, n := range names {
		docxFixture(t, filepath.Join(book, n), docxBody)
	}
	paths, n := collectManuscript(book)
	if n != len(names) {
		t.Fatalf(".docx 分章册应当全被收进来：期望 %d 册，实得 %d（%v）", len(names), n, paths)
	}
	if len(paths) != len(names) {
		t.Fatalf("册数不符：%v", paths)
	}
	if got := filepath.Base(paths[0]); got != "第一章 签字，滚出顾家.docx" {
		t.Errorf("第 1 册必须排在最前（按章号，不按目录序/字节序）：%v", paths)
	}
	if got := filepath.Base(paths[len(paths)-1]); got != "第八章 神秘代号S与带血的手表.docx" {
		t.Errorf("第 8 册必须排在最后：%v", paths)
	}
	// 书卡上的「几章几字」也要照 .docx 算（原来这里是 0 章 / 0 字）。
	s := newTestServer(t)
	it := s.scanNovelDir(book, "离婚后，千亿前夫跪求复合")
	if it.Chapters != len(names) || it.Chars <= 0 {
		t.Errorf("书卡统计不对：Chapters=%d Chars=%d（.docx 必须与 .md/.txt 同等对待）", it.Chapters, it.Chars)
	}
}

// ---------- ⑥ 全本优先：别被同目录的封面图顶掉 ----------

func TestFullBookFileSkipsNonTextEntries(t *testing.T) {
	book := t.TempDir()
	if err := os.MkdirAll(filepath.Join(book, "全本"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 排序上 "AAA-封面.jpg" 在最前：旧实现取 Glob 的 fbs[0] 就会整份跳过全本。
	if err := os.WriteFile(filepath.Join(book, "全本", "AAA-封面.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(book, "全本", "全本.md"), []byte("# 第一章 签约\n\n正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := fullBookFile(book); filepath.Base(got) != "全本.md" {
		t.Errorf("全本应当取到那个可读正文文件，实得 %q", got)
	}
	// 反面：全本目录里只有图 ⇒ 没有全本可用
	book2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(book2, "全本"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(book2, "全本", "cover.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := fullBookFile(book2); got != "" {
		t.Errorf("没有可读正文时应当返回空串，实得 %q", got)
	}
}

// ---------- ⑦ 不读的格式要逐条点名 ----------

func TestNovelUnsupportedReportNamesFiles(t *testing.T) {
	book := t.TempDir()
	for _, n := range []string{"第一章.pdf", "第二章.pdf", "第三章.doc"} {
		if err := os.WriteFile(filepath.Join(book, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 封面目录里的图/文档与正文无关，不该拿来吓用户
	if err := os.MkdirAll(filepath.Join(book, "封面"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(book, "封面", "封面样式.pdf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := novelUnsupportedReport(book, 3)
	for _, want := range []string{".pdf ×2", ".doc ×1", "第一章.pdf", novelTextExtsHint()} {
		if !strings.Contains(rep, want) {
			t.Errorf("点名文案里该有 %q，实得：%s", want, rep)
		}
	}
	if strings.Contains(rep, "封面样式.pdf") {
		t.Errorf("封面/ 目录里的文件不该算进正文格式点名：%s", rep)
	}
	// 反面：格式都在支持范围里 ⇒ 不许报（否则每本书都会挂一条没用的警告）
	ok := t.TempDir()
	if err := os.WriteFile(filepath.Join(ok, "第一章.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	docxFixture(t, filepath.Join(ok, "第二章.docx"), docxBody)
	if rep := novelUnsupportedReport(ok, 3); rep != "" {
		t.Errorf("全是可读格式时不该报任何东西，实得：%s", rep)
	}
}

// ---------- ⑧ HTTP 面：书卡列得出、详情列得出、阅读读得到 ----------

func TestNovelDocxBookIsListedAndReadable(t *testing.T) {
	s := newTestServer(t)
	if s.novelRoot() == "" {
		t.Fatal("测试服务器的小说库根目录为空，夹具不成立")
	}
	bookName := "离婚后，千亿前夫跪求复合"
	book := filepath.Join(s.novelRoot(), bookName)
	chaps := []string{
		"第一章 签字，滚出顾家.docx", "第二章 天价违约金与跪地求饶.docx",
		"第三章 全网直播与苏雅的真面目.docx", "第四章 豪门竹马与顾城的崩溃.docx",
		"第五章 落魄重逢与那碗猪脚饭.docx", "第六章 废弃码头与恶魔契约.docx",
		"第七章 慈善晚宴与致命陷阱.docx", "第八章 神秘代号S与带血的手表.docx",
	}
	for _, n := range chaps {
		docxFixture(t, filepath.Join(book, n), docxBody)
	}

	// ① 书卡：原来这里是 `chapters=0 / chars=0`（只认 .md/.txt 时的真实现象）
	code, out := doReq(t, s, "GET", "/api/novels", nil)
	if code != 200 {
		t.Fatalf("列表应 200，实际 %d", code)
	}
	item := findNovel(t, out, bookName)
	if item == nil {
		t.Fatalf("书卡里找不到 %s：%v", bookName, out["novels"])
	}
	if got := int(item["chapters"].(float64)); got != len(chaps) {
		t.Errorf("书卡章数应为 %d（8 个 .docx 分章），实得 %d", len(chaps), got)
	}
	// 字数必须是**真实正文数**：.docx 的字节数里大半是 XML 标签，
	// 按"3 字节/汉字"估会把这本书写成 2.3 倍（真机实测 40135 估 vs 17577 真）。
	perChapter := len([]rune(mustRead(t, filepath.Join(book, chaps[0]))))
	wantChars := perChapter * len(chaps)
	if got := int(item["chars"].(float64)); got != wantChars {
		t.Errorf("书卡字数应为每章 %d 字 ×%d 章 = %d（按解出来的正文数），实得 %d —— 用字节估就会虚高",
			perChapter, len(chaps), wantChars, got)
	}

	// ② 详情：8 个 .docx 要作为章节列出来
	_, det := doReq(t, s, "GET", "/api/novels/detail?path="+url.QueryEscape(book), nil)
	arr, _ := det["chapters"].([]any)
	if len(arr) != len(chaps) {
		t.Fatalf("详情应列出 %d 章，实得 %d：%v", len(chaps), len(arr), det["chapters"])
	}

	// ③ 阅读：读回来的必须是**正文**，不是 zip 二进制
	target := filepath.Join(book, chaps[0])
	code, r := doReq(t, s, "GET", "/api/novels/read?path="+url.QueryEscape(target), nil)
	if code != 200 {
		t.Fatalf("读 .docx 应 200，实际 %d（%v）", code, r)
	}
	text, _ := r["text"].(string)
	if strings.HasPrefix(text, "PK") || strings.Contains(text, "\x00") {
		t.Errorf("把 .docx 当二进制读了（zip 头/空字节）：%q", text)
	}
	if !strings.Contains(text, "第一章 签字，滚出顾家") {
		t.Errorf("读回来的不是这一章的正文：%q", text)
	}
	if int(r["chars"].(float64)) < 20 {
		t.Errorf("正文字数不对：%v", r["chars"])
	}

	// ④ 不支持的格式：400 + 说清支持哪些（不许把二进制回给阅读框）
	bad := filepath.Join(book, "第九章 未支持.pdf")
	if err := os.WriteFile(bad, []byte("%PDF-1.7"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, r = doReq(t, s, "GET", "/api/novels/read?path="+url.QueryEscape(bad), nil)
	if code != 400 {
		t.Errorf("不支持的格式应 400，实际 %d（%v）", code, r)
	}
	if msg, _ := r["error"].(string); !strings.Contains(msg, "平台可读") {
		t.Errorf("错误里要说清支持哪些格式：%v", r)
	}
}

func findNovel(t *testing.T, out map[string]any, name string) map[string]any {
	t.Helper()
	arr, _ := out["novels"].([]any)
	for _, it := range arr {
		m, _ := it.(map[string]any)
		if m != nil && m["name"] == name {
			return m
		}
	}
	return nil
}

// ---------- ⑨ 判据只有一个出处（谁再写第二份扩展名表就红） ----------

func TestNovelFormatJudgeHasSingleOwner(t *testing.T) {
	j := codeLines(mustServerSrc(t, "novel_formats.go"))
	if !strings.Contains(j, "func isNovelTextFile(") {
		t.Fatal("novel_formats.go 里找不到 isNovelTextFile —— 判据本体不见了")
	}
	if !strings.Contains(j, "func readNovelTextFile(") {
		t.Fatal("novel_formats.go 里找不到 readNovelTextFile —— 取正文入口不见了")
	}
	// 旧判据必须彻底消失（它的实现只认 .md/.txt，留着就会有人继续用）
	if strings.Contains(j, "hasTxtExt") {
		t.Error("novel_formats.go 里还留着旧判据 hasTxtExt")
	}
	for _, f := range []string{"novels.go", "novels_produce.go", "novel_format.go", "novels_text.go"} {
		src := mustServerSrc(t, f)
		if strings.Contains(codeLines(src), "hasTxtExt(") {
			t.Errorf("%s 还在调用旧判据 hasTxtExt（它只认 .md/.txt）", f)
		}
	}
	// 三个消费点都必须过新判据（少一处就会出现"这边认那边不认"）
	for _, f := range []string{"novels.go", "novels_produce.go", "novel_format.go"} {
		if !strings.Contains(codeLines(mustServerSrc(t, f)), "isNovelTextFile(") {
			t.Errorf("%s 没用唯一的格式判据 isNovelTextFile（「哪些算正文」必须只有一处口径）", f)
		}
	}
	// 三个读正文的地方都必须过统一入口（裸 os.ReadFile 会把 .docx 当二进制读回来）
	for _, f := range []string{"novels.go", "novels_produce.go"} {
		if !strings.Contains(codeLines(mustServerSrc(t, f)), "readNovelTextFile(") {
			t.Errorf("%s 取正文没有走 readNovelTextFile（按扩展名解码的唯一入口）", f)
		}
	}
}
