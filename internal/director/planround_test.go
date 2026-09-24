package director

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/llm"
)

// fakeLLM 起一个假 /chat/completions:按序返回给定正文与 finish_reason,
// 并把每次请求落进 calls 供断言。
func fakeLLM(t *testing.T, replies []string, finishes []string) (*llm.Client, *[]string) {
	t.Helper()
	var got []string
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []llm.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var last string
		if len(body.Messages) > 0 {
			last = body.Messages[len(body.Messages)-1].Content
		}
		got = append(got, last)
		i := n
		n++
		if i >= len(replies) {
			i = len(replies) - 1
		}
		finish := "stop"
		if i < len(finishes) {
			finish = finishes[i]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"role": "assistant", "content": replies[i]},
				"finish_reason": finish,
			}},
			"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 200, "total_tokens": 300},
		})
	}))
	t.Cleanup(srv.Close)
	return llm.New(srv.URL, "test-key", "test-model", 60), &got
}

const goodPlan = `{"title":"三万六千块砖","logline":"少年石匠刻下第一块砖","style":"guoman","characters":[],"scenes":[],"segments":[{"from":1,"to":3,"brief":"开场"}]}`

// TestCallPlanRoundRepairsBadJSON 模型先回了一堆说明(非 JSON)时,修复轮必须救回来。
// 这正是 2026-09-24 那条"规划轮输出解析失败"最可能的形态之一。
func TestCallPlanRoundRepairsBadJSON(t *testing.T) {
	client, calls := fakeLLM(t, []string{"我先想想这个故事该怎么拍,大致分三段…", goodPlan}, nil)
	head, err := callPlanRound(context.Background(), client, "sys", "user", nil)
	if err != nil {
		t.Fatalf("修复轮应救回来,实得: %v", err)
	}
	if head.Title != "三万六千块砖" || len(head.Segments) != 1 {
		t.Errorf("解析结果不对:%+v", head)
	}
	if len(*calls) != 2 {
		t.Fatalf("应恰好调用两轮,实际 %d", len(*calls))
	}
	if !strings.Contains((*calls)[1], "Return ONLY the JSON object") {
		t.Errorf("第二轮应是修复指令,实际:%s", (*calls)[1][:120])
	}
}

// TestCallPlanRoundRepairsTruncation 被 max_tokens 截断时:即使本地能把截断的
// 尾巴补成一个"能解析"的对象,也必须再问一轮把方案问全 ——
// 补出来的那份后面的段全是占位 brief,直接采用等于悄悄降质。
func TestCallPlanRoundRepairsTruncation(t *testing.T) {
	truncated := `{"title":"砖","segments":[{"from":1,"to":3,"brief":"开场"},{"from":4,"to":6,"br`
	client, calls := fakeLLM(t, []string{truncated, goodPlan}, []string{"length", "stop"})
	head, err := callPlanRound(context.Background(), client, "sys", "user", nil)
	if err != nil {
		t.Fatalf("截断后修复轮应救回来,实得: %v", err)
	}
	if head.Logline == "" {
		t.Errorf("应采用第二轮问全的方案,实得 %+v", head)
	}
	if len(*calls) != 2 {
		t.Fatalf("截断场景应再问一轮(共 2 轮),实际 %d", len(*calls))
	}
	if !strings.Contains((*calls)[1], "CUT OFF by the output limit") {
		t.Errorf("截断场景的修复指令应点名截断并给出「缩短」要求,实际:%s", (*calls)[1][:160])
	}
}

// TestCallPlanRoundFallsBackToPartialPlan 连修复轮也拿不回完整方案时:
// 用补全后的那份继续跑,但**必须**通过 OnNote 说出来 —— 静默降级等于骗人,
// 直接报错又会让 48 集产线停在第 1 集(用户报障的原始痛点)。
func TestCallPlanRoundFallsBackToPartialPlan(t *testing.T) {
	truncated := `{"title":"砖","segments":[{"from":1,"to":3,"brief":"开场"},{"from":4,"to":6,"br`
	client, _ := fakeLLM(t, []string{truncated, "抱歉,我这次还是没写完"}, []string{"length", "length"})
	var notes []string
	head, err := callPlanRound(context.Background(), client, "sys", "user", func(f string, a ...any) {
		notes = append(notes, fmt.Sprintf(f, a...))
	})
	if err != nil {
		t.Fatalf("兜底路径不该报错(否则产线停在第 1 集): %v", err)
	}
	if head.Title != "砖" || len(head.Segments) != 1 {
		t.Errorf("应退回补全后的部分方案,实得 %+v", head)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "截断") {
		t.Errorf("降级必须发提示,实得 %v", notes)
	}
}

// TestCallPlanRoundErrorIsDiagnosable 两轮都救不回来时,错误里必须带
// finish_reason / token 用量 / 原文摘录 —— 否则日志里又是一句无从下手的"解析失败"。
func TestCallPlanRoundErrorIsDiagnosable(t *testing.T) {
	client, _ := fakeLLM(t,
		[]string{"抱歉,我无法完成。", "还是不行。"},
		[]string{"length", "length"})
	_, err := callPlanRound(context.Background(), client, "sys", "user", nil)
	if err == nil {
		t.Fatal("应当失败")
	}
	msg := err.Error()
	// 非截断场景要能一路走到底:这里两轮都不是 JSON,兜底也没有,必须报错;
	// 报错内容取**最后一次**回复(第一次的原文已无意义)
	for _, want := range []string{"finish=length", "completion=200 tokens", "原文摘录", "还是不行"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误信息缺少 %q:\n%s", want, msg)
		}
	}
	if !strings.Contains(msg, "max_tokens") {
		t.Errorf("截断场景应点名 max_tokens:\n%s", msg)
	}
}

// TestParsePlanHeadUnwrapsWrapper 模型把结果包了一层({“plan”:{…}})也要能认。
func TestParsePlanHeadUnwrapsWrapper(t *testing.T) {
	for _, raw := range []string{
		`{"plan":` + goodPlan + `}`,
		"```json\n{\"data\":" + goodPlan + "}\n```",
	} {
		head, _, err := parsePlanHead(raw)
		if err != nil {
			t.Fatalf("应能拆一层包装: %v(%s)", err, raw)
		}
		if head.Title != "三万六千块砖" {
			t.Errorf("解析结果不对:%+v", head)
		}
	}
}

// TestParseShotsBatchUnwrapsWrapper 分镜批次同理。
func TestParseShotsBatchUnwrapsWrapper(t *testing.T) {
	raw := `{"result":{"shots":[{"id":3,"h3_prompt":"A"},{"id":4,"h3_prompt":"B"}]}}`
	shots, err := parseShotsBatch(raw, 3, 7)
	if err != nil {
		t.Fatalf("应能拆一层包装: %v", err)
	}
	if len(shots) != 2 {
		t.Errorf("镜头数不对:%+v", shots)
	}
}

// TestSnippetKeepsBothEnds 摘录必须两端都留:截断线索在尾、格式线索在头。
func TestSnippetKeepsBothEnds(t *testing.T) {
	s := snippet(strings.Repeat("头", 50)+"中段"+strings.Repeat("尾", 50), 10)
	if !strings.Contains(s, "头") || !strings.Contains(s, "尾") || !strings.Contains(s, "中略") {
		t.Errorf("摘录两端不齐:%s", s)
	}
	if n := len([]rune(s)); n > 60 {
		t.Errorf("摘录过长(%d 字),会把日志刷爆", n)
	}
	_ = fmt.Sprint()
}
