package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// board_as_angles_test.go 三视图细节板**顶替转台图当角度参考**（2026-09-27 用户定调：
// 「有了三视图，就不需要转台图、背面、侧面、四分之三了；三视图做成 16:9 比例，2K」）。
//
// 为什么不能只"停掉转台图"就完事：转台图是那类镜头（中景/全景/远景）的**角度参考**
// （RefPlan 的 `character_angles`）—— 直接停掉而不补位，H3 就只剩一张正面定妆照，
// 侧面/背面的镜头会自己编。所以口径是"**换那张图，不换角色的位置**"：
// 角色名仍是 character_angles，参考图顺序不变，RefManifest 与 director 的
// REFERENCE ORDER 契约都不用动。
func TestRefsUseBoardAsAngleReference(t *testing.T) {
	dir := t.TempDir()
	master := filepath.Join(dir, "characters_char_shenyan.png")
	writeTestPNG(t, master, 768, 1344)
	// 只有板子、**没有**转台图 —— 正是新默认（views 关）下的盘上形态
	board := BoardOutPath(master)
	writeTestPNG(t, board, BoardWidth, BoardHeight)

	assets := map[string]project.Asset{
		"char:char_01": {ID: "char:char_01", Kind: "characters", Name: "沈砚", File: "characters_char_shenyan.png"},
	}
	// 中景：需要角度参考 → 应给"定妆照 + 板子"两张，角色名仍是 character_angles
	shot := project.Shot{ID: 1, Scene: "", ShotSize: "中景", Characters: []string{"char_01"}}
	refs, roles, _ := RefPlan(dir, shot, assets, false)
	if len(refs) != 2 {
		t.Fatalf("中景应装配 定妆照 + 板子 两张，实际 %d 张", len(refs))
	}
	if filepath.Clean(refs[1].Path) != filepath.Clean(board) {
		t.Errorf("第二张应是三视图板，实际 %s", refs[1].Path)
	}
	if len(roles) != 2 || roles[1].Kind != "character_angles" {
		t.Fatalf("第二张的身份仍是 character_angles（角色名不变，只有那张图换了），实际 %+v", roles)
	}

	// 近景/特写：角度参考不附（板子缩下去只剩 1/4 细节，没必要占一张参考位）
	closeShot := project.Shot{ID: 2, ShotSize: "特写", Characters: []string{"char_01"}}
	refs2, _, _ := RefPlan(dir, closeShot, assets, false)
	if len(refs2) != 1 {
		t.Errorf("特写只该给定妆照一张，实际 %d 张", len(refs2))
	}

	// 转台图在时**优先**用它（显式开了多视角的老项目 / 后台补过的项目）。
	// 注意：turnaroundPath 只在文件存在时返回路径，所以要先落盘再问它。
	turn := filepath.Join(dir, "characters_char_shenyan_turnaround.png")
	writeTestPNG(t, turn, 3328, 1248)
	if got := turnaroundPath(dir, "characters_char_shenyan.png"); got == "" {
		t.Fatalf("夹具的转台图命名与 turnaroundPath 的推导不一致：%s", turn)
	}
	refs3, _, _ := RefPlan(dir, shot, assets, false)
	if len(refs3) != 2 || filepath.Clean(refs3[1].Path) != filepath.Clean(turn) {
		t.Errorf("有转台图时应优先用转台图（老路留着），实际 %+v", refs3)
	}
}

// TestBoardReplacesTurnaroundByDefault 默认口径：多视角关（不再跑转台视频），板子照旧生成。
func TestBoardReplacesTurnaroundByDefault(t *testing.T) {
	p := project.DefaultStageParams()
	if p.Assets.ViewsOn() {
		t.Error("多视角默认应为关（三视图板接手角度参考）")
	}
	// 板子的生成**不受多视角开关影响**：它是角度参考 + 人审资产，独立生成
	src, err := os.ReadFile("stages_assets.go")
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(string(src), "c.genCharacterBoard(", "c.genCharacterViews(") {
		t.Error("资产阶段应同时保留两条路：三视图板（人形才出，不受开关管）与多视角（受 ViewsOn 管）")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// TestBoardSizeIsActuallyWired 板子的 16:9·2K 必须**真的接到生成口**上。
//
// 这条是补上一次翻车的洞（2026-09-27 用户拿着截图问「你改了吗？三视图为什么还是 9:16」）：
// 我上一轮只改了 `BoardSize` 的数值 —— 而 `BoardOptions` 当时**根本没有尺寸字段**，
// `GenerateBoard` 也就没往引擎传尺寸；2.1 的编辑链按**第一张参考图的画幅**决定输出 latent
// （见 comfyengine/imageedit.go 的 buildEditGraphQwen21），于是板子照旧跟着定妆照出成 9:16。
// 界面上的日志倒是写了"2048×1152"——**日志说了，图上没有**，这种"口径没接线"最难看出来。
//
// 所以这里钉四件事，缺一件就说明又断在某一环：
//
//	① BoardOptions 收画布；② GenerateBoard 把画布当第一张参考图；
//	③ 调用点真的先拼出 16:9 画布再传进去；④ 旧画幅的板子会被判为过期（否则永远不重出）。
func TestBoardSizeIsActuallyWired(t *testing.T) {
	src, err := os.ReadFile("board.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		"Canvas     string",             // ① 选项里要有画布
		"ref := o.Master",               // ② 生成时按画布优先取参考图
		"ref = o.Canvas",                //
		"canvas = BoardCanvasPath(out)", // ③ 调用点拼画布
		"boardCanvas(ctx, c.ff, masterPath, canvas, bw, bh)",
		"w != BoardWidth || h != BoardHeight", // ④ 画幅变了要重出
	} {
		if !strings.Contains(s, want) {
			t.Errorf("板子的 16:9·2K 没接到生成口上，缺：%q", want)
		}
	}
	// 引擎侧那条前提也要在（编辑链按第一张参考图的画幅出图）——它变了这条接线就得重想
	edit, err := os.ReadFile("../engine/comfyengine/imageedit.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(edit), "按第一张参考图的画幅") {
		t.Error("编辑链「按第一张参考图画幅决定输出」这条前提在源码里没了 —— 画布那条路要重新确认")
	}
}
