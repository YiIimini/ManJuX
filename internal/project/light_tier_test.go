package project

import "testing"

// light_tier_test.go 场景**时间档**的判据。夹具是**真机原文**：2026-10-08《顺其自然
// （真实事迹短片）》第 01 集 plan.json 里那 7 个场景的 `light_state`，一字未改。
//
// 真机代价：旧词表把「灯 / 烛 / 月」当时间词 ⇒ 这 7 个场景里 **5 个被判成夜戏**，
// 于是这一场每一镜的提示词里都被注入 `This is a NIGHT scene: …`
// （见 pipeline.sceneLightingClause）⇒ 白天场景被模型按夜景渲 ⇒ 成片 13 镜里
// **4 镜被判黑屏**（其中一镜实测 100% 暗）、那几镜的台词**整镜没念**。
// 用户报的是「视频看的莫名其妙的，配音也不对」。
var realSceneLightStates = []struct {
	name  string
	light string
	want  LightTier
}{
	// ① 清晨白天。注意**末尾那一句是否定**（"不得跳到…夜晚"）：它是约束不是描述。
	{"老城邮局营业厅", "清晨七点四十,内景;主光为左侧高窗的冷蓝白日光,柜台暖橙吊灯作补光,相邻镜头不得跳到正午或夜晚。", LightDay},
	{"慈善总会财务窗口", "次日上午,内景;主光为右侧百叶窗切出的冷白日光带,台灯暖光为次光。", LightDay},
	// ② 傍晚：旧口径算"夜"，新口径算"暗光"（不是白天，但也不该被叫夜戏）。
	{"街口募捐点", "1999年12月傍晚,外景;主光为左侧地平线残余的冷蓝暮光,右侧路灯暖黄光为副光。", LightDim},
	{"总会会长办公室", "午后,内景;主光为左侧窗户斜射的暖金日光,室内保持低照度暖灰暗部。", LightDay},
	// ③ 「十一月底」是**月份**、「台灯」是道具 —— 旧词表两个都当夜（这一条是双料误判）。
	{"慈善总会档案室", "今年十一月底,内景;主光为高处小窗的冷白日光柱,绿罩台灯暖光为次光。", LightDay},
	{"山区村小教室", "冬日晴晨,内景;主光为右侧窗墙斜射的暖金日光,过道保持冷灰暗部。", LightDay},
	{"城市地铁站入口", "冬日午后,外景;主光为天顶薄云漫射的冷银灰光,站内暖橙灯为次光。", LightDay},
}

func TestLightTierOfRealBookScenes(t *testing.T) {
	if len(realSceneLightStates) != 7 {
		t.Fatalf("夹具必须是真机 7 个场景，现在是 %d 个", len(realSceneLightStates))
	}
	nights := 0
	for _, c := range realSceneLightStates {
		got := LightTierOf(c.light)
		if got != c.want {
			t.Errorf("场景「%s」判成 %v，应为 %v\n  光态原文：%s", c.name, got, c.want, c.light)
		}
		if got == LightNight {
			nights++
		}
	}
	// 这一集的现场是"5/7 被判夜戏"，修好后必须**一镜夜戏都没有**。
	if nights != 0 {
		t.Errorf("真机这 7 个场景里还有 %d 个被判成夜戏（修好前是 5 个）—— 白天场景会被渲成夜景", nights)
	}
}

// TestLightPropWordsAreNotTimeEvidence 「灯 / 烛 / 月」不是时间词：它们白天夜里都出现。
func TestLightPropWordsAreNotTimeEvidence(t *testing.T) {
	cases := []struct {
		light string
		want  LightTier
	}{
		{"室内点着一盏油灯,主光来自头顶吊灯。", LightUnknown},   // 灯：道具
		{"农历八月,内景;灯笼高挂,桌上两支红烛。", LightUnknown}, // 月（月份）+ 烛（道具）+ 灯（道具）
		{"傍晚,外景;路灯暖黄光为副光。", LightDim},          // 有"傍晚"⇒ 暗光（路灯只是副光，不参与判）
		{"夜晚,外景;只有远处一盏路灯。", LightNight},        // 有"夜晚"⇒ 夜（灯仍是道具）
		{"月夜,外景;月光下只有一条土路。", LightNight},       // "夜"是真时间词
	}
	for _, c := range cases {
		if got := LightTierOf(c.light); got != c.want {
			t.Errorf("光态「%s」判成 %v，应为 %v", c.light, got, c.want)
		}
	}
}

// TestLightConstraintClauseIsNotEvidence **否定小句**不参与判据。
//
// 真机邮局那条的末尾是「相邻镜头不得跳到正午或夜晚」—— 若按字面匹配到「夜晚」，
// 整场（白天）就被判夜戏，模型按夜景渲。
func TestLightConstraintClauseIsNotEvidence(t *testing.T) {
	cases := []struct {
		light string
		want  LightTier
	}{
		{"清晨七点四十,内景;相邻镜头不得跳到正午或夜晚。", LightDay},
		{"白天,内景;不能太暗,也不要出现夜晚的蓝调。", LightDay},
		{"午后,外景;避免黄昏的暖调,保持日光。", LightDay},
		{"morning, interior; do not jump to a night look.", LightDay},
		// 但**独立成句**的夜仍是夜（约束只吃掉它自己那一小句）。
		{"夜里,外景。相邻镜头不得跳到白天。", LightNight},
	}
	for _, c := range cases {
		if got := LightTierOf(c.light); got != c.want {
			t.Errorf("光态「%s」判成 %v，应为 %v", c.light, got, c.want)
		}
	}
}

// TestPlanUsesNightSceneRealBook 真机那 7 个场景**一条夜戏都不算**（旧口径会算出 5 条）。
func TestPlanUsesNightSceneRealBook(t *testing.T) {
	plan := &Plan{}
	perScene := map[string]int{}
	byID := map[string]Scene{}
	byName := map[string]Scene{}
	for i, c := range realSceneLightStates {
		id := "scene_" + string(rune('a'+i))
		sc := Scene{ID: id, Name: c.name, LightState: c.light}
		plan.Scenes = append(plan.Scenes, sc)
		perScene[id] = 1
		byID[id] = sc
		byName[c.name] = sc
	}
	if planUsesNightScene(plan, perScene, byID, byName) {
		t.Error("真机这 7 个场景被判成\"用到了夜戏\"—— 旧词表的误判会顺着这条路影响场次节检查")
	}
}

// TestBriefHasNightRealSceneTimes 剧本场次表（`time` 列）的夜场判据同一条口径。
func TestBriefHasNightRealSceneTimes(t *testing.T) {
	cases := []struct {
		times []string
		want  bool
	}{
		{[]string{"清晨", "上午", "午后", "傍晚"}, false},
		{[]string{"清晨", "夜", "午后"}, true},
		{[]string{"夜晚", "外"}, true},
		{[]string{"傍晚", "黄昏"}, false}, // 傍晚不是夜（旧口径也不算，这一点两版一致）
	}
	for _, c := range cases {
		var scenes []ScriptScene
		for _, tm := range c.times {
			scenes = append(scenes, ScriptScene{Time: tm})
		}
		if got := briefHasNight(scenes); got != c.want {
			t.Errorf("时段 %v 判成 %v，应为 %v", c.times, got, c.want)
		}
	}
}
