package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// refman_testAssets 造一个"有定妆照 + 有拼合转身图"的角色,外加一个场景。
func refmanAssets(t *testing.T) (dir string, shot project.Shot, assets map[string]project.Asset) {
	t.Helper()
	dir = t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("q.png")
	write("q_turnaround.png")
	write("tent.png")
	assets = map[string]project.Asset{
		"char:char_q": {ID: "char:char_q", Name: "顾青禾", File: "q.png"},
		"scene:tent":  {ID: "scene:tent", Name: "旧城派送站棚", File: "tent.png"},
	}
	shot = project.Shot{ID: 6, Mode: "ref2va", Characters: []string{"char_q"}, Scene: "tent"}
	return dir, shot, assets
}

// TestRefPlanRoles 角色归属:定妆照是权威、拼合图是"同一个角色的另一组角度"、场景永远最后。
func TestRefPlanRoles(t *testing.T) {
	dir, shot, assets := refmanAssets(t)
	refs, roles, missing, _ := RefPlan(dir, shot, assets, false)
	if len(missing) != 0 {
		t.Fatalf("不该报缺失,实际 %v", missing)
	}
	if len(refs) != 3 || len(roles) != 3 {
		t.Fatalf("1 角色(含拼合图)+ 1 场景 = 3 张,实际 refs=%d roles=%d", len(refs), len(roles))
	}
	want := []string{"character_front", "character_angles", "scene"}
	for i, w := range want {
		if roles[i].Kind != w {
			t.Errorf("<Picture %d> 身份应为 %s,实际 %s", i+1, w, roles[i].Kind)
		}
		if roles[i].Picture != i+1 {
			t.Errorf("<Picture 编号必须从 1 连续,实际第 %d 项写的 %d", i+1, roles[i].Picture)
		}
	}
	// engine 侧 Kind 不能变(引擎按 character/scene 决定上传与首帧语义)
	if refs[0].Kind != "character" || refs[1].Kind != "character" || refs[2].Kind != "scene" {
		t.Errorf("engine.RefImage.Kind 必须仍是 character/character/scene,实际 %s/%s/%s",
			refs[0].Kind, refs[1].Kind, refs[2].Kind)
	}
	if filepath.Base(refs[0].Path) != "q.png" || filepath.Base(refs[1].Path) != "q_turnaround.png" {
		t.Errorf("定妆照必须在拼合图之前,实际 %s / %s", refs[0].Path, refs[1].Path)
	}
	// 旧签名(兼容壳)必须与 RefPlan 给出同一组图
	back, _, _ := AssembleShotRefs(dir, shot, assets, false)
	if len(back) != len(refs) {
		t.Errorf("AssembleShotRefs 兼容壳与 RefPlan 不一致:%d vs %d", len(back), len(refs))
	}
}

// TestRefManifestKillsTheMiscount 本次事故的闸门:LLM 猜"两张图、场景是 <Picture 2>",
// 实际是"三张图、第 2 张是主角自己的拼合图" —— 权威清单必须把这件事说死。
func TestRefManifestKillsTheMiscount(t *testing.T) {
	dir, shot, assets := refmanAssets(t)
	_, roles, _, _ := RefPlan(dir, shot, assets, false)
	m := RefManifest(shot, roles)
	for _, want := range []string{
		"Exactly 3 picture(s) are attached", // 张数由渲染器说
		"<Picture 2> = 顾青禾 AGAIN",           // 第 2 张是同一个角色
		"multi-view strip (several angles",  // 它是拼合图
		"NOT a scene",                       // 明确否掉"场景"这个误读
		"NEVER take the face from it",       // 不允许从拼合图取脸
		"<Picture 3> = 旧城派送站棚 (the SCENE",   // 场景才是最后一张
		"OVERRIDES any picture count",       // 覆盖上文
		"that sentence is WRONG",            // 上文说错了也要照清单
	} {
		if !strings.Contains(m, want) {
			t.Errorf("权威清单缺少 %q:\n%s", want, m)
		}
	}
}

// TestEnsureRefManifestIsIdempotentAndLossless 幂等 + 不丢原文 + 无参考图时不动原文。
func TestEnsureRefManifestIsIdempotentAndLossless(t *testing.T) {
	dir, shot, assets := refmanAssets(t)
	_, roles, _, _ := RefPlan(dir, shot, assets, false)
	base := "subject_definitions:\n<Subject 1> is Gu Qinghe …\n\nTwo pictures are attached in total.\n"
	got := EnsureRefManifest(base, shot, roles)
	if !strings.Contains(got, "Two pictures are attached in total.") {
		t.Error("原有提示词内容不得丢失")
	}
	if n := strings.Count(EnsureRefManifest(got, shot, roles), refManifestKey); n != 1 {
		t.Errorf("权威清单必须幂等,实际 %d 次", n)
	}
	if plain := EnsureRefManifest(base, shot, nil); plain != base {
		t.Error("没有参考图(t2va)时不得改动提示词")
	}
}

// TestEnsureRefManifestBindsSubjectToItsPicture 官方全参考模式的**绑定句式**必须落在
// subject_definitions 里(2026-09-26 真机实测:角色仍漂移)。
//
// 官方 ref-en.txt §2.1/§2.2 要求:`<Subject 1> is the young woman **in <Picture 1>**, …`,
// 并且"仅用于定义角色/场景/服装/风格的图片**不要**单列 <Picture N>,而要在对应
// <Subject N> 的定义里引用它"。我们原先只写"以附图为准"(泛指),模型于是照**文字**重画 ——
// 同一个沈砚在不同镜里成了不同的人。这条用例钉住"名字行被绑到它实际那张图上"。
func TestEnsureRefManifestBindsSubjectToItsPicture(t *testing.T) {
	dir, shot, assets := refmanAssets(t)
	_, roles, _, _ := RefPlan(dir, shot, assets, false)
	name := roles[0].Name
	base := "subject_definitions:\n<Subject 1> is " + name + "（human）— 十八九岁、男、体型：瘦 —— 以附图为准。\n"
	got := EnsureRefManifest(base, shot, roles)
	if !strings.Contains(got, "<Subject 1> is "+name+" in <Picture 1>") {
		t.Fatalf("角色行必须绑到它实际那张图(官方句式 in <Picture K>),实际:\n%s", got)
	}
	if !strings.Contains(got, "（human）— 十八九岁") {
		t.Error("绑定不得吃掉原文(名字之后的描述必须原样保留)")
	}
	// 幂等:再跑一次不得出现两个 in <Picture
	if n := strings.Count(EnsureRefManifest(got, shot, roles), " in <Picture 1>"); n != 1 {
		t.Errorf("绑定必须幂等,实际出现 %d 次", n)
	}
	// 对不上的 subject(讲的不是这些参考图里的对象)不许硬塞编号
	other := "subject_definitions:\n<Subject 1> is 路人甲 — 无参考图。\n"
	if out := EnsureRefManifest(other, shot, roles); strings.Contains(out, "<Subject 1> is 路人甲 in <Picture") {
		t.Errorf("名字对不上时不许猜编号:\n%s", out)
	}
}

// TestOnScreenTextPolicyForbidsOwnNotes 画面文字规则必须点名"提示词自己的说明文字不是画面内容",
// 并禁止烧字幕/系统面板(2026-09-26 真机实测:成片里出现【全城第一骂名状态】这种系统提示字幕,
// 以及把我们自己的声线说明行/画风说明行烧成英文乱码字幕)。
func TestOnScreenTextPolicyForbidsOwnNotes(t *testing.T) {
	got := EnsureOnScreenTextRule("subject_definitions:\n<Subject 1> is 沈砚。\n")
	for _, want := range []string{
		"NEVER turn this prompt's own text", // 自己的说明文字不许画进画面
		"voice descriptions",                // 点名被念出来的那一类行
		"No burned-in subtitles",            // 不许烧字幕
		"post-production",                   // 字幕/系统面板归后期
	} {
		if !strings.Contains(got, want) {
			t.Errorf("画面文字规则缺少「%s」:\n%s", want, got)
		}
	}
	if !strings.Contains(got, onScreenTextMarker) {
		t.Error("幂等判据标记串丢了 —— 会重复叠加")
	}
}

// TestRefManifestCompactStripsSayWhichCellIsAuthoritative 去冗余模式只喂拼合图时,
// 必须点明"最左格就是定妆照、它才是权威" —— 否则模型会在四格之间挑一张脸。
func TestRefManifestCompactStripsSayWhichCellIsAuthoritative(t *testing.T) {
	dir, shot, assets := refmanAssets(t)
	refs, roles, _, _ := RefPlan(dir, shot, assets, true)
	if len(refs) != 2 {
		t.Fatalf("去冗余后应为 拼合图 + 场景 = 2 张,实际 %d", len(refs))
	}
	if roles[0].Kind != "character_front_in_strip" {
		t.Fatalf("去冗余模式的首图身份应标记为 character_front_in_strip,实际 %s", roles[0].Kind)
	}
	m := RefManifest(shot, roles)
	if !strings.Contains(m, "LEFTMOST cell is that character's front sheet") {
		t.Errorf("必须点明最左格才是定妆照:\n%s", m)
	}
}

// TestRefPlanAndManifestArePairedAtEveryCallSite 结构性闸门:
// **每一个装配参考图的地方都必须同时写权威清单**。
//
// 为什么用源码扫描而不是行为测试:漏掉的是"某一条出像素路径忘了调用",行为测试
// 只覆盖它自己被调用的那条路径。这与 frontend_guard 系列闸门同一思路 ——
// 同一职责散在多处时,靠断言"每处都在"来防漏。
func TestRefPlanAndManifestArePairedAtEveryCallSite(t *testing.T) {
	files := []string{
		"stages_render.go", "upgrade2k.go",
		"../server/handlers_enhance.go",
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读不到 %s: %v", f, err)
		}
		src := string(b)
		if n := strings.Count(src, "AssembleShotRefs("); n > 0 {
			t.Errorf("%s 仍在使用旧接口 AssembleShotRefs(%d 处)—— 它拿不到每张图的身份,"+
				"必须换成 RefPlan,否则写不出权威清单", f, n)
		}
		plans := strings.Count(src, "RefPlan(")
		mans := strings.Count(src, "EnsureRefManifest(")
		if plans == 0 && mans == 0 {
			t.Errorf("%s 既没装配参考图也没写清单 —— 若这里确实出像素,就是漏了权威清单", f)
			continue
		}
		if plans != mans {
			t.Errorf("%s:RefPlan 用了 %d 次、EnsureRefManifest 用了 %d 次 —— "+
				"装配了参考图却不写权威清单的路径,会把拼合图当场景图(见 refmanifest.go)", f, plans, mans)
		}
	}
}

// TestRefPlanDropsAnglesForCloseShots 近景/特写不附多视角拼合图(2026-09-25)。
//
// 拼合图每格只剩约 1/4 像素(refs.go 顶部记过:只喂它时"服装对、脸变了"),而特写里
// 画面几乎只有一张脸 —— 其它角度用不上,那 4 个低分辨率的脸却仍在参与取脸。
// 中景以上才需要其它角度,所以按景别分流。
func TestRefPlanDropsAnglesForCloseShots(t *testing.T) {
	dir, shot, assets := refmanAssets(t)
	cases := []struct {
		size      string
		wantRefs  int
		wantKinds []string
	}{
		{"特写", 2, []string{"character_front", "scene"}},
		{"近景", 2, []string{"character_front", "scene"}},
		{"中景", 3, []string{"character_front", "character_angles", "scene"}},
		{"全景", 3, []string{"character_front", "character_angles", "scene"}},
		{"", 3, []string{"character_front", "character_angles", "scene"}}, // 未知景别按保留
	}
	for _, tc := range cases {
		s := shot
		s.ShotSize = tc.size
		refs, roles, _, _ := RefPlan(dir, s, assets, false)
		if len(refs) != tc.wantRefs {
			t.Errorf("景别 %q:期望 %d 张,实际 %d", tc.size, tc.wantRefs, len(refs))
			continue
		}
		for i, want := range tc.wantKinds {
			if roles[i].Kind != want {
				t.Errorf("景别 %q 的第 %d 张身份应为 %s,实际 %s", tc.size, i+1, want, roles[i].Kind)
			}
		}
		// 权威清单必须跟着实际张数走(否则又是一次"提示词说 3 张、实际 2 张")
		m := RefManifest(s, roles)
		if !strings.Contains(m, fmt.Sprintf("Exactly %d picture(s)", tc.wantRefs)) {
			t.Errorf("景别 %q 的清单张数不对:\n%s", tc.size, m)
		}
		if tc.wantRefs == 2 && strings.Contains(m, "multi-view strip") {
			t.Errorf("景别 %q 既然没附拼合图,清单里就不该再提它:\n%s", tc.size, m)
		}
	}
}

// TestWantsAnglesStrip 判据本体:按中文景别词分流,未知一律保留。
func TestWantsAnglesStrip(t *testing.T) {
	for _, s := range []string{"特写", "大特写", "近景"} {
		if wantsAnglesStrip(s) {
			t.Errorf("%q 不该附拼合图", s)
		}
	}
	for _, s := range []string{"中景", "全景", "远景", "中近景", "full shot", ""} {
		if !wantsAnglesStrip(s) {
			t.Errorf("%q 应保留拼合图(宁可多一张,不要把需要的镜头降级)", s)
		}
	}
}
