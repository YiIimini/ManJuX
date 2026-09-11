// novels.go 小说库:扫描设置目录中的小说(书=子文件夹,优先「全本」,次选合并「正文」
// 分卷章节;封面取「封面」文件夹内图片),支持弹窗阅读详情与一键视频创作。
package server

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/pipeline"
)

func (s *Server) splitNovelToProjects(ctx context.Context, name, text string, max int, artStyle string, onEps func(series string, newEps []string)) (string, []string, error) {
	g := s.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	// 拆集参数从设置读取(0=默认值;2026-09-11 用户定调:灵活配置)
	splitCfg := g.Split
	dflt := func(v, d int) int {
		if v <= 0 {
			return d
		}
		return v
	}
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

	// ---- 章节感知自动补缺(2026-09-11 用户定调:管线自动补,不靠人工)----
	// 重拆已有系列时检出中间缺章 → 删除插入点之后的分集,文本截断到首个
	// 缺章处,重拆尾部续接集号(原「按集号去重」对中间缺章无效:新拆集号
	// 与已有撞车全被跳过,缺章永远补不上)。
	gapStartEp, gapStyle := 0, ""
	var gapPrev epItem
	if s.novelProduced(name) {
		var okFill bool
		text, gapStartEp, gapPrev, gapStyle, okFill = s.autoGapFillTrim(name, text)
		_ = okFill
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
	if gapPrev.Ep > 0 { // 自动补缺:前情从保留区末集续
		lastEp = gapPrev
	}
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
				// 集粒度铁律:一集=一个完整剧情事件(2026-09-11 晚用户实证
				// 「前后不知道说的什么」:每章强拆 2-3 集,一集只剩半件事,
				// 42 秒 4 角色流水账,核心冲突被推到下集——碎拆比粗拆更废)。
				// 密度回归:默认 1 章 1 集,信息极密集章最多 2 集,且必须在
				// 自然剧情转折处切;上限=章数+章数/4+1(约 1.25 集/章)
				nCh := countChapterHeads(ck.body)
				maxEps := nCh + nCh/4 + 1
				if maxEps > 16 {
					maxEps = 16
				}
				if maxEps < 4 {
					maxEps = 4
				}
				epsHint := "本段通常 3-6 集,一集必须讲完一个完整剧情事件"
				if nCh > 0 {
					epsHint = fmt.Sprintf("本段覆盖 %d 章:**每章正好 1 集(章名即集名),本段应为 %d 集**;一集从该章的开场钩子讲到结尾悬念,事件弧完整;章内信息量大就多给分镜(**不设上限**),不要把一章拆成两集,也不要把两章压进一集", nCh, nCh)
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
	planned := gapStartEp   // 自动补缺时从保留区末集续接编号
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
			} else if gapStyle != "" {
				// 自动补缺:风格沿用保留集,防重拆尾部风格漂移
				globalStyle = gapStyle
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
					Message: "🎨 视频渲染风格沿用保留分集(补缺模式防漂移)"})
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
	// 一章一集校验:若 LLM 把一章拆成多集,集数会多于章数,后续所有「按集对章」的
	// 核对都会错位 —— 单独报出来,让用户判断是否重拆(不自动合并:合并要重排
	// Brief 与集号,风险高于收益)。
	if dup := checkOneChapterPerEpisode(plannedEps); len(dup) > 0 {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: fmt.Sprintf("⚠ 有 %d 章被拆成了多集(第 %v 章):按「一章一集」规则应为一章一集,建议重拆", len(dup), dup)})
	}
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
			// 交叉验证:LLM 标注的 chapters 可能漏章 —— 实测首段把第 1、2 章漏标,
			// 而两章内容其实都在(分别落在第 1~3 集里;分集标题是 LLM 自起的,不能作判据)。
			// 这里再用该集**原文里实际出现的章头**(epCoveredChapters)补一次覆盖,两者取
			// 并集,避免「标注漏项」被误报成「内容丢失」进而诱导用户删集重拆。
			if pid := s.seriesExistingEp(series, ep.Ep); pid != "" {
				for _, c := range s.epCoveredChapters(pid) {
					cov[c] = true
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
			// 措辞必须交代代价:若缺的是最前面几章,"删除缺章位置之后的分集"
			// 意味着删掉**全部**已建分集重拆 —— 这不是轻描淡写的操作。
			tip := ""
			if len(missing) > 0 && missing[0] <= 3 {
				tip = "注意:缺章在最前面,自动补缺会删除全部分集后重拆,代价很大 —— 请先核对是否只是 chapters 标注漏项"
			} else {
				tip = "重新提交一键创作可自动补缺(会删除缺章位置之后的分集并重拆续接)"
			}
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
				Message: fmt.Sprintf("⚠ 章节覆盖核对:缺 %d 章未读到(第 %v 章)——%s", len(missing), missing, tip)})
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

// chapterHeadOffset 返回指定章号的章头行在文本中的字节偏移(未找到=-1)。
func chapterHeadOffset(text string, ch int) int {
	numRe := regexp.MustCompile(`第\s*0*([0-9]{1,4})\s*[章回节]`)
	for _, loc := range chapterHeadRe.FindAllStringIndex(text, -1) {
		if m := numRe.FindStringSubmatch(text[loc[0]:loc[1]]); m != nil {
			if v, e := strconv.Atoi(m[1]); e == nil && v == ch {
				return loc[0]
			}
		}
	}
	return -1
}

// epCoveredChapters 解析分集 source.txt【本集原文】块的章头 → 覆盖章号(升序)。
// 无原文附着的分集返回 nil(覆盖未知)。
func (s *Server) epCoveredChapters(id string) []int {
	src, err := s.store.LoadSource(id)
	if err != nil {
		return nil
	}
	i := strings.Index(src, "【本集原文】")
	if i < 0 {
		return nil
	}
	numRe := regexp.MustCompile(`第\s*0*([0-9]{1,4})\s*[章回节]`)
	var out []int
	for _, line := range chapterHeadRe.FindAllString(src[i:], -1) {
		if m := numRe.FindStringSubmatch(line); m != nil {
			if v, e := strconv.Atoi(m[1]); e == nil {
				out = append(out, v)
			}
		}
	}
	sort.Ints(out)
	return out
}

// autoGapFillTrim 章节感知自动补缺:重拆已有系列时检出中间缺章——删除
// 缺章插入点之后的分集(条件缓存同步清理,资产库不动),文本截断到首个
// 缺章处重拆尾部;起始集号与保留区续接、全局风格沿用保留集、前情取自
// 保留区末集。原「按集号去重」对中间缺章无效:重拆新集与已有集号撞车
// 全被跳过,缺章永远补不上(2026-09-11 用户定调:管线自动补,不靠人工)。
// ok=false:无缺章/缺章在末尾(普通补缺可续建)/产线运行中/无法定位。
func (s *Server) autoGapFillTrim(name, text string) (string, int, epItem, string, bool) {
	heads := chapterNumSet(text)
	if len(heads) == 0 {
		return text, 0, epItem{}, "", false
	}
	reEp := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `·第(\d+)集`)
	type epCov struct {
		id    string
		num   int
		chMin int // 覆盖的最小章号;-1=未知(无原文附着)
	}
	var covs []epCov
	covered := map[int]bool{}
	for _, p := range s.store.List() {
		m := reEp.FindStringSubmatch(p.ID)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		chs := s.epCoveredChapters(p.ID)
		c := epCov{id: p.ID, num: n, chMin: -1}
		if len(chs) > 0 {
			c.chMin = chs[0]
			for _, x := range chs {
				covered[x] = true
			}
		}
		covs = append(covs, c)
	}
	if len(covs) == 0 {
		return text, 0, epItem{}, "", false
	}
	var missing []int
	for c := range heads {
		if !covered[c] {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return text, 0, epItem{}, "", false
	}
	sort.Ints(missing)
	c0 := missing[0]
	sort.Slice(covs, func(i, j int) bool { return covs[i].num < covs[j].num })
	// 插入点:第一个「覆盖起点晚于首缺章 或 覆盖未知」的集,它起全部删除重拆
	cut := 0
	for _, c := range covs {
		if c.chMin > c0 || c.chMin < 0 {
			cut = c.num
			break
		}
	}
	if cut == 0 {
		return text, 0, epItem{}, "", false // 缺章在全部已知覆盖之后:普通补缺续建新集即可
	}
	// 产线运行中不许删集(运行项目目录被删,渲染中途炸)
	for _, c := range covs {
		if c.num >= cut && s.pipe.Running(c.id) != nil {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
				Message: fmt.Sprintf("⚠ 检出缺章(第 %v 章)但批量产线运行中:请先停止产线再重新提交,自动补缺将删除第 %d 集起的分集并重拆续接", missing, cut)})
			return text, 0, epItem{}, "", false
		}
	}
	offset := chapterHeadOffset(text, c0)
	if offset < 0 {
		return text, 0, epItem{}, "", false
	}
	// 保留区末集 → 前情提要
	var prev epItem
	for _, c := range covs {
		if c.num == cut-1 {
			if src, err := s.store.LoadSource(c.id); err == nil {
				prev.Ep = c.num
				if m := regexp.MustCompile(`·第\d+集·(.+)$`).FindStringSubmatch(c.id); m != nil {
					prev.Title = m[1]
				}
				if i := strings.Index(src, "【本集剧情】"); i >= 0 {
					b := src[i+len("【本集剧情】"):]
					if j := strings.Index(b, "\n\n【本集原文】"); j >= 0 {
						b = b[:j]
					}
					prev.Brief = strings.TrimSpace(b)
				}
			}
			break
		}
	}
	// 全局风格沿用保留集(防重拆尾部风格漂移)
	style := ""
	if len(covs) > 0 {
		if src, err := s.store.LoadSource(covs[0].id); err == nil {
			if m := regexp.MustCompile(`(?m)^全局风格[:：](.+)$`).FindStringSubmatch(src); m != nil {
				style = strings.TrimSpace(m[1])
			}
		}
	}
	// 删除插入点起的分集
	removed := 0
	for _, c := range covs {
		if c.num < cut {
			continue
		}
		if err := s.store.Delete(c.id); err == nil {
			removed++
			if eng, err := s.reg.Get("comfy"); err == nil {
				if ce, ok := eng.(*comfyengine.Engine); ok {
					ce.CleanupProjectCache(c.id)
				}
			}
		}
	}
	if removed == 0 {
		return text, 0, epItem{}, "", false
	}
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
		Message: fmt.Sprintf("🔧 检出缺章 第 %v 章:自动补缺——删除第 %d 集起的 %d 个分集,从第 %d 章起重拆续接(保留分集与资产库不动)", missing, cut, removed, c0)})
	s.opLog("自动补缺:缺章 %v,删除第 %d 集起 %d 个分集,从第 %d 章重拆续接", missing, cut, removed, c0)
	return text[offset:], cut - 1, prev, style, true
}

// failNote 创建失败数附注。
