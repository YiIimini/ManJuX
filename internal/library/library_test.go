package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestMatchSkipsCorruptedEntry 损坏的 asset.json 必须被安全跳过(按未命中处理),
// 既不能 panic,也不能把半截数据当有效条目。这条路径此前完全静默 ——
// 表现只是「角色形象莫名漂移」,无从排查。
func TestMatchSkipsCorruptedEntry(t *testing.T) {
	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "characters", "坏条目")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asset.json"), []byte(`{"kind":"characters",`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := l.Match("characters", "坏条目"); ok {
		t.Fatal("损坏条目不应命中")
	}
}

// TestMatchSkipsEntryWithoutImage 条目 JSON 正常但图片缺失时同样按未命中处理,
// 不能让调用方拿到一个指向不存在文件的路径。
func TestMatchSkipsEntryWithoutImage(t *testing.T) {
	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "scenes", "缺图场景")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	e := Entry{Kind: "scenes", Name: "缺图场景", Image: "scenes/缺图场景/portrait.png"}
	raw, _ := json.Marshal(&e)
	if err := os.WriteFile(filepath.Join(dir, "asset.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := l.Match("scenes", "缺图场景"); ok {
		t.Fatal("缺图条目不应急命中")
	}
}

// TestPutWritesValidEntryAndNoTmpLeft Put 之后条目必须可完整读回,
// 且原子写不得残留 .tmp 文件(残留意味着 rename 未完成)。
func TestPutWritesValidEntryAndNoTmpLeft(t *testing.T) {
	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "src.png")
	if err := os.WriteFile(src, []byte("fake-png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put("characters", "林凡", "描述", "prompt", src, "proj", "upload"); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	entry, img, ok := l.Match("characters", "林凡")
	if !ok {
		t.Fatal("Put 后应能命中")
	}
	if entry.Name != "林凡" || img == "" {
		t.Fatalf("条目内容异常: %+v", entry)
	}
	raw, err := os.ReadFile(filepath.Join(root, "characters", "林凡", "asset.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed Entry
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("asset.json 应为合法 JSON: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "characters", "林凡", "asset.json.tmp")); !os.IsNotExist(err) {
		t.Error("原子写不应残留 .tmp 文件")
	}
}

// TestPutCollectsRelatedImagesAndClearRelated 多视角原图/转台图的「收集 + 清理」契约。
//
// 回归 2026-09-12:资产阶段曾把抽帧写进 <base>_views/ 子目录,而 relatedImages /
// ClearRelated 用的是**非递归** glob <base>_view_*.png —— 两处都用同一个 glob,
// 于是多视角原图既收不进库(详情弹窗只有定妆照),重抽后也清不掉(仍是上一张脸
// 的过期角度图)。本测试从「生产者按契约放在主图同目录」这一侧钉住两者成对生效。
//
// 文件名里的 tag 必须与前端 LibraryView.labelOf() 认识的标签
// (threequarter / side / back / turnaround)逐字一致,否则库里有图、标签却只会
// 退化显示成笼统的「多视角」。
func TestPutCollectsRelatedImagesAndClearRelated(t *testing.T) {
	src := t.TempDir()
	master := filepath.Join(src, "characters_char_c1.png")
	for _, f := range []string{
		master,
		filepath.Join(src, "characters_char_c1_view_threequarter.png"),
		filepath.Join(src, "characters_char_c1_view_side.png"),
		filepath.Join(src, "characters_char_c1_view_back.png"),
		filepath.Join(src, "characters_char_c1_turnaround.png"),
		// 三视图细节板（pipeline/board.go）：与其它关联图同一条契约 —— 同目录同主名，
		// 收得进库、换图时也清得掉。加新后缀若漏改 relatedGlobs，这里立刻红。
		filepath.Join(src, "characters_char_c1_board.png"),
	} {
		if err := os.WriteFile(f, []byte("png:"+filepath.Base(f)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 子目录里的抽帧(旧实现的位置)不应被收进来 —— glob 非递归是刻意的契约:
	// 多视角抽帧必须与定妆照同目录,否则收不进库也清不掉。
	sub := filepath.Join(src, "characters_char_c1_views")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "view_0.png"), []byte("old-layout"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	e, err := l.Put("characters", "契约角色", "描述", "prompt", master, "proj", "krea2")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}

	// 期望:主图 + 3 张多视角原图 + 转台图 + 三视图细节板,一张都不能少(子目录帧不计入)。
	// 主图在库内统一叫 portrait.png,关联图保留生成时的基名(前端 labelOf 靠
	// 基名里的 _view_<tag>/_turnaround/_board 判用途,所以基名必须原样保留)。
	wantSorted := []string{
		"characters/契约角色/characters_char_c1_board.png",
		"characters/契约角色/characters_char_c1_turnaround.png",
		"characters/契约角色/characters_char_c1_view_back.png",
		"characters/契约角色/characters_char_c1_view_side.png",
		"characters/契约角色/characters_char_c1_view_threequarter.png",
		"characters/契约角色/portrait.png",
	}
	got := append([]string(nil), e.Images...)
	sort.Strings(got)
	if len(got) != len(wantSorted) {
		t.Fatalf("关联图数量不符: got %v want %v", got, wantSorted)
	}
	for i := range wantSorted {
		if got[i] != wantSorted[i] {
			t.Fatalf("关联图清单不符: got %v want %v", got, wantSorted)
		}
	}
	if e.Image != "characters/契约角色/portrait.png" {
		t.Fatalf("主图路径异常: %s", e.Image)
	}
	// 每张都要真的落盘且非空(否则详情弹窗拿到的是死链接)
	for _, rel := range got {
		st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("关联图未落盘 %s: %v", rel, err)
		}
		if st.Size() == 0 {
			t.Fatalf("关联图为 0 字节: %s", rel)
		}
	}
	// Images 必须持久化(重开库时详情弹窗仍要能看到全部图片)
	raw, err := os.ReadFile(filepath.Join(root, "characters", "契约角色", "asset.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed Entry
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("asset.json 应为合法 JSON: %v", err)
	}
	if len(parsed.Images) != len(wantSorted) {
		t.Fatalf("asset.json 里 Images 数量不符: got %v want %v", parsed.Images, wantSorted)
	}

	// 重抽路径:ClearRelated 必须把 4 张关联图全清掉,主图与条目本身留下
	l.ClearRelated("characters", "契约角色")
	cleared := 0
	for _, rel := range got {
		_, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if rel == e.Image {
			if statErr != nil {
				t.Errorf("ClearRelated 误删了主图: %v", statErr)
			}
			continue
		}
		cleared++
		if !os.IsNotExist(statErr) {
			t.Errorf("ClearRelated 未清掉 %s", rel)
		}
	}
	// 3 张多视角原图 + 转台图 + 三视图板 = 5 张（换脸后这些全是过期图，必须一起清；主图不动）
	if cleared != 5 {
		t.Errorf("应清掉 5 张关联图(3 多视角 + 转台 + 三视图板),实际 %d 张", cleared)
	}
	if _, err := os.Stat(filepath.Join(root, "characters", "契约角色", "asset.json")); err != nil {
		t.Errorf("ClearRelated 误删了 asset.json: %v", err)
	}
}

// TestPutKeepsExistingRelatedImages 重新入库(源图只有单张定妆照)时,
// 条目目录里**已经存在**的多视角图不能被丢弃。
//
// 回归 2026-09-12:库复用的项目、重抽后的再次入库、渲染前多视角补齐回写,
// 这三条链路调用 Put 时给的源图都只有定妆照一张。若 Images 只按源图收集,
// 库条目就会从「定妆照 + 3 视角 + 转台图」退化成「只有定妆照」——
// 详情弹窗的多视角凭空消失,后续项目复用也拿不到第二张参考。
func TestPutKeepsExistingRelatedImages(t *testing.T) {
	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	master := filepath.Join(src, "characters_char_c1.png")
	if err := os.WriteFile(master, []byte("portrait"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put("characters", "留图角色", "描述", "prompt", master, "p1", "krea2"); err != nil {
		t.Fatalf("首次 Put 失败: %v", err)
	}
	if got, _, ok := l.Match("characters", "留图角色"); !ok || len(got.Images) != 1 {
		t.Fatalf("首次入库应只有主图,实际 %+v", got)
	}
	// 模拟渲染前补齐多视角:库目录内直接落角度图(与生产同目录同命名规则)
	dir := filepath.Join(root, "characters", "留图角色")
	for _, f := range []string{"portrait_view_side.png", "portrait_view_back.png", "portrait_turnaround.png"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("view:"+f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(l.RelatedFiles("characters", "留图角色")); n != 3 {
		t.Fatalf("RelatedFiles 应看到 3 张固有角度图,实际 %d", n)
	}
	// 再次入库:源图仍只有定妆照
	e, err := l.Put("characters", "留图角色", "描述", "prompt", master, "p1", "library")
	if err != nil {
		t.Fatalf("再次 Put 失败: %v", err)
	}
	if len(e.Images) != 4 {
		t.Fatalf("重新入库后角度图被丢弃: %v", e.Images)
	}
	parsed, _, _ := l.Match("characters", "留图角色")
	if len(parsed.Images) != 4 {
		t.Fatalf("asset.json 里的 Images 应保留 4 张: %v", parsed.Images)
	}
}

// TestSetMetaKeepsImagesAndCount SetMeta 只改描述与提示词,图片清单/来源/复用计数
// 一个都不能动(否则详情弹窗里改一次描述就会把多视角参考和统计抹掉)。
func TestSetMetaKeepsImagesAndCount(t *testing.T) {
	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "characters_char_c2.png")
	if err := os.WriteFile(src, []byte("portrait"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put("characters", "元数据角色", "旧描述", "old prompt", src, "proj-x", "gacha"); err != nil {
		t.Fatal(err)
	}
	l.BumpUsed("characters", "元数据角色")
	if err := os.WriteFile(filepath.Join(root, "characters", "元数据角色", "portrait_turnaround.png"), []byte("turn"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := l.SetMeta("characters", "元数据角色", "新描述", "new prompt")
	if err != nil {
		t.Fatalf("SetMeta 失败: %v", err)
	}
	if e.Description != "新描述" || e.ImagePrompt != "new prompt" {
		t.Fatalf("描述/提示词未更新: %+v", e)
	}
	if e.Source != "gacha" || e.SourceProject != "proj-x" || e.UsedCount != 1 {
		t.Fatalf("来源/复用计数被改动: %+v", e)
	}
	if e.Image != "characters/元数据角色/portrait.png" {
		t.Fatalf("主图路径被改动: %s", e.Image)
	}
}

// TestPutFromEntryDirKeepsGeneratedViews 源图与条目目录是**同一个目录**时,
// 重新入库必须保住该目录里刚生成的角度图。
//
// 回归 2026-09-12(真机实证):「补全多视角」由库自己生成 3 帧 + 转台图后落在条目
// 目录内,再调 Put(源图 = 条目自己的 portrait.png)刷新图片清单。若实现是
// 「先清空目录里的角度图再按源图拷」,刚生成的四张会被自己删掉、拷贝时源文件已
// 不存在 —— 接口返回 200、日志写「补全多视角完成」,而条目里只剩一张定妆照,
// 用户看到的就是"点了补全、还是单张正面照"。
func TestPutFromEntryDirKeepsGeneratedViews(t *testing.T) {
	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "portrait_src.png")
	if err := os.WriteFile(src, []byte("portrait"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put("characters", "自生成角色", "描述", "prompt", src, "proj", "upload"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "characters", "自生成角色")
	for _, f := range []string{
		"portrait_view_threequarter.png", "portrait_view_side.png",
		"portrait_view_back.png", "portrait_turnaround.png",
	} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("view:"+f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	master, _, ok := l.Match("characters", "自生成角色")
	if !ok {
		t.Fatal("条目应存在")
	}
	// 这就是 libraryViews 的收尾动作:用条目自己的主图再 Put 一次刷新清单
	e, err := l.Put("characters", "自生成角色", master.Description, master.ImagePrompt,
		filepath.Join(root, filepath.FromSlash(master.Image)), "", "upload")
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if len(e.Images) != 5 {
		t.Fatalf("刚生成的角度图被自己删掉了: got %v", e.Images)
	}
	for _, f := range []string{
		"portrait_view_threequarter.png", "portrait_view_side.png",
		"portrait_view_back.png", "portrait_turnaround.png",
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("角度图应仍在磁盘上 %s: %v", f, err)
		}
	}
	// 角度图还在,渲染侧推导第二张参考的契约就必须命中
	if !containsStr(e.Images, "characters/自生成角色/portrait_turnaround.png") {
		t.Errorf("Images 缺少转台图: %v", e.Images)
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestFindMatchesSameCharacterUnderDifferentNames 同一角色换个叫法必须复用同一条目。
//
// 回归 2026-09-12(用户报「相同角色无中生有多产生出定妆照,名称不一致和名称一致
// 都有出现」):分集分镜由 LLM 独立撰写,同一角色会换称呼 —— 实测同一本小说里机关人
// 在 第01集叫「机关人（一三七四一号）」、第02集叫「阿枢（一三七四一号）」、
// 第03集叫「机关人（一三七四一）」,肩上小人也有三种叫法。按名精确匹配会为同一个人
// 反复出定妆照(库里的 19 个角色里至少 5 个是同人分身)。
func TestFindMatchesSameCharacterUnderDifferentNames(t *testing.T) {
	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "p.png")
	if err := os.WriteFile(src, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	const desc = "少年形制的梨木机关人，打磨极细的哑光木面，刀刻平直细眉、挺直鼻梁、唇线抿成一条极克制的直线；耳后到颈侧刻着一行极小的小楷编号"
	if _, err := l.Put("characters", "机关人（一三七四一号）", desc, "prompt", src, "p1", "krea2"); err != nil {
		t.Fatal(err)
	}
	// 上装一个不同角色,用于验证"不许误并"
	const desc2 = "十六七岁的赶车少年帮工，个子已长开而肩膀还窄；长圆脸，晒黑的皮肤，短而毛糙的黑发，直而粗的眉毛"
	if _, err := l.Put("characters", "石柱子", desc2, "prompt", src, "p1", "krea2"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, desc, want string
	}{
		{"阿枢（一三七四一号）", desc, "机关人（一三七四一号）"},                   // 名字全变、描述逐字相同
		{"机关人（一三七四一）", desc, "机关人（一三七四一号）"},                   // 序号尾少一字
		{"机关人", desc, "机关人（一三七四一号）"},                          // 去掉括号内容
		{"机关人（一三七四一号）", "完全不同的描述甲甲甲甲甲甲甲甲甲甲甲甲", "机关人（一三七四一号）"}, // 名字精确匹配优先
	}
	for _, c := range cases {
		e, _, ok := l.Find("characters", c.name, c.desc)
		if !ok {
			t.Errorf("Find(%q) 未命中(应判为同一角色)", c.name)
			continue
		}
		if e.Name != c.want {
			t.Errorf("Find(%q) 命中 %q, want %q", c.name, e.Name, c.want)
		}
	}
	// 别名必须落盘:下次按名字直接命中,界面上也能看出是同一角色
	e, _, ok := l.Find("characters", "阿枢（一三七四一号）", desc)
	if !ok || len(e.Aliases) == 0 {
		t.Fatalf("判定为同一角色后应登记别名,实际 ok=%v aliases=%v", ok, e.Aliases)
	}
	reopened, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, _, ok := reopened.Find("characters", "阿枢（一三七四一号）", "短"); !ok || got.Name != "机关人（一三七四一号）" {
		t.Errorf("别名应持久化(短描述也要能按别名命中):ok=%v got=%v", ok, got)
	}
	// 不同角色不得被合并:石柱子描述与机关人毫无相似处
	if got, _, ok := l.Find("characters", "赶车的少年", desc2); !ok || got.Name != "石柱子" {
		t.Errorf("同人判定只应认同一角色:ok=%v got=%v", ok, got)
	}
	if got, _, ok := l.Find("characters", "机关人", desc2); ok && got.Name == "石柱子" {
		t.Errorf("不同角色不该被描述相似度误并:got=%v", got.Name)
	}
}

// TestNormNameAndDescSimilar 归一化与相似度的边界。
func TestNormNameAndDescSimilar(t *testing.T) {
	if normName("机关人（一三七四一号）") != "机关人" {
		t.Errorf("括号内容应丢弃: %q", normName("机关人（一三七四一号）"))
	}
	if normName("阿枢肩上小人") == normName("石柱子") {
		t.Error("不同名字归一化后不应相等")
	}
	if normName("肩上小人的") == normName("肩上小人") {
		// 「的」被去掉后相等 —— 这是刻意行为
	} else {
		t.Errorf("「的」应被忽略: %q vs %q", normName("肩上小人的"), normName("肩上小人"))
	}
	long := "少年形制的梨木机关人，打磨极细的哑光木面，刀刻平直细眉、挺直鼻梁、唇线抿成一条极克制的直线；耳后到颈侧刻着一行极小的小楷编号"
	if d := descSimilar(long, long); d < 0.99 {
		t.Errorf("同一段描述相似度应为 1,实际 %.2f", d)
	}
	if d := descSimilar(long, "十六七岁的赶车少年帮工，个子已长开而肩膀还窄；长圆脸，晒黑的皮肤"); d >= 0.6 {
		t.Errorf("不同角色描述相似度必须低于阈值,实际 %.2f", d)
	}
	if d := descSimilar("太短", "也短"); d != 0 {
		t.Errorf("过短描述不应参与判断,实际 %.2f", d)
	}
}
