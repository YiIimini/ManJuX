package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 真机探针（2026-10-09 用户「问题好多啊」）：拿**盘上那集**的方案 + 资产表，走渲染侧同一条装配，
// 看三个内心镜的参考图到底是「Q 版」还是「本人」。
//
// 为什么必须用真机数据：夹具是我按理解写的，而这次事故恰恰是"夹具写对了、生产形态没覆盖"
// （夹具用的是 `char:chibi_char_bing` 这个生产上不存在的键）。数据不在就跳过。
func TestZZProbeRealInnerShotRefs(t *testing.T) {
	projDir := "../../data/projects/被开除一年后，我回公司送水/被开除一年后，我回公司送水·第01集·这筐纸别动"
	pb, err := os.ReadFile(filepath.Join(projDir, "plan", "plan.json"))
	if err != nil {
		t.Skipf("盘上没有这份真机方案：%v", err)
	}
	ab, err := os.ReadFile(filepath.Join(projDir, "assets", "asset_map.json"))
	if err != nil {
		t.Skipf("盘上没有真机资产表：%v", err)
	}
	var plan project.Plan
	if err := json.Unmarshal(pb, &plan); err != nil {
		t.Fatal(err)
	}
	var assets map[string]project.Asset
	if err := json.Unmarshal(ab, &assets); err != nil {
		t.Fatal(err)
	}
	// 与预检/渲染同一条读取边界：先自愈（chibi_ids 归一就在这里发生）。
	hr := project.HealPlan(&plan)
	t.Logf("HealPlan：ChibiRefs=%d Unknown=%v", hr.ChibiRefs, hr.ChibiRefUnknown)

	checked := 0
	for _, sh := range plan.Shots {
		if strings.TrimSpace(sh.VoKind) != project.VoKindInner {
			continue
		}
		checked++
		refs, roles, missing, warn := RefPlan(projDir, sh, assets, true)
		var kinds []string
		for _, r := range roles {
			kinds = append(kinds, r.Kind+":"+filepath.Base(r.Name))
		}
		t.Logf("镜 #%d chibi_ids=%v characters=%v ⇒ refs=%d roles=%v missing=%v warn=%v",
			sh.ID, sh.ChibiIDs, sh.Characters, len(refs), kinds, missing, warn)
		if len(refs) == 0 {
			t.Errorf("镜 #%d 一张参考图都没有", sh.ID)
			continue
		}
		if len(missing) > 0 || len(warn) > 0 {
			t.Errorf("镜 #%d 有缺失/告警（参考图绑得不干净）：missing=%v warn=%v", sh.ID, missing, warn)
		}
		for _, r := range refs {
			if strings.Contains(filepath.Base(r.Path), "char_jianning.png") {
				t.Errorf("镜 #%d 附了**本人**定妆照 %s —— 内心镜口径要求由 Q 版演出、本人不演这段",
					sh.ID, filepath.Base(r.Path))
			}
		}
	}
	if checked == 0 {
		t.Skip("这一集没有内心镜")
	}
}
