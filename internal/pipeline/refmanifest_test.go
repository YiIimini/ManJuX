package pipeline

import (
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
	refs, roles, missing := RefPlan(dir, shot, assets, false)
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
	back, _ := AssembleShotRefs(dir, shot, assets, false)
	if len(back) != len(refs) {
		t.Errorf("AssembleShotRefs 兼容壳与 RefPlan 不一致:%d vs %d", len(back), len(refs))
	}
}

// TestRefManifestKillsTheMiscount 本次事故的闸门:LLM 猜"两张图、场景是 <Picture 2>",
// 实际是"三张图、第 2 张是主角自己的拼合图" —— 权威清单必须把这件事说死。
func TestRefManifestKillsTheMiscount(t *testing.T) {
	dir, shot, assets := refmanAssets(t)
	_, roles, _ := RefPlan(dir, shot, assets, false)
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
	_, roles, _ := RefPlan(dir, shot, assets, false)
	base := "subject_definitions:\n<Subject 1> is Gu Qinghe …\n\nTwo pictures are attached in total.\n"
	got := EnsureRefManifest(base, RefManifest(shot, roles))
	if !strings.Contains(got, "Two pictures are attached in total.") {
		t.Error("原有提示词内容不得丢失")
	}
	if n := strings.Count(EnsureRefManifest(got, RefManifest(shot, roles)), refManifestKey); n != 1 {
		t.Errorf("权威清单必须幂等,实际 %d 次", n)
	}
	if plain := EnsureRefManifest(base, ""); plain != base {
		t.Error("没有参考图(t2va)时不得改动提示词")
	}
}

// TestRefManifestCompactStripsSayWhichCellIsAuthoritative 去冗余模式只喂拼合图时,
// 必须点明"最左格就是定妆照、它才是权威" —— 否则模型会在四格之间挑一张脸。
func TestRefManifestCompactStripsSayWhichCellIsAuthoritative(t *testing.T) {
	dir, shot, assets := refmanAssets(t)
	refs, roles, _ := RefPlan(dir, shot, assets, true)
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
