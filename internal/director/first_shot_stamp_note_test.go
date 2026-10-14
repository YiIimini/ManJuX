package director

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// first_shot_stamp_note_test.go 首镜时间戳被抹平时**必须播报**（2026-10-14）。
//
// 【为什么单钉播报这一环】平台在读取边界上改了用户的正文（删掉 `[Shot 1] At 00:00.000`）——
// 这种"平台自己动手改文档"的动作如果不播报，用户看到盘上的 `plan.json` 和界面上写的对不上，
// 只会以为平台又出了 bug（2026-10-14 那轮"配音全没了"的报障就是这么开始的）。
// 这里走**真入口** `finishPlan`（三个产出路径共用的收尾），确认：
// ① 正文真的被归一；② 真切点一个字没动；③ `OnNote` 里有一句人话把这件事说出来。
func TestFinishPlanAnnouncesFirstShotStampFix(t *testing.T) {
	const body = "detailed_description:\nCinematic, 2D-animated. \n" +
		"[Shot 1] At 00:00.000 a medium shot holds on 周砚 in the half-open doorway.\n" +
		"[Shot 2] At 00:03.000 the shot cuts to a wide framing from behind him.\n\n" +
		"overall_soundscape:\nRain hammering the iron above his head.\n\n" +
		"non_diegetic_music:\nN/A\n"
	plan := &project.Plan{
		Characters: []project.Character{{ID: "char_zhou", Name: "周砚", Voice: "A low clear baritone."}},
		Scenes:     []project.Scene{{ID: "scene_lane", Name: "铜锣巷七号门口"}},
		Shots: []project.Shot{{
			ID: 2, Duration: 7, Scene: "scene_lane", H3Prompt: body,
			Dialogue: []project.Line{{Speaker: "周砚", Text: "谁在外面？"}},
		}},
	}
	var notes []string
	got, err := finishPlan(context.Background(), nil, plan,
		"《人间收件箱》系列短视频 第01集\n全局风格:GUOFENG\n\n【本集原文】雨夜叩门。",
		Options{OnNote: func(f string, a ...any) { notes = append(notes, fmt.Sprintf(f, a...)) }})
	if err != nil {
		t.Fatalf("收尾不该报错（client=nil 时自纠跳过即可）：%v", err)
	}
	if strings.Contains(got.Shots[0].H3Prompt, "[Shot 1] At") {
		t.Errorf("收尾没抹掉首镜时间戳：%s", headOf(got.Shots[0].H3Prompt, 120))
	}
	if !strings.Contains(got.Shots[0].H3Prompt, "[Shot 2] At 00:03.000") {
		t.Errorf("真切点被误伤：%s", headOf(got.Shots[0].H3Prompt, 200))
	}
	said := false
	for _, n := range notes {
		if strings.Contains(n, "首镜") && strings.Contains(n, "时间戳") {
			said = true
			break
		}
	}
	if !said {
		t.Errorf("平台改了用户的正文却一句话都不说 = 静默改文档。实际播报：%v", notes)
	}
}

func headOf(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
