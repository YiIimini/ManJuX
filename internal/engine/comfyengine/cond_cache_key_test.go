package comfyengine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
)

// 条件缓存键的覆盖范围回归(2026-09-26 渲染管线审查,高危)。
//
// 症状没有别的观测面:键漏了某项输入时,渲染**照样成功**,只是画面/口型跟的还是
// 上一次的条件。最典型的一条是音频驱动 —— 同一镜换一版配音重跑,接口返回 ok:true、
// 日志写「✔ 音频驱动完成」,而口型跟着**旧配音**走;其次是换了 CLIP/VAE 或切到
// express 档(int8 VAE)后旧隐空间被复用。
//
// 这里逐个输入做"变一下 → 键必须变"的断言,并额外验证**没变时必须稳定**
// (否则缓存永远不命中,等于白写)。

func keyOf(t *testing.T, job engine.Job, c config.ComfySettings) string {
	t.Helper()
	return condCacheName(job.ProjectID, job.ShotID, job.Prompt, condKeyParts(job, c), job.Width, job.Height, job.Length)
}

// writeFile 造一个内容确定的文件(指纹按内容算)。
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCondCacheKeyCoversEveryConditionInput(t *testing.T) {
	dir := t.TempDir()
	refA := writeFile(t, dir, "ref_a.png", "ref-a")
	refB := writeFile(t, dir, "ref_b.png", "ref-b")
	voice1 := writeFile(t, dir, "voice1.wav", "voice-one")
	voice2 := writeFile(t, dir, "voice2.wav", "voice-two")
	c := testComfySettings()

	base := baseJob("ref2va")
	base.RefImages = []engine.RefImage{{Kind: "character", Path: refA}}

	// 基准:参考图 + 一段配音
	withAudio := base
	withAudio.Audios = []string{voice1}
	baseKey := keyOf(t, withAudio, c)

	// ⓪ 输入完全没变 ⇒ 键必须稳定(否则缓存永远命中不了,两阶段渲染等于白做)
	if got := keyOf(t, withAudio, c); got != baseKey {
		t.Fatalf("输入未变时键必须稳定:%s vs %s", got, baseKey)
	}

	// ① 同一路径、**内容变了**(同名覆盖配音/定妆照)⇒ 键必须变
	overwritten := writeFile(t, dir, "voice1.wav", "voice-one-revised")
	samePathNewContent := base
	samePathNewContent.Audios = []string{overwritten}
	if got := keyOf(t, samePathNewContent, c); got == baseKey {
		t.Error("同路径但配音内容变了,条件缓存键必须失效(否则口型仍跟旧配音,而日志报成功)")
	}

	// ② 换一版配音(不同文件)⇒ 键必须变
	otherVoice := base
	otherVoice.Audios = []string{voice2}
	if got := keyOf(t, otherVoice, c); got == baseKey {
		t.Error("换配音后条件缓存键必须失效 —— 音频是 CONDITIONING 的一等输入(ref_audios)")
	}

	// ③ 去掉音频 ⇒ 键必须变(纯图条件与音图条件不可混用)
	noAudio := base
	if got := keyOf(t, noAudio, c); got == baseKey {
		t.Error("有/无音频参考的键必须不同")
	}

	// ④ 换参考图(内容不同)⇒ 键必须变
	otherRef := base
	otherRef.Audios = []string{voice1}
	otherRef.RefImages = []engine.RefImage{{Kind: "character", Path: refB}}
	if got := keyOf(t, otherRef, c); got == baseKey {
		t.Error("换参考图后条件缓存键必须失效")
	}

	// ⑤ 模式变了(Ref2V ↔ I2V 不是同一个条件节点)⇒ 键必须变
	modeChanged := base
	modeChanged.Audios = []string{voice1}
	modeChanged.Mode = "t2va"
	if got := keyOf(t, modeChanged, c); got == baseKey {
		t.Error("模式变了条件节点就变了,键必须失效")
	}

	// ⑥ 加速档变了(express 换 int8 视频 VAE)⇒ 键必须变
	express := base
	express.Audios = []string{voice1}
	express.Accel = "express"
	if got := keyOf(t, express, c); got == baseKey {
		t.Error("加速档变了(express 走 int8 VAE)键必须失效")
	}

	// ⑦ CLIP / 视频 VAE / 音频 VAE 文件名变了 ⇒ 键必须变
	for _, mut := range []struct {
		name string
		do   func(*config.ComfySettings)
	}{
		{"CLIP", func(s *config.ComfySettings) { s.CLIP = "another_clip.safetensors" }},
		{"视频 VAE", func(s *config.ComfySettings) { s.VideoVAE = "another_video_vae.safetensors" }},
		{"音频 VAE", func(s *config.ComfySettings) { s.AudioVAE = "another_audio_vae.safetensors" }},
	} {
		cc := c
		mut.do(&cc)
		if got := keyOf(t, withAudio, cc); got == baseKey {
			t.Errorf("换 %s 后条件缓存键必须失效(编码器换了,隐空间不再是同一套)", mut.name)
		}
	}

	// ⑧ 渲染尺寸/帧数变了 ⇒ 键必须变(条件图本身按这两个数编码)
	bigger := withAudio
	bigger.Width = withAudio.Width + 64
	if got := keyOf(t, bigger, c); got == baseKey {
		t.Error("画幅变了键必须失效")
	}
	longer := withAudio
	longer.Length = withAudio.Length + 17
	if got := keyOf(t, longer, c); got == baseKey {
		t.Error("帧数变了键必须失效")
	}
}

// TestCondCacheKeyVersionBumps 键的格式版本必须参与哈希。
//
// 它是"换键后旧 .ok 一定命中不了"的唯一保证:若忘了把它掺进哈希,改键组成时就可能
// 与旧键撞上(旧 .pt 里存的是另一种条件),而这种错配没有任何运行期症状。
func TestCondCacheKeyVersionBumps(t *testing.T) {
	job := baseJob("ref2va")
	parts := condKeyParts(job, testComfySettings())
	withV := condCacheName(job.ProjectID, job.ShotID, job.Prompt, parts, 768, 1344, 124)

	old := condKeyVersion
	condKeyVersion = old + "-next"
	defer func() { condKeyVersion = old }()
	next := condCacheName(job.ProjectID, job.ShotID, job.Prompt, parts, 768, 1344, 124)
	if withV == next {
		t.Error("键格式版本变了,缓存名必须跟着变(否则旧格式的缓存可能被误命中)")
	}
}

// TestPurgeLegacyCondCache 键格式换代时的一次性清理。
//
// 不清的后果是旧 .pt 既命中不了、也不会被删(原先只在删项目时才清),
// 而本机该目录实测堆到 4.9GB / 17.4GB —— 只增不减。
func TestPurgeLegacyCondCache(t *testing.T) {
	root := t.TempDir()
	comfyRoot := filepath.Join(root, "comfyui")
	// 本机实际布局:模型在 ComfyUI/shared/models(extra_model_paths 的 base_path)
	ptDir := filepath.Join(comfyRoot, "ComfyUI", "shared", "models", "conditioning")
	sidecar := filepath.Join(root, "data", "cond_cache", "manjux_proj1")
	for _, d := range []string{ptDir, sidecar} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old1, old2 := "s1_aaaaaaaa", "s2_bbbbbbbb"
	for _, n := range []string{old1, old2} {
		if err := os.WriteFile(filepath.Join(sidecar, n+".ok"), []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ptDir, n+".pt"), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 别的项目的记账目录不在本次清理范围
	otherSidecar := filepath.Join(root, "data", "cond_cache", "manjux_proj2")
	if err := os.MkdirAll(otherSidecar, 0o755); err != nil {
		t.Fatal(err)
	}
	otherOk := filepath.Join(otherSidecar, "s1_cccccccc.ok")
	if err := os.WriteFile(otherOk, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := config.ComfySettings{RootDir: comfyRoot}
	purgeLegacyCondCache(c, "proj1")

	for _, n := range []string{old1, old2} {
		if _, err := os.Stat(filepath.Join(sidecar, n+".ok")); !os.IsNotExist(err) {
			t.Errorf("旧格式记账 %s.ok 应被清掉", n)
		}
		if _, err := os.Stat(filepath.Join(ptDir, n+".pt")); !os.IsNotExist(err) {
			t.Errorf("旧格式数据 %s.pt 应被清掉(shared 布局也要能找到)", n)
		}
	}
	if _, err := os.Stat(otherOk); err != nil {
		t.Error("别的项目的记账不得被误删")
	}
	if _, err := os.Stat(filepath.Join(sidecar, ".key"+condKeyVersion)); err != nil {
		t.Error("清理后应写下键版本标记,避免每次渲染都重复扫目录")
	}

	// 第二次调用是空操作:新写的缓存(标记之后建立的)不能被删
	fresh := filepath.Join(sidecar, "s3_dddddddd.ok")
	if err := os.WriteFile(fresh, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	purgeLegacyCondCache(c, "proj1")
	if _, err := os.Stat(fresh); err != nil {
		t.Error("已迁移过的项目不得再清一次 —— 那会把刚预编码好的缓存删掉")
	}
}
