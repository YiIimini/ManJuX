package pipeline

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/library"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 资产库复用路径的契约测试(审计:别名命中会静默丢掉全部多视角/转台图)。
//
// 守三件事:
//  1. 台账/关联图一律按**条目自己的名字**查 —— RelatedFiles/EntryDir/BumpUsed 都是
//     按传入名字推目录的,而别名命中时条目目录名是 e.Name、调用方手上是本集叫法 →
//     目录不存在 → 库里那几张 _view_*.png / _turnaround.png 一张都不复制,界面却仍报
//     「♻ 资产库复用」。这条测试用"本集叫法"复用"另一个叫法"的角色来钉住它。
//  2. 写进项目资产的 ImagePrompt 必须是**条目自己的**提示词:本集方案那段措辞与复制
//     过来的像素不是一回事(下游会拿它拼多视角编辑指令、并反写回共享库)。
//  3. 复用计数要落在条目上(List 按 UsedCount 排序,涨不上去说明根本没命中)。
func TestLibraryReuseViaAliasBringsViewsAndCounts(t *testing.T) {
	root := t.TempDir()
	lib, err := library.New(filepath.Join(root, "library"))
	if err != nil {
		t.Fatal(err)
	}
	const desc = "十八九岁的少年,清瘦长脸,细长单眼皮,短发低马尾,靛蓝布长衫,肩上挎着旧书箱"
	portrait := filepath.Join(root, "portrait.png")
	writeTestPNG(t, portrait, 832, 1248)
	if _, err := lib.Put("characters", "沈砚", desc, "PROMPT-FROM-LIBRARY", portrait, "proj1", "krea2"); err != nil {
		t.Fatal(err)
	}
	entryDir, err := lib.EntryDir("characters", "沈砚")
	if err != nil {
		t.Fatal(err)
	}
	// 库里已有多视角图 + 转台图(文件主名来自最初入库那个项目的命名)
	writeTestPNG(t, filepath.Join(entryDir, "characters_char_shenyan_view_side.png"), 832, 1248)
	writeTestPNG(t, filepath.Join(entryDir, "characters_char_shenyan_turnaround.png"), 3328, 1248)

	cfg, err := config.NewManager(filepath.Join(root, "settings.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := project.NewStore(filepath.Join(root, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	proj, err := store.Create("第一集", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	c := New(store, cfg, engine.NewRegistry(), new(ff.FF), NewBus(), lib)

	// 本集把同一个角色叫成「阿枢」:精确名必然 miss,靠描述相似度命中同一条目
	r, ok := c.reuseFromLibrary(proj.ID, "characters", "char_01", "阿枢", desc)
	if !ok {
		t.Fatal("同一角色的另一种叫法必须复用库里条目,否则会重新生成一张脸")
	}
	if r.Name != "沈砚" {
		t.Errorf("必须用条目自己的名字(下游按它写库/查关联图),实际 %q", r.Name)
	}
	if r.Copied != 2 {
		t.Errorf("库里的多视角图与转台图必须一并带进项目,实际复制 %d 张 —— "+
			"一张都不带会让 H3 只有正面照可用(界面却仍报「资产库复用」)", r.Copied)
	}
	if r.Prompt != "PROMPT-FROM-LIBRARY" {
		t.Errorf("配对元数据应取条目自己的提示词,实际 %q", r.Prompt)
	}
	if got := r.PromptFor("本集方案的新措辞"); got != "PROMPT-FROM-LIBRARY" {
		t.Errorf("条目有提示词时不得被本集措辞顶掉,实际 %q", got)
	}
	// 项目侧命名必须能被渲染期推导出来(转台图靠主图主名 + _turnaround 找)
	if tp := turnaroundPath(store.Dir(proj.ID), r.Rel); tp == "" {
		t.Errorf("复制过来的转台图必须按项目侧命名落盘,否则渲染永远拼不出第二张参考(rel=%s)", r.Rel)
	}
	// 复用计数落在条目上
	e, _, ok := lib.Match("characters", "沈砚")
	if !ok {
		t.Fatal("条目应仍在")
	}
	if e.UsedCount != 1 {
		t.Errorf("复用计数应 +1(库管理页按它排序),实际 %d", e.UsedCount)
	}
	// 不许为本集叫法新建一个条目(那会让同一角色在库里变成两条,描述还是空的)
	if _, _, ok := lib.Match("characters", "阿枢"); ok {
		t.Error("别名命中时不得新建条目 —— 同一角色两条记录会让后续取参考图取决于目录名排序")
	}
}

// 库里没有这个对象时必须老实返回 ok=false(交给调用方生成),不能凭空造一个。
func TestLibraryReuseMissReturnsFalse(t *testing.T) {
	root := t.TempDir()
	lib, err := library.New(filepath.Join(root, "library"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.NewManager(filepath.Join(root, "settings.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := project.NewStore(filepath.Join(root, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	proj, err := store.Create("第一集", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	c := New(store, cfg, engine.NewRegistry(), new(ff.FF), NewBus(), lib)
	if _, ok := c.reuseFromLibrary(proj.ID, "characters", "char_09", "查无此人", "一段足够长的描述,不该命中任何条目"); ok {
		t.Error("库为空时必须返回未命中")
	}
	// 库为 nil(禁用)时同样不 panic
	c.lib = nil
	if _, ok := c.reuseFromLibrary(proj.ID, "characters", "char_09", "查无此人", "一段足够长的描述"); ok {
		t.Error("资产库禁用时应返回未命中")
	}
}

// 渲染前补齐必须"先查库、再生成",而且**查库不需要引擎**(审计:
// backfillAssets 完全不查资产库,同一角色在新一集会重新生成另一张脸并反写覆盖共享条目)。
//
// 这条走完整调用路径:库里有角色与场景 → 即使注册表里没有任何本地引擎,也必须把两张图
// 复用进项目(复用只是复制像素),而不是报「缺 N 张参考图,将降级纯文生」。
func TestBackfillAssetsReusesLibraryWithoutEngine(t *testing.T) {
	root := t.TempDir()
	lib, err := library.New(filepath.Join(root, "library"))
	if err != nil {
		t.Fatal(err)
	}
	const charDesc = "十八九岁的少年,清瘦长脸,细长单眼皮,短发低马尾,靛蓝布长衫"
	portrait := filepath.Join(root, "portrait.png")
	writeTestPNG(t, portrait, 832, 1248)
	if _, err := lib.Put("characters", "沈砚", charDesc, "PROMPT-CHAR", portrait, "proj1", "krea2"); err != nil {
		t.Fatal(err)
	}
	entryDir, err := lib.EntryDir("characters", "沈砚")
	if err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, filepath.Join(entryDir, "characters_char_shenyan_turnaround.png"), 3328, 1248)
	sceneImg := filepath.Join(root, "scene.png")
	writeTestPNG(t, sceneImg, 768, 1344)
	if _, err := lib.Put("scenes", "临水城", "河港小镇,青石板路与吊脚楼,傍晚小雨", "PROMPT-SCENE", sceneImg, "proj1", "krea2"); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.NewManager(filepath.Join(root, "settings.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := project.NewStore(filepath.Join(root, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	proj, err := store.Create("第二集", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	plan := &project.Plan{
		Style:      "国漫二维动画",
		Characters: []project.Character{{ID: "char_01", Name: "阿枢", Kind: "human", Description: charDesc}},
		Scenes:     []project.Scene{{ID: "scene_01", Name: "临水城", Description: "河港小镇", ImagePrompt: "a river port town"}},
		Shots: []project.Shot{
			{ID: 1, Mode: "ref2va", Characters: []string{"char_01"}, Scene: "scene_01"},
		},
	}
	// 注册表里**没有**本地引擎:旧实现在这里就直接放弃并报"将降级纯文生"
	c := New(store, cfg, engine.NewRegistry(), new(ff.FF), NewBus(), lib)
	assets := map[string]project.Asset{}
	if ok := c.backfillAssets(context.Background(), proj, plan, assets, needForShots(plan.Shots)); !ok {
		t.Fatal("库里有整套参考图时不该报补齐失败")
	}
	if len(assets) != 2 {
		t.Fatalf("角色与场景都应从库里复用进来,实际 %d 个资产", len(assets))
	}
	if a := assets["char:char_01"]; a.File == "" || a.Source != "library" {
		t.Errorf("角色应从库复用,实际 %+v", a)
	}
	if a := assets["scene:scene_01"]; a.File == "" || a.Source != "library" {
		t.Errorf("场景应从库复用,实际 %+v", a)
	}
	// 装配参考图时必须真的能拿到这两张(而不是 missing)
	refs, roles, missing, overCap := RefPlan(store.Dir(proj.ID), plan.Shots[0], assets, false)
	if len(missing) != 0 {
		t.Errorf("复用的资产不该再算缺失:%v", missing)
	}
	if len(overCap) != 0 {
		t.Errorf("三张图不该触发超上限裁剪:%v", overCap)
	}
	if len(refs) != 3 || len(roles) != 3 {
		t.Errorf("应装配 定妆照 + 转台图 + 场景 共 3 张,实际 %d 张(refs=%d)", len(refs), len(roles))
	}
	// 项目侧的资产表必须落盘(否则下一阶段读不到)
	saved, err := store.LoadAssets(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 {
		t.Errorf("复用结果必须写进项目资产表,实际 %d 条", len(saved))
	}
}

// 库里没有时必须老实返回 false(交给调用方降级/生成),不许凭空造资产。
func TestBackfillAssetsWithoutEngineAndWithoutLibrary(t *testing.T) {
	root := t.TempDir()
	lib, err := library.New(filepath.Join(root, "library"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.NewManager(filepath.Join(root, "settings.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := project.NewStore(filepath.Join(root, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	proj, err := store.Create("第三集", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	plan := &project.Plan{
		Style:      "国漫二维动画",
		Characters: []project.Character{{ID: "char_01", Name: "沈砚", Kind: "human", Description: "十八九岁的少年,清瘦长脸"}},
		Shots:      []project.Shot{{ID: 1, Mode: "ref2va", Characters: []string{"char_01"}}},
	}
	c := New(store, cfg, engine.NewRegistry(), new(ff.FF), NewBus(), lib)
	assets := map[string]project.Asset{}
	if ok := c.backfillAssets(context.Background(), proj, plan, assets, needForShots(plan.Shots)); ok {
		t.Error("库为空且引擎不可用时应返回 false(调用方据此提示将降级纯文生)")
	}
	if len(assets) != 0 {
		t.Errorf("没有任何来源时不该凭空产生资产,实际 %d 个", len(assets))
	}
}

// 渲染前补多视角的实体类型取值链(审计:只按方案文本推断,无视库里看图判定)。
func TestViewsEntityKindPrefersLibraryJudgement(t *testing.T) {
	cases := []struct {
		name    string
		text    EntityKind
		libKind string
		want    EntityKind
	}{
		{"库里有看图判定 → 用库里的", KindHuman, "object", KindObject},
		{"库里没有记录 → 用文本推断", KindHuman, "", KindHuman},
		{"库里的值不合规 → 回落文本推断", KindHuman, "不是合法键", KindHuman},
		{"库里大小写/空格不规范 → 归一化后仍认", KindHuman, " object ", KindObject},
		{"库说 chibi → 认", KindHuman, "chibi", KindChibi},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := viewsEntityKind(tc.text, tc.libKind); got != tc.want {
				t.Fatalf("viewsEntityKind(%s,%q) = %s,期望 %s", tc.text, tc.libKind, got, tc.want)
			}
		})
	}
}

// 场景图方向与项目画幅相反时必须被判定出来(审计:横向场景图会被原样搬进竖幅项目)。
func TestSceneOrientationMismatch(t *testing.T) {
	cases := []struct {
		name           string
		iw, ih, pw, ph int
		want           bool
	}{
		{"竖幅项目 + 竖向场景图 → 一致", 832, 1248, 768, 1344, false},
		{"竖幅项目 + 横向场景图 → 不一致", 1248, 832, 768, 1344, true},
		{"横幅项目 + 横向场景图 → 一致", 1248, 832, 1344, 768, false},
		{"横幅项目 + 竖向场景图 → 不一致", 832, 1248, 1344, 768, true},
		{"方形场景图 → 按不相反处理(不猜)", 1024, 1024, 768, 1344, false},
		{"读不到尺寸 → 不判断", 0, 0, 768, 1344, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sceneOrientationMismatch(tc.iw, tc.ih, tc.pw, tc.ph); got != tc.want {
				t.Fatalf("sceneOrientationMismatch(%d,%d,%d,%d) = %v,期望 %v",
					tc.iw, tc.ih, tc.pw, tc.ph, got, tc.want)
			}
		})
	}
}

// 复用提示词取值:条目没记提示词时才回落到调用方手上那段(不制造空提示词)。
func TestLibReusePromptForFallback(t *testing.T) {
	if got := (libReuse{}).PromptFor("本集措辞"); got != "本集措辞" {
		t.Errorf("条目没记提示词时应回落,实际 %q", got)
	}
	if got := (libReuse{Prompt: "  "}).PromptFor("本集措辞"); got != "本集措辞" {
		t.Errorf("空白提示词等同于没记,实际 %q", got)
	}
}

// writeTestPNG 落一张真 PNG(asset 复用/方向判定都要读得到真实宽高)。
func writeTestPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 200, G: 180, B: 160, A: 255})
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// 顺带钉住 imagePixelSize:方向判定依赖它,读不到尺寸就永远不报警。
func TestImagePixelSize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.png")
	writeTestPNG(t, p, 1248, 832)
	w, h, err := imagePixelSize(p)
	if err != nil {
		t.Fatal(err)
	}
	if w != 1248 || h != 832 {
		t.Fatalf("读到的尺寸 %dx%d 与写入的不符", w, h)
	}
	if _, _, err := imagePixelSize(filepath.Join(t.TempDir(), "none.png")); err == nil {
		t.Error("文件不存在时应返回错误(调用方据此不报警)")
	}
}
