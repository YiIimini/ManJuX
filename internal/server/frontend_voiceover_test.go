package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// frontend_voiceover_test.go 工作台里要**看得见**画外演出（技能侧 2026-09-27 口径）。
//
// 为什么要一枚标记：`vo_kind` 决定**这一镜的参考图**（内心镜给 Q 版、系统镜给机器人＋面板），
// 也就是"画面里该出现谁"。人审时若只能靠翻提示词正文才知道，等于把"看画面"变成"读英文"。
// 另外 Q 版形象（kind=chibi）会被当成"这个人的第 N 套衣服"—— 页面上要能区分。
func TestFrontendShowsVoiceoverShots(t *testing.T) {
	root := filepath.Join("..", "..", "frontend", "src")

	novels, err := os.ReadFile(filepath.Join(root, "views", "NovelsView.vue"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(novels)
	for _, want := range []string{"s.vo_kind === 'inner'", "s.vo_kind === 'system'", "内心·Q版演出", "系统·面板＋机器人"} {
		if !strings.Contains(src, want) {
			t.Errorf("剧本脚本页签缺 %q（vo_kind 要看得见）", want)
		}
	}
	// 认不出的取值要显眼（它会被当普通镜渲染）
	if !strings.Contains(src, "vo_kind 不认识") {
		t.Error("未知 vo_kind 要给一枚显眼的标记（会被当普通镜渲染）")
	}
	// 名字在本地查，不在后端再拼一份
	if !strings.Contains(src, "function voCastText(") || !strings.Contains(src, "script.value?.characters") {
		t.Error("演出者名字要在本地查（script.characters/props 就在手上）")
	}

	proj, err := os.ReadFile(filepath.Join(root, "views", "ProjectView.vue"))
	if err != nil {
		t.Fatal(err)
	}
	ps := string(proj)
	if !strings.Contains(ps, "function isChibiCard(") || !strings.Contains(ps, "chibi") {
		t.Error("项目页要能认出 Q 版形象（kind=chibi / id chibi_ 前缀）")
	}
	if !strings.Contains(ps, "isChibiCard(row.c)") {
		t.Error("Q 版卡上要有一枚标识，否则用户不知道多出来的卡是什么")
	}
}
