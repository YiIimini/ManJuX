package pipeline

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 翻译会话的**输出容错**：模型常带 markdown 围栏与前后废话，解析要能扛住。
func TestParsePromptMapToleratesFencesAndChatter(t *testing.T) {
	cases := []string{
		`{"尉迟彻":"a broad-shouldered man in dark grey duty armour"}`,
		"好的，这是结果：\n```json\n{\"尉迟彻\":\"a broad-shouldered man in dark grey duty armour\"}\n```\n希望有帮助",
		"结果如下\n\n{\n  \"尉迟彻\": \"a broad-shouldered man in dark grey duty armour\"\n}\n",
	}
	for i, raw := range cases {
		m := parsePromptMap(raw)
		if m["尉迟彻"] == "" {
			t.Errorf("第 %d 个用例解析失败:%q", i, raw)
		}
	}
	// 解析不出来时返回 nil（调用方据此放弃，而不是写半截提示词进卡）
	if parsePromptMap("模型今天不想干活") != nil {
		t.Error("没有 JSON 时应返回 nil")
	}
}

// **带汉字的翻译结果一律不采用** —— 图像模型会把汉字画成乱码糊在脸上，
// 宁可用"只有年龄与头身比"的残缺提示词，也不要一张满脸乱码的定妆照。
func TestTranslateCardPromptsRejectsCJKOutput(t *testing.T) {
	// 直接验判据本身：这是 TranslateCardPrompts 里那道"宁缺勿滥"的闸。
	for _, bad := range []string{
		"一个宽肩男人，穿深灰值勤装甲",                       // 全中文
		"a broad-shouldered man, 深灰值勤装甲 armor", // 中英混排
	} {
		if project.CountCJK(bad) == 0 {
			t.Errorf("这段该被判为含汉字:%q", bad)
		}
	}
	if project.CountCJK("a broad-shouldered man in dark grey duty armour") != 0 {
		t.Error("纯英文不该被判为含汉字")
	}
}
