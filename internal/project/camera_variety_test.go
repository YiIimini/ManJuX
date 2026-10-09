package project

import (
	"strings"
	"testing"
)

// 整集运镜种类（2026-10-09 用户：「场景和分镜运镜很单一」）。
//
// 夹具照**用户看到的那种片子**写：十几镜翻来覆去只有推与拉。这种方案在旧三条门上
// **全绿**（没有空值、没有术语、也没有连续 5 镜同值）—— 这就是"运镜很单一"能一路走到成片的原因。
func TestCameraMonotoneKinds(t *testing.T) {
	pushPull := make([]Shot, 0, 12)
	for i := 1; i <= 12; i++ {
		cam := "The camera pushes in with small amplitude"
		if i%2 == 0 {
			cam = "The camera pulls back slowly"
		}
		pushPull = append(pushPull, Shot{ID: i, Camera: cam})
	}
	used, unused, n, monotone := CameraMonotoneKinds(pushPull)
	if !monotone {
		t.Fatalf("12 镜只有推/拉交替必须判单调，实际 used=%v n=%d", used, n)
	}
	if n != 12 {
		t.Errorf("认得出的样本镜数该是 12，实际 %d", n)
	}
	if strings.Join(used, ",") != "push,pull" {
		t.Errorf("用到的种类该按官方顺序是 push,pull，实际 %v", used)
	}
	if len(unused) == 0 {
		t.Error("没用过的种类不能为空 —— 自纠要拿它给模型挑一个")
	}
	// 旧三条门在这种方案上只该响新这一条（别把新门接到旧门上糊过去）
	if got := CameraIssues(pushPull); len(got) != 1 || got[0].Kind != CamMonotone {
		t.Errorf("推/拉交替只该报 CamMonotone，实际 %+v", got)
	}

	// 铺开到 4 种 → 不判单调（12 镜里推/拉/摇/定格是正常片子）
	var varied []Shot
	cams := []string{"The camera pushes in", "The camera pans right", "Static Shot", "Tracking Shot following her"}
	for i := 1; i <= 12; i++ {
		varied = append(varied, Shot{ID: i, Camera: cams[i%len(cams)]})
	}
	if _, _, _, mono := CameraMonotoneKinds(varied); mono {
		t.Error("铺开到 4 种运镜不该判单调")
	}
	// 样本太薄不判：阈值以下全是同一种也不判
	var few []Shot
	for i := 1; i <= PlanCameraVarietyMinShots-1; i++ {
		few = append(few, Shot{ID: i, Camera: "The camera pushes in"})
	}
	if _, _, _, mono := CameraMonotoneKinds(few); mono {
		t.Errorf("只有 %d 镜时样本太薄（阈值 %d），不该判单调", len(few), PlanCameraVarietyMinShots)
	}
	// 认不出的写法既不计种类、也不计样本（保守：宁可少报，也不要把正常运镜算成"没用过"）
	if ks := CameraKindsIn("handheld drifting through the crowd"); len(ks) != 0 {
		t.Errorf("认不出的写法不该归到任何官方种类，实际 %v", ks)
	}
	// 复合运镜算两种（官方顺序：push 在 pan 之前）
	if ks := CameraKindsIn("panning right while pushing in"); strings.Join(ks, ",") != "push,pan" {
		t.Errorf("复合运镜该算两种、按官方顺序，实际 %v", ks)
	}
}

// 渲染前门：整集只有推/拉的方案必须被报出来（此前这条只写在提示词里，产出侧没人管）。
func TestValidatePlanCatchesMonotoneCamera(t *testing.T) {
	p := monotoneCameraPlan()
	if !hasIssue(ValidatePlan(p), "warn", "运镜只有") {
		t.Fatalf("整集只有推/拉必须报运镜单调：%v", PlanIssuesText(ValidatePlan(p)))
	}
	// 把运镜那一句换开 → 不该再报（证明判据读的是**方案里的运镜**，不是别的东西）。
	open := []string{"The camera zooms in", "The camera trucks left", "The camera tilts up", "The camera arcs around her"}
	for i := range p.Shots {
		p.Shots[i].Camera = open[i%len(open)]
	}
	if hasIssue(ValidatePlan(p), "warn", "运镜只有") {
		t.Errorf("铺开 4 种运镜后不该再报：%v", PlanIssuesText(ValidatePlan(p)))
	}
}

// monotoneCameraPlan 最小形态的方案：12 镜清一色推/拉（其余字段照合规写法，别让无关规则搅浑判据）。
func monotoneCameraPlan() *Plan {
	p := samplePlan()
	p.Shots = nil
	for i := 1; i <= 12; i++ {
		cam := "The camera pushes in with small amplitude"
		if i%2 == 0 {
			cam = "The camera pulls back slowly"
		}
		p.Shots = append(p.Shots, Shot{
			ID: i, Scene: "scene_room", Characters: []string{"char_a"},
			ShotSize: "中景", Camera: cam, Action: "他往前走",
			HookType: "reveal",
			Dialogue: []Line{{Speaker: "阿甲", Text: "我准了。"}},
			Duration: 5, Mode: "ref2va",
			EndState: "他站在门槛前，右手扶门框，视线朝院中",
			H3Prompt: "subject_definitions:\n<Subject 1> is 阿甲 ...\n\ndetailed_description:\n" +
				SampleStyleSentence + "\n[Shot 1] he walks. <d>[Chinese]我准了。</d>",
		})
	}
	return p
}
