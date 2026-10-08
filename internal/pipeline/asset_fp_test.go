package pipeline

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 合成前的新旧资产混用核对（2026-09-28 全面审计）。
//
// 官方 qc-checklist 的重生成纪律："用户重做任何产物后，下游必须全用最新批准版本，且**不得在
// 最终拼接里静默混用新旧资产**"。真实事故：换了定妆照只重渲前 5 镜 ⇒ 成片里同一个人前后两张脸。
//
// 【2026-10-08 深度排查：夹具照**生产键形态**重写】
// 资产表的键是 `char:<卡id>` / `scene:<卡id>` / `prop:<卡id>`（refs.go:113/174/200、
// stages_assets.go 的 check("characters", "char:"+ch.ID…)）。原来这份夹具照**裸 id** 写，
// 于是 shotAssetFP 直接 `assets[id]` 查表时"夹具命中、生产落空"—— 这道门在生产里
// **每一次查表都落空**，指纹只随镜内 id 列表变、与定妆照指纹无关，测试却一直绿。
// 现在夹具照生产写：一旦有人把键改回裸 id，下面那几条"换指纹必须变"的断言立刻变红。
func TestStaleAssetClips(t *testing.T) {
	assets := map[string]project.Asset{
		"char:char_a":    {ID: "char_a", Kind: "characters", File: "a.png", Fingerprint: "fp-old-a"},
		"scene:scene_rm": {ID: "scene_rm", Kind: "scenes", File: "r.png", Fingerprint: "fp-room"},
		"char:char_bot":  {ID: "char_bot", Kind: "characters", File: "bot.png", Fingerprint: "fp-bot"},
		"prop:prop_holo": {ID: "prop_holo", Kind: "props", File: "holo.png", Fingerprint: "fp-holo"},
	}
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Scene: "scene_rm", Characters: []string{"char_a"}},
		{ID: 2, Scene: "scene_rm", Characters: []string{"char_a"}},
		{ID: 3, Scene: "scene_rm", Characters: []string{"char_a"}},
		{ID: 4, Scene: "scene_rm", Characters: []string{"char_a"},
			SystemAssets: map[string]string{"bot": "char_bot", "panel": "prop_holo"}},
	}}
	// 渲的时候都是旧资产
	oldFP := shotAssetFP(plan.Shots[0], assets)
	oldFP4 := shotAssetFP(plan.Shots[3], assets)
	if oldFP == oldFP4 {
		t.Fatal("镜 4 多引了两张系统卡片，指纹不该与镜 1 相同 —— 说明系统卡片没被算进去")
	}
	manifest := map[string]project.ClipManifest{
		"1": {ShotID: 1, Status: project.ShotDone, AssetFP: oldFP},
		"2": {ShotID: 2, Status: project.ShotDone, AssetFP: oldFP},
		"3": {ShotID: 3, Status: project.ShotDone}, // 老清单：没有这一列
		"4": {ShotID: 4, Status: project.ShotDone, AssetFP: oldFP4},
	}
	// 全都没变 ⇒ 无 stale
	stale, unknown := StaleAssetClips(plan, manifest, assets)
	if len(stale) != 0 {
		t.Fatalf("资产没变时不该判 stale，实际 %v", stale)
	}
	if len(unknown) != 1 || unknown[0] != 3 {
		t.Fatalf("缺列的那一镜应进 unknown，实际 %v", unknown)
	}

	// 定妆照重生成（指纹变了）⇒ 前两镜判为旧资产（**这条就是"换了脸没重渲"的事故形态**）
	assets2 := map[string]project.Asset{}
	for k, v := range assets {
		assets2[k] = v
	}
	assets2["char:char_a"] = project.Asset{ID: "char_a", Kind: "characters", File: "a.png", Fingerprint: "fp-NEW-a"}
	// 定妆照重生成（指纹变了）⇒ 引用了它的镜都判旧资产（镜 4 也引用了同一个角色，所以是 [1 2 4]）
	stale, _ = StaleAssetClips(plan, manifest, assets2)
	if len(stale) != 3 || stale[0] != 1 || stale[1] != 2 || stale[2] != 4 {
		t.Fatalf("换过定妆照后引用它的镜应判旧资产（1/2/4），实际 %v", stale)
	}
	// 只改**无关**资产（本镜没引用的）不该误报
	assets3 := map[string]project.Asset{}
	for k, v := range assets {
		assets3[k] = v
	}
	assets3["char:char_b"] = project.Asset{ID: "char_b", Kind: "characters", File: "b.png", Fingerprint: "whatever"}
	if stale, _ := StaleAssetClips(plan, manifest, assets3); len(stale) != 0 {
		t.Errorf("本镜没用到的资产变化不该判 stale，实际 %v", stale)
	}
	// 系统卡片（bot/panel）的指纹变了 ⇒ 只有引用了它的镜 4 变 stale
	assets4 := map[string]project.Asset{}
	for k, v := range assets {
		assets4[k] = v
	}
	assets4["char:char_bot"] = project.Asset{ID: "char_bot", Kind: "characters", File: "bot.png", Fingerprint: "fp-bot-2"}
	if stale, _ := StaleAssetClips(plan, manifest, assets4); len(stale) != 1 || stale[0] != 4 {
		t.Errorf("机器人卡片换过之后只有镜 4 该判 stale，实际 %v", stale)
	}
}

func TestShotAssetFPSensitivity(t *testing.T) {
	assets := map[string]project.Asset{
		"char:char_a":    {ID: "char_a", Kind: "characters", Fingerprint: "1"},
		"prop:prop_x":    {ID: "prop_x", Kind: "props", Fingerprint: "2"},
		"scene:scene_rm": {ID: "scene_rm", Kind: "scenes", Fingerprint: "3"},
	}
	sh := project.Shot{ID: 1, Scene: "scene_rm", Characters: []string{"char_a"}, PropIDs: []string{"prop_x"}}
	a := shotAssetFP(sh, assets)
	// 缺一个资产 ⇒ 指纹必须变（否则"参考图挂了空"这种事故查不出来）
	sh2 := sh
	sh2.PropIDs = nil
	if b := shotAssetFP(sh2, assets); a == b {
		t.Error("缺资产时指纹应变化")
	}
	// 顺序不同 ⇒ 指纹必须相同（同一次渲染不该因为数组顺序被判成两批资产）
	assets2 := map[string]project.Asset{
		"prop:prop_x":    {ID: "prop_x", Kind: "props", Fingerprint: "2"},
		"char:char_a":    {ID: "char_a", Kind: "characters", Fingerprint: "1"},
		"scene:scene_rm": {ID: "scene_rm", Kind: "scenes", Fingerprint: "3"},
	}
	if shotAssetFP(sh, assets2) != a {
		t.Error("资产表顺序不同不该改变指纹")
	}
	if len(a) != 16 {
		t.Errorf("指纹应是 16 位十六进制，实际 %q", a)
	}
	// 没有资产的镜头返回空串（不参与核对，也不会误判）
	if got := shotAssetFP(project.Shot{ID: 9}, assets); got != "" {
		t.Errorf("没有引用资产的镜头应返回空串，实际 %q", got)
	}
}

// TestAssetFPKeysMatchProduction 键形态必须与**生产**一致，否则这道门是死的。
//
// 【这条单列的理由】2026-10-08 深度排查抓到的 P0：shotAssetFP 用裸 id 查
// `assets[id]`，而资产表的键带 `char:`/`scene:`/`prop:` 前缀 —— 每一次查表都落空，
// "新旧资产混用"这道门**从未咬到过**（夹具当年也照裸键写，掩盖了它）。
// 判据不看代码长相，只看行为：逐个把被引用资产的指纹改掉，这一列的指纹**必须**跟着变。
func TestAssetFPKeysMatchProduction(t *testing.T) {
	assets := map[string]project.Asset{
		"char:qiqi":     {ID: "qiqi", Kind: "characters", File: "qiqi.png", Fingerprint: "fp1"},
		"scene:cabin":   {ID: "cabin", Kind: "scenes", File: "cabin.png", Fingerprint: "fp2"},
		"prop:badge":    {ID: "badge", Kind: "props", File: "badge.png", Fingerprint: "fp3"},
		"char:botcard":  {ID: "botcard", Kind: "characters", File: "bot.png", Fingerprint: "fp4"},
		"prop:holocard": {ID: "holocard", Kind: "props", File: "holo.png", Fingerprint: "fp5"},
	}
	sh := project.Shot{ID: 1, Scene: "cabin", Characters: []string{"qiqi"}, PropIDs: []string{"badge"},
		SystemAssets: map[string]string{"bot": "botcard", "panel": "holocard"}}
	base := shotAssetFP(sh, assets)
	if base == "" {
		t.Fatal("引用了五个资产的镜头不该算出空指纹")
	}
	clone := func() map[string]project.Asset {
		m := make(map[string]project.Asset, len(assets))
		for k, v := range assets {
			m[k] = v
		}
		return m
	}
	for _, key := range []string{"char:qiqi", "scene:cabin", "prop:badge", "char:botcard", "prop:holocard"} {
		m := clone()
		a := m[key]
		a.Fingerprint += "-new"
		m[key] = a
		if shotAssetFP(sh, m) == base {
			t.Errorf("%s 的图换过了、这一列的指纹却没变 —— 说明查表又落空了（键形态漂回裸 id？）", key)
		}
	}
	// 带前缀的 id 与裸 id 必须算同一个资产（防双前缀，与 cast_distinct 同一纪律）。
	sh2 := sh
	sh2.Characters = []string{"char:qiqi"}
	if got := shotAssetFP(sh2, assets); got != base {
		t.Errorf("镜内 id 已带 char: 前缀时应与裸 id 等价（防双前缀），实际 %q ≠ %q", got, base)
	}
}
