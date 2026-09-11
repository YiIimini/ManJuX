package comfyengine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// 工作流图契约测试。
//
// 移植思路来自 ComfyUI-ALLinONE-MinimaxH3 的 tests/validate_workflows.py:
// 在把图提交给 ComfyUI **之前**,先把图自身校验一遍。四层断言 ——
//
//	结构层:每个节点必须有非空 class_type 与 inputs 字典
//	链接层:每个 [源id, 索引] 链接必须指向本图中真实存在的节点(消灭悬空引用,
//	        这类错误此前只会在渲染时以 ComfyUI 原始 node_errors 的形式暴露)
//	语义层:关键路径接线不得漂移(模型链、guider/scheduler 同源)
//	参数层:帧数与像素尺寸必须落在模型网格上
//
// 与 Preflight 互补:Preflight 查「ComfyUI 侧有没有这个节点」,本测试查
// 「我们组装出来的图本身对不对」。

func testComfySettings() config.ComfySettings {
	return config.ComfySettings{
		UNetRef2VA:      "test_ref2va.safetensors",
		UNetFL2VA:       "test_fl2va.safetensors",
		TurboLoRARef2VA: "test_turbo_ref2v.safetensors",
		TurboLoRAFL2VA:  "test_turbo_fl2v.safetensors",
		VideoVAE:        "test_video_vae.safetensors",
		AudioVAE:        "test_audio_vae.safetensors",
		CLIP:            "test_clip.safetensors",
	}
}

func baseJob(mode string) engine.Job {
	return engine.Job{
		ProjectID: "proj1", ShotID: 3,
		Prompt: "test prompt",
		Mode:   mode,
		Width:  768, Height: 1344, Length: 124,
		Seed: 42, Steps: 8, Accel: "turbo",
		OutputDir: ".", OutputName: "shot_003",
	}
}

// assertGraphContract 结构层 + 链接层的通用校验。
func assertGraphContract(t *testing.T, graph map[string]any) {
	t.Helper()
	if len(graph) == 0 {
		t.Fatal("图为空")
	}
	for id, raw := range graph {
		node, ok := raw.(map[string]any)
		if !ok {
			t.Errorf("节点 %s 不是对象(%T)", id, raw)
			continue
		}
		ct, _ := node["class_type"].(string)
		if strings.TrimSpace(ct) == "" {
			t.Errorf("节点 %s 缺少 class_type", id)
		}
		inputs, ok := node["inputs"].(map[string]any)
		if !ok {
			t.Errorf("节点 %s 缺少 inputs 字典", id)
			continue
		}
		for name, v := range inputs {
			arr, ok := v.([]any)
			if !ok || len(arr) != 2 {
				continue
			}
			srcID, isStr := arr[0].(string)
			if !isStr {
				continue // 普通数组输入(如 devices 列表),不是链接
			}
			if _, exists := graph[srcID]; !exists {
				t.Errorf("节点 %s 的输入 %q 指向不存在的节点 %q(悬空链接)", id, name, srcID)
			}
			if _, isInt := arr[1].(int); !isInt {
				t.Errorf("节点 %s 的输入 %q 链接索引不是整数: %v", id, name, arr[1])
			}
		}
	}
}

// assertLink 断言某节点的某输入确实来自期望节点的期望输出槽。
func assertLink(t *testing.T, graph map[string]any, nodeID, input, wantSrc string, wantIdx int) {
	t.Helper()
	node, ok := graph[nodeID].(map[string]any)
	if !ok {
		t.Errorf("节点 %s 不存在", nodeID)
		return
	}
	inputs, _ := node["inputs"].(map[string]any)
	arr, ok := inputs[input].([]any)
	if !ok || len(arr) != 2 {
		t.Errorf("节点 %s 的 %q 不是链接: %v", nodeID, input, inputs[input])
		return
	}
	if arr[0] != wantSrc || arr[1] != wantIdx {
		t.Errorf("节点 %s 的 %q 应为 [%s,%d],实际 %v", nodeID, input, wantSrc, wantIdx, arr)
	}
}

// findNode 按 class_type 找节点 id 与 inputs(避免测试硬编码节点 id)。
func findNode(t *testing.T, graph map[string]any, classType string) (string, map[string]any) {
	t.Helper()
	for id, raw := range graph {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if node["class_type"] == classType {
			inputs, _ := node["inputs"].(map[string]any)
			return id, inputs
		}
	}
	return "", nil
}

// TestRenderGraphContract 覆盖各模式与各开关组合,逐一校验图自洽。
func TestRenderGraphContract(t *testing.T) {
	c := testComfySettings()
	seamJob := baseJob("ref2va")
	seamJob.Seam = true
	seamJob.PrevShot = 2

	cases := []struct {
		name     string
		job      engine.Job
		condRef  string
		uploaded []string
	}{
		{name: "ref2va 带参考图", job: baseJob("ref2va"), uploaded: []string{"a.png", "b.png"}},
		{name: "ref2va 无参考图", job: baseJob("ref2va")},
		{name: "t2va 纯文生", job: baseJob("t2va")},
		{name: "条件缓存命中", job: baseJob("ref2va"), condRef: "cache:manjux_proj1_shot3_abcd1234", uploaded: []string{"a.png"}},
		{name: "镜间接缝", job: seamJob, uploaded: []string{"a.png"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			graph := buildRenderGraph(tc.job, c, tc.condRef, tc.uploaded, nil, tc.job.Width, tc.job.Height, tc.job.Length)
			assertGraphContract(t, graph)
		})
	}
}

// TestEncodeGraphContract 预编码图同样要自洽(两阶段渲染的缓存分支)。
func TestEncodeGraphContract(t *testing.T) {
	c := testComfySettings()
	job := baseJob("ref2va")
	graph := buildEncodeGraph(job, c, []string{"a.png"}, nil, 768, 1344, 124, "manjux_proj1_shot3_abcd1234")
	assertGraphContract(t, graph)
}

// TestRenderGraphModelChainIntact 语义层:模型链与采样链不得漂移。
func TestRenderGraphModelChainIntact(t *testing.T) {
	c := testComfySettings()
	job := baseJob("ref2va")
	graph := buildRenderGraph(job, c, "", []string{"a.png"}, nil, 768, 1344, 124)

	// 采样器与调度器必须吃**同一条**模型链的输出 —— 否则 LoRA/SigmaShift 会在
	// 其中一条路径上静默失效(实测画质劣化的典型来源)。
	_, guider := findNode(t, graph, "BasicGuider")
	_, sched := findNode(t, graph, "BasicScheduler")
	if guider == nil || sched == nil {
		t.Fatal("缺少 BasicGuider 或 BasicScheduler 节点")
	}
	if fmt.Sprint(guider["model"]) != fmt.Sprint(sched["model"]) {
		t.Errorf("guider 与 scheduler 的 model 必须同源(否则加速 LoRA 只在一侧生效):%v vs %v",
			guider["model"], sched["model"])
	}

	// SigmaShift 必须挂在模型链上,且 guider 取的是它的输出
	shiftID, shiftInputs := findNode(t, graph, "MiniMaxH3SigmaShift")
	if shiftID == "" {
		t.Fatal("缺少 MiniMaxH3SigmaShift")
	}
	if _, ok := shiftInputs["model"]; !ok {
		t.Error("SigmaShift 必须接收上游模型链接")
	}
	if fmt.Sprint(guider["model"]) != fmt.Sprint([]any{shiftID, 0}) {
		t.Errorf("guider 的 model 应取自 SigmaShift(%s) 的输出,实际 %v", shiftID, guider["model"])
	}

	// 条件节点必须把 clip/vae/尺寸都接上
	condID, _ := findNode(t, graph, "MiniMaxH3ReferenceToVideo")
	if condID == "" {
		t.Fatal("ref2va 模式必须使用 MiniMaxH3ReferenceToVideo")
	}
	for _, in := range []string{"clip", "vae", "prompt", "width", "height", "length"} {
		node := graph[condID].(map[string]any)
		inputs := node["inputs"].(map[string]any)
		if _, ok := inputs[in]; !ok {
			t.Errorf("条件节点缺少必需输入 %q", in)
		}
	}
}

// TestRenderGraphParamsOnGrid 参数层:帧数与像素尺寸必须落在模型网格上。
func TestRenderGraphParamsOnGrid(t *testing.T) {
	c := testComfySettings()
	// 17k+5 帧网格(H3 硬约束)
	for _, length := range []int{5, 22, 39, 124, 141, 294} {
		if (length-5)%17 != 0 {
			t.Fatalf("测试用例自身不合法: %d 不在 17k+5 网格上", length)
		}
		job := baseJob("ref2va")
		job.Length = length
		graph := buildRenderGraph(job, c, "", []string{"a.png"}, nil, 768, 1344, length)
		condID, inputs := findNode(t, graph, "MiniMaxH3ReferenceToVideo")
		if condID == "" {
			t.Fatal("缺少条件节点")
		}
		if got := inputs["length"]; got != length {
			t.Errorf("条件节点的 length 应原样透传 %d,实际 %v", length, got)
		}
		if got := inputs["width"]; got != 768 {
			t.Errorf("width 应透传 768,实际 %v", got)
		}
		if got := inputs["height"]; got != 1344 {
			t.Errorf("height 应透传 1344,实际 %v", got)
		}
	}
}

// TestRenderGraphSeamChain 语义层:接缝链必须完整(Load → Context → Save → Trim),
// 缺任何一环都会让「上一镜尾帧续接」静默退化成普通渲染。
func TestRenderGraphSeamChain(t *testing.T) {
	c := testComfySettings()
	job := baseJob("ref2va")
	job.Seam = true
	job.PrevShot = 2
	graph := buildRenderGraph(job, c, "", []string{"a.png"}, nil, 768, 1344, 124)

	for _, cls := range []string{
		"MiniMaxH3MotionContextLoadLatent",
		"MiniMaxH3MotionContext",
		"MiniMaxH3MotionContextSaveLatent",
		"MiniMaxH3MotionContextTrim",
	} {
		if id, _ := findNode(t, graph, cls); id == "" {
			t.Errorf("开启接缝时缺少节点 %s", cls)
		}
	}

	// Load 必须读**上一镜**的槽位,Save 必须写**本镜**槽位
	_, loadInputs := findNode(t, graph, "MiniMaxH3MotionContextLoadLatent")
	if fmt.Sprint(loadInputs["clip_index"]) != "2" {
		t.Errorf("接缝应加载上一镜(clip_index=2),实际 %v", loadInputs["clip_index"])
	}

	// 音画同裁:Trim 必须同时接住视频与音频两条解码路径,且裁剪帧数**由
	// MotionContext 节点算出**(其输出槽 1)而非外部硬编码 —— 钉入多少就裁多少,
	// 两侧任一路径错接都会让产物音画错位或时长漂移。
	trimID, trimInputs := findNode(t, graph, "MiniMaxH3MotionContextTrim")
	if trimID == "" {
		t.Fatal("开启接缝时必须存在 Trim 节点")
	}
	for _, in := range []string{"images", "audio", "trim_frames", "match_tail"} {
		if _, ok := trimInputs[in]; !ok {
			t.Errorf("Trim 节点缺少输入 %q(音画同裁依赖它)", in)
		}
	}
	videoDecodeID, _ := findNode(t, graph, "VAEDecode")
	audioDecodeID, _ := findNode(t, graph, "VAEDecodeAudio")
	ctxID, _ := findNode(t, graph, "MiniMaxH3MotionContext")
	if videoDecodeID == "" || audioDecodeID == "" || ctxID == "" {
		t.Fatalf("接缝链缺少解码或上下文节点(video=%q audio=%q ctx=%q)", videoDecodeID, audioDecodeID, ctxID)
	}
	assertLink(t, graph, trimID, "images", videoDecodeID, 0)
	assertLink(t, graph, trimID, "audio", audioDecodeID, 0)
	assertLink(t, graph, trimID, "trim_frames", ctxID, 1)

	// SaveLatent 必须存**采样后**的 latent(下一镜才能续接),而不是初始 latent
	saveID, _ := findNode(t, graph, "MiniMaxH3MotionContextSaveLatent")
	samplerID, _ := findNode(t, graph, "SamplerCustomAdvanced")
	if saveID == "" || samplerID == "" {
		t.Fatal("接缝链缺少存档节点或采样器")
	}
	assertLink(t, graph, saveID, "latent", samplerID, 0)
	// 存档槽位必须是本镜号,加载槽位是上一镜号 —— 错位会让续接取到错误的帧
	saveInputs := graph[saveID].(map[string]any)["inputs"].(map[string]any)
	if fmt.Sprint(saveInputs["clip_index"]) != "3" {
		t.Errorf("存档槽应为本镜号 3,实际 %v", saveInputs["clip_index"])
	}
}
