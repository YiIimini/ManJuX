package project

import (
	"strings"
	"testing"
)

// 「片段 → 小镜头」两层结构的守卫（用户 2026-09-28 架构）。
//
// 用户原话：「一个分镜里可以包含多个小分镜，小分镜里包含运镜」。
// 平台此前是单层：小镜头只作为文本写在 `h3_prompt` 的多镜形式里。
func TestCutsTwoLayer(t *testing.T) {
	// ── ① 从正文读回结构（存量方案与新技能侧产物只有正文）───────────────────
	const doc = "detailed_description:\nA cinematic film.\n" +
		"[Shot 1] wide shot of the courtyard at dusk.\n" +
		"[Shot 2] At 00:02.000, the camera pushes in on the blade.\n" +
		"[Shot 3] At 00:03.500, close-up of his hand; static shot."
	cuts := ParseCutsFromPrompt(doc)
	if len(cuts) != 3 {
		t.Fatalf("该从正文读回 3 刀，实际 %d：%+v", len(cuts), cuts)
	}
	if cuts[0].At != 0 || cuts[1].At != 2.0 || cuts[2].At != 3.5 {
		t.Errorf("时间点读错了：%+v", cuts)
	}

	// ── ② 只有一镜（或时间点不递增）时**不猜**，返回 nil ────────────────────
	if got := ParseCutsFromPrompt("detailed_description:\nA film.\n[Shot 1] wide shot."); got != nil {
		t.Errorf("单镜不该读出小镜头结构，实际 %+v", got)
	}
	if got := ParseCutsFromPrompt("x\n[Shot 1] a.\n[Shot 2] At 00:03.000, b.\n[Shot 3] At 00:01.000, c."); got != nil {
		t.Errorf("时间点不递增的正文不是规范的多镜形式，不该猜，实际 %+v", got)
	}

	// ── ③ 时长计算：每刀到下一刀为止，最后一刀用片段时长补足 ─────────────────
	sh := Shot{ID: 1, Duration: 5, Cuts: []Cut{{At: 0}, {At: 2}, {At: 3.5}}}
	if d := sh.CutSeconds(0); d != 2 {
		t.Errorf("第 1 刀该是 2 秒，实际 %.2f", d)
	}
	if d := sh.CutSeconds(2); d != 1.5 {
		t.Errorf("最后一刀该用片段时长补足（5−3.5=1.5），实际 %.2f", d)
	}

	// ── ④ 判据：合规的片段不该报 ───────────────────────────────────────────
	ok := &Plan{Shots: []Shot{{ID: 1, Duration: 5, Cuts: []Cut{
		{At: 0, Action: "他起身"}, {At: 2, Action: "手按刀柄"}}}}}
	if got := CutIssues(ok); len(got) != 0 {
		t.Errorf("合规的两刀片段不该报，实际 %v", got)
	}
	if got := CutCoverageIssues(ok); len(got) != 0 {
		t.Errorf("切了的片段不该报「没切」，实际 %v", got)
	}

	// ── ⑤ 判据：一镜到底的 5 秒片段必须报（这正是"拖沓"的成因）──────────────
	bare := &Plan{Shots: []Shot{{ID: 1, Duration: 5, H3Prompt: "x\n[Shot 1] a single continuous take."}}}
	if got := CutCoverageIssues(bare); len(got) != 1 {
		t.Errorf("5 秒 0 刀必须报，实际 %v", got)
	}
	// 4 秒及以下允许只有一刀（切无可切）
	short := &Plan{Shots: []Shot{{ID: 1, Duration: 4, H3Prompt: "x\n[Shot 1] a beat."}}}
	if got := CutCoverageIssues(short); len(got) != 0 {
		t.Errorf("4 秒片段不切是允许的，实际 %v", got)
	}

	// ── ⑥ 判据：刀长越界 / 首刀不在 0 / 超出片段时长 ────────────────────────
	bad := &Plan{Shots: []Shot{{ID: 1, Duration: 5, Cuts: []Cut{
		{At: 0.5}, {At: 4.8}, {At: 5.0}}}}}
	msgs := strings.Join(CutIssues(bad), " ")
	for _, want := range []string{"第一刀不在 0 秒", "每刀 1.5–4.0 秒", "之外", "时长之和"} {
		if !strings.Contains(msgs, want) {
			t.Errorf("该报「%s」，实际：%s", want, msgs)
		}
	}
	// 刀数超过上限也要报
	many := &Plan{Shots: []Shot{{ID: 1, Duration: 10, Cuts: []Cut{
		{At: 0}, {At: 2}, {At: 4}, {At: 6}, {At: 8}}}}}
	if got := strings.Join(CutIssues(many), " "); !strings.Contains(got, "超过上限") {
		t.Errorf("5 刀超过上限该报，实际：%s", got)
	}

	// ── ⑦ 回填：结构为空时从正文填，已有结构时不覆盖（幂等）────────────────
	p := &Plan{Shots: []Shot{{ID: 1, Duration: 5, H3Prompt: doc}}}
	if n := p.FillCutsFromPrompt(); n != 1 {
		t.Errorf("该回填 1 个片段，实际 %d", n)
	}
	if n := p.FillCutsFromPrompt(); n != 0 {
		t.Errorf("已有结构时不该再回填（幂等），实际 %d", n)
	}

	// ── ⑧ 渲染回官方多镜形式（首镜不写时间戳，官方 §5.2）────────────────────
	out := CutsToPromptSection([]Cut{
		{At: 0, Action: "wide shot", Camera: "Static Shot"},
		{At: 2, Action: "close on the blade", Camera: "The camera pushes in"},
	})
	if !strings.Contains(out, "[Shot 1]") || !strings.Contains(out, "[Shot 2] At 00:02.000,") {
		t.Errorf("该渲染成官方多镜形式（首镜无时间戳、其余带 At），实际：\n%s", out)
	}
	if strings.Contains(strings.Split(out, "\n")[0], "At ") {
		t.Errorf("第一个镜头不该写时间戳（官方 §5.2：它从 0 开始）：\n%s", out)
	}
}
