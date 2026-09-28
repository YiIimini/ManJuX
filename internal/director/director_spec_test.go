package director

import (
	"go/ast"
	"go/parser"
	"go/token"
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
// 9 视角词表（含"空值合法"）、物件配额 2–4 组、闭嘴句、头身比；**当时缺**的是下面这些：
//
// 【头身比口径变更 2026-09-28】用户《二、人物道具提取指令》写"九头身"、《【额外】人物道具场景提取》写"8.5 头身"，
// 而技能侧规范此前写 7.5（那是我在用户报"比例太丑"时拍的值，把"丑"误判成"头身数太高"）。
// 用户今天明确要求照 `D:\Ai\Datas\漫剧制作` 的口径 ⇒ 取 **8.5**。技能侧规范需同步改这一句。
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
		"eight and a half heads", "matte skin with a soft natural finish",
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
	// 三处（单轮/分批/计划）都引用同一个载体：正文只有**一份**（prompts/sysFaceVocabRules.md）。
	//
	// 【2026-09-28 改口径】此前钉的是"源文件里 const 定义只有一处"。提示词已搬进 go:embed 的
	// Markdown（根因治理：raw string 里出现反引号会截断编译，一天栽了七次）⇒ 判据随之改成
	// "载体文件恰好一份、且源码里仍是同一个符号"。
	src := mustReadFile(t, "director.go")
	if n := strings.Count(src, "sysFaceVocabRules"); n < 4 {
		t.Errorf("sysFaceVocabRules 应被三处提示词引用（定义 1 + 引用 3），实际出现 %d 次", n)
	}
	files := 0
	for _, name := range PromptNames() {
		if name == "sysFaceVocabRules" {
			files++
		}
	}
	if files != 1 {
		t.Fatalf("sysFaceVocabRules 的载体文件应恰好 1 份（prompts/sysFaceVocabRules.md），实际 %d 份", files)
	}
}

// TestNoRawStringPromptsInDirector 源码里**不许再出现反引号**。
//
// 这是给"反引号陷阱"上的永久锁：提示词原本写成 Go raw string（反引号定界），正文里出现任何一个
// 反引号都会截断字符串、编译报错 —— 2026-09-28 一天为此栽了七次。搬进 go:embed 的 Markdown 之后，
// director.go 里不该再有 raw string；哪天有人又在里面写 `...`，这条守卫会立刻红。
func TestNoRawStringPromptsInDirector(t *testing.T) {
	// 用 AST 判，而不是字符扫描：提示词正文搬到 prompts/*.md 之后，director.go 里不该再有任何
	// **散文型** raw string（≥120 字符或含换行）。字符扫描会把字符串字面量/注释里的反引号误判，
	// 第一版就是这么被测出来的（守卫自己先假红）。
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "director.go", mustReadFile(t, "director.go"), 0)
	if err != nil {
		t.Fatalf("解析 director.go 失败:%v", err)
	}
	var bad []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || !strings.HasPrefix(lit.Value, "`") {
			return true
		}
		body := strings.TrimSuffix(strings.TrimPrefix(lit.Value, "`"), "`")
		if len(body) >= 120 || strings.Contains(body, "\n") {
			bad = append(bad, firstLineOf(body))
		}
		return true
	})
	if len(bad) > 0 {
		t.Errorf("director.go 里还有 %d 段**散文型 raw string**（首行：%s）—— 提示词请写进 prompts/*.md；"+
			"这种写法里出现任何反引号都会截断编译（一天栽过七次）", len(bad), strings.Join(bad, " / "))
	}
}
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len([]rune(s)) > 40 {
		s = string([]rune(s)[:40])
	}
	return s
}

// TestEveryPromptSegmentHasText 每段提示词都要有正文，且不得为空（空提示词比编译不过更危险：
// 模型会凭感觉干活，而且没有痕迹）。
func TestEveryPromptSegmentHasText(t *testing.T) {
	names := PromptNames()
	if len(names) < 20 {
		t.Fatalf("提示词载体过少（%d 段），搬运动作可能没跑完", len(names))
	}
	for _, name := range names {
		if strings.TrimSpace(promptText(name)) == "" {
			t.Errorf("提示词段 %s 是空的", name)
		}
	}
}

// TestUserInstructionFilesAdaptedIntoH3 用户三份指令（三视图/场景/仿真人10S）的**意图**
// 必须按 **H3 格式**落在提示词里 —— 不是把即梦那套中文词表搬进来。
//
// 依据（用户 2026-09-28 明确要求）：「不是直接落，是参考后整理匹配进 H3 提示词格式」。
// 三份文件是即梦/Seedance 的字段式写法（【关键道具】【运镜】独立成行、中文负向词表），
// 而 H3 只有散文段落 + <d> 说话人体系，且**没有独立负向通道** ⇒ 必须转写：
//
//	· 负向词（模糊/变形/畸形手指/闪烁/口型不对）→ 正文末尾的**散文排除句**；
//	· 运镜/角度 → 官方英文词表 + 写进镜头句子里（已在 sysViewRules / sysShotRules 里）；
//	· 旁白 → 转成**该角色的内心独白**（同一音色 + 闭嘴句）或剧情内字幕，不留独立旁白音色；
//	· 占位一致性（道具持有者/朝向、角色站位、特效落点）→ 写进每镜散文（H3 逐镜无记忆）。
func TestUserInstructionFilesAdaptedIntoH3(t *testing.T) {
	for name, p := range ShotPrompts() {
		for _, want := range []string{
			"EXCLUSIONS",                  // 负向词 → 散文排除句（并标明是平台规则，不是官方要求）
			"PLATFORM rule, not official", // 防止再次把平台自定规则冒充成官方要求（本轮同步抓到两条）
			"no deformed or fused hands",  // 畸形手指/变形
			"no extra or missing fingers",
			"no flicker or strobing",      // 闪烁
			"no colour banding",           // 色彩失真
			"no warped or drifting faces", // 变形
			"blocking",                    // 占位一致性（段落标题是小写词也认）
		} {
			if !strings.Contains(strings.ToLower(p), strings.ToLower(want)) {
				t.Errorf("%s 缺 %q —— 用户三份指令的意图没按 H3 格式落进提示词", name, want)
			}
		}
	}
	// 旁白口径：不留独立旁白音色
	if !strings.Contains(SystemPrompt, "NO NARRATOR LANE") {
		t.Error("旁白要转成内心独白/剧情内字幕（用户指定的《仿真人10S》口径），不能留独立旁白音色")
	}
	if strings.Contains(SystemPrompt, "narration (旁白) is an off-screen voiceover; characters' lips stay closed. Use sparingly.") {
		t.Error("旧的「旁白保留、少用」口径应已被 NO NARRATOR LANE 取代")
	}
	// 场景五步公式（四、场景提示词指令 的意图，按英文散文要求落）
	if !strings.Contains(SystemPrompt, "FIVE STEPS") || !strings.Contains(SystemPrompt, "how the light touches the material") {
		t.Error("场景板要按五步公式要求（光→主体→材质与光互动→画质后缀→物件），否则就是「简陋单一」")
	}
}

// TestHighlightRulesReachAllPrompts 抓重点纪律必须进**三条**提示词（计划轮定 segments/brief，
// 两条分镜轮写 shots）——2026-09-28 用户：「分镜脚本 LLM 解析是否按《一、网文小说转剧本提示词指令》
// 来啊，抓重点啊」。只挂一半的话，计划轮会把"平淡的节拍"先写进 brief，分镜轮再忠实地拍出来。
func TestHighlightRulesReachAllPrompts(t *testing.T) {
	all := map[string]string{"计划轮": planSystemPrompt}
	for k, v := range ShotPrompts() {
		all[k] = v
	}
	for name, p := range all {
		for _, want := range []string{
			"抓重点纪律",
			"开篇 3 秒爆点",
			"每 80 字",
			"段段留钩",
			"砍掉所有冗余铺垫",
		} {
			if !strings.Contains(p, want) {
				t.Errorf("%s 缺 %q —— 抓重点纪律没进这条提示词", name, want)
			}
		}
	}
}
