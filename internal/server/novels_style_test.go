package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// novels_style_test.go 渲染风格的**裁决**与**平台自有行同步**（2026-10-09）。
//
// 用户原话：「小说库导入视频制作时，用户需要选择对应的渲染风格，目前所有视频渲染风格都是一样的」。
// 这两条是这件事的机检：裁决必须确定性（用户选 > 立项 > LLM），同步必须幂等且只动平台自己那一行。

// TestResolveImportStyle 导入制作时的风格裁决（纯函数，直接喂闸门本体）。
func TestResolveImportStyle(t *testing.T) {
	// 立项 art_style 的真机形态：技能侧写的一串英文风格（《我捡的团子》那本就是这个位置）
	const brief = "GUOFENG ANIMATED-FEATURE ART DIRECTION, photoreal 3D surface"

	// ① 用户勾了预设（可多选）+ 附加要求 ⇒ 它就是权威串，且**覆盖**立项串
	got := resolveImportStyle(brief, []string{"guoman_2d", "ink_wash"}, "夜景不要霓虹")
	if got.From != "user" {
		t.Fatalf("勾了预设就必须按用户算，实际来源 %q", got.From)
	}
	if got.Style == brief || !strings.Contains(got.Style, "CHINESE 2D ANIME STYLE") ||
		!strings.Contains(got.Style, "INK WASH") || !strings.Contains(got.Style, "夜景不要霓虹") {
		t.Fatalf("权威风格串应由「预设 + 附加要求」拼成并覆盖立项串，实际：%q", got.Style)
	}
	if len(got.Unknown) != 0 {
		t.Fatalf("这两个 key 都是平台自己的预设，不该被判未知：%v", got.Unknown)
	}
	if len(got.Styles) != 2 || got.Styles[0] != "guoman_2d" {
		t.Fatalf("选中的 key 要留着写进项目参数，实际：%v", got.Styles)
	}

	// ② 没勾、也没写附加要求 ⇒ 沿用立项 art_style（改动前的老行为，保持不变）
	got = resolveImportStyle(brief, nil, "")
	if got.From != "brief" || got.Style != brief {
		t.Fatalf("没选就该沿用立项串，实际 %q/%q", got.From, got.Style)
	}

	// ③ 都没有 ⇒ 空（由分镜会话自定，之后仍被「全局风格:」锁定）
	got = resolveImportStyle("", nil, "   ")
	if got.From != "llm" || got.Style != "" {
		t.Fatalf("都没有时不该编一个风格串出来，实际 %q/%q", got.From, got.Style)
	}

	// ④ 未知 key：如实点名，**不吞掉**同批里认得的项
	got = resolveImportStyle("", []string{"guoman_2d", "made_up_style"}, "")
	if got.From != "user" || len(got.Unknown) != 1 || got.Unknown[0] != "made_up_style" {
		t.Fatalf("未知 key 要点名且只点名它，实际：%v", got.Unknown)
	}
	if !strings.Contains(got.Style, "CHINESE 2D ANIME STYLE") {
		t.Fatalf("未知 key 不该把整批选择吞掉，实际：%q", got.Style)
	}

	// ⑤ 只写了自由文本（没勾预设）也算"用户明确要求" ⇒ 生效
	got = resolveImportStyle(brief, nil, "冷色调，35mm，雨夜氛围")
	if got.From != "user" || got.Style != "冷色调，35mm，雨夜氛围" {
		t.Fatalf("只给附加要求也应生效（用户明确说了），实际 %q/%q", got.From, got.Style)
	}
}

// TestSetGlobalStyleLine 平台自有的「全局风格:」行同步：幂等、只动那一行、空风格不动。
func TestSetGlobalStyleLine(t *testing.T) {
	// 真机形态：建集时写的抬头 + 那一行 + 空行 + 【本集剧情】（见 novels_split.go 的 createSeg）
	const src = "《我捡的团子，暖了三千人的船》系列短视频 第01 集《霜里刨出个活物》\n" +
		"全局风格:OLD STYLE STRING\n\n【本集剧情】开场爆点：他从冰里刨出一个团子。"

	next, changed := setGlobalStyleLine(src, "NEW STYLE STRING")
	if !changed {
		t.Fatal("该改的没改：用户选了风格，那一行必须跟着走（否则 enforceStyleLock 会按旧串覆写回去）")
	}
	if strings.Contains(next, "OLD STYLE STRING") {
		t.Fatalf("旧串没被换掉：%q", next)
	}
	if !strings.Contains(next, globalStyleLabel+"NEW STYLE STRING") {
		t.Fatalf("没有写成新串：%q", next)
	}
	if !strings.Contains(next, "《我捡的团子，暖了三千人的船》系列短视频 第01 集") ||
		!strings.Contains(next, "【本集剧情】开场爆点：他从冰里刨出一个团子。") {
		t.Fatalf("只许动「全局风格:」那一行，别的字一个都不许碰：%q", next)
	}

	// 幂等：已经是这个值 ⇒ 不产生无意义写入（参数抽屉会被反复保存）
	if again, c2 := setGlobalStyleLine(next, "NEW STYLE STRING"); c2 || again != next {
		t.Fatal("同一个风格串重复同步必须是无写入的幂等")
	}

	// 没有这一行（技能侧导入的历史包 / 手改过的输入）⇒ 插在第一行之后，其余原样
	const noLine = "《某剧》第01集\n【本集剧情】…"
	out, c3 := setGlobalStyleLine(noLine, "NEW STYLE STRING")
	if !c3 {
		t.Fatal("缺行时应当补上（否则用户选的风格在这一集上不生效）")
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 3 || lines[0] != "《某剧》第01集" ||
		lines[1] != globalStyleLabel+"NEW STYLE STRING" || lines[2] != "【本集剧情】…" {
		t.Fatalf("插行位置不对（必须在抬头之后、其余原样）：%q", out)
	}

	// 空风格 ⇒ 原样返回、changed=false（绝不许把那一行改成空值/删掉）
	if s, c := setGlobalStyleLine(src, "   "); c || s != src {
		t.Fatal("风格为空时不许动输入（把\"取消选择\"解释成\"清空风格\"会毁掉原有的风格串）")
	}

	// 读回的原值必须与写入口同一判据（播报"原值是什么"靠它）
	if v := globalStyleValue(src); v != "OLD STYLE STRING" {
		t.Fatalf("读回原值不对：%q", v)
	}
}

// TestGlobalStyleLabelMatchesDirector 「全局风格:」这个标签是**跨包契约**：
// server 写它、director 读它（`extractGlobalStyle` → `enforceStyleLock` 逐镜覆写 plan.Style）。
// 两边任一改动而另一边没跟 ⇒ 用户选的风格静默失效（这正是本轮要修的那类"改了没用"）。
func TestGlobalStyleLabelMatchesDirector(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "director", "director.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, globalStyleLabel) {
		t.Fatalf("director 里找不到 %q —— 写入口与读入口的标签已经不一致了", globalStyleLabel)
	}
	if !strings.Contains(src, "func extractGlobalStyle(") || !strings.Contains(src, "func enforceStyleLock(") {
		t.Fatal("director 侧读全局风格/覆写 plan.Style 的两个函数不见了（风格锁定链断了）")
	}
}
