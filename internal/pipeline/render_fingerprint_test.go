package pipeline

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// TestRenderFingerprintCoversRefineTier 换档必须换指纹。
//
// renderFingerprint 的注释里写着这条工程教训:"指纹必须覆盖采样质量配置(VAE/LoRA 档位),
// 否则换档后旧缓存静默命中"。精修档(refine)是新的采样质量档,漏掉它的后果很具体:
// 用户把项目切到精修档、点了续跑,而每一镜都"已有产物"被跳过 —— 他以为在跑 20 步,
// 屏幕上却一帧没动,于是"切了档也没变化"。所以这条钉住档位进指纹。
func TestRenderFingerprintCoversRefineTier(t *testing.T) {
	base := project.RenderParams{Width: 768, Height: 1344, Steps: 24, Seam: true}

	turbo := base
	turbo.Accel = "turbo"
	refine := base
	refine.Accel = "refine"

	fpTurbo := renderFingerprint("same prompt", nil, turbo, "ref2va", 7)
	fpRefine := renderFingerprint("same prompt", nil, refine, "ref2va", 7)
	if fpTurbo == fpRefine {
		t.Error("切到精修档必须换指纹,否则旧产物静默命中 —— 用户以为切了档,其实一帧没重渲")
	}
	// 同参数必须稳定(否则每次续跑都全量重渲)
	if again := renderFingerprint("same prompt", nil, refine, "ref2va", 7); again != fpRefine {
		t.Error("同参数两次指纹不一致 —— 会让续跑失去意义")
	}
}
