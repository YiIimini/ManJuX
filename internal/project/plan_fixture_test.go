package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// plan_fixture_test.go **双侧共组的端到端证据**（不是自造样本，是真产物）。
//
// testdata/skill-produced-plan-ep01.json 是技能端 ManJuNovel 的
// `scripts/manju_plan.py build` 对《拜我别后悔》第 1 章真跑出来的方案
// （56 镜 / 278 秒 / 4 场景），原样拷进来的。平台侧校验器必须**收得下**它 ——
// 这条测试把"技能端产出 → 平台端守门"这条接缝钉住：
//
//   - 规则一旦收紧(比如把 warn 升成 fail、或加一条新 fail),这里立刻红:
//     要么修技能端产出,要么承认契约变更 —— 而不许悄悄让技能端的包全被拒;
//   - 有人改技能端脚本导致产出不合契约,把新产物拷进来重跑,同样立刻红。
//
// 为什么用"真产物"而不是手写样本:手写样本只能证明校验器自洽,证明不了两侧真的对得上。
func loadFixture(t *testing.T, name string) *Plan {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读不到夹具 %s: %v", name, err)
	}
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("夹具不是合法 Plan JSON: %v", err)
	}
	return &p
}

// TestSkillProducedPlanPassesContract 技能端真产物必须过契约(fail 级零命中)。
func TestSkillProducedPlanPassesContract(t *testing.T) {
	plan := loadFixture(t, "skill-produced-plan-ep01.json")
	issues := ValidatePlan(plan)
	fails := PlanFails(issues)
	if len(fails) > 0 {
		t.Fatalf("技能端产物被平台拒收(%d 条 fail)—— 两侧契约已经走散:\n%s",
			len(fails), joinIssues(fails))
	}
	if len(plan.Shots) < 20 {
		t.Fatalf("夹具像是残缺的:只有 %d 镜", len(plan.Shots))
	}
	// 结构事实也要对:每镜的场景/角色都必须是已声明的 id(悬空引用是双侧共组最怕的错)
	sceneIDs := map[string]bool{}
	for _, s := range plan.Scenes {
		sceneIDs[s.ID] = true
	}
	charIDs := map[string]bool{}
	for _, c := range plan.Characters {
		charIDs[c.ID] = true
	}
	for _, sh := range plan.Shots {
		if !sceneIDs[sh.Scene] {
			t.Fatalf("镜 %d 引用了未声明的场景 %q", sh.ID, sh.Scene)
		}
		for _, cid := range sh.Characters {
			if !charIDs[cid] {
				t.Fatalf("镜 %d 引用了未声明的角色 %q", sh.ID, cid)
			}
		}
		if sh.Duration < PlanMinShotSeconds || sh.Duration > PlanMaxShotSeconds {
			t.Fatalf("镜 %d 时长 %d 秒越界", sh.ID, sh.Duration)
		}
	}
	// 技能端产出的时长应当已经满足台词预算(FitDialogueDurations 在导入时应当空转):
	// 这是"技能端算过一遍"的意义 —— 平台不用替它改东西。
	for _, sh := range plan.Shots {
		if need := NeededShotSeconds(sh); need > sh.Duration && need <= PlanMaxShotSeconds {
			t.Fatalf("镜 %d 时长 %d 秒装不下台词(需 %d 秒)——技能端没做时长预算",
				sh.ID, sh.Duration, need)
		}
	}
	if len(issues) > 0 {
		t.Logf("契约 warn(%d 条,不阻断):\n%s", len(issues), joinIssues(issues))
	}
}

// TestSkillProducedManifestDeclaresContract manifest 必须声明契约名与版本。
func TestSkillProducedManifestDeclaresContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "skill-produced-manifest-ep01.json"))
	if err != nil {
		t.Fatalf("读不到 manifest 夹具: %v", err)
	}
	var man struct {
		Contract string `json:"contract"`
		Version  int    `json:"version"`
		Counts   struct {
			Shots int `json:"shots"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(raw, &man); err != nil {
		t.Fatalf("manifest 不是合法 JSON: %v", err)
	}
	if man.Contract != "manjux.plan" {
		t.Fatalf("契约名不符: %q(应为 manjux.plan)", man.Contract)
	}
	if man.Version != PlanContractVersion {
		t.Fatalf("契约版本走散: 技能端 v%d / 平台端 v%d —— 必须同版本一起改",
			man.Version, PlanContractVersion)
	}
	plan := loadFixture(t, "skill-produced-plan-ep01.json")
	if man.Counts.Shots != len(plan.Shots) {
		t.Fatalf("manifest 记的镜数(%d)与 plan 实际(%d)不一致", man.Counts.Shots, len(plan.Shots))
	}
}

func joinIssues(issues []Issue) string {
	out := ""
	for _, it := range issues {
		out += "  " + it.String() + "\n"
	}
	return out
}
