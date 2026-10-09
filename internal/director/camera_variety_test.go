package director

import (
	"os"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 分镜提示词里的「运镜铺开」硬约束必须**在拼好的 SystemPrompt 里**（模型吃的是那一段，
// 不是磁盘上那个文件）—— 2026-10-09 用户：「分镜运镜很单一」。
func TestSystemPromptCarriesCameraVarietyRule(t *testing.T) {
	for _, want := range []struct{ needle, why string }{
		{"CAMERA VARIETY", "规则本身要进提示词"},
		{"at least four different types", "要给出可执行的条数（\"多用几种\"这种散文对模型不起作用）"},
		{"Zoom / Push / Pan / Truck / Tilt / Pedestal / Arc / Tracking / Static / Shake / POV / Roll",
			"要照官方 §4.3 的 12 条词表点名，模型才有词可用"},
	} {
		if !strings.Contains(SystemPrompt, want.needle) {
			t.Errorf("SystemPrompt 缺 %q —— %s", want.needle, want.why)
		}
	}
}

// 整集运镜单调要有**有界**自纠：只换运镜那一句、最多 repairMaxCameraRewrites 镜，
// 而且不许把真缺陷（空提示词）挤出预算（2026-10-09 用户：「分镜运镜很单一」）。
func TestRepairRewritesMonotoneCamera(t *testing.T) {
	plan := monotoneRepairPlan()
	issues := collectRepairable(plan)
	if len(issues) == 0 {
		t.Fatal("整集只有推/拉 + 一镜空提示词，自纠一条都没收")
	}
	// ① 真缺陷优先：第一条必须是那镜空提示词（预算先给真缺陷）
	if !strings.Contains(issues[0].note, "提示词是空的") {
		t.Errorf("空提示词必须排在运镜单调之前，实际第一条：%s", issues[0].note)
	}
	// ② 运镜类修复有界、且只改运镜那一句
	cam := 0
	for _, is := range issues {
		if !strings.Contains(is.note, "运镜与整集其它镜重复") {
			continue
		}
		cam++
		if !strings.Contains(is.fix, "Rewrite ONLY the camera-motion sentence") {
			t.Errorf("指令必须限定「只改运镜那一句」，否则会把画面意图改坏：%s", is.fix)
		}
		if !strings.Contains(is.fix, "NOT used yet") || !strings.Contains(is.fix, "zoom") {
			t.Errorf("指令要给出**没用过**的官方运动类型让模型挑：%s", is.fix)
		}
		if is.shotID == plan.Shots[0].ID {
			t.Errorf("空提示词那一镜不该同时收一条运镜修复（#%d）", is.shotID)
		}
	}
	// 【2026-10-09 反向验证咬出来的摆设守卫】原来只断言 cam == repairMaxCameraRewrites ——
	// 拿**同一个常量**当预期值，把常量改成 0 时两边一起变，测试照样绿。
	// 所以另外钉两条：上限本身的取值（改动要连着理由一起改）、以及"至少收了运镜修复"。
	if repairMaxCameraRewrites != 4 {
		t.Errorf("运镜自纠上限被改成 %d —— 理由写在常量旁边（取 4：换三四镜就足以打破「一直是推/拉」）；"+
			"改它就要连着理由一起改，别只改数", repairMaxCameraRewrites)
	}
	if cam < 1 {
		t.Errorf("运镜单调一条修复都没收 —— 判据或接线断了")
	}
	if cam != repairMaxCameraRewrites {
		t.Errorf("运镜修复该打满上限 %d 条，实际 %d 条", repairMaxCameraRewrites, cam)
	}
	// ③ 运镜铺开之后一条都不该再有（判据读的是**方案里的运镜**）
	open := []string{"The camera zooms in", "The camera pans left", "Static Shot", "The camera tilts down"}
	for i := range plan.Shots {
		plan.Shots[i].Camera = open[i%len(open)]
	}
	for _, is := range collectRepairable(plan) {
		if strings.Contains(is.note, "运镜与整集其它镜重复") {
			t.Errorf("运镜已铺开，不该再收运镜修复：%s", is.note)
		}
	}
}

// 接线：运镜单调的判据只能有一处（project.CameraMonotoneKinds）——
// 自纠自己再算一套阈值，就会出现「门说合规、修的时候又说违规」。
func TestRepairUsesTheSameCameraJudge(t *testing.T) {
	raw, err := os.ReadFile("repair.go")
	if err != nil {
		t.Fatalf("读不到 repair.go：%v", err)
	}
	if !strings.Contains(string(raw), "project.CameraMonotoneKinds(plan.Shots)") {
		t.Error("自纠没接 project.CameraMonotoneKinds —— 那就是第二套阈值")
	}
}

// monotoneRepairPlan 12 镜清一色推/拉 + 第 1 镜空提示词（真缺陷）。
//
// 每镜 7 秒且正文里有两刀 —— 免得踩上"一镜到底"与"词数超标"那两条，把判据搅浑。
func monotoneRepairPlan() *project.Plan {
	p := &project.Plan{}
	for i := 1; i <= 12; i++ {
		cam := "The camera pushes in with small amplitude"
		if i%2 == 0 {
			cam = "The camera pulls back slowly"
		}
		h3 := ""
		if i > 1 {
			// 照**真实形态**写：`[Shot N]` 必须在行首（`h3ShotHeadRe` 是 `(?m)^\s*\[Shot\s+\d+\]`）——
			// 一行里塞两个 `[Shot N]` 只会算一刀，于是每镜都踩上"一镜到底"，把判据搅浑。
			h3 = "detailed_description:\n" +
				"[Shot 1] she lifts the blade on the wet flagstones.\n" +
				"[Shot 2] At 00:03.500, the blade settles on the stone."
		}
		p.Shots = append(p.Shots, project.Shot{ID: i, Scene: "scene_room", Camera: cam, Duration: 7, H3Prompt: h3})
	}
	return p
}
