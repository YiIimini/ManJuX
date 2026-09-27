package llm

import (
	"errors"
	"fmt"
	"testing"
)

// TestIsRateLimited 限流判定的口径：**各家的说法都要认，别把普通错误也算进来**。
//
// 为什么单独一位（2026-09-27 用户报障）：限流是**临时且全局**的（同一份 key 的所有调用
// 都受影响），与"这次调用本身有问题"处置完全不同 —— 前者该静默一段时间、只播报一次；
// 后者该逐条如实告警。判错一边的后果分别是"继续把接口打满"与"把真问题当成限流吞掉"。
func TestIsRateLimited(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"平台自己的 429", &statusError{code: 429, body: "too many"}, true},
		{"智谱免费档 1305（用户实测那条）", errors.New(`VLM HTTP 429: {"error":{"code":"1305","message":"该模型当前访问量过大，请您稍后再试"}}`), true},
		{"OpenAI 文案", errors.New("HTTP 429: rate_limit_exceeded"), true},
		{"通义 Throttling", errors.New("Throttling: Request was denied"), true},
		{"中文限流", errors.New("请求过于频繁，请稍后再试"), true},
		{"配额", errors.New("You exceeded your current quota"), true},
		{"普通 500 不算限流", errors.New("VLM HTTP 500: internal error"), false},
		{"解析失败不算限流", errors.New("认不出判定结果(既没有 SAME 也没有 DIFFERENT)"), false},
		{"未配置不算限流", errors.New("未配置视觉模型"), false},
		{"空错误", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRateLimited(tc.err); got != tc.want {
				t.Errorf("IsRateLimited = %v,期望 %v（%v）", got, tc.want, tc.err)
			}
		})
	}
	// 包装过的错误也要认（上层常用 fmt.Errorf("%w") 加语境）
	wrapped := fmt.Errorf("定妆照自检失败: %w", &statusError{code: 429, body: "x"})
	if !IsRateLimited(wrapped) {
		t.Error("被 fmt.Errorf 包装后仍应认出 429")
	}
}

// TestAppendJSONHint json 模式必须让提示词里出现 "json"（DeepSeek 实测硬要求）。
func TestAppendJSONHint(t *testing.T) {
	// 有 system → 追加在它后面
	msgs := []map[string]any{{"role": "system", "content": "你是审片员"}, {"role": "user", "content": "看图"}}
	out := appendJSONHint(msgs)
	if s, _ := out[0]["content"].(string); !contains(s, "json") {
		t.Errorf("system 提示词里要出现 json：%v", out[0])
	}
	if len(out) != 2 {
		t.Errorf("只该改第一条 system，不该多出消息：%d", len(out))
	}
	// 没有 system → 新加一条在最前
	out2 := appendJSONHint([]map[string]any{{"role": "user", "content": "看图"}})
	if len(out2) != 2 || out2[0]["role"] != "system" || !contains(fmt.Sprint(out2[0]["content"]), "json") {
		t.Errorf("没有 system 时要补一条在最前：%v", out2)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
