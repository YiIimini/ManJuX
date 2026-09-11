package project

import "testing"

// TestSanitizePreservesContentDriven 回归 2026-09-11:MaxShots/TotalSeconds 的 0
// 是「内容驱动」这一显式语义,不是「未填」。旧实现把 MaxShots<1 一律钳成 1,
// 于是用户首跑正常的 8-12 镜,在参数抽屉点过一次保存后就退化成「1 镜 / 7 秒」。
func TestSanitizePreservesContentDriven(t *testing.T) {
	p := StageParams{Script: ScriptParams{MaxShots: 0, TotalSeconds: 0}}
	p.Sanitize()
	if p.Script.MaxShots != 0 {
		t.Fatalf("MaxShots=0 应保持内容驱动语义,实际被改成 %d", p.Script.MaxShots)
	}
	if p.Script.TotalSeconds != 0 {
		t.Fatalf("TotalSeconds=0 应保持自动语义,实际被改成 %d", p.Script.TotalSeconds)
	}
}

// TestSanitizeClampsCeiling 负数归一、超限钳到 MaxShotsCeiling。
func TestSanitizeClampsCeiling(t *testing.T) {
	cases := []struct{ in, want int }{
		{-5, 0},
		{0, 0},
		{1, 1},
		{6, 6},
		{20, 20},
		{MaxShotsCeiling, MaxShotsCeiling},
		{MaxShotsCeiling + 1, MaxShotsCeiling},
		{999, MaxShotsCeiling},
	}
	for _, c := range cases {
		p := StageParams{Script: ScriptParams{MaxShots: c.in}}
		p.Sanitize()
		if p.Script.MaxShots != c.want {
			t.Errorf("MaxShots %d → 期望 %d,实际 %d", c.in, c.want, p.Script.MaxShots)
		}
	}
}

// TestSanitizeTotalSecondsGuard 指定镜头数时过短时长回退默认节拍;
// 内容驱动(0)时时长原样保留,不被镜头数误伤。
func TestSanitizeTotalSecondsGuard(t *testing.T) {
	cases := []struct {
		name           string
		shots, seconds int
		want           int
	}{
		{"6 镜 12 秒过短应回退 42", 6, 12, 42},
		{"6 镜 24 秒正好达下限不回退", 6, 24, 24},
		{"6 镜 60 秒合法保持", 6, 60, 60},
		{"内容驱动时时长原样保留", 0, 12, 12},
		{"内容驱动且时长为 0", 0, 0, 0},
		{"指定镜头数但时长为 0(自动)", 8, 0, 0},
		{"负时长归 0", 6, -3, 0},
	}
	for _, c := range cases {
		p := StageParams{Script: ScriptParams{MaxShots: c.shots, TotalSeconds: c.seconds}}
		p.Sanitize()
		if p.Script.TotalSeconds != c.want {
			t.Errorf("%s:MaxShots=%d TotalSeconds=%d → 期望 %d,实际 %d",
				c.name, c.shots, c.seconds, c.want, p.Script.TotalSeconds)
		}
	}
}

// TestSanitizeDoesNotTouchOtherStages 回归保障:本次改动只应影响 script 段,
// 其余段的既有钳制逻辑不得被带入回归。
func TestSanitizeDoesNotTouchOtherStages(t *testing.T) {
	p := StageParams{}
	p.Sanitize()
	if p.Assets.Source != "upload" {
		t.Errorf("非法 Assets.Source 应回退 upload,实际 %q", p.Assets.Source)
	}
	if p.Render.Engine != "auto" {
		t.Errorf("非法 Render.Engine 应回退 auto,实际 %q", p.Render.Engine)
	}
}
