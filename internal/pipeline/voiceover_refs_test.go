package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// voiceover_refs_test.go 画外演出镜的**参考图装配**（技能侧 2026-09-27 口径 · 用户定调）：
//
//	内心活动 = 由该角色的 **Q 版形象**演出，本人闭嘴、不演这段；
//	系统     = 科幻悬浮**全息面板**（道具）＋ **Q 版小机器人**（人物）同框，人闭嘴、不念系统台词。
//
// 参考图按这个口径走，所以"字段对不对"直接决定"画面里出现的是谁"：
// 内心镜如果照旧附本人那张正面照，模型会把**本人和 Q 版都画进去**（口径要的是只有 Q 版）。

func writePNGs(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		writeTestPNG(t, filepath.Join(dir, n), 64, 64)
	}
}

func TestRefsInnerShotUsesChibiNotThePerson(t *testing.T) {
	dir := t.TempDir()
	writePNGs(t, dir, "p01.png", "p01_chibi.png", "scene01.png")
	assets := map[string]project.Asset{
		"char:char_01":       {ID: "char:char_01", Kind: "characters", Name: "沈砚", File: "p01.png"},
		"char:chibi_char_01": {ID: "char:chibi_char_01", Kind: "characters", Name: "沈砚·Q版", File: "p01_chibi.png"},
		"scene:scene_01":     {ID: "scene:scene_01", Kind: "scenes", Name: "临水城", File: "scene01.png"},
	}
	shot := project.Shot{
		ID: 1, Scene: "scene_01", ShotSize: "中景", Characters: []string{"char_01"},
		VoKind: project.VoKindInner, ChibiIDs: []string{"chibi_char_01"},
	}
	refs, roles, missing, _ := RefPlan(dir, shot, assets, false)
	if len(missing) != 0 {
		t.Fatalf("不该有缺失：%v", missing)
	}
	if len(refs) != 2 {
		t.Fatalf("内心镜应装配 Q版 + 场景 两张，实际 %d 张（附上本人那张会把两个人都画进画面）", len(refs))
	}
	if roles[0].Kind != "character_chibi" || roles[0].ID != "chibi_char_01" {
		t.Errorf("第一张应是 Q 版形象，实际 %+v", roles[0])
	}
	if filepath.Base(refs[0].Path) != "p01_chibi.png" {
		t.Errorf("第一张应是 Q 版那张图，实际 %s", refs[0].Path)
	}
	for _, r := range roles {
		if r.Kind == "character_front" {
			t.Error("内心镜不该再附本人的正面照（口径：本人不演这段）")
		}
	}
	// 正文清单要说清"这是同一个人的 Q 版、本人不在画面里"
	mf := RefManifest(shot, roles)
	for _, want := range []string{"CHIBI", "SAME character", "NOT in this frame"} {
		if !strings.Contains(mf, want) {
			t.Errorf("权威清单缺 %q：\n%s", want, mf)
		}
	}
}

func TestRefsSystemShotAddsBotAndPanel(t *testing.T) {
	dir := t.TempDir()
	writePNGs(t, dir, "p01.png", "sysbot.png", "holo.png", "scene01.png")
	assets := map[string]project.Asset{
		"char:char_01":      {ID: "char:char_01", Kind: "characters", Name: "沈砚", File: "p01.png"},
		"char:chibi_sys_01": {ID: "char:chibi_sys_01", Kind: "characters", Name: "系统小机器人", File: "sysbot.png"},
		"prop:prop_holo_01": {ID: "prop:prop_holo_01", Kind: "props", Name: "全息面板", File: "holo.png"},
		"scene:scene_01":    {ID: "scene:scene_01", Kind: "scenes", Name: "临水城", File: "scene01.png"},
	}
	shot := project.Shot{
		ID: 2, Scene: "scene_01", ShotSize: "中景", Characters: []string{"char_01"},
		VoKind: project.VoKindSystem, PropIDs: []string{"prop_holo_01"},
		SystemAssets: map[string]string{"bot": "chibi_sys_01", "panel": "prop_holo_01"},
	}
	refs, roles, missing, _ := RefPlan(dir, shot, assets, false)
	if len(missing) != 0 {
		t.Fatalf("不该有缺失：%v", missing)
	}
	// 本人（若在场）+ 机器人 + 面板 + 场景
	if len(refs) != 4 {
		t.Fatalf("系统镜应装配 4 张（本人/机器人/面板/场景），实际 %d 张：%+v", len(refs), roles)
	}
	kinds := []string{}
	for _, r := range roles {
		kinds = append(kinds, r.Kind)
	}
	joined := strings.Join(kinds, ",")
	for _, want := range []string{"system_bot", "system_panel", "character_front", "scene"} {
		if !strings.Contains(joined, want) {
			t.Errorf("参考图角色里缺 %q（实际 %s）", want, joined)
		}
	}
	mf := RefManifest(shot, roles)
	for _, want := range []string{"CHIBI ROBOT", "HOLOGRAPHIC PANEL", "BLANK", "MACHINE"} {
		if !strings.Contains(mf, want) {
			t.Errorf("权威清单缺 %q（面板不许画字、机器人是机器不是人，都要说清）：\n%s", want, mf)
		}
	}
}

// TestRefsInnerFallsBackWhenChibiMissing Q 版卡还没出图时**不许静默丢掉身份参考**。
//
// 口径是"本人不演这段"，但"没有 Q 版图"时若照旧跳过本人那张，这一镜就只剩场景 ——
// 模型会凭文字自己编一张脸（比"出了本人"更糟）。所以兜底是：拿不到 Q 版就用本人照。
func TestRefsInnerFallsBackWhenChibiMissing(t *testing.T) {
	dir := t.TempDir()
	writePNGs(t, dir, "p01.png", "scene01.png")
	assets := map[string]project.Asset{
		"char:char_01":   {ID: "char:char_01", Kind: "characters", Name: "沈砚", File: "p01.png"},
		"scene:scene_01": {ID: "scene:scene_01", Kind: "scenes", Name: "临水城", File: "scene01.png"},
	}
	shot := project.Shot{
		ID: 3, Scene: "scene_01", ShotSize: "中景", Characters: []string{"char_01"},
		VoKind: project.VoKindInner, ChibiIDs: []string{"chibi_char_01"}, // 卡在方案里，但图还没出
	}
	_, roles, _, _ := RefPlan(dir, shot, assets, false)
	found := false
	for _, r := range roles {
		if r.Kind == "character_front" {
			found = true
		}
	}
	if !found {
		t.Error("Q 版图拿不到时要退回本人的正面照（否则这一镜没有任何身份参考）")
	}
}

// TestPlainShotUnaffected 普通镜一个字节都不该变（口径只作用于 inner/system）。
func TestPlainShotUnaffected(t *testing.T) {
	dir := t.TempDir()
	writePNGs(t, dir, "p01.png", "scene01.png")
	assets := map[string]project.Asset{
		"char:char_01":   {ID: "char:char_01", Kind: "characters", Name: "沈砚", File: "p01.png"},
		"scene:scene_01": {ID: "scene:scene_01", Kind: "scenes", Name: "临水城", File: "scene01.png"},
	}
	shot := project.Shot{ID: 4, Scene: "scene_01", ShotSize: "中景", Characters: []string{"char_01"}}
	refs, roles, _, _ := RefPlan(dir, shot, assets, false)
	if len(refs) != 2 || roles[0].Kind != "character_front" || roles[1].Kind != "scene" {
		t.Errorf("普通镜应仍为 本人照 + 场景，实际 %+v", roles)
	}
	_ = os.Stdout
}
