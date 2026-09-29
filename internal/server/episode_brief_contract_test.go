package server

import (
	"os"
	"strings"
	"testing"
)

// 剧本（① 小说转剧本）必须**结构完整**（2026-09-29 用户：
// 「说明内容转剧本不给力，然后导致分镜脚本不给力」）。
//
// 剧本是分镜**唯一**的输入：少了「目的/冲突/爽点」这一节，分镜就不知道哪一下是重点
// （成片重点平摊＝压不住场）；少了「关键道具与信息」，道具的位置关系就丢
// （实测「Q版总蹲在碑生左肩上」在成片里变成了单独蹲在石台上）。
//
// 判据落在**实际要求模型写出的那几节**上（输出契约逐字照抄，缺节就报），
// 而不是"我改过提示词"。
func TestEpisodeBriefMustCarryItsStructure(t *testing.T) {
	// ① 输出契约必须在系统提示词里（否则模型没有理由按这个结构写）
	raw := readBriefSource(t)
	for _, want := range []struct{ needle, why string }{
		{"【开场爆点】", "3 秒爆点的位置要单独标出（用户文件第一条）"},
		{"【场 1|地点|时间|内外】", "场次头（地点/时间/内外）—— 分镜靠它分场与定机位"},
		{"目的:", "每场的目的 —— 分镜靠它决定「这场要交代什么」"},
		{"爽点:", "每场的爽点（小/大）—— 分镜靠它决定特写与时长给谁"},
		{"钩子:", "每场的钩子 —— 用户文件的「段段留钩」"},
		{"【关键道具与信息】", "道具与信息点清单（含位置，如「蹲在碑生左肩上」）"},
		{"【本集钩子】", "本集终极钩子"},
	} {
		if !strings.Contains(raw, want.needle) {
			t.Errorf("① 剧本的输出契约缺少 %q —— %s", want.needle, want.why)
		}
	}
	// ② 缺节要**如实报出来**（此前是静默的：剧本照样回填、产线照跑，只是分镜凭空猜）
	if !strings.Contains(raw, "missingBriefSections(out)") {
		t.Error("剧本回填后没有结构自检 —— 缺节会静默传下去")
	}
	// ③ 判据本身：完整剧本过、缺节的剧本要报出缺哪几节
	good := "【开场爆点】他改了一个字。\n【场 1|第七坡碑林|日|外】\n目的:立住主角\n冲突:丙十七 vs 邢五\n" +
		"转折:册上的字变了\n爽点:大 —— 当众打脸\n钩子:折了三年寿\n正文:……\n【关键道具与信息】\n- 碑刀|右手|改字\n【本集钩子】册子翻页"
	if miss := missingBriefSections(good); len(miss) != 0 {
		t.Fatalf("完整剧本被误判缺节：%v", miss)
	}
	thin := "【场 1|碑林|日|外】\n正文:他改了个字。" // 上一版实测就长这样：散文，没有结构标注
	miss := missingBriefSections(thin)
	for _, want := range []string{"开场爆点", "目的", "冲突", "爽点", "钩子", "关键道具与信息", "本集钩子"} {
		found := false
		for _, m := range miss {
			if strings.Contains(m, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("缺节自检漏了 %q（实得 %v）", want, miss)
		}
	}
}

// readBriefSource 读 ① 剧本那一步的实现源码（就在同一个包内）。
func readBriefSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("novels_jobs.go")
	if err != nil {
		t.Fatalf("读 novels_jobs.go 失败：%v", err)
	}
	return string(raw)
}
