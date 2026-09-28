package project

import "testing"

// 运镜的三条产出侧判据。
//
// 【为什么要有这些门】2026-09-28 用户问「运镜是否合理」时才发现：运镜此前**只写在提示词里**，
// 产出侧一条判据都没有 —— `Camera` 可空、连续十镜同一个运镜没人管、写成 dolly 这类术语照过。
// 而"连续同景别"早就有门了，运镜却没有。
func TestCameraIssues(t *testing.T) {
	has := func(issues []CameraIssue, kind CameraIssueKind) bool {
		for _, i := range issues {
			if i.Kind == kind {
				return true
			}
		}
		return false
	}

	// ① 每镜都要有运镜（平台规则；官方 §4.3 只规定写法与词表）
	if got := CameraIssues([]Shot{{ID: 1, Action: "她转身"}}); !has(got, CamMissing) {
		t.Errorf("空运镜该被报出来，实际 %v", got)
	}
	// ② 禁摄影术语（平台规则：H3 认的是自然英文动作，术语进正文只是噪音）
	if got := CameraIssues([]Shot{{ID: 1, Camera: "a slow dolly in on her face"}}); !has(got, CamJargon) {
		t.Errorf("dolly 这类术语该被报出来，实际 %v", got)
	}
	// ③ 连续同一个运镜超过上限
	rep := make([]Shot, 0, PlanMaxConsecutiveSameCamera+2)
	for i := 1; i <= PlanMaxConsecutiveSameCamera+2; i++ {
		rep = append(rep, Shot{ID: i, Camera: "Static Shot"})
	}
	if got := CameraIssues(rep); !has(got, CamRepeat) {
		t.Errorf("连续 %d 镜同一个运镜该被报出来，实际 %v", len(rep), got)
	}
	// ④ 合规的分镜：官方写法（自然英文动作 + 幅度/速度按需）不该报
	ok := []Shot{
		{ID: 1, Camera: "The camera pushes in with small amplitude at slow speed toward the letter"},
		{ID: 2, Camera: "The camera pans right with large amplitude, revealing the doorway"},
		{ID: 3, Camera: "Static Shot"},
		{ID: 4, Camera: "Tracking Shot following her"},
	}
	if got := CameraIssues(ok); len(got) != 0 {
		t.Errorf("合规分镜不该报运镜问题，实际 %v", got)
	}
	// ⑤ 空运镜**不参与**"连续同一个运镜"的统计 —— 否则十镜连空会被报成
	//    「连续十镜同一个运镜「」」，用户看不出到底是哪种毛病
	if _, _, n := longestSameCameraRun([]Shot{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}); n != 0 {
		t.Errorf("空运镜不该被算成「同一个运镜」的连续段，实际长度 %d", n)
	}
	// ⑥ 上限内不报（恰好 4 镜同运镜是允许的）
	var atLimit []Shot
	for i := 1; i <= PlanMaxConsecutiveSameCamera; i++ {
		atLimit = append(atLimit, Shot{ID: i, Camera: "Pan Left"})
	}
	if got := CameraIssues(atLimit); has(got, CamRepeat) {
		t.Errorf("恰好 %d 镜同运镜不该报（上限就是它），实际 %v", PlanMaxConsecutiveSameCamera, got)
	}
	// ⑦ 判据只报**种类与镜号**，不报成句的中文 —— 文案在 plan_contract 里，
	//    这样 `docs/门目录.md`（gatecat 扫字面格式串生成）才收得到这三条门
	for _, i := range CameraIssues(rep) {
		if i.Kind == CamRepeat && (i.Cam == "" || i.Start == 0 || i.Run == 0) {
			t.Errorf("CamRepeat 必须带全「哪个运镜 / 从哪镜起 / 连续几镜」，实际 %+v", i)
		}
	}
}
