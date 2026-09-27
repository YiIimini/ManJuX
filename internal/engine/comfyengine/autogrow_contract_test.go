package comfyengine

import (
	"testing"
)

// hasAutogrowSlot 条件节点上是否存在「聚合键.模板名」的第 i 个槽位。
func hasAutogrowSlot(inputs map[string]any, aggregate, template string, i int) bool {
	_, ok := inputs[autogrowKey(aggregate, template, i)]
	return ok
}

// autogrowSlotCount 统计某聚合键下已提交的槽位数(只认点号键)。
func autogrowSlotCount(inputs map[string]any, aggregate string) int {
	n := 0
	for k := range inputs {
		if len(k) > len(aggregate) && k[:len(aggregate)+1] == aggregate+"." {
			n++
		}
	}
	return n
}

// TestAutogrowKeysAreDotted autogrow 输入必须用点号键提交。
//
// 2026-09-27 实测(真机 ComfyUI 0.37.4 + pruned int8 底模):
//   - 聚合数组 ref_images=[link,…] → 节点收到空 dict,ref_items=0,参考图完全不生效,且不报错;
//   - 点号键 ref_images.ref_image_0 → ref_items=1,conditioning 带 minimax_refs,帧指纹随参考图改变。
//
// 这条测试就是「参考图整条链路是否还活着」的守门人:一旦哪天又写回聚合数组,这里立刻红。
func TestAutogrowKeysAreDotted(t *testing.T) {
	c := testComfySettings()
	job := baseJob("ref2va")
	graph := buildRenderGraph(job, c, "", []string{"a.png", "b.png", "c.png"}, []string{"voice.wav"}, 768, 1344, 124)

	condID, inputs := findNode(t, graph, "MiniMaxH3ReferenceToVideo")
	if condID == "" {
		t.Fatal("ref2va 模式必须使用 MiniMaxH3ReferenceToVideo")
	}

	if got := autogrowSlotCount(inputs, "ref_images"); got != 3 {
		t.Errorf("3 张参考图应提交 3 个 ref_images.* 槽位,实际 %d", got)
	}
	if got := autogrowSlotCount(inputs, "ref_audios"); got != 1 {
		t.Errorf("1 个参考音频应提交 1 个 ref_audios.* 槽位,实际 %d", got)
	}
	for i := 0; i < 3; i++ {
		if !hasAutogrowSlot(inputs, "ref_images", "ref_image_", i) {
			t.Errorf("缺少参考图槽位 ref_images.ref_image_%d", i)
		}
	}
	if !hasAutogrowSlot(inputs, "ref_audios", "ref_audio_", 0) {
		t.Error("缺少参考音频槽位 ref_audios.ref_audio_0")
	}

	// 聚合键本身不得出现:ComfyUI 会把它静默丢成空 dict(见 autogrow.go 的注释)。
	for _, agg := range []string{"ref_images", "ref_videos", "ref_video_audios", "ref_audios"} {
		if _, ok := inputs[agg]; ok {
			t.Errorf("节点上出现了聚合键 %q —— ComfyUI 会静默丢成空 dict,该路参考将完全不生效", agg)
		}
	}

	// 每个槽位都必须真的指向图里存在的节点(否则等于把参考喂给空气)。
	for i := 0; i < 3; i++ {
		key := autogrowKey("ref_images", "ref_image_", i)
		l, ok := inputs[key].([]any)
		if !ok || len(l) != 2 {
			t.Fatalf("%s 不是节点引用,实际 %v", key, inputs[key])
		}
		id, _ := l[0].(string)
		if _, exists := graph[id]; !exists {
			t.Errorf("%s 指向了不存在的节点 %q", key, id)
		}
	}
}

// TestAutogrowNoRefsNoKeys 没有参考图/音频时不应出现任何槽位键
// (空槽位会把 autogrow 的三条自动输入链一起拉出来,平白给普通镜头加噪音)。
func TestAutogrowNoRefsNoKeys(t *testing.T) {
	c := testComfySettings()
	job := baseJob("t2va")
	graph := buildRenderGraph(job, c, "", nil, nil, 768, 1344, 124)

	for _, cls := range []string{"MiniMaxH3ReferenceToVideo", "MiniMaxH3ImageToVideo"} {
		_, inputs := findNode(t, graph, cls)
		if inputs == nil {
			continue
		}
		for _, agg := range []string{"ref_images", "ref_videos", "ref_video_audios", "ref_audios"} {
			if _, ok := inputs[agg]; ok {
				t.Errorf("%s 上出现了聚合键 %q", cls, agg)
			}
			if n := autogrowSlotCount(inputs, agg); n != 0 {
				t.Errorf("%s 上出现了 %d 个 %s.* 槽位,但本次没有任何参考素材", cls, n, agg)
			}
		}
	}
}
