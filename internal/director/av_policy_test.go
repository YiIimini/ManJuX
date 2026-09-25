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
		"Speakers (official",
		"stable ID (S1)",
		"SAME id across shots",
		"<scenetrans>",
		"<cutoff>",
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
	if !strings.Contains(SystemPrompt, `Write "N/A" when there is no non-diegetic music`) {
		t.Error("配乐规则必须按官方 §4.7:没有非叙事配乐就写 N/A")
	}
	if !strings.Contains(SystemPrompt, "do not score every shot") {
		t.Error("必须写明「不要每镜都配乐」(用户报的正是背景配乐一直都有)")
	}
	if !strings.Contains(SystemPrompt, "DIEGETIC") {
		t.Error("必须区分叙事内音乐(角色听得到的)与非叙事配乐")
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
	// 官方 §4.5:可见文字必须用英文双引号逐条写出、逐字保留;引号之外不得有文字
	for _, want := range []string{
		"ON-SCREEN TEXT (official rule)",
		"ENGLISH DOUBLE QUOTATION MARKS",
		"verbatim",
		"garbled",
	} {
		if !strings.Contains(strings.ToLower(SystemPrompt), strings.ToLower(want)) {
			t.Errorf("分镜提示词缺少画面文字条款(%q)", want)
		}
	}
	if strings.Contains(SystemPrompt, `e.g. "No on-screen text`) {
		t.Error("exclusions 不该再停留在「例如」的语气(等于不要求)")
	}
}

// TestDialoguePunctuationAndUnclear ④ 台词的字词与标点分开管(官方 §4.4 + §5.4)。
//
// 我上一轮写的是"保留原标点",与官方**相反**:官方要求字词逐字保留,而标点统一成基本
// 书写符号(去波浪号/emoji/装饰标点、句末以 . ? ! 收尾),听不清的片段写 [unclear] 不许猜。
// 标点杂乱与硬猜出来的字,都是"配音听着不对"的来源。
func TestDialoguePunctuationAndUnclear(t *testing.T) {
	for _, want := range []string{
		"official §5.4",
		"[unclear]",
		"normalise it to the basic written marks",
		"before </d>",
	} {
		if !strings.Contains(SystemPrompt, want) {
			t.Errorf("分镜提示词缺少台词标点条款(%q)", want)
		}
	}
	if strings.Contains(SystemPrompt, "VERBATIM inside <d> tags with original punctuation") {
		t.Error("「保留原标点」与官方 §5.4 相反,不得再出现")
	}
}

// TestSoundscapeSentencesFollowOfficialRange ⑤ 音景句数必须是官方区间 1-4 / 1-3,
// "成片不能吵"只能写成**取区间低端**,不能私自把官方区间改小 —— 那是我上一轮的自造口径。
func TestSoundscapeSentencesFollowOfficialRange(t *testing.T) {
	if !strings.Contains(SystemPrompt, "1-4 English sentences in ONE continuous paragraph") {
		t.Error("overall_soundscape 必须写官方 §4.6 的 1-4 句")
	}
	if !strings.Contains(SystemPrompt, "1-3 English sentences") {
		t.Error("non_diegetic_music 必须写官方 §4.7 的 1-3 句")
	}
	if !strings.Contains(SystemPrompt, "LOW end of the official 1-4 sentence range") {
		t.Error("「别吵」必须表述为取官方区间低端,而不是另一个区间")
	}
	if strings.Contains(SystemPrompt, "keep overall_soundscape to 1-2") {
		t.Error("自造的 1-2 句区间不得再出现(与官方 1-4 冲突)")
	}
	// 官方 §4.6:台词/演唱/叙事内音乐已经在描述段里,音景不得重复
	if !strings.Contains(SystemPrompt, "never repeat them here") {
		t.Error("音景必须写明不得重复台词/演唱/叙事内音乐(官方 §4.6)")
	}
}
