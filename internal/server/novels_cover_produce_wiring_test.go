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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	if !strings.Contains(prompt, "不出现任何文字") {
		t.Errorf("默认词里要有竖版封面口径（留标题位、不画字）：%q", prompt)
	}
	hint, _ := out["hint"].(string)
	for _, want := range []string{coverRedoPromptFile, "默认词"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint 要说清「这行词是哪来的」（缺 %q）：%q", want, hint)
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
	for _, want := range []string{"离婚后，千亿前夫跪求复合", "DREAMLIKE CHINESE ARCHITECTURAL FANTASY", "不出现任何文字"} {
		if !strings.Contains(got, want) {
			t.Errorf("默认词缺 %q（封面要与正片同一口径，且必须给得出构图口径）：\n%s", want, got)
		}
	}
	// 反面：这本书什么都没声明 ⇒ 仍然要给得出书名 + 构图口径（不许空着手让用户对着空框发呆）
	got2 := coverSeedPrompt(t.TempDir(), "没声明风格的书")
	for _, want := range []string{"没声明风格的书", "不出现任何文字"} {
		if !strings.Contains(got2, want) {
			t.Errorf("没有风格声明时默认词缺 %q：\n%s", want, got2)
		}
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

	// 后台异步建集：轮询等它出现（快路径是秒级的，最多等 20 秒）
	series := safeSeries(bookName)
	var ids []string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ids = ids[:0]
		for _, p := range s.store.List() {
			if strings.HasPrefix(p.ID, series+"·第") {
				ids = append(ids, p.ID)
			}
		}
		if len(ids) >= len(titles) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(ids) != len(titles) {
		t.Fatalf("8 个 .docx 分章应建成 8 集，实得 %d：%v", len(ids), ids)
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

// ⑦ 前端接线：散文件书置灰、后端那句 hint 要显示、少一层兜底。
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
