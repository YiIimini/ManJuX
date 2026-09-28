package project

import (
	"strings"
	"testing"
)

// `familyMatchName` 的四种情形（2026-09-29 代码健康审计 P1-7）。
//
// ## 它为什么值得一条测试
//
// 它不是判据，是**错误信息质量**部件：把「说话人不在角色表里」升级成
// 「说话人「沈砚」与卡名「沈砚·七品」不是完全一致，但家族基名一致（按 沈砚 判定为同一人）」。
//
// 而它**一行都没被测试跑过**（审计逐行核过覆盖率）。风险在于：它一旦回归
// （匹配错卡、漏匹配、误匹配），消息就**静默退化**成通用告警、或者**给出错误的卡名** ——
// 而通用那条分支本身是被覆盖的，所以没有任何测试会红。
//
// 一条"只说给人听"的逻辑，恰恰是最容易被改坏又最难被发现的。
func TestFamilyMatchName(t *testing.T) {
	plan := func(names ...string) *Plan {
		p := &Plan{}
		for i, n := range names {
			p.Characters = append(p.Characters, Character{ID: "char_" + string(rune('a'+i)), Name: n})
		}
		return p
	}

	// ① **基名命中**：说话人写的是简称，卡上是带档名的全名 → 认出是同一人
	if got := familyMatchName(plan("沈砚·七品"), "沈砚"); got != "沈砚·七品" {
		t.Errorf("该认出「沈砚」对应卡「沈砚·七品」，实际 %q", got)
	}
	// ② **同名跳过**：卡名与说话人**逐字相同**时不算"家族命中"（那本来就直接匹配上了，
	//    这里返回它反而会让上层以为"名字对不上、只是同一人"）
	if got := familyMatchName(plan("沈砚"), "沈砚"); got != "" {
		t.Errorf("同名不该走家族兜底，实际返回 %q", got)
	}
	// ③ **基名为空**：`CharFamilyBaseName` 认不出（纯符号之类）时直接返回空，
	//    不能拿整个名字去比 —— 否则会误判成"家族命中"
	if got := familyMatchName(plan("沈砚·七品"), "！@#"); got != "" {
		t.Errorf("基名认不出时该返回空，实际 %q", got)
	}
	// ④ **无命中**：表里没有同一家族的卡 → 空（上层照旧报通用告警）
	if got := familyMatchName(plan("沈砚·七品", "守钟女"), "陆青梧"); got != "" {
		t.Errorf("表里没有陆家的卡，不该命中，实际 %q", got)
	}
	// ⑤ **空名卡不参与**：cards 里有空名字的条目时不能拿它当命中（那会给出空结果或多个结果）
	if got := familyMatchName(plan("", "沈砚·七品"), "沈砚"); got != "沈砚·七品" {
		t.Errorf("空名卡该被跳过，实际 %q", got)
	}
	// ⑥ 认出的卡名必须**真的在表里**（防止"编一个名字给用户看"）
	for _, names := range [][]string{{"沈砚·七品", "守钟女"}, {"碑生（丙十七）", "陆青梧"}} {
		for _, probe := range []string{"沈砚", "碑生", "守钟", "陆青梧"} {
			got := familyMatchName(plan(names...), probe)
			if got == "" {
				continue
			}
			found := false
			for _, n := range names {
				if n == got {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("对 %q 返回了表里不存在的卡名 %q（用户会拿着一个查不到的名字去改）", probe, got)
			}
		}
	}
	// ⑦ 调用点契约的**反向确认**：它只识别、不改写 —— 函数签名返回 string 而不是 *Character
	//    （这条断言靠签名本身保证，写在这里是为了让"为什么是 string"有处可查）
	var _ func(*Plan, string) string = familyMatchName
	_ = strings.TrimSpace
}
