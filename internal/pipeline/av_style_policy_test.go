package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 三条"成片听感/画风"政策的闸门(2026-09-24 用户报障)。
//
// TestStyleComesFromUpstream ③ 平台不抢风格:上游给了风格就贯彻,没给才兜底。
func TestStyleComesFromUpstream(t *testing.T) {
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams() // 平台兜底开关默认开
	shot := &project.Shot{ID: 1, H3Prompt: "x"}

	// 上游指定风格(技能端写入方案) → 平台只负责贯彻,不覆盖
	withStyle := &project.Plan{Style: "写实电影感,35mm 胶片颗粒"}
	got := ShotRenderPrompt(proj, withStyle, shot)
	if !strings.Contains(got, "STYLE AUTHORITY") {
		t.Error("上游指定的风格必须带「照此渲染、逐镜一致」的执行段")
	}
	if strings.Contains(got, "MEDIUM OVERRIDE") {
		t.Error("上游已指定风格时不得做介质覆盖(平台端不限制风格)")
	}
	if !strings.Contains(got, "写实电影感") {
		t.Error("上游风格串必须原样保留在提示词里")
	}
	// 上游没给风格 → 平台兜底(且说明这是兜底)
	bare := &project.Plan{}
	fb := ShotRenderPrompt(proj, bare, shot)
	if !strings.Contains(fb, RenderMediumMarker) {
		t.Error("没有风格设定时应带平台兜底画风块")
	}
	if !strings.Contains(fb, "兜底画风") {
		t.Error("兜底时要说明这是兜底、风格应由上游指定")
	}
}
