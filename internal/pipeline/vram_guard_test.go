package pipeline

import (
	"errors"
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
)

// 显存护栏准入判据的穷举测试(审计 P0-5)。
//
// 这条守的是"引擎忙到答不上探测"这个真实状态:旧判据
// `aerr == nil && (Running+Pending) == 0` 会把 /queue 查询失败(数字保持 0)
// 读成"空闲",于是 /free 把正在跑的权重卸出显存 —— 接下来每步重新流式加载,
// 实测 150s/步,整镜 45 分钟超时。后果比"多占一会儿显存"严重得多。
func TestVRAMReleaseAllowed(t *testing.T) {
	cases := []struct {
		name   string
		st     *comfyengine.AdminStatus
		err    error
		expect bool
		why    string
	}{
		{"探测失败(引擎不可达)", nil, errors.New("dial tcp: connection refused"), false,
			"探测不到就不许当空闲 —— 这正是旧实现的漏洞"},
		{"队列数字未知(查询被拒)", &comfyengine.AdminStatus{Alive: true}, nil, false,
			"/queue 报错时 Running/Pending 是 0,但与真正的空闲不可区分"},
		{"队列已知且为空", &comfyengine.AdminStatus{Alive: true, QueueKnown: true}, nil, true,
			"唯一允许释放的状态"},
		{"队列已知且有正在跑的", &comfyengine.AdminStatus{Alive: true, QueueKnown: true, Running: 1}, nil, false,
			"别人正在用,不许抢"},
		{"队列已知且有排队的", &comfyengine.AdminStatus{Alive: true, QueueKnown: true, Pending: 2}, nil, false,
			"刚投的任务还在队列里"},
		{"引擎在跑但状态对象为空", nil, nil, false, "拿不到状态就不释放"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vramReleaseAllowed(tc.st, tc.err); got != tc.expect {
				t.Fatalf("vramReleaseAllowed = %v,期望 %v(%s)", got, tc.expect, tc.why)
			}
		})
	}
}
