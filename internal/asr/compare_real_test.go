package asr

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// 夹具 = **真机第01集 13 镜的 ASR 转写原文**（testdata/shot_0NN.asr.txt，从渲染产物
// clips/shot_0NN.mp4 转出来的原文件，一个字没改），配上同一集 plan/plan.json 里的台词原文。
//
// 【为什么夹具必须照真实形态写】这份夹具咬过我三处：① 只按整句比时，镜 1 一整段里塞了 5 个小句，
// 相似度全是 0；② 光看对齐片段内部，镜 4 的第二遍接在第一遍后面 ⇒ 一条假「自造」告警；
// ③ 汉字数字不折算时，镜 4 的「U-80001」被写成「右八零零一」⇒ 念过的台词被判成漏念。
// 换个"两三句意思一下"的夹具，这三处一个都不会露头。
func realShotPlan() map[int][]string {
	return map[int][]string{
		1:  {"重新检测到生命反应。", "异动来源，硬件钢印U-80001。"},
		2:  {"该单元不在名册上。", "名册最后一行是多少。"},
		3:  {"80000。", "读我的钢印。硬件不认名册。"},
		4:  {"钢印U-80001。", "已读取。无对应档位。"},
		5:  {"下一行是空的。连一道格线都没留。"},
		6:  {"把钢印接回去。", "该钢印不在名册内。"},
		7:  {"硬件不认名册。", "自检窗口，七十二标准时。"},
		8:  {"U-79996。", "U-79997。"},
		9:  {"U-79998。"},
		10: {"收到。"},
		11: {"她不知道自己为什么要挂上它。"},
		12: nil,
		13: {"谁烧的门。外面还有活物吗。"},
	}
}

func loadRealShot(t *testing.T, id int) *Result {
	t.Helper()
	r, err := Parse(filepath.Join("testdata", fmt.Sprintf("shot_%03d.asr.txt", id)))
	if err != nil {
		t.Fatalf("解析真机转写夹具失败: %v", err)
	}
	return r
}

// TestCompareRealShots 用真机 13 镜的对账结果钉住判据。
//
// 【期望值是怎么定的】不是"算法输出什么就记什么"，而是一条条对着音轨能验证的事实：
//
//	镜 1  音轨多出「没禁条」（两句台词之间凭空多出一句话）
//	镜 2  干净（两句台词都念对）
//	镜 3  第一句「80000。」一次都没念 + 多出一整串「跟强我的对数还修戴眼影一下,也续点倾向」
//	镜 4  第二句「已读取。无对应档位。」被念了两遍
//	镜 5  干净（「格线」被听成「隔线」，同音字不算缺陷）
//	镜 6  「该钢印不在名册内。」两遍
//	镜 7  「自检窗口，七十二标准时。」三遍
//	镜 8  音轨里是「优势79997 优势7999例 优势799997」—— 编号被念成一团（2 句台词的编号都出现了 3 段相像的话）
//	镜 9  干净（「U-79998」被写成「游戏期99998」，同一类编号听写差异，只把相似度当数据摆出来）
//	镜 10 干净
//	镜 11 多出一串「图墙白头圣呢﹔裙尾便宜着人」
//	镜 12 计划里本就没台词、音轨里也没人说话（segs=0）⇒ 什么都不报
//	镜 13 干净
func TestCompareRealShots(t *testing.T) {
	type want struct {
		kinds    []string // 该镜应有的结论（按顺序）；空 = 干净
		contains []string // 命中时报告里必须出现的原文片段（证明引的是 ASR 原文）
	}
	cases := map[int]want{
		1:  {kinds: []string{"extra"}, contains: []string{"没禁条"}},
		2:  {},
		3:  {kinds: []string{"missing", "extra"}, contains: []string{"80000", "跟强我的对数还修戴眼影一下,也续点倾向"}},
		4:  {kinds: []string{"repeat"}, contains: []string{"出现了 2 段与这句相像的话"}},
		5:  {},
		6:  {kinds: []string{"repeat"}, contains: []string{"出现了 2 段与这句相像的话"}},
		7:  {kinds: []string{"repeat"}, contains: []string{"出现了 3 段与这句相像的话"}},
		8:  {kinds: []string{"repeat", "repeat"}, contains: []string{"出现了 3 段与这句相像的话"}},
		9:  {},
		10: {},
		11: {kinds: []string{"extra"}, contains: []string{"图墙白头圣呢"}},
		12: {},
		13: {},
	}
	plan := realShotPlan()
	for id := 1; id <= 13; id++ {
		id := id
		t.Run(fmt.Sprintf("shot_%03d", id), func(t *testing.T) {
			v := Compare(plan[id], loadRealShot(t, id))
			var kinds []string
			var detail strings.Builder
			for _, f := range v.Findings {
				kinds = append(kinds, f.Kind)
				detail.WriteString(f.Kind + " " + f.Line + " | " + f.Detail + "\n")
			}
			w := cases[id]
			if strings.Join(kinds, ",") != strings.Join(w.kinds, ",") {
				t.Fatalf("结论不符: 得到 %v，期望 %v\n%s", kinds, w.kinds, detail.String())
			}
			for _, c := range w.contains {
				if !strings.Contains(detail.String(), c) {
					t.Fatalf("报告里必须出现 %q（引原文），实际:\n%s", c, detail.String())
				}
			}
		})
	}
}

// TestCompareLineDataIsDataNotVerdict 逐句相似度只是**数据**：同音字差异（镜 5 格线/隔线、
// 镜 13 标点）不该产生任何结论，而相似度本身要如实报出来供人判断。
func TestCompareLineDataIsDataNotVerdict(t *testing.T) {
	plan := realShotPlan()
	for _, id := range []int{5, 13} {
		v := Compare(plan[id], loadRealShot(t, id))
		if len(v.Findings) != 0 {
			t.Fatalf("镜 %d 不该有结论（同音字/标点差异是噪声），实际: %+v", id, v.Findings)
		}
		if len(v.Lines) != len(plan[id]) {
			t.Fatalf("镜 %d 应当逐句给出数据，得到 %d 句", id, len(v.Lines))
		}
		for _, l := range v.Lines {
			if l.Sim < 0.8 {
				t.Fatalf("镜 %d 的「%s」相似度只有 %.2f —— 这句是念对的（镜 5 隔线/镜 13 标点），判据不该压到这么低", id, l.Line, l.Sim)
			}
		}
	}
	// 镜 3 的「80000。」确实没念：相似度 0，且只报一条结论（漏念），不重复报。
	v := Compare(plan[3], loadRealShot(t, 3))
	if len(v.Lines) != 2 || v.Lines[0].Sim != 0 || v.Lines[0].Times != 0 {
		t.Fatalf("镜 3 第一句的数据应为 sim=0 times=0，实际 %+v", v.Lines)
	}
}

// TestCompareBoundaries 边界：没验过不报、计划为空而音轨有话说要报、整条音轨没声音要报漏念。
func TestCompareBoundaries(t *testing.T) {
	// ① 没有 ASR 结果 ⇒ 什么都不报（"没验过"不许被当成"合格"，也不许被当成"有问题"）。
	if v := Compare([]string{"收到。"}, nil); !v.Empty() {
		t.Fatalf("没有 ASR 结果时不该有任何结论: %+v", v)
	}
	// ② 计划里没台词、音轨里却有人说话 ⇒ 计划外的话（真机镜 12 是 segs=0，反过来才要报）。
	v := Compare(nil, &Result{Segments: []Segment{{Start: 0, End: 3, Text: "我不该说话。"}}})
	if len(v.Findings) != 1 || v.Findings[0].Kind != "extra" {
		t.Fatalf("无台词镜里有语音应当报 extra: %+v", v.Findings)
	}
	// ③ 计划有台词、音轨一个字没有 ⇒ 逐句漏念。
	v = Compare([]string{"收到。", "该钢印不在名册内。"}, &Result{})
	if len(v.Findings) != 2 || v.Findings[0].Kind != "missing" || v.Findings[1].Kind != "missing" {
		t.Fatalf("音轨无语音时应逐句报 missing: %+v", v.Findings)
	}
	// ④ 计划与音轨都为空（真机镜 12）⇒ 什么都不报。
	if v := Compare(nil, loadRealShot(t, 12)); !v.Empty() {
		t.Fatalf("镜 12（计划无台词、音轨无声）不该有结论: %+v", v)
	}
}

// TestDisplayKeepsOriginalText 折算只用于比对：报告里引的必须是 ASR 原文。
//
// 真机镜 3 的音轨原文是「…戴眼影一下,也续点倾向。」，折算会把「一」写成「1」；
// 真机镜 4 的原文是「刚印右八零零一」，折算会写成「刚印右8001」。用户是照着报告去听音轨的，
// 报告里出现折算产物（「眼影1下」）就等于让人拿着一句不存在的话去找声音。
func TestDisplayKeepsOriginalText(t *testing.T) {
	raw := "刚印右八零零一﹑以读曲,无对应档位。"
	n := normalizeIdx(raw)
	if n.s != "刚印右8001以读曲无对应档位" {
		t.Fatalf("比对串应折算汉字数字，实际 %q", n.s)
	}
	if got := n.rawText(); got != raw {
		t.Fatalf("原文应逐字保留，实际 %q", got)
	}
	// 整段显示（含句中标点）。**末尾的句号不在内**：区间到最后一个被比较的字为止，
	// 它后面的标点不属于任何「说出来的字」—— 报告里多一个句号没有意义，少一个字才有。
	if got := n.display(0, len([]rune(n.s))); got != "刚印右八零零一﹑以读曲,无对应档位" {
		t.Fatalf("display 应还原原文（含句中标点），实际 %q", got)
	}
	// 局部显示：比对串的第 4~6 个字是「0,0,1」（原文是「零,零,一」）—— 折算了照样能指回原文。
	if got := n.display(4, 7); got != "零零一" {
		t.Fatalf("display 局部应给出原文汉字数字，实际 %q", got)
	}
}

// TestRepeatNeedsWholeLineBoundary 「重复」的窗口必须首尾都落在台词上（真机镜 1 的血泪）：
// 只按相似度取窗时，「没禁条」的后一个字「条」会与下一句台词凑出 0.64 的相似度 ⇒
// 自造句被截成「没禁」两个字 ⇒ 短于告警下限 ⇒ 这条真缺陷会被静默丢掉。
func TestRepeatNeedsWholeLineBoundary(t *testing.T) {
	key := "异动来源硬件钢印u80001" // 归一化后的镜 1 第二句台词
	if sim := similarity(key, "条易动来源硬件刚硬u美001"); sim < simHit {
		t.Fatalf("夹具前提变了：这一窗的相似度 %.2f 已低于 simHit，本用例不再覆盖错位窗口", sim)
	}
	if windowMatches(key, "条易动来源硬件刚硬u美001") {
		t.Fatal("错位一个字（多吃了「条」）的窗口不该被认成台词")
	}
	if !windowMatches(key, "易动来源硬件刚硬u美001") {
		t.Fatal("同音字替换（异/易、钢/刚、印/硬）的窗口应当被认成台词")
	}
}
