package director

import (
	"encoding/json"
	"strings"
	"testing"
)

// jsonx.go 的契约测试:LLM 输出的"提取 / 补全 / 诊断"三件事必须稳。
//
// 起因(2026-09-24 用户 48 集产线卡在第 1 集):规划轮失败只回一句
// "输出解析失败(缺少 title/segments)",现场无法复盘。修法是把提取做扎实、
// 截断尽力补全、失败时给出可诊断的原因 —— 这三条都用这里的用例钉住。

func TestExtractJSONObject(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		want  string
		wantK bool // 期望 ok=true
	}{
		{
			name:  "纯对象",
			raw:   `{"title":"砖","segments":[{"from":1,"to":3,"brief":"x"}]}`,
			want:  `{"title":"砖","segments":[{"from":1,"to":3,"brief":"x"}]}`,
			wantK: true,
		},
		{
			name:  "围栏包裹",
			raw:   "```json\n{\"title\":\"砖\",\"segments\":[{\"from\":1,\"to\":3}]}\n```",
			want:  `{"title":"砖","segments":[{"from":1,"to":3}]}`,
			wantK: true,
		},
		{
			name:  "前面有说明文字",
			raw:   "好的,这是方案:\n{\"title\":\"砖\",\"segments\":[{\"from\":1,\"to\":3}]}\n希望有帮助。",
			want:  `{"title":"砖","segments":[{"from":1,"to":3}]}`,
			wantK: true,
		},
		{
			name:  "说明里带花括号(旧实现的死穴)",
			raw:   "格式是 {…} 这样的对象,内容如下:\n{\"title\":\"砖\",\"segments\":[{\"from\":1,\"to\":3}]}",
			want:  `{"title":"砖","segments":[{"from":1,"to":3}]}`,
			wantK: true,
		},
		{
			name:  "字符串里有花括号",
			raw:   `{"title":"砖{不是结构}","segments":[{"from":1,"to":3}]}`,
			want:  `{"title":"砖{不是结构}","segments":[{"from":1,"to":3}]}`,
			wantK: true,
		},
		{
			name:  "被截断(不配平)",
			raw:   `{"title":"砖","segments":[{"from":1,"to":3,"brief":"x"},{"from":4,"to":6,"bri`,
			wantK: false,
		},
		{
			name:  "没有对象",
			raw:   "抱歉,我无法完成这个请求。",
			wantK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractJSONObject(tc.raw)
			if ok != tc.wantK {
				t.Fatalf("ok=%v,期望 %v(取到 %q)", ok, tc.wantK, got)
			}
			if tc.want != "" && got != tc.want {
				t.Errorf("取到 %q,期望 %q", got, tc.want)
			}
			if ok && !json.Valid([]byte(repairJSONStrings(got))) {
				t.Errorf("返回的片段不是合法 JSON:%q", got)
			}
		})
	}
}

func TestCloseTruncatedJSON(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantOK   bool
		wantSubs []string // 补全结果里必须保留的内容
	}{
		{
			name:     "段数组被截断",
			in:       `{"title":"砖","segments":[{"from":1,"to":3,"brief":"a"},{"from":4,"to":6,"br`,
			wantOK:   true,
			wantSubs: []string{`"title":"砖"`, `{"from":1,"to":3,"brief":"a"}`},
		},
		{
			name:     "对象整体被截断在字符串里",
			in:       `{"title":"三万六千块砖","segments":[{"from":1,"to":5,"brief":"开场;`,
			wantOK:   false, // 只闭合了一个元素的边界都没有 → 不能瞎补
			wantSubs: nil,
		},
		{
			name:     "缺失闭合括号",
			in:       `{"title":"砖","segments":[{"from":1,"to":3,"brief":"a"}`,
			wantOK:   true,
			wantSubs: []string{`"brief":"a"`},
		},
		{
			name:   "本来就配平",
			in:     `{"title":"砖","segments":[]}`,
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := closeTruncatedJSON(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v,期望 %v(得到 %q)", ok, tc.wantOK, got)
			}
			if !ok {
				return
			}
			if !json.Valid([]byte(repairJSONStrings(got))) {
				t.Fatalf("补全结果仍不是合法 JSON:%q", got)
			}
			for _, sub := range tc.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("补全结果丢了 %q:%q", sub, got)
				}
			}
		})
	}
}

func TestParsePlanHeadDiagnostics(t *testing.T) {
	t.Run("合法", func(t *testing.T) {
		head, _, err := parsePlanHead(`{"title":"砖","segments":[{"from":1,"to":3,"brief":"a"}]}`)
		if err != nil {
			t.Fatalf("不该失败: %v", err)
		}
		if head.Title != "砖" || len(head.Segments) != 1 {
			t.Errorf("解析结果不对:%+v", head)
		}
	})
	t.Run("缺 title 要指名", func(t *testing.T) {
		_, _, err := parsePlanHead(`{"segments":[{"from":1,"to":3,"brief":"a"}]}`)
		if err == nil || !strings.Contains(err.Error(), "缺少 title") {
			t.Errorf("应报缺少 title,实得 %v", err)
		}
	})
	t.Run("缺 segments 要指名", func(t *testing.T) {
		_, _, err := parsePlanHead(`{"title":"砖"}`)
		if err == nil || !strings.Contains(err.Error(), "缺少 segments") {
			t.Errorf("应报缺少 segments,实得 %v", err)
		}
	})
	t.Run("纯文字要报没找到 JSON", func(t *testing.T) {
		_, _, err := parsePlanHead("我先想想这个故事该怎么拍。")
		if err == nil || !strings.Contains(err.Error(), "未找到 JSON 对象") {
			t.Errorf("应报未找到 JSON 对象,实得 %v", err)
		}
	})
}

// TestParseShotsBatchTruncated 分镜批次被截断时,补全后应能取回完整的那些镜头。
func TestParseShotsBatchTruncated(t *testing.T) {
	raw := `{"shots":[{"id":3,"h3_prompt":"A"},{"id":4,"h3_prompt":"B"},{"id":5,"h3_pro`
	shots, err := parseShotsBatch(raw, 3, 7)
	if err != nil {
		t.Fatalf("截断的批次应能补全出前两镜: %v", err)
	}
	if len(shots) != 2 || shots[0].ID != 3 || shots[1].ID != 4 {
		t.Errorf("补全结果不对:%+v", shots)
	}
}
