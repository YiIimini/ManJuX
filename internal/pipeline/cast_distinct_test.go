package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// cast_distinct_test.go 撞脸自检的契约（2026-09-27 用户报「周廷和主角脸相似，这不合理吧」）。
//
// 定调：**提示词写对了也可能出成一个人** —— 那两张的年龄/脸型/眼型在纸面上分得很开，
// 出图却是两个瘦长脸的年轻人。读提示词的检查看不见五官，比例自检看三庭眼距（都正常），
// 朝向自检看是不是正面 —— 三张网都漏，所以必须有"看图比脸"这一层。

// TestIdentityLockExcerptPrefersShapeOverPlot 同剧对比要拿**外形锁**，不能拿剧情。
//
// 实测事故：《求你们骂我》周廷那条 description 是一整段剧情表行
// （「1 周廷 周家二少爷，六品武夫 他 三年前就做过一次成功的平账：火从西头起…1／8／31」）。
// 把它当"特征"塞进对比块，模型读到的是剧情 —— "要跟别人不一样"这句就完全落空，
// 只剩它自己的先验：一张通用帅哥脸。
func TestIdentityLockExcerptPrefersShapeOverPlot(t *testing.T) {
	plot := "1 周廷 周家二少爷，六品武夫 他 三年前就做过一次成功的平账：火从西头起、货仓是田家的"
	lock := "Zhou Ting — half-body . IDENTITY LOCK (do not change): thirty-one years old; square face, high cheekbones, deep eye sockets; sallow, slightly oily"
	plan := &project.Plan{Characters: []project.Character{
		{ID: "char_01", Name: "沈砚", Description: "主角", ImagePrompt: "Shen Yan — half-body. IDENTITY LOCK (do not change): nineteen years old; thin long face"},
		{ID: "char_06", Name: "周廷", Description: plot, ImagePrompt: "Zhou Ting — half-body . " + lock},
	}}
	blk := castContrastBlock(plan, "沈砚", KindHuman)
	if !strings.Contains(blk, "square face") || !strings.Contains(blk, "thirty-one") {
		t.Errorf("对比块要用周廷的**外形锁**（方脸/31 岁），实际：%s", blk)
	}
	if strings.Contains(blk, "货仓是田家的") {
		t.Errorf("对比块不该塞剧情（那是「特征」的假象）：%s", blk)
	}
	// 措辞要**点名对比**，不只是抽象的 "different face shape"
	for _, want := range []string{"at a glance", "never reuse one face template"} {
		if !strings.Contains(blk, want) {
			t.Errorf("对比块的措辞要具体到「一眼可辨/不许共用一张脸模板」，缺 %q", want)
		}
	}
}

// TestPortraitFaceIDClauseIsLast 定妆照的最后一句必须是"这张脸是谁"。
//
// 为什么钉顺序：模型对提示词**末尾**的约束最敏感（比例锚点放最后是同一条设计），
// 而画风块里那句 "idealised… simplified, readable features" 是往一个模板上拉的力 ——
// 身份锁必须压在它后面，否则 31 岁的方脸会被抹平成"年轻瘦长脸"。
func TestPortraitFaceIDClauseIsLast(t *testing.T) {
	plan := &project.Plan{
		Style: "GUOFENG ANIMATED-FEATURE ART DIRECTION (period Chinese)",
		Characters: []project.Character{
			{ID: "char_01", Name: "沈砚", ImagePrompt: "Shen Yan — half-body. IDENTITY LOCK (do not change): nineteen years old; thin long face; phoenix eyes"},
			{ID: "char_06", Name: "周廷", ImagePrompt: "Zhou Ting — half-body. IDENTITY LOCK (do not change): thirty-one years old; square face; deep eye sockets"},
		},
	}
	proj := &project.Project{Config: project.ProjectConfig{Params: project.DefaultStageParams()}}
	prompt := PortraitPrompt(PortraitSubject{
		Kind: KindHuman, Name: "周廷", Description: "周家二少爷",
		ImagePrompt: plan.Characters[1].ImagePrompt, FacesOn: true,
	}, plan)

	id := strings.LastIndex(prompt, "FACE ID")
	if id < 0 {
		t.Fatalf("定妆照提示词里应有 FACE ID 段：\n%s", prompt)
	}
	// FACE ID 必须是**收尾段**：它之后只剩空白（模型对末尾最敏感）
	if rest := strings.TrimSpace(prompt[id:]); strings.Contains(rest, "【全片视觉风格】") {
		t.Error("FACE ID 之后不该再有风格块 —— 它得是最后一段")
	}
	// 自己的外形锁要**逐字**出现在这一句里（不是摘要、不是改写）
	if !strings.Contains(prompt[id:], "thirty-one years old; square face") {
		t.Errorf("FACE ID 段要逐字重述本角色的外形锁：\n%s", prompt[id:])
	}
	// 要点名"不许像谁"（拿别人的外形锁当反例），并说清理想化≠可互换
	for _, want := range []string{"NOT:", "thin long face", "interchangeable"} {
		if !strings.Contains(prompt[id:], want) {
			t.Errorf("FACE ID 段缺 %q：\n%s", want, prompt[id:])
		}
	}
	_ = proj
}

// TestCastSeparationClausePointsAtDimensions 分离指令必须点明**改哪几个维度**。
func TestCastSeparationClausePointsAtDimensions(t *testing.T) {
	c := CastSeparationClause("沈砚")
	for _, want := range []string{"沈砚", "FACE SHAPE", "AGE READ", "eye shape", "skin tone", "hair silhouette", "identity lock"} {
		if !strings.Contains(c, want) {
			t.Errorf("分离指令缺 %q（不给维度的「要不一样」模型听不懂）：%s", want, c)
		}
	}
	if strings.Contains(CastSeparationClause(""), "「」") {
		t.Error("没给名字时不该出现空书名号")
	}
}

// TestCastPeersPicksOtherCharacters 比对对象 = 同剧**已出图**的其他角色（排除自己与 Q 版）。
func TestCastPeersPicksOtherCharacters(t *testing.T) {
	dir := t.TempDir()
	mk := func(n string) string {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
		return n
	}
	plan := &project.Plan{Characters: []project.Character{
		{ID: "char_01", Name: "沈砚"}, {ID: "char_06", Name: "周廷"},
		{ID: "char_07", Name: "苏晚"}, {ID: "char_01_chibi", Name: "沈砚Q版"},
		{ID: "char_09", Name: "还没出图的"},
	}}
	assets := map[string]project.Asset{
		"char:char_01":       {File: mk("a.png")},
		"char:char_06":       {File: mk("b.png")},
		"char:char_07":       {File: mk("c.png")},
		"char:char_01_chibi": {File: mk("d.png")},
	}
	peers := castPeers(dir, plan, assets, "char:char_06")
	if len(peers) != 2 {
		t.Fatalf("应挑出沈砚与苏晚两张（排除自己/Q版/没出图的），实际 %+v", peers)
	}
	for _, p := range peers {
		if p.name == "周廷" || p.name == "沈砚Q版" {
			t.Errorf("不该把自己或 Q 版列为比对对象：%+v", p)
		}
	}
}

// TestAssetStageCallsCastDistinctness 接线：撞脸自检必须在定妆照出图后真的被调用，
// 而且判成撞脸时要**带着分离指令**重出（不是只换种子）。
//
// 这条守住"口径没接线"那类错（同一个坑当天已经踩过一次：板子尺寸只改了日志没接到生成口）。
func TestAssetStageCallsCastDistinctness(t *testing.T) {
	src, err := os.ReadFile("stages_assets.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "c.checkCastDistinctness(ctx, proj, assets, kind, id, name, imagePrompt, ek, path, genAsset)") {
		t.Error("资产阶段要在定妆照出图后调用撞脸自检")
	}
	if !strings.Contains(s, "CastSkipNoCapability") {
		t.Error("没配视觉模型时要如实说「撞脸自检没跑」（每运行一次提醒一次），不能静默通过")
	}
	cd, err := os.ReadFile("cast_distinct.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cd), "CastSeparationClause(other)") {
		t.Error("判成撞脸后要带**分离指令**重出（只换种子不够 —— 上一轮已经「按着身份锁」画错了）")
	}
}
