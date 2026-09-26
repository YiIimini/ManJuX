package project

import (
	"os"
	"path/filepath"
	"testing"
)

// mode 空值推断的闸门(2026-09-26 真机实测暴露)。
//
// 事故形态(求你们骂我·第01集):技能端产出的 plan 里 shot **没有 mode 字段**,
// 契约把空值交给平台推断,而平台从没推断过 —— 渲染阶段只在 mode=="ref2va" 时才装配
// 参考图,于是刚生成好的 6 张定妆照一张没用上,64 镜全按纯文生跑:
//
//	✔ 资产就绪:6/6(库复用 0)
//	镜头 1/64 渲染开始(,4s,seed=…,参考图 0 张:角色 0)   ← 空 mode 的现场
//
// 同一份 mode 还被 asset_scope / stages_assets / final2k 当门用,所以推断必须在
// plan 边界做一次,而不是散着改三四处的 `!= "ref2va"`。
func TestNormalizeShotModesInfersRef2VA(t *testing.T) {
	p := &Plan{Shots: []Shot{
		{ID: 1, Mode: ""},       // 技能端没写 → 推断 ref2va
		{ID: 2, Mode: "  "},     // 空白也当没写
		{ID: 3, Mode: "t2va"},   // 写入方明确说了纯文生 → 不许改
		{ID: 4, Mode: "ref2va"}, // 已经是具体值 → 不动
	}}
	n := p.NormalizeShotModes()
	if n != 2 {
		t.Fatalf("应推断 2 镜(空 + 空白),实际 %d", n)
	}
	want := []string{"ref2va", "ref2va", "t2va", "ref2va"}
	for i, w := range want {
		if p.Shots[i].Mode != w {
			t.Errorf("镜 %d 应推断成 %q,实际 %q", p.Shots[i].ID, w, p.Shots[i].Mode)
		}
	}
	// 幂等:再跑一次不该再"推断"(否则每次读取都报变了多少镜)
	if again := p.NormalizeShotModes(); again != 0 {
		t.Errorf("推断必须幂等,第二次却报 %d 镜", again)
	}
	// nil 安全:调度器/测试里空 plan 很常见
	var nilPlan *Plan
	if n := nilPlan.NormalizeShotModes(); n != 0 {
		t.Errorf("nil plan 不该 panic 也不该报数,实际 %d", n)
	}
}

// TestEffectiveShotModeMatchesNormalize 单镜版与整 plan 版**必须同一个答案** ——
// 渲染阶段用 EffectiveShotMode,而资产/2K 门看的是 NormalizeShotModes 的结果,
// 两者分叉就会出现"资产按 ref2va 生成了、渲染却按 t2va 跑"(正是本次的形态)。
func TestEffectiveShotModeMatchesNormalize(t *testing.T) {
	for _, in := range []string{"", "   ", "ref2va", "t2va", "ref2v"} {
		sh := Shot{ID: 7, Mode: in}
		one := EffectiveShotMode(sh)
		p := &Plan{Shots: []Shot{sh}}
		p.NormalizeShotModes()
		if one != p.Shots[0].Mode {
			t.Errorf("mode=%q 时单镜版给 %q、整 plan 版给 %q —— 两处推断分叉", in, one, p.Shots[0].Mode)
		}
	}
}

// TestLoadPlanInfersModeFromDisk 从磁盘读出来的方案就已经是具体值 ——
// 存量项目(几十集已导入的)不改盘也能恢复正确行为。
func TestLoadPlanInfersModeFromDisk(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("构造 Store 失败: %v", err)
	}
	id := "某书·第01集·某集"
	if _, err := s.Create(id, ProjectConfig{}); err != nil {
		t.Fatalf("建项目失败: %v", err)
	}
	// 手写一份"技能端产出的"plan:mode 字段根本没出现(真机里的样子)
	raw := []byte(`{"shots":[{"id":1,"scene":"s1","characters":["c1"],"duration":5,"h3_prompt":"x"}]}`)
	if err := os.MkdirAll(filepath.Dir(s.PlanPath(id)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.PlanPath(id), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadPlan(id)
	if err != nil {
		t.Fatalf("LoadPlan 失败: %v", err)
	}
	if got.Shots[0].Mode != "ref2va" {
		t.Fatalf("读盘后 mode 应已推断为 ref2va(否则渲染阶段不装配参考图),实际 %q", got.Shots[0].Mode)
	}
	// 盘上的字节不该被读取动过(读取只改语义,写回是导入那一刻的事)
	if again, _ := os.ReadFile(s.PlanPath(id)); string(again) != string(raw) {
		t.Error("LoadPlan 不该把推断写回磁盘")
	}
}
