package llm

import (
	"testing"
)

// LLM 用量必须**真的被收到**（2026-09-29 审计 #9）。
//
// 背景：`Usage` 从 2026-09-24 起就被解析出来了（当初为了查"输出被 max_tokens 截断"），
// 但**全平台没有任何地方读它** —— 用户看不到"这一集花了多少"。
// 现在有两个收口点：Client 自己的累计，以及**包级钩子**（14 个创建点共用一处）。
func TestUsageAccounting(t *testing.T) {
	// ① Client 自己的累计
	c := New("http://127.0.0.1:1/v1", "k", "m", 5)
	c.recordUsage(Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150})
	c.recordUsage(Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30})
	tot, n := c.UsageTotal()
	if n != 2 || tot.TotalTokens != 180 || tot.PromptTokens != 120 || tot.CompletionTokens != 60 {
		t.Errorf("累计错了：calls=%d total=%+v", n, tot)
	}

	// ② 包级钩子：一处挂、全部 Client 都报
	var got []Usage
	SetGlobalUsageHook(func(u Usage) { got = append(got, u) })
	defer SetGlobalUsageHook(nil)
	c2 := New("http://127.0.0.1:1/v1", "k", "m", 5)
	c2.recordUsage(Usage{TotalTokens: 7})
	if len(got) != 1 || got[0].TotalTokens != 7 {
		t.Errorf("包级钩子该收到 1 次用量，实际 %+v", got)
	}

	// ③ 钩子被清零后不再报（SetGlobalUsageHook(nil) 要真的停掉，不是留个空指针）
	SetGlobalUsageHook(nil)
	c2.recordUsage(Usage{TotalTokens: 9})
	if len(got) != 1 {
		t.Errorf("取消钩子后不该再收到（实际多出 %d 条）", len(got)-1)
	}

	// ④ `SetUsageHook` 是每个 Client 一份：只影响自己，清零累计
	var per []Usage
	c3 := New("http://127.0.0.1:1/v1", "k", "m", 5)
	c3.SetUsageHook(func(u Usage) { per = append(per, u) })
	c3.recordUsage(Usage{TotalTokens: 3})
	if len(per) != 1 {
		t.Errorf("Client 级钩子该收到 1 次，实际 %d", len(per))
	}
	// 清零：重新挂一次，累计从头开始
	c3.SetUsageHook(func(Usage) {})
	if _, n := c3.UsageTotal(); n != 0 {
		t.Errorf("重新挂钩子该清零累计（新一轮工作的语义），实际 calls=%d", n)
	}
}
