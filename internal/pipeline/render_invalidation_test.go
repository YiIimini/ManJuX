package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 单镜 seed 覆盖必须进渲染指纹(审计:ShotTuneDialog 的 seed 输入框静默不生效)。
//
// 旧实现把**项目配置种子**写进指纹,而真正参与采样的是 ShotSeed 派生值 —— 于是
// 用户不动提示词/时长/模式、只改 seed 重渲时,inputFP 逐字不变 → 走"产物有效,跳过"
// 分支 → 视频还是旧的那条,前端却弹「已按微调参数重渲」。
func TestSeedOverrideEntersRenderFingerprint(t *testing.T) {
	rp := project.RenderParams{Width: 768, Height: 1344, Steps: 8, Seed: 0}
	shot := project.Shot{ID: 7, Characters: []string{"char_01"}}

	// 没显式换 seed:指纹里的种子与旧版逐字一致(rp.Seed)——
	// 否则升级一版就会让存量项目的已渲镜头全部判失效(几十集全量重渲)。
	if got, want := fingerprintSeed(rp, 0, shot), rp.Seed; got != want {
		t.Errorf("未覆盖时指纹种子应为项目配置值 %d(保持与旧指纹串一致),实际 %d", want, got)
	}

	// 显式换 seed:必须变成真正参与采样的那个值
	const override = 12345
	got := fingerprintSeed(rp, override, shot)
	if got == rp.Seed {
		t.Fatal("单镜 seed 覆盖没有进指纹 —— 换 seed 重渲会被断点续跑判成'产物有效'而跳过")
	}
	if want := ShotSeed(rp.Seed, override, shot.ID, shot.Characters); got != want {
		t.Errorf("指纹里的种子应与实际采样种子一致(%d),实际 %d", want, got)
	}
	// 稳定性:同参数必须稳定,否则每次续跑都全量重渲
	if again := fingerprintSeed(rp, override, shot); again != got {
		t.Error("同参数两次指纹种子不一致 —— 续跑判据会永远失配")
	}
	// 换一个 seed 值必须换指纹(这才是"抽卡/微调"的意义)
	if fingerprintSeed(rp, override+1, shot) == got {
		t.Error("不同 seed 覆盖应得到不同指纹种子")
	}
	// 换镜号/换角色组合也要变(ShotSeed 的既有语义)
	if fingerprintSeed(rp, override, project.Shot{ID: 8, Characters: shot.Characters}) == got {
		t.Error("不同镜号应得到不同指纹种子")
	}
	// 整串指纹层面再钉一次:端到端可复现
	fpA := renderFingerprint("p", nil, rp, "ref2va", fingerprintSeed(rp, override, shot))
	fpB := renderFingerprint("p", nil, rp, "ref2va", fingerprintSeed(rp, 0, shot))
	if fpA == fpB {
		t.Error("换 seed 后整串指纹必须不同,否则跳过判据仍会命中旧产物")
	}
}

// 渲染模型身份标签要覆盖"同样决定画面、但不体现在底模文件名里"的全局设置,
// 同时**未偏离默认值时不得改变标签串**(否则升级一版把存量镜头全判失效)。
func TestRenderModelTagCoversPictureAffectingSettings(t *testing.T) {
	def := config.Default()
	g := *def
	base := renderModelTag(g, "ref2va", "comfy")
	if strings.Contains(base, "|") {
		t.Errorf("全默认设置下不该附加任何运行期标记(会让存量项目全部重渲),实际 %q", base)
	}
	// 幂等/稳定
	if again := renderModelTag(g, "ref2va", "comfy"); again != base {
		t.Error("同设置两次标签不一致 —— 续跑判据会永远失配")
	}

	// 换 VAE → 画面变了,标签必须变
	g2 := *def
	g2.Comfy.VideoVAE = "another_vae.safetensors"
	if renderModelTag(g2, "ref2va", "comfy") == base {
		t.Error("换 VideoVAE 必须改模型标签:否则用户换了 VAE 点重渲,所有镜头仍被判'产物有效'跳过")
	}
	// 换 CLIP 同理
	g3 := *def
	g3.Comfy.CLIP = "another_clip.safetensors"
	if renderModelTag(g3, "ref2va", "comfy") == base {
		t.Error("换 CLIP 必须改模型标签")
	}
	// 显式关掉 SageAttention(它影响画面)
	g4 := *def
	off := false
	g4.Comfy.SageAttention = &off
	if renderModelTag(g4, "ref2va", "comfy") == base {
		t.Error("显式关掉 SageAttention 必须改模型标签")
	}
	// 云端分辨率(768P → 2K)是真正的出像素规格
	apiBase := renderModelTag(*def, "ref2va", "api")
	g5 := *def
	g5.MiniMax.Resolution = "2K"
	if renderModelTag(g5, "ref2va", "api") == apiBase {
		t.Error("云端分辨率改了必须改模型标签,否则调成 2K 后重渲仍是 768P 的画面")
	}
	// nil(自动)与 true 是同一个状态:不得产生标记差异
	on := true
	g6 := *def
	g6.Comfy.SageAttention = &on
	if renderModelTag(g6, "ref2va", "comfy") != base {
		t.Error("SageAttention 显式 true 与默认(自动)是同一状态,不该改标签")
	}
}
