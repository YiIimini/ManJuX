package comfyengine

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// 项目参数 render.cond_cache 必须真的能关掉条件缓存(审计:它是假开关)。
//
// 判据顺序 = 任务显式表态 > 全局设置。旧实现只看全局 c.CondCache:用户在项目里关掉它,
// 只要全局开着就照样写 .pt(代码里记过该目录堆到 4.9GB),反过来项目开着而全局关着也
// 没人告知 —— 开关能点、能存、无日志、无效果。
func TestCondCacheEnabledPrefersJobOverGlobal(t *testing.T) {
	on, off := true, false
	globalOn := config.ComfySettings{CondCache: true}
	globalOff := config.ComfySettings{CondCache: false}

	cases := []struct {
		name   string
		job    engine.Job
		global config.ComfySettings
		want   bool
	}{
		{"项目未表态 + 全局开 → 开", engine.Job{}, globalOn, true},
		{"项目未表态 + 全局关 → 关", engine.Job{}, globalOff, false},
		{"项目显式关 + 全局开 → 关(这正是旧实现做不到的)", engine.Job{CondCache: &off}, globalOn, false},
		{"项目显式开 + 全局关 → 开", engine.Job{CondCache: &on}, globalOff, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := condCacheEnabled(tc.job, tc.global); got != tc.want {
				t.Fatalf("condCacheEnabled = %v,期望 %v", got, tc.want)
			}
		})
	}
}
