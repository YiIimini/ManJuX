package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// zz_probe_voice_live_test.go 探针：拿真机 plan 走**真实装配**，逐句打印"绑成了哪条声线"。
func TestZZProbeVoiceBinding(t *testing.T) {
	root := filepath.Join("..", "..", "data", "projects")
	id := "顺其自然（真实事迹短片）·第01集·顺其自然"
	dir := filepath.Join(root, "顺其自然（真实事迹短片）", id)
	if _, err := os.Stat(dir); err != nil {
		t.Skip("没有真机项目，跳过探针")
	}
	store, err := project.NewStore(root)
	if err != nil {
		t.Skip(err)
	}
	proj, ok := store.Get(id)
	if !ok {
		t.Skip("项目读不到")
	}
	plan, err := store.LoadPlan(id)
	if err != nil {
		t.Fatal(err)
	}
	prevRebound, prevSkip, prevUnres := plan.VoiceRebound, plan.SpeechPlacementSkipped, plan.VoiceUnresolved
	for i := range plan.Shots {
		sh := plan.Shots[i]
		out := ShotRenderPrompt(proj, plan, &plan.Shots[i])
		t.Logf("=== 镜 %-3d 重绑 +%d 未认 +%d 清单跳过 +%d 清单在=%v",
			sh.ID, plan.VoiceRebound-prevRebound, plan.VoiceUnresolved-prevUnres,
			plan.SpeechPlacementSkipped-prevSkip, strings.Contains(out, speechPlacementMarker))
		prevRebound, prevSkip, prevUnres = plan.VoiceRebound, plan.SpeechPlacementSkipped, plan.VoiceUnresolved
		cut := out
		if k := strings.Index(cut, onScreenTextMarker); k >= 0 {
			cut = cut[:k]
		}
		// 每句：<d> 之前那一行（引述）
		for _, part := range strings.Split(cut, "<d>")[1:] {
			j := strings.Index(part, "</d>")
			if j < 0 {
				continue
			}
			line := strings.TrimSpace(part[:j])
			idx := strings.Index(cut, "<d>"+line+"</d>")
			head := ""
			if idx > 0 {
				ls := strings.LastIndex(cut[:idx], "\n") + 1
				head = strings.Join(strings.Fields(cut[ls:idx]), " ")
			}
			t.Logf("    line=%q", line)
			t.Logf("      引述=%s", tailOf(head, 230))
		}
	}
}
