package server

import (
	"strings"
	"testing"
)

// frontend_qc_visibility_test.go 质检结论的**界面可见性**闸门。
//
// 【为什么这条是 P0】（2026-10-08 用户：「好好再深度检查排查问题，别总是我在使用的时候让我发现啊」）
//
// 后端早就在算这些结论（`qc_report.json` 的 `speech`=ASR 对账 / `voice`=音色基频核对 /
// `text_detect`=画面烧字），也随 `GET /api/projects/{id}` **原样下发**到浏览器
// （handlers_project.go 的 out["qc"]）。但两个质检渲染点只画了
//
//	ok / 时长 / 暗像素 / 有音轨 / problems / vlm
//
// —— 连原有的 `warnings`（音色判负、念重了、画面烧字都写在这里）都从不渲染。结果：
// 用户报的「女的配男音」「同一句念两遍」在界面上**一个都看不到**，只有当时那次会话的
// 日志流里有（而日志重开应用就没了，还会被一键清理清掉）。算出来却不显示 = 等于没做。
//
// 判据按"漏接线的**症状**"写，而不是"某个字符串在不在"：
//   - 摘要模块缺了 skipped/checked === false 分支 ⇒ 会把"其实没验"显示成"验过且干净"；
//   - 视图不引摘要模块 ⇒ 结论只有日志里有；
//   - 视图不渲染 warnings ⇒ 逐镜告警界面上看不到。
func TestQcConclusionsAreVisibleInUI(t *testing.T) {
	src := frontendSources(t)

	helper, ok := src["qcLines.js"]
	if !ok {
		t.Fatal("frontend/src/qcLines.js 不在 —— 质检结论的界面摘要没有落脚点")
	}
	for _, want := range []struct{ needle, why string }{
		{"checked === false", "\"没验过\"与\"验过且干净\"必须分得开（与后端三态 Checked/Skipped/Error 同口径）"},
		{"skipped", "跳过（未验/不判）必须单独分支，不许并进\"通过\""},
		{"error", "失败要写成\"未判定，不当作通过\""},
		{"findings", "判负要把 findings 的 detail 摆出来（那是给人的动作指引）"},
		{"音色", "音色核对（女声卡念成男声带）要能被用户看见"},
		{"语音", "语音对账（ASR：念重了/多念了/漏念）要能被用户看见"},
		{"画面文字", "画面文字检测（模型往画面里烧字）要能被用户看见"},
	} {
		if !strings.Contains(helper, want.needle) {
			t.Errorf("qcLines.js 缺少 %s（%q）", want.why, want.needle)
		}
	}

	for _, v := range []string{"views/ProjectView.vue", "components/StageDetailDrawer.vue"} {
		body, ok := src[v]
		if !ok {
			t.Fatalf("找不到前端文件 %s", v)
		}
		if !strings.Contains(body, "qcProblemLines") {
			t.Errorf("%s 没接 qcProblemLines —— 质检结论（语音/音色/画面文字）又只活在日志里", v)
		}
		if !strings.Contains(body, "qcProblemLines(r)") {
			t.Errorf("%s 的结论行没绑定到逐镜报告对象 r", v)
		}
		if !strings.Contains(body, "warnings") {
			t.Errorf("%s 没渲染 r.warnings —— 逐镜告警（音色判负/念重了/烧字）在界面上看不到", v)
		}
	}

	// 设置页那句文案会**误导用户去做错事**（以为分镜要拿原文去改编），与新口径反着来。
	if s, ok := src["views/SettingsView.vue"]; ok {
		if strings.Contains(s, "看全文改编") {
			t.Error("设置页还把原文说成\"给分镜看全文改编\"的输入 —— 新口径是剧本为主源、原文只是素材")
		}
		if !strings.Contains(s, "素材") {
			t.Error("设置页那份\"附原文上限\"的说明要讲清原文只是素材（供核实细节），否则用户不知道该提供什么")
		}
	}

	// 局部重绘的目标取自角色卡：后端只在**顶层**下发 plan（out["plan"]），
	// 写成 pj.value.project?.plan?.characters 会永远取到 undefined ⇒ 默认目标一直是泛指的
	// "the character"（等于这个功能从没带上角色名）。
	if c, ok := src["components/ShotCard.vue"]; ok {
		if strings.Contains(c, "project?.plan") {
			t.Error("ShotCard 的局部重绘目标路径又写错了：后端只在顶层下发 plan（pj.value.plan）")
		}
	}
}
