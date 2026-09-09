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
- 输出严格 JSON(无 markdown):{"book":"书名(中文,≤10字)","style":"英文全局视觉风格","episodes":[
   {"ep":1,"title":"本集小标题(中文,≤8字)","brief":"本集完整剧情大纲,中文 300-500 字,必须自足:仅凭此 brief 就能写出本集分镜。包含:开场钩子、关键情节、重要台词原文、情绪转折、结尾钩子"}]}
- 拆集规则:每集对应一个 45-75 秒短视频(6-10 镜);按叙事节拍切分(一集=一个完整小事件);总集数由内容体量决定,不注水不丢主线;集与集剧情连续、brief 互相衔接。
- 长文只拆主线,支线可并集或舍弃;每集 brief 中的台词必须是原文关键句。`

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
		writeErr(w, 409, fmt.Errorf("《%s》已创建过视频项目,请勿重复创建", name))
		return
	}
	// 异步后台拆集建项目:立即返回,进度看全局日志(30 秒心跳+逐集创建事件),
	// 首页项目列表随 novel 事件实时刷新——弹窗不再同步等待 LLM(2-6 分钟)。
	go func() {
		jobSet(name, "running", 0, "AI 拆集通读中…")
		series, _, err := s.splitNovelToProjects(context.Background(), name, in.Text, in.Max)
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
		Message: fmt.Sprintf("▶ 批量产线启动:《%s》共 %d 集,逐集串行(分镜→定妆→渲染→质检→成片)", prefix, len(eps))})
	for i, ep := range eps {
		batch.mu.Lock()
		if batch.stop {
			batch.mu.Unlock()
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", Message: "⏹ 批量产线已停止"})
			return
		}
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
				Message: fmt.Sprintf("⏩ 批量产线 %d/%d:《%s》已成片,跳过(断点续跑)", i+1, len(eps), ep)})
			continue
		}

		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
			Message: fmt.Sprintf("▶ 批量产线 %d/%d:开始《%s》", i+1, len(eps), ep)})
		if s.pipe.Running(ep) != nil {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", Message: fmt.Sprintf("《%s》已在运行,跳过", ep)})
			continue
		}
		if err := s.pipe.Start(ep, pipeline.PhaseAll, nil, "", 0); err != nil {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "error",
				Message: fmt.Sprintf("✕ 批量产线暂停于 %d/%d:《%s》启动失败:%v(不自动跳集)", i+1, len(eps), ep, err)})
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
				Message: fmt.Sprintf("⏹ 批量产线暂停于 %d/%d:《%s》已被中止,不自动跳集(重新点「批量产线」从中断处续跑)", i+1, len(eps), ep)})
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
		Message: fmt.Sprintf("🎉 批量产线全部完成:《%s》%d 集处理结束,成片见各集项目/作品库", prefix, len(eps))})
}

func epNo(name string) int {
	re := regexp.MustCompile(`·第(\d+)集`)
	if m := re.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 9999
}
