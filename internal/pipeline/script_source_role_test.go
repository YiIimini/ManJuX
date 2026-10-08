package pipeline

import (
	"os"
	"strings"
	"testing"
)

// script_source_role_test.go 剧本阶段的**接线门**：分镜必须以【本集剧情】(剧本) 为主源。
//
// 为什么用"读源码断言"：这条口径的价值全在"提示词真的按这么拼"与"没剧本时真的说了"，
// 而这两件事都不报错、也不会让别的测试变红（脚本阶段只有真机才会跑到）。见 AGENTS.md
// 「一条规则 → 一条测试」：散文规则管不住模型，接线也得有机检。
func TestScriptStageUsesScriptAsPrimarySource(t *testing.T) {
	raw, err := os.ReadFile("stages_script.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	// ① 必须**解析**两段（剧本 / 原文），而不是把整团文本当"原文"用。
	if !strings.Contains(src, "project.EpisodeSourceParts(text)") {
		t.Error("剧本阶段没有解析【本集剧情】/【本集原文】—— 分镜又只能把两者当同一份'原文'用")
	}
	// ② 有剧本时，提示词必须写明"以剧本为准、逐场按它排"。
	if !strings.Contains(src, "SCRIPT FIRST") {
		t.Error("有剧本时没有给模型写明「以剧本为准排分镜」")
	}
	// ③ **没有剧本时必须说出来**（用户口径的反面：不能静默退回原文还说一切正常）。
	if !strings.Contains(src, "没有剧本") {
		t.Error("缺失剧本时没有如实播报 —— 用户只会看到分镜围着原文转")
	}
	// ④ 反面：不许再让分镜阶段"从原文提炼/重建"（那是专职编剧会话的活，见 server/novels_jobs.go）。
	if strings.Contains(src, "ADAPTATION IS A REWRITE") {
		t.Error("剧本阶段还在让模型「从原文提炼/重建」—— 等于把编剧的活重做，且两头都不作数")
	}
}
