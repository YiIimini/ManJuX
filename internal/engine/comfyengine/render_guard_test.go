package comfyengine

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
)

// readSourceFile 读本包源码(用于断言"打点位置"这类没有运行期观测面的约定)。
func readSourceFile(name string) (string, error) {
	raw, err := os.ReadFile(name)
	return string(raw), err
}

// deadEngine 造一个必定不可达的引擎(端口 9 上不会有 ComfyUI,连接立即被拒),
// 且 AutoStart=false —— 于是 Available 会立刻返回明确错误,不会试图拉起进程。
func deadEngine(t *testing.T) *Engine {
	t.Helper()
	c := config.ComfySettings{BaseURL: "http://127.0.0.1:9"}
	return New(func() config.ComfySettings { return c })
}

// TestRenderRejectsMissingLength 漏传帧数必须当场报错,不能兜成 5 帧。
//
// 回归(2026-09-26 渲染管线审查,高危):Job.Length 的注释是"17k+5 网格,engine 自行
// 对齐",而 AlignLength(0) 会把它兜成 **5 帧 = 0.21 秒**。音频驱动接口恰恰漏传了
// Length —— 用户点一次「音频驱动」会拿到一支 0.21 秒的片子,而接口返回 ok:true、
// 日志写「✔ 音频驱动完成」。这类"能把活干成笑话"的默认值不该存在。
func TestRenderRejectsMissingLength(t *testing.T) {
	e := deadEngine(t)
	job := baseJob("ref2va")
	job.Length = 0

	_, err := e.Render(context.Background(), job)
	if err == nil {
		t.Fatal("Length 缺失时必须报错,而不是兜成 5 帧继续渲")
	}
	if !strings.Contains(err.Error(), "帧数") {
		t.Errorf("错误信息必须点明是帧数没给,实际:%v", err)
	}
	// 必须是**参数校验**先失败:信息里不该出现"引擎连不上"之类的措辞,
	// 否则用户会去查一个根本没坏的引擎。
	if strings.Contains(err.Error(), "ComfyUI 未运行") {
		t.Errorf("Length 校验应发生在引擎可用性检查之前,实际:%v", err)
	}

	// 给了帧数则不在这条判据上失败(引擎不可达是另一回事,这里只要求错误措辞变了)
	job.Length = 124
	if _, err := e.Render(context.Background(), job); err != nil && strings.Contains(err.Error(), "帧数未指定") {
		t.Errorf("给了 Length 不该再报帧数缺失:%v", err)
	}
}

// TestShouldIdleStop 空闲看门狗判据(纯函数,便于把边界钉死)。
//
// 回归(2026-09-26 渲染管线审查):判据只看"最近一次引擎调用",而管线在镜与镜之间
// 不碰引擎 —— 单镜耗时一旦超过 IdleStopMin(默认 10 分钟,refine/quality 档与长镜
// 都是),巡检落在镜间空档就会 taskkill 掉正在产线的引擎。
func TestShouldIdleStop(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		lastBusy  time.Time
		idleMin   int
		owned     bool
		queueBusy bool
		alive     bool
		want      bool
		why       string
	}{
		{
			name:     "空闲够久且队列空、代管启动 → 停",
			lastBusy: now.Add(-20 * time.Minute), idleMin: 10, owned: true, alive: true, want: true,
		},
		{
			name:     "刚打过点(镜间空档被进出两次打点压到秒级)→ 不停",
			lastBusy: now.Add(-3 * time.Second), idleMin: 10, owned: true, alive: true, want: false,
			why: "长镜渲染期间 lastBusy 会在收尾刷新,空档里不该被杀",
		},
		{
			name:     "快到阈值(9 分钟)→ 不停",
			lastBusy: now.Add(-9 * time.Minute), idleMin: 10, owned: true, alive: true, want: false,
		},
		{
			name:     "队列里有活(正在渲染/用户排队)→ 不停",
			lastBusy: now.Add(-30 * time.Minute), idleMin: 10, owned: true, queueBusy: true, alive: true, want: false,
		},
		{
			name:     "外部实例(非代管)→ 永不接管",
			lastBusy: now.Add(-60 * time.Minute), idleMin: 10, owned: false, alive: true, want: false,
		},
		{
			name:     "设置里禁用(0)→ 不停",
			lastBusy: now.Add(-60 * time.Minute), idleMin: 0, owned: true, alive: true, want: false,
		},
		{
			name:     "已死 → 不做动作",
			lastBusy: now.Add(-60 * time.Minute), idleMin: 10, owned: true, alive: false, want: false,
		},
	}
	for _, c := range cases {
		got := shouldIdleStop(c.lastBusy, now, c.idleMin, c.owned, c.queueBusy, c.alive)
		if got != c.want {
			t.Errorf("%s: shouldIdleStop = %v, want %v(%s)", c.name, got, c.want, c.why)
		}
	}
}

// TestLongRenderKeepsEngineBusy 长作业必须"进出都打点"。
//
// shouldIdleStop 只看 lastBusy,所以判据的另一半在**打点位置**:只在入口打点的话,
// 单镜跑 20 分钟时 lastBusy 已经旧了 20 分钟,镜间空档必然落入"空闲"窗口。
// 这类"调用位置"没有运行期观测面,只能对着源码钉。
func TestLongRenderKeepsEngineBusy(t *testing.T) {
	src := readEngineSource(t)
	if !strings.Contains(src, "defer touchBusy()") {
		t.Error("Render 必须在收尾也打点(defer touchBusy()),否则长镜渲染会让引擎在镜间被判空闲杀掉")
	}
	// 局部重绘同理(上传源视频 + 跑十几分钟)
	mask, err := readSourceFile("maskgraph.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mask, "defer touchBusy()") {
		t.Error("MaskFix 也必须收尾打点")
	}
}

// TestRenderDoesNotFreeVRAMPerShot 逐镜 /free 必须已移除。
//
// /free 会 unload_models:一集 N 镜就多付 N 次 20GB 权重的重载,还把两阶段条件
// 缓存省下的时间吃掉;管线侧的口径是"渲染期间常驻、收尾才交还"。这条只能对着源码
// 钉(渲染路径需要真引擎才跑得起来)。
func TestRenderDoesNotFreeVRAMPerShot(t *testing.T) {
	src := readEngineSource(t)
	// 只允许在显式注释里提到 Free(解释为什么不调)
	for i, ln := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(ln, "client().Free(") {
			t.Errorf("engine.go:%d 渲染路径里出现了 /free(逐镜卸载模型会让下一镜重新加载 20GB 权重):%s", i+1, trimmed)
		}
	}
}
