package pipeline

import (
	"strings"
	"testing"
)

// style_medium_test.go 多风格组合的**写法**与**互斥判定**（2026-10-09 用户：「H3 不是可以多风格组合吗？」）。
//
// 查了官方原文后的结论：
//   - 官方风格是**开头一句话里的并列形容**（`h3-prompt-writing/references/base-en.txt` §4.1：
//     「Common styles include `Cinematic`, `live-action`, `2D-animated`, `3D CG`…」，
//     示例 `[Shot 1] Live-action, cinematic, a medium-wide shot frames...`）⇒ 组合用**逗号**；
//   - 官方唯一用 ` + ` 组合的是**任务类型**（`ref-en.txt`：「combine the task types with ` + `」），不是风格；
//   - 平台自己的规则写着风格串**不许含互斥词组**（`docs/技能侧改动-电影级口径.md` 检查表）。
//
// 所以：多选组合允许（写成一串），但**介质矛盾**要能判定出来并如实点名。
func TestComposeStyleHintUsesOfficialStyleSentence(t *testing.T) {
	got := ComposeStyleHint([]string{"guofeng_cinematic", "guoman_2d"}, "")
	if !strings.Contains(got, "GUOFENG ANIMATED-FEATURE ART DIRECTION") ||
		!strings.Contains(got, "CHINESE 2D ANIME STYLE") {
		t.Fatalf("两个预设都要进串，实际：%q", got)
	}
	if !strings.Contains(got, ", ") {
		t.Errorf("官方风格句是逗号并列（示例 `Live-action, cinematic`），实际：%q", got)
	}
	if strings.Contains(got, " + ") {
		t.Errorf("` + ` 是官方给**任务类型**的组合语法，不该用在风格上：%q", got)
	}
	// 去重：同一个预设勾两次不该出现两遍
	dup := ComposeStyleHint([]string{"guoman_2d", "guoman_2d"}, "")
	if strings.Count(dup, "CHINESE 2D ANIME STYLE") != 1 {
		t.Fatalf("重复勾选要去重，实际：%q", dup)
	}
	// 附加要求仍然接在预设串之后（分隔符落在**预设串末尾**，不是预设串开头 —— 第一版断言写错，
	// 判据自己拦住了我）
	hint := ComposeStyleHint([]string{"guoman_2d"}, "夜景不要霓虹")
	if !strings.HasSuffix(hint, "夜景不要霓虹") || !strings.Contains(hint, "DONGHUA QUALITY; 夜景不要霓虹") {
		t.Fatalf("附加要求应接在预设串之后，实际：%q", hint)
	}
	// 未知 key 不进串（由 UnknownStyleKeys 单独点名）
	if s := ComposeStyleHint([]string{"made_up"}, ""); s != "" {
		t.Fatalf("未知 key 不该编出串来，实际：%q", s)
	}
}

// TestStyleMediumConflicts 介质互斥判定。
func TestStyleMediumConflicts(t *testing.T) {
	// ① 真机会出事故的组合：三维 CG 国风 + 二维动画 ⇒ 必须判出来（点名两项与介质）
	got := StyleMediumConflicts([]string{"guofeng_cinematic", "guoman_2d"})
	if len(got) != 1 {
		t.Fatalf("三维 CG × 二维动画 必须判为矛盾，实际：%v", got)
	}
	if !strings.Contains(got[0], "国风电影级") || !strings.Contains(got[0], "三维 CG") ||
		!strings.Contains(got[0], "国漫二维动画") || !strings.Contains(got[0], "二维动画") {
		t.Fatalf("要点名是哪两项、各是什么介质，实际：%q", got[0])
	}
	// ② 真人实拍 + 水墨 ⇒ 也是矛盾
	if len(StyleMediumConflicts([]string{"live_action", "ink_wash"})) != 1 {
		t.Error("真人实拍 × 手绘/水墨 必须判为矛盾")
	}
	// ③ 同介质（三维 CG 的两个预设）⇒ **不算**矛盾（可以叠题材）
	if c := StyleMediumConflicts([]string{"guofeng_cinematic", "pixar_3d"}); len(c) != 0 {
		t.Fatalf("同为三维 CG 不该判矛盾，实际：%v", c)
	}
	// ④ 未声明介质的题材/调子类 ⇒ 不参与（刻意保守：不猜介质，免得误报）
	if c := StyleMediumConflicts([]string{"cyberpunk", "film_noir", "hongkong"}); len(c) != 0 {
		t.Fatalf("未声明介质的预设不该被判矛盾，实际：%v", c)
	}
	// ⑤ 单项 / 空 / 未知 key 都不报
	if c := StyleMediumConflicts([]string{"guoman_2d"}); len(c) != 0 {
		t.Fatalf("单项不该报矛盾：%v", c)
	}
	if c := StyleMediumConflicts(nil); len(c) != 0 {
		t.Fatalf("空不该报矛盾：%v", c)
	}
	if c := StyleMediumConflicts([]string{"made_up_style", "guoman_2d"}); len(c) != 0 {
		t.Fatalf("未知 key 不该参与判定：%v", c)
	}
	// ⑥ 三个介质一起勾：一次说清（不是只报第一对）
	if c := StyleMediumConflicts([]string{"live_action", "guoman_2d", "claymation"}); len(c) != 1 ||
		!strings.Contains(c[0], "真人实拍") || !strings.Contains(c[0], "二维动画") || !strings.Contains(c[0], "定格动画") {
		t.Fatalf("三种介质应一次点名，实际：%v", c)
	}
	// ⑦ 输出顺序固定（map 遍历无序 ⇒ 播报与判据都要稳）
	a := StyleMediumConflicts([]string{"guoman_2d", "live_action"})
	b := StyleMediumConflicts([]string{"live_action", "guoman_2d"})
	if len(a) != 1 || len(b) != 1 || a[0] != b[0] {
		t.Fatalf("与勾选顺序无关、输出要固定，实际：%v vs %v", a, b)
	}
	// ⑧ 介质中文名只此一份（界面也用 MediumLabel，别在前端再抄一张表）
	if MediumLabel("live") != "真人实拍" || MediumLabel("unknown_x") != "unknown_x" {
		t.Fatalf("MediumLabel 不对：%q / %q", MediumLabel("live"), MediumLabel("unknown_x"))
	}
}
