package pipeline

import (
	"os"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 参数按能力过滤 + 告警(审计 P1-2)。
//
// 守两件事:① 做不到的参数被跳过时**说出来**(旧实现静默);② 管线里不再靠
// 引擎名做能力判断(字符串比较在新增引擎时必漏)。
func TestUnsupportedParamNote(t *testing.T) {
	rp := project.RenderParams{Seam: true, CondCache: true}
	note := unsupportedParamNote(engine.CapsOf("api"), rp)
	if note == "" {
		t.Fatal("云端 + 开了接缝/条件缓存时必须给出提示,否则用户以为生效了")
	}
	for _, want := range []string{"镜间连续接缝", "条件缓存", "本地 ComfyUI"} {
		if !strings.Contains(note, want) {
			t.Errorf("提示里应包含 %q,实际 %q", want, note)
		}
	}
	// 本地引擎:全部支持,不该有噪声
	if n := unsupportedParamNote(engine.CapsOf("comfy"), rp); n != "" {
		t.Errorf("本地引擎不该报「不支持」,实际 %q", n)
	}
	// 用户没开这些参数时也不该提示
	if n := unsupportedParamNote(engine.CapsOf("api"), project.RenderParams{}); n != "" {
		t.Errorf("没设过的参数不该刷告警,实际 %q", n)
	}
}

// TestPipelineDoesNotBranchOnEngineName 源码级判据:管线里不得再用引擎名做能力判断。
func TestPipelineDoesNotBranchOnEngineName(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, f := range files {
		name := f.Name()
		if f.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatal(rerr)
		}
		scanned++
		for i, ln := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(ln)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.Contains(ln, `engName == "comfy"`) || strings.Contains(ln, `engName != "comfy"`) {
				t.Errorf("%s:%d 用引擎名判断能力 —— 请改用 engine.CapsOf(engName)", name, i+1)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("没扫到源码 —— 闸门失效")
	}
}
