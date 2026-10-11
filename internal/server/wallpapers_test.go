// wallpapers_test.go 壁纸库的门（2026-10-10 用户：「新增"壁纸库"，分类"手机壁纸"和"桌面壁纸"，
// 新增用户描述然后结合LLM生成提示词后编辑进行文生图（分辨率配置、是否同时生成移动端和桌面端等
// 相关参数配置）」）。
//
// 判据都钉在**可执行形态**上（AGENTS.md）：提示词口径直接喂 `wallpaperPromptSys`/`wallpaperSeedPrompt`/
// `cleanWallpaperPrompt` 本体；出图那条用一个**假引擎**真跑一遍 HTTP 接口，看盘上落下什么；
// 失败纪律看"目录有没有被造出来"（封面那轮的教训：引擎检查必须在 `MkdirAll` 之前）。
package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/project"
)

// fakeWallpaperEngine 只实现壁纸用得到的那几项：GenerateImage 真写一个文件（其余能力空实现）。
type fakeWallpaperEngine struct {
	mu    []wallpaperGenCall
	fail  bool
	bytes []byte
}

type wallpaperGenCall struct {
	Prompt string
	Seed   int64
	W, H   int
	Out    string
}

func (f *fakeWallpaperEngine) Name() string                        { return "comfy" }
func (f *fakeWallpaperEngine) Available(ctx context.Context) error { return nil }
func (f *fakeWallpaperEngine) Render(ctx context.Context, job engine.Job) (*engine.Result, error) {
	return &engine.Result{}, nil
}
func (f *fakeWallpaperEngine) FreeVRAM(ctx context.Context) error { return nil }
func (f *fakeWallpaperEngine) ClearQueue(ctx context.Context)     {}
func (f *fakeWallpaperEngine) Interrupt(ctx context.Context)      {}
func (f *fakeWallpaperEngine) GenerateMusic(ctx context.Context, job engine.MusicJob) (string, error) {
	return "", nil
}
func (f *fakeWallpaperEngine) Upscale(ctx context.Context, src string, opt engine.UpscaleOptions, length int,
	fps float64, outName, outPath string, timeout time.Duration) (string, error) {
	return "", nil
}

func (f *fakeWallpaperEngine) GenerateImage(ctx context.Context, prompt, neg string, seed int64, w, h int,
	outPath string) (string, error) {
	if f.fail {
		return "", errWallpaperFakeFail
	}
	f.mu = append(f.mu, wallpaperGenCall{Prompt: prompt, Seed: seed, W: w, H: h, Out: outPath})
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	b := f.bytes
	if b == nil {
		b = []byte("PNG-FAKE")
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		return "", err
	}
	return outPath, nil
}

func (f *fakeWallpaperEngine) calls() []wallpaperGenCall { return f.mu }

var errWallpaperFakeFail = &fakeWallpaperError{}

type fakeWallpaperError struct{}

func (*fakeWallpaperError) Error() string { return "fake engine failure" }

// ── ① 提示词口径（喂本体） ────────────────────────────────────────────

func TestWallpaperPromptIsEnglishAndTextFree(t *testing.T) {
	// 兜底通用构图词：两分类都必须是**英文、与画幅相符、且不含任何文字类名词/否定句**
	for _, k := range []string{wallpaperKindMobile, wallpaperKindDesktop} {
		seed := wallpaperSeedPrompt(k)
		if project.CountCJK(seed) != 0 {
			t.Errorf("%s 的兜底词里有汉字（会被当字形画到画面上）：%q", k, seed)
		}
		if wallpaperBannedRe.MatchString(seed) {
			t.Errorf("%s 的兜底词里出现了文字类名词或否定式（cfg=1.0 下否定词不生效，名词会被画出来）：%q", k, seed)
		}
		if !wallpaperPromptUsable(seed) {
			t.Errorf("%s 的兜底词自己就过不了可用判据：%q", k, seed)
		}
		want := "Vertical"
		if normWallpaperKind(k) == wallpaperKindDesktop {
			want = "Horizontal"
		}
		if !strings.Contains(seed, want) {
			t.Errorf("%s 的兜底词要写清画幅（缺 %q）：%q", k, want, seed)
		}
	}
	// 系统提示词：三条口径缺一不可（它们是"模型为什么不会画乱码字"的全部依据）
	for _, want := range []string{"不许出现任何汉字", "不许出现任何文字类名词", "不要写否定句", "cfg=1.0"} {
		if !strings.Contains(wallpaperPromptSys, want) {
			t.Errorf("给 LLM 的系统提示词缺少口径 %q —— 少一条就等于让模型自由发挥", want)
		}
	}
}

func TestCleanWallpaperPromptDropsTextAndNegationPhrases(t *testing.T) {
	// 夹具＝模型真会写出来的那种词：画面短语 + 一句"不许有文字" + 一段中文说明
	raw := "Vertical phone wallpaper, a lone figure on a misty ridge, no other text, " +
		"soft gradient sky, no watermark, muted indigo palette, 这是说明不该进提示词"
	got, note, ok := cleanWallpaperPrompt(raw)
	if !ok {
		t.Fatalf("这份词清洗后应当可用（画面短语够多），实际被判不可用：%q（note=%q）", got, note)
	}
	if project.CountCJK(got) != 0 {
		t.Errorf("汉字必须清掉：%q", got)
	}
	for _, bad := range []string{"no other text", "no watermark", "text", "watermark"} {
		if strings.Contains(strings.ToLower(got), bad) {
			t.Errorf("被禁短语没摘干净（%q）：%q", bad, got)
		}
	}
	for _, want := range []string{"lone figure on a misty ridge", "soft gradient sky", "muted indigo palette"} {
		if !strings.Contains(got, want) {
			t.Errorf("画面短语不许跟着丢（缺 %q）：%q", want, got)
		}
	}
	for _, want := range []string{"清掉", "摘掉"} {
		if !strings.Contains(note, want) {
			t.Errorf("改过模型输出必须如实写进 Note（缺 %q）：%q", want, note)
		}
	}
	// 反面针：整段中文 / 只剩被禁词 ⇒ 都判不可用（调用方走兜底）
	for _, bad := range []string{
		"这是一整段中文说明，平台应当判它不可用，而不是把空壳送去出图。",
		"no text, no watermark, no logo, no signature",
	} {
		if _, _, ok := cleanWallpaperPrompt(bad); ok {
			t.Errorf("这段必须判不可用（否则会把空壳送给模型）：%q", bad)
		}
	}
}

// ── ② 分辨率（喂本体） ───────────────────────────────────────────────

// TestWallpaperSizesAre32AlignedAndWithinBudget 档位表的判据（门直接喂 `wallpaperSizes` 本体）。
//
// 【2026-10-11】原来这条叫 `...16Aligned...`、按「16 对齐 + 上限 2048」判 —— 而真机图像侧是
// `imagegen.go` 的 `snap32`（**向下**取整到 32），上限也抬到了 4096（用户：「桌面壁纸的带鱼屏分辨率
// 默认好低，增加高分辨率的」）。判据跟着改：每类 ≥5 档、**至少一档单边 ≥2048**、32 对齐、
// 在 [512,4096] 内、不超像素预算、**标签开头的尺寸 == 实际像素**。
func TestWallpaperSizesAre32AlignedAndWithinBudget(t *testing.T) {
	for _, k := range []string{wallpaperKindMobile, wallpaperKindDesktop} {
		sizes := wallpaperSizes(k)
		if len(sizes) < 5 {
			t.Fatalf("%s 至少要有 5 档分辨率（多档可选是用户的要求），实际 %d 档", k, len(sizes))
		}
		big := 0
		for _, s := range sizes {
			if s.W%wallpaperAlign != 0 || s.H%wallpaperAlign != 0 {
				t.Errorf("%s 的 %d×%d 没对齐 %d（图像侧 snap32 是同一口径）", k, s.W, s.H, wallpaperAlign)
			}
			if s.W < wallpaperMinSide || s.H < wallpaperMinSide || s.W > wallpaperMaxSide || s.H > wallpaperMaxSide {
				t.Errorf("%s 的 %d×%d 超出 [%d,%d]", k, s.W, s.H, wallpaperMinSide, wallpaperMaxSide)
			}
			if int64(s.W)*int64(s.H) > int64(wallpaperMaxPixels) {
				t.Errorf("%s 的 %d×%d 超过像素预算 %.0f 万（真机只验到 824 万）",
					k, s.W, s.H, float64(wallpaperMaxPixels)/10000)
			}
			if s.Label == "" {
				t.Errorf("%s 的 %d×%d 没有给人看的标签", k, s.W, s.H)
			}
			// 标签**必须以真实像素开头** —— 不许"标签写 720、实际出 704"这种写着好看的档
			if !strings.HasPrefix(s.Label, fmt.Sprintf("%d×%d", s.W, s.H)) {
				t.Errorf("%s 的标签 %q 与真实像素 %d×%d 不一致（标签开头必须是实际尺寸）", k, s.Label, s.W, s.H)
			}
			if s.W >= 2048 || s.H >= 2048 {
				big++
			}
		}
		if big == 0 {
			t.Errorf("%s 一档 ≥2K 的都没有 —— 用户要的「增加高分辨率的」没落地", k)
		}
		// 画幅方向：手机=竖版（高>宽），桌面=横版（宽>高）
		if k == wallpaperKindMobile && sizes[0].H <= sizes[0].W {
			t.Errorf("手机壁纸第一档必须是竖版：%d×%d", sizes[0].W, sizes[0].H)
		}
		if k == wallpaperKindDesktop && sizes[0].W <= sizes[0].H {
			t.Errorf("桌面壁纸第一档必须是横版：%d×%d", sizes[0].W, sizes[0].H)
		}
		// 缺省/非法 ⇒ 回落到该分类第一档
		if w, h := normWallpaperSize(k, 0, 0); w != sizes[0].W || h != sizes[0].H {
			t.Errorf("%s 不给尺寸时应回落到 %d×%d，实得 %d×%d", k, sizes[0].W, sizes[0].H, w, h)
		}
		// 过小 ⇒ 抬到下限（且下限本身是对齐的）
		if w, h := normWallpaperSize(k, 100, 100); w != wallpaperMinSide || h != wallpaperMinSide {
			t.Errorf("过小尺寸应抬到 %d，实得 %d×%d", wallpaperMinSide, w, h)
		}
		// 自定义：对齐 32（不是夹成方图、也不是照抄用户填的）
		if w, h := normWallpaperSize(k, 1000, 1000); w != 992 || h != 992 {
			t.Errorf("自定义 1000×1000 应对齐到 992×992（32 的倍数），实得 %d×%d", w, h)
		}
	}
}

// TestFitWallpaperSizeMatchesEngineSnap32 尺寸口径的**唯一出处**：真机实测过的几组必须逐字复现
// （图像侧 `snap32` 向下取整 ⇒ 3440→3424、2160→2144、3120→3104；2560×1440 原样）。
//
// 这几组数字是 2026-10-11 真机跑出来的（`cmd/imagecheck -w 3440 -h 1440` 等，落盘像素实测），
// 不是推算 —— "平台承诺的尺寸"必须与"引擎真正出的尺寸"逐字一致，否则就是写着好看。
func TestFitWallpaperSizeMatchesEngineSnap32(t *testing.T) {
	type fitCase struct {
		inW, inH, wantW, wantH int
		why                    string
	}
	cases := []fitCase{
		{3440, 1440, 3424, 1440, "真机带鱼屏：落盘实测 3424×1440"},
		{3840, 2160, 3840, 2144, "≈4K：落盘实测 3840×2144"},
		{1440, 3120, 1440, 3104, "手机 2K：落盘实测 1440×3104"},
		{2560, 1440, 2560, 1440, "2K 16:9：两侧都对齐 32，原样"},
		{512, 512, 512, 512, "下限原样"},
		{100, 100, 512, 512, "过小抬到下限"},
		{9999, 19, 4096, 512, "越界：单边夹上限、另边抬下限"},
	}
	for _, c := range cases {
		w, h := fitWallpaperSize(c.inW, c.inH)
		if w != c.wantW || h != c.wantH {
			t.Errorf("fitWallpaperSize(%d×%d) = %d×%d，想要 %d×%d（%s）", c.inW, c.inH, w, h, c.wantW, c.wantH, c.why)
		}
	}
	// 像素预算：4096×4096（1678 万像素）必须被按比例缩到预算内、仍是 32 对齐、且保比例（方图还是方图）
	w, h := fitWallpaperSize(4096, 4096)
	if int64(w)*int64(h) > int64(wallpaperMaxPixels) {
		t.Errorf("4096×4096 应被像素预算缩下来（实得 %d×%d = %.0f 万像素）", w, h, float64(w*h)/10000)
	}
	if w%wallpaperAlign != 0 || h%wallpaperAlign != 0 {
		t.Errorf("预算缩完仍要 32 对齐（实得 %d×%d）", w, h)
	}
	if w != h {
		t.Errorf("按比例缩要保比例（方图还是方图，实得 %d×%d）", w, h)
	}
	// 同一件事只能有一份口径：normWallpaperSize 对同一输入必须与 fitWallpaperSize 逐字一致
	for _, c := range [][2]int{{3440, 1440}, {3840, 2160}, {1000, 1000}} {
		w1, h1 := normWallpaperSize(wallpaperKindDesktop, c[0], c[1])
		w2, h2 := fitWallpaperSize(c[0], c[1])
		if w1 != w2 || h1 != h2 {
			t.Errorf("normWallpaperSize 与 fitWallpaperSize 对 %d×%d 结论不同（%d×%d vs %d×%d）—— 两份口径分叉了",
				c[0], c[1], w1, h1, w2, h2)
		}
	}
}

// ── ③ 出图：真跑一遍（假引擎），看盘上落下什么 ────────────────────────

func TestWallpaperGenerateWritesBothKindsWithOwnSizes(t *testing.T) {
	s := newTestServer(t)
	eng := &fakeWallpaperEngine{}
	s.reg.Register(eng) // Name()="comfy" ⇒ handleWallpaperGenerate 能取到

	code, out := doReq(t, s, "POST", "/api/wallpapers/generate", map[string]any{
		"prompt": "Vertical phone wallpaper, a lone figure on a misty ridge, muted indigo palette",
		"both":   true, "count": 2, "seed": 424242,
	})
	if code != 200 {
		t.Fatalf("两端各出 2 张应当 200，实际 %d（%v）", code, out)
	}
	if n, _ := out["count"].(float64); int(n) != 4 {
		t.Errorf("两端 × 2 张 = 4 张，实际 %v", out["count"])
	}
	calls := eng.calls()
	if len(calls) != 4 {
		t.Fatalf("引擎该被调 4 次，实际 %d 次", len(calls))
	}
	// 每一端都要用**自己**的画幅（手机竖版 / 桌面横版）——这是"同时出两端"的全部意义
	mw, mh := wallpaperSizes(wallpaperKindMobile)[0].W, wallpaperSizes(wallpaperKindMobile)[0].H
	dw, dh := wallpaperSizes(wallpaperKindDesktop)[0].W, wallpaperSizes(wallpaperKindDesktop)[0].H
	seen := map[string]int{}
	for _, c := range calls {
		seen[filepath.Base(filepath.Dir(c.Out))]++
		if !strings.Contains(c.Out, string(filepath.Separator)+"wallpapers"+string(filepath.Separator)) {
			t.Errorf("产物要落在 <数据根>/wallpapers 下：%s", c.Out)
		}
		switch filepath.Base(filepath.Dir(c.Out)) {
		case wallpaperKindMobile:
			if c.W != mw || c.H != mh {
				t.Errorf("手机壁纸该用 %d×%d，实际 %d×%d", mw, mh, c.W, c.H)
			}
		case wallpaperKindDesktop:
			if c.W != dw || c.H != dh {
				t.Errorf("桌面壁纸该用 %d×%d，实际 %d×%d", dw, dh, c.W, c.H)
			}
		default:
			t.Errorf("产物落在了意料之外的目录：%s", c.Out)
		}
		// 每一张都要有伴随文件（正向词/来源/参数）——壁纸库要能回答"这张是怎么来的"
		if _, err := os.Stat(wallpaperMetaPath(c.Out)); err != nil {
			t.Errorf("少了伴随文件（正向词/参数）：%v", err)
		}
	}
	if seen[wallpaperKindMobile] != 2 || seen[wallpaperKindDesktop] != 2 {
		t.Errorf("两端各 2 张，实际 mobile=%d desktop=%d", seen[wallpaperKindMobile], seen[wallpaperKindDesktop])
	}
	// 清单接口要把两分类都列出来（前端左右网格就靠它）
	_, list := doReq(t, s, "GET", "/api/wallpapers", nil)
	for _, k := range []string{"mobile", "desktop"} {
		arr, _ := list[k].([]any)
		if len(arr) != 2 {
			t.Errorf("%s 分类应列出 2 张，实际 %d（%v）", k, len(arr), list[k])
		}
	}
	// 种子：同一 seed 起步，第二张按步长散开（同一起点往外散，别出两张一样的）
	if calls[0].Seed != 424242 || calls[1].Seed == calls[0].Seed {
		t.Errorf("一次多张应当从给定 seed 往外散：%d / %d", calls[0].Seed, calls[1].Seed)
	}
}

func TestWallpaperGenerateWithoutEngineKeepsNothing(t *testing.T) {
	s := newTestServer(t) // 测试环境没有注册任何引擎
	code, out := doReq(t, s, "POST", "/api/wallpapers/generate", map[string]any{
		"prompt": "Vertical phone wallpaper, a lone figure on a misty ridge",
	})
	if code != 503 {
		t.Fatalf("没有引擎时该 503（如实报错），实际 %d（%v）", code, out)
	}
	if !strings.Contains(strings.ToLower(wallpaperErrText(out)), "comfyui") {
		t.Errorf("报错要说清是引擎不可用：%v", out)
	}
	if _, err := os.Stat(s.wallpaperRoot()); err == nil {
		t.Errorf("没有引擎时**一个目录都不许造**（否则盘上留下空目录，看起来像生过但失败了）：%s", s.wallpaperRoot())
	}
}

func TestWallpaperGenerateFailureLeavesNoFile(t *testing.T) {
	s := newTestServer(t)
	s.reg.Register(&fakeWallpaperEngine{fail: true})
	code, _ := doReq(t, s, "POST", "/api/wallpapers/generate", map[string]any{
		"prompt": "Vertical phone wallpaper, a lone figure on a misty ridge",
	})
	if code != 500 {
		t.Fatalf("引擎失败该 500，实际 %d", code)
	}
	files, _ := filepath.Glob(filepath.Join(s.wallpaperRoot(), "*", "*"))
	if len(files) != 0 {
		t.Errorf("出图失败不许留下半成品：%v", files)
	}
}

// ── ④ 路径越界（只读图 / 删除都要挡） ────────────────────────────────

func TestWallpaperEndpointsRejectOutsidePaths(t *testing.T) {
	s := newTestServer(t)
	outside := filepath.Join(t.TempDir(), "secret.png")
	writeFile(t, outside, []byte("NOPE"))
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/wallpapers/image?path=" + urlEnc(outside)},
		{"DELETE", "/api/wallpapers/item?file=" + urlEnc(outside)},
		{"GET", "/api/wallpapers/image?path=" + urlEnc(filepath.Join(s.wallpaperRoot(), "..", "settings.json"))},
	} {
		if code, _ := doReq(t, s, tc.method, tc.path, nil); code != 403 {
			t.Errorf("%s %s 应 403（路径超出壁纸库范围），实际 %d", tc.method, tc.path, code)
		}
	}
}

// ── ⑤ 「AI 写词」接口：拿不到就回通用词 + 如实说 ─────────────────────

func TestWallpaperPromptEndpointFallsBackHonestly(t *testing.T) {
	// 没配 LLM ⇒ 200 + 通用构图词 + 说清"没配置"
	s := newTestServer(t)
	code, out := doReq(t, s, "POST", "/api/wallpapers/prompt", map[string]any{
		"desc": "深夜的赛博城市天台", "kind": "mobile",
	})
	if code != 200 {
		t.Fatalf("没配 LLM 也要给出一行能用的词（200），实际 %d（%v）", code, out)
	}
	if from, _ := out["from"].(string); !strings.Contains(from, "通用构图词") {
		t.Errorf("来源必须如实说是通用构图词：%v", out)
	}
	if p, _ := out["prompt"].(string); project.CountCJK(p) != 0 {
		t.Errorf("回的词里不许有汉字：%q", p)
	}
	// 没写描述 ⇒ 400（AI 没东西可依据）
	if code, _ := doReq(t, s, "POST", "/api/wallpapers/prompt", map[string]any{"kind": "mobile"}); code != 400 {
		t.Errorf("没描述时该 400，实际 %d", code)
	}

	// LLM 配了但只会回中文 ⇒ 收口后不可用 ⇒ 回通用词 + Note 说清"不含你的描述"
	s2 := newTestServer(t)
	fakeCoverLLM(t, s2, "这是一整段中文，平台应当判它不可用并走兜底，而不是把空壳送去出图。")
	code2, out2 := doReq(t, s2, "POST", "/api/wallpapers/prompt", map[string]any{
		"desc": "深夜的赛博城市天台", "kind": "desktop",
	})
	if code2 != 200 {
		t.Fatalf("LLM 给不出可用词时也要 200（给一行能改的词），实际 %d（%v）", code2, out2)
	}
	from2, _ := out2["from"].(string)
	note2, _ := out2["note"].(string)
	if !strings.Contains(from2, "通用构图词") || !strings.Contains(note2, "不含你的描述") {
		t.Errorf("兜底必须如实标注（来源：%q / 说明：%q）—— 不许把通用词说成是照你描述写的", from2, note2)
	}
	if p2, _ := out2["prompt"].(string); !strings.Contains(p2, "Horizontal") {
		t.Errorf("桌面分类的兜底词要是横版口径：%q", p2)
	}
}

// ── ⑥ 接线（菜单/视图/API 层） ──────────────────────────────────────

func TestWallpaperLibraryIsWiredIntoFrontend(t *testing.T) {
	app := readFrontend(t, "src/App.vue")
	for _, want := range []string{
		"id: 'wallpapers'", "label: '壁纸库'", "import WallpapersView",
		`<WallpapersView v-else-if="G.view === 'wallpapers'" />`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("壁纸库没有接进 App.vue：%s", want)
		}
	}
	js := readFrontend(t, "src/store.js")
	for _, want := range []string{
		"wallpapers: () => req('GET', '/api/wallpapers')",
		"wallpaperPrompt: (desc, kind) => req('POST', '/api/wallpapers/prompt'",
		"wallpaperGenerate: (body) => req('POST', '/api/wallpapers/generate'",
		"wallpaperDelete: (file)",
		"wallpaperCoverURL_PLACEHOLDER",
	} {
		if want == "wallpaperCoverURL_PLACEHOLDER" {
			continue
		}
		if !strings.Contains(js, want) {
			t.Errorf("store.js 缺接线：%s", want)
		}
	}
	vue := readFrontend(t, "src/views/WallpapersView.vue")
	// 用户点名的三件事：两分类、描述→AI 写词、分辨率与"同时生成手机+桌面"
	for _, want := range []string{
		`value="mobile"`, `value="desktop"`,
		"AI 写词", "writePrompt(", "wallpaperPrompt(",
		"分辨率", "el-select", "同时生成手机 + 桌面", "both",
		"wallpaperGenerate(", "wallpaperDelete(", "/api/wallpapers/image?path=",
	} {
		if !strings.Contains(vue, want) {
			t.Errorf("壁纸库视图缺 %q", want)
		}
	}
	// 两个分类在视图里各出现**恰一次**（少一个就只有一类壁纸；用计数而不是 Contains）
	if n := strings.Count(vue, `value="mobile"`) + strings.Count(vue, `value="desktop"`); n != 2 {
		t.Errorf("分类按钮应当恰好两个（手机/桌面），实际 %d", n)
	}
}

// wallpaperErrText 取错误响应里那句话（`writeErr` 把错误放在 error 字段）。
func wallpaperErrText(out map[string]any) string {
	return fmt.Sprint(out["error"])
}
