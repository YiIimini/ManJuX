package pipeline

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/library"
)

// 跨项目同名资产的**来源与覆盖**留痕（2026-09-28 全面审计）。
//
// 病根：库按 kind+name **精确匹配**，Put 又是**同名覆盖** —— 两个项目各有一个「林昭」
// （网文里撞名很常见）时：B 一生成就把 A 的条目换成 B 的脸，A 此后的重渲拿到 B 的脸，
// 而两边界面都看不出异常。这里测两件事：① 覆盖要留痕；② 复用要能说出"来自哪部戏"。
func TestLibraryCrossProjectOverwriteLeavesTrace(t *testing.T) {
	dir := t.TempDir()
	lib, err := library.New(dir)
	if err != nil {
		t.Fatalf("开库失败:%v", err)
	}
	// 造两张真 PNG（Put 会校验源图非空）
	srcA := filepath.Join(t.TempDir(), "a.png")
	srcB := filepath.Join(t.TempDir(), "b.png")
	writeTestPNG(t, srcA, 768, 1344)
	writeTestPNG(t, srcB, 768, 1344)

	// A 项目先入库
	e1, err := lib.Put("characters", "林昭", "十八岁少女，杏眼，齐肩短发，素色短打", "P-A", srcA, "projA", "krea2")
	if err != nil {
		t.Fatalf("A 入库失败:%v", err)
	}
	if e1.OverwriteNote != "" || len(e1.OverwroteProjects) != 0 {
		t.Errorf("首次入库不该有覆盖记录，实际 note=%q over=%v", e1.OverwriteNote, e1.OverwroteProjects)
	}

	// B 项目生成同名角色 ⇒ 覆盖
	e2, err := lib.Put("characters", "林昭", "四十岁刀客，络腮胡，旧皮甲", "P-B", srcB, "projB", "krea2")
	if err != nil {
		t.Fatalf("B 入库失败:%v", err)
	}
	if e2.OverwriteNote == "" {
		t.Fatal("跨项目同名覆盖必须留下可读说明（否则那部戏无声无息换了脸）")
	}
	for _, want := range []string{"projA", "projB"} {
		if !strings.Contains(e1.OverwriteNote+e2.OverwriteNote, want) {
			t.Errorf("覆盖说明里应出现 %s，实际 %q", want, e2.OverwriteNote)
		}
	}
	if len(e2.OverwroteProjects) != 1 || e2.OverwroteProjects[0] != "projA" {
		t.Errorf("被覆盖的项目名要记下来，实际 %v", e2.OverwroteProjects)
	}

	// 同项目再入库（正常更新）不该报"被别的项目覆盖"
	e3, err := lib.Put("characters", "林昭", "四十岁刀客，络腮胡，旧皮甲（重出）", "P-B2", srcB, "projB", "krea2")
	if err != nil {
		t.Fatalf("B 再次入库失败:%v", err)
	}
	if e3.OverwriteNote != "" {
		t.Errorf("同项目更新不该报跨项目覆盖，实际 %q", e3.OverwriteNote)
	}
	// 但历史覆盖记录要保留（后来的人要能查出来"这张脸换过主人"）
	if len(e3.OverwroteProjects) != 1 || e3.OverwroteProjects[0] != "projA" {
		t.Errorf("历史覆盖记录应保留，实际 %v", e3.OverwroteProjects)
	}
}

func TestFirstSentence(t *testing.T) {
	cases := map[string]string{
		"":    "(这条资产没有描述)",
		"   ": "(这条资产没有描述)",
		"十八岁少女，杏眼。后面还有别的": "十八岁少女，杏眼",
		"一句话没有标点":         "一句话没有标点",
	}
	for in, want := range cases {
		if got := firstSentence(in); got != want {
			t.Errorf("firstSentence(%q) = %q，期望 %q", in, got, want)
		}
	}
	// 超长要截断（日志里不能淹没重点）
	long := ""
	for i := 0; i < 30; i++ {
		long += "很长很长的描述"
	}
	if got := firstSentence(long); len([]rune(got)) > 41 {
		t.Errorf("超长描述应截断到 40 字加省略号，实际 %d 字", len([]rune(got)))
	}
}
