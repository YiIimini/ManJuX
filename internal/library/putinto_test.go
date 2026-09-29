package library

// 「按 id 改条目」不许**新建**条目（2026-09-29 真机实测）。
//
// 【现场】界面上的「抽卡/重抽」走 `/api/library/{kind}/{name}/regen`，而这个 `{name}`
// 段其实是**卡片 id**（前端 `libKey(e)` = `e.id`）。处理器先用 `Match`（认 id）找到了条目，
// 却拿 URL 那一段去做 `Put` —— 于是每抽一次就在库里多一条以 id 命名的重复条目：
// 实测 7 个角色抽一轮变 **14 条**（原条目带描述/提示词/三视图板，新条目只有一张主图、
// 连 `id` 字段都没有）。之后 `Find`/复用取到哪一条取决于目录名排序 —— 本仓审计里
// 记过同一形态（「同一角色在库里变成两条，表现为随机的一致性差异」）。
//
// 判据因此是**条数不变**：按 id 或按名字更新同一个角色，库里永远只有一条。

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPutIntoKeepsSingleEntry(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	img := func(name string) string {
		p := filepath.Join(src, name)
		if err := os.WriteFile(p, tinyPNG, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	// 自动化链路建卡：按卡片 id 入库，目录按**显示名**建（见 PutCard 的口径）
	if _, err := l.PutCard("characters", "char_qiqi", "祁七", "外观二十岁", "PROMPT", img("a.png"), "p1", "qwen-image-2.1"); err != nil {
		t.Fatal(err)
	}
	dirs, _ := os.ReadDir(filepath.Join(root, "characters"))
	if len(dirs) != 1 {
		t.Fatalf("夹具应先有 1 条，实得 %d", len(dirs))
	}
	first := dirs[0].Name()

	// ① 按 **id** 更新（界面「抽卡」就是这么传的）：条数不变、还是同一个目录
	if _, err := l.PutInto("characters", "char_qiqi", "外观二十岁", "PROMPT2", img("b.png"), "p1", "qwen-image-2.1"); err != nil {
		t.Fatal(err)
	}
	dirs, _ = os.ReadDir(filepath.Join(root, "characters"))
	if len(dirs) != 1 {
		names := []string{}
		for _, d := range dirs {
			names = append(names, d.Name())
		}
		t.Fatalf("按 id 更新**新建了条目**（库里现在 %d 条：%v）—— 真机实测这么抽一轮 7 个角色变 14 条", len(dirs), names)
	}
	if dirs[0].Name() != first {
		t.Fatalf("更新后目录变了：%s → %s（目录必须取条目自己的，不许按参数重推）", first, dirs[0].Name())
	}
	// ② 按**名字**更新同一条：还是那一条
	if _, err := l.PutInto("characters", "祁七", "外观二十岁", "PROMPT3", img("c.png"), "p1", "qwen-image-2.1"); err != nil {
		t.Fatal(err)
	}
	if got := len(l.List()["characters"]); got != 1 {
		t.Fatalf("按名字更新后库里应有 1 条，实得 %d", got)
	}
	// ③ 元数据（id / 名字 / 提示词）都得留住 —— 重复条目的症状之一就是新条目没有 id
	e, _, ok := l.Match("characters", "char_qiqi")
	if !ok {
		t.Fatal("按 id 应该还能命中（新条目没有 id，命中不了就是重复条目的形态）")
	}
	if e.ID != "char_qiqi" || e.Name != "祁七" {
		t.Fatalf("更新把身份字段弄丢了：id=%q name=%q", e.ID, e.Name)
	}
	if e.ImagePrompt != "PROMPT3" {
		t.Fatalf("提示词没跟上：%q", e.ImagePrompt)
	}
}
