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
TITLE: …
CHAPTERS: …
BRIEF:
…
- 台词照抄原文即可:「」""'' 任何引号都能用,分节格式没有转义问题;唯一要求是每集必须以 ===EP N=== 标记开头、TITLE/CHAPTERS/BRIEF 行标签齐全。
- 拆集规则:每集对应一个 56-84 秒短视频(8-12 镜);**一集=一个完整剧情事件弧**:开场钩子→冲突/过程→结尾悬念或结果,观众单看一集能看懂发生了什么;默认 1 章正好 1 集,只有信息密度极高的章才拆 2 集(在自然剧情转折处切);严禁把一个事件拆碎到多集(半件事一集=观众不知所云),也严禁多章事件压进一集;总集数由章数决定,不注水不碎拆不丢主线;集与集剧情连续、brief 互相衔接。
- 完整改编:主线与影响剧情走向的支线全部保留,不得舍弃情节;一般 1-2 章一集(单章信息量大可拆多集),集数由内容体量决定;每集 brief 中的台词必须是原文关键句。
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
		jobSet(name, "running", 0, "AI 拆集通读中…")
		series, _, err := s.splitNovelToProjects(context.Background(), name, in.Text, in.Max, "", nil)
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
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `·第\d+集`)
	var eps []string
	for _, p := range s.store.List() {
		if re.MatchString(p.ID) {
			eps = append(eps, p.ID)
		}
	}
	if len(eps) == 0 {
		writeErr(w, 404, fmt.Errorf("未找到「%s·第N集」系列项目,请先整本拆集", prefix))
		return
	}
	sort.Slice(eps, func(i, j int) bool { return epNo(eps[i]) < epNo(eps[j]) })

	if !s.startBatch(prefix, eps) {
		writeErr(w, 409, fmt.Errorf("批量产线已在运行(%s 第 %d/%d 集)", batch.prefix, batch.cur, len(batch.projects)))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "total": len(eps)})
}

func (s *Server) runNovelBatch(prefix string, eps []string) {
	defer func() {
		batch.mu.Lock()
		batch.running = false
		batch.mu.Unlock()
	}()
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
		Message: fmt.Sprintf("▶ 批量产线启动:《%s》首批 %d 集,逐集串行(分镜→定妆→渲染→质检→成片)%s", prefix, len(eps),
			func() string {
				if prodAlive(prefix) {
					return ";拆集仍在进行,新集将实时入队"
				}
				return ""
			}())})
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

		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
			Message: fmt.Sprintf("▶ 批量产线 %d/%d:开始《%s》", i+1, batchTotal(), ep)})
		if s.pipe.Running(ep) != nil {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", Message: fmt.Sprintf("《%s》已在运行,跳过", ep)})
			continue
		}
		if err := s.pipe.Start(ep, pipeline.PhaseAll, nil, "", 0); err != nil {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "error",
				Message: fmt.Sprintf("✕ 批量产线暂停于 %d/%d:《%s》启动失败:%v(不自动跳集)", i+1, batchTotal(), ep, err)})
			return
		}
		// 等本集跑完(轮询 Running;单集上限 2.5 小时兜底)
		deadline := time.Now().Add(150 * time.Minute)
		for time.Now().Before(deadline) {
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
