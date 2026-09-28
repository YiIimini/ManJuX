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

// splitNovelToProjects 把一本小说拆成"一集一个项目"。
//
// sheet 是技能侧写的「人物外形锚表」(可为空):它由 createSeg 拼进**每一集**的 source.txt ——
// 脚本阶段读什么,锚表就得在什么里面(见 episodeSourceWithSheet 的真机实证说明)。
// 传空串 = 本书没有锚表,行为与接入之前完全一致。
func (s *Server) splitNovelToProjects(ctx context.Context, name, text string, max int, artStyle, sheet string, onEps func(series string, newEps []string)) (string, []string, error) {
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
	}
	globalStyle := ""
	var epsAll []string // 全部集(含补缺命中的已有集)
	var lastEp epItem   // 跨段前情(上一集概要,供 SaveSource 前情提要)
	if gapPrev.Ep > 0 { // 自动补缺:前情从保留区末集续
		lastEp = gapPrev
	}
	createFail := 0
	createdTotal := 0

	type chunk struct{ label, body string }

	createSeg := func(segEps []epItem, ck chunk) int {
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
			// 外形锚表拼在**这一集**的 source 上(而不是拆集输入上):脚本阶段读的就是它,
			// 拼错地方 = 角色卡照旧现编(2026-09-27 真机实证,见 episodeSourceWithSheet)。
			src = episodeSourceWithSheet(src, sheet)
			// 原文:平台按章切片直接给(不依赖 LLM 写的 chapters 字符串);旧路径仅作兜底
			orig := ep.Orig
			if orig == "" {
				orig = extractEpOriginal(ck.body, ep.Chapters, maxOrig)
			}
			if orig != "" {
				if r := []rune(orig); maxOrig > 0 && len(r) > maxOrig {
					orig = string(r[:maxOrig])
				}
				src += "\n\n【本集原文】(分镜必须沿此原文节拍逐段改编,覆盖全部关键情节)\n" + orig
			}
			_ = s.store.SaveSource(epName, src)
			// 把「这集覆盖哪些章」写进项目元数据:覆盖核对与「自动补缺」从此读这个真源,
			// 不再依赖 LLM 的 chapters 字符串或【本集原文】是否附着成功(事故根因)。
			if len(ep.ChapNums) > 0 {
				if pj, ok := s.store.Get(epName); ok {
					pj.Chapters = append([]int(nil), ep.ChapNums...)
					_ = s.store.SaveMeta(pj)
				}
			}
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
		return len(created)
	}

	// ---- 一章一档快路径(2026-09-12 用户定调「一章一档还需要慢慢拆集?」)----
	// 正文是一章一档时,章界/章号/章名/该章正文**全部已知** —— "拆集"这件事本身不需要
	// LLM 参与:平台按章切片直接建集(秒级),LLM 只负责给每章写一条 brief(后台并行补,
	// 写完回填该集 source.txt)。相比"分段等 LLM 拆",首集可开跑的时间从数分钟降到秒级,
	// 而且集数=章数、集名=章名、每集原文按章切片 —— 三项都由构造保证,不存在跳章/并章。
	if chaps := SplitChapters(text); len(chaps) >= 8 {
		eps := alignEpsToChapters(chaps, nil) // 一章一集;brief 先留空,后台补
		// 集号:平台按章序连续编排(补缺模式从保留区末集续接,与分段路径同一规则)
		num := gapStartEp
		for i := range eps {
			num++
			eps[i].Ep = num
		}
		createdN := createSeg(eps, chunk{}) // 内部已通知产线(传的是**本次新建**的集)
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
			Message: fmt.Sprintf("⚡ 一章一档:已按章节文件直接建集 %d 集(新建 %d,其余已存在;章名即集名、原文逐章附着);各集剧情 brief 后台并行补写中", len(eps), createdN)})
		// 一集都没新建(重跑同一本书)时,createSeg 不会回调(它只在新建>0 时通知),
		// 但产线仍需要拿到这批已存在的集去消费 —— 这里补一次。
		// 注意不能无条件再通知一次:上一版就是这么把 64 集入队两遍变成 128 集的
		// (用户 2026-09-12 实证「那来的 128 集」)。
		if createdN == 0 && onEps != nil && len(epsAll) > 0 {
			onEps(series, epsAll)
		}
		// 【2026-09-28 用户口径】这里本来是「**给全部集并行补写 brief**」——每集最多 1.2 万字原文丢给
		// LLM，一本几十集的书就是几十个并发大请求，用户点一次导入就"一上来全解析"了 ✗。
		// 改成**轮到哪一集才解析哪一集**（流水线：第 N 集解析→渲染→第 N+1 集解析→渲染…），
		// 由批量产线在处理该集前调用 ensureEpisodeBrief（见 novels_jobs.go）。
		_ = ctx
		_ = prevSum
		return series, epsAll, nil
	}

	// ---- 分层拆解 + 流水线化 ----
	// 全本(如 240 章 39 万字)单次调用输出上限装不下完整拆集;按章节切块
	// 逐段拆解,且**逐段建项**:onEps 回调让产线在首段就绪时即启动,后续段
	// 静默补集(生产者-消费者,2026-09-10 用户定调)。
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
			// 平台强制一章一集(2026-09-12):集名取章名、集与章一一对应、原文按章切片。
			// LLM 只写 brief —— 它此前会跳章/并章,并把"整段范围"抄进每集的 chapters 字段,
			// 导致原文附着失败 + 覆盖核对误判(64 集被误删成 63 集的事故根因)。
			if chaps := SplitChapters(ck.body); len(chaps) > 0 {
				if len(eps) != len(chaps) {
					s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
						Message: fmt.Sprintf("⚠ 第 %d/%d 段:LLM 给了 %d 集、本段实际 %d 章 —— 已按「一章一集」校正为 %d 集",
							ci+1, len(chunks), len(eps), len(chaps), len(chaps))})
				}
				eps = alignEpsToChapters(chaps, eps)
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
			// ①真源优先:平台在拆集时写进项目的章号(Project.Chapters)。
			// ②其次:该集【本集原文】里实际出现的章头。
			// ③最后才看 LLM 写在 chapters 字段里的字符串 —— 实测它会把"整段范围"
			//   抄进每一集,是 2026-09-12「误判缺章 → 误删正确分集」事故的直接诱因。
			if pid := s.seriesExistingEp(series, ep.Ep); pid != "" {
				if pj, ok := s.store.Get(pid); ok && len(pj.Chapters) > 0 {
					for _, c := range pj.Chapters {
						cov[c] = true
					}
					continue
				}
				for _, c := range s.epCoveredChapters(pid) {
					cov[c] = true
				}
			}
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
		// 覆盖真源:优先读项目里持久化的 Chapters(平台拆集时写的),其次读原文章头。
		chs := p.Chapters
		if len(chs) == 0 {
			chs = s.epCoveredChapters(p.ID)
		}
		c := epCov{id: p.ID, num: n, chMin: -1}
		if len(chs) > 0 {
			sort.Ints(chs)
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
	// 内容实证护栏(2026-09-12 血案):"Chapters 字段/原文章头都没看到"≠"内容真的丢了"。
	// 实测事故里,第 9/10/11 章的**元数据**缺失被判成缺章,于是删掉本来正确的分集重拆,
	// 64 集变 63 集、第 009 章内容真的丢了。这里加一道硬校验:只有该章正文确实不在
	// 任何分集的 source.txt 里,才允许动刀;否则只告警、不删集。
	if bad := s.chapterTextMissingAnywhere(text, c0); !bad {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: fmt.Sprintf("⚠ 第 %d 章未见于任何分集的覆盖信息,但其正文实际已在某个分集里 —— 判定为元数据缺失,已跳过自动补缺(不删集);如确需重拆请手动处理", c0)})
		return text, 0, epItem{}, "", false
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

// chapterTextMissingAnywhere 判定"第 num 章的正文是否确实不在任何分集里"。
//
// 判据:取该章正文开头的一段特征串(跳过章头行、去掉空白),在**所有**分集的
// source.txt 里找;找到即说明内容还在(只是元数据缺失),不该删集重拆。
// 找不到特征串时保守返回 true(无从判断时按老行为走,不做更激进的改动)。
func (s *Server) chapterTextMissingAnywhere(text string, num int) bool {
	for _, ch := range SplitChapters(text) {
		if ch.Num != num {
			continue
		}
		// 特征串:章头之后的正文切片(去空白,取 24 字)
		body := ch.Text
		if i := strings.Index(body, "\n"); i >= 0 {
			body = body[i+1:]
		}
		probe := strings.Join(strings.Fields(body), "")
		if r := []rune(probe); len(r) > 24 {
			probe = string(r[:24])
		}
		if len([]rune(probe)) < 8 {
			return true // 该章正文太短,无从判断
		}
		for _, p := range s.store.List() {
			if src, err := s.store.LoadSource(p.ID); err == nil && strings.Contains(strings.Join(strings.Fields(src), ""), probe) {
				return false // 内容还在某个分集里
			}
		}
		return true
	}
	return true
}

// fillEpisodeBriefs 一章一档快路径的后台步骤:为每章并行补写剧情节拍 brief,并回填到
// 该集 source.txt 的【本集剧情】与前情提要。
//
// 为什么要并行且不阻塞建集:集与章的对应关系、集名、原文在"建集"这一刻就已经确定,
// brief 只影响分镜 LLM 的改编质量(它同时还能看到完整原文)。用户 2026-09-12 的诉求是
// 「一章一档还需要慢慢拆集?」——所以建集必须秒级完成、产线立即可开跑,brief 随后补齐;
// 每集渲染要数分钟,后台补写远远跑在前面。
func (s *Server) fillEpisodeBriefs(_ context.Context, series string, eps []epItem, artStyle string, briefMin, briefMax, prevSum int) {
	// 独立上下文:本函数是后台补写,而调用方(拆集生产者)在建集后立刻返回、
	// 其 ctx 随即被取消 —— 实测用调用方 ctx 会让 64 个 brief 请求在同一秒内
	// 全部 "context canceled"(建集是秒级的,brief 是分钟级的,生命周期必须分开)。
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	conc := 3
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	briefs := make([]string, len(eps))
	for i := range eps {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ep := eps[i]
			b := s.genOneChapterBrief(ctx, series, ep, artStyle, briefMin, briefMax)
			if strings.TrimSpace(b) == "" {
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", ProjectID: fmt.Sprintf("%s·第%02d集·%s", series, ep.Ep, safeSeries(ep.Title)),
					Message: fmt.Sprintf("⚠ 第 %d 集《%s》的剧情 brief 生成失败(不影响渲染:分镜仍可用本集原文改编)", ep.Ep, ep.Title)})
				return
			}
			briefs[i] = b
			// 回填 source:补齐【本集剧情】与前情提要(建集时 brief 尚不存在)
			epID := fmt.Sprintf("%s·第%02d集·%s", series, ep.Ep, safeSeries(ep.Title))
			src, err := s.store.LoadSource(epID)
			if err != nil {
				return
			}
			src = replaceEpisodeBrief(src, b)
			if i > 0 && briefs[i-1] != "" {
				src = upsertPrevSummary(src, eps[i-1].Ep, eps[i-1].Title, clampRunes(briefs[i-1], prevSum))
			}
			_ = s.store.SaveSource(epID, src)
		}(i)
	}
	wg.Wait()
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
		Message: fmt.Sprintf("✔ 各集剧情 brief 补写完成(%d 集,已回填到分集 source)", len(eps))})
}

// genOneChapterBrief 单章 brief:把该章原文交给 LLM,产出「剧情节拍 + 关键台词」清单。
func (s *Server) genOneChapterBrief(ctx context.Context, series string, ep epItem, artStyle string, briefMin, briefMax int) string {
	g := s.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	if !client.Configured() {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(g.LLM.Timeout)*time.Second)
	defer cancel()
	sys := fmt.Sprintf("你在为一集短视频写「剧情节拍 brief」(中文,%d-%d 字):按该章原文的先后顺序逐拍列出事件、出场人物、"+
		"关键台词(逐字保留重要对白)、情绪转折与结尾悬念;只写这一章的内容,不要扩写、不要引用其它章节。", briefMin, briefMax)
	user := fmt.Sprintf("作品:《%s》 第%d集《%s》%s\n\n【本章原文】\n%s", series, ep.Ep, ep.Title,
		func() string {
			if strings.TrimSpace(artStyle) != "" {
				return "(风格:" + artStyle + ")"
			}
			return ""
		}(), clampRunes(ep.Orig, 12000))
	out, err := client.Chat(cctx, []llm.Message{{Role: "system", Content: sys}, {Role: "user", Content: user}}, false)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// replaceEpisodeBrief 把 source.txt 里的【本集剧情】替换为新 brief(没有该段则插入)。
func replaceEpisodeBrief(src, brief string) string {
	const mark = "【本集剧情】"
	if i := strings.Index(src, mark); i >= 0 {
		head := src[:i+len(mark)]
		rest := src[i+len(mark):]
		// 剧情段到下一个空行为止
		if j := strings.Index(rest, "\n\n"); j >= 0 {
			return head + brief + rest[j:]
		}
		return head + brief
	}
	// 没有剧情段:插到全局风格行之后
	if i := strings.Index(src, "\n\n"); i >= 0 {
		return src[:i] + "\n\n" + mark + brief + src[i:]
	}
	return src + "\n\n" + mark + brief
}

// upsertPrevSummary 写入/替换该集 source.txt 里的【前情提要】(前情来自上一集 brief)。
func upsertPrevSummary(src string, prevEp int, prevTitle, prevBrief string) string {
	line := fmt.Sprintf("【前情提要】第%02d集《%s》:%s", prevEp, prevTitle, prevBrief)
	re := regexp.MustCompile(`(?m)^【前情提要】[^\n]*$`)
	if re.MatchString(src) {
		return re.ReplaceAllString(src, line)
	}
	// 插到标题行之后
	if i := strings.Index(src, "\n"); i >= 0 {
		return src[:i+1] + line + src[i+1:]
	}
	return line + "\n" + src
}
