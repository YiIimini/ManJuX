package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 两个**只读端点**的契约（2026-09-29 审计 #8）。
//
// `GET /api/projects/{id}/source` 与 `/plan` 前端不用（前端自己就有这两个数据），
// 它们是给**外部/技能端**读项目内容的通路 —— 技能端要在平台之外核对产出的方案与原文。
// 正因为前端不调，它们**跑坏了也没人发现**：这一条就是补上那份缺失的覆盖。
func TestReadOnlyProjectEndpoints(t *testing.T) {
	s := newTestServer(t)

	// 造一个有方案的项目（照真实形态：**先建项目**，再存方案与源文本 ——
	// `store.Get` 走的是索引，只 SavePlan/SaveSource 是查不到这个项目的）
	const id = "只读端点测试·第01集"
	if _, err := s.store.Create(id, project.ProjectConfig{}); err != nil {
		t.Fatalf("建项目失败: %v", err)
	}
	plan := &project.Plan{
		Title: "只读端点测试",
		Shots: []project.Shot{{ID: 1, Scene: "s1", Action: "他推门", Duration: 5,
			H3Prompt: "detailed_description:\n[Shot 1] he opens the door."}},
	}
	if err := s.store.SavePlan(id, plan); err != nil {
		t.Fatalf("存方案失败: %v", err)
	}
	if err := s.store.SaveSource(id, "第一集原文：他推开门，屋里没有人。"); err != nil {
		t.Fatalf("存源文本失败: %v", err)
	}

	get := func(path string) (int, string) {
		req := httptest.NewRequest("GET", path, nil)
		// 来源守卫只放行本机（`guardAPI`）—— httptest 默认 Host 是 example.com，会被 403。
		// 这里显式设成本机地址，测的才是**端点本身**而不是守卫（守则有它自己的测试）。
		req.Host = "127.0.0.1:8790"
		req.RemoteAddr = "127.0.0.1:45678"
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	// ── /plan：返回方案（外部核对"平台生成的方案长什么样"）──────────────────
	code, body := get("/api/projects/" + id + "/plan")
	if code != 200 {
		t.Fatalf("/plan 该 200，实际 %d：%s", code, body[:min(200, len(body))])
	}
	var gotPlan map[string]any
	if err := json.Unmarshal([]byte(body), &gotPlan); err != nil {
		t.Fatalf("/plan 的响应必须是 JSON（外部要机读）：%v", err)
	}
	if shots, ok := gotPlan["shots"].([]any); !ok || len(shots) != 1 {
		t.Errorf("/plan 该把镜头原样带出来，实际 %v", gotPlan["shots"])
	}

	// ── /source：返回源文本（外部核对"这一集喂给模型的是哪段原文"）──────────
	code, body = get("/api/projects/" + id + "/source")
	if code != 200 {
		t.Fatalf("/source 该 200，实际 %d：%s", code, body[:min(200, len(body))])
	}
	if !strings.Contains(body, "他推开门") {
		t.Errorf("/source 该返回原文正文，实际：%s", body[:min(200, len(body))])
	}

	// ── 不存在的项目：**如实 404**，不是 200 + 空内容 ──────────────────────
	// （"读不到"与"内容为空"是两件事：外部工具照后者去判断会以为这个项目没有正文）
	for _, p := range []string{"/plan", "/source"} {
		code, _ := get("/api/projects/" + "不存在的项目·第99集" + p)
		if code == 200 {
			t.Errorf("%s 对不存在的项目该返回非 200（否则外部会当成「内容为空」），实际 200", p)
		}
		if code >= 500 {
			t.Errorf("%s 对不存在的项目不该是 5xx（那是服务端故障的语义），实际 %d", p, code)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
