package pipeline

import (
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 「同角色的多张卡当一个人」在管线侧的口径(技能侧清单 §4.2)。
//
// 三处必须归族:统计(人数)、接缝判据(换档不是换人)、资产库复用(跨集复用同一档,
// 而不是把它当"新角色重新定妆")。第四处——资产范围——**刻意不归族**,见 asset_scope.go。

// 人数统计:变体不额外计一个新角色(《翻篇》12 个角色 21 张卡)。
func TestCharFamilyCountIgnoresVariants(t *testing.T) {
	cases := []struct {
		name string
		ids  []string
		want int
	}{
		{"同一人的四档算一个人", []string{"char_01v1", "char_01v2", "char_01v3", "char_01v4"}, 1},
		{"两个角色 + 各自的档", []string{"char_01v1", "char_01v4", "char_05v2", "char_05v3"}, 2},
		{"没有档位的普通角色各自算一个", []string{"char_02", "char_03"}, 2},
		{"空值与空白不计入", []string{"", "  ", "char_01v1"}, 1},
		{"空清单", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := charFamilyCount(tc.ids); got != tc.want {
				t.Fatalf("charFamilyCount(%v) = %d,期望 %d", tc.ids, got, tc.want)
			}
		})
	}
	// 与计划侧的口径必须同源(一个按 id,一个按 Character)
	chars := []project.Character{
		{ID: "char_01v1", Name: "翻篇·收容所"},
		{ID: "char_01v2", Name: "翻篇·进城"},
		{ID: "char_02", Name: "小喇叭"},
	}
	if a, b := charFamilyCount([]string{"char_01v1", "char_01v2", "char_02"}), project.CharFamilyCount(chars); a != b {
		t.Errorf("两个口径必须一致:ids=%d chars=%d", a, b)
	}
}

// 接缝:同一角色**换造型档**不该被当成"换了人"。
//
// 现有判据只看场景(见 seamBlockers 的说明:接缝要的是空间连续,不是人相同),
// 所以变体切换本来就不断缝;这条测试把"将来若重新引入按人判"的底线钉住 ——
// 判据必须走 charactersIntersect(家族口径),而不是裸 id 相等。
func TestSeamTreatsVariantsAsSamePerson(t *testing.T) {
	plan := &project.Plan{
		Scenes: []project.Scene{{ID: "s1", Name: "收容所"}},
		Shots: []project.Shot{
			{ID: 1, Scene: "s1", Characters: []string{"char_01v1"}},
			{ID: 2, Scene: "s1", Characters: []string{"char_01v2"}}, // 同一人换了一身衣服
			{ID: 3, Scene: "s1", Characters: []string{"char_02"}},   // 真的换人
		},
	}
	prev, _ := shotByID(plan, 1)
	switched, _ := shotByID(plan, 2)
	if !seamAllowed(plan, switched, prev, 0) {
		t.Errorf("同一角色换造型档不该断接缝(那是同一场戏里的连续镜头),原因:%v",
			seamBlockers(plan, switched, prev, 0))
	}
	if !charactersIntersect(prev.Characters, switched.Characters) {
		t.Error("同一个人换了一档,角色集合必须判为有交集(否则将来按人判会白断接缝)")
	}
	other, _ := shotByID(plan, 3)
	if charactersIntersect(switched.Characters, other.Characters) {
		t.Error("不同角色之间不该判为有交集")
	}
	if charactersIntersect([]string{""}, []string{""}) {
		t.Error("空 id 不算交集")
	}
	if !charactersIntersect([]string{"char_01v4"}, []string{"char_1"}) {
		t.Error("编号写法不同(前导零)也要认出是同一个人")
	}
}

// 跨集复用:**同一档**在库里就复用(不重出),而**另一档**不许拿它的像素顶替
// (那会把"换了服装"这件事抹掉 —— §4.1 的整条链就白做了)。
func TestLibraryReuseAcrossEpisodesRespectsVariants(t *testing.T) {
	rig := newSheetRig(t)
	src := filepath.Join(t.TempDir(), "ep01-归零.png")
	writeTestPNG(t, src, 64, 96)
	const desc = "枕头大的圆滚滚土黄短毛小狗,右耳一道豁口,胸口一小撮白,脖子上挂着磨平了字的铜狗牌"
	if _, err := rig.lib.Put("characters", "翻篇·归零", desc, "PROMPT-归零", src, "ep01", "krea2"); err != nil {
		t.Fatal(err)
	}
	// 同一档:命中并复用像素
	r, ok := rig.c.reuseFromLibrary(rig.proj.ID, "characters", "char_01v4", "翻篇·归零", desc)
	if !ok || r.Name != "翻篇·归零" {
		t.Fatalf("同一档的库条目必须复用(跨集不必重出定妆照),实际 ok=%v name=%q", ok, r.Name)
	}
	// 另一档(库里有的是「归零」,本集要「收容所」):不许复用它的像素
	if r, ok := rig.c.reuseFromLibrary(rig.proj.ID, "characters", "char_01v1", "翻篇·收容所", desc); ok {
		t.Fatalf("不同造型档之间不许复用像素,实际复用了「%s」—— 那会让本档的服装永远不出现在画面里", r.Name)
	}
	// 同一角色的另一种**叫法**(不涉及档名):照旧复用
	if _, err := rig.lib.Put("characters", "沈砚", "十八九岁的少年,清瘦长脸,细长单眼皮,短发低马尾,靛蓝布长衫", "P", src, "ep01", "krea2"); err != nil {
		t.Fatal(err)
	}
	if r, ok := rig.c.reuseFromLibrary(rig.proj.ID, "characters", "char_09", "阿枢", "十八九岁的少年,清瘦长脸,细长单眼皮,短发低马尾,靛蓝布长衫"); !ok || r.Name != "沈砚" {
		t.Errorf("同一角色的另一种叫法必须照旧复用,实际 ok=%v name=%q", ok, r.Name)
	}
}
