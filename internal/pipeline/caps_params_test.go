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
	rp := project.RenderParams{Seam: true, CondCache: project.Bool(true)}
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

// 云端不支持 seed:项目设了固定 seed 时必须在**能力清单**里说清"不可复现",
// 而不是只靠逐镜日志(用户需要在一个地方看清哪些参数本次根本不生效)。
func TestUnsupportedParamNoteFixedSeedOnCloud(t *testing.T) {
	// 云端 + 固定 seed → 必须提示,且要点明"不可复现"与"要复现请切本地"
	note := unsupportedParamNote(engine.CapsOf("api"), project.RenderParams{Seed: 12345})
	if note == "" {
		t.Fatal("云端 + 固定 seed 必须提示:云端请求体没有 seed 字段,产物不可复现")
	}
	for _, want := range []string{"seed", "不可复现", "本地 ComfyUI"} {
		if !strings.Contains(note, want) {
			t.Errorf("提示里应包含 %q,实际 %q", want, note)
		}
	}
	// 云端 + 没设固定 seed(0 = 每镜随机)→ 无提示(否则每个项目都刷一条噪声)
	if n := unsupportedParamNote(engine.CapsOf("api"), project.RenderParams{Seed: 0}); n != "" {
		t.Errorf("seed=0(每镜随机)在云端没有'失效'可言,不该提示,实际 %q", n)
	}
	// 本地引擎支持 seed → 无提示
	if n := unsupportedParamNote(engine.CapsOf("comfy"), project.RenderParams{Seed: 12345}); n != "" {
		t.Errorf("本地引擎支持 seed,不该报「不支持」,实际 %q", n)
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
