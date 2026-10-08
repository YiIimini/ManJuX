package qc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/asr"
	"github.com/YiIimini/ManJuX/internal/project"
)

// zz_probe_speech_live_test.go 探针：拿真机片段逐镜转写 + 与计划台词对账（带时间点）。
// 只在有那份真机项目 + 本机装了 ASR 时跑；否则跳过（不假装验过）。
func TestZZProbeSpeechLive(t *testing.T) {
	tool := asr.Locate()
	if tool == "" {
		t.Skip("本机没有 ASR（D:\\Ai\\Tools\\ASR\\asr.cmd），跳过")
	}
	root := filepath.Join("..", "..", "data", "projects")
	id := "顺其自然（真实事迹短片）·第01集·顺其自然"
	dir := filepath.Join(root, "顺其自然（真实事迹短片）", id)
	if _, err := os.Stat(dir); err != nil {
		t.Skip("没有那份真机项目，跳过")
	}
	store, err := project.NewStore(root)
	if err != nil {
		t.Skip(err)
	}
	plan, err := store.LoadPlan(id)
	if err != nil {
		t.Fatal(err)
	}
	for i := range plan.Shots {
		sh := plan.Shots[i]
		clip := filepath.Join(dir, "clips", fmt.Sprintf("shot_%03d.mp4", sh.ID))
		if _, err := os.Stat(clip); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		res, err := asr.Transcribe(ctx, tool, clip, "medium")
		if err != nil {
			t.Logf("镜头 %d 转写失败: %v", sh.ID, err)
			cancel()
			continue
		}
		rep := asr.Compare(project.PlanSpokenTexts(&sh), res)
		cancel()
		t.Logf("=== 镜头 %d  计划=%v", sh.ID, project.PlanSpokenTexts(&sh))
		for _, sg := range res.Segments {
			t.Logf("    [%.2f-%.2f] %s", sg.Start, sg.End, sg.Text)
		}
		for _, l := range rep.Lines {
			t.Logf("    对账 %q sim=%.2f times=%d", l.Line, l.Sim, l.Times)
		}
		for _, f := range rep.Findings {
			t.Logf("    ⚠ %s: %s", f.Kind, f.Detail)
		}
	}
}
