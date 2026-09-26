package server

import (
	"os"
	"path/filepath"
	"testing"
)

// 导入脚本时封面必须来自**小说自己的封面**(2026-09-26 用户:
// 「脚本制作视频使用的封面也要是小说对应的封面才对啊,为什么现在是生成的角色作为封面?」)。
//
// 根因:projectCover 的兜底是 自定义封面 > 首个角色定妆照 > 场景图,而「脚本创作 / 项目包导入」
// 两条入口没有把小说封面复制进项目(只有 LLM 拆集那条路做了),于是导入出来的集全掉到
// "角色定妆照"兜底上 —— 封面成了制作中间物。
func TestImportAppliesNovelCover(t *testing.T) {
	s := newTestServer(t)
	root := s.novelRoot()
	book := filepath.Join(root, "书D")
	// 小说库约定:书目录/封面/封面.png
	if err := os.MkdirAll(filepath.Join(book, "封面"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(book, "封面", "封面.png"), []byte("cover-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 技能端的包放在 <书>/脚本/<集包>
	mkPackage(t, book, filepath.Join("脚本", "书D·第01集·开局"), "书D", "第01集")

	if code, out := doReq(t, s, "POST", "/api/projects/import-package",
		map[string]any{"path": filepath.Join(book, "脚本")}); code != 200 {
		t.Fatalf("导入应 200,实际 %d(%v)", code, out)
	}
	ids := listIDs(t, s)
	if len(ids) != 1 {
		t.Fatalf("应导入 1 集,实际 %v", ids)
	}
	got := filepath.Join(s.store.Dir(ids[0]), "assets", "cover.png")
	if b, err := os.ReadFile(got); err != nil {
		t.Fatalf("项目里应写入小说封面 assets/cover.png:%v", err)
	} else if string(b) != "cover-bytes" {
		t.Errorf("封面内容应逐字节来自小说封面,实际 %q", string(b))
	}
	if cv := s.projectCover(ids[0]); cv != "assets/cover.png" {
		t.Errorf("项目封面应指向小说封面(否则仍会退回角色定妆照),实际 %q", cv)
	}
}

// TestImportedCoverDoesNotOverwriteCustom 用户手动换过的封面不许被书封面顶掉 ——
// "我明明换过封面,它又变回去了"是最难查的一类。
func TestImportedCoverDoesNotOverwriteCustom(t *testing.T) {
	s := newTestServer(t)
	root := s.novelRoot()
	book := filepath.Join(root, "书E")
	_ = os.MkdirAll(filepath.Join(book, "封面"), 0o755)
	_ = os.WriteFile(filepath.Join(book, "封面", "封面.png"), []byte("book-cover"), 0o644)
	mkPackage(t, book, "脚本", "书E", "第01集")

	if code, _ := doReq(t, s, "POST", "/api/projects/import-package",
		map[string]any{"path": filepath.Join(book, "脚本")}); code != 200 {
		t.Fatal("导入失败")
	}
	ids := listIDs(t, s)
	// 用户自定义封面(上传路径写的就是 assets/cover.<ext>)
	custom := filepath.Join(s.store.Dir(ids[0]), "assets", "cover.jpg")
	if err := os.WriteFile(custom, []byte("my-cover"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 再导入一次(等价于重新点一次入口)
	if code, _ := doReq(t, s, "POST", "/api/projects/import-package",
		map[string]any{"path": filepath.Join(book, "脚本")}); code != 200 {
		t.Fatal("重复导入失败")
	}
	if b, _ := os.ReadFile(custom); string(b) != "my-cover" {
		t.Error("用户自定义封面被书封面覆盖了")
	}
}

// TestCoverInDirPicksFirstImage 封面取 `<书>/封面/` 里名字序第一张,非图片一律不算。
func TestCoverInDirPicksFirstImage(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "封面"), 0o755)
	for name, body := range map[string]string{"b.png": "b", "a.jpg": "a", "note.txt": "t", ".hidden.png": "h"} {
		_ = os.WriteFile(filepath.Join(dir, "封面", name), []byte(body), 0o644)
	}
	got := coverInDir(dir)
	if filepath.Base(got) != "a.jpg" {
		t.Errorf("应取名字序第一张图片 a.jpg,实际 %q", filepath.Base(got))
	}
	if coverInDir(t.TempDir()) != "" {
		t.Error("没有封面目录时应返回空")
	}
}
