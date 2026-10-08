package pipeline

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// scene_exposure_test.go 场景参考图的**曝光自检**。
//
// 夹具两个来源，都是真机实测：
//   - 光态原文 = 2026-10-08《顺其自然（真实事迹短片）》第 01 集 plan.json 的 7 个场景（一字未改）；
//   - 平均亮度 = 那 7 张场景图实测（0-255，1×1 缩小后取灰度）。
//
// 现场：白天场景的三张图只有 58/66/77，而提示词里写着 lighting comes from it
// ⇒ 整场镜镜偏黑 ⇒ 成片 4 镜被判黑屏（其中一镜 100% 暗）、那几镜的台词整镜没念。

// realSceneExposure 真机 7 张场景图：光态原文 + 实测平均亮度。
var realSceneExposure = []struct {
	name      string
	light     string
	luma      float64
	wantWarn  bool
	wantTier  project.LightTier
	wantWords string // 告警里必须出现的词（wantWarn 时）
}{
	// 亮度列 = 用**平台自己的 imageMeanLuma** 在真机那 7 张图上量的（zz_probe_scene_luma_live_test.go
	// 就是量它用的探针），不是估的。
	{"老城邮局营业厅", "清晨七点四十,内景;主光为左侧高窗的冷蓝白日光,柜台暖橙吊灯作补光,相邻镜头不得跳到正午或夜晚。", 67.8, true, project.LightDay, "白天"},
	{"总会会长办公室", "午后,内景;主光为左侧窗户斜射的暖金日光,室内保持低照度暖灰暗部。", 70.7, true, project.LightDay, "白天"},
	{"慈善总会档案室", "今年十一月底,内景;主光为高处小窗的冷白日光柱,绿罩台灯暖光为次光。", 74.1, true, project.LightDay, "白天"},
	// 傍晚（暗光）：89.1 分不算"白天场景偏暗"，暗光阈值 60 —— 不该报。
	{"街口募捐点", "1999年12月傍晚,外景;主光为左侧地平线残余的冷蓝暮光,右侧路灯暖黄光为副光。", 89.1, false, project.LightDim, ""},
	{"慈善总会财务窗口", "次日上午,内景;主光为右侧百叶窗切出的冷白日光带,台灯暖光为次光。", 110.2, false, project.LightDay, ""},
	{"山区村小教室", "冬日晴晨,内景;主光为右侧窗墙斜射的暖金日光,过道保持冷灰暗部。", 100.0, false, project.LightDay, ""},
	{"城市地铁站入口", "冬日午后,外景;主光为天顶薄云漫射的冷银灰光,站内暖橙灯为次光。", 157.1, false, project.LightDay, ""},
}

func TestSceneExposureVerdictRealBook(t *testing.T) {
	warned := 0
	for _, c := range realSceneExposure {
		sc := project.Scene{ID: "scene_x", Name: c.name, LightState: c.light}
		got := sceneExposureVerdict(sc, c.luma)
		if !c.wantWarn {
			if got != "" {
				t.Errorf("场景「%s」（%v/亮度 %.0f）不该告警，却报了：%s", c.name, c.wantTier, c.luma, got)
			}
			continue
		}
		warned++
		if got == "" {
			t.Errorf("场景「%s」是白天场景、图只有 %.0f/255，却没告警 —— 整场会被渲黑", c.name, c.luma)
			continue
		}
		if c.wantWords != "" && !strings.Contains(got, c.wantWords) {
			t.Errorf("场景「%s」的告警要点明光态档位（缺「%s」）：%s", c.name, c.wantWords, got)
		}
		// 告警必须说清后果与改法（用户看不到提示词，只能看日志）。
		for _, need := range []string{"整体偏暗", "重新生成"} {
			if !strings.Contains(got, need) {
				t.Errorf("场景「%s」的告警缺「%s」：%s", c.name, need, got)
			}
		}
		// 告警必须带上**这一张**的实测亮度（用户要能拿去核对）
		if !strings.Contains(got, strconv.FormatFloat(c.luma, 'f', 1, 64)) {
			t.Errorf("场景「%s」的告警没带上实测亮度 %.1f：%s", c.name, c.luma, got)
		}
	}
	if warned != 3 {
		t.Errorf("真机 7 张场景图里应有 3 张告警（邮局 67.8 / 会长办公室 70.7 / 档案室 74.1），实际 %d 张", warned)
	}
}

// TestSceneExposureVerdictEdgeCases 夜戏本来就该暗；光态空着由别的门去报，这里不重复。
func TestSceneExposureVerdictEdgeCases(t *testing.T) {
	night := project.Scene{Name: "巷口", LightState: "夜,外景;只有远处一盏路灯。"}
	if got := sceneExposureVerdict(night, 12); got != "" {
		t.Errorf("夜戏不该因偏暗告警：%s", got)
	}
	blank := project.Scene{Name: "某场", LightState: ""}
	if got := sceneExposureVerdict(blank, 10); got != "" {
		t.Errorf("光态空着不该在这里报（交给场景卡缺 light_state 那道门）：%s", got)
	}
	// 光态没写时间（灯不算判据）⇒ 用更保守的下限 55：偏暗要说，但不算"白天场景"。
	unknown := project.Scene{Name: "屋里", LightState: "室内点着一盏油灯。"}
	if got := sceneExposureVerdict(unknown, 40); got == "" {
		t.Error("光态没写时间的场景图 40/255 也该提醒（下限 55）")
	}
	if got := sceneExposureVerdict(unknown, 80); got != "" {
		t.Errorf("光态没写时间、亮度 80 不该告警：%s", got)
	}
}

// TestImageMeanLumaReadsPixels 亮度是**量出来的**，不是估的。
func TestImageMeanLumaReadsPixels(t *testing.T) {
	dir := t.TempDir()
	for _, v := range []uint8{20, 100, 200} {
		p := filepath.Join(dir, "gray.png")
		img := image.NewGray(image.Rect(0, 0, 40, 24))
		for i := range img.Pix {
			img.Pix[i] = v
		}
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			f.Close()
			t.Fatal(err)
		}
		f.Close()
		got, ok := imageMeanLuma(p)
		if !ok {
			t.Fatalf("灰度 %d 的 PNG 读不出亮度", v)
		}
		if got < float64(v)-1.5 || got > float64(v)+1.5 {
			t.Errorf("灰度 %d 的图量出 %.2f（应≈%d）", v, got, v)
		}
	}
	if _, ok := imageMeanLuma(filepath.Join(dir, "nope.png")); ok {
		t.Error("文件不存在时必须返回 ok=false（不许编一个亮度出来）")
	}
}

// TestSceneLightingClauseTierWiring 场级光照锚：白天场景**不许**被写成夜戏。
//
// 真机这一集 7 个场景里 5 个被判夜戏，注入的就是这一句 This is a NIGHT scene。
func TestSceneLightingClauseTierWiring(t *testing.T) {
	plan := &project.Plan{}
	for i, c := range realSceneExposure {
		plan.Scenes = append(plan.Scenes, project.Scene{ID: sceneID(i), Name: c.name, LightState: c.light})
	}
	for i, c := range realSceneExposure {
		shot := project.Shot{Scene: sceneID(i)}
		got := sceneLightingClause(plan, &shot)
		isNight := strings.Contains(got, "This is a NIGHT scene")
		switch c.wantTier {
		case project.LightDay:
			if isNight {
				t.Errorf("白天场景「%s」的光照锚里写了 NIGHT scene（真机就是这么把 5 个白天场景渲成夜景的）：%s", c.name, got)
			}
			if !strings.Contains(got, "DAYLIT") {
				t.Errorf("白天场景「%s」的光照锚缺 DAYLIT 约束：%s", c.name, got)
			}
		case project.LightDim:
			if isNight {
				t.Errorf("傍晚场景「%s」被当成夜戏了：%s", c.name, got)
			}
			if !strings.Contains(got, "dusk/dawn") {
				t.Errorf("傍晚场景「%s」的光照锚缺低光约束（dusk/dawn 那句）：%s", c.name, got)
			}
		}
		// 光态原文必须照抄进场（模型靠它统一同一场戏的光）
		if !strings.Contains(got, c.light) {
			t.Errorf("场景「%s」的光照锚没照抄光态原文", c.name)
		}
	}
	// 真夜戏仍要有"画面内可见光源"那句。
	nightPlan := &project.Plan{Scenes: []project.Scene{{ID: "s1", Name: "巷口", LightState: "夜,外景;只有远处一盏路灯。"}}}
	got := sceneLightingClause(nightPlan, &project.Shot{Scene: "s1"})
	if !strings.Contains(got, "This is a NIGHT scene") || !strings.Contains(got, "visible in-frame light source") {
		t.Errorf("真夜戏缺\"必须有画面内光源\"那句：%s", got)
	}
}

func sceneID(i int) string { return "scene_" + string(rune('a'+i)) }

// TestSceneExposureIsWiredIntoAssets 曝光自检必须**接在资产阶段**（场景图一出图就量）。
//
// 此前只有成片侧的 QC 看得见"这场戏被渲黑了"—— 那时 13 镜已经白渲完、用户已经在看片了。
func TestSceneExposureIsWiredIntoAssets(t *testing.T) {
	raw, err := os.ReadFile("stages_assets.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, "sceneExposureVerdict(") {
		t.Error("资产阶段没有接场景图曝光自检 —— 白天场景出了暗图要等成片才被发现")
	}
	if !strings.Contains(src, "imageMeanLuma(path)") {
		t.Error("曝光自检要在**刚生成的那张图**上量（imageMeanLuma(path)）")
	}
	if !strings.Contains(src, `if kind == "scenes" {`) {
		t.Error("只有场景图适用（角色定妆照是影棚白底，量了会天天误报）")
	}
	// 必须排在生成之后：那时才有 path。
	iGen := strings.Index(src, "genAsset(kind, id, name, imagePrompt")
	iCheck := strings.Index(src, "sceneExposureVerdict(")
	if iGen < 0 || iCheck < 0 || iCheck < iGen {
		t.Error("曝光自检要排在场景图生成之后（否则拿不到图）")
	}
	// 只 warn、不自动重出（重出要 GPU，亮不亮是创作判断）—— 由用户点「重新生成」。
	if !strings.Contains(src, `"warn", 0, "⚠ 场景「%s」%s"`) {
		t.Error("曝光问题要以 warn 如实播报（不许静默，也不许替用户自动重出）")
	}
}
