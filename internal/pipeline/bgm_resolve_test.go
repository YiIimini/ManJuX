package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/library"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 配乐的**三级解析**与"生成→入库→复用"闭环（2026-09-28 用户要求）。
//
// 用户原话："生成的背景音乐也要做成资产库里进行管理，后续存在相同的可以直接复用。"
// 这里测的是这条链的**逻辑**（不依赖 13.68 GB 的本地模型）：用注入的生成器造一个音频文件，
// 走完"库里没有 ⇒ 生成 ⇒ 入库 ⇒ 下次同需求直接命中"。
func TestBGMGenerateThenReuse(t *testing.T) {
	dir := t.TempDir()
	lib, err := library.New(filepath.Join(dir, "lib"))
	if err != nil {
		t.Fatal(err)
	}
	c := &Coordinator{lib: lib}

	brief := "低沉竹笛与弦乐，压抑，慢速，碑下独白段落"
	proj := &project.Project{ID: "ep01"}
	proj.Config.Params.Assemble.BGMDescription = brief
	plan := &project.Plan{Shots: []project.Shot{{ID: 1, Duration: 60}, {ID: 2, Duration: 60}}}

	// 生成器：造一个真文件（Put 只校验非空，不解码 —— 这正是音频能进同一个库的原因）
	calls := 0
	musicGenHook = func(ctx context.Context, tags string, seconds float64) (string, error) {
		calls++
		if !strings.Contains(tags, "instrumental") || !strings.Contains(tags, "no vocals") {
			t.Errorf("tags 必须钉住纯器乐（H3 片段自带同期声，配乐带唱词会打架），实际 %q", tags)
		}
		if seconds < 40 || seconds > 180 {
			t.Errorf("生成时长应在 40–180 秒之间（铺底会自动循环），实际 %.0f", seconds)
		}
		p := filepath.Join(dir, "gen.mp3")
		if err := os.WriteFile(p, []byte("ID3FAKE-AUDIO"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p, nil
	}
	defer func() { musicGenHook = nil }()

	name := library.MusicEntryName(brief)

	// 第一次：库空 ⇒ 生成
	path, note := c.bgmPathOf(proj, plan)
	if path == "" {
		t.Fatalf("应生成配乐，实际没有（说明 %q）", note)
	}
	if calls != 1 {
		t.Fatalf("应调用生成器 1 次，实际 %d", calls)
	}
	if !strings.Contains(note, "已生成") || !strings.Contains(note, "入库") {
		t.Errorf("说明里要写清已生成并入库，实际 %q", note)
	}
	// 入库校验：条目存在、来源标记对
	e, _, ok := lib.Match("music", name)
	if !ok {
		t.Fatal("生成的配乐必须入库（否则下次还会重新生成）")
	}
	if e.Source != "ace-step" {
		t.Errorf("来源应记为 ace-step，实际 %q", e.Source)
	}

	// 第二次（同需求，换一种标点写法）：必须**直接复用**，不再调用生成器
	proj2 := &project.Project{ID: "ep02"}
	proj2.Config.Params.Assemble.BGMDescription = "低沉竹笛与弦乐, 压抑 ,慢速,碑下独白段落"
	path2, note2 := c.bgmPathOf(proj2, plan)
	if calls != 1 {
		t.Errorf("同需求第二次不该再生成，实际调用 %d 次", calls)
	}
	if path2 == "" {
		t.Fatalf("应复用库里的配乐，实际没有（说明 %q）", note2)
	}
	if !strings.Contains(note2, "复用") {
		t.Errorf("说明里要写清是复用，实际 %q", note2)
	}
}

// TestBGMExplicitFileWins 显式指定的文件永远优先（人指定的一律压过自动）。
func TestBGMExplicitFileWins(t *testing.T) {
	c := &Coordinator{}
	proj := &project.Project{ID: "ep01"}
	// 用**绝对路径**：相对路径要按项目目录解析，而这里没有 store（另一条测试覆盖降级）
	abs := filepath.Join(t.TempDir(), "mytrack.mp3")
	proj.Config.Params.Assemble.BGM = abs
	proj.Config.Params.Assemble.BGMDescription = "低沉竹笛"
	path, note := c.bgmPathOf(proj, &project.Plan{})
	if !strings.HasSuffix(filepath.ToSlash(path), "mytrack.mp3") || path != abs {
		t.Errorf("显式文件优先，实际 %q", path)
	}
	if !strings.Contains(note, "项目参数指定") {
		t.Errorf("要说明来源是项目参数，实际 %q", note)
	}
}

// TestBGMNoEngineIsHonest 没有本地引擎时必须**如实说明**，不能沉默、不能假装有配乐。
func TestBGMNoEngineIsHonest(t *testing.T) {
	dir := t.TempDir()
	lib, _ := library.New(filepath.Join(dir, "lib"))
	c := &Coordinator{lib: lib} // reg 为 nil ⇒ 没有引擎
	proj := &project.Project{ID: "ep01"}
	proj.Config.Params.Assemble.BGMDescription = "低沉竹笛与弦乐"
	path, note := c.bgmPathOf(proj, &project.Plan{Shots: []project.Shot{{ID: 1, Duration: 30}}})
	if path != "" {
		t.Errorf("没有引擎时不该产出配乐路径，实际 %q", path)
	}
	if !strings.Contains(note, "没有") {
		t.Errorf("必须如实说明为什么没有配乐，实际 %q", note)
	}
}

// TestBGMNoDescriptionNoBGM 没写配乐需求时不该乱生成（空需求生成出来的曲子毫无意义）。
func TestBGMNoDescriptionNoBGM(t *testing.T) {
	c := &Coordinator{}
	proj := &project.Project{ID: "ep01"}
	path, note := c.bgmPathOf(proj, &project.Plan{})
	if path != "" || note != "" {
		t.Errorf("没写需求时应静默返回空（compose 会如实播报未铺 BGM），实际 path=%q note=%q", path, note)
	}
}
