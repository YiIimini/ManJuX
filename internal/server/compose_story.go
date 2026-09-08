// compose_story.go AI 现场创作:用户只给需求(一句话/一个点子),
// LLM 即时发挥写出完整故事源文本,直接落入项目「剧本」。
package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/pipeline"
)

const composeSystem = `你是顶尖短视频编剧。根据用户需求,现场创作一个完整的短故事:
- 中文正文 600-900 字,只输出故事本身(不要标题、不要解释、不要 markdown);
- 场景高度集中(2-4 个地点,适合短视频拍摄);
- 结构:第一段 3 秒钩子开场 → 中段一次意外/反转 → 结尾爽点或留钩;
- 人物 2-4 个,每个都有记忆点(外貌特征+性格标签),关键对白口语化、有梗;
- 动作与画面描写具象(可视化的),避免大段心理独白;
- 节奏快,信息密度高,每段都在推进。`

// handleComposeStory POST /api/projects/{id}/compose-story
// body {prompt: 创作需求(必填), style_hint: 风格偏好(可选)}
func (s *Server) handleComposeStory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.store.Get(id); !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	var in struct {
		Prompt    string `json:"prompt"`
		StyleHint string `json:"style_hint"`
	}
	if err := readBody(r, &in); err != nil || strings.TrimSpace(in.Prompt) == "" {
		writeErr(w, 400, fmt.Errorf("请填写创作需求(一句话或一个点子)"))
		return
	}

	g := s.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	if !client.Configured() {
		writeErr(w, 400, fmt.Errorf("LLM 未配置:请先在设置中填写"))
		return
	}

	// 心跳:推理模型创作 1-3 分钟,期间全局事件流可见
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
				s.bus.Publish(pipeline.Event{
					ProjectID: id, Phase: "compose", Level: "info",
					Message: fmt.Sprintf("⏳ AI 剧本创作中…已等待 %d 秒(推理模型深度创作,通常 1-3 分钟)", int(time.Since(start).Seconds())),
				})
			}
		}
	}()
	s.bus.Publish(pipeline.Event{
		ProjectID: id, Phase: "compose", Level: "info",
		Message: "AI 开始按需求现场创作故事:" + strings.TrimSpace(in.Prompt),
	})

	user := "创作需求:" + strings.TrimSpace(in.Prompt)
	if h := strings.TrimSpace(in.StyleHint); h != "" {
		user += "\n风格偏好:" + h
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(g.LLM.Timeout)*time.Second+time.Minute)
	defer cancel()
	story, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: composeSystem},
		{Role: "user", Content: user},
	}, false)
	if err != nil {
		s.bus.Publish(pipeline.Event{ProjectID: id, Phase: "compose", Level: "error", Message: "AI 创作失败:" + err.Error()})
		writeErr(w, 500, fmt.Errorf("AI 创作失败: %w", err))
		return
	}
	story = strings.TrimSpace(story)
	if len([]rune(story)) < 200 {
		writeErr(w, 500, fmt.Errorf("AI 创作内容过短(%d 字),请重试", len([]rune(story))))
		return
	}
	if err := s.store.SaveSource(id, story); err != nil {
		writeErr(w, 500, err)
		return
	}
	n := len([]rune(story))
	s.bus.Publish(pipeline.Event{
		ProjectID: id, Phase: "compose", Level: "success",
		Message: fmt.Sprintf("✔ AI 创作完成:%d 字,已写入「剧本」,可直接生成分镜", n),
	})
	writeJSON(w, 200, map[string]any{"ok": true, "chars": n})
}
