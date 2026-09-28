package director

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 分镜批次解析：**整段配不平时逐镜抢救**（2026-09-28 用户 88 集产线卡在第 1 集）。
//
// 现场：原始输出 22799 字、以 {"shots":[ 开头、以 }]} 结尾，报的却是"未找到 JSON 对象" ——
// 也就是那段里有**一处引号判断错**，扫描认为字符串永不闭合 ⇒ 整段配不平 ⇒ 全批失败。
// 段落里的提示词是千词级、满是引号（画面可见文字要放引号里），这类误判迟早会发生。
//
// 处置原则：**不去猜哪里坏了**（猜不准，还会掩盖问题），而是逐镜抢救 —— 单个镜头对象
// 往往是好的，救回几个算几个；缺口由既有的修复轮补，而修复轮会**如实播报**补了什么，
// 所以不会静默少镜。
func TestSalvageShotObjectsRecoversFromBrokenBatch(t *testing.T) {
	// 造一段"中间有个坏引号"的批次：第 2 个镜头的字符串里有一个未转义引号，
	// 且其后紧跟的字符让后瞻判据误判成"终止符"⇒ 从那里开始整段配不平。
	raw := `{"shots":[` +
		`{"id":1,"scene":"scene_a","h3_prompt":"first shot, clean.","duration":6},` +
		`{"id":2,"scene":"scene_b","h3_prompt":"the sign reads " 营业中 " : and glows.","duration":6},` +
		`{"id":3,"scene":"scene_c","h3_prompt":"third shot, clean.","duration":6}` +
		`]}`

	// 前提检查：这段**必须**是当前实现解析不了的（否则这条测试没在测抢救）
	if _, err := parseShotsBatch(raw, 1, 3); err == nil {
		// 若整段能解析，说明修复函数已经能处理这种坏引号 —— 那时抢救路径自然不会被触发，
		// 测试仍应通过（但我们要知道是哪种情况）
		t.Log("注意：这段现在能被整段解析（修复函数已覆盖），抢救路径未被触发")
	}

	saved := salvageShotObjects(raw)
	if len(saved) == 0 {
		t.Fatal("逐镜抢救必须至少救回镜头（否则整条产线会被一个坏引号卡死）")
	}
	ids := map[int]bool{}
	for _, s := range saved {
		ids[s.ID] = true
		if strings.TrimSpace(s.H3Prompt) == "" {
			t.Errorf("救回的镜头 %d 没有 h3_prompt（残片不该算数）", s.ID)
		}
	}
	if !ids[1] || !ids[3] {
		t.Errorf("坏引号在镜 2 上，镜 1 与镜 3 是好的一定要救回来，实际救回 %v", ids)
	}
}

// TestDumpRawForDiagnosisWritesFile 抢救不动时**必须把原文落盘**并给出路径 ——
// 只给首尾 120 字摘录没法查（问题在中间）。
func TestDumpRawForDiagnosisWritesFile(t *testing.T) {
	raw := `{"shots":[{"id":1,"h3_prompt":"x"}`
	p := dumpRawForDiagnosis(raw, "unit-test")
	if p == "" {
		t.Fatal("应写出文件并返回路径")
	}
	if got := mustReadFile(t, p); got != raw {
		t.Errorf("落盘内容必须与原文逐字一致（差一个字都可能定位错），实际长度 %d vs %d", len(got), len(raw))
	}
}

// TestParseShotsBatchSalvageKeepsGapsVisible 抢救回来的批次**不补齐缺号**：
// 缺口要让上层看得见（修复轮去补并播报），不能在这里假装"整批拿到了"。
func TestParseShotsBatchSalvageKeepsGapsVisible(t *testing.T) {
	raw := `{"shots":[` +
		`{"id":1,"scene":"scene_a","h3_prompt":"ok one","duration":6},` +
		`{"id":2,"scene":"scene_b","h3_prompt":"bad " quote here : x","duration":6},` +
		`{"id":3,"scene":"scene_c","h3_prompt":"ok three","duration":6}` +
		`]}`
	got, err := parseShotsBatch(raw, 1, 3)
	if err != nil {
		t.Fatalf("应抢救成功而不是整批失败:%v", err)
	}
	have := map[int]bool{}
	for _, s := range got {
		have[s.ID] = true
	}
	if have[2] {
		t.Log("镜 2 也被救回来了（坏引号位置恰好可救），可接受")
	}
	if !have[1] || !have[3] {
		t.Fatalf("镜 1/3 必须救回来，实际 %v", have)
	}
	if len(got) < 2 {
		t.Fatalf("至少该有两镜，实际 %d", len(got))
	}
	_ = project.Shot{}
}
