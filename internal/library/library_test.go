package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestMatchSkipsCorruptedEntry 损坏的 asset.json 必须被安全跳过(按未命中处理),
// 既不能 panic,也不能把半截数据当有效条目。这条路径此前完全静默 ——
// 表现只是「角色形象莫名漂移」,无从排查。
func TestMatchSkipsCorruptedEntry(t *testing.T) {
	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "characters", "坏条目")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asset.json"), []byte(`{"kind":"characters",`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := l.Match("characters", "坏条目"); ok {
		t.Fatal("损坏条目不应命中")
	}
}

// TestMatchSkipsEntryWithoutImage 条目 JSON 正常但图片缺失时同样按未命中处理,
// 不能让调用方拿到一个指向不存在文件的路径。
func TestMatchSkipsEntryWithoutImage(t *testing.T) {
	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "scenes", "缺图场景")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	e := Entry{Kind: "scenes", Name: "缺图场景", Image: "scenes/缺图场景/portrait.png"}
	raw, _ := json.Marshal(&e)
	if err := os.WriteFile(filepath.Join(dir, "asset.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := l.Match("scenes", "缺图场景"); ok {
		t.Fatal("缺图条目不应急命中")
	}
}

// TestPutWritesValidEntryAndNoTmpLeft Put 之后条目必须可完整读回,
// 且原子写不得残留 .tmp 文件(残留意味着 rename 未完成)。
func TestPutWritesValidEntryAndNoTmpLeft(t *testing.T) {
	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "src.png")
	if err := os.WriteFile(src, []byte("fake-png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put("characters", "林凡", "描述", "prompt", src, "proj", "upload"); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	entry, img, ok := l.Match("characters", "林凡")
	if !ok {
		t.Fatal("Put 后应能命中")
	}
	if entry.Name != "林凡" || img == "" {
		t.Fatalf("条目内容异常: %+v", entry)
	}
	raw, err := os.ReadFile(filepath.Join(root, "characters", "林凡", "asset.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed Entry
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("asset.json 应为合法 JSON: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "characters", "林凡", "asset.json.tmp")); !os.IsNotExist(err) {
		t.Error("原子写不应残留 .tmp 文件")
	}
}

// TestPutCollectsRelatedImagesAndClearRelated 多视角原图/转台图的「收集 + 清理」契约。
//
// 回归 2026-09-12:资产阶段曾把抽帧写进 <base>_views/ 子目录,而 relatedImages /
// ClearRelated 用的是**非递归** glob <base>_view_*.png —— 两处都用同一个 glob,
// 于是多视角原图既收不进库(详情弹窗只有定妆照),重抽后也清不掉(仍是上一张脸
// 的过期角度图)。本测试从「生产者按契约放在主图同目录」这一侧钉住两者成对生效。
//
// 文件名里的 tag 必须与前端 LibraryView.labelOf() 认识的标签
// (threequarter / side / back / turnaround)逐字一致,否则库里有图、标签却只会
// 退化显示成笼统的「多视角」。
func TestPutCollectsRelatedImagesAndClearRelated(t *testing.T) {
	src := t.TempDir()
	master := filepath.Join(src, "characters_char_c1.png")
	for _, f := range []string{
		master,
		filepath.Join(src, "characters_char_c1_view_threequarter.png"),
		filepath.Join(src, "characters_char_c1_view_side.png"),
		filepath.Join(src, "characters_char_c1_view_back.png"),
		filepath.Join(src, "characters_char_c1_turnaround.png"),
	} {
		if err := os.WriteFile(f, []byte("png:"+filepath.Base(f)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 子目录里的抽帧(旧实现的位置)不应被收进来 —— glob 非递归是刻意的契约:
	// 多视角抽帧必须与定妆照同目录,否则收不进库也清不掉。
	sub := filepath.Join(src, "characters_char_c1_views")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "view_0.png"), []byte("old-layout"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	e, err := l.Put("characters", "契约角色", "描述", "prompt", master, "proj", "krea2")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}

	// 期望:主图 + 3 张多视角原图 + 转台图,一张都不能少(子目录帧不计入)。
	// 主图在库内统一叫 portrait.png,关联图保留生成时的基名(前端 labelOf 靠
	// 基名里的 _view_<tag>/_turnaround 判用途,所以基名必须原样保留)。
	wantSorted := []string{
		"characters/契约角色/characters_char_c1_turnaround.png",
		"characters/契约角色/characters_char_c1_view_back.png",
		"characters/契约角色/characters_char_c1_view_side.png",
		"characters/契约角色/characters_char_c1_view_threequarter.png",
		"characters/契约角色/portrait.png",
	}
	got := append([]string(nil), e.Images...)
	sort.Strings(got)
	if len(got) != len(wantSorted) {
		t.Fatalf("关联图数量不符: got %v want %v", got, wantSorted)
	}
	for i := range wantSorted {
		if got[i] != wantSorted[i] {
			t.Fatalf("关联图清单不符: got %v want %v", got, wantSorted)
		}
	}
	if e.Image != "characters/契约角色/portrait.png" {
		t.Fatalf("主图路径异常: %s", e.Image)
	}
	// 每张都要真的落盘且非空(否则详情弹窗拿到的是死链接)
	for _, rel := range got {
		st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("关联图未落盘 %s: %v", rel, err)
		}
		if st.Size() == 0 {
			t.Fatalf("关联图为 0 字节: %s", rel)
		}
	}
	// Images 必须持久化(重开库时详情弹窗仍要能看到全部图片)
	raw, err := os.ReadFile(filepath.Join(root, "characters", "契约角色", "asset.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed Entry
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("asset.json 应为合法 JSON: %v", err)
	}
	if len(parsed.Images) != len(wantSorted) {
		t.Fatalf("asset.json 里 Images 数量不符: got %v want %v", parsed.Images, wantSorted)
	}

	// 重抽路径:ClearRelated 必须把 4 张关联图全清掉,主图与条目本身留下
	l.ClearRelated("characters", "契约角色")
	cleared := 0
	for _, rel := range got {
		_, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if rel == e.Image {
			if statErr != nil {
				t.Errorf("ClearRelated 误删了主图: %v", statErr)
			}
			continue
		}
		cleared++
		if !os.IsNotExist(statErr) {
			t.Errorf("ClearRelated 未清掉 %s", rel)
		}
	}
	if cleared != 4 {
		t.Errorf("应清掉 4 张关联图,实际 %d 张", cleared)
	}
	if _, err := os.Stat(filepath.Join(root, "characters", "契约角色", "asset.json")); err != nil {
		t.Errorf("ClearRelated 误删了 asset.json: %v", err)
	}
}
