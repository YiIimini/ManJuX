package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// TestAssembleShotRefsOrder 顺序即契约:<Picture 1..N> = characters 数组顺序,
// <Picture N+1> = 场景。director 的 sysH3Format 按这个顺序给 LLM 编号,
// 两侧一旦漂移就会出现「LLM 以为是角色甲、实际指向角色乙」的错位。
func TestAssembleShotRefsOrder(t *testing.T) {
	shot := project.Shot{Scene: "scene_inn", Characters: []string{"char_a", "char_b"}}
	assets := map[string]project.Asset{
		"char:char_a":     {File: "assets/characters/a.png"},
		"char:char_b":     {File: "assets/characters/b.png"},
		"scene:scene_inn": {File: "assets/scenes/inn.png"},
	}
	refs, missing := AssembleShotRefs("proj", shot, assets)
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
	shot := project.Shot{Scene: "scene_inn", Characters: []string{"char_a", "char_b"}}
	assets := map[string]project.Asset{
		"char:char_b":     {File: "assets/characters/b.png"},
		"scene:scene_inn": {File: "assets/scenes/inn.png"},
	}
	refs, missing := AssembleShotRefs("proj", shot, assets)
	if len(missing) != 1 || missing[0] != "char_a" {
		t.Fatalf("应报出缺失角色 char_a,实际 %v", missing)
	}
	if len(refs) != 2 || refs[0].Kind != "character" || refs[1].Kind != "scene" {
		t.Errorf("剩余参考图应为 角色→场景,实际 %v", refs)
	}
}

// TestAssembleShotRefsSingleCharacter 单角色镜:场景落在 <Picture 2>。
func TestAssembleShotRefsSingleCharacter(t *testing.T) {
	shot := project.Shot{Scene: "s", Characters: []string{"char_a"}}
	assets := map[string]project.Asset{"char:char_a": {File: "a.png"}, "scene:s": {File: "s.png"}}
	refs, missing := AssembleShotRefs("proj", shot, assets)
	if len(refs) != 2 || len(missing) != 0 {
		t.Fatalf("单角色 + 场景应为 2 张且无缺失,实际 refs=%d missing=%v", len(refs), missing)
	}
}

// TestAssembleShotRefsEmptyFileTreatedMissing File 为空的资产等同缺失,
// 不能把空路径塞进参考图列表。
func TestAssembleShotRefsEmptyFileTreatedMissing(t *testing.T) {
	shot := project.Shot{Characters: []string{"char_a"}}
	assets := map[string]project.Asset{"char:char_a": {File: ""}}
	refs, missing := AssembleShotRefs("proj", shot, assets)
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
	shot := project.Shot{Characters: []string{"char_a"}}
	assets := map[string]project.Asset{"char:char_a": {File: "a.png"}}
	refs, missing := AssembleShotRefs("proj", shot, assets)
	if len(refs) != 1 || len(missing) != 0 {
		t.Fatalf("无场景不应报缺失,实际 refs=%d missing=%v", len(refs), missing)
	}
}
