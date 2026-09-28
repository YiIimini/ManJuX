package pipeline

import (
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// transition.go 场次切换的**转场字幕**（用户《【额外】仿真人10S分镜头指令》与《五、3D漫剧15秒分镜指令》）。
//
// 用户那两份分镜文件都要求：不同场景之间用**渐隐渐显**转场，并让画面里**自然出现地点·时间的字幕**
// （"[地点・时间]"，例如 "马路边・夜晚"）—— 不作为单独字段，而在画面描述里以字幕形式呈现。
// 平台此前完全没有这条 ✗：场景一换就是硬切，观众不知道"这是哪儿、什么时候"。
//
// 【为什么由平台做】这类东西不需要创作：场次一变、地点时间就是**已知量**（场景卡的名字本身就写成
// 「西坡碑林·黄昏」这种「地点·时间」结构），而且官方屏幕文字规则要求画面文字**逐字写进英文双引号**。
// 让 LLM 每镜自觉复述只会时灵时不灵（实测：建立镜头串它就不照做），所以在**装配点**确定性补上。
//
// 只在**场次相对上一镜发生变化**时补（同一场景内连拍不重复打字幕）；首镜也算场次切换（从黑起）。
func EnsureTransitionCaption(prompt string, plan *project.Plan, shot project.Shot, prev *project.Shot) string {
	if plan == nil {
		return prompt
	}
	// 首镜（或方案里找不到上一镜）也算切场：从黑渐显 + 地点时间字幕
	changed := prev == nil || !strings.EqualFold(strings.TrimSpace(prev.Scene), strings.TrimSpace(shot.Scene))
	if !changed {
		return prompt
	}
	if strings.Contains(prompt, transitionMarker) {
		return prompt // 幂等：二次装配不改结果（同一套纪律见 EnsurePropAnchors / EnsureRefManifest）
	}
	loc := strings.TrimSpace(shot.Scene)
	if sc, ok := sceneByID(plan, shot.Scene); ok && strings.TrimSpace(sc.Name) != "" {
		loc = strings.TrimSpace(sc.Name)
	}
	// 【为什么字幕不写进提示词，而是留给后期】用户文件自己给了答案：
	// 《五、3D漫剧15秒分镜指令》写"转场字幕（即梦生成，**若不可靠则后期替换**）"，而实测正是"不可靠"：
	// H3 画汉字会被绘成伪汉字（同一集额头印记/碑面都糊过），所以让模型去"写地名"必然出鬼画符。
	// 于是这里只让模型做**它能做对的事** —— 从黑渐显的转场；字幕文字交给 compose 的 ASS 轨道
	// （平台本来就在烧字幕，汉字字体与排版都现成，见 compose 的场景字幕事件）。
	// 提示词点名"不要画任何文字"，否则模型常会自作主张糊一排假字上去。
	_ = loc
	return prompt + "\n" + transitionMarker + "\n" +
		"SCENE TRANSITION: the shot opens from black with a soft dissolve into the new location (no hard cut). " +
		"Leave the lower part of the frame visually quiet: the location-and-time caption is added in post-production, " +
		"so paint NO lettering, no signage and no caption of your own anywhere in this shot."
}

const transitionMarker = "SCENE TRANSITION (written by the renderer: scene changes carry a fade-in and a location-time caption)"

// SceneCaptionFor 这一镜要不要打场次字幕，要打就打什么字（空串 = 不打）。
//
// 与 EnsureTransitionCaption 同一套判据（首镜或换场才打），但这里给的是**给人看的汉字**
// —— 它不去提示词，而是走 compose 的 ASS 字幕轨道（见 compose 的场次字幕事件）。
// 分工的理由写在 EnsureTransitionCaption 的注释里：模型画不准汉字，字幕该由后期烧。
func SceneCaptionFor(plan *project.Plan, shot project.Shot, prev *project.Shot) string {
	if plan == nil {
		return ""
	}
	if prev != nil && strings.EqualFold(strings.TrimSpace(prev.Scene), strings.TrimSpace(shot.Scene)) {
		return ""
	}
	if sc, ok := sceneByID(plan, shot.Scene); ok && strings.TrimSpace(sc.Name) != "" {
		return strings.TrimSpace(sc.Name)
	}
	return ""
}

// sceneByID 按 id 取场景卡。
func sceneByID(plan *project.Plan, id string) (project.Scene, bool) {
	for _, sc := range plan.Scenes {
		if sc.ID == id {
			return sc, true
		}
	}
	return project.Scene{}, false
}

// HasTransitionCaption 正文里有没有转场字幕条款（契约门/测试用）。
func HasTransitionCaption(prompt string) bool {
	return strings.Contains(prompt, transitionMarker)
}
