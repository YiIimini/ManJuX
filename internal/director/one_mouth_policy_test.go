package director

import (
	"strings"
	"testing"
)

// one_mouth_policy_test.go 分镜规则段里的**「一次只一张嘴」**（2026-10-12，平台规则）。
//
// 【为什么这条值得单独钉（用户给了一篇公众号实测文，要求按它排查"抢台词"）】
// 文章给的第一条是**硬规矩**：「别让好几个人同时处在正在说话的状态里」——
// 模型分不清谁在出声的时候，它会自己挑一个，挑错了就是观众看到的"抢词"。
// 做法是"一个画面里只留一张嘴"（正反打 / 反应镜 / 背影 / 侧身）。
//
// 平台这一侧此前只有**渲染前**的兜底（pipeline.EnsureSingleSpeakerClause：一镜里已有两位
// 画内说话人时补一条硬约束），而**产出侧**（让 LLM 写分镜的那一段）只写了
// "At most 2 speakers per shot." —— 那句话管的是**条数**，管不住"两个人在同一时刻都在说话"。
// 这条门钉住产出侧也把话说清楚：不该等到渲染前才救。
//
// 与官方口径的关系（写清楚免得被当成官方要求）：官方 §4.4 允许 `(S1,S2)` 这种**同时说话**的
// 复合 id，所以这一条是**平台规则、不是官方的** —— 文案里必须标明，别冒充官方引用。
func TestShotRulesForbidSimultaneousSpeakers(t *testing.T) {
	rules := sysShotRules
	if strings.TrimSpace(rules) == "" {
		t.Fatal("sysShotRules 是空的 —— 规则段没被 embed 进来")
	}
	for _, want := range []string{
		"ONE SPEAKING MOUTH AT A TIME",
		"never write two people as speaking at the same moment",
		"one talking mouth per frame",
		"each line its own inner shot",
		// 标成平台规则（不冒充官方）：
		"平台规则",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("分镜规则段缺少 %q —— 产出侧不写这条，抢词只能等渲染前兜底", want)
		}
	}
	// 原有的"每镜最多 2 位说话人"必须还在（新条款是加严，不是替换）。
	if !strings.Contains(rules, "At most 2 speakers per shot") {
		t.Error("原有的「At most 2 speakers per shot」被弄丢了 —— 那是条数的上限，与新条款不是一回事")
	}
	// 单轮与分批**共用同一段**（director.go 的 var 说明）：两处都不许漏。
	if !strings.Contains(SystemPrompt, "ONE SPEAKING MOUTH AT A TIME") {
		t.Error("SystemPrompt（单轮）里没有这条 —— 单轮那条路会漏掉")
	}
	if !strings.Contains(batchSystemPrompt, "ONE SPEAKING MOUTH AT A TIME") {
		t.Error("batchSystemPrompt（分批）里没有这条 —— 分批那条路会漏掉")
	}
}

// 反面：这条规则不许被写成官方要求（官方 §4.4 明确允许 `(S1,S2)` 同时说话）。
func TestOneMouthRuleIsMarkedAsPlatformRule(t *testing.T) {
	idx := strings.Index(sysShotRules, "ONE SPEAKING MOUTH AT A TIME")
	if idx < 0 {
		t.Fatal("规则段里没有这条")
	}
	line := sysShotRules[idx:]
	if k := strings.Index(line, "\n"); k >= 0 {
		line = line[:k]
	}
	if !strings.Contains(line, "平台规则") {
		t.Errorf("这条必须在本行标成**平台规则**（官方 §4.4 允许 (S1,S2) 同时说话）：\n%s", line)
	}
	if strings.Contains(line, "official §") {
		t.Errorf("不许把这条写成官方要求（官方没有它）：\n%s", line)
	}
}
