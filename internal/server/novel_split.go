// novel_split.go 整本小说批量产线:LLM 拆集 → 每集一个子项目(共享资产库,
// 同名角色/场景自动复用定妆照)→ 逐集串行跑全流程。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/pipeline"
)

const novelSplitSystem = `你是短视频连载的总编剧。把用户给的小说/长文拆成多集短视频剧本大纲:
- 输出格式(分节纯文本,严格按此模板;不是 JSON,不要 markdown 代码块,不要用 {} [] 包裹任何内容):
BOOK: 书名(中文,≤10字)
STYLE: 英文全局视觉风格
===EP 1===
TITLE: 本集小标题(中文,≤8字)
CHAPTERS: 第X章~第Y章
BRIEF:
本集完整剧情大纲,中文 400-700 字,必须自足:逐拍列出剧情节拍(每拍一句话,拍与拍因果相连不留断层);包含:开场钩子、每拍场面与人物动作、重要台词原文、情绪转折、结尾钩子。正文直接换行书写,写完为止,直到下一个 ===EP=== 标记
===EP 2===
TITLE: …(**直接取该章标题原文**,去掉"第N章"前缀即为集名;章无标题时用该章核心事件短语。不要另起文艺标题 —— 章名就是集名,便于对账与定位)
CHAPTERS: …
BRIEF:
…
- 台词照抄原文即可:「」""'' 任何引号都能用,分节格式没有转义问题;唯一要求是每集必须以 ===EP N=== 标记开头、TITLE/CHAPTERS/BRIEF 行标签齐全。
- 拆集规则:**一章正好一集,严格 1:1,集数=章数**。每集的时长与镜头数由内容决定(**不设镜头数上限**,内容多就给足镜头);一集=一个完整剧情事件弧:开场钩子→冲突/过程→结尾悬念或结果,观众单看一集能看懂发生了什么。章内内容多就**多给分镜**,绝不因此把一章拆成两集;也绝不多章压进一集。不注水、不碎拆、不丢主线;集与集剧情连续、brief 互相衔接。
- 完整改编:主线与影响剧情走向的支线全部保留,不得舍弃情节;一章一集(章内信息量大则增加分镜数,不增加集数);每集 brief 中的台词必须是原文关键句。
- CHAPTERS 行:本集覆盖的章节范围,章节标题必须与正文中的章节标题逐字一致(格式如"第三章 夜探王府~第五章 破晓";本集只覆盖某章一部分时也写该章起止);系统将据此把原文全文附给分镜,brief 与 CHAPTERS 必须对应同一范围。
- 你可能拿到的是全书的一个分段(分段拆解模式):严格按给定的起始集号连续编号,并与【前情提要】无缝衔接,不得重复已拆过的剧情。`

// handleNovelSplit POST /api/novel-split {name, text, max_episodes?}
func (s *Server) handleNovelSplit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Text string `json:"text"`
		Max  int    `json:"max_episodes"`
	}
	if err := readBody(r, &in); err != nil || strings.TrimSpace(in.Text) == "" {
		writeErr(w, 400, fmt.Errorf("请提供小说正文"))
		return
	}
	if len([]rune(in.Text)) < 200 {
		writeErr(w, 400, fmt.Errorf("小说正文太短(至少 200 字)"))
		return
	}
	g := s.cfg.Get()
	if g.LLM.BaseURL == "" || g.LLM.APIKey == "" || g.LLM.Model == "" {
		writeErr(w, 400, fmt.Errorf("LLM 未配置"))
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "连载"
	}
	if s.novelProduced(name) && name != "连载" {
		// 补缺模式:重拆按集号去重,已有分集不动(2026-09-10 与一键创作入口统一)
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
			Message: "《" + name + "》已有系列,进入补缺模式:已有分集保持不动,仅补建缺失的集"})
	}
	// 异步后台拆集建项目:立即返回,进度看全局日志(30 秒心跳+逐集创建事件),
	// 首页项目列表随 novel 事件实时刷新——弹窗不再同步等待 LLM(2-6 分钟)。
	go func() {
		// 注册键与拆集内部 safeSeries 规整键一致,防占位卡孤儿(同 novels.go)
		name = safeSeries(name)
		// 这条入口(手工粘贴文本拆集)拿不到小说目录,取不到外形锚表 —— 传空串,
		// 行为与本轮改动之前一致(锚表只走"从小说库生产"那条有目录可查的路)。
		series, _, err := s.splitNovelToProjects(context.Background(), name, in.Text, in.Max, "", "", nil)
		if err != nil {
			jobSet(name, "failed", 0, "拆集失败:"+err.Error())
		}
		_ = series
	}()
	writeJSON(w, 200, map[string]any{"ok": true, "submitted": true,
		"message": "已提交后台拆集(通常 2-6 分钟):完成后分集项目自动出现在首页,日志有实时心跳"})
}

func clampRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func jsonUnmarshalStrict(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}

// ---------- 批量产线:逐集串行全流程 ----------

// novelBatch 批量产线(全局单例)。
//
// **逐集串行铁律**(用户 2026-09-28 定调,两次强调):
//   - 第一版原话:「LLM 一集一集地去解析,不要直接一上来就全解析了,太慢了」;
//   - 第二版原话:「批量跑一集进行这一集的 LLM 解析后进行渲染管线顺序,到下一集就是
//     下一集的 LLM 解析后进行渲染管线顺序,后面依次顺序,流水线一样到了那一步就做那一步」。
//
// 落地成两条硬约束:① 集数照常全部入队、一路顺下去(不等人点「开始」);
// ② **一集的 LLM 解析只属于那一集** —— 轮到第 N 集才解析第 N 集;替下一集提前解析**只在
// 安全窗口里**做(上一集方案已落盘 + 此刻没有别的 LLM 解析在跑,判据见 canPreparse)。
//
// 第 ② 条在同一天里被用户纠了两次,两次都记在这儿,免得再走回去:
//   - 「说了 LLM 解析按集来,为什么一来就直接两集开始解析」—— 违规的是**两集同时解析**;
//   - 「提前预解析那也是当前没有相同任务的条件下才去执行啊」—— **有条件的提前是允许的**。
//
// 第一版把它读成"不许提前",把预解析整个删掉了;这一版把门补齐,守卫见 batch_serial_test.go。
type novelBatch struct {
	mu       sync.Mutex
	prefix   string
	projects []string
	cur      int // 当前执行到第几个(1 基)
	stop     bool
	running  bool
}

var batch = &novelBatch{}

// handleNovelBatch POST /api/novel-batch {prefix} 开始;DELETE 停止;GET 状态。
func (s *Server) handleNovelBatch(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		batch.mu.Lock()
		defer batch.mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"running": batch.running, "prefix": batch.prefix,
			"current": batch.cur, "total": len(batch.projects),
		})
		return
	case http.MethodDelete:
		batch.mu.Lock()
		batch.stop = true
		batch.mu.Unlock()
		// 同步取消所有拆集生产者的 LLM 调用(2026-09-11:停止=拆集+产线全停)
		prodCancelAll()
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	var in struct {
		Prefix string `json:"prefix"`
	}
	if err := readBody(r, &in); err != nil || strings.TrimSpace(in.Prefix) == "" {
		writeErr(w, 400, fmt.Errorf("请提供剧集前缀(主项目名)"))
		return
	}
	prefix := strings.TrimSpace(in.Prefix)
	eps := s.episodesOf(prefix)
	if len(eps) == 0 {
		writeErr(w, 404, fmt.Errorf("未找到「%s·第N集」系列项目,请先整本拆集", prefix))
		return
	}

	// 启动前先收拾死批次:单例被"停下来的旧批次"占着时,新批次永远起不来
	// (这是本应用自己的 bug,不该让用户去手动点「停止」)。
	s.reapStaleBatch()
	if !s.startProd(prefix, eps) {
		writeErr(w, 409, fmt.Errorf("批量产线已在运行(%s 第 %d/%d 集)", batch.prefix, batch.cur, len(batch.projects)))
		return
	}
	// 回执必须带**书名**与首个集名:原先只回 {ok,total},前端只能弹"共 N 集" ——
	// 用户点了 A 卡却启动了 B 卡时,界面上一个字都对不上,谁也说不清刚才点的是哪本
	// (2026-09-26 用户报「我点的是《求你们骂我》,结果是另一部」)。机器可判的必须在回执里。
	writeJSON(w, 200, map[string]any{
		"ok": true, "prefix": prefix, "total": len(eps), "first": filepath.Base(eps[0]),
	})
}

// episodesOf 一本书的**集项目清单**:按 `^前缀·第N集` 匹配项目 ID,按集号升序。
//
// 收成一处的理由:这条"什么算这本书的一集"的口径原先只写在批量入口里,而
// 「脚本创作」导入后也要按同一口径开产线 —— 两处各写一遍,迟早一处松一处紧,
// 表现就是"从 A 入口启动的批次比从 B 入口少几集"(2026-09-26 收口)。
func (s *Server) episodesOf(prefix string) []string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil
	}
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `·第\d+集`)
	var eps []string
	for _, p := range s.store.List() {
		if re.MatchString(p.ID) {
			eps = append(eps, p.ID)
		}
	}
	sort.Slice(eps, func(i, j int) bool { return epNo(eps[i]) < epNo(eps[j]) })
	return eps
}

// reapStaleBatch 回收"已经不干活、却还占着单例"的批次。
//
// 【为什么必须有它】批量产线是全局单例(一个 batch),而失败/被卡住的那批**不会自己让位**:
// 它停在第 N 集,`batch.running` 仍是 true,于是之后任何一个入口来启动都拿不到产线 ——
// 用户点了「脚本创作」导入 40 集,界面上什么都不发生(2026-09-26 实测:13:35 那批导入了
// 40 集却没有任何 ▶ 启动行,根因就是 13:23 那批死批次赖着)。
//
// **这是本应用自己的 bug,不该甩给用户去点「停止」**:启动前先看当前集到底有没有在跑 ——
// Coordinator 说没在跑,就把它请走,然后让新的批次正常开起来。
//
// 判据取"当前集是否真在跑"而不是看时间戳:批次可能正在两个集之间的间隙里,那种情况
// Coordinator 也说没在跑,但它的循环还活着 —— 所以请求停止后要**等它自己让出 running**,
// 等不到才强制复位(最后手段,并明确播报)。
func (s *Server) reapStaleBatch() {
	batch.mu.Lock()
	if !batch.running {
		batch.mu.Unlock()
		return
	}
	prefix := batch.prefix
	id := ""
	if batch.cur >= 0 && batch.cur < len(batch.projects) {
		id = batch.projects[batch.cur]
	}
	batch.mu.Unlock()

	// 真在跑:绝不许抢(同一条产线共用一张显卡,并发只会互相拖死)
	if id != "" && s.pipe != nil && s.pipe.Running(id) != nil {
		return
	}

	batch.mu.Lock()
	batch.stop = true
	batch.mu.Unlock()
	for i := 0; i < 50; i++ { // 最多等 5 秒让它收尾
		time.Sleep(100 * time.Millisecond)
		batch.mu.Lock()
		running := batch.running
		batch.mu.Unlock()
		if !running {
			break
		}
	}
	batch.mu.Lock()
	if batch.running { // 还赖着:强制复位(单例不许被死批次永久霸占)
		batch.running, batch.projects, batch.cur, batch.stop, batch.prefix = false, nil, 0, false, ""
		batch.mu.Unlock()
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: fmt.Sprintf("⚠ 已回收一个停止后仍占着产线的旧批次(《%s》)—— 新批次可以开了", prefix)})
		return
	}
	batch.mu.Unlock()
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
		Message: fmt.Sprintf("♻ 旧批次(《%s》)没有在跑,已让位给新的批次", prefix)})
}

func (s *Server) runNovelBatch(prefix string, eps []string) {
	defer func() {
		batch.mu.Lock()
		batch.running = false
		batch.mu.Unlock()
	}()
	// 启动横幅必须写清**跑的是哪一本、哪几集**:原先只有书名与集数,用户点了 A 卡却启动了
	// B 卡时界面上对不上账(2026-09-26 用户报「我点的是《求你们骂我》,结果是另一部」)。
	// 首末集名一列,"点 A 跑 B"当场可查 —— 机器可判的信息不该只留在用户脑子里。
	tail := ""
	if prodAlive(prefix) {
		tail = ";拆集仍在进行,新集将实时入队"
	}
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
		Message: fmt.Sprintf("▶ 批量产线启动:《%s》首批 %d 集,逐集串行(分镜→定妆→渲染→质检→成片)｜本批:%s … %s%s",
			prefix, len(eps), filepath.Base(eps[0]), filepath.Base(eps[len(eps)-1]), tail)})
	waitAnnounced := false
	for i := 0; ; i++ {
		batch.mu.Lock()
		if batch.stop {
			batch.mu.Unlock()
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", Message: "⏹ 批量产线已停止"})
			return
		}
		if i >= len(batch.projects) {
			alive := prodAlive(prefix)
			total := len(batch.projects)
			batch.mu.Unlock()
			if !alive {
				break // 自然收尾:已知集全部处理完且拆集已结束
			}
			if !waitAnnounced {
				waitAnnounced = true
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
					Message: fmt.Sprintf("⏳ 产线已追平拆集进度(%d 集全部入队/处理中),等待新段拆集…", total)})
			}
			time.Sleep(4 * time.Second)
			i-- // 原地等待新分集入队
			continue
		}
		waitAnnounced = false
		ep := batch.projects[i]
		batch.cur = i + 1
		batch.mu.Unlock()

		// 断点续跑:已有成片(768P/2K 任一)的集直接跳过 —— 中断后再点「批量产线」
		// 从第一个未完成集继续;半成品集由全流程指纹机制续渲剩余镜头。
		done := func() bool {
			for _, f := range []string{"final.mp4", "final_2k.mp4"} {
				if st, err := os.Stat(filepath.Join(s.store.Dir(ep), "output", f)); err == nil && st.Size() > 0 {
					return true
				}
			}
			return false
		}()
		if done {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
				Message: fmt.Sprintf("⏩ 批量产线 %d/%d:《%s》已成片,跳过(断点续跑)", i+1, batchTotal(), ep)})
			continue
		}

		// **流水线的一步**（用户 2026-09-28）：轮到这一集了，先做它的 LLM 解析（剧情 brief），
		// 再进渲染管线 —— 而不是导入时就把整本书的集全解析掉（那是"一上来就全解析，太慢了"）。
		s.ensureEpisodeBrief(ep)
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
			Message: fmt.Sprintf("▶ 批量产线 %d/%d:开始《%s》", i+1, batchTotal(), ep)})
		// 这一集已经在跑(用户自己单独启动过它,不是本产线起的)⇒ 本轮**不再重复启动**,
		// 但**也绝不跳过它**:跳过的后果是产线直接去起下一集,两集并发抢同一张显卡
		// (正是 2026-09-12 那场"定妆照整批失败"的形态)。落到下面等它跑完再续。
		alreadyRunning := s.pipe.Running(ep) != nil
		if alreadyRunning {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
				Message: fmt.Sprintf("⏳ 《%s》已在运行(不是本产线启动的),产线等它跑完再续下一集", ep)})
		}
		// **一集的 LLM 解析只属于那一集**(见 novelBatch 的逐集串行铁律):这里只起 `ep`。
		// 替下一集提前解析的事**不在这儿做** —— 它要等到"本集方案已落盘、LLM 空出来"的安全
		// 窗口(见下面等待循环里的 preparseNextScript)。在起集之前就动手,正是"第 1 集刚开跑、
		// 第 2 集的分镜也在解析"那个形态(2026-09-28 用户报「一来就直接两集开始解析」)。
		startPhase := pipeline.PhaseAll
		if hasUsablePlan(s.store.Dir(ep)) {
			// 方案已在(上次跑过留下,或用户单独跑过这一集)⇒ 不必再解析一遍,直接从资产开始。
			startPhase = pipeline.PhaseAssets
		}
		if !alreadyRunning {
			if err := s.pipe.Start(ep, startPhase, nil, "", 0); err != nil {
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "error",
					Message: fmt.Sprintf("✕ 批量产线暂停于 %d/%d:《%s》启动失败:%v(不自动跳集)", i+1, batchTotal(), ep, err)})
				return
			}
		}
		epStart := time.Now()
		// 等本集跑完。判定口径(2026-09-12 重构):**看有没有产物进展,而不是看跑了多久**。
		//   - 只要项目目录里还有新产物落地(定妆照/转台图/manifest 状态/镜头 mp4),
		//     就一直等 —— 角色多、多视角转身多的集本来就慢(实测一集 10 个角色光转身
		//     就 50 分钟),旧实现的固定 2.5 小时上限会把这种"慢而正常"的集判成异常;
		//   - 连续 batchStallLimit 分钟**一点变化都没有**才判卡住并暂停产线;
		//   - batchEpisodeHardCap 是绝对兜底,防极端情况下无限等。
		hardDeadline := epStart.Add(batchEpisodeHardCap)
		fresh := projectFreshest(s.store.Dir(ep))
		lastActive := time.Now()
		// 预解析下一集:只在 canPreparse 定义的**安全窗口**里做(见 preparseNextScript)。
		// 一集只试一次;窗口一直不出现就算了 —— 轮到下一集时它自己会按正常流程解析。
		preparsed := false
		for time.Now().Before(hardDeadline) {
			time.Sleep(10 * time.Second)
			batch.mu.Lock()
			stopped := batch.stop
			batch.mu.Unlock()
			if stopped {
				_ = s.pipe.Stop(ep)
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", Message: "⏹ 批量产线已停止(当前集已中止)"})
				return
			}
			if s.pipe.Running(ep) == nil {
				break
			}
			// LLM 空出来了吗:本集已进入资产/渲染阶段(方案早就落盘)、且此刻没有任何
			// script 阶段在跑 —— 这就是预解析下一集的安全窗口。
			if !preparsed {
				preparsed = s.preparseNextScript(i)
			}
			if f := projectFreshest(s.store.Dir(ep)); f.After(fresh) {
				fresh, lastActive = f, time.Now()
			} else if time.Since(lastActive) > batchStallLimit {
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
					Message: fmt.Sprintf("⏸ 批量产线暂停于 %d/%d:《%s》近 %s 没有任何新产物(疑似卡住),已保留它继续跑,不再并行开下一集;确认后再点「批量产线」续跑",
						i+1, batchTotal(), ep, batchStallLimit.Round(time.Minute))})
				return
			}
		}
		// 绝对兜底到点、而该集**仍在运行**时,绝不能当成"完成"往下走。
		// 旧实现直接落到下面的「✔ 完成」分支:上一集还在渲染,下一集已经开跑,
		// 于是多集并发抢一张显卡 —— 出图任务排在长视频任务后面排队超时(角色/场景
		// 定妆照整批失败)、单镜从 3-5 分钟涨到 20+ 分钟,越跑越慢(2026-09-12 实证:
		// 第01/02/03 集三条管线同时在跑,三张定妆照 + 一张场景图被杀)。
		// 这里改为:保留它继续跑,产线停住等用户 —— 单集慢是慢,不会把别集一起拖垮。
		if s.pipe.Running(ep) != nil {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
				Message: fmt.Sprintf("⏸ 批量产线暂停于 %d/%d:《%s》已跑 %s 仍未结束(超过单集 %s 绝对上限),已保留它继续跑,不再并行开下一集(同一条产线共用一张显卡,并发只会让两集一起变慢甚至超时);等它跑完再点「批量产线」续跑",
					i+1, batchTotal(), ep, time.Since(epStart).Round(time.Minute), batchEpisodeHardCap.Round(time.Minute))})
			return
		}
		// 排队铁律:只有「自然完成」才续下一集;中止/失败一律停住队列,
		// 不得把被停的集当成完成继续空转跳集(2026-09-08 用户规则)。
		res := s.pipe.Result(ep)
		if res == pipeline.ResultStopped {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
				Message: fmt.Sprintf("⏹ 批量产线暂停于 %d/%d:《%s》已被中止,不自动跳集(重新点「批量产线」从中断处续跑)", i+1, batchTotal(), ep)})
			return
		}
		if res == pipeline.ResultFailed {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "error",
				Message: fmt.Sprintf("✕ 批量产线暂停于 %d/%d:《%s》运行失败,不自动跳集(修复问题后重新点「批量产线」续跑)", i+1, len(eps), ep)})
			return
		}
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
			Message: fmt.Sprintf("✔ 批量产线 %d/%d:《%s》完成", i+1, len(eps), ep)})
	}
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
		Message: fmt.Sprintf("🎉 批量产线全部完成:《%s》%d 集处理结束,成片见各集项目/作品库", prefix, batchTotal())})
}

func epNo(name string) int {
	re := regexp.MustCompile(`·第(\d+)集`)
	if m := re.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 9999
}

// batchTotal 当前产线总集数(动态:流式拆集会持续追加)。
func batchTotal() int {
	batch.mu.Lock()
	defer batch.mu.Unlock()
	return len(batch.projects)
}

// tailRunes 取字符串尾部 n 字(诊断输出截断用)。
func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return string(r)
	}
	return string(r[len(r)-n:])
}

// 批量产线的等待策略常量。
const (
	// batchStallLimit 连续多久"项目目录一点新产物都没有"就判该集卡住并暂停产线。
	// 定 60 分钟:单镜渲染最长 45 分钟(超时后还会释放显存重试一次),中间会写
	// manifest 状态,所以正常等待不会连续一小时毫无动静。
	batchStallLimit = 60 * time.Minute
	// batchEpisodeHardCap 单集绝对上限(12 小时),防极端情况下产线无限等。
	batchEpisodeHardCap = 12 * time.Hour
)

// projectFreshest 项目目录里最近一次文件改动时间(跳过 logs/)。
//
// 作为"这条管线还在产出吗"的活性信号:定妆照、多视角转台图、manifest 状态、
// 镜头 mp4、成片 —— 任何一步落地都会刷新它。比"跑了多久"可靠得多:
// 慢而正常的集(角色多、转身多)不会被误判,死住的集会被抓出来。
func projectFreshest(dir string) time.Time {
	var newest time.Time
	_ = filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if e.IsDir() {
			if e.Name() == "logs" { // 管线自己的日志不算"产物"
				return filepath.SkipDir
			}
			return nil
		}
		if fi, ierr := e.Info(); ierr == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
		return nil
	})
	return newest
}

// canPreparse 现在能不能预解析下一集的剧本分镜?三个条件缺一不可。
//
// 用户 2026-09-28 两次定调都指向这里:
//   - 「说了 LLM 解析按集来,为什么一来就直接两集开始解析」—— 两集**同时**解析是违规的;
//   - 「提前预解析那也是当前没有相同任务的条件下才去执行啊」—— **有条件的提前是允许的**。
//
// 上一版(同一天)把这两句读成了"不许提前",于是把预解析整个删掉 —— 读窄了:该修的是**门**,
// 不是机制。这一版把门补齐:
//
//   - prevPlanReady:上一集的方案已落盘。这是**跨集一致性的补给线** —— script 阶段开头会扫
//     兄弟集的 plan.json,把"已确立的角色名 + 形象描述"注入提示词(见 pipeline.bookRoster),
//     而 plan.json 是 script 阶段**末尾**才落盘的(stages_script.go 的 SavePlan)。上一集方案
//     还没落盘就解析下一集 ⇒ 下一集读不到上一集的角色 ⇒ 它自己发明角色名与形象 ⇒ 跨集漂移
//     (2026-09-09 用户实证「貅兜兜跨集飘逸」;实测第01集「貅满堂」到第02集被写成「貅满仓」,
//     一字之差即匹配失效、重生成)。这条门把时机天然推到"本集已进入资产/渲染阶段"。
//   - !llmBusy:此刻没有任何 script 阶段在跑 —— 一集一集来,同一时刻只有一集在做 LLM 解析。
//   - nextNeedsPlan:下一集存在、且它还没有可用方案(有就不必再解析一遍)。
//
// 纯函数,便于穷举(见 batch_serial_test.go)。
func canPreparse(prevPlanReady, llmBusy, nextNeedsPlan bool) bool {
	return nextNeedsPlan && prevPlanReady && !llmBusy
}

// preparseNextScript 在**安全窗口**里预解析下一集的剧本分镜(纯 LLM,与当前集的显卡阶段并行)。
//
// 窗口由 canPreparse 定义;**不满足就什么都不做** —— 轮到下一集时它自己按正常流程解析
// (产线是逐集串行的:一集的解析只属于那一集,见 novelBatch 的逐集串行铁律)。
// 窗口通常出现在"本集已进入资产/渲染、LLM 空出来了"的时候,于是轮到下一集时它已经有方案,
// 直接从资产阶段起步,等于每集白赚一段解析时间。
//
// 返回值=这次真的启动了(产线用它记"一集只试一次")。
func (s *Server) preparseNextScript(cur int) bool {
	batch.mu.Lock()
	curID, next := "", ""
	if cur >= 0 && cur < len(batch.projects) {
		curID = batch.projects[cur]
	}
	if cur+1 < len(batch.projects) {
		next = batch.projects[cur+1]
	}
	batch.mu.Unlock()
	if next == "" {
		return false
	}
	ok := canPreparse(
		hasUsablePlan(s.store.Dir(curID)),               // 上一集方案已落盘(跨集角色锚定读得到)
		s.pipe.RunningPhase(pipeline.PhaseScript) != "", // 此刻没有别的 LLM 解析在跑
		!hasUsablePlan(s.store.Dir(next)),               // 下一集确实还缺方案
	)
	if !ok {
		return false
	}
	if err := s.pipe.Start(next, pipeline.PhaseScript, nil, "", 0); err != nil {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: fmt.Sprintf("⚠ 下一集《%s》的剧本预解析没抢到(%v)—— 不影响主流程,轮到它时按正常顺序解析", next, err)})
		return false
	}
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
		Message: fmt.Sprintf("🧠 本集方案已就绪、LLM 空出来了:顺手把下一集《%s》的**剧本分镜**解析掉"+
			"(纯 LLM,不占显卡,与当前渲染并行;轮到它时直接从资产阶段起步)", next)})
	return true
}

// hasUsablePlan 这一集是否已有可用的 plan.json（有就跳过解析、直接从资产阶段起步）。
func hasUsablePlan(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "plan", "plan.json"))
	return err == nil && !st.IsDir() && st.Size() > 0
}
