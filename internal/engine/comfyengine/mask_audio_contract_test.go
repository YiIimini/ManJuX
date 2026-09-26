package comfyengine

import (
	"fmt"
	"strings"
	"testing"
)

// TestRenderGraphAudioDrive 音频驱动契约。
//
// Audio Drive 的本质是 H3 Ref2V 的音频条件:把定稿配音喂进 ref_audios,人物的
// 口型与动作节奏就会跟随音频。这里锁定三件事:聚合键名正确、音频加载节点存在、
// 以及「只有音频没有参考图」的纯音频驱动也成立(那时仍须走 Ref2V 而不是 t2va)。
func TestRenderGraphAudioDrive(t *testing.T) {
	c := testComfySettings()
	job := baseJob("ref2va")

	// ① 图 + 音频
	graph := buildRenderGraph(job, c, "", []string{"a.png"}, []string{"voice.wav"}, 768, 1344, 124)
	assertGraphContract(t, graph)
	_, inputs := findNode(t, graph, "MiniMaxH3ReferenceToVideo")
	if inputs == nil {
		t.Fatal("ref2va 模式必须使用 MiniMaxH3ReferenceToVideo")
	}
	if _, ok := inputs["ref_audios"]; !ok {
		t.Error("带音频参考时必须给出 ref_audios 聚合键(音频驱动的入口)")
	}
	if id, _ := findNode(t, graph, "LoadAudio"); id == "" {
		t.Error("缺少 LoadAudio 节点:音频没有被真正接入图")
	}

	// ② 纯音频驱动(无参考图)—— 仍应走 Ref2V 并带 ref_audios
	g2 := buildRenderGraph(job, c, "", nil, []string{"voice.wav"}, 768, 1344, 124)
	assertGraphContract(t, g2)
	_, in2 := findNode(t, g2, "MiniMaxH3ReferenceToVideo")
	if in2 == nil {
		t.Fatal("纯音频驱动也必须走 Ref2V(否则音频条件无处安放)")
	}
	if _, ok := in2["ref_audios"]; !ok {
		t.Error("纯音频驱动应带 ref_audios")
	}

	// ③ 无音频时不得出现该键(避免给普通镜头平白增加一个空输入)
	g3 := buildRenderGraph(job, c, "", []string{"a.png"}, nil, 768, 1344, 124)
	_, in3 := findNode(t, graph, "MiniMaxH3ReferenceToVideo")
	_ = in3
	_, inPlain := findNode(t, g3, "MiniMaxH3ReferenceToVideo")
	if _, ok := inPlain["ref_audios"]; ok {
		t.Error("没有音频参考时不应出现 ref_audios")
	}
}

// TestMaskGraphContract 局部重绘链路契约:关键节点齐备且整图自洽。
//
// 链路:LoadVideo → GetVideoComponents → SAM3(跟踪) → TrackToMask(遮罩)
// → SubjectCrop(裁出) → H3 Ref2V(重绘) → SubjectUncrop(贴回) → SaveVideo。
// 任何一环缺失,局部重绘都会退化成"整镜重渲"或直接失败。
func TestMaskGraphContract(t *testing.T) {
	c := testComfySettings()
	job := baseJob("ref2va")
	opt := MaskOptions{SourceVideo: "shot_003.mp4", Prompt: "test prompt", Target: "the bronze sparrow", ObjectIndex: "0"}

	graph := buildMaskGraph(job, c, opt, 768, 1344, 124)
	assertGraphContract(t, graph)

	for _, cls := range []string{
		"LoadVideo", "GetVideoComponents", "CheckpointLoaderSimple",
		"SAM3_VideoTrack", "SAM3_TrackToMask", "MVEx_SubjectCrop",
		"MiniMaxH3ReferenceToVideo", "MVEx_SubjectUncrop", "CreateVideo", "SaveVideo",
	} {
		if id, _ := findNode(t, graph, cls); id == "" {
			t.Errorf("局部重绘链路缺少节点 %s", cls)
		}
	}

	// 重绘的参考图必须来自裁剪结果(否则重绘的是全画面,等于整镜重渲)
	cropID, _ := findNode(t, graph, "MVEx_SubjectCrop")
	_, condInputs := findNode(t, graph, "MiniMaxH3ReferenceToVideo")
	refs, _ := condInputs["ref_images"].([]any)
	if len(refs) != 1 {
		t.Fatalf("重绘应恰好带 1 张参考图(裁剪出的主体),实际 %d", len(refs))
	}
	if link0, ok := refs[0].([]any); !ok || len(link0) != 2 || link0[0] != cropID {
		t.Errorf("参考图应来自 SubjectCrop(%s),实际 %v", cropID, refs[0])
	}

	// 回贴必须同时拿到"重绘结果"与"原画面",否则贴不回原位
	uncropID, uncropInputs := findNode(t, graph, "MVEx_SubjectUncrop")
	if uncropID == "" {
		t.Fatal("缺少 SubjectUncrop")
	}
	for _, in := range []string{"cropped_images", "original_images", "cropped_masks"} {
		if _, ok := uncropInputs[in]; !ok {
			t.Errorf("SubjectUncrop 缺少输入 %q(缺它就无法把重绘结果贴回原位)", in)
		}
	}
}

// TestMaskGraphSamplesFullCanvas 局部重绘的采样画布是**整幅机位画布**。
//
// 2026-09-26 渲染管线审查指出:本文件的注释与架构文档曾写"H3 只看到要重绘的那一块、
// 省一个数量级成本",与图不符 —— 裁剪结果(MVEx_SubjectCrop)只作为 ref_images 参考图
// 进入条件,而 H3 的 width/height 一直是整幅 w×h,采样成本与整镜重渲同级;
// "省"只体现在"其余像素逐帧原样保留"这个语义上。这里把真实代价钉死:
// 若哪天改成按裁剪尺寸采样(需要运行时尺寸,属于改链路的设计决定),这条会失败,
// 提醒同时改掉代价描述,别再让注释替代码许愿。
func TestMaskGraphSamplesFullCanvas(t *testing.T) {
	c := testComfySettings()
	job := baseJob("ref2va")
	graph := buildMaskGraph(job, c, MaskOptions{
		SourceVideo: "shot_003.mp4", Prompt: "p", Target: "the bronze sparrow", ObjectIndex: "0",
	}, 768, 1344, 124)

	_, inputs := findNode(t, graph, "MiniMaxH3ReferenceToVideo")
	if inputs == nil {
		t.Fatal("缺少 H3 条件节点")
	}
	if inputs["width"] != 768 || inputs["height"] != 1344 {
		t.Errorf("采样画布应是整幅机位画布(768x1344),实际 %vx%v —— 若真改成按裁剪尺寸采样,"+
			"请同时更正文档与注释里的成本口径", inputs["width"], inputs["height"])
	}
	for _, k := range []string{"denoise_mask", "mask"} {
		if _, ok := inputs[k]; ok {
			t.Errorf("遮罩并未进入采样输入(实际是后置贴回),不该出现 %q", k)
		}
	}
	// 遮罩只接给裁剪与回贴这两个节点
	cropID, cropIn := findNode(t, graph, "MVEx_SubjectCrop")
	if cropID == "" || cropIn == nil {
		t.Fatal("缺少 MVEx_SubjectCrop")
	}
	if fmt.Sprint(cropIn["masks"]) == "" {
		t.Error("裁剪节点必须拿到遮罩")
	}
	if _, in := findNode(t, graph, "MVEx_SubjectUncrop"); in == nil {
		t.Fatal("缺少 MVEx_SubjectUncrop")
	}
}

// TestMaskGraphWiresSAM3Conditioning SAM3 跟踪节点的**必需输入**必须真的接上。
//
// 2026-09-24 全项目审计发现的功能性死链:这张图从建立起既没传 initial_mask、
// 也没传 conditioning,而 nodes_sam3.py 里写着
//
//	if conditioning ... elif initial_mask is None: raise ValueError("Either initial_mask or conditioning must be provided")
//
// —— 也就是说「局部重绘」这个 UI 上有按钮、有弹窗、有 API 的功能,**必然失败**,
// 而且失败报文是 ComfyUI 侧的原始 ValueError。这里把修复钉死:
// SAM3_VideoTrack 必须拿到 conditioning,且它来自 SAM3 checkpoint 自带的 CLIP
// (CheckpointLoaderSimple 的输出槽 1)编码出的文本。
func TestMaskGraphWiresSAM3Conditioning(t *testing.T) {
	c := testComfySettings()
	job := baseJob("ref2va")
	opt := MaskOptions{SourceVideo: "shot_003.mp4", Prompt: "p", Target: "the bronze sparrow"}

	graph := buildMaskGraph(job, c, opt, 768, 1344, 124)
	assertGraphContract(t, graph)

	ckptID, _ := findNode(t, graph, "CheckpointLoaderSimple")
	if ckptID == "" {
		t.Fatal("缺少 CheckpointLoaderSimple(SAM3 跟踪模型与文本编码器都来自它)")
	}
	_, trackInputs := findNode(t, graph, "SAM3_VideoTrack")
	if trackInputs == nil {
		t.Fatal("缺少 SAM3_VideoTrack")
	}
	condLink, ok := trackInputs["conditioning"].([]any)
	if !ok || len(condLink) != 2 {
		t.Fatalf("SAM3_VideoTrack 必须接 conditioning(否则节点直接抛 ValueError),实际 %v", trackInputs["conditioning"])
	}
	encID, _ := findNode(t, graph, "CLIPTextEncode")
	if encID == "" {
		t.Fatal("缺少 CLIPTextEncode:文本条件没有被真正编出来")
	}
	if condLink[0] != encID {
		t.Errorf("conditioning 应来自 CLIPTextEncode(%s),实际 %v", encID, condLink[0])
	}
	// CLIP 必须取自 SAM3 checkpoint 的 CLIP 槽(槽 1),不是主模型或别处的 CLIP
	encNode := graph[encID].(map[string]any)
	clipLink, _ := encNode["inputs"].(map[string]any)["clip"].([]any)
	if len(clipLink) != 2 || clipLink[0] != ckptID || clipLink[1] != 1 {
		t.Errorf("CLIPTextEncode 的 clip 应取自 SAM3 checkpoint 的槽 1([%s,1]),实际 %v", ckptID, clipLink)
	}
	// 文本就是用户给的目标短语
	if got := encNode["inputs"].(map[string]any)["text"]; got != "the bronze sparrow" {
		t.Errorf("编码的文本应是用户给的目标,实际 %v", got)
	}
	// 模型权重名:缺它同样必然失败,顺带钉住(审计指出 CHANGELOG 曾声称有断言而其实没有)
	ck := graph[ckptID].(map[string]any)["inputs"].(map[string]any)
	if ck["ckpt_name"] != sam3Checkpoint {
		t.Errorf("CheckpointLoaderSimple 应加载 %s,实际 %v", sam3Checkpoint, ck["ckpt_name"])
	}
}

// TestMaskOptionsRequireTarget 目标短语是硬前置:缺它要在提交任务之前就报错,
// 而不是把请求发到 ComfyUI 再抛原始报文(这条链路要上传源视频、跑十几分钟)。
func TestMaskOptionsRequireTarget(t *testing.T) {
	if err := (MaskOptions{Prompt: "p"}).validate(); err == nil {
		t.Fatal("缺 Target 时必须报错")
	} else if !strings.Contains(err.Error(), "SAM3") {
		t.Errorf("错误信息要说清为什么要填目标(SAM3 开放词表),实际:%v", err)
	}
	if err := (MaskOptions{Prompt: "p", Target: "  "}).validate(); err == nil {
		t.Error("只有空白字符也算没填")
	}
	if err := (MaskOptions{Prompt: "p", Target: "the lantern"}).validate(); err != nil {
		t.Errorf("填了目标不该报错:%v", err)
	}
}
