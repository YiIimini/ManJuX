package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/project"
)

// asset_cards_test.go 技能侧素材三卡 → 角色参考图提示词的真源替换（追补单 N4）。
//
// 样本逐字取自真实产出（`data/小说/求你们骂我/素材/角色卡.md` 的沈砚一节），
// 并用**真实目录约定**（`<小说库>/<书名>/素材/角色卡.md` 对 `<projects>/<书名>/<书名·第01集·x>/`）
// 跑一遍端到端 —— 这条接线的全部风险都在"书名怎么对上"，不用真目录测等于没测。

const cardSample = `# 角色卡 · 求你们骂我

> 口径：` + "`references/资产提示词-三卡规范.md`" + `。

---

### 沈砚（主角 · 灾星）
- 身份锁（中文原始，供人核对）：年龄 十九岁；身形 清瘦
**① 定妆（身份源 · 正面半身 · 纯白底）**
` + "```text" + `
Shen Yan — front-facing half-body portrait.
IDENTITY LOCK (do not change): nineteen years old; thin and lean; palms have thick calluses.
Signature item: Shen Yan's umbrella.
plain pure white seamless background, no scenery, no other people.
GUOFENG ANIMATED-FEATURE ART DIRECTION (period Chinese): a photoreal 3D surface with IDEALISED faces.
` + "```" + `
**② 三视图细节板（一致性校对用 · 一张板）**
` + "```text" + `
Shen Yan — character turnaround board. LABELS: the character name, role tag.
` + "```" + `

### 阿婆（真情锚）
**① 定妆（身份源 · 正面半身 · 纯白底）**
` + "```text" + `
Granny — front-facing half-body portrait. IDENTITY LOCK: seventy years old, white clouded eye.
GUOFENG ANIMATED-FEATURE ART DIRECTION (period Chinese): a photoreal 3D surface.
` + "```" + `
`

func TestParseCharacterCardsAndStripStyle(t *testing.T) {
	cards := project.ParseCharacterCards(cardSample)
	if len(cards) != 2 {
		t.Fatalf("应解析出 2 个角色，实际 %d：%v", len(cards), cards)
	}
	// 名字要剥掉定位后缀
	if _, ok := cards["沈砚"]; !ok {
		t.Errorf("角色名应剥掉（主角 · 灾星），实际 %v", cards)
	}
	shen := cards["沈砚"]
	for _, keep := range []string{"front-facing half-body portrait", "IDENTITY LOCK", "nineteen years old", "plain pure white seamless background"} {
		if !strings.Contains(shen, keep) {
			t.Errorf("定妆串应保留 %q：\n%s", keep, shen)
		}
	}
	// **风格句必须剥掉**（否则与平台的介质块同场 = 两套风格，模型取中值）
	if strings.Contains(shen, "GUOFENG") || strings.Contains(shen, "photoreal 3D surface") {
		t.Errorf("卡片自带的风格句必须剥掉：\n%s", shen)
	}
	// ② 三视图板不该混进来（板上要求写角色名标签，而平台的三视图板不许画字）
	if strings.Contains(shen, "turnaround board") {
		t.Errorf("定妆串里混进了三视图板内容：\n%s", shen)
	}
}

func TestMatchCardPrompt(t *testing.T) {
	cards := project.ParseCharacterCards(cardSample)
	// 精确命中
	if _, ok := project.MatchCardPrompt("沈砚", cards); !ok {
		t.Error("精确名字应命中")
	}
	// 造型档后缀（同一角色另一套衣服）退到点名前那段
	if _, ok := project.MatchCardPrompt("沈砚·一品", cards); !ok {
		t.Error("`沈砚·一品` 应命中沈砚的卡")
	}
	// 不在卡里的角色 → 不命中（平台照旧用自己的提示词）
	if _, ok := project.MatchCardPrompt("柳三娘", cards); ok {
		t.Error("卡里没有的角色不该命中")
	}
	if _, ok := project.MatchCardPrompt("", cards); ok {
		t.Error("空名字不该命中")
	}
	if _, ok := project.MatchCardPrompt("沈砚", nil); ok {
		t.Error("没有卡片时不该命中")
	}
}

func TestApplyCardPromptsOverridesOnlyWhenCardExists(t *testing.T) {
	root := t.TempDir()
	novelRoot := filepath.Join(root, "小说")
	projRoot := filepath.Join(root, "projects")
	book := "求你们骂我"
	if err := os.MkdirAll(filepath.Join(novelRoot, book, "素材"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(novelRoot, book, "素材", "角色卡.md"), []byte(cardSample), 0o644); err != nil {
		t.Fatal(err)
	}
	// 配置：小说库指到临时目录（applyCardPrompts 靠它定位书目录）
	cfgPath := filepath.Join(root, "settings.json")
	cm, err := config.NewManager(cfgPath, root)
	if err != nil {
		t.Fatal(err)
	}
	s := cm.Get()
	s.NovelDir = novelRoot
	if err := cm.Update(s); err != nil {
		t.Fatal(err)
	}
	st, err := project.NewStore(projRoot)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.Create(book+"·第01集·求你们骂我", project.ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	// 平台的目录约定：把项目挪到 <projects>/<书名>/<集名>/（书名靠父目录名对上）
	if err := os.MkdirAll(filepath.Join(projRoot, book), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(st.Dir(proj.ID), filepath.Join(projRoot, book, proj.ID)); err != nil {
		t.Fatal(err)
	}

	c := &Coordinator{store: st, cfg: cm, bus: NewBus()}
	plan := &project.Plan{
		Characters: []project.Character{
			{ID: "shen", Name: "沈砚", ImagePrompt: "LLM 写的一段定妆提示词"},
			{ID: "shen2", Name: "沈砚·一品", ImagePrompt: "LLM 写的另一段"},
			{ID: "liu", Name: "柳三娘", ImagePrompt: "卡里没有这个人，应保持原样"},
		},
	}
	n := c.applyCardPrompts(proj, plan)
	if n != 2 {
		t.Fatalf("应覆盖 2 个角色（沈砚 / 沈砚·一品），实际 %d", n)
	}
	if !strings.Contains(plan.Characters[0].ImagePrompt, "IDENTITY LOCK") {
		t.Errorf("有卡的角色应改用卡片定妆串，实际 %q", plan.Characters[0].ImagePrompt)
	}
	if strings.Contains(plan.Characters[0].ImagePrompt, "GUOFENG") {
		t.Error("交出去的提示词里不该带卡片自己的风格句")
	}
	if plan.Characters[2].ImagePrompt != "卡里没有这个人，应保持原样" {
		t.Errorf("卡里没有的角色必须原样不动，实际 %q", plan.Characters[2].ImagePrompt)
	}
	// 幂等：再跑一次不应该又"覆盖"（计数为 0）
	if n2 := c.applyCardPrompts(proj, plan); n2 != 0 {
		t.Errorf("已经是卡片串时不该重复计数，实际 %d", n2)
	}

	// 没有卡片的书：一个字节都不该动
	proj2, err := st.Create("没卡的书·第01集·x", project.ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projRoot, "没卡的书"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(st.Dir(proj2.ID), filepath.Join(projRoot, "没卡的书", proj2.ID)); err != nil {
		t.Fatal(err)
	}
	plan2 := &project.Plan{Characters: []project.Character{{ID: "a", Name: "甲", ImagePrompt: "原样"}}}
	if n := c.applyCardPrompts(proj2, plan2); n != 0 || plan2.Characters[0].ImagePrompt != "原样" {
		t.Errorf("没有素材三卡的书不该被改：n=%d prompt=%q", n, plan2.Characters[0].ImagePrompt)
	}
}
