package director

import (
	"fmt"
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

// TestSettingsShotLimitIsEffective 设置页的「镜头数安全上限」必须真的生效(审计 P1-1)。
//
// 这个字段此前只有一个界面:Go 侧零读取点,内容驱动模式的上限永远是硬编码的 120。
func TestSettingsShotLimitIsEffective(t *testing.T) {
	// 配置里给了 30:内容驱动时上限就是 30(而不是 120)
	if got := shotLimitOf(Options{ShotLimit: 30}, true); got != 30 {
		t.Errorf("内容驱动时应采用设置的 30,实际 %d", got)
	}
	p := planWithShots(40)
	if err := validate(p, shotLimitOf(Options{ShotLimit: 30}, true)); err != nil {
		t.Fatal(err)
	}
	if len(p.Shots) != 30 {
		t.Errorf("应收到设置的上限 30,实际 %d", len(p.Shots))
	}
	// 0 / 负数 = 用内置安全网
	for _, v := range []int{0, -5} {
		if got := shotLimitOf(Options{ShotLimit: v}, true); got != project.MaxShotsCeiling {
			t.Errorf("ShotLimit=%d 应用内置安全网 %d,实际 %d", v, project.MaxShotsCeiling, got)
		}
	}
	// 填一个比安全网更大的值不该让安全网失效
	if got := shotLimitOf(Options{ShotLimit: 9999}, true); got != project.MaxShotsCeiling {
		t.Errorf("超过安全网的上限应被收回 %d,实际 %d", project.MaxShotsCeiling, got)
	}
	// 指定了镜头数时,设置不参与(那是"我要 8 镜"的明确意图)
	if got := shotLimitOf(Options{MaxShots: 8, ShotLimit: 30}, false); got != 8 {
		t.Errorf("指定镜头数时应用 8,实际 %d", got)
	}
}

// TestTruncationIsLoud 截断必须说出来(审计 P1-1 的配套纪律:静默截断 = 用户只看到
// 成片短了、不知道原因,也不知道去哪调)。
func TestTruncationIsLoud(t *testing.T) {
	var notes []string
	opt := Options{ShotLimit: 10, OnNote: func(f string, a ...any) {
		notes = append(notes, fmt.Sprintf(f, a...))
	}}
	plan := planWithShots(25)
	if err := validateAndNote(plan, opt, true); err != nil {
		t.Fatal(err)
	}
	if len(plan.Shots) != 10 {
		t.Fatalf("应截断到 10,实际 %d", len(plan.Shots))
	}
	if len(notes) != 1 {
		t.Fatalf("必须且只提示一次,实际 %d 条:%v", len(notes), notes)
	}
	// 提示要可执行:说明丢了多少镜、上限在哪调、填 0 是什么语义
	for _, want := range []string{"25", "10", "丢了 15", "设置", "填 0"} {
		if !strings.Contains(notes[0], want) {
			t.Errorf("提示里应包含 %q,实际 %q", want, notes[0])
		}
	}
	// 没触发截断时不该有噪声
	notes = nil
	small := planWithShots(5)
	if err := validateAndNote(small, opt, true); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Errorf("未截断时不该提示,实际 %v", notes)
	}
}
