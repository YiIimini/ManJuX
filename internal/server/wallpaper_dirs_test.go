package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wallpaper_dirs_test.go 壁纸「输出目录可选 + 分辨率档更多」的门（2026-10-10 用户：
// 「壁纸需用户选择生成后输出的目录，壁纸分辨率参数多增加些」）。
//
// 三条纪律（对应 wallpaper_dirs.go 的文件头）：
//
//	① 目录**只有一个出处**（`wallpaperDirOf`）：清单、生成、删除、只读图、设为壁纸全走它 ——
//	   "清单读 A 处、生成写 B 处"这种洞不许存在（源码级门咬住）。
//	② 改目录**只改以后往哪写**：老目录的图**不搬**（门直接断言老文件还在盘上），
//	   而老图在新配置下不再属于这一类（只读图 403）—— 说清、可逆。
//	③ 尺寸口径要**如实**：超上限/没对齐时必须显示"**实际出多少**"（不许写着 3440、出的是 2048 还装没事）。

const wallpapersDirPromptFixture = "Horizontal desktop wallpaper, wide cinematic landscape composition, " +
	"large calm negative space, soft gradient sky, muted teal palette"

// wallpapersGenerateOne 用假引擎在某个分类出一张，返回落盘路径。
func wallpapersGenerateOne(t *testing.T, s *Server, kind string, seed int64) string {
	t.Helper()
	code, out := doReq(t, s, "POST", "/api/wallpapers/generate", map[string]any{
		"prompt": wallpapersDirPromptFixture, "kind": kind, "count": 1, "seed": seed,
	})
	if code != 200 {
		t.Fatalf("出图应当 200，实际 %d（%v）", code, out)
	}
	items, _ := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("应当恰好出 1 张，实际 %d（%v）", len(items), out)
	}
	it, _ := items[0].(map[string]any)
	f, _ := it["file"].(string)
	if f == "" {
		t.Fatalf("响应里没有落盘路径：%v", out)
	}
	return f
}

// ── 分辨率（纯函数直接喂） ──────────────────────────────────────────────

func TestSizeForScreenKeepsAspectAndLimit(t *testing.T) {
	cases := []struct{ sw, sh int }{
		{3440, 1440}, // 真机带鱼屏
		{7680, 3200}, // 真机另一张壁纸的标注尺寸（远超上限）
		{1920, 1080}, // 最常见的桌面
		{3840, 2160}, // 4K
		{1280, 720},
		{1024, 768}, // 4:3
	}
	for _, c := range cases {
		w, h, note := sizeForScreen(c.sw, c.sh)
		if w <= 0 || h <= 0 {
			t.Errorf("屏幕 %d×%d 应当能算出尺寸，实得 %d×%d", c.sw, c.sh, w, h)
			continue
		}
		if w%wallpaperAlign != 0 || h%wallpaperAlign != 0 {
			t.Errorf("屏幕 %d×%d ⇒ %d×%d 没对齐 %d", c.sw, c.sh, w, h, wallpaperAlign)
		}
		if w > wallpaperMaxSide || h > wallpaperMaxSide || w < wallpaperMinSide || h < wallpaperMinSide {
			t.Errorf("屏幕 %d×%d ⇒ %d×%d 超出 [%d,%d]", c.sw, c.sh, w, h, wallpaperMinSide, wallpaperMaxSide)
		}
		if !strings.Contains(note, fmt.Sprintf("%d×%d", c.sw, c.sh)) {
			t.Errorf("说明里必须写清**屏幕是多少**（不然用户以为出的是原始尺寸）：%q", note)
		}
		// 比例不许跑偏（16 对齐会带来小偏差，留 3%）
		sr := float64(c.sw) / float64(c.sh)
		wr := float64(w) / float64(h)
		if wr < sr*0.97 || wr > sr*1.03 {
			t.Errorf("屏幕 %d×%d 比例 %.3f，出图 %d×%d 比例 %.3f —— 跑偏超过 3%%", c.sw, c.sh, sr, w, h, wr)
		}
	}
	// 真机带鱼屏：现在上限抬到 4096，「按本机屏幕」应当给出**接近原生**的 3424×1440（32 对齐落在这里），
	// 并如实说清是按平台口径算的（用户最容易被"写着 3440、出的是别的东西"糊弄）
	if _, _, note := sizeForScreen(3440, 1440); !strings.Contains(note, "3424×1440") || !strings.Contains(note, "32 对齐") {
		t.Errorf("带鱼屏必须在说明里给出**真实出货尺寸**与口径：%q", note)
	}
	if w, h, note := sizeForScreen(0, 0); w != 0 || h != 0 || note != "" {
		t.Errorf("读不到屏幕 ⇒ 应当回 (0,0,\"\")，界面据此置灰；实得 %d,%d,%q", w, h, note)
	}
}

func TestWallpaperSizeNoteTellsTheTruth(t *testing.T) {
	if note := wallpaperSizeNote(wallpaperKindDesktop, 2560, 1440); note != "" {
		t.Errorf("按口径能原样出的尺寸不该有说明（否则用户会以为被改了）：%q", note)
	}
	// 真机带鱼屏：3440/1440 都对齐不到 32 ⇒ 实出 3424×1440（不是被"夹上限"，是对齐）
	note := wallpaperSizeNote(wallpaperKindDesktop, 3440, 1440)
	for _, want := range []string{"3440×1440", "3424×1440", "对齐"} {
		if !strings.Contains(note, want) {
			t.Errorf("带鱼屏说明里必须出现 %q，实际 %q", want, note)
		}
	}
	// 超过单边上限：要同时说清上限与实出尺寸
	note = wallpaperSizeNote(wallpaperKindDesktop, 6000, 2000)
	for _, want := range []string{"4096", "上限"} {
		if !strings.Contains(note, want) {
			t.Errorf("超上限时说明里必须出现 %q，实际 %q", want, note)
		}
	}
	// 超过**像素预算**（4096×4096 = 1678 万像素，真机没验过这个量级）⇒ 按比例缩并说明为什么
	note = wallpaperSizeNote(wallpaperKindDesktop, 4096, 4096)
	if !strings.Contains(note, "像素预算") {
		t.Errorf("超预算时必须如实说清是预算挡的（不许静默缩小），实际 %q", note)
	}
	if note := wallpaperSizeNote(wallpaperKindDesktop, 1000, 600); !strings.Contains(note, "992×576") {
		t.Errorf("没对齐时要说出实际尺寸（1000→992、600→576），实际 %q", note)
	}
	if note := wallpaperSizeNote(wallpaperKindMobile, 0, 0); !strings.Contains(note, "第一档") {
		t.Errorf("没填尺寸要说明走的是第一档，实际 %q", note)
	}

	// 界面不许自己算这个数：必须回来问这一处（HTTP 也走一遍，防止"函数写好了没人接"）
	cs := newTestServer(t)
	code, body := doReq(t, cs, "GET", "/api/wallpapers/size?kind=desktop&w=3440&h=1440", nil)
	if code != 200 {
		t.Fatalf("尺寸口径接口应当 200，实际 %d（%v）", code, body)
	}
	if int(body["w"].(float64)) != 3424 || int(body["h"].(float64)) != 1440 {
		t.Errorf("真机带鱼屏 3440×1440 该给 3424×1440（32 对齐），实际 %v×%v", body["w"], body["h"])
	}
	if note, _ := body["note"].(string); !strings.Contains(note, "对齐") {
		t.Errorf("这一处也要如实说清为什么变了：%q", note)
	}
}

// ── 输出目录（HTTP，假引擎） ────────────────────────────────────────────

func TestWallpaperOutputDirIsConfigurable(t *testing.T) {
	s := newTestServer(t)
	s.reg.Register(&fakeWallpaperEngine{})

	dst := filepath.Join(t.TempDir(), "我的壁纸", "桌面")
	code, body := doReq(t, s, "POST", "/api/wallpapers/dir", map[string]any{"kind": "desktop", "dir": dst})
	if code != 200 {
		t.Fatalf("改输出目录应当 200，实际 %d（%v）", code, body)
	}
	if got, _ := body["dir"].(string); !sameFile(got, dst) {
		t.Fatalf("应当生效成 %s，实际 %q", dst, got)
	}

	// ① 生成**落到新目录**
	out := wallpapersGenerateOne(t, s, wallpaperKindDesktop, 4242)
	if !pathWithin(dst, out) {
		t.Errorf("出图必须落到用户选的目录里：%s 不在 %s 内", out, dst)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("出图文件不在盘上：%v", err)
	}

	// ② 清单**从新目录读**（不是"生成写 A、清单读 B"）
	code, body = doReq(t, s, "GET", "/api/wallpapers", nil)
	if code != 200 {
		t.Fatalf("GET 应当 200，实际 %d", code)
	}
	items, _ := body["desktop"].([]any)
	if len(items) != 1 {
		t.Fatalf("桌面分类应当有 1 张（在新目录里），实际 %d（%v）", len(items), body["desktop"])
	}
	it, _ := items[0].(map[string]any)
	if f, _ := it["file"].(string); !sameFile(f, out) {
		t.Errorf("清单里的那张应当是刚出的 %s，实际 %q", out, f)
	}
	dirs, _ := body["dirs"].(map[string]any)
	dinfo, _ := dirs[wallpaperKindDesktop].(map[string]any)
	if p, _ := dinfo["path"].(string); !sameFile(p, dst) {
		t.Errorf("dirs.desktop.path 应当是 %s，实际 %q", dst, p)
	}
	if custom, _ := dinfo["custom"].(bool); !custom {
		t.Errorf("改过之后 custom 应当是 true：%v", dinfo)
	}
	if exists, _ := dinfo["exists"].(bool); !exists {
		t.Errorf("刚建过的目录 exists 应当是 true：%v", dinfo)
	}

	// ③ 只读图：新目录里的图能读（守卫跟着目录走）
	if code, _ := doReq(t, s, "GET", "/api/wallpapers/image?path="+urlEnc(out), nil); code != 200 {
		t.Errorf("新目录里的图应当能只读输出，实际 %d", code)
	}

	// ④ 设置落盘（重启后还记得）
	raw, err := os.ReadFile(wallpaperSettingsPath(s.wallpaperRoot()))
	if err != nil {
		t.Fatalf("设置没落盘：%v", err)
	}
	if !strings.Contains(string(raw), "我的壁纸") {
		t.Errorf("设置文件里应当记着这个目录，实际 %s", string(raw))
	}
}

func TestWallpaperDirRejectsBadTargetsAndKeepsSetting(t *testing.T) {
	s := newTestServer(t)
	def := wallpaperDefaultDir(s.wallpaperRoot(), wallpaperKindDesktop)

	notDir := filepath.Join(t.TempDir(), "不是目录.txt")
	writeFile(t, notDir, []byte("x"))

	cases := []struct {
		name, dir, want string
	}{
		{"相对路径", filepath.Join("壁纸", "桌面"), "绝对路径"},
		{"是个文件", notDir, "不是目录"},
	}
	if vol := filepath.VolumeName(def); vol != "" {
		cases = append(cases, struct{ name, dir, want string }{"驱动器根", vol + string(filepath.Separator), "驱动器根"})
	}
	for _, c := range cases {
		code, body := doReq(t, s, "POST", "/api/wallpapers/dir", map[string]any{"kind": "desktop", "dir": c.dir})
		if code != 400 {
			t.Errorf("%s（%q）该 400，实际 %d（%v）", c.name, c.dir, code, body)
			continue
		}
		if msg := errTextOf(body); !strings.Contains(msg, c.want) {
			t.Errorf("%s 的报错要说清为什么（含 %q），实际 %q", c.name, c.want, msg)
		}
	}
	// 系统目录：本机有 SystemRoot 才测（不许拿"别的机器上可能没有"当借口静默跳过整条纪律）
	if root := strings.TrimSpace(os.Getenv("SystemRoot")); root != "" {
		code, body := doReq(t, s, "POST", "/api/wallpapers/dir",
			map[string]any{"kind": "desktop", "dir": filepath.Join(root, "manjux-wallpaper-tmp")})
		if code != 400 || !strings.Contains(errTextOf(body), "系统目录") {
			t.Errorf("指向系统目录该 400 且说清，实际 %d（%v）", code, body)
		}
	}

	// 全被拒之后：设置**保持原样**（不许把库指到一个用不了的目录）
	if got := s.wallpaperDirOf(wallpaperKindDesktop); !sameFile(got, def) {
		t.Errorf("被拒之后设置必须保持默认 %s，实际 %s", def, got)
	}
	if _, err := os.ReadFile(wallpaperSettingsPath(s.wallpaperRoot())); err == nil {
		t.Errorf("一次都没设成功过，不该留下设置文件")
	}
}

func TestWallpaperDirChangeKeepsOldFilesAndGuardFollows(t *testing.T) {
	s := newTestServer(t)
	s.reg.Register(&fakeWallpaperEngine{})

	old := wallpapersGenerateOne(t, s, wallpaperKindDesktop, 7)

	dst := filepath.Join(t.TempDir(), "别处")
	if code, body := doReq(t, s, "POST", "/api/wallpapers/dir", map[string]any{"kind": "desktop", "dir": dst}); code != 200 {
		t.Fatalf("改目录应当 200，实际 %d（%v）", code, body)
	}
	// ① 老图**一个都不许搬**（改目录只改"以后往哪写"）
	if _, err := os.Stat(old); err != nil {
		t.Errorf("换目录不许搬动老文件：%v", err)
	}
	// ② 新清单是空的 —— 界面必须如实（不许把老目录的图还列在这儿）
	if n := len(s.listWallpapers(wallpaperKindDesktop)); n != 0 {
		t.Errorf("换到新目录后清单该是空的，实际 %d 张", n)
	}
	// ③ 老图在新配置下不再属于这一类 ⇒ 只读图 403（报错要说清为什么）
	code, body := doReq(t, s, "GET", "/api/wallpapers/image?path="+urlEnc(old), nil)
	if code != 403 {
		t.Errorf("老目录的图在新配置下该 403，实际 %d（%v）", code, body)
	}
	if msg := errTextOf(body); !strings.Contains(msg, "输出目录") {
		t.Errorf("这条 403 要说清是「改过输出目录」造成的，实际 %q", msg)
	}
	// ④ 可逆：恢复默认之后老图又能读
	if code, body := doReq(t, s, "POST", "/api/wallpapers/dir", map[string]any{"kind": "desktop", "dir": ""}); code != 200 {
		t.Fatalf("恢复默认应当 200，实际 %d（%v）", code, body)
	} else if note, _ := body["note"].(string); !strings.Contains(note, "恢复默认") {
		t.Errorf("恢复默认要如实说明（老图不搬），实际 %q", note)
	}
	if code, _ := doReq(t, s, "GET", "/api/wallpapers/image?path="+urlEnc(old), nil); code != 200 {
		t.Errorf("恢复默认后老图应当又能读，实际 %d", code)
	}
}

func TestWallpaperScreenEndpointReportsWhatWillBeMade(t *testing.T) {
	s := newTestServer(t)
	s.wallpaperScreen = func() (int, int) { return 3440, 1440 } // 门不许依赖跑测试那台机器的屏幕

	code, body := doReq(t, s, "GET", "/api/wallpapers", nil)
	if code != 200 {
		t.Fatalf("GET 应当 200，实际 %d", code)
	}
	sc, _ := body["screen"].(map[string]any)
	if sc == nil {
		t.Fatalf("响应里没有 screen：%v", body)
	}
	if int(sc["w"].(float64)) != 3440 || int(sc["h"].(float64)) != 1440 {
		t.Errorf("screen 要回本机屏幕：%v", sc)
	}
	wantW, wantH, _ := sizeForScreen(3440, 1440)
	if int(sc["fit_w"].(float64)) != wantW || int(sc["fit_h"].(float64)) != wantH {
		t.Errorf("screen.fit 要是 %d×%d（界面直接填这个），实际 %v", wantW, wantH, sc)
	}
	// 上限抬到 4096 之后，真机 3440×1440 的屏幕**不再被夹**，但仍要如实说清"出的是 3424×1440 以及为什么"
	// （32 对齐/像素预算这套口径就是用户最容易觉得"我写 3440 你怎么出别的"的地方）
	if note, _ := sc["note"].(string); !strings.Contains(note, "3424×1440") || !strings.Contains(note, "32 对齐") {
		t.Errorf("要如实说清实际出货尺寸与口径（不然用户以为能出 3440 宽）：%q", note)
	}
	lim, _ := body["limits"].(map[string]any)
	if lim == nil || int(lim["max_side"].(float64)) != wallpaperMaxSide || int(lim["align"].(float64)) != wallpaperAlign {
		t.Errorf("limits 要把硬口径交给界面显示：%v", body["limits"])
	}

	// 读不到屏幕 ⇒ 0（前端据此把「按我的屏幕」置灰）
	s2 := newTestServer(t)
	_, b2 := doReq(t, s2, "GET", "/api/wallpapers", nil)
	if w := int(b2["screen"].(map[string]any)["w"].(float64)); w != 0 {
		t.Errorf("读不到屏幕时应当回 0，实际 %d", w)
	}
}

// ── 接线（源码级 + 前端计数） ───────────────────────────────────────────

func TestWallpaperDirWiringIsComplete(t *testing.T) {
	srv := mustServerSrc(t, "server.go")
	want := `m.HandleFunc("POST /api/wallpapers/dir", s.handleWallpaperDir)`
	if n := strings.Count(srv, want); n != 1 {
		t.Errorf("server.go 里这条路由应恰出现 1 次，实际 %d 次：%s", n, want)
	}

	main := mustServerSrc(t, "wallpapers.go")
	dirs := mustServerSrc(t, "wallpaper_dirs.go")
	contract := mustServerSrc(t, "http_contract_test.go")

	// ① 目录唯一出处：清单不给目录参数（给参数就留了"读 A 写 B"的门）
	if n := strings.Count(main, "func (s *Server) listWallpapers(kind string) []wallpaperItem"); n != 1 {
		t.Errorf("listWallpapers 必须是**只收分类**的方法（目录自己从 wallpaperDirOf 取），实际 %d 处", n)
	}
	if strings.Contains(main, "func listWallpapers(") {
		t.Errorf("旧的两参数 listWallpapers 不许留着（那就是第二个出处）")
	}
	// ② 生成处用唯一出处
	if n := strings.Count(main, "dir := s.wallpaperDirOf(k)"); n != 1 {
		t.Errorf("生成落盘处应当用 s.wallpaperDirOf(k)（恰 1 处），实际 %d 处", n)
	}
	// ③ 默认目录只在 wallpaperDefaultDir 里拼一次
	if n := strings.Count(dirs, "filepath.Join(root, normWallpaperKind(kind))"); n != 1 {
		t.Errorf("默认目录只许在 wallpaperDefaultDir 里拼一次，实际 %d 处", n)
	}
	// ④ 播报：改失败/写失败/读不懂三处都要说（静默降级是本仓库的硬伤）
	for _, wantLog := range []string{"⚠ 壁纸输出目录没改成", "⚠ 壁纸输出目录设置写失败", "⚠ 壁纸输出目录设置读不懂"} {
		if !strings.Contains(dirs, wantLog) {
			t.Errorf("wallpaper_dirs.go 少了播报 %q", wantLog)
		}
	}
	// ⑤ 探可写：设目录时必须当场写一个临时文件（"能写"这件事不许等出图时才炸）。
	// 判据咬**调用那一行**（`if err := writeProbeFile(clean); err != nil {`）——
	// 只判"文件里出现过 writeProbeFile"的话，把调用改成永不成立照样绿（反向验证 E4 咬出）。
	if !strings.Contains(dirs, "if err := writeProbeFile(clean); err != nil {") || !strings.Contains(dirs, "os.CreateTemp") {
		t.Errorf("设目录要当场探可写（`if err := writeProbeFile(clean); err != nil {` + os.CreateTemp）")
	}
	// ⑥ 测试替身：屏幕分辨率也要注入（否则某条用例会读跑测试那台机器的屏幕）
	if !strings.Contains(contract, "s.wallpaperScreen = ") {
		t.Errorf("newTestServer 要注入固定的屏幕分辨率替身")
	}
}

func TestWallpaperDirFrontendWiring(t *testing.T) {
	vue := readFrontend(t, "src/views/WallpapersView.vue")
	store := readFrontend(t, "src/store.js")

	for _, want := range []string{"wallpaperDir:", "wallpaperSize:"} {
		if !strings.Contains(store, want) {
			t.Errorf("store.js 没导出 %s", want)
		}
	}
	// 判据咬**按钮标记**（不是那几个字）：`按本机屏幕` 也出现在 JS 的文案里（`已按本机屏幕填写`），
	// 只数文字的话，把按钮删了照样绿（反向验证 E9/E11 咬出）。
	for _, want := range []string{"api.wallpaperDir(", "api.wallpaperSize(", "OpenDirectoryDialog",
		">改目录</el-button>", ">按本机屏幕</el-button>", ">恢复默认</el-button>", "输出目录"} {
		if n := strings.Count(vue, want); n < 1 {
			t.Errorf("WallpapersView.vue 里少了 %q（接口/能力写好了没人用 = 没做）", want)
		}
	}
	// 自定义宽高两个输入框都要在（只给一个 = 做一半）
	if n := strings.Count(vue, "deskW"); n < 1 {
		t.Errorf("自定义宽要能填（deskW）")
	}
	if n := strings.Count(vue, "deskH"); n < 1 {
		t.Errorf("自定义高要能填（deskH）")
	}
	// 实际会出多少必须显示出来（不许写着 3440 出 2048）
	if !strings.Contains(vue, "sizeNote") {
		t.Errorf("要显示 sizeNote（超上限/没对齐时的实际尺寸）")
	}
}
