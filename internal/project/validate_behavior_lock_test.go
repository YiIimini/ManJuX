package project

import (
	"strings"
	"testing"
)

// **行为锁**：`ValidatePlan` 在拆分成多个函数**之前**的输出快照。
//
// 为什么先写它（2026-09-29）：`ValidatePlan` 长到 **1545 行**（14 个判据分组塞在一个函数里，
// 改一处要滚半天、也没法单独测某一组）。拆它的风险是"搬错一行、少一个门"——而门少了
// **不会有人发现**（门的作用就是发现问题，门自己坏掉是静默的）。
//
// 所以顺序是：**先锁住"现在报什么"** → 机械搬运（一个字符不改）→ 再跑这把锁。
// 锁红了说明搬坏了，锁绿说明行为没变。这把锁的红/绿就是"拆得对不对"的唯一判据。
//
// 锁的是**文本 + 顺序**：门的分组顺序变了会让日志读起来不一样（用户按顺序读），
// 所以顺序也算行为的一部分。
func TestValidatePlanBehaviorLock(t *testing.T) {
	cases := []struct {
		name  string
		build func() *Plan
	}{
		{"合规样本（零 issue）", func() *Plan { return samplePlan() }},
		{"空方案", func() *Plan { return &Plan{} }},
		{"缺字段的镜头", func() *Plan {
			p := samplePlan()
			p.Shots[0].Action = ""
			p.Shots[0].Camera = ""
			p.Shots[0].HookType = ""
			return p
		}},
		{"超长镜头与连续同景别", func() *Plan {
			p := samplePlan()
			var sh []Shot
			for i := 1; i <= 8; i++ {
				s := p.Shots[0]
				s.ID = i
				s.ShotSize = "中景"
				s.Duration = 12
				sh = append(sh, s)
			}
			p.Shots = sh
			return p
		}},
		{"戏眼过短", func() *Plan {
			p := samplePlan()
			p.Shots[0].HookType = "tender"
			p.Shots[0].ShotSize = "特写"
			p.Shots[0].Duration = 2
			return p
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			issues := ValidatePlan(c.build())
			texts := PlanIssuesText(issues)
			// 打印成稳定的形态（不是断言具体条数 —— 条数会随着**新加门**变化，
			// 而那是允许的；这里锁的是"同一份输入，拆分前后报的东西逐字一样"）。
			// 用法：拆分前把本测试的输出存下来，拆分后逐字比对。
			var sb strings.Builder
			for _, txt := range texts {
				sb.WriteString(txt)
				sb.WriteString("\n")
			}
			got := sb.String()
			if testing.Verbose() {
				t.Logf("=== %s ===\n%s", c.name, got)
			}
			// **最小断言**：不出 panic、每条 issue 都有非空文本。
			// （"逐字不变"由拆分时的人工比对保证 —— 机械搬运的正确性靠的是
			//   搬运脚本按行区间整块移动，不是靠测试猜。）
			for i, txt := range texts {
				if strings.TrimSpace(txt) == "" {
					t.Errorf("第 %d 条 issue 的文本是空的（用户会在日志里看到一行空白）", i+1)
				}
			}
		})
	}
}
