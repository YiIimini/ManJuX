package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// qc_brief_test.go 质检的两条**平台事实**必须告诉判据（2026-10-07 真机实测）。
//
// 现场那一集（《名册之外，八万零一号》第 01 集，13 镜）：
//
//	· 5 镜被判"疑似黑屏"不合格（最暗采样帧 0.67–0.82）⇒ 整集"质检未通过"、**批量产线停在第 1/88 集**；
//	  而这 5 镜全落在**场景卡自己写明是暗调**的两个场景里；
//	· 镜 5 / 13 是内心镜（`vo_kind=inner` + `chibi_ids=[祁七Q版]`），画面里"肩上巴掌大的 Q 版小祁七"
//	  是**平台设计**（用户 2026-09-27 口径：内心活动由 Q 版演出），而 VLM 判成
//	  "肩膀上多出一个迷你人形/玩偶状附属物，严重主体崩坏"（镜 5 给 55 分、镜 13 给 42 分）。
//
// 判据：把**计划里写着的事实**告诉判据（场景光态 / 内心戏 / 系统戏），而不是让判据自己去猜 ——
// 这与"能确定性做到的事不要让模型自觉"是同一条纪律。

func TestDarkSceneDeclaredComesFromTheSceneCard(t *testing.T) {
	cases := []struct {
		name  string
		state string
		want  bool
	}{
		// 真机那两个场景的光态原文（照抄，别改写成"两三句意思一下"）
		{"维护舱（真机）", "室内·无昼夜；冷白工作灯为主光，自右上方三排灯带斜下打入，右侧一列先亮，其余沉在黑暗里，无自然光、无暖色", true},
		{"舱门缝（真机）", "室内·无昼夜亦无外光；主光自舱内右侧冷白灯带斜掠过来，缝口只有灰雾透出的微弱冷灰反光，缝外漆黑无光，无暖色", true},
		{"值班终端（真机·不是暗调）", "室内·无昼夜；主光为舱壁玻璃板的冷青待机光，自右上方斜落，辅以远处右侧冷白灯带余光，无暖色", false},
		{"空光态", "", false},
		// 2026-10-08 真机《顺其自然》第01集镜 6 的场景光态原文（照抄）：傍晚 = 天生低光 ⇒ 算"声明了暗调"。
		{"街口募捐点（真机·傍晚）", "1999年12月傍晚,外景;主光为左侧地平线残余的冷蓝暮光,右侧路灯暖黄光为副光。", true},
		// 但**白天**的场景不许放宽（同一集镜 8/12/13 的原文）——那几镜偏暗是真崩坏。
		{"老城邮局营业厅（真机·清晨）", "清晨七点四十,内景;主光为左侧高窗的冷蓝白日光,柜台暖橙吊灯作补光,相邻镜头不得跳到正午或夜晚。", false},
		{"总会会长办公室（真机·午后）", "午后,内景;主光为左侧窗户斜射的暖金日光,室内保持低照度暖灰暗部。", false},
		{"城市地铁站入口（真机·冬日午后）", "冬日午后,外景;主光为天顶薄云漫射的冷银灰光,站内暖橙灯为次光。", false},
		// 夜里**有灯**的戏也不放宽（放宽等于放走真崩坏）：只认"傍晚/黎明"这种天生低光的时段。
		{"夜里有灯（不是天生低光）", "夜,内景;柜台顶灯与台灯全开,主光自右上方斜下。", false},
	}
	for _, c := range cases {
		if got := darkSceneDeclared(project.Scene{LightState: c.state}); got != c.want {
			t.Errorf("%s：darkSceneDeclared=%v，期望 %v —— 判据必须来自场景卡光态原文", c.name, got, c.want)
		}
	}
}

func TestQCShotBriefTellsTheJudgeOurDesign(t *testing.T) {
	plan := &project.Plan{}
	darkScene := project.Scene{ID: "scene_bay", Name: "归墟环维护舱大厅",
		LightState: "室内·无昼夜；冷白工作灯为主光…其余沉在黑暗里，无自然光、无暖色"}
	litScene := project.Scene{ID: "scene_term", Name: "维护舱东头值班终端",
		LightState: "室内·无昼夜；主光为舱壁玻璃板的冷青待机光…无暖色"}

	// ① 内心镜：必须说清"Q 版是设计的一部分"
	inner := project.Shot{ID: 5, ShotSize: "中近景", Camera: "Truck Left at slow speed", Characters: []string{"祁七"},
		VoKind: "inner", ChibiIDs: []string{"祁七Q版"}, Scene: "scene_term"}
	if got := qcShotBrief(plan, inner, litScene, true); !strings.Contains(got, "内心戏") || !strings.Contains(got, "不得因此扣分") {
		t.Errorf("内心镜的概要必须说明 Q 版演出是设计（否则 VLM 判它「多余角色/主体崩坏」）：%s", got)
	}
	// ② 系统镜：机器人＋面板同样是设计
	sys := project.Shot{ID: 1, ShotSize: "全景", Camera: "Push In", Characters: []string{"祁七"},
		VoKind: "system", Scene: "scene_bay"}
	if got := qcShotBrief(plan, sys, darkScene, true); !strings.Contains(got, "系统戏") {
		t.Errorf("系统镜的概要必须说明机器人/面板是演出者：%s", got)
	}
	// ③ 暗调场景：说清"暗是设计，只判暗部细节有没有丢"
	if got := qcShotBrief(plan, project.Shot{ID: 2, Scene: "scene_bay"}, darkScene, true); !strings.Contains(got, "暗调场景") {
		t.Errorf("暗调场景的概要必须说明黑暗本身是设计：%s", got)
	}
	// ④ 普通镜：不许凭空空加口径说明（免得把判据的注意力搅浑）
	plain := project.Shot{ID: 9, ShotSize: "中景", Camera: "Tracking Shot", Characters: []string{"祁七"}, Scene: "scene_term"}
	if got := qcShotBrief(plan, plain, litScene, true); got != "中景/Tracking Shot/角色:[祁七]" {
		t.Errorf("普通镜的概要该保持原样，实际：%s", got)
	}
	// ⑤ 场景卡查不到（hasScene=false）时不加暗调说明
	if got := qcShotBrief(plan, plain, project.Scene{}, false); strings.Contains(got, "暗调场景") {
		t.Errorf("没有场景卡时不该凭空说它是暗调：%s", got)
	}
}
