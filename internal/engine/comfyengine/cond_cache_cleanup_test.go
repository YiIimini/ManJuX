package comfyengine

import (
	"os"
	"strings"
	"testing"
)

// cond_cache_cleanup_test.go 项目条件缓存清理的**如实播报**闸门（2026-10-08 深度排查）。
//
// 原来 `os.RemoveAll(sidecar)` 失败时直接 `return`：什么都不说，而调用方（删项目时）照样报
// "条件缓存一并移除"。后果特别难查 —— `.pt` 数据在前面已经删了、`.ok` 记账还留着，
// 下次渲染 `condExists` 命中**坏缓存**，报出来的是 ComfyUI 的节点错误。
//
// 判据（源码接线，因为这个失败路径需要制造"目录被占用"）：
//
//	① 删不掉要打日志（含"手动清空"的处置建议）；
//	② 必须**尽力清掉 .ok 记账**（否则坏缓存还会被命中）。
func TestCondCacheCleanupReportsFailureAndClearsOK(t *testing.T) {
	raw, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []struct{ needle, why string }{
		{"os.Remove(filepath.Join(sidecar, cache+\".ok\"))", "删不掉目录时要尽力清掉 .ok 记账（否则下次渲染命中坏缓存、报节点错误）"},
		{"项目条件缓存的记账目录删不掉", "删不掉必须打日志（原来静默 return，调用方还报\"已移除\"）"},
		{"请在引擎空闲时手动清空它", "要给出可操作的处置建议"},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("接线缺 %q —— %s", want.needle, want.why)
		}
	}
}
