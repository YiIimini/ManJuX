package comfyengine

import (
	"os"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine"
)

// readEngineSource 读 engine.go 源码(用于断言"顺序"这类没有运行观测面的约定)。
func readEngineSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// 接缝长度补偿的判据(审计 P2)。
//
// 回归场景:接缝模式会把上一镜尾部 22 帧钉在本镜头部、再由 Trim 裁掉,所以渲染
// 长度要加回 22 帧。旧实现在"上一镜没有接缝存档 → 降级普通渲染"时只清了 PrevShot,
// **没有回滚 +22**,而且降级后的图里没有 Trim 节点 —— 那一镜因此多渲 22 帧(约
// 0.9 秒)且不裁头,逐镜累积进成片。判据必须落在"帧数"上,否则它只会表现为
// "成片比预期长一点",没人会去查。
func TestSeamRenderLength(t *testing.T) {
	const jobLen = 120 // 5 秒 @24fps

	plain := seamRenderLength(jobLen, false)
	if plain != engine.AlignLength(jobLen) {
		t.Fatalf("非接缝时不该做补偿:%d", plain)
	}
	if frames := seamRenderLength(jobLen, true); frames <= plain {
		t.Fatalf("接缝生效时必须把被裁掉的头补回来:%d vs %d", frames, plain)
	}
	// 补偿量必须恰好覆盖钉入的帧数(再多就是多渲,再少就会短)
	if got := seamRenderLength(jobLen, true) - plain; got < seamHeadFrames {
		t.Errorf("补偿量 %d 帧 < 钉入的 %d 帧 —— 成片会短", got, seamHeadFrames)
	}
	// 降级(seamActive=false)与"从未开接缝"必须完全一致:这正是旧实现漏掉的那条路径
	if seamRenderLength(jobLen, false) != engine.AlignLength(jobLen) {
		t.Error("降级后的帧数必须回到普通渲染的算法")
	}
}

// TestSeamHeadFramesMatchesGraph 补偿用的帧数必须与图里 MotionContext 的
// context_length 一致 —— 改一处漏一处会让成片悄悄变长或变短。
func TestSeamHeadFramesMatchesGraph(t *testing.T) {
	job := baseJob("ref2va")
	job.Seam = true
	job.PrevShot = 1
	graph := buildRenderGraph(job, testComfySettings(), "", []string{"a.png"}, nil, 768, 1344, 120)

	_, inputs := findNode(t, graph, classMotionCtx)
	if inputs == nil {
		t.Fatal("接缝模式下应包含 MotionContext 节点")
	}
	if got := inputs["context_length"]; got != "22" {
		t.Fatalf("图的 context_length = %v,期望字符串 %q", got, "22")
	}
	if seamHeadFrames != 22 {
		t.Fatalf("seamHeadFrames=%d 与图里的 %q 不一致", seamHeadFrames, "22")
	}
	// 降级(PrevShot=0)时不该有 MotionContext 节点 —— 没有它就没人裁头,
	// 这正是"降级时不能补 22 帧"的原因。
	plain := baseJob("ref2va")
	plain.Seam = true
	plain.PrevShot = 0
	if _, in2 := findNode(t, buildRenderGraph(plain, testComfySettings(), "", []string{"a.png"}, nil, 768, 1344, 120), classMotionCtx); in2 != nil {
		t.Error("PrevShot=0 时不该有 MotionContext(降级路径)")
	}
}

// TestSeamCompensationOrderIsBeforeCondCache 长度补偿必须发生在条件缓存**之前**。
//
// 缓存键含 length:若先按"接缝长度"算缓存、再降级成普通长度渲染,那份条件与图
// 就不匹配(缓存失效或条件错位),而且要多渲 22 帧才会被发现。判据是源码顺序 ——
// 这类"顺序错位"没有别的观测面。
func TestSeamCompensationOrderIsBeforeCondCache(t *testing.T) {
	raw := readEngineSource(t)
	seam := strings.Index(raw, "seamRenderLength(job.Length")
	cache := strings.Index(raw, "cacheName := condCacheName")
	if seam < 0 || cache < 0 {
		t.Fatal("源码里找不到判据锚点(函数被改名了?)")
	}
	if seam > cache {
		t.Error("接缝探测与长度补偿必须在条件缓存之前完成")
	}
}
