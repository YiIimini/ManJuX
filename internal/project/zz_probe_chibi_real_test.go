package project

import (
	"fmt"
	"os"
	"testing"
)

// 真机探针（2026-10-09 用户「问题好多啊」）：读**盘上那份方案**，看 `chibi_ids` 归一是否生效、
// 门是否不再报悬空。数据不存在时跳过（别的机器/别的机器上没有这集）。
func TestZZProbeRealChibiNormalize(t *testing.T) {
	rel := "../../data/projects/被开除一年后，我回公司送水/被开除一年后，我回公司送水·第01集·这筐纸别动/plan/plan.json"
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Skipf("盘上没有这份真机方案：%v", err)
	}
	var p Plan
	if err := jsonUnmarshal(string(b), &p); err != nil {
		t.Fatalf("读方案失败：%v", err)
	}
	before := map[int][]string{}
	for _, sh := range p.Shots {
		if len(sh.ChibiIDs) > 0 {
			before[sh.ID] = append([]string{}, sh.ChibiIDs...)
		}
	}
	t.Logf("归一之前 chibi_ids：%v", before)

	fail := 0
	for _, it := range ValidatePlan(&p) {
		if it.Level == "fail" {
			fail++
			t.Logf("门 fail（归一前）：%s", it)
		}
	}
	hr := HealPlan(&p)
	t.Logf("HealPlan：ChibiRefs=%d Unknown=%v", hr.ChibiRefs, hr.ChibiRefUnknown)
	for _, sh := range p.Shots {
		if len(before[sh.ID]) > 0 {
			t.Logf("镜 #%d：%v → %v", sh.ID, before[sh.ID], sh.ChibiIDs)
		}
	}
	for _, it := range ValidatePlan(&p) {
		if it.Level == "fail" && containsChibiDangling(it.Msg) {
			t.Errorf("归一之后仍在报 chibi_ids 悬空：%s", it)
		}
	}
	fmt.Println("（探针结束）")
}

func containsChibiDangling(msg string) bool {
	return contains(msg, "chibi_ids→") && contains(msg, "悬空")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
