package pipeline

import (
	"image"
	"os"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// scene_exposure.go 场景参考图的**曝光自检**（确定性：量像素，不调模型）。
//
// 【为什么要有它（2026-10-08 真机《顺其自然（真实事迹短片）》）】场景图是这一场**每一镜**的
// 光照来源 —— 提示词里明写着「background layout, props and lighting come from it」。
// 实测那一集 7 张场景图的平均亮度（0-255，1×1 缩小后取灰度）：
//
//	老城邮局营业厅   58   ← 光态写「清晨七点四十…冷蓝白日光」        （白天场景，图是暗的）
//	总会会长办公室   66   ← 光态写「午后…暖金日光…斜射」            （白天场景，图是暗的）
//	慈善总会档案室   77   ← 光态写「今年十一月底…冷白日光柱」        （白天场景，图是暗的）
//	街口募捐点       87   ← 光态写「1999年12月傍晚…暮光」（暗光，合理）
//	慈善总会财务窗口 115
//	山区村小教室     104
//	城市地铁站入口   165
//
// 而角色定妆照是 154~198（影棚白底）。成片里那 4 镜被判黑屏（一镜 100% 暗）、台词整镜没念。
// 这条自检把「白天场景出了一张暗图」在**资产阶段**就点出来（此前只有成片侧才看得见）。
//
// 判据只有一条、可复算：平均灰度 < 阈值（白天 90 / 暗光 60）⇒ warn 并给出实测值与该场每一镜的后果。
// **不自动重出**（重出要 GPU、且亮不亮是创作判断）—— 只如实播报，由用户点「重新生成」。
const (
	sceneLumaMinDay       = 90.0
	sceneLumaMinDim       = 60.0
	sceneLumaUnknownFloor = 55.0
)

// imageMeanLuma 一张图的平均灰度（0-255）。抽样步长按边长自适应，几千像素就够判断"整体偏暗"。
func imageMeanLuma(path string) (float64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return 0, false
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return 0, false
	}
	step := 1
	for b.Dx()/step > 64 || b.Dy()/step > 64 {
		step *= 2
	}
	var sum, n float64
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			r, g, bl, _ := img.At(x, y).RGBA() // 16 位定点
			// 亮度按 Rec.601 权重（与 ffmpeg 的 YAVG 口径接近，便于两边对照）
			sum += (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 257.0
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return sum / n, true
}

// sceneExposureVerdict 这张场景图相对它的**光态**是不是太暗了（返回说明，空 = 没问题）。
//
// 只对场景图用；角色定妆照是影棚白底，不适用。
func sceneExposureVerdict(sc project.Scene, luma float64) string {
	tier := project.LightTierOf(sc.LightState)
	min := sceneLumaUnknownFloor
	label := "光态没写明时间"
	switch tier {
	case project.LightDay:
		min, label = sceneLumaMinDay, "光态是白天"
	case project.LightDim:
		min, label = sceneLumaMinDim, "光态是暗光（傍晚/黄昏）"
	case project.LightNight:
		return "" // 夜戏本来就该暗：不报
	}
	if strings.TrimSpace(sc.LightState) == "" {
		return "" // 光态空着由别的门去报，这里不重复
	}
	if luma >= min {
		return ""
	}
	return "这张场景图**整体偏暗**（平均亮度实测 " + strconv.FormatFloat(luma, 'f', 1, 64) + "/255，阈值 " + strconv.FormatFloat(min, 'f', 1, 64) +
		"；" + label + "：" + firstSentence(sc.LightState) + "）—— 场景图是这一场**每一镜**的光照来源" +
		"（提示词写着 lighting comes from it），它偏暗这场戏就会镜镜偏黑（真机实测那一集 4 镜被判黑屏、" +
		"其中一镜 100% 暗，连带整镜没念台词）。改法：对这一张点「重新生成」，或在场景卡里把光态与" +
		"描述写得更亮（正对窗、白天、室内照明充足），再重出"
}

// sceneCardFor 按资产 id 找场景卡（资产阶段拿到的 id 就是场景卡的 id）。
func sceneCardFor(plan *project.Plan, id string) (project.Scene, bool) {
	if plan == nil {
		return project.Scene{}, false
	}
	want := strings.TrimSpace(id)
	for _, sc := range plan.Scenes {
		if sc.ID == want || "scene:"+sc.ID == want || sc.Name == want {
			return sc, true
		}
	}
	return project.Scene{}, false
}
