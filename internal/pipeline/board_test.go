package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/project"
)

// board_test.go 角色**三视图细节板**的契约
// （官方 3d-animation-short-generator STEP 3 + 技能侧整改单 §2.2）。
//
// 三件必须钉死的事：
//  1. 版面与取景：板子提示词必须带版面清单、且**原来的人像取景已被换掉**（不换就是
//     "半身胸像"与"多视图板"打架，模型二选一糊）；幂等（装配点可能被调两次）。
//  2. 归属：只给人形角色出板子；板子**不是** H3 的参考图（refs 只认主图与转台图）。
//  3. 时效：定妆照重出过（朝向自检换种子）后，旧板必须判为过期并重出 —— 否则留着
//     一个"另一个人的脸"的板子骗人审。

func boardPortraitPrompt() string {
	return "young man, narrow face, low ponytail to the shoulder, combed back forehead clear, " +
		"grey linen robe, plain background, upper-body framing, front-facing, 9:16, " +
		"MATTE CINEMATIC RENDER: a cinematic photoreal 3D render with real materials."
}

func TestBoardPromptReplacesPortraitFraming(t *testing.T) {
	got := BoardImagePrompt(boardPortraitPrompt())

	if !strings.Contains(got, BoardMarker) || !strings.Contains(got, "PRODUCTION REFERENCE SHEET") {
		t.Fatalf("板子提示词缺少版面标记(幂等判据):\n%s", got)
	}
	// 版面清单按**技能侧《平台侧生成规范》§3.4** 的 15 格（3 全身 ＋ 6 头角度 ＋ 6 局部细节）：
	// 平台兜底清单此前与它不一致（头角度与局部细节两项不同），已对齐 —— 有卡片的 `board_prompt` 时
	// 以卡片为准（见 BoardPromptWithCard），这段是卡里没有板串时的兜底。
	for _, want := range []string{
		"FRONT, LEFT PROFILE and BACK", "six head angles", "six detail tiles",
		"pure white seamless studio backdrop", "blank and unmarked",
		"complete and fully inside the picture",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("版面清单缺 %q:\n%s", want, got)
		}
	}
	// **版面清单与取景句里一个否定词都不许有**（2026-09-28 用户报「不要出现莫名的文字，咋还是有」）。
	//
	// 原来这里写的是 "no text, no letters, no numerals, no watermark, no logo, no UI, no
	// like/save buttons, no social-media chrome, no screenshot framing" —— 看着在禁文字，
	// 实际在**教模型画字**：图片侧是蒸馏模型（CFG 1.0），实测对否定词免疫、却会把名词当指令
	// 画出来（"no epaulettes" 直接画出肩章）。"no text" 就画出 text —— 用户看到的乱码段落
	// 正是这一串的产物。所以门从"必须有那串禁词"反转为"**不许有否定词**"。
	lowBoard := strings.ToLower(BoardLayoutClause + " " + BoardFraming)
	for _, bad := range []string{"no ", "not ", "never ", "without ", "avoid "} {
		if strings.Contains(lowBoard, bad) {
			t.Errorf("版面/取景句不许出现否定词 %q（会被照着画）:\n%s", bad, lowBoard)
		}
	}
	// 人像取景必须被换掉：同一段提示词里既要求"半身胸像"又要求"多视图板"= 二选一地糊
	low := strings.ToLower(got)
	for _, bad := range []string{"upper-body framing", "upper body", "front-facing", "plain background"} {
		if strings.Contains(low, bad) {
			t.Errorf("板子提示词里残留人像取景 %q:\n%s", bad, got)
		}
	}
	// 身份与风格必须**逐字保留**（板子是派生物，不是重新描述一遍）
	if !strings.Contains(got, "low ponytail to the shoulder") {
		t.Error("板子必须保留定妆提示词里的身份特征(发型三要素)")
	}
	if !strings.Contains(got, "MATTE CINEMATIC RENDER") {
		t.Error("板子必须继承全片风格块(与定妆照同一套介质)")
	}
	// 幂等
	if again := BoardImagePrompt(got); again != got {
		t.Fatalf("二次装配改变了结果:\n一次:%s\n二次:%s", got, again)
	}
}

// TestBoardSizeIsFixed2K16x9 板子画幅 = **固定 16:9 · 2K**（2026-09-27 用户定调）。
//
// 这条口径**故意不跟随项目画幅**（与资产图那条相反）：板子是"一张规格书"而不是"一帧画面"，
// 横版是它的天然形状；而项目画幅是竖的（9:16），跟随它只会把板子挤成竖条。
// 2K 也不是为了好看：板子顶替转台图当角度参考（见 RefPlan），喂给 H3 时整张都会被缩到
// 入口分辨率 —— 2048 宽下每个全身约 680px（与定妆照 768 同量级），1344 宽下只剩约 448px。
func TestBoardSizeIsFixed2K16x9(t *testing.T) {
	// 不同项目画幅都得到同一张板子的画幅（竖版/横版/方形一视同仁）
	for _, tc := range []struct{ w, h int }{{768, 1344}, {1344, 768}, {1024, 1024}} {
		w, h := BoardSize(tc.w, tc.h)
		if w != BoardWidth || h != BoardHeight {
			t.Fatalf("板子画幅应固定 %d×%d，实际 %d×%d（项目 %d×%d）",
				BoardWidth, BoardHeight, w, h, tc.w, tc.h)
		}
	}
	if BoardWidth <= BoardHeight {
		t.Fatalf("板子必须是横版（官方 reference sheet 是 16:9），实际 %d×%d", BoardWidth, BoardHeight)
	}
	if BoardWidth%32 != 0 || BoardHeight%32 != 0 {
		t.Errorf("画幅必须落在 32 的网格上，实际 %d×%d", BoardWidth, BoardHeight)
	}
	if ratio := float64(BoardWidth) / float64(BoardHeight); ratio < 1.7 || ratio > 1.9 {
		t.Errorf("画幅应接近 16:9，实际比例 %.2f", ratio)
	}
	// 2K：短边不许回到"按项目长边折算"的老数值（1344×756 那种）
	if BoardWidth < 2000 {
		t.Errorf("板子宽度应到 2K 档（≥2000），实际 %d —— 否则每个全身只剩 ~448px，头部细节会糊", BoardWidth)
	}
}

func TestBoardPathAndSuffix(t *testing.T) {
	master := filepath.Join("dir", "characters_char_shenyan.png")
	if got, want := BoardOutPath(master), filepath.Join("dir", "characters_char_shenyan"+BoardSuffix+".png"); got != want {
		t.Fatalf("板子路径 = %q，期望 %q", got, want)
	}
	// 库 → 项目改名：库里叫 characters_char_张三_board.png，落进项目必须按主图主名拼
	got := RelatedSuffixName("myproject_portrait", "/lib/characters_char_张三_board.png")
	if got != "myproject_portrait"+BoardSuffix+".png" {
		t.Fatalf("关联图改名 = %q，期望 %q（不改名 → 项目里有板子却永远匹配不上）", got, "myproject_portrait"+BoardSuffix+".png")
	}
}

func TestHumanoidBoardKind(t *testing.T) {
	for _, ek := range []EntityKind{KindHuman, KindChibi} {
		if !HumanoidBoardKind(ek) {
			t.Errorf("%s 应该出三视图板", ek)
		}
	}
	for _, ek := range []EntityKind{KindObject, KindSpiritPet, KindBeast, KindIncorporeal, KindScene} {
		if HumanoidBoardKind(ek) {
			t.Errorf("%s 不该出三视图板(非人形没有\"3 全身 + 6 个头\"这回事)", ek)
		}
	}
}

func TestBoardStaleFollowsMaster(t *testing.T) {
	dir := t.TempDir()
	master := filepath.Join(dir, "m.png")
	board := BoardOutPath(master)
	if err := os.WriteFile(master, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !boardStale(master, board) {
		t.Fatal("板子还不存在时必须判为需要出")
	}
	if err := os.WriteFile(board, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if boardStale(master, board) {
		t.Fatal("板子比定妆照新时不该重出(续跑/重进项目不烧重复 GPU)")
	}
	// 定妆照因朝向自检换种子重出 → 板子过期
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(master, future, future); err != nil {
		t.Fatal(err)
	}
	if !boardStale(master, board) {
		t.Fatal("定妆照重出后旧板必须判为过期(它已经是另一张脸)")
	}
	// 定妆照不在 → 不折腾（调用方本就拿不到身份源）
	if boardStale(filepath.Join(dir, "missing.png"), board) {
		t.Error("读不到定妆照时不该判「需要出」")
	}
}

// TestAssembleShotRefsNeverUsesBoard 板子**不是** H3 的参考图：它带着 3 个全身 + 12 个小图，
// 喂进 Ref2V 会与"身份照抄定妆照"打架（refs 只认主图与 <base>_turnaround.png）。
func TestAssembleShotRefsNeverUsesBoard(t *testing.T) {
	dir := t.TempDir()
	base := "characters_char_a"
	master := filepath.Join(dir, base+".png")
	turn := filepath.Join(dir, base+"_turnaround.png")
	board := filepath.Join(dir, base+BoardSuffix+".png")
	for _, f := range []string{master, turn, board} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 中景以上才带转台图（特写/近景画面里几乎只有一张脸，其余角度是身份信号的稀释源）
	shot := project.Shot{ID: 1, Scene: "scene_room", Characters: []string{"char_a"}, ShotSize: "中景", Mode: "ref2va"}
	assets := map[string]project.Asset{"char:char_a": {ID: "char:char_a", Kind: "characters", Name: "阿甲", File: base + ".png"}}

	refs, _, _, _ := RefPlan(dir, shot, assets, false)
	for _, r := range refs {
		if strings.Contains(r.Path, BoardSuffix) {
			t.Fatalf("板子被当成镜头参考图了:%v", r.Path)
		}
	}
	// 反向控制：转台图在同景别下确实会被带上（否则本用例是空的）
	found := false
	for _, r := range refs {
		if strings.Contains(r.Path, "_turnaround") {
			found = true
		}
	}
	if !found {
		t.Fatal("用例无效：近景镜本该带上转台图，实际没带 —— refs 的判据变了")
	}
}

// TestGenerateBoardRefusesWithoutEditPath 拿不到图像编辑链路（非本地 ComfyUI）时**如实报错**，
// 不悄悄退回文生图 —— 退回文生图等于再画一张脸，而板子的价值全在"是同一个人的多视图"。
func TestGenerateBoardRefusesWithoutEditPath(t *testing.T) {
	dir := t.TempDir()
	master := filepath.Join(dir, "m.png")
	if err := os.WriteFile(master, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := GenerateBoard(context.Background(), stubBoardEngine{}, BoardOptions{Master: master, Prompt: boardPortraitPrompt()})
	if err == nil || !strings.Contains(err.Error(), "图像编辑") {
		t.Fatalf("非 ComfyUI 引擎必须报「需要图像编辑能力」，实际 err=%v", err)
	}
	// 缺身份源也要拦
	if _, err := GenerateBoard(context.Background(), stubBoardEngine{}, BoardOptions{Prompt: "x"}); err == nil {
		t.Fatal("没有定妆照时必须报错（板子是派生物，没有身份源就没有板子）")
	}
}

// stubBoardEngine 只为"能力不足时报错"这一条路径存在的假引擎。
type stubBoardEngine struct{}

func (stubBoardEngine) Name() string                                               { return "stub" }
func (stubBoardEngine) Available(context.Context) error                            { return nil }
func (stubBoardEngine) Render(context.Context, engine.Job) (*engine.Result, error) { return nil, nil }
func (stubBoardEngine) ClearQueue(context.Context)                                 {}
func (stubBoardEngine) Interrupt(context.Context)                                  {}
func (stubBoardEngine) FreeVRAM(context.Context) error                             { return nil }
func (stubBoardEngine) GenerateImage(context.Context, string, string, int64, int, int, string) (string, error) {
	return "", nil
}
func (stubBoardEngine) Upscale(context.Context, string, engine.UpscaleOptions, int, float64, string, string, time.Duration) (string, error) {
	return "", nil
}

// TestScaleImageExact 板子尺寸归一化（2026-09-28 实测驱动）。
//
// 依据：编辑链只保证"画幅比例跟着第一张参考图"，**不保证像素数** —— 本机要求 2048×1152，
// 实测产物 1376×768（链内部缩了 1.5 倍），日志写着 16:9·2K 而盘上是别的尺寸。
// 这条测试真跑一次 ffmpeg：缩放后尺寸必须**精确等于**目标。
func TestScaleImageExact(t *testing.T) {
	f, err := ff.New("", "") // 空路径 = 走 PATH 解析（本机 PATH 上有 ffmpeg，AGENTS.md 记过）
	if err != nil {
		t.Skipf("本机没有 ffmpeg，跳过:%v", err)
	}
	_ = err
	dir := t.TempDir()
	src := filepath.Join(dir, "board.png")
	// 造一张 1376×768 的图（正是实测产物尺寸）
	cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi",
		"-i", "color=c=white:s=1376x768", "-frames:v", "1", src)
	if err := cmd.Run(); err != nil {
		t.Skipf("造图失败(跳过):%v", err)
	}
	bw, bh := BoardSize(0, 0)
	if err := scaleImageExact(context.Background(), f, src, bw, bh); err != nil {
		t.Fatalf("归一化失败:%v", err)
	}
	if w, h := imagePixelSizeOrZero(src); w != bw || h != bh {
		t.Errorf("归一化后应精确为 %d×%d，实际 %d×%d", bw, bh, w, h)
	}
}

// GenerateMusic 桩：本测试不涉及配乐生成，给安全空实现（与云端引擎同一政策）。
func (s stubBoardEngine) GenerateMusic(ctx context.Context, job engine.MusicJob) (string, error) {
	return "", nil
}
