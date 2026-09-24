package pipeline

import (
	"strings"
	"testing"
)

// 白底化的提示词契约(用户 2026-09-24:上传图也要走 Qwen-Image-Edit 生成白底角色)。
//
// 这条路径是"改背景",不是"重画",所以它的验收点和多视角那条完全不同:
//   - 必须明说"只改背景"(否则模型会顺手美颜/重画);
//   - 必须明说"同介质"(上传的是真人照片就还他照片 —— 这里**刻意不加**动画介质块,
//     加了等于把真人改成赛璐璐,那是另一种"改内容");
//   - 必须禁止加道具/文字/阴影(白底棚拍场景最常出的错)。
func TestWhiteBgPromptContract(t *testing.T) {
	p := WhiteBgPrompt("鲁青砚", "三十岁,灰布长衫")
	for _, want := range []string{
		"pure white (#FFFFFF)",
		"same face",
		"BACKGROUND CHANGE only",
		"do NOT restyle",
		"keep it a photograph", // 同介质锁
		"do NOT add or remove any object",
		"do NOT add text, watermark, logo or frame",
		"do NOT add a cast shadow",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("白底化指令缺少 %q:\n%s", want, p)
		}
	}
	// 刻意**不带**动画介质块:上传图不该被改成赛璐璐
	if strings.Contains(p, MediumMarker) || strings.Contains(p, RenderMediumMarker) {
		t.Error("白底化不该注入动画介质块 —— 那是「重画」,不是「换背景」")
	}
	// 身份补充要经过 compactIdentity(剥掉面部形容词),并保留族群锚点。
	// 判据只作用于**补充那一段**:提示词主体本来就有 "same face / facial features"
	// 这类"保持不变"的条款(整串搜会误报),而 "chinese" 里还含 "chin" 这个子串。
	p2 := WhiteBgPrompt("老王", "a Chinese man with rosy cheeks and a long chin")
	const hintPrefix = "(The subject is: "
	i := strings.Index(p2, hintPrefix)
	if i < 0 {
		t.Fatalf("应带身份补充段:%s", p2)
	}
	hint := strings.ToLower(p2[i+len(hintPrefix):])
	for _, bad := range []string{"rosy", "cheeks", "long chin"} {
		if strings.Contains(hint, bad) {
			t.Errorf("身份补充不该含 %q(长相以参考图为准):%q", bad, hint)
		}
	}
	if !strings.Contains(hint, "chinese") {
		t.Error("族群是身份锚,应保留")
	}
	// 没有名字/描述时不该出现空括号
	if p3 := WhiteBgPrompt("", ""); strings.Contains(p3, "(The subject is:") {
		t.Error("没有身份补充时不该留空括号")
	}
}

// TestWhiteBgNegativeBansRestyle 负面提示要覆盖"换个人/换画风/加杂物"三类。
func TestWhiteBgNegativeBansRestyle(t *testing.T) {
	n := WhiteBgNegative()
	for _, want := range []string{"another person", "different costume", "changed art style", "watermark"} {
		if !strings.Contains(n, want) {
			t.Errorf("负面提示应包含 %q,实际 %q", want, n)
		}
	}
}
