package pipeline

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// zz_probe_speech_placement_live_test.go 探针：拿真机 plan 走**真实装配**，看逐刀清单数出了什么。
func TestZZProbeSpeechPlacement(t *testing.T) {
	root := filepath.Join("..", "..", "data", "projects")
	dir := filepath.Join(root, "顺其自然（真实事迹短片）", "顺其自然（真实事迹短片）·第01集·顺其自然")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("没有真机项目，跳过探针")
	}
	store, err := project.NewStore(root)
	if err != nil {
		t.Skip(err)
	}
	id := "顺其自然（真实事迹短片）·第01集·顺其自然"
	proj, ok := store.Get(id)
	if !ok {
		t.Skip("项目读不到")
	}
	plan, err := store.LoadPlan(id)
	if err != nil {
		t.Fatal(err)
	}
	for i := range plan.Shots {
		sh := plan.Shots[i]
		if sh.ID != 1 && sh.ID != 5 && sh.ID != 10 {
			continue
		}
		got := ShotRenderPrompt(proj, plan, &plan.Shots[i])
		t.Logf("==== shot %d cuts=%d dialogue=%d", sh.ID, len(sh.Cuts), len(sh.Dialogue))
		for _, m := range regexp.MustCompile(`(?s)<d>.*?</d>`).FindAllString(got, -1) {
			t.Logf("   D: %s", strings.ReplaceAll(m, "\n", " "))
		}
		for _, c := range cutSpeechMap(got) {
			t.Logf("   cut at=%-14q lines=%d %v", c.At, len(c.Lines), c.Lines)
		}
		// 被当成"台词"的占位符（省略号/空/纯标点）
		for _, c := range cutSpeechMap(got) {
			for _, l := range c.Lines {
				t2 := strings.TrimSpace(l)
				if t2 == "" || strings.Trim(t2, ".。…·、-—-～~()（）[]【】 ") == "" {
					t.Logf("   ⚠ 占位符被当成台词：%q", l)
				}
			}
		}
	}
}
