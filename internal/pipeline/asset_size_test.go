package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 资产画幅闸门(甲2):定妆照/场景图的生成尺寸必须与**成片画幅**一致。
//
// 事故形态:参考图写死 832×1248(2:3),成片 768×1344(9:16) —— H3 拿一张比例不对的
// 参考图去渲画面,只能自行裁切/拉伸,构图漂移(人脸被拉宽、头顶被切)从第一帧就埋下了。
// 多视角转台图同理:它是横向拼图,但**单格比例必须与主图一致**。

func projWithRender(w, h int) *project.Project {
	p := &project.Project{}
	p.Config.Params = project.DefaultStageParams()
	p.Config.Params.Render.Width, p.Config.Params.Render.Height = w, h
	return p
}

// TestAssetImageSizeFollowsRenderCanvas 尺寸口径只有一个来源:项目的 render 画幅。
func TestAssetImageSizeFollowsRenderCanvas(t *testing.T) {
	cases := []struct {
		name         string
		proj         *project.Project
		wantW, wantH int
	}{
		{"出厂竖幅 9:16", projWithRender(768, 1344), 768, 1344},
		{"横幅 16:9", projWithRender(1344, 768), 1344, 768},
		{"方形", projWithRender(768, 768), 768, 768},
		{"render 未填 → 平台默认 768×1344", &project.Project{}, 768, 1344},
		{"没有项目上下文 → 平台默认 768×1344", nil, 768, 1344},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, h := assetImageSize(tc.proj, "characters")
			if w != tc.wantW || h != tc.wantH {
				t.Fatalf("assetImageSize = %d×%d,期望 %d×%d", w, h, tc.wantW, tc.wantH)
			}
		})
	}
	// 与成片同一口径:改了成片画幅,资产尺寸必须跟着改(不是各写各的常量)
	a := projWithRender(768, 1344)
	b := projWithRender(1344, 768)
	aw, ah := assetImageSize(a, "characters")
	bw, bh := assetImageSize(b, "characters")
	if aw == bw && ah == bh {
		t.Error("成片画幅改了而资产尺寸没变 —— 参考图与成片又会不同比例")
	}
}

// TestPropAssetIsSquare 道具图是**方幅**：这条是"跟随成片画幅"那条口径的**明确例外**。
//
// 理由（2026-09-27 补道具图那轮）：技能侧那张卡的规范是「1:1 白底单件」，
// 而道具图不进 H3 参考图集合（官方：道具只写文字）—— 竖幅里画一件居中静物等于大半张白纸。
// 断言写在这里，是为了让下一个人看到"角色/场景跟画幅、道具不跟"时知道这是**定的**，不是漏改。
func TestPropAssetIsSquare(t *testing.T) {
	for _, tc := range []struct{ w, h, want int }{
		{768, 1344, 768}, // 出厂竖幅 → 取短边
		{1344, 768, 768}, // 横幅 → 同样取短边
		{1024, 1024, 1024},
		{1080, 1920, 1072}, // 1080 不是 16 的倍数 → 向下对齐
	} {
		p := projWithRender(tc.w, tc.h)
		w, h := assetImageSize(p, "props")
		if w != h {
			t.Errorf("道具图必须是正方形，实际 %d×%d（项目 %d×%d）", w, h, tc.w, tc.h)
		}
		if w != tc.want {
			t.Errorf("道具图边长应为短边向下对齐 16 的倍数（期望 %d），实际 %d", tc.want, w)
		}
	}
	// 方形项目里两者应当一致（别为了道具又引一套系数）
	p := projWithRender(768, 768)
	cw, ch := assetImageSize(p, "characters")
	pw, ph := assetImageSize(p, "props")
	if cw != pw || ch != ph {
		t.Errorf("方幅项目里角色与道具应同尺寸，实际 角色 %d×%d / 道具 %d×%d", cw, ch, pw, ph)
	}
}

// TestNoHardcodedAssetResolution 结构性闸门:资产生成路径里不得再写死 832×1248。
func TestNoHardcodedAssetResolution(t *testing.T) {
	for _, f := range []string{"stages_assets.go", "coordinator.go"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读不到 %s: %v", f, err)
		}
		for i, ln := range strings.Split(string(raw), "\n") {
			code := ln
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx]
			}
			if strings.Contains(code, "832") || strings.Contains(code, "1248") {
				t.Errorf("%s:%d 资产画幅仍写死:%s —— 必须走 assetImageSize(项目成片画幅)",
					f, i+1, strings.TrimSpace(ln))
			}
		}
	}
}

// TestStripFrameBox 视图条单格的框:取主图(定妆照)自身的像素,读不到才回落默认。
func TestStripFrameBox(t *testing.T) {
	if w, h := stripFrameBox(768, 1344); w != 768 || h != 1344 {
		t.Errorf("有主图尺寸时应用它,实际 %d×%d", w, h)
	}
	if w, h := stripFrameBox(0, 0); w != DefaultAssetWidth || h != DefaultAssetHeight {
		t.Errorf("读不到主图尺寸时应回落到 %d×%d,实际 %d×%d",
			DefaultAssetWidth, DefaultAssetHeight, w, h)
	}
}

// TestTurnaroundFrameSizeFollowsMaster 转身视频的渲出色度 = 定妆照的像素
// (旧实现写死 832×1248:定妆照换成 9:16 后,抽帧与主图不同比例,拼图只能靠裁切对齐)。
func TestTurnaroundFrameSizeFollowsMaster(t *testing.T) {
	dir := t.TempDir()
	master := filepath.Join(dir, "char_x.png")
	writeTestPNG(t, master, 768, 1344)
	if w, h := turnaroundFrameSize(master); w != 768 || h != 1344 {
		t.Errorf("转身视频画幅应等于定妆照像素(768×1344),实际 %d×%d", w, h)
	}
	// 读不到(文件不存在)时回落平台默认,不猜
	if w, h := turnaroundFrameSize(filepath.Join(dir, "none.png")); w != DefaultAssetWidth || h != DefaultAssetHeight {
		t.Errorf("读不到定妆照尺寸时应回落 %d×%d,实际 %d×%d",
			DefaultAssetWidth, DefaultAssetHeight, w, h)
	}
}
