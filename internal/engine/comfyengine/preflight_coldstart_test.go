package comfyengine

import (
	"context"
	"strings"
	"testing"
)

// TestPreflightChecksWhenAlive 引擎活着时要真的报出缺失节点。
//
// 这条此前没有覆盖:Preflight 是"缺插件"的唯一前置防线(渲染跑到一半才失败的话,
// 用户拿到的是 ComfyUI 的原始 node_errors)。这里用假 ComfyUI 钉住正常路径。
func TestPreflightChecksWhenAlive(t *testing.T) {
	// 只声明 SigmaShift:ref2va/i2v 的根节点都缺 → 必须报出来
	e, _ := newFakeEngine(t, map[string][]string{
		"MiniMaxH3SigmaShift.model": {"x"},
	})
	got := e.Preflight(context.Background(), PreflightOptions{Modes: []string{"ref2va", "t2va"}})
	joined := strings.Join(got, " | ")
	if len(got) == 0 {
		t.Fatal("引擎可达且节点缺失时必须报缺失")
	}
	for _, want := range []string{"MiniMaxH3ReferenceToVideo", "MiniMaxH3ImageToVideo"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺失清单应包含 %s,实际:%s", want, joined)
		}
	}

	// 节点齐备时不报缺失(误报会挡住整条产线)
	full := newFakeEngineForNodes(t, classRef2V, classI2V, classSigmaShift)
	if got := full.Preflight(context.Background(), PreflightOptions{Modes: []string{"ref2va", "t2va"}}); len(got) != 0 {
		t.Errorf("节点齐备时不应报缺失,实际 %v", got)
	}
}

// newFakeEngineForNodes 起一个"这些节点都存在"的假 ComfyUI。
func newFakeEngineForNodes(t *testing.T, classes ...string) *Engine {
	t.Helper()
	combos := map[string][]string{}
	for _, c := range classes {
		combos[c+".model"] = []string{"x"} // 只要该节点存在(有任意 combo 输入)即可
	}
	e, _ := newFakeEngine(t, combos)
	return e
}

// TestColdStartStillDoesNotFabricateMissing 冷启动的边界仍然必须是"不误报缺失"。
//
// 2026-09-26 渲染管线审查指出:引擎不可达时旧实现直接返回"无缺失",而渲染阶段并没有
// 先调过 Available —— 于是冷启动(引擎被空闲看门狗停掉后是常态)时,「缺插件节点 /
// 缺 SAM3 权重 / 缺放大权重」的体检被静默跳过,失败退回"跑到一半抛原始报文"。
//
// 修法是"体检前先尝试把引擎拉起来"(见 engineReachable),而**不是**把"连不上"伪造成
// "缺节点/缺文件":调用方拿到的报错文案是「ComfyUI 缺少渲染所需节点」「缺 SAM3 权重」,
// 拿它去说"引擎连不上"会把用户指向一个根本没坏的地方(这条边界由
// TestUpscaleAssetsEngineDown 与本用例共同钉住)。
func TestColdStartStillDoesNotFabricateMissing(t *testing.T) {
	// 冷启动 + 不允许代管启动(AutoStart=false):拉起必然失败
	e := deadEngine(t)
	if got := e.Preflight(context.Background(), PreflightOptions{Modes: []string{"ref2va"}}); len(got) != 0 {
		t.Errorf("拉不起引擎时不得伪造成「节点缺失」,实际 %v", got)
	}
	if got := e.MaskAssets(context.Background()); len(got) != 0 {
		t.Errorf("拉不起引擎时不得伪造成「缺 SAM3 权重」,实际 %v", got)
	}
}

// TestPreflightTriesToStartEngine 源码级判据:三处体检都必须走"先确保可用"的闸门。
//
// 为什么用源码判据:"冷启动时先尝试拉起引擎"只能靠真的起一次 ComfyUI 才观测得到,
// 而这条约定的回归成本极高(冷启动下整条产线会在渲染阶段直接失败)。判据落在调用点上,
// 与机器上装了什么无关。
func TestPreflightTriesToStartEngine(t *testing.T) {
	src, err := readSourceFile("preflight.go")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(src, "e.engineReachable(ctx)"); n != 3 {
		t.Errorf("Preflight / UpscaleAssets / MaskAssets 都要先走 engineReachable(ctx),实际出现 %d 次", n)
	}
	// 不许再出现"只看 Alive 就返回空"的旧写法
	if strings.Contains(src, "if !e.client().Alive(ctx) {") {
		t.Error("体检入口不得再用裸 Alive 判据 —— 那正是冷启动静默跳过体检的原因")
	}
}
