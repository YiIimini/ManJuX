package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 合成前的新旧资产混用核对（2026-09-28 全面审计）。
//
// 官方 qc-checklist 的重生成纪律："用户重做任何产物后，下游必须全用最新批准版本，且**不得在
// 最终拼接里静默混用新旧资产**"。真实事故：换了定妆照只重渲前 5 镜 ⇒ 成片里同一个人前后两张脸。
func TestStaleAssetClips(t *testing.T) {
	assets := map[string]project.Asset{
		"char_a":     {ID: "char_a", Kind: "characters", File: "a.png", Fingerprint: "fp-old-a"},
		"scene_room": {ID: "scene_room", Kind: "scenes", File: "r.png", Fingerprint: "fp-room"},
	}
	plan := &project.Plan{Shots: []project.Shot{
		{ID: 1, Scene: "scene_room", Characters: []string{"char_a"}},
		{ID: 2, Scene: "scene_room", Characters: []string{"char_a"}},
		{ID: 3, Scene: "scene_room", Characters: []string{"char_a"}},
	}}
	// 渲的时候都是旧资产
	oldFP := shotAssetFP(plan.Shots[0], assets)
	manifest := map[string]project.ClipManifest{
		"1": {ShotID: 1, Status: project.ShotDone, AssetFP: oldFP},
		"2": {ShotID: 2, Status: project.ShotDone, AssetFP: oldFP},
		"3": {ShotID: 3, Status: project.ShotDone}, // 老清单：没有这一列
	}
	// 全都没变 ⇒ 无 stale
	stale, unknown := StaleAssetClips(plan, manifest, assets)
	if len(stale) != 0 {
		t.Fatalf("资产没变时不该判 stale，实际 %v", stale)
	}
	if len(unknown) != 1 || unknown[0] != 3 {
		t.Fatalf("缺列的那一镜应进 unknown，实际 %v", unknown)
	}

	// 定妆照重生成（指纹变了）⇒ 前两镜判为旧资产
	assets2 := map[string]project.Asset{
		"char_a":     {ID: "char_a", Kind: "characters", File: "a.png", Fingerprint: "fp-NEW-a"},
		"scene_room": {ID: "scene_room", Kind: "scenes", File: "r.png", Fingerprint: "fp-room"},
	}
	stale, _ = StaleAssetClips(plan, manifest, assets2)
	if len(stale) != 2 || stale[0] != 1 || stale[1] != 2 {
		t.Fatalf("换过定妆照后前两镜应判旧资产，实际 %v", stale)
	}
	// 只改**无关**资产（本镜没引用的）不该误报
	assets3 := map[string]project.Asset{
		"char_a":     {ID: "char_a", Kind: "characters", File: "a.png", Fingerprint: "fp-old-a"},
		"scene_room": {ID: "scene_room", Kind: "scenes", File: "r.png", Fingerprint: "fp-room"},
		"char_b":     {ID: "char_b", Kind: "characters", File: "b.png", Fingerprint: "whatever"},
	}
	if stale, _ := StaleAssetClips(plan, manifest, assets3); len(stale) != 0 {
		t.Errorf("本镜没用到的资产变化不该判 stale，实际 %v", stale)
	}
}

func TestShotAssetFPSensitivity(t *testing.T) {
	assets := map[string]project.Asset{
		"char_a": {ID: "char_a", Kind: "characters", Fingerprint: "1"},
		"prop_x": {ID: "prop_x", Kind: "props", Fingerprint: "2"},
	}
	sh := project.Shot{ID: 1, Scene: "scene_room", Characters: []string{"char_a"}, PropIDs: []string{"prop_x"}}
	a := shotAssetFP(sh, assets)
	// 缺一个资产 ⇒ 指纹必须变（否则"参考图挂了空"这种事故查不出来）
	sh2 := sh
	sh2.PropIDs = nil
	if b := shotAssetFP(sh2, assets); a == b {
		t.Error("缺资产时指纹应变化")
	}
	// 顺序不同 ⇒ 指纹必须相同（同一次渲染不该因为数组顺序被判成两批资产）
	assets2 := map[string]project.Asset{
		"prop_x": {ID: "prop_x", Kind: "props", Fingerprint: "2"},
		"char_a": {ID: "char_a", Kind: "characters", Fingerprint: "1"},
	}
	if shotAssetFP(sh, assets2) != a {
		t.Error("资产表顺序不同不该改变指纹")
	}
	if !strings.HasPrefix(a, "") || len(a) != 16 {
		t.Errorf("指纹应是 16 位十六进制，实际 %q", a)
	}
	// 没有资产的镜头返回空串（不参与核对，也不会误判）
	if got := shotAssetFP(project.Shot{ID: 9}, assets); got != "" {
		t.Errorf("没有引用资产的镜头应返回空串，实际 %q", got)
	}
}
