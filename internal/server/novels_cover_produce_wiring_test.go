// novels_cover_produce_wiring_test.go 「视频制作」与「封面重做」两条入口的门（2026-10-10）。
//
// 用户：「小说视频制作和封面重做也需要重新实现功能逻辑」。两条入口各有一个**真机撞得到**的洞：
//
//	① 封面重做：这本书**没有留档正向词**（`封面/pos-qwen21.txt`）时，
//	   `GET /api/novels/cover-config` 回 **400** ⇒ 弹窗根本打不开；而弹窗本身支持"直接写一份词
//	   再生成"（cover-redo 认 in.Prompt），候选对比视图里还专门写着「这本书还没有封面」
//	   （CoverCompare.vue）—— 也就是说 **既没有封面、又没有留档词** 恰恰是封面重做最该顶用的场合，
//	   却成了唯一走不通的场合（用户被卡在"先让技能端生成一次"那一步）。
//	② 视频制作：书没有封面图时 `applyCover` 里 `if cover == "" { return }` **静默跳过** ⇒
//	   一集集建出来全都没有封面，而用户完全不知道原因 —— 同一个平台上「脚本创作·包导入」那条
//	   入口在同样情形下是如实播报的（两条入口口径必须一致，见 novel_cover.go 的 missingCoverMsg）。
//
// 判据按 AGENTS.md 落在**可执行形态**上：HTTP 状态码、建出来的 source.txt、播报出去的事件。
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/project"
)

// cjkOutsideQuotesOf 词里**引号之外**的汉字数 —— 封面词的核心判据（与 project/prompt_cjk.go 的
// 官方 ON-SCREEN TEXT 口径同源：引号外的汉字会被模型当"要画的字"照抄；引号里才是"要画上去的字"）。
func cjkOutsideQuotesOf(prompt string) int {
	return project.CountCJK(coverPromptQuotedRe.ReplaceAllString(prompt, ""))
}

// fakeCoverLLM 起一个 OpenAI 兼容的假 LLM（正文由调用方给定；content=="" 表示让接口 500），
// 并把它写进本服务器的配置；返回"被请求了几次"的读取函数（用来证明"留档优先时不调 LLM"）。
func fakeCoverLLM(t *testing.T, s *Server, content string) func() int {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if content == "" {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}}})
	}))
	t.Cleanup(srv.Close)
	g := s.cfg.Get()
	g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout = srv.URL, "test-key", "cover-model", 30
	if err := s.cfg.Update(g); err != nil {
		t.Fatalf("写测试用 LLM 配置失败：%v", err)
	}
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

// docxChapterXML 造一册 .docx 的 document.xml：章头照真机形态**切成多个 run**
// （Word 的常态），正文里带一个只属于这一章的特征串，便于断言"读到的是这一章"。
func docxChapterXML(num int, title, marker string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t>第</w:t></w:r><w:r><w:t>` + fmt.Sprintf("%d章 %s", num, title) + `</w:t></w:r></w:p>
<w:p><w:r><w:t>` + marker + `</w:t></w:r></w:p>
<w:p><w:r><w:t>她把笔放下，转身往门口走。</w:t></w:r></w:p>
</w:body></w:document>`
}

// ① 没有留档正向词 ⇒ 弹窗照样能开（200 + 一份按书名推的默认词 + hint 说清来源）
func TestCoverConfigOpensWithoutArchivedPrompt(t *testing.T) {
	s := newTestServer(t)
	// 真机同形：书目录里只有正文，**没有 封面/ 目录**（那本书就是这样）
	book := filepath.Join(s.novelRoot(), "书K")
	writeFile(t, filepath.Join(book, "第一章 签字.docx"), []byte("x"))

	code, out := doReq(t, s, "GET", "/api/novels/cover-config?path="+urlEnc(book), nil)
	if code != 200 {
		t.Fatalf("这本书没有留档正向词，配置接口必须 200（弹窗要能开、用户要能直接写一份），实际 %d（%v）", code, out)
	}
	if out["applicable"] != true {
		t.Errorf("书目录应标 applicable=true，实际 %v", out["applicable"])
	}
	if out["has_prompt"] != false {
		t.Errorf("没有留档文件时 has_prompt 必须是 false，实际 %v", out["has_prompt"])
	}
	prompt, _ := out["prompt"].(string)
	if !strings.Contains(prompt, "书K") {
		t.Errorf("默认词里必须有书名（否则用户拿到一行不知道是谁的封面）：%q", prompt)
	}
	// 【2026-10-10 平台自己的图像侧口径】正文必须英文、**书名放进英文引号**（官方 ON-SCREEN TEXT：
	// 引号里的字才是"要画上去的字"，引号外的汉字会被模型当字形来源）。于是判据很干脆：
	// 引号之外**一个汉字都不许有**；引号里就是书名。
	if n := cjkOutsideQuotesOf(prompt); n != 0 {
		t.Errorf("封面词的引号外有 %d 个汉字 —— 那正是「会被画成字形」的来源（书名只许出现在引号里）：%q", n, prompt)
	}
	if !hasCoverTitle(prompt, "书K") {
		t.Errorf("书名必须放进引号（官方口径下「要画在画面上的字」的唯一写法）：%q", prompt)
	}
	// 【2026-10-10 用户「封面文字内容版本需要优化」】默认词里的"画面文字"口径改成**正向的唯一一处文字**
	// （`coverTextClause`）：不能说 no other text 这类否定式 —— 出图侧 cfg=1.0 **否定词不生效**，
	// 而提到的名词会被当成要画的东西（真机那张写着 no other text，书名下方照样多一行乱码伪字）。
	for _, want := range []string{"Vertical 3:4", "The only lettering anywhere in the image is the cover title"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("默认词里要有竖版封面口径与「只许书名一处文字」的正向口径（缺 %q）：%q", want, prompt)
		}
	}
	for _, bad := range []string{"No other text", "no other text", "no watermark", "no subtitles"} {
		if strings.Contains(prompt, bad) {
			t.Errorf("默认词里不许出现否定式文字条款（cfg=1.0 下不生效，还会把名词喂给模型）：%q", bad)
		}
	}
	if out["llm_ready"] != false {
		t.Errorf("测试服务器没配 LLM，llm_ready 应为 false（前端据此置灰「AI 按书名写词」），实际 %v", out["llm_ready"])
	}
	hint, _ := out["hint"].(string)
	for _, want := range []string{coverRedoPromptFile, "模板词", "AI"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint 要说清「这行词是哪来的 / 还能怎么做得更好」（缺 %q）：%q", want, hint)
		}
	}

	// 反面：有留档词 ⇒ 用留档词、has_prompt=true、不再提示"这是默认词"
	writeFile(t, filepath.Join(book, "封面", coverRedoPromptFile), []byte("A cinematic key visual of a river city"))
	_, out2 := doReq(t, s, "GET", "/api/novels/cover-config?path="+urlEnc(book), nil)
	if out2["has_prompt"] != true {
		t.Errorf("有留档文件时 has_prompt 必须是 true，实际 %v", out2["has_prompt"])
	}
	if p, _ := out2["prompt"].(string); p != "A cinematic key visual of a river city" {
		t.Errorf("有留档词时不许用默认词顶替：%q", p)
	}
	if h, _ := out2["hint"].(string); h != "" {
		t.Errorf("有留档词时不该再提示默认词：%q", h)
	}
}

// ② 默认词只能由**这本书自己声明过的东西**拼出来（喂 coverSeedPrompt 本体，不喂上游结果）
func TestCoverSeedPromptUsesBookStyle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "立项.json"),
		[]byte(`{"art_style":"DREAMLIKE CHINESE ARCHITECTURAL FANTASY"}`))
	got := coverSeedPrompt(dir, "离婚后，千亿前夫跪求复合")
	for _, want := range []string{"离婚后，千亿前夫跪求复合", "DREAMLIKE CHINESE ARCHITECTURAL FANTASY", "Vertical 3:4"} {
		if !strings.Contains(got, want) {
			t.Errorf("模板词缺 %q（封面要与正片同一口径，且必须给得出竖版口径）：\n%s", want, got)
		}
	}
	// 【2026-10-10 的平台口径，判据钉在这条上】引号外**一个汉字都不许有**：
	// 引号外的汉字会被模型当成"要画的字"照抄（上一版整句中文就是踩了这个）；
	// 引号里只有书名（官方 ON-SCREEN TEXT 通道）。
	if n := cjkOutsideQuotesOf(got); n != 0 {
		t.Errorf("模板词引号外有 %d 个汉字（会被画到封面上）：\n%s", n, got)
	}
	if !hasCoverTitle(got, "离婚后，千亿前夫跪求复合") {
		t.Errorf("书名必须在引号里：\n%s", got)
	}
	// 反面：这本书什么都没声明 ⇒ 仍然要给得出书名 + 竖版口径（不许空着手让用户对着空框发呆）
	got2 := coverSeedPrompt(t.TempDir(), "没声明风格的书")
	for _, want := range []string{"没声明风格的书", "Vertical 3:4"} {
		if !strings.Contains(got2, want) {
			t.Errorf("没有风格声明时模板词缺 %q：\n%s", want, got2)
		}
	}
	if n := cjkOutsideQuotesOf(got2); n != 0 {
		t.Errorf("没有风格声明时引号外也不许有汉字（实得 %d 个）：\n%s", n, got2)
	}
}

// ③ 散文件小说（小说库根下直接一个 .md/.docx）：config 如实标不可用，redo 明确 400
func TestCoverRedoRejectsLooseFileBook(t *testing.T) {
	s := newTestServer(t)
	f := filepath.Join(s.novelRoot(), "散文件小说.md")
	writeFile(t, f, []byte("# 第一章\n\n正文\n"))

	code, out := doReq(t, s, "GET", "/api/novels/cover-config?path="+urlEnc(f), nil)
	if code != 200 {
		t.Fatalf("配置接口对散文件书也应 200（把「为什么不支持」说清楚），实际 %d（%v）", code, out)
	}
	if out["applicable"] != false {
		t.Errorf("散文件书必须如实标 applicable=false（否则前端会打开一个注定失败的弹窗），实际 %v", out["applicable"])
	}
	if h, _ := out["hint"].(string); !strings.Contains(h, "单个文件") {
		t.Errorf("hint 要说清「是单个文件不行」：%q", h)
	}

	code, out = doReq(t, s, "POST", "/api/novels/cover-redo", map[string]any{"path": f, "prompt": "x"})
	if code != 400 {
		t.Errorf("散文件书的封面重做应 400（明确拒绝，不是 500），实际 %d（%v）", code, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "单个文件") {
		t.Errorf("要说清是**哪一类书**不行 —— 原来只报「书目录不存在」，而它明明就在那儿：%q", msg)
	}
}

// ④ 「这本书没有封面图」只有一句话、一个级别（两条入口共用）；播报要真的发出去
func TestMissingCoverMsgPointsAtBothWays(t *testing.T) {
	msg := missingCoverMsg("书L")
	for _, want := range []string{"书L", "封面重做", "<书目录>/封面"} {
		if !strings.Contains(msg, want) {
			t.Errorf("缺封面那句话缺 %q（要把两条可行路径都说出来）：%q", want, msg)
		}
	}
	s := newTestServer(t)
	s.noteMissingNovelCover("书L")
	evs, _ := s.bus.Recent(0)
	found := false
	for _, ev := range evs {
		if ev.Level == "warn" && strings.Contains(ev.Message, "书L") && strings.Contains(ev.Message, "没有封面图") {
			found = true
		}
	}
	if !found {
		t.Error("noteMissingNovelCover 必须真往总线上播一条 warn —— 判据只看事件，不看函数被没被调用")
	}
}

// ⑤ 端到端：视频制作把**一册一章的 .docx 书**建成 8 集，每集原文就是那一章；
// 这本书没有封面图 ⇒ 必须如实播报一句（原 applyCover 静默跳过的反向针）。
//
// 这条同时钉住两件事（都是用户点「视频制作」真会遇到的）：
//   - 上一轮的 .docx 支持要一路走到**建出来的 source.txt**（不只是"函数认识 .docx"）；
//   - 封面缺失不许静默。
func TestProduceDocxBookBuildsEpisodesAndReportsMissingCover(t *testing.T) {
	s := newTestServer(t)
	// 一章一档快路径本身不调 LLM（这正是它的意义），但拆集入口要求 LLM 已配置；
	// 给一份能过"已配置"检查的假配置（地址指向本机无用端口，全程不会被调用）。
	g := s.cfg.Get()
	g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model = "http://127.0.0.1:9/v1", "test-key", "test-model"
	if err := s.cfg.Update(g); err != nil {
		t.Fatalf("写入测试用 LLM 配置失败：%v", err)
	}

	// 真机同形：书名带全角逗号（safeSeries 会把它规整成「·」）、8 个中文数字章号的 .docx
	// 平铺在书根（没有「正文」目录）、**没有封面图**。
	const bookName = "离婚后，千亿前夫跪求复合"
	book := filepath.Join(s.novelRoot(), bookName)
	titles := []string{"签字滚出顾家", "天价违约金与跪地求饶", "全网直播与苏雅的真面目", "豪门竹马与顾城的崩溃",
		"落魄重逢与那碗猪脚饭", "废弃码头与恶魔契约", "慈善晚宴与致命陷阱", "神秘代号与带血的手表"}
	markers := make([]string, len(titles))
	for i, ti := range titles {
		markers[i] = fmt.Sprintf("本草正文特征串第%d号", i+1)
		docxFixture(t, filepath.Join(book, fmt.Sprintf("第%d章 %s.docx", i+1, ti)),
			docxChapterXML(i+1, ti, markers[i]))
	}

	code, out := doReq(t, s, "POST", "/api/novels/produce", map[string]any{"path": book})
	if code != 200 {
		t.Fatalf("视频制作应接受这本书（8 个 .docx 分章），实际 %d（%v）", code, out)
	}

	// 后台异步建集：轮询等到**正文真的落盘**为止（快路径是秒级的，最多等 20 秒）。
	//
	// 【为什么要等"写完了"而不是只等"目录出现了"（2026-10-11 修偶发红）】建集是**异步**的：
	// 目录先出现、`source.txt` 之后才写。原来只轮询到"8 个目录都在"就往下断言 ⇒ 最后建的那一集
	// 正文还没落盘时就被判"缺该章正文特征串"，全量自检里约 1/3 概率红（而单跑这条 30 遍全过 ——
	// 典型的**等待条件太松**）。现在等到**每一集的正文里都有它自己那一章的正文特征串**为止。
	series := safeSeries(bookName)
	var ids []string
	deadline := time.Now().Add(20 * time.Second)
	for {
		ids = ids[:0]
		for _, p := range s.store.List() {
			if strings.HasPrefix(p.ID, series+"·第") {
				ids = append(ids, p.ID)
			}
		}
		ready := len(ids) >= len(titles)
		if ready {
			for i := range titles {
				id := fmt.Sprintf("%s·第%02d集·%s", series, i+1, safeSeries(titles[i]))
				src, err := s.store.LoadSource(id)
				if err != nil || !strings.Contains(src, markers[i]) {
					ready = false // 这一集还在写：继续等（不是在下面判负）
					break
				}
			}
		}
		if ready || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// 集数本身也要判（8 个 .docx 分章应建成 8 集）—— 与上面的等待无关，是这条门原本的判据。
	if len(ids) != len(titles) {
		t.Fatalf("%d 个 .docx 分章应建成 %d 集，实得 %d：%v", len(titles), len(titles), len(ids), ids)
	}

	// 判据落在**建出来的 source.txt**：每集的【本集原文】必须是那一章（.docx 被真读进来了）
	for i := range titles {
		id := fmt.Sprintf("%s·第%02d集·%s", series, i+1, safeSeries(titles[i]))
		src, err := s.store.LoadSource(id)
		if err != nil {
			t.Fatalf("读不到第 %d 集的 source（%s）：%v", i+1, id, err)
		}
		if !strings.Contains(src, fmt.Sprintf("第%d章 %s", i+1, titles[i])) {
			t.Errorf("第 %d 集的【本集原文】里没有那一章的章头（.docx 没被读进来）：%q", i+1, headOf(src))
		}
		if !strings.Contains(src, markers[i]) {
			t.Errorf("第 %d 集原文缺该章正文特征串 %q —— 附上去的不是这一章", i+1, markers[i])
		}
		for j, other := range markers {
			if j != i && strings.Contains(src, other) {
				t.Errorf("第 %d 集的原文里混进了第 %d 章的内容（章界错位）", i+1, j+1)
			}
		}
	}

	// 没有封面图 ⇒ 必须有一句 warn（原来 applyCover 里 `if cover == "" { return }` 静默跳过）
	evs, _ := s.bus.Recent(0)
	missed := false
	for _, ev := range evs {
		if ev.Level == "warn" && strings.Contains(ev.Message, "没有封面图") && strings.Contains(ev.Message, bookName) {
			missed = true
		}
	}
	if !missed {
		t.Error("这本书没有封面图，视频制作必须如实播报一句 —— 否则一集集建出来全都没封面，用户无从知道原因")
	}
}

// headOf 取一段文本的前 60 个 rune（报错时够看出问题）。
func headOf(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}

// ⑥ 两条入口都必须真的调用共用那句（口径只许有一份；少一处就退回"一条报一条哑"）
func TestBothCoverEntriesShareOneMessage(t *testing.T) {
	cover := codeLines(mustServerSrc(t, "novel_cover.go"))
	if !strings.Contains(cover, "s.noteMissingNovelCover(") {
		t.Error("applyNovelCover（脚本创作·包导入那条入口）没有调用共用的缺封面播报")
	}
	prod := codeLines(mustServerSrc(t, "novels_produce.go"))
	if !strings.Contains(prod, "s.noteMissingNovelCover(") {
		t.Error("handleNovelProduce（一键视频创作那条入口）没有调用共用的缺封面播报 —— " +
			"原来这里是 `if cover == \"\" { return }` 静默跳过")
	}
	if len(strings.Split(prod, "noteMissingNovelCover(")) > 2 {
		t.Error("缺封面那句话只许报一次：视频制作这条入口里出现了多处播报（回调每次都会调 applyCover）")
	}
}

// ⑦ 前端接线：散文件书置灰**且置灰原因写在 tip 气泡里**、后端那句 hint 要显示、少一层兜底。
//
// 【2026-10-10 用户】「显示『（后端 cover-redo 会回 400…）-->』干嘛？鼠标移动上去在 tip 气泡里
// 显示不行吗？」—— 那次是我改注释时把 `<!--` 开头那行删掉了，说明文字**渲染到了界面上**
// （`vite build` 不报错）。所以这条门除了"接线在不在"，还钉"给人看的解释写在 title 气泡里"：
// 注释成对由 `frontend_comment_balance_test.go` 单独管。
func TestNovelCoverRedoFrontendWiring(t *testing.T) {
	vue := readFrontend(t, "src/views/NovelsView.vue")
	for _, want := range []string{
		`:disabled="n.type === 'file'"`, // 散文件小说没有 封面/ 目录 ⇒ 菜单项置灰
		`if (c.applicable === false) {`, // 兜底：不打开一个"点了生成必然失败"的弹窗
		`{{ covMeta.hint }}`,            // 后端那句 hint（这行词是哪来的）要显示出来
		`covMeta?.has_prompt === false`, // 用户要能看出手上这行是"平台推的默认词"
	} {
		if !strings.Contains(vue, want) {
			t.Errorf("封面重做前端缺少接线：%s", want)
		}
	}
	// **两个**封面入口都要在气泡里说清"为什么点不动"（用户：「鼠标移动上去在 tip 气泡里显示不行吗？」）。
	// 用计数而不是 Contains：只改一个、留另一个，Contains 照样绿（同型教训：判据要能咬住"少做一半"）。
	for _, pair := range []struct {
		snippet string
		want    int
	}{
		{`:title="n.type === 'file'`, 2}, // 置灰时给原因的三元
		{`'封面只对书目录可用`, 2},                // 那句话本身
	} {
		if n := strings.Count(vue, pair.snippet); n != pair.want {
			t.Errorf("封面入口的「置灰原因写在 tip 气泡里」应出现 %d 次，实际 %d 次：%s",
				pair.want, n, pair.snippet)
		}
	}
}

// ⑧ 真机探针（默认跳过）：盘上那本书现在打开「封面重做」会拿到什么默认词。
//
// 这本书**没有 封面/ 目录、没有留档正向词、立项.json 也没有风格字段** —— 恰恰是"最需要封面重做"
// 的那种书。改之前它连弹窗都开不出来（config 400）；现在至少给得出书名 + 竖版封面口径。
// 跑法：$env:MANJUX_LIVE_PROBE=1; go test ./internal/server/ -run TestZZProbeCoverSeedLive -v
func TestZZProbeCoverSeedLive(t *testing.T) {
	if os.Getenv("MANJUX_LIVE_PROBE") != "1" {
		t.Skip("设 MANJUX_LIVE_PROBE=1 才跑（读盘上那本书）")
	}
	book := strings.TrimSpace(os.Getenv("MANJUX_NOVEL_PROBE_DIR"))
	if book == "" {
		book = filepath.Join("..", "..", "data", "小说", "离婚后，千亿前夫跪求复合")
	}
	if fi, err := os.Stat(book); err != nil || !fi.IsDir() {
		t.Skipf("盘上没找到这本书（%v）：%s", err, book)
	}
	st := scanNovelStyle(book)
	t.Logf("本书风格字段：Found=%v Field=%q Card=%q", st.Found(), st.Field, st.Card)
	seed := coverSeedPrompt(book, filepath.Base(book))
	t.Logf("封面默认词（%d 字）：%s", len([]rune(seed)), seed)
	if !strings.Contains(seed, filepath.Base(book)) {
		t.Errorf("默认词里必须有书名：%q", seed)
	}
}

// ⑨ 数据竞争回归守卫：异步拆集的 goroutine **不许改写 handler 的局部变量 name**。
//
// 真检测器是八门第 5 道（`go test -race`）—— 2026-10-10 就是它咬出这一条的（告警指向
// `handleNovelProduce.func2` 的写 vs `handleNovelProduce` 的读）。这条是**机械回归守卫**：
// 谁把 `name := safeSeries(name)` 改回 `name = safeSeries(name)`，这里立刻红，
// 不必等 -race 恰好抽到那个调度（那种"偶发"正是最难查的一类）。
//
// 判据只看**代码行**（`codeLines`）：上面这段注释里就写着那个坏写法，拿全文比会被自己的注释判红
// —— 第 43 轮栽过一次。
func TestProduceGoroutineDoesNotWriteCapturedName(t *testing.T) {
	src := codeLines(mustServerSrc(t, "novels_produce.go"))
	if !strings.Contains(src, "name := safeSeries(name)") {
		t.Error("视频制作的异步 goroutine 里必须用 `name := safeSeries(name)`（局部遮蔽）—— " +
			"写回外层的 name 会与 handler 随后拼的响应文案并发读写同一个变量（-race 门已咬出过一次）")
	}
	if strings.Contains(src, "name = safeSeries(name)") {
		t.Error("`name = safeSeries(name)` 就是那个数据竞争的写法，不许回来")
	}
}

// ⑩ 封面词的**唯一出处**：留档 > **平台 LLM 按书名写** > 模板兜底。
//
// 【2026-10-10 用户「平台有LLM功能啊」】上一轮我拿模板句当封面词，用户当场指出平台有 LLM。
// 这条门钉的就是"词到底是谁写的"，三档各有一次断言，且**降级必须体现在 From/Note 里**。
func TestCoverPromptForPrefersArchivedThenLLM(t *testing.T) {
	s := newTestServer(t)
	book := filepath.Join(s.novelRoot(), "书M")
	writeFile(t, filepath.Join(book, "第一章.docx"), []byte("x"))
	calls := fakeCoverLLM(t, s,
		`A lone woman in a rain-soaked neon alley, vertical 3:4 poster key visual, dramatic rim light, highly detailed. `+
			`The cover title text "《书M》" typeset clearly on the poster. No other text, no subtitles, no watermark.`)

	// ① 有留档 ⇒ 用留档，且**一次 LLM 都不调**（技能端写过一版就尊重那一版）
	writeFile(t, filepath.Join(book, "封面", coverRedoPromptFile), []byte("A cinematic key visual of a river city"))
	cp := s.coverPromptFor(book)
	if cp.Prompt != "A cinematic key visual of a river city" || !strings.Contains(cp.From, "留档") {
		t.Errorf("有留档词时应原样用它：%+v", cp)
	}
	if n := calls(); n != 0 {
		t.Errorf("有留档词时不该调 LLM（白烧钱还改了技能端的口径），实际调了 %d 次", n)
	}

	// ② 没有留档 ⇒ **平台 LLM 按书名写**（这是用户点的那条路）
	if err := os.Remove(filepath.Join(book, "封面", coverRedoPromptFile)); err != nil {
		t.Fatal(err)
	}
	cp = s.coverPromptFor(book)
	if !strings.Contains(cp.From, "LLM") {
		t.Errorf("没有留档词时应由平台 LLM 写（From 要如实说是谁写的）：%+v", cp)
	}
	if !strings.Contains(cp.Prompt, "书M") || !hasCoverTitle(cp.Prompt, "书M") {
		t.Errorf("AI 写的词里必须有引号里的书名：%q", cp.Prompt)
	}
	if n := calls(); n != 1 {
		t.Errorf("该正好调一次 LLM，实际 %d 次", n)
	}
}

// ⑪ LLM 写的词**平台侧收口**：引号外的汉字必须清掉（官方 ON-SCREEN TEXT 口径），
// 而引号里的书名留着 —— 清掉几个字也要如实写进 Note。
func TestCoverPromptForStripsCJKOutsideQuotes(t *testing.T) {
	s := newTestServer(t)
	book := filepath.Join(s.novelRoot(), "书N")
	writeFile(t, filepath.Join(book, "第一章.docx"), []byte("x"))
	fakeCoverLLM(t, s,
		`A cinematic vertical 3:4 poster key visual, rain-soaked neon alley, dramatic rim light. `+
			`这是说明：主角站在雨里（这几个字不该出现在画面上）。 `+
			`The cover title text "《书N》" typeset clearly on the poster. No other text, no watermark.`)
	cp := s.coverPromptFor(book)
	if n := cjkOutsideQuotesOf(cp.Prompt); n != 0 {
		t.Errorf("引号外的汉字必须清掉（模型会把它当字形画出来），实得 %d 个：%q", n, cp.Prompt)
	}
	if !hasCoverTitle(cp.Prompt, "书N") {
		t.Errorf("引号里的书名不许被清掉：%q", cp.Prompt)
	}
	// 【这条是**反向验证逼出来的**】只看"引号里还有书名"是不够的：把引号内的书名也清掉，
	// 平台会**自己补一句**书名标题，于是上面两条断言照样绿（判据挂在"会被下游补写的数据"上，
	// 正是 AGENTS.md 说的摆设守卫形态）。夹具里模型**明明给了**引号书名，所以：
	// Note 里**不许**出现"平台补了一句" —— 出现就说明模型给的那份书名没被留住。
	if strings.Contains(cp.Note, "补了一句") {
		t.Errorf("模型已经给了引号里的书名，平台不该再补（说明原句被清掉了）：%+v", cp)
	}
	if !strings.Contains(cp.Note, "清掉") {
		t.Errorf("清掉了几个字要如实写在 Note 里（不许静默改模型的输出）：%+v", cp)
	}
}

// ⑫ LLM 给不出可用结果 ⇒ **模板兜底 + 如实标注**（两条路径各一条：清洗后不成句 / 调用失败）。
func TestCoverPromptForFallsBackWithHonestNote(t *testing.T) {
	s := newTestServer(t)
	book := filepath.Join(s.novelRoot(), "书O")
	writeFile(t, filepath.Join(book, "第一章.docx"), []byte("x"))
	fakeCoverLLM(t, s, "这是一整段中文说明，平台应当判它不可用并走兜底模板，而不是把空壳送去出图。")
	cp := s.coverPromptFor(book)
	if !strings.Contains(cp.From, "模板") {
		t.Errorf("整段中文被洗成空壳 ⇒ 必须走兜底模板：%+v", cp)
	}
	if !strings.Contains(cp.Note, "不成句") {
		t.Errorf("Note 要说清为什么没用 LLM 的结果：%+v", cp)
	}
	if n := cjkOutsideQuotesOf(cp.Prompt); n != 0 {
		t.Errorf("兜底模板同样要守引号规矩：%q", cp.Prompt)
	}

	// 另一条：LLM 调用直接失败（未配置 / 500）
	s2 := newTestServer(t)
	book2 := filepath.Join(s2.novelRoot(), "书P")
	writeFile(t, filepath.Join(book2, "第一章.docx"), []byte("x"))
	cp2 := s2.coverPromptFor(book2) // 该服务器**没配 LLM**
	if !strings.Contains(cp2.From, "模板") || !strings.Contains(cp2.Note, "未配置") {
		t.Errorf("LLM 没配时也要给出模板 + 说清原因：%+v", cp2)
	}
}

// ⑬ 「AI 按书名写词」接口：**强制走 LLM**（哪怕技能端留过档 —— 用户点它就是要一份新写的）；
// 拿不到就回模板 + 说清（200，不报 500：用户要的是"一行能用的词"）。
func TestCoverPromptEndpointForcesLLM(t *testing.T) {
	s := newTestServer(t)
	book := filepath.Join(s.novelRoot(), "书Q")
	writeFile(t, filepath.Join(book, "封面", coverRedoPromptFile), []byte("ARCHIVED PROMPT"))
	llmWord := `Vertical 3:4 poster key visual for the Chinese short drama "《书Q》", single hero subject, ` +
		`dramatic cinematic lighting, highly detailed. The cover title text "《书Q》" typeset on the poster. No other text.`
	fakeCoverLLM(t, s, llmWord)

	code, out := doReq(t, s, "POST", "/api/novels/cover-prompt", map[string]any{"path": book})
	if code != 200 {
		t.Fatalf("AI 写词应 200，实际 %d（%v）", code, out)
	}
	if out["from"] != "llm" {
		t.Errorf("这个接口的语义就是「让 AI 写」，即使有留档也要走 LLM：%v", out)
	}
	if p, _ := out["prompt"].(string); !strings.Contains(p, "dramatic cinematic lighting") {
		t.Errorf("该回 LLM 写的那份词，实际 %q", p)
	}

	// 反面：LLM 挂了 ⇒ 200 + 模板 + 说清（不是 500）
	s2 := newTestServer(t)
	book2 := filepath.Join(s2.novelRoot(), "书R")
	writeFile(t, filepath.Join(book2, "第一章.docx"), []byte("x"))
	fakeCoverLLM(t, s2, "") // content=="" ⇒ 假 LLM 回 500
	code, out2 := doReq(t, s2, "POST", "/api/novels/cover-prompt", map[string]any{"path": book2})
	if code != 200 || out2["from"] != "template" {
		t.Fatalf("LLM 挂了也要给人一行能改的词（200 + template），实际 %d（%v）", code, out2)
	}
	if note, _ := out2["note"].(string); !strings.Contains(note, "LLM 没用上") {
		t.Errorf("Note 要说清 LLM 为什么没用上：%q", note)
	}
}

// ⑭ 「以小说名称直接创作封面」的落地函数：没有引擎时**报错且不动现封面**
// （一键出图最容易写出的缺陷就是"失败还把人家的封面覆盖掉"）。
func TestSeedCoverFromTitleWithoutEngineKeepsExistingCover(t *testing.T) {
	s := newTestServer(t)
	book := filepath.Join(s.novelRoot(), "书S")
	writeFile(t, filepath.Join(book, "封面", "封面.png"), []byte("OLD-COVER"))
	p, err := s.seedCoverFromTitle(book)
	if err == nil {
		t.Fatalf("没有引擎时该如实报错，实际返回 %q", p)
	}
	if !strings.Contains(err.Error(), "ComfyUI") {
		t.Errorf("报错要说清是引擎不可用：%v", err)
	}
	if got := string(readFile(t, filepath.Join(book, "封面", "封面.png"))); got != "OLD-COVER" {
		t.Errorf("失败不许动现封面，实际变成了 %q", got)
	}
	if _, err := os.Stat(filepath.Join(book, "封面", coverCandidatesDir)); err == nil {
		t.Error("没有引擎时不该在书目录里造候选目录（引擎检查在创建目录之前）")
	}
}

// ⑮ 两条一键入口的接线（源码级，只看代码行）：视频制作要真的调用它、且默认开；
// 前端要有「按书名出封面」菜单项与视频制作里的复选框。
func TestTitleCoverIsWiredIntoProduceAndMenu(t *testing.T) {
	prod := codeLines(mustServerSrc(t, "novels_produce.go"))
	for _, want := range []string{"AutoCover *bool", "autoCover :=", "s.seedCoverFromTitle(in.Path)"} {
		if !strings.Contains(prod, want) {
			t.Errorf("视频制作缺「书没有封面时按书名出一张」的接线：%s", want)
		}
	}
	vue := readFrontend(t, "src/views/NovelsView.vue")
	for _, want := range []string{
		`command="cover-title"`,      // 菜单里的一键入口
		"quickCoverFromTitle(",       // 它的实现（调 cover-redo 出候选）
		"AI 按书名写词",                   // 弹窗里让 LLM 写词
		"aiCoverPrompt(",             // 它的接线
		`v-model="produceAutoCover"`, // 视频制作对话框里的复选框
		"produceAutoCover.value",     // 它真的被传给了后端
	} {
		if !strings.Contains(vue, want) {
			t.Errorf("封面「按书名」这条路的前端接线缺：%s", want)
		}
	}
	js := readFrontend(t, "src/store.js")
	for _, want := range []string{"auto_cover: autoCover !== false", "novelCoverPrompt:"} {
		if !strings.Contains(js, want) {
			t.Errorf("store.js 缺接线：%s", want)
		}
	}
}

// ⑯ 真机探针（默认跳过）：拿**真 settings.json 里的平台 LLM**，给盘上那本书写一份封面词，
// 看真模型守不守平台自己的口径（英文正文 / 引号里的书名 / 引号外无汉字）。
//
// 跑法：$env:MANJUX_LIVE_PROBE=1; go test ./internal/server/ -run TestZZProbeCoverPromptLive -v
// （会真的调一次平台 LLM；书目录可用 $env:MANJUX_NOVEL_PROBE_DIR 指定，配置可用 $env:MANJUX_SETTINGS 指定）
func TestZZProbeCoverPromptLive(t *testing.T) {
	if os.Getenv("MANJUX_LIVE_PROBE") != "1" {
		t.Skip("设 MANJUX_LIVE_PROBE=1 才跑（会真的调一次平台 LLM）")
	}
	book := strings.TrimSpace(os.Getenv("MANJUX_NOVEL_PROBE_DIR"))
	if book == "" {
		book = filepath.Join("..", "..", "data", "小说", "离婚后，千亿前夫跪求复合")
	}
	if fi, err := os.Stat(book); err != nil || !fi.IsDir() {
		t.Skipf("盘上没找到这本书（%v）：%s", err, book)
	}
	settings := strings.TrimSpace(os.Getenv("MANJUX_SETTINGS"))
	if settings == "" {
		settings = filepath.Join("..", "..", "data", "settings.json")
	}
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Skipf("读不到真机 settings.json（%v）：%s", err, settings)
	}
	var real struct {
		LLM struct {
			BaseURL  string `json:"base_url"`
			APIKey   string `json:"api_key"`
			Model    string `json:"model"`
			TimeoutS int    `json:"timeout_seconds"`
		} `json:"llm"`
	}
	if err := json.Unmarshal(raw, &real); err != nil {
		t.Fatalf("解析 settings.json 失败：%v", err)
	}
	s := newTestServer(t)
	g := s.cfg.Get()
	g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model = real.LLM.BaseURL, real.LLM.APIKey, real.LLM.Model
	if real.LLM.TimeoutS > 0 {
		g.LLM.Timeout = real.LLM.TimeoutS
	}
	if err := s.cfg.Update(g); err != nil {
		t.Fatalf("写入真机 LLM 配置失败：%v", err)
	}
	// 只测 LLM 那一档（跳过留档：真机那本书本来就没有 封面/pos-qwen21.txt）
	t.Logf("书：%s（LLM=%s / %s）", filepath.Base(book), real.LLM.BaseURL, real.LLM.Model)
	start := time.Now()
	prompt, note, err := s.llmCoverPrompt(book)
	if err != nil {
		t.Fatalf("真机 LLM 写封面词失败（%.1fs）：%v", time.Since(start).Seconds(), err)
	}
	t.Logf("耗时 %.1fs；引号外汉字 %d 个；Note=%q\n封面词（%d 字）：%s",
		time.Since(start).Seconds(), cjkOutsideQuotesOf(prompt), note, len([]rune(prompt)), prompt)
	if n := cjkOutsideQuotesOf(prompt); n != 0 {
		t.Errorf("真模型给的词里引号外还有 %d 个汉字（会被画成字形）—— 收口没生效？", n)
	}
	if !hasCoverTitle(prompt, filepath.Base(book)) {
		t.Errorf("真模型给的词里没有引号里的书名（平台该补一句，检查收口）：%q", prompt)
	}
}
