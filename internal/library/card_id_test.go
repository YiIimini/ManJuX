package library

import (
	"os"
	"path/filepath"
	"testing"
)

// 库的 **ID 关联**（用户 2026-09-28 定调）：「资产以元数据保存下来，然后直接以数据ID关联，
// 程序里都以数据ID调用，很简单的逻辑啊，这样就不会有什么名称问题」。
//
// 在此之前，库是按**名字**建目录、按名字命中：名字里多一个括号注解（卡片叫「碑生（丙十七）」
// 而提示词写「丙十七」）、换个称呼、繁简标点差异都会 miss —— 同一个角色被反复当新角色重出
// 定妆照；两个项目各有一个「林昭」时，后入库的还会把前者的脸永久覆盖掉。
//
// 这三条测试就是那个口径的守卫。

func mkImg(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("PNG-"+name), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// 同一张卡（ID 相同）：名字怎么变都命中同一条目，不会新建第二条。
func TestPutCardKeepsOneEntryPerCardID(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := l.PutCard("characters", "char_bingsheng", "碑生（丙十七）", "石场少年", "P", mkImg(t, src, "a.png"), "ep01", "krea2"); err != nil {
		t.Fatal(err)
	}
	// 下一集分镜把它写成简称 —— 同一张卡，不该另起一条
	if _, err := l.PutCard("characters", "char_bingsheng", "丙十七", "石场少年", "P", mkImg(t, src, "a.png"), "ep02", "krea2"); err != nil {
		t.Fatal(err)
	}

	dirs, err := os.ReadDir(filepath.Join(root, "characters"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, d := range dirs {
		if d.IsDir() {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("同一张卡只该有一条库条目（名字变了不代表换了人），实际 %d 条", n)
	}

	// 按名字怎么写都能命中：ID 命中优先，退回名字/描述也能命中
	for _, probe := range []struct{ name, desc string }{
		{"丙十七", "石场少年"},
		{"碑生（丙十七）", "石场少年"},
		{"碑生", "石场少年"},
	} {
		if _, _, ok := l.FindForCard("characters", "char_bingsheng", probe.name, probe.desc); !ok {
			t.Errorf("按 ID 必须命中（探测名 %q）", probe.name)
		}
	}
}

// 同名不同 ID（跨项目撞名）：两条条目各存各的，绝不允许后者覆盖前者的脸。
func TestPutCardSameNameDifferentIDDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := l.PutCard("characters", "char_linzhao_a", "林昭", "十八岁少女，杏眼", "P-A", mkImg(t, src, "a.png"), "projA", "krea2"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.PutCard("characters", "char_linzhao_b", "林昭", "四十岁刀客，络腮胡", "P-B", mkImg(t, src, "b.png"), "projB", "krea2"); err != nil {
		t.Fatal(err)
	}

	ea, _, ok := l.FindForCard("characters", "char_linzhao_a", "林昭", "")
	if !ok || ea == nil || ea.Description != "十八岁少女，杏眼" {
		t.Fatalf("A 的条目被 B 覆盖了（这正是「同名就换脸」那个 bug）：%+v", ea)
	}
	eb, _, ok := l.FindForCard("characters", "char_linzhao_b", "林昭", "")
	if !ok || eb == nil || eb.Description != "四十岁刀客，络腮胡" {
		t.Fatalf("B 的条目不对：%+v", eb)
	}
}

// 没有 ID 的条目（手动上传、存量）仍走名字路径 —— 不能为了 ID 化把这条路堵死。
func TestFindForCardFallsBackToNameWithoutID(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := l.Put("characters", "上传角色", "手动上传的定妆照", "P", mkImg(t, src, "c.png"), "proj", "upload"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := l.FindForCard("characters", "", "上传角色", "手动上传的定妆照"); !ok {
		t.Error("没有卡片 ID 时（上传资产）必须按名字命中，否则这条复用路径就断了")
	}
}
