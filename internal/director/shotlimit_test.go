package director

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 镜头数上限的语义回归(2026-09-24 实跑发现):
//
// 一集按原文节拍规划出 32 镜、分批也照 32 镜全跑完了,末了被 validate() 里写死的
// `len(p.Shots) > 20 → 截断` **静默砍成 20 镜** —— 既白花 3 轮 LLM 调用,
// 又正好违反用户反复强调的「不跳段、不省略」。上限必须跟着**意图**走:
// 内容驱动 = MaxShotsCeiling(安全网),指定镜头数 = 那个数。

func planWithShots(n int) *project.Plan {
	p := &project.Plan{Title: "砖"}
	long := strings.Repeat("A", 420) // validate 有"提示词过短疑似截断"的守卫
	for i := 1; i <= n; i++ {
		p.Shots = append(p.Shots, project.Shot{ID: i, Duration: 6, H3Prompt: long})
	}
	return p
}

func TestValidateKeepsContentDrivenShotCount(t *testing.T) {
	opt := Options{MaxShots: defaultContentDrivenShots} // 内容驱动时被归一成兜底值
	p := planWithShots(32)
	if err := validate(p, shotLimitOf(opt, true)); err != nil {
		t.Fatalf("不该失败: %v", err)
	}
	if len(p.Shots) != 32 {
		t.Errorf("内容驱动下 32 镜不应被截断,实际剩 %d 镜", len(p.Shots))
	}
}

// TestValidateClampsExplicitShotCount 指定了镜头数就按指定值收口(模型超产要砍)。
func TestValidateClampsExplicitShotCount(t *testing.T) {
	opt := Options{MaxShots: 8}
	p := planWithShots(20)
	if err := validate(p, shotLimitOf(opt, false)); err != nil {
		t.Fatalf("不该失败: %v", err)
	}
	if len(p.Shots) != 8 {
		t.Errorf("指定 8 镜应收口到 8,实际 %d", len(p.Shots))
	}
}

// TestValidateCeilingIsSafetyNet 内容驱动也仍受 MaxShotsCeiling 保护(拦失控)。
func TestValidateCeilingIsSafetyNet(t *testing.T) {
	p := planWithShots(project.MaxShotsCeiling + 40)
	if err := validate(p, shotLimitOf(Options{MaxShots: defaultContentDrivenShots}, true)); err != nil {
		t.Fatalf("不该失败: %v", err)
	}
	if len(p.Shots) != project.MaxShotsCeiling {
		t.Errorf("应被安全网收到 %d,实际 %d", project.MaxShotsCeiling, len(p.Shots))
	}
	_ = strings.TrimSpace
}
