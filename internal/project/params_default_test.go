package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestImportedProjectGetsDefaultParams 技能端导入的项目(config.params 为空 {})读盘后
// 必须拿到平台默认参数 —— 否则资产阶段连引擎都不去要,整条批量在第 1 集断。
//
// 2026-09-26 实测事故链:params 空 → assets.source 空 → 资产 0/3 却报 ✔ →
// 镜头降级 t2va → 渲染阶段"无可用引擎"。契约承诺"导入后与平台自产包等价",
// 而等价的前提就是这条兜底。
func TestImportedProjectGetsDefaultParams(t *testing.T) {
	root := t.TempDir()
	id := "某书·第01集·某集"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 技能端的 project.json:只有 id/name/config,params 是空的
	raw, _ := json.Marshal(map[string]any{
		"id": id, "name": id, "config": map[string]any{"params": map[string]any{}},
	})
	if err := os.WriteFile(filepath.Join(dir, "project.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := st.Get(id)
	if !ok {
		t.Fatal("读不到刚导入的项目")
	}
	def := DefaultStageParams()
	if p.Config.Params.Assets.Source != def.Assets.Source {
		t.Errorf("空参数应补成平台默认(assets.source=%q),实际 %q",
			def.Assets.Source, p.Config.Params.Assets.Source)
	}
	if len(p.Config.Params.Script.Styles) == 0 {
		t.Error("风格预设也必须是默认值 —— 否则每镜提示词会缺风格句")
	}

	// 反向:用户配过的参数不许被默认值覆盖
	p2 := &Project{Config: ProjectConfig{Params: StageParams{
		Assets: AssetsParams{Source: "skip"},
		Script: ScriptParams{Styles: []string{"live_action"}},
	}}}
	p2.ensureDefaultParams()
	if p2.Config.Params.Assets.Source != "skip" || p2.Config.Params.Script.Styles[0] != "live_action" {
		t.Errorf("已有配置被默认值覆盖了:%+v", p2.Config.Params)
	}
}
