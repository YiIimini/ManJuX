package comfyengine

import "testing"

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
	opt := MaskOptions{SourceVideo: "shot_003.mp4", Prompt: "test prompt", ObjectIndex: "0"}

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
