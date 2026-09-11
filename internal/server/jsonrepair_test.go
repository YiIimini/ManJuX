// jsonrepair_test.go 回归:LLM 输出 JSON 中的未转义台词引号(2026-09-11
// 实证:第 6/13 段拆集 brief 照抄对话 "……谢了。""下周日,面摊老位置。"
// 半角引号未转义,整体与逐集解析全灭)。样例取自真实故障输出尾部。
package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// 用户实炸样例:()围栏 + 多行 pretty JSON + brief 内连续未转义半角引号。
const dialogueQuoteSample = `({
  "book": "面摊",
  "style": "anime illustration",
  "episodes": [
    {
      "ep": 6,
      "title": "毕业照",
      "chapters": "第17章~第18章",
      "brief": "他指腹摸过那道重新绣好的金线,针脚细密。"……谢了。""下周日,面摊老位置。师弟,面钱记你账上。"婚后第三天,穷奇快递站转来一封没有寄件人的信。纪野拆开,是山海市第一学校的毕业照——追风站在第一排正中间,笑得像朵花。"
    }
  ]
})`

func TestRepairUnescapedDialogueQuotesWholeDoc(t *testing.T) {
	var pl struct {
		Book     string `json:"book"`
		Style    string `json:"style"`
		Episodes []struct {
			Ep    int    `json:"ep"`
			Title string `json:"title"`
			Brief string `json:"brief"`
		} `json:"episodes"`
	}
	if err := jsonUnmarshalStrict(trimJSON(repairJSONStrings(dialogueQuoteSample)), &pl); err != nil {
		t.Fatalf("整体解析仍失败: %v", err)
	}
	if len(pl.Episodes) != 1 || pl.Episodes[0].Ep != 6 {
		t.Fatalf("集数解析错误: %+v", pl)
	}
	brief := pl.Episodes[0].Brief
	if !strings.Contains(brief, `谢了。""下周日`) || !strings.Contains(brief, "笑得像朵花") {
		t.Fatalf("brief 内容丢失: %q", brief)
	}
}

func TestRepairUnescapedDialogueQuotesExtract(t *testing.T) {
	eps := extractEpisodes(dialogueQuoteSample)
	if len(eps) != 1 {
		t.Fatalf("逐集提取应得 1 集,实得 %d", len(eps))
	}
	if v, ok := eps[0]["brief"].(string); !ok || !strings.Contains(v, "师弟,面钱记你账上") {
		t.Fatalf("逐集提取 brief 内容丢失: %v", eps[0])
	}
}

// 正常 JSON(无杂引号)不得被修复逻辑改坏。
func TestRepairKeepsNormalJSON(t *testing.T) {
	raw := `{"book":"书","style":"s","episodes":[{"ep":1,"title":"t","chapters":"第1章","brief":"普通内容,没有引号问题。"},{"ep":2,"title":"t2","chapters":"第2章","brief":""}]}`
	var m map[string]any
	if err := json.Unmarshal([]byte(repairJSONStrings(raw)), &m); err != nil {
		t.Fatalf("正常 JSON 被改坏: %v", err)
	}
	if eps := extractEpisodes(raw); len(eps) != 2 {
		t.Fatalf("正常 JSON 逐集提取应得 2 集,实得 %d", len(eps))
	}
}

// 已转义引号、空字符串、内容引号+半角逗号混排。
func TestRepairMixedQuotes(t *testing.T) {
	raw := `{"a":"","b":"他说\"好\",然后","c":"他喊"停",然后走了","d":"尾"}`
	var m map[string]any
	if err := json.Unmarshal([]byte(repairJSONStrings(raw)), &m); err != nil {
		t.Fatalf("混排引号修复失败: %v", err)
	}
	if m["b"] != `他说"好",然后` {
		t.Fatalf("b 内容错误: %q", m["b"])
	}
	if m["c"] != `他喊"停",然后走了` {
		t.Fatalf("c 内容错误: %q", m["c"])
	}
}

// 过度转义收尾(2026-09-11 16:17 实证:第 2/6 段,引号禁令提示词诱发,
// LLM 把字符串收尾定界引号写成 \",字符串永不闭合,整体+逐集解析全灭)。
func TestRepairOverescapedTerminator(t *testing.T) {
	// 用户实炸尾部形态:…「按流程办。」\"}]}"})
	raw := `{"book":"旧账","style":"s","episodes":[
{"ep":1,"title":"一","chapters":"第1章","brief":"开局。"},
{"ep":2,"title":"二","chapters":"第2章","brief":"雷九把木匣盖上:「办。」「按流程办。」\"}]}"})`
	eps := extractEpisodes(raw)
	if len(eps) != 2 {
		t.Fatalf("逐集提取应抢救 2 集(收尾过度转义),实得 %d", len(eps))
	}
	if v, _ := eps[1]["brief"].(string); !strings.Contains(v, "「按流程办。」") {
		t.Fatalf("第2集 brief 内容丢失: %q", v)
	}
	// 已有的合法转义 \\ 不受影响:路径 a\"b 后跟普通文字仍按内容保留
	keep := `{"a":"x\"y 尾"}`
	var m map[string]any
	if err := json.Unmarshal([]byte(repairJSONStrings(keep)), &m); err != nil {
		t.Fatalf("合法转义被改坏: %v", err)
	}
}
