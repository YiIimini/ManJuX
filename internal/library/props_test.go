package library

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPropsKindSupported 道具分类(用户 2026-09-26:「资产库 新增一个 道具 管理页面」)。
//
// 钉三件事:
//
//	① 三类目录都会被建出来 —— 少建一个的后果是"存进去了,列表里看不见"(List 只遍历
//	   存在的目录),而且不报错;
//	② 主图文件名按分类区分(props/prop.png),翻库时一眼认得出这是什么;
//	③ 未知分类必须被拒 —— kind 是 URL 路径参数,放行等于让请求随便在 data 下建目录。
func TestPropsKindSupported(t *testing.T) {
	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range Kinds {
		if st, err := os.Stat(filepath.Join(root, k)); err != nil || !st.IsDir() {
			t.Errorf("New 应建出 %s 目录(kind=%v):%v", k, Kinds, err)
		}
	}

	src := filepath.Join(root, "src.png")
	if err := os.WriteFile(src, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := l.Put("props", "铜雀", "青铜铸雀,翅羽层叠", "a bronze sparrow lantern", src, "", "upload")
	if err != nil {
		t.Fatalf("道具入库失败: %v", err)
	}
	if e.Image != "props/铜雀/prop.png" {
		t.Errorf("道具主图应为 props/<名>/prop.png,实际 %q", e.Image)
	}
	if e.Kind != "props" {
		t.Errorf("条目 kind 应记 props,实际 %q", e.Kind)
	}

	all := l.List()
	// 分组必须**总是**存在(哪怕为空):前端拿 undefined 会直接崩
	for _, k := range Kinds {
		if all[k] == nil {
			t.Errorf("List 必须给出 %s 分组(空也要给空数组)", k)
		}
	}
	if len(all["props"]) != 1 || all["props"][0].Name != "铜雀" {
		t.Errorf("道具应出现在 props 分组,实际 %v", all["props"])
	}
	// 匹配走的是同一套(kind 透传)
	if _, img, ok := l.Match("props", "铜雀"); !ok || filepath.Base(img) != "prop.png" {
		t.Errorf("道具按名匹配失败:ok=%v img=%s", ok, img)
	}

	// 未知分类:目录穿越的最后一道闸
	if _, err := l.Put("stuffs", "x", "", "", src, "", ""); err == nil {
		t.Error("未知 kind 必须被拒绝")
	}
	if _, err := l.EntryDir("../../etc", "x"); err == nil {
		t.Error("未知 kind(含路径穿越)必须被拒绝")
	}
}
