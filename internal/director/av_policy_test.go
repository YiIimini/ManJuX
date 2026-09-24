package director

import (
	"strings"
	"testing"
)

// 成片听感的两条政策闸门(2026-09-24 用户报障:人员配音混乱、背景配乐一直都有)。
//
// 两条都是**提示词政策**,没有运行时信号可观测,只能靠断言钉住 ——
// 谁把它们改回去,下一个用户又得重新报一次。

// TestVoiceConsistencyRuleIsPresent ① 声线:必须要求"照抄角色卡上的 voice 原文、
// 全片同一句、两个角色不得相同、角色卡没写就按年龄性别推一次并沿用"。
//
// 用户报的「人员配音混乱不清」,来源就是这条缺失:每个镜头的声线描述由 LLM 现编,
// 同一角色在不同镜头里音色不同、两个角色还可能共用一条描述。
func TestVoiceConsistencyRuleIsPresent(t *testing.T) {
	for _, want := range []string{
		"Voice consistency",
		"copied",
		"never give two different characters the same voice",
		"reuse the exact same wording",
	} {
		if !strings.Contains(sysH3Format, want) && !strings.Contains(SystemPrompt, want) {
			t.Errorf("导演提示词缺少声线一致性规则(%q)—— 这正是「人员配音混乱」的来源", want)
		}
	}
}

// TestMusicDefaultsToNone ② 配乐:默认 N/A,只在真正需要的镜头才写。
//
// 旧文案在 schema 里给了具体乐器示例("Guzheng and low strings, slow tempo…"),
// 于是 LLM 几乎每镜都配乐 —— 用户听到的就是"背景配乐一直都有"。
func TestMusicDefaultsToNone(t *testing.T) {
	if !strings.Contains(SystemPrompt, `Write "N/A" as the DEFAULT`) {
		t.Error("配乐规则必须把 N/A 设为默认 —— 否则 LLM 每镜都配乐")
	}
	if !strings.Contains(SystemPrompt, "must not be permanently scored") {
		t.Error("硬约束里必须写明「不能全片一直有配乐」")
	}
	// 旧的具体乐器示例不该再作为默认示范出现(它会强烈暗示"要写音乐")
	if strings.Contains(SystemPrompt, "Guzheng and low strings, slow tempo, sparse rhythm, gradually building dynamics") {
		t.Error("配乐 schema 里不该再留「照抄这个例子」的具体乐器示范")
	}
}

// TestOnScreenTextExclusionIsMandatory ③ 画面文字:exclusions 不能再是"例如"。
//
// 旧文案是 `e.g. "No on-screen text, ..."` —— 写不写全凭自觉,于是招牌/牌匾/书页上的字
// 被模型自己发明,渲染成错字(用户 2026-09-24 报「视频渲染中的文字有时候出现错误的字」)。
func TestOnScreenTextExclusionIsMandatory(t *testing.T) {
	for _, want := range []string{
		"MANDATORY exclusions sentence",
		"no on-screen text",
		"no signage or plaques",
		"render",
		"verbatim",
	} {
		if !strings.Contains(strings.ToLower(SystemPrompt), strings.ToLower(want)) {
			t.Errorf("分镜提示词缺少画面文字条款(%q)", want)
		}
	}
	if strings.Contains(SystemPrompt, `in prose: e.g. "No on-screen text`) {
		t.Error("exclusions 不该再是「例如」的语气(那等于不要求)")
	}
}
