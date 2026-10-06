package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 改描述 / 删除：界面传的是**卡片 id**，定位必须 id 优先（2026-09-30 深度审计）。
//
// 【现场】`SetMeta` 与 `Delete` 原来是 `locateByNameLocked`（只按名字/别名），
// 而界面两条路传的都是 `libKey(e)` = **卡片 id**：
//
//	LibAssetDialog「保存描述 / 保存提示词」→ PUT /api/library/{kind}/{id} → SetMeta
//	资产库卡片垃圾桶 / 批量删除            → DELETE /api/library/{kind}/{id} → Delete
//
// 后果有两层：
//
//	① 名字对不上 id ⇒ 「库资产不存在: char_qq」—— 而同屏的抽卡 / AI 生成描述能用
//	   （它们走 Match，本来就是 id 优先）⇒ 用户看到的是"随机失败"；
//	② **同名不同人**（本仓明确允许的数据，见 `match_card_id_test.go`）时按名字只到第一条 ⇒
//	   点第二个「老张」的垃圾桶删掉的是第一个，而第二个根本删不掉。
//
// 判据：改/删都要精确落到 id 指定的那一条；按显示名调用（存量调用方）行为不变。
func TestSetMetaAndDeleteAcceptCardID(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	// 两个**同名但不同卡**的角色 —— 真实形态（同名不同人是合法数据）
	if _, err := l.PutCard("characters", "char_laozhang_a", "老张", "修表匠", "P",
		mkImg(t, src, "a.png"), "ep01", "krea2"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.PutCard("characters", "char_laozhang_b", "老张", "快递员", "P",
		mkImg(t, src, "b.png"), "ep01", "krea2"); err != nil {
		t.Fatal(err)
	}
	dirA, err := l.EntryDir("characters", "char_laozhang_a")
	if err != nil {
		t.Fatal(err)
	}
	dirB, err := l.EntryDir("characters", "char_laozhang_b")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(dirA) == filepath.Clean(dirB) {
		t.Fatalf("夹具不成立：两张卡落到了同一个目录 %s", dirA)
	}

	// ① 按卡片 id 改描述：必须改到 id 指定的那一条，另一条一个字不动
	if _, err := l.SetMeta("characters", "char_laozhang_b", "第二条的描述", "PROMPT-B"); err != nil {
		t.Fatalf("按卡片 id 改描述必须成功（界面传的就是 id）：%v", err)
	}
	if got := readEntryDesc(t, dirB); got != "第二条的描述" {
		t.Errorf("id 指定的那一条没被改到（desc=%q）", got)
	}
	if got := readEntryDesc(t, dirA); got == "第二条的描述" {
		t.Error("改到了**第一条** —— 同名不同人时按名字定位的老毛病（界面保存描述会串到别人身上）")
	}

	// ② 按卡片 id 删除：删掉 id 指定的那一条，另一条必须还在
	if err := l.Delete("characters", "char_laozhang_b"); err != nil {
		t.Fatalf("按卡片 id 删除必须成功：%v", err)
	}
	if _, err := os.Stat(dirB); !os.IsNotExist(err) {
		t.Error("id 指定的那一条应被删掉")
	}
	if _, err := os.Stat(dirA); err != nil {
		t.Error("另一条（同名不同 id）必须留着 —— 删错人比删不掉更糟")
	}

	// ③ 按**显示名**仍然照旧能用（存量调用方不该被这次改动影响）
	if _, err := l.PutCard("characters", "char_lisi", "李四", "账房", "P",
		mkImg(t, src, "c.png"), "ep01", "krea2"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetMeta("characters", "李四", "按名字改", ""); err != nil {
		t.Fatalf("按显示名改描述不该被拒：%v", err)
	}
	if err := l.Delete("characters", "李四"); err != nil {
		t.Fatalf("按显示名删除不该被拒：%v", err)
	}
	if _, err := l.EntryDir("characters", "李四"); err == nil {
		t.Error("按显示名删除应生效")
	}
}

// readEntryDesc 读某条目目录里 asset.json 的描述字段（判据用真实落盘内容，不看返回值）。
func readEntryDesc(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "asset.json"))
	if err != nil {
		t.Fatalf("读 asset.json 失败：%v", err)
	}
	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("解析 asset.json 失败：%v", err)
	}
	return e.Description
}
