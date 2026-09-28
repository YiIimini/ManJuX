package comfyengine

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine"
)

// 配乐图的**契约测试**（不需要模型，只校验图的形状与官方蓝本一致）。
//
// 为什么值得单独写：这一整块知识的来源是"读本机 ComfyUI 源码 + 官方蓝本"（见
// docs/ACE-Step接入记录.md），而 ComfyUI 升级会改名、改输入。图构造错了，症状是
// "生成失败/产物不对"，排查起来很远；这里把契约钉死，drift 一眼可见。
func TestMusicGraphContract(t *testing.T) {
	m := DefaultMusicModels()
	g := buildMusicGraph(m, engine.MusicJob{Tags: "calm guzheng, slow, instrumental", Seconds: 90, BPM: 70}, 12345, "bgm_test")

	// ① 节点类名必须与本地 ComfyUI 的 node_id 一致（一个字母都不能差）
	wantTypes := map[string]string{
		"1":  "UNETLoader",
		"2":  "DualCLIPLoader",
		"3":  "VAELoader",
		"4":  "ModelSamplingAuraFlow",
		"5":  "TextEncodeAceStepAudio1.5",
		"6":  "ConditioningZeroOut",
		"7":  "EmptyAceStep1.5LatentAudio",
		"8":  "KSampler",
		"9":  "VAEDecodeAudio",
		"10": "SaveAudioMP3",
	}
	for id, want := range wantTypes {
		nd, ok := g[id].(map[string]any)
		if !ok {
			t.Fatalf("图里缺少节点 %s", id)
		}
		if got := nd["class_type"]; got != want {
			t.Errorf("节点 %s 的类名应是 %s，实际 %v", id, want, got)
		}
	}

	// ② DualCLIPLoader：**两个编码器都要**且 type 必须是 ace（我最初误以为 4B 可选，实测被纠正）
	dual := g["2"].(map[string]any)["inputs"].(map[string]any)
	if dual["clip_name1"] != m.TextEncoder1 || dual["clip_name2"] != m.TextEncoder2 {
		t.Errorf("两个文本编码器都要给：%v / %v", dual["clip_name1"], dual["clip_name2"])
	}
	if dual["type"] != "ace" {
		t.Errorf("DualCLIPLoader 的 type 必须是 ace，实际 %v", dual["type"])
	}

	// ③ 文本编码节点：纯器乐（lyrics 空）、时长与速度照传、种子可复现
	enc := g["5"].(map[string]any)["inputs"].(map[string]any)
	if enc["lyrics"] != "" {
		t.Errorf("配乐必须是纯器乐（lyrics 空），实际 %v", enc["lyrics"])
	}
	if enc["tags"] != "calm guzheng, slow, instrumental" {
		t.Errorf("tags 要原样传给模型，实际 %v", enc["tags"])
	}
	if enc["duration"] != 90.0 {
		t.Errorf("duration 要传目标秒数，实际 %v", enc["duration"])
	}
	if enc["bpm"] != 70 {
		t.Errorf("bpm 要传，实际 %v", enc["bpm"])
	}
	if enc["seed"] != int64(12345) {
		t.Errorf("种子要传（同一颗种子可复现同一段曲子），实际 %v", enc["seed"])
	}

	// ④ 空 latent 的秒数与文本编码一致（两处对不上会生成一段长度不符的曲子）
	lat := g["7"].(map[string]any)["inputs"].(map[string]any)
	if lat["seconds"] != 90.0 {
		t.Errorf("空 latent 的秒数必须与 duration 一致，实际 %v", lat["seconds"])
	}

	// ⑤ 连线必须真的接上（形状对但线没接，症状是"生成成功但产物不对"）
	ks := g["8"].(map[string]any)["inputs"].(map[string]any)
	for _, k := range []string{"model", "positive", "negative", "latent_image"} {
		if _, ok := ks[k].([]any); !ok {
			t.Errorf("KSampler 的 %s 必须是连线", k)
		}
	}
	dec := g["9"].(map[string]any)["inputs"].(map[string]any)
	if _, ok := dec["samples"].([]any); !ok {
		t.Error("VAEDecodeAudio 的 samples 必须是连线")
	}

	// ⑥ 模型文件名照配置走（上游 repack 改名时只改配置，不改代码）
	if g["1"].(map[string]any)["inputs"].(map[string]any)["unet_name"] != m.Diffusion {
		t.Error("扩散模型名要来自配置")
	}
	if g["3"].(map[string]any)["inputs"].(map[string]any)["vae_name"] != m.VAE {
		t.Error("VAE 名要来自配置")
	}
}

// TestMusicGraphDefaults 缺省值必须兜住（BPM/秒数为 0 时不能把 0 传给模型）。
func TestMusicGraphDefaults(t *testing.T) {
	g := buildMusicGraph(DefaultMusicModels(), engine.MusicJob{Tags: "x"}, 1, "p")
	enc := g["5"].(map[string]any)["inputs"].(map[string]any)
	if enc["bpm"] != 120 {
		t.Errorf("BPM 缺省应为 120，实际 %v", enc["bpm"])
	}
	if enc["duration"] != 120.0 {
		t.Errorf("时长缺省应为 120 秒，实际 %v", enc["duration"])
	}
}

// TestGenerateMusicRejectsEmptyTags 空提示词必须拒绝（否则会生成一段随机曲子还报成功）。
func TestGenerateMusicRejectsEmptyTags(t *testing.T) {
	e := &Engine{}
	if _, err := e.GenerateMusic(nil, engine.MusicJob{OutputDir: "d", OutputName: "n"}); err == nil {
		t.Error("配乐提示词为空时必须报错")
	}
	if _, err := e.GenerateMusic(nil, engine.MusicJob{Tags: "x"}); err == nil {
		t.Error("落点未指定时必须报错")
	}
}
