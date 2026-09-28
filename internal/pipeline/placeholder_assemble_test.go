package pipeline

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 失败镜的占位片段能不能进合成（2026-09-28 修自己上轮留下的半成品）。
//
// 官方 fallback 第 4 级："Skip the shot and add a `placeholder: missing clip` note for
// downstream review" —— 占位的**全部意义**就是保住时间轴、让缺口看得见、成片照出。
// 此前这里死认 ShotDone ⇒ 一个镜头失败就整片合不出来，占位代码等于白写。
//
// 但**不能**因为"宽容"就把"还没渲"也放进来（那不是带瑕疵出片，那是还没做）。
func TestIsPlaceholderClipOnlyForMarkedFailures(t *testing.T) {
	cases := []struct {
		name string
		m    project.ClipManifest
		want bool
	}{
		{"正常完成", project.ClipManifest{Status: project.ShotDone, File: "a.mp4"}, false},
		{"失败且带占位标记", project.ClipManifest{Status: project.ShotFailed, File: "a.mp4",
			Error: project.PlaceholderMarker + " —— 官方阶梯已试到第二级，仍失败：超时"}, true},
		{"失败但没写占位（产物丢了那种）", project.ClipManifest{Status: project.ShotFailed, File: "a.mp4",
			Error: "显存不足"}, false},
		{"还没渲（状态为空）", project.ClipManifest{}, false},
		{"渲染中", project.ClipManifest{Status: project.ShotRendering}, false},
	}
	for _, c := range cases {
		if got := isPlaceholderClip(c.m); got != c.want {
			t.Errorf("%s：isPlaceholderClip=%v，期望 %v", c.name, got, c.want)
		}
	}
}
