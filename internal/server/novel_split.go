// novel_split.go 整本小说批量产线:LLM 拆集 → 每集一个子项目(共享资产库,
// 同名角色/场景自动复用定妆照)→ 逐集串行跑全流程。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
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
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "连载"
	}
	g := s.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	if !client.Configured() {
		writeErr(w, 400, fmt.Errorf("LLM 未配置"))
		return
	}

	// 心跳:拆集要通读全文,推理模型数分钟
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
			case <-t.C:
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
					Message: fmt.Sprintf("⏳ 小说拆集通读中…已等待 %d 秒(整本拆解通常 2-6 分钟)", int(time.Since(start).Seconds()))})
			}
		}
	}()
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
		Message: fmt.Sprintf("开始整本拆集:%s(正文 %d 字)", name, len([]rune(in.Text)))})

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(g.LLM.Timeout)*time.Second+2*time.Minute)
	defer cancel()
	maxLine := ""
	if in.Max > 0 {
		maxLine = fmt.Sprintf("\n- 最多拆 %d 集。", in.Max)
	}
	raw, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: novelSplitSystem},
		{Role: "user", Content: "小说正文:\n\"\"\"\n" + in.Text + "\n\"\"\"" + maxLine + "\n输出拆集 JSON。"},
	}, true)
	if err != nil {
		writeErr(w, 500, fmt.Errorf("拆集失败: %w", err))
		return
	}
	var plan struct {
		Book     string `json:"book"`
		Style    string `json:"style"`
		Episodes []struct {
			Ep    int    `json:"ep"`
			Title string `json:"title"`
			Brief string `json:"brief"`
		} `json:"episodes"`
	}
	if err := jsonUnmarshalStrict(trimJSON(raw), &plan); err != nil || len(plan.Episodes) == 0 {
		writeErr(w, 500, fmt.Errorf("拆集结果解析失败,请重试"))
		return
	}
	sort.Slice(plan.Episodes, func(i, j int) bool { return plan.Episodes[i].Ep < plan.Episodes[j].Ep })

	// 系列名:用户填了主名用主名;留空则用 LLM 从小说内容识别出的书名
	series := name
	if series == "连载" && strings.TrimSpace(plan.Book) != "" {
		series = strings.TrimSpace(plan.Book)
	}
	// 每集建子项目:名「主名·第01集·标题」,源文本=书名+风格+前情+本集 brief
	created := []map[string]any{}
	for i, ep := range plan.Episodes {
		epName := fmt.Sprintf("%s·第%02d集·%s", series, ep.Ep, ep.Title)
		if _, ok := s.store.Get(epName); ok {
			created = append(created, map[string]any{"project": epName, "ep": ep.Ep, "title": ep.Title, "exists": true})
			continue
		}
		if _, err := s.store.Create(epName, project.ProjectConfig{}); err != nil {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
				Message: fmt.Sprintf("子项目创建失败「%s」:%v", epName, err)})
			continue
		}
		var prev string
		if i > 0 {
			p := plan.Episodes[i-1]
			prev = fmt.Sprintf("\n【前情提要】第%02d集《%s》:%s\n", p.Ep, p.Title, clampRunes(p.Brief, 120))
		}
		src := fmt.Sprintf("《%s》系列短视频 第%02d 集《%s》\n全局风格:%s%s\n\n【本集剧情】%s",
			name, ep.Ep, ep.Title, plan.Style, prev, ep.Brief)
		_ = s.store.SaveSource(epName, src)
		created = append(created, map[string]any{"project": epName, "ep": ep.Ep, "title": ep.Title})
	}
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
		Message: fmt.Sprintf("✔ 拆集完成:《%s》共 %d 集,已创建 %d 个子项目(共享资产库,角色自动复用)", series, len(plan.Episodes), len(created))})
	writeJSON(w, 200, map[string]any{"book": series, "style": plan.Style, "episodes": created})
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

	batch.mu.Lock()
	if batch.running {
		batch.mu.Unlock()
		writeErr(w, 409, fmt.Errorf("批量产线已在运行(%s 第 %d/%d 集)", batch.prefix, batch.cur, len(batch.projects)))
		return
	}
	batch.prefix, batch.projects, batch.cur, batch.stop, batch.running = prefix, eps, 0, false, true
	batch.mu.Unlock()

	go s.runNovelBatch(prefix, eps)
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

		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
			Message: fmt.Sprintf("▶ 批量产线 %d/%d:开始《%s》", i+1, len(eps), ep)})
		if s.pipe.Running(ep) != nil {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", Message: fmt.Sprintf("《%s》已在运行,跳过", ep)})
			continue
		}
		if err := s.pipe.Start(ep, pipeline.PhaseAll, nil, "", 0); err != nil {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "error", Message: fmt.Sprintf("《%s》启动失败:%v", ep, err)})
			continue
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

