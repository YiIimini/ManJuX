package director

import (
	"os"
	"strings"
	"testing"
)

// mustReadFile 读本包源文件（守卫测试要断言"定义只有一处"，读源码是最直接的判据）。
func mustReadFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", name, err)
	}
	return string(raw)
}

// director_spec_test.go 平台直出提示词要**真的带**技能侧《平台侧生成规范》的硬口径。
//
// 2026-09-27 对着那份规范逐条审的结果：director 里**早已有**事件隔离串、建立镜头串＋禁小景别、
// 9 视角词表（含"空值合法"）、物件配额 2–4 组、闭嘴句、7.5 头身；**当时缺**的是下面这些：
//
//	① 五官词表（§3.2）：此前只要求"写清脸型眼型"，LLM 于是爱写 `large bright eyes`/`精致五官`/
//	   `绝美` 这类**尺寸词与抽象词** —— 那正是"全剧一张模板脸"的根源（同一天还报过"周廷和主角脸相似"）；
//	② 敏感词禁止表＋安全替代（§2.4）：赤足/深V/低胸/超模身材/半透/毛孔可见… 云端会拦，本地也照写；
//	③ 叙事配额与 MAX 算式（§6.4/§6.5）：首句 ≤12 s、无台词段 ≤12 s、戏眼 ≥5 s、`MAX(台词,动作)`；
//	④ 自包含（§2）：禁"同上/如上"、禁留空 —— 一镜一次生成，模型没有上一镜的记忆。
//
// 这份测试钉的就是"这四条别悄悄掉"：**改词等于换词表**，所以断言的是稳定片段而不是整句。
func TestDirectorPromptsCarrySpecRules(t *testing.T) {
	want := []string{
		// ① 五官词表：具体类型名 + 禁尺寸词
		"FACE VOCABULARY", "鹅蛋脸", "丹凤眼", "远山眉", "薄唇轻抿",
		"large eyes", "精致五官", "绝美",
		// ② 敏感词与安全替代
		"SAFE WORDING", "cloth shoes", "high collar", "opaque matte fabric",
		"seven and a half heads", "matte skin with a soft natural finish",
		// ③ 叙事配额 + MAX 算式
		"MAX(dialogue time, action time)", "12 seconds", "AT LEAST 5 seconds",
		// ④ 自包含
		"NEVER write", "同上",
		// ⑤ 角色品质条与锚表权威（§3.3/§3.1，写在五官/安全段里 ⇒ 三条提示词都带）
		"plastic or silicone skin is forbidden", "individual strands readable",
		"MUST wear shoes", "at least 4 premium materials", "Anchor sheet is the ONLY authority",
		"发型三要素",
		// ⑥ 分镜必须解除静态卡的约束（§6.9；**只在这两条写 shots 的提示词里**）
		"THE SHOT MUST MOVE", "no flutter, no dynamic action",
	}
	for name, prompt := range ShotPrompts() {
		for _, w := range want {
			if !strings.Contains(prompt, w) {
				t.Errorf("%s 缺 %q —— 技能侧生成规范的口径掉了", name, w)
			}
		}
	}
}

// TestSceneAndDynamicBlocks Scene 块只在**写 scenes 的提示词**里（分批分镜轮不产出场景卡）；
// 动态解除块只在**写 shots 的提示词**里 —— 混在一起的守卫会要求"每条提示词都有全部规则"，
// 那不是事实，也就守不住真正的缺口（见 ShotPrompts 的注释）。
func TestSceneAndDynamicBlocks(t *testing.T) {
	sceneMarkers := []string{
		"FOUR ELEMENTS", "at least 2 of these five", "A-versus-B", "ALLOWED decay",
		"foreground / midground / background",
	}
	if !strings.Contains(SystemPrompt, "## SCENE CARDS") {
		t.Fatal("单轮直出提示词里应有场景卡段")
	}
	for _, m := range sceneMarkers {
		if !strings.Contains(SystemPrompt, m) {
			t.Errorf("场景卡段缺 %q（§4.2/§4.5/§4.7 的口径：四要素、三层与五项≥2、A 与 B 句式、反向白名单）", m)
		}
	}
	// 分批分镜轮**不该**带场景卡段（它不产出场景卡）
	if strings.Contains(batchSystemPrompt, "## SCENE CARDS") {
		t.Error("分批分镜轮不产出场景卡，不该带那一段")
	}
}

// TestFaceVocabIsOneSection 五官/敏感词那段只能**有一份定义**（三处引用同一个常量）。
//
// 两处各写一份的后果：改了词表只改一处，于是"分批分镜轮"和"单轮直出"对同一个角色给出
// 两种五官口径 —— 本仓记过多次的"同一个语义两处实现"。
func TestFaceVocabIsOneSection(t *testing.T) {
	if n := strings.Count(strings.Join([]string{SystemPrompt}, ""), "## FACE VOCABULARY"); n != 1 {
		t.Errorf("单轮直出提示词里 FACE VOCABULARY 段出现 %d 次（应恰好 1 次）", n)
	}
	if n := strings.Count(batchSystemPrompt, "## FACE VOCABULARY"); n != 1 {
		t.Errorf("分批提示词里 FACE VOCABULARY 段出现 %d 次（应恰好 1 次）", n)
	}
	// 三处（单轮/分批/计划）都引用同一个常量：源文件里 const 定义只有一处
	src := mustReadFile(t, "director.go")
	if n := strings.Count(src, "const sysFaceVocabRules"); n != 1 {
		t.Fatalf("sysFaceVocabRules 应只有一处 const 定义，实际 %d 处", n)
	}
	if n := strings.Count(src, "sysFaceVocabRules"); n < 4 {
		t.Errorf("sysFaceVocabRules 应被三处提示词引用（定义 1 + 引用 3），实际出现 %d 次", n)
	}
}
