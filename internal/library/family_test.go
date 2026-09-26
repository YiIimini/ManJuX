package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 造型家族(§4.2)在资产库侧的契约。
//
// 技能侧把"造型随剧情变"落成一角色多张卡:主卡 + `翻篇·归零` / `糖球·雨夜` …
// 库这边于是会同时躺着同一个人的好几档定妆照。守三件事:
//  1. **像素不得跨档复用**:变体卡共享同一份形象卡描述(实测 Jaccard ≈ 0.9),
//     Find 的"描述相似度"判据若照旧生效,会把「翻篇·归零」的定妆照换成
//     「翻篇·收容所」那张 —— 造型档永远不出现在画面里;
//  2. 同一角色的**另一种叫法**照旧合并(老行为一点不能退);
//  3. FindInFamily 只给"派生起点"(另一档的图只能当图生图参考),且必须主卡优先、绝不跨族。
const familyDesc = "枕头大的圆滚滚土黄短毛小狗，右耳一道豁口，胸口一小撮白，脖子上挂着磨平了字的铜狗牌；" +
	"造型档：收容所（场 1–7）毛打结、左后腿缠旧纱布、牌子磨旧"

// variantDesc 同一份形象卡,只有尾部「造型档」那一行不同(真机数据就是这样)。
const variantDesc = "枕头大的圆滚滚土黄短毛小狗，右耳一道豁口，胸口一小撮白，脖子上挂着磨平了字的铜狗牌；" +
	"造型档：归零 毛重新长齐、铜牌被摘下收进口袋"

func newFamilyLib(t *testing.T) *Library {
	t.Helper()
	root := t.TempDir()
	l, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "p.png")
	if err := os.WriteFile(src, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put("characters", "翻篇·收容所", familyDesc, "PROMPT-收容所", src, "ep01", "krea2"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put("characters", "糖球·雨夜", variantDesc, "PROMPT-糖球", src, "ep01", "krea2"); err != nil {
		t.Fatal(err)
	}
	return l
}

// 同一角色的**另一档**绝不能被 Find 当成"同一张卡"复用像素。
func TestFindDoesNotCrossVariantCards(t *testing.T) {
	l := newFamilyLib(t)
	// 逐字相同的描述(真机数据就是同一份形象卡):按描述相似度必然 ≥0.8 ——
	// 而它必须**不命中**,否则变体卡的定妆照会变成另一档的衣服。
	if e, _, ok := l.Find("characters", "翻篇·归零", familyDesc); ok {
		t.Fatalf("「翻篇·归零」不得命中「%s」:那是同一个人**另一套服装/状态**,"+
			"直接复用像素会让造型档永远不出现在画面里", e.Name)
	}
	// 同一张卡(名字同档)照旧命中 —— 分档守卫不能把正常复用一起挡掉
	if e, _, ok := l.Find("characters", "翻篇·收容所", familyDesc); !ok || e.Name != "翻篇·收容所" {
		t.Fatalf("同一张卡必须照旧命中,实际 ok=%v name=%v", ok, e.Name)
	}
	// 名字没写档名的一方同样是"可能另一档":不许拿带档名的条目像素去顶
	if e, _, ok := l.Find("characters", "翻篇", familyDesc); ok {
		t.Fatalf("没写档名的「翻篇」不得命中带档名的条目「%s」(无法确认是不是同一档)", e.Name)
	}
}

// 同一角色的另一种叫法(不涉及造型档)必须照旧合并 —— 老行为不能因为分档守卫退化。
func TestFindStillMergesAliasesOfSameCharacter(t *testing.T) {
	l := newFamilyLib(t)
	// ① 同一档、只是空格/标点写法不同:归一化后相等 → 照旧命中
	if e, _, ok := l.Find("characters", "翻篇 · 收容所", variantDesc); !ok || e.Name != "翻篇·收容所" {
		t.Errorf("同一张卡的写法差异必须照旧命中,实际 ok=%v name=%v", ok, nameOf(e, ok))
	}
	// ② 完全不涉及造型档的角色换个叫法:描述相似度这条老行为照旧生效
	// (回归 2026-09-12「相同角色无中生有多产生出定妆照」那条)
	const desc = "少年形制的梨木机关人，打磨极细的哑光木面，刀刻平直细眉、挺直鼻梁、唇线抿成一条极克制的直线"
	src := filepath.Join(t.TempDir(), "p2.png")
	if err := os.WriteFile(src, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put("characters", "机关人（一三七四一号）", desc, "PROMPT-机关人", src, "ep02", "krea2"); err != nil {
		t.Fatal(err)
	}
	if e, _, ok := l.Find("characters", "阿枢（一三七四一号）", desc); !ok || e.Name != "机关人（一三七四一号）" {
		t.Errorf("同一角色的另一种叫法仍应命中,实际 ok=%v name=%v", ok, nameOf(e, ok))
	}
}

// nameOf 断言文案里安全地取条目名(未命中时条目为 nil,直接取属性会 panic)。
func nameOf(e *Entry, ok bool) string {
	if !ok || e == nil {
		return "<未命中>"
	}
	return e.Name
}

// FindInFamily 给的是"派生起点":同族、主卡优先、且绝不跨族。
func TestFindInFamily(t *testing.T) {
	l := newFamilyLib(t)
	e, img, ok := l.FindInFamily("characters", "翻篇·归零")
	if !ok {
		t.Fatal("同族条目存在时必须给得出派生起点(否则变体卡只能退回文生图,同一角色的脸会漂)")
	}
	if e.Name != "翻篇·收容所" {
		t.Errorf("同族里应挑最靠前的条目(主卡优先),实际 %q", e.Name)
	}
	if img == "" {
		t.Error("必须给出图片绝对路径(图生图要拿它当 --init-image)")
	}
	if e.Family != "翻篇" {
		t.Errorf("条目的 family 字段应是基名,实际 %q", e.Family)
	}
	// 主卡存在时,即使还有别的档,也优先给主卡
	src := filepath.Join(t.TempDir(), "m.png")
	if err := os.WriteFile(src, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Put("characters", "翻篇", familyDesc, "PROMPT-主卡", src, "ep01", "krea2"); err != nil {
		t.Fatal(err)
	}
	if got, _, ok := l.FindInFamily("characters", "翻篇·归零"); !ok || got.Name != "翻篇" {
		t.Errorf("主卡(名字不带档名)必须优先于任何变体档,实际 ok=%v name=%v", ok, nameOf(got, ok))
	}
	// 同族的另一档也要能找到(库里只有「糖球·雨夜」,求「糖球·决裂」)
	if got, _, ok := l.FindInFamily("characters", "糖球·决裂"); !ok || got.Name != "糖球·雨夜" {
		t.Errorf("应挑同族条目,实际 ok=%v name=%v", ok, nameOf(got, ok))
	}
	// 库里没有的族 → 老实返回 false,不许拿别人的图凑数
	if _, _, ok := l.FindInFamily("characters", "老墙·认错"); ok {
		t.Error("库中没有这一族时必须返回 false(调用方据此降级并播报)")
	}
	// 空名 / 非法分类不 panic
	if _, _, ok := l.FindInFamily("characters", ""); ok {
		t.Error("空名不该命中")
	}
	if _, _, ok := l.FindInFamily("不存在分类", "翻篇·归零"); ok {
		t.Error("非法分类不该命中")
	}
}

// 列表与详情都要能看出"主卡与变体卡是一家人"(前端据此分组显示"一个人 + 他的 N 个造型档")。
func TestListAndMatchExposeFamily(t *testing.T) {
	l := newFamilyLib(t)
	byName := map[string]string{}
	for _, e := range l.List()["characters"] {
		byName[e.Name] = e.Family
	}
	if byName["翻篇·收容所"] != "翻篇" || byName["糖球·雨夜"] != "糖球" {
		t.Errorf("列表里每条都要带家族基名,实际 %v", byName)
	}
	e, _, ok := l.Match("characters", "翻篇·收容所")
	if !ok || e.Family != "翻篇" {
		t.Errorf("详情(Match)也要带家族基名,实际 ok=%v family=%q", ok, e.Family)
	}
	// family 只是**返回值**,不落盘:名字改了基名跟着变,存下来只会与名字不同步
	// (条目目录名走 safeName —— 它会把「·」去掉,所以路径要由 EntryDir 推)
	dir, err := l.EntryDir("characters", "翻篇·收容所")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "asset.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\"family\"") {
		t.Errorf("family 不该被写进 asset.json(它是按名字现算的):%s", string(raw))
	}
	if l2, err := New(l.Root()); err != nil {
		t.Fatal(err)
	} else if got, _, ok := l2.Match("characters", "翻篇·收容所"); !ok || got.Family != "翻篇" {
		t.Errorf("重开后仍要能现算家族基名,实际 ok=%v family=%q", ok, got.Family)
	}
}
