package server

import (
	"os"
	"strings"
	"testing"
)

// 手动跑剧本/全流程之前，平台必须**自己**把剧本这一步补上
// （2026-09-30 用户：「这些不都是平台这边的LLM的事吗？咋还需要别人提供？」）。
//
// 剧本是平台自己的 LLM 会话产出的（`ensureEpisodeBrief`），此前只在批量产线那条路上被调用：
// 手动只跑「剧本分镜」时，若这一集还没轮到过（`【本集剧情】` 空），分镜就会在没有剧本的情况下硬跑，
// 预检再报「剧本缺 N 个必需节」，把人绕进"去补哪几节"。
//
// 判据落在**接线**上（这是真跑覆盖不到的地方，见 commit 537b1f3 的说明）。
func TestRunProjectEnsuresBriefFirst(t *testing.T) {
	raw, err := os.ReadFile("handlers_project.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	// ① run 入口必须调 ensureEpisodeBrief
	if !strings.Contains(src, "s.ensureEpisodeBrief(id)") {
		t.Error("run 入口没有先确保本集剧本 —— 手动跑剧本分镜会撞空剧本")
	}
	// ② 而且要**在启动管线之前**（顺序反了就白补）
	iBrief := strings.Index(src, "s.ensureEpisodeBrief(id)")
	iStart := strings.Index(src, "s.pipe.Start(id, in.Phase")
	if iBrief < 0 || iStart < 0 || iBrief > iStart {
		t.Error("补剧本必须在 s.pipe.Start 之前调用")
	}
	// ③ 只对剧本相关阶段做（别的阶段不该顺手跑一次 LLM 会话）
	if !strings.Contains(src, "in.Phase == pipeline.PhaseScript") {
		t.Error("补剧本没有限定在剧本相关阶段")
	}
	// ④ 空剧本与"缺节"要分开报（用户被那句"缺 5 个必需节"绕住过）—— 判据在剧本那一步的源码里
	jobs, err := os.ReadFile("novels_jobs.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jobs), "briefLooksEmpty(out)") {
		t.Error("空剧本没有单独报出来")
	}
}
