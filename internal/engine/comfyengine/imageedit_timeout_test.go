package comfyengine

import (
	"strings"
	"testing"
	"time"
)

// TestImageEditTimeoutUsesImageTier 多视角图像编辑的兜底超时必须是**图像级**档位。
//
// 回归(2026-09-26 渲染管线审查,medium):旧实现把 opt.Timeout(=0)原样透传给
// WaitForDone,而后者的 timeout<=0 回退是 defaultShotTimeout = **45 分钟**(单镜视频上限)。
// 一张 1-3 分钟的编辑图卡住要白等 45 分钟,而这条链路在"渲染前资产补齐"里(每角色 3 张、
// 还有重试),最坏把整条管线堵上小时级。
//
// 项目里早有图像级兜底(defaultImageTimeout,handlers_enhance.go 的注释写着这条路当初
// 就是因为"定妆图共用一个 45 分钟"才另立的),只是编辑链路没用上。
func TestImageEditTimeoutUsesImageTier(t *testing.T) {
	if got := imageEditTimeout(ImageEditOptions{}); got != defaultImageTimeout {
		t.Errorf("未指定超时时应用图像级兜底 %v,实际 %v", defaultImageTimeout, got)
	}
	if got := imageEditTimeout(ImageEditOptions{Timeout: -time.Second}); got != defaultImageTimeout {
		t.Errorf("负值同样按未指定处理,实际 %v", got)
	}
	// 显式指定必须以调用方为准(设置页可调的口径不能被架空)
	want := 3 * time.Minute
	if got := imageEditTimeout(ImageEditOptions{Timeout: want}); got != want {
		t.Errorf("显式超时应原样生效,实际 %v", got)
	}
	// 兜底必须真的比单镜上限短 —— 否则这条修复没有意义
	if defaultImageTimeout >= defaultShotTimeout {
		t.Errorf("图像级兜底 %v 必须短于单镜上限 %v", defaultImageTimeout, defaultShotTimeout)
	}

	// 接线层:EditImage 必须走 imageEditTimeout(照着 submitAndWait(…, opt.Timeout, …)
	// 再写一遍就会退回 45 分钟档位,而那种错没有任何运行期症状)
	src, err := readSourceFile("imageedit.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "imageEditTimeout(opt)") {
		t.Error("EditImage 必须用 imageEditTimeout(opt) 决定超时,不得直接把 opt.Timeout 传下去")
	}
}
