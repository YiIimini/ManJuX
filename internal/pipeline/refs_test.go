package pipeline

import (
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/project"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkAssetsOnDisk 把夹具里声明的资产**真的写到盘上**。
//
// 【为什么必须这样写（2026-09-28）】参考图判据现在是"盘上真的有这份文件"
// （`project.FileOnDisk`），而不是"File 字段非空"—— 字段写着有、盘上没有的条目，用在这里
// 就是"参考图没附上、静默降级纯文生"。夹具照真实形态写，判据测的才是真行为；
// 之前这些夹具传的是假目录 `"proj"`、文件也不存在，等于把判据测成了"字段非空"。
func mkAssetsOnDisk(t *testing.T, dir string, assets map[string]project.Asset) {
	t.Helper()
	for _, a := range assets {
		if strings.TrimSpace(a.File) == "" {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(a.File))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestAssembleShotRefsOrder 顺序即契约:<Picture 1..N> = characters 数组顺序,
// <Picture N+1> = 场景。director 的 sysH3Format 按这个顺序给 LLM 编号,
// 两侧一旦漂移就会出现「LLM 以为是角色甲、实际指向角色乙」的错位。
func TestAssembleShotRefsOrder(t *testing.T) {
	dir := t.TempDir()
	shot := project.Shot{Scene: "scene_inn", Characters: []string{"char_a", "char_b"}}
	assets := map[string]project.Asset{
		"char:char_a":     {File: "assets/characters/a.png"},
		"char:char_b":     {File: "assets/characters/b.png"},
		"scene:scene_inn": {File: "assets/scenes/inn.png"},
	}
	mkAssetsOnDisk(t, dir, assets)
	refs, missing := AssembleShotRefs(dir, shot, assets, true)
	if len(missing) != 0 {
		t.Fatalf("不应有缺失,实际 %v", missing)
	}
	if len(refs) != 3 {
		t.Fatalf("2 角色 + 1 场景应为 3 张,实际 %d", len(refs))
	}
	if refs[0].Kind != "character" || refs[1].Kind != "character" || refs[2].Kind != "scene" {
		t.Errorf("顺序应为 角色→角色→场景,实际 %s→%s→%s", refs[0].Kind, refs[1].Kind, refs[2].Kind)
	}
}

// TestAssembleShotRefsMissingCharacter 角色缺定妆照必须报 missing:
// 缺首角色时剩下的 <Picture 1> 实际是第二个角色,而提示词仍按 characters
// 顺序描述,必然指错图 —— 这正是调用方必须告警而非静默降级的原因。
func TestAssembleShotRefsMissingCharacter(t *testing.T) {
	dir := t.TempDir()
	shot := project.Shot{Scene: "scene_inn", Characters: []string{"char_a", "char_b"}}
	assets := map[string]project.Asset{
		"char:char_b":     {File: "assets/characters/b.png"},
		"scene:scene_inn": {File: "assets/scenes/inn.png"},
	}
	mkAssetsOnDisk(t, dir, assets)
	refs, missing := AssembleShotRefs(dir, shot, assets, true)
	if len(missing) != 1 || missing[0] != "char_a" {
		t.Fatalf("应报出缺失角色 char_a,实际 %v", missing)
	}
	if len(refs) != 2 || refs[0].Kind != "character" || refs[1].Kind != "scene" {
		t.Errorf("剩余参考图应为 角色→场景,实际 %v", refs)
	}
}

// TestAssembleShotRefsSingleCharacter 单角色镜:场景落在 <Picture 2>。
func TestAssembleShotRefsSingleCharacter(t *testing.T) {
	dir := t.TempDir()
	shot := project.Shot{Scene: "s", Characters: []string{"char_a"}}
	assets := map[string]project.Asset{"char:char_a": {File: "a.png"}, "scene:s": {File: "s.png"}}
	mkAssetsOnDisk(t, dir, assets)
	refs, missing := AssembleShotRefs(dir, shot, assets, true)
	if len(refs) != 2 || len(missing) != 0 {
		t.Fatalf("单角色 + 场景应为 2 张且无缺失,实际 refs=%d missing=%v", len(refs), missing)
	}
}

// TestAssembleShotRefsEmptyFileTreatedMissing File 为空的资产等同缺失,
// 不能把空路径塞进参考图列表。
func TestAssembleShotRefsEmptyFileTreatedMissing(t *testing.T) {
	dir := t.TempDir()
	shot := project.Shot{Characters: []string{"char_a"}}
	assets := map[string]project.Asset{"char:char_a": {File: ""}}
	refs, missing := AssembleShotRefs(dir, shot, assets, true)
	if len(refs) != 0 || len(missing) != 1 {
		t.Fatalf("空 File 应视为缺失,实际 refs=%d missing=%v", len(refs), missing)
	}
}

// TestTurnaroundPath 多视角转台图的引用判据:
// 只有在同目录存在 <base>_turnaround.png 时才追加 —— 没有转台图的存量项目
// 参考图组成必须完全不变(否则渲染指纹会变,导致全量重渲)。
func TestTurnaroundPath(t *testing.T) {
	dir := t.TempDir()
	rel := "assets/characters/char_x.png"

	// ① 无转台图 → 空串(老项目行为不变)
	if got := turnaroundPath(dir, rel); got != "" {
		t.Errorf("没有转台图时应返回空串,实际 %q", got)
	}

	// ② 有转台图 → 返回同目录同主名的 _turnaround
	charDir := filepath.Join(dir, "assets", "characters")
	if err := os.MkdirAll(charDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(charDir, "char_x")
	for _, f := range []string{base + ".png", base + "_turnaround.png"} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := base + "_turnaround.png"
	if got := turnaroundPath(dir, rel); got != want {
		t.Errorf("期望 %q,实际 %q", want, got)
	}

	// ③ 空路径不得 panic
	if got := turnaroundPath(dir, ""); got != "" {
		t.Errorf("空路径应返回空串,实际 %q", got)
	}
}

// TestAssembleShotRefsNoScene 没有场景的镜头不应被误报缺失。
func TestAssembleShotRefsNoScene(t *testing.T) {
	dir := t.TempDir()
	shot := project.Shot{Characters: []string{"char_a"}}
	assets := map[string]project.Asset{"char:char_a": {File: "a.png"}}
	mkAssetsOnDisk(t, dir, assets)
	refs, missing := AssembleShotRefs(dir, shot, assets, true)
	if len(refs) != 1 || len(missing) != 0 {
		t.Fatalf("无场景不应报缺失,实际 refs=%d missing=%v", len(refs), missing)
	}
}

// TestAssembleShotRefsCompactDropsRedundantPortrait 参考图去冗余契约。
//
// 回归 2026-09-12(用户「合理分配资源」):转台图是把「正面定妆照 + 三个转身角度」
// 横向拼成的一张图,左起第一格**就是**定妆照本身。旧实现把定妆照与转台图两张都喂,
// 一半信息重复、每个角色的参考图却翻倍 —— 多模态上下文变长,每步注意力更贵、显存与
// 内存占用更高。新行为:有转台图就只喂转台图;没有才退回单张定妆照;显式关闭
// (compact=false)时逐字复现旧行为,用于需要复现历史指纹的场景。
func TestAssembleShotRefsCompactDropsRedundantPortrait(t *testing.T) {
	dir := t.TempDir()
	// 角色甲:有转台图;角色乙:只有定妆照
	for _, f := range []string{
		"a.png", "a_turnaround.png", "b.png", "scene.png",
	} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	shot := project.Shot{Scene: "s", Characters: []string{"a", "b"}}
	assets := map[string]project.Asset{
		"char:a": {File: "a.png"}, "char:b": {File: "b.png"}, "scene:s": {File: "scene.png"},
	}

	compact, missing := AssembleShotRefs(dir, shot, assets, true)
	if len(missing) != 0 {
		t.Fatalf("不应缺失:%v", missing)
	}
	if len(compact) != 3 {
		t.Fatalf("去冗余后应 2 角色 + 1 场景 = 3 张,实际 %d:%v", len(compact), compact)
	}
	if filepath.Base(compact[0].Path) != "a_turnaround.png" {
		t.Errorf("有转台图的角色应只喂转台图(含正面),实际 %s", filepath.Base(compact[0].Path))
	}
	if filepath.Base(compact[1].Path) != "b.png" {
		t.Errorf("无转台图的角色应喂定妆照,实际 %s", filepath.Base(compact[1].Path))
	}
	if compact[2].Kind != "scene" {
		t.Errorf("场景必须最后,实际 %s", compact[2].Kind)
	}

	legacy, _ := AssembleShotRefs(dir, shot, assets, false)
	if len(legacy) != 4 {
		t.Fatalf("关闭去冗余应逐字复现旧行为(4 张),实际 %d:%v", len(legacy), legacy)
	}
	if filepath.Base(legacy[0].Path) != "a.png" || filepath.Base(legacy[1].Path) != "a_turnaround.png" {
		t.Errorf("旧行为应为 定妆照→转台图,实际 %s→%s",
			filepath.Base(legacy[0].Path), filepath.Base(legacy[1].Path))
	}
}

// TestCompactRefsDefaultOff 未表态(nil)时按**关闭**处理。
//
// 回归 2026-09-12:曾把去冗余设成默认开,结果渲染出"服装对、脸变了"的另一个人 ——
// 转台图是 4 格拼合图,模型缩放入口时每格只剩约 1/4 像素,锐利的脸被摊薄。默认必须
// 两张都喂(定妆照锁脸 + 转台图补角度),去冗余只在显式开启时生效。
func TestCompactRefsDefaultOff(t *testing.T) {
	var a project.AssetsParams
	if a.CompactRefsOn() {
		t.Error("未表态应视为关闭参考图去冗余(默认两张都喂)")
	}
	on := true
	a.CompactRefs = &on
	if !a.CompactRefsOn() {
		t.Error("显式开启时应视为开启")
	}
	if project.DefaultStageParams().Assets.CompactRefsOn() {
		t.Error("新建项目默认不应开启去冗余")
	}
}

// TestCapRefsToLimit H3 公开契约的参考图上限（1 首帧 + 1 尾帧 + 9 参考图）。
//
// 出处：drama-skills(MIT) 的 skills/short-drama-video-prompts/references/minimax-h3.md。
// 一镜里角色多 + 场景板 + 拼合图时会超；超额交给后端等于把"编号前移、模型对错号"交给运气。
func TestCapRefsToLimit(t *testing.T) {
	mk := func(n int) ([]engine.RefImage, []RefRole) {
		var refs []engine.RefImage
		var roles []RefRole
		for i := 1; i <= n; i++ {
			refs = append(refs, engine.RefImage{Kind: "characters", Path: "p" + itoa(i)})
			roles = append(roles, RefRole{Picture: i, Kind: "character_front", Name: "角色" + itoa(i), ID: "char_" + itoa(i)})
		}
		return refs, roles
	}
	// 没超：原样返回、不播报
	refs, roles := mk(9)
	if got, gr, note := capRefsToLimit(refs, roles); note != "" || len(got) != 9 || len(gr) != 9 {
		t.Fatalf("9 张不该被裁，实际 %d 张 note=%q", len(got), note)
	}
	// 超了：裁到 9、从尾部裁、并说清裁了谁
	refs, roles = mk(12)
	got, gr, note := capRefsToLimit(refs, roles)
	if len(got) != MaxH3RefImages || len(gr) != MaxH3RefImages {
		t.Fatalf("应裁到 %d 张，实际 %d", MaxH3RefImages, len(got))
	}
	if note == "" || !strings.Contains(note, "角色12") {
		t.Errorf("要如实说出裁掉了哪些，实际：%s", note)
	}
	if gr[0].Name != "角色1" {
		t.Errorf("身份优先：第一个角色的定妆照必须留下，实际 %s", gr[0].Name)
	}
}
