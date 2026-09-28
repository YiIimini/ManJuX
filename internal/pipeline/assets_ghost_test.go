package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 资产表的"鬼条目"必须被剔掉：File 指向不存在的文件、或者是 0 字节的半个文件。
//
// 【2026-09-28 全面审计】资产表是渲染取参考图的**唯一来源**（refs.go 只看 File 非空，不验
// 文件在不在），留一条鬼条目，那一镜就静默降级纯文生 —— 用户看到"同一角色逐镜换人"，
// 界面上却只有"资产就绪 N/N"。而"N/N 就绪"本身是**过程计数**（生成函数返回 nil 就算数），
// 从来没跟盘上事实对过账。
func TestDropMissingAssetsJudgesByDisk(t *testing.T) {
	dir := t.TempDir()
	mk := func(rel string, body []byte) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("assets/characters/ok.png", []byte("png"))
	mk("assets/characters/empty.png", nil) // 0 字节：写到一半
	mk("assets/characters/adir.png", []byte("x"))
	if err := os.Remove(filepath.Join(dir, "assets", "characters", "adir.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets", "characters", "adir.png"), 0o755); err != nil {
		t.Fatal(err)
	}

	assets := map[string]project.Asset{
		"char:ok":     {Name: "有图的", File: "assets/characters/ok.png"},
		"char:empty":  {Name: "半张图", File: "assets/characters/empty.png"},
		"char:gone":   {Name: "被删的", File: "assets/characters/gone.png"},
		"char:isdir":  {Name: "同名目录", File: "assets/characters/adir.png"},
		"char:nofile": {Name: "还没出图的", File: ""},
	}

	ghosts := dropMissingAssets(dir, assets)
	if len(ghosts) != 3 {
		t.Fatalf("该剔掉 3 条（0 字节 / 文件不在 / 同名目录），实际 %v", ghosts)
	}
	// 按**集合**比而不是按顺序比：排序只是为了让播报稳定，不该成为判据的一部分。
	got := map[string]bool{}
	for _, g := range ghosts {
		got[g] = true
	}
	for _, w := range []string{"半张图", "同名目录", "被删的"} {
		if !got[w] {
			t.Errorf("该剔掉 %q，实际 %v", w, ghosts)
		}
	}
	if _, ok := assets["char:ok"]; !ok {
		t.Error("有图的条目不该被剔掉")
	}
	if _, ok := assets["char:nofile"]; !ok {
		t.Error("File 为空的条目代表「这张还没图」，由渲染前的补齐链负责，不该在这里删")
	}
	for _, id := range []string{"char:empty", "char:gone", "char:isdir"} {
		if _, ok := assets[id]; ok {
			t.Errorf("鬼条目 %s 应已从表里剔除（否则渲染会拿它去取参考图）", id)
		}
	}
}
