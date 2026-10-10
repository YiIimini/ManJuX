package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 视觉预算：空正文（思考模型把 completion 吃干净）必须**自动加预算重试一次**，而不是直接判"判不了"。
//
// 【真机实测，2026-10-10】平台自己的 VLM 是推理模型（`deepseek-flash`）：同一张图、同一份 5 条评分表，
// max_tokens=2048/4000 ⇒ **正文 0 字**（finish=length，推理 token 占满），8192 才出正文（思考占 5347）。
// 也就是说旧预算的余量只有 1.5 倍 —— 评分表再长一点或一次发多张图就会整条"判不了"。
//
// 这条判的是**行为**（真的重试了、且重试时预算更大），不是"常量是多少"：
// 把 `budget = visionMaxTokensRetry` 那一步删掉，这条测试立刻红。
func TestChatVisionRetriesWithBiggerBudgetOnEmptyContent(t *testing.T) {
	var budgets []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		b, _ := body["max_tokens"].(float64)
		budgets = append(budgets, int(b))
		w.Header().Set("Content-Type", "application/json")
		if len(budgets) == 1 {
			// 第一次：推理把预算吃干净、正文为空 —— 真机那条链路的形态。
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{
					"message":       map[string]any{"role": "assistant", "content": "", "reasoning_content": "想了想很久…"},
					"finish_reason": "length",
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"role": "assistant", "content": "1) 构图：头约占 1/3 …"},
				"finish_reason": "stop",
			}},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key", "deepseek-flash", 30)
	got, err := c.ChatVision(context.Background(), "你是美术指导", []VisionContent{{Role: "user", Text: "评审这张图"}}, false)
	if err != nil {
		t.Fatalf("空正文后该自动加预算重试，实际直接报错：%v", err)
	}
	if got != "1) 构图：头约占 1/3 …" {
		t.Errorf("重试后应返回正文，实际 %q", got)
	}
	if len(budgets) != 2 {
		t.Fatalf("该正好重试一次（共 2 次请求），实际 %d 次：%v", len(budgets), budgets)
	}
	if budgets[0] != visionMaxTokens {
		t.Errorf("起步预算该是 %d，实际 %d", visionMaxTokens, budgets[0])
	}
	if budgets[1] <= budgets[0] {
		t.Errorf("重试必须给**更大**的预算，实际 %v（这才是修法本身，不是把错误信息改好看）", budgets)
	}
	if budgets[1] != visionMaxTokensRetry {
		t.Errorf("重试预算该是 %d，实际 %d", visionMaxTokensRetry, budgets[1])
	}
}

// 反向针：两次都空（连重试也吃干净）时必须**如实报错**并带上 finish 与预算，不许静默返回空串
// —— "判不了"要让上游看得见，否则质检会把空结论当"没有意见"。
func TestChatVisionFailsLoudWhenStillEmptyAfterRetry(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": ""},
				"finish_reason": "length",
			}},
		})
	}))
	defer srv.Close()
	c := New(srv.URL, "test-key", "deepseek-flash", 30)
	got, err := c.ChatVision(context.Background(), "", []VisionContent{{Role: "user", Text: "看图"}}, false)
	if err == nil {
		t.Fatalf("两次都空必须报错，实际返回 %q", got)
	}
	if got != "" {
		t.Errorf("报错时不该返回内容，实际 %q", got)
	}
	if n != 2 {
		t.Errorf("该只重试一次（共 2 次请求），实际 %d 次", n)
	}
	if !contains(err.Error(), "finish=length") {
		t.Errorf("错误里该带上 finish 原因，实际：%v", err)
	}
}
