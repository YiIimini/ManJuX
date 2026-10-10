// cover_text_clause_test.go 「封面文字」这一轮（2026-10-10）的门。
//
// 用户原话：「封面文字内容版本需要优化」。口径当场定为**封面上只许有书名一处文字**。
//
// 先取证再改（不是口味）：真机那张封面（`data/小说/离婚后，千亿前夫跪求复合/封面/封面.png`，
// 16:16 生成）用的词里明明写着 `no other text`，出图仍在书名下方多画了一行乱码伪字 ——
// 平台自己的 VLM 原话：「主标题：《离婚后，千亿前夫跪求复合》；下方小字：乱码/糊成一团」
// 「7) 不能：主标题正确清晰，但底部乱码小字必须删掉或重做后才可用」。
// 根因：出图侧 cfg=1.0，**否定词不生效**，而提到的名词会被当成要画的东西。
//
// 于是这一轮改两处、各配门：
//
//	① 正向词那侧：画面文字收成**一处口径**（coverTextClause），LLM 的输出由平台收口成那一句；
//	② 出图那侧：平台视觉模型**看图判一次**，不合格（书名之外还有字形 / 书名没认全）就重出或如实播报。
//
// 判据都直接喂**闸门本体**（coverTextClause / coverSeedPrompt / cleanCoverPrompt / judgeCoverText /
// coverTextRerollDecision），不喂"上游已经过滤过的结果" —— 否则就是摆设守卫（AGENTS.md）。
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// fakeCoverVLM 起一个假的**视觉模型**（OpenAI 兼容），内容由调用方给定；返回被请求次数。
// 与 fakeCoverLLM 同一手法：判据只看"平台自己读到了什么、怎么处置"。
func fakeCoverVLM(t *testing.T, s *Server, content string) func() int {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}}})
	}))
	t.Cleanup(srv.Close)
	g := s.cfg.Get()
	g.VLM.BaseURL, g.VLM.APIKey, g.VLM.Model = srv.URL, "test-key", "vlm-cover-model"
	if err := s.cfg.Update(g); err != nil {
		t.Fatalf("写测试用 VLM 配置失败：%v", err)
	}
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

// ① 画面文字口径本体：**正向陈述 + 书名在英文引号里 + 一个否定词都不许有**。
//
// 直接喂 coverTextClause：判据挂在"平台自己写的那句话"上，改坏它这条门就红。
func TestCoverTextClauseIsPositiveAndQuotesTitle(t *testing.T) {
	clause := coverTextClause("书X")

	if !strings.Contains(clause, "The only lettering anywhere in the image is the cover title") {
		t.Errorf("口径必须是「这一处是唯一的一处文字」的正向陈述：%q", clause)
	}
	if !strings.Contains(clause, `"《书X》"`) {
		t.Errorf("书名必须放在**英文双引号**里（官方 ON-SCREEN TEXT：引号里的字才是要画上去的字）：%q", clause)
	}
	if n := cjkOutsideQuotesOf(clause); n != 0 {
		t.Errorf("引号外不许有汉字（会被模型当字形画出来），实得 %d 个：%q", n, clause)
	}
	// **否定式一个都不许有**：cfg=1.0 下它们不生效，还会把 subtitle/watermark 这些名词喂进模型 ——
	// 真机那张封面的乱码小字就是这么来的。
	for _, bad := range []string{"no other", "no subtitle", "no watermark", "without", "subtitle", "watermark", "caption", "logo", "signature"} {
		if strings.Contains(strings.ToLower(clause), bad) {
			t.Errorf("口径里不许出现 %q（否定词不生效；名词会被画出来）：%q", bad, clause)
		}
	}
}

// ② 模板默认词（喂 coverSeedPrompt 本体）：**恰有一处**书名，且就是 coverTextClause 那一句。
//
// 旧版这里写的是 `The cover title text "《书名》" typeset clearly on the poster. No other text,
// no subtitles, no watermark, no signature.` —— 真机出图照样多一行乱码伪字。
func TestCoverSeedPromptHasExactlyOneTextClause(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "立项.json"), []byte(`{"art_style":"COLD MARBLE URBAN DRAMA"}`))
	got := coverSeedPrompt(dir, "书X")

	if n := strings.Count(got, "《书X》"); n != 1 {
		t.Errorf("画面文字只许有一处书名（书名出现 %d 次 ⇒ 模型可能画两处）：\n%s", n, got)
	}
	if !strings.Contains(got, coverTextClause("书X")) {
		t.Errorf("默认词的画面文字必须逐字用 coverTextClause（唯一出处，别处再拼一遍就会两处口径）：\n%s", got)
	}
	if !strings.Contains(got, "COLD MARBLE URBAN DRAMA") {
		t.Errorf("本书声明过的风格串要留在词里（封面与正片同一口径）：\n%s", got)
	}
	if n := cjkOutsideQuotesOf(got); n != 0 {
		t.Errorf("引号外不许有汉字，实得 %d 个：\n%s", n, got)
	}
	for _, bad := range []string{"no other text", "No other text", "no watermark"} {
		if strings.Contains(got, bad) {
			t.Errorf("默认词里不许再出现否定式文字条款（真机就是它导致乱码小字）：%q", bad)
		}
	}
}

// ③ 平台收口（喂 cleanCoverPrompt 本体）：模型/留档自己写的画面文字句整段摘掉，
// 换成 coverTextClause；**画面描述不能跟着丢**；书名只留一处；幂等。
//
// 夹具照真机那份词的形态写（`/封面/.cover-meta.json` 里存着它）。
func TestCleanCoverPromptEnforcesOneTextClause(t *testing.T) {
	raw := `Vertical 3:4 poster cover, single dominant visual centered slightly high. ` +
		`A poised 24-year-old Chinese woman in a sleek black suit stands in a cold marble penthouse living room, ` +
		`scattered red banknotes at her feet, low-key cinematic lighting, shallow depth of field, ultra-detailed. ` +
		`The cover title text "《书Y》" typeset on the poster in the lower third, elegant golden serif lettering ` +
		`on a dark clean background. no other text, no subtitles, no watermark, no signature.`
	got, note, ok := cleanCoverPrompt(raw, "书Y")
	if !ok {
		t.Fatalf("这份词清洗后应当可用，实际被判不可用（note=%q）", note)
	}
	// 画面描述必须留下（不能因为"这句里提到了书名"就整句摘掉）
	for _, want := range []string{"marble penthouse", "low-key cinematic lighting", "scattered red banknotes"} {
		if !strings.Contains(got, want) {
			t.Errorf("摘画面文字句时把画面描述也丢了（缺 %q）：%q", want, got)
		}
	}
	// 画面文字：**恰一处**，且就是平台那一句
	if n := strings.Count(got, "《书Y》"); n != 1 {
		t.Errorf("书名只许出现一处（实际 %d 处 —— 两处会让模型画两处文字）：%q", n, got)
	}
	if !strings.HasSuffix(got, coverTextClause("书Y")) {
		t.Errorf("画面文字条款必须收成平台那一句（唯一出处）：%q", got)
	}
	for _, bad := range []string{"no other text", "no subtitles", "no watermark", "no signature", "typeset"} {
		if strings.Contains(got, bad) {
			t.Errorf("收口后不许残留模型写的画面文字（%q）：%q", bad, got)
		}
	}
	if !strings.Contains(note, "摘掉") {
		t.Errorf("摘掉了东西必须如实写进 Note（不许静默改模型的输出）：%q", note)
	}
	// **幂等**：再收一次不许变（否则"重试/二次收口"会越收越走样）
	again, _, ok2 := cleanCoverPrompt(got, "书Y")
	if !ok2 || again != got {
		t.Errorf("收口必须幂等：\n一次=%q\n二次=%q", got, again)
	}
}

// ③b 收口的**反面针**：模型只提到书名、没讲画面上的字时，句子要留着（只抹书名那一处）。
//
// 这条是设计过程中被老门咬出来的（`TestCoverPromptEndpointForcesLLM` 断言词里必须留着
// `dramatic cinematic lighting`）：第一版把"含书名的整句"一起摘掉，构图/打光全丢。
func TestEnforceCoverTextKeepsSceneSentenceMentioningTitle(t *testing.T) {
	raw := `Vertical 3:4 poster key visual for the Chinese short drama "《书Z》", single hero subject, ` +
		`dramatic cinematic lighting, highly detailed. The cover title text "《书Z》" typeset on the poster. No other text.`
	got, dropped, untitled := enforceCoverText(raw, "书Z")
	if !strings.Contains(got, "dramatic cinematic lighting") || !strings.Contains(got, "single hero subject") {
		t.Errorf("只是**提到**书名的句子要留下（只抹书名那一处），实际 %q", got)
	}
	if dropped != 2 { // 画面文字句 + 否定式句
		t.Errorf("该摘掉 2 句（title text 那句 + no other text 那句），实际 %d", dropped)
	}
	if untitled != 1 { // 第一句里"\""《书Z》\""那一处
		t.Errorf("该抹掉 1 处书名（第一句里那个），实际 %d：%q", untitled, got)
	}
	if n := strings.Count(got, "《书Z》"); n != 1 {
		t.Errorf("收完后书名只许剩一处（就是 coverTextClause 那句），实际 %d 处：%q", n, got)
	}
}

// ④ 出图之后的文字判定：真判过 ⇒ judged=true 且结论照模型原话带出来；
// 没配视觉模型 ⇒ **judged=false + 说清原因**（不许把"没判成"当成"干净"）。
//
// 判据直接喂 `judgeCoverTextData`（判定本体，只吃 data URL）——`judgeCoverText` 要经
// `imageDataURL`，那条路依赖本机 ffmpeg，门会在没装工具的机器上变成摆设。
func TestJudgeCoverTextReportsVerdictAndNeverFakesClean(t *testing.T) {
	// 反面针先来：**没配视觉模型**时不许返回"干净"
	s0 := newTestServer(t)
	v0 := s0.judgeCoverTextData(context.Background(), "data:image/png;base64,AAAA", "书V")
	if v0.Judged || v0.Bad() {
		t.Errorf("没配视觉模型就判不了 ⇒ Judged=false（既不能说合格也不能说不合格），实际 %+v", v0)
	}
	if v0.Err == "" || !strings.Contains(v0.Text(), "没判成") {
		t.Errorf("判不了要如实说清原因（播报/字段都用 Text()）：%+v", v0)
	}

	// 真机同形：书名之外还有一行乱码伪字
	s := newTestServer(t)
	fakeCoverVLM(t, s, `{"texts":["《书V》","乱码伪字一行"],"title_readable":true,"other_than_title":true,`+
		`"note":"书名下方还有一行无法辨认的字"}`)
	v := s.judgeCoverTextData(context.Background(), "data:image/png;base64,AAAA", "书V")
	if !v.Judged || !v.Stray || !v.TitleOK {
		t.Fatalf("该判出「书名之外还有字形」：%+v", v)
	}
	if !v.Bad() {
		t.Error("书名之外还有字形 ⇒ 不合格（要重出或如实播报）")
	}
	for _, want := range []string{"乱码伪字一行", "无法辨认"} {
		if !strings.Contains(v.Text(), want) {
			t.Errorf("判定要带模型的原话（用户才看得见问题在哪）：%q（缺 %q）", v.Text(), want)
		}
	}

	// 合格那一路：只有书名一处、书名认得全
	s2 := newTestServer(t)
	fakeCoverVLM(t, s2, `{"texts":["《书V》"],"title_readable":true,"other_than_title":false,"note":"只有书名"}`)
	v2 := s2.judgeCoverTextData(context.Background(), "data:image/png;base64,AAAA", "书V")
	if !v2.Judged || v2.Bad() || v2.Text() != "只有书名一处文字" {
		t.Errorf("只有书名一处 ⇒ 合格：%+v（Text=%q）", v2, v2.Text())
	}
}

// ⑤ 重出策略（纯函数，穷举）：这张表就是"出图之后怎么处置"的全部规则。
//
// 为什么单独一门：出图那一段需要真 ComfyUI（测试环境没有引擎），策略必须能在没有引擎时被验。
func TestCoverTextRerollDecision(t *testing.T) {
	bad := coverTextVerdict{Judged: true, Stray: true, TitleOK: true, Texts: []string{"乱码伪字一行"}, Note: "书名下方还有字"}
	if bad.Bad() != true {
		t.Fatalf("书名之外还有字形 ⇒ 不合格：%+v", bad)
	}
	good := coverTextVerdict{Judged: true, TitleOK: true}
	unjudged := coverTextVerdict{Err: "未配置视觉模型（设置里填 VLM 地址/密钥/模型）"}
	garbledTitle := coverTextVerdict{Judged: true, TitleOK: false, Note: "书名糊成一团"}

	// 第一轮不合格 ⇒ 换种子重出（消息里要有模型原话，用户才知道为什么）
	if again, msg := coverTextRerollDecision(bad, 0, coverTextTries); !again || !strings.Contains(msg, "乱码伪字一行") {
		t.Errorf("第一轮不合格该重出一轮，并说清为什么：again=%v msg=%q", again, msg)
	}
	// 最后一轮仍不合格 ⇒ 不重出（有界），但必须**如实播报**
	again, msg := coverTextRerollDecision(bad, coverTextTries-1, coverTextTries)
	if again {
		t.Error("已经用完重出轮数（有界）：不许无限重出")
	}
	for _, want := range []string{"仍不合格", "如实播报"} {
		if !strings.Contains(msg, want) {
			t.Errorf("用完轮数要以「%s」收尾（不许假装干净）：%q", want, msg)
		}
	}
	// 合格 ⇒ 不重出、也不啰嗦
	if again, msg := coverTextRerollDecision(good, 0, coverTextTries); again || msg != "" {
		t.Errorf("合格那张不该重出、也不该报：again=%v msg=%q", again, msg)
	}
	// **没判成 ⇒ 不重出，但要说清"别当成验过"**
	again, msg = coverTextRerollDecision(unjudged, 0, coverTextTries)
	if again {
		t.Error("没判成就不该重出（重出靠的还是同一套判据）")
	}
	for _, want := range []string{"没判成", "别当成验过"} {
		if !strings.Contains(msg, want) {
			t.Errorf("没判成要如实说清（%s）：%q", want, msg)
		}
	}
	// 书名没认全 ⇒ 同样不合格（封面就是靠标题认书）
	if !garbledTitle.Bad() {
		t.Error("书名没认全的封面不能用（真机那张 VLM 的结论就是「不能」）")
	}
}

// ⑥ 接线（源码级，只看代码行）：两处入口都要真的走"判定 + 如实带出去"。
func TestCoverTextQCIsWiredIntoBothEntries(t *testing.T) {
	// 自动出封面（视频制作那条，没人盯着）：判 + 按策略重出 + **只有最后一张落盘**
	auto := codeLines(mustServerSrc(t, "novels_cover_redo.go"))
	for _, want := range []string{
		"last = s.judgeCoverText(ctx, cand, filepath.Base(bookDir))",
		"coverTextRerollDecision(last, try, coverTextTries)",
		"文字判定:%s", // 播报里要带上判定（用户看得到"这张到底验没验过"）
	} {
		if !strings.Contains(auto, want) {
			t.Errorf("自动出封面缺接线：%s", want)
		}
	}
	// **重出期间不许落盘**：`applyCoverCandidate` 只许在循环之后调一次。
	// 判据只看 `seedCoverFromTitle` 自己的函数体（另一个 handler 里也调它，全文件计数会误报）。
	body := funcBody(auto, "func (s *Server) seedCoverFromTitle")
	if strings.Count(body, "s.applyCoverCandidate(bookDir, cand)") != 1 {
		t.Errorf("重出期间不许落盘：applyCoverCandidate 在 seedCoverFromTitle 里只许出现一次（循环之后），实际 %d 次",
			strings.Count(body, "s.applyCoverCandidate(bookDir, cand)"))
	}
	if strings.Index(body, "s.applyCoverCandidate(bookDir, cand)") < strings.Index(body, "for try := 0;") {
		t.Error("落盘必须排在重出循环**之后**（否则重出的每一张都会被落盘）")
	}
	// 出候选（弹窗/菜单一键，有人挑）：判定要带进每张候选的 JSON
	for _, want := range []string{
		"verdicts[i] = s.judgeCoverText(jctx, out[i].File, filepath.Base(bookDir))",
		`Judged   bool   ` + "`json:\"judged\"`",
		`Stray    bool   ` + "`json:\"stray\"`",
		`TitleOK  bool   ` + "`json:\"title_ok\"`",
		`TextNote string ` + "`json:\"text_note\"`",
		"coverTextSummary(verdicts)",
	} {
		if !strings.Contains(auto, want) {
			t.Errorf("封面候选缺文字判定接线：%s", want)
		}
	}
	// 前端：候选卡上要看得到判定（含"未判"这一态 —— 不许显示成干净）
	vue := readFrontend(t, "src/views/NovelsView.vue")
	for _, want := range []string{
		"文字 OK", "文字不合规", "文字未判", "covTextNote(",
	} {
		if !strings.Contains(vue, want) {
			t.Errorf("候选卡缺文字判定显示：%s", want)
		}
	}
	// 影像侧口径（给 LLM 的系统提示词）也要改成正向那一套：**不许再要求写 no other text**
	sys := mustServerSrc(t, "novel_cover_prompt.go")
	if strings.Contains(sys, "必须写明：no other text") {
		t.Error("系统提示词里还留着「必须写明 no other text」—— 那是 cfg=1.0 下不生效的否定式")
	}
	if !strings.Contains(sys, "封面上只许有书名这一处文字") {
		t.Error("系统提示词要说清口径：封面上只许有书名这一处文字（正向陈述）")
	}
}

// ⑦ 真机探针（默认跳过）：拿盘上那本书**真跑一次**文字判定，看平台自己的视觉模型怎么说当前封面。
// 跑法：$env:MANJUX_LIVE_PROBE=1; go test ./internal/server/ -run TestZZProbeCoverTextLive -v
func TestZZProbeCoverTextLive(t *testing.T) {
	if os.Getenv("MANJUX_LIVE_PROBE") != "1" {
		t.Skip("设 MANJUX_LIVE_PROBE=1 才跑（会真的调一次平台视觉模型）")
	}
	cover := strings.TrimSpace(os.Getenv("MANJUX_COVER_PROBE"))
	if cover == "" {
		cover = filepath.Join("..", "..", "data", "小说", "离婚后，千亿前夫跪求复合", "封面", "封面.png")
	}
	if _, err := os.Stat(cover); err != nil {
		t.Skipf("盘上没有这张封面（%v）：%s", err, cover)
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
		VLM struct {
			BaseURL string `json:"base_url"`
			APIKey  string `json:"api_key"`
			Model   string `json:"model"`
		} `json:"vlm"`
	}
	if err := json.Unmarshal(raw, &real); err != nil {
		t.Fatalf("解析 settings.json 失败：%v", err)
	}
	s := newTestServer(t)
	// 判定的第一步是压图（imageDataURL 走 ffmpeg）：测试服务器默认只给一个零值 ff.FF，
	// 所以要在这里真的从 PATH 解析一次 —— 本机没装 ffmpeg 就**如实跳过**（不假装验过）。
	realFF, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机没找到 ffmpeg（%v）：判定要先压图，这条探针跑不了", err)
	}
	s.ff = realFF
	g := s.cfg.Get()
	g.VLM.BaseURL, g.VLM.APIKey, g.VLM.Model = real.VLM.BaseURL, real.VLM.APIKey, real.VLM.Model
	if err := s.cfg.Update(g); err != nil {
		t.Fatalf("写入真机 VLM 配置失败：%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*60*1e9)
	defer cancel()
	v := s.judgeCoverText(ctx, cover, "离婚后，千亿前夫跪求复合")
	t.Logf("判定：judged=%v stray=%v title_ok=%v\n原话：%s", v.Judged, v.Stray, v.TitleOK, v.Text())
	if !v.Judged {
		t.Fatalf("真机视觉模型没判成：%s", v.Err)
	}
}
