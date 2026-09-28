package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// transition_test.go 场次切换的转场字幕（用户《【额外】仿真人10S分镜头指令》《五、3D漫剧15秒分镜指令》）。
//
// 两份文件都要求：不同场景之间**渐隐渐显**转场，并让画面里自然出现**地点·时间字幕**（"[地点・时间]"）。
// 平台此前完全没有这条 —— 场景一换就是硬切，观众不知道"这是哪儿、什么时候"。
func TestEnsureTransitionCaption(t *testing.T) {
	plan := &project.Plan{
		Scenes: []project.Scene{
			{ID: "scene_bei", Name: "西坡碑林·黄昏"},
			{ID: "scene_shed", Name: "拓碑棚"},
		},
		Shots: []project.Shot{
			{ID: 1, Scene: "scene_bei"},
			{ID: 2, Scene: "scene_bei"},
			{ID: 3, Scene: "scene_shed"},
		},
	}
	base := "detailed_description:\n[Shot 1] he walks."

	// 首镜：从黑起，带地点时间字幕
	first := EnsureTransitionCaption(base, plan, plan.Shots[0], nil)
	if !HasTransitionCaption(first) {
		t.Fatal("首镜也算场次切换（从黑渐显），要补转场字幕")
	}
	if !strings.Contains(first, "added in post-production") || !strings.Contains(first, "paint NO lettering") {
		t.Error("转场镜要明确「字幕后期加、你别画任何文字」——模型画汉字必成伪汉字")
	}
	if got := project.SceneCaptionFor(plan, 0); got != "西坡碑林·黄昏" {
		t.Errorf("后期字幕的文字应取场景卡名字，实际 %q", got)
	}
	if got := project.SceneCaptionFor(plan, 1); got != "" {
		t.Errorf("同场景连拍不打字幕，实际 %q", got)
	}
	// 幂等
	if again := EnsureTransitionCaption(first, plan, plan.Shots[0], nil); again != first {
		t.Error("二次装配不该改结果")
	}
	// 同场景连拍：不重复打字幕
	same := EnsureTransitionCaption(base, plan, plan.Shots[1], &plan.Shots[0])
	if HasTransitionCaption(same) {
		t.Error("同一场景内连拍不该重复加转场字幕（否则每镜都冒一条地名）")
	}
	// 换场：必须补，且取新场景的名字
	sw := EnsureTransitionCaption(base, plan, plan.Shots[2], &plan.Shots[1])
	if !HasTransitionCaption(sw) {
		t.Error("场次变化必须补转场字幕")
	}
	if got := project.SceneCaptionFor(plan, 2); got != "拓碑棚" {
		t.Errorf("换场要打新场景名字幕，实际 %q", got)
	}
	// 提示词里**不许**出现要模型画的字幕文字（汉字会被画成伪汉字）
	if strings.Contains(sw, "拓碑棚") {
		t.Error("地名汉字不该进提示词 —— 模型画不准汉字，字幕交给后期 ASS")
	}
}
