package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// wallpaper_desktop_test.go 桌面壁纸「设成系统壁纸」的门（2026-10-10 用户：
// 「桌面壁纸可以管理设置当前系统桌面壁纸（幻灯片、单图等相关功能）」）。
//
// 三条纪律（对应文件头的口径）：
//
//	① **绝不动真桌面**：设系统壁纸的出口在测试里一律换成记录器/抛错器（newTestServer 默认就是空操作），
//	   本文件里任何一条用例都不许调真 `SystemParametersInfoW`。**真机探针在另一个文件**
//	   （zz_probe_wallpaper_desktop_live_test.go），要 MANJUX_LIVE_PROBE=1 才跑。
//	② **判据喂闸门本体**：间隔/顺序/「下一个该设谁」都直接喂纯函数，不构造"已经被上游筛过的数据"。
//	③ **失败不许写状态**：设图失败时 `desktop.json` 必须保持原样 —— 否则界面说「已设为壁纸」，系统上什么都没变。

// ── 夹具（照真机形态写：文件名、画幅、伴随信息、来源都跟真机一致） ──────────

// deskFixture 往壁纸库的 desktop/ 分类里落一张真图连同它的伴随信息，返回绝对路径。
//
// 伴随信息里带 `created`：轮换顺序（从早到晚）就是照它排的 —— 真机上这个值来自
// `/api/wallpapers` 那张清单（`wallpaperItem.Created`），门里必须给**确定的**时间点，否则顺序没法断言。
func deskFixture(t *testing.T, s *Server, name, created string) string {
	t.Helper()
	p := filepath.Join(wallpaperKindDir(s.wallpaperRoot(), wallpaperKindDesktop), name)
	writeFile(t, p, []byte("PNG-FAKE-"+name))
	b, err := json.MarshalIndent(wallpaperItem{
		File: p, Kind: wallpaperKindDesktop, W: 1920, H: 1088, Seed: 3440,
		Prompt: "horizontal desktop wallpaper, wide cinematic landscape composition, large calm negative space",
		From:   "平台 LLM 按你的描述写", Created: created,
	}, "", "  ")
	if err != nil {
		t.Fatalf("夹具序列化失败:%v", err)
	}
	writeFile(t, wallpaperMetaPath(p), b)
	return p
}

// errTextOf 取错误响应里的那句话（形状见 writeErr：{"error": "..."}）。
func errTextOf(body map[string]any) string {
	s, _ := body["error"].(string)
	return s
}

// nonCommentLines 只留**代码行**（整行注释与行尾注释都去掉）。
//
// 为什么要有它：本轮的判据之一是「平台不许写 Windows 那套幻灯片配置」——
// 而文件头**注释里**正解释着那个键（`SlideshowDirectoryPath1` 是二进制 blob）。
// 断言若连着注释一起咬，就会把"解释"判成"违规"（第 49 轮"断言命中注释"同一形态）。
func nonCommentLines(src string) []string {
	out := make([]string, 0, 256)
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") {
			continue
		}
		if i := strings.Index(t, "//"); i >= 0 {
			t = strings.TrimSpace(t[:i])
		}
		out = append(out, t)
	}
	return out
}

// ── 纯函数（直接喂本体） ─────────────────────────────────────────────────

func TestNormDeskInterval(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 30}, {-5, 30}, {1, 1}, {30, 30}, {1440, 1440}, {1441, 1440}, {100000, 1440},
	}
	for _, c := range cases {
		if got := normDeskInterval(c.in); got != c.want {
			t.Errorf("normDeskInterval(%d) = %d，想要 %d（间隔要夹在 [1,1440] 分钟，没给就 30）", c.in, got, c.want)
		}
	}
	if got := normDeskMode("SLIDESHOW"); got != deskModeSlideshow {
		t.Errorf("normDeskMode 大小写不敏感，实际 %q", got)
	}
	if got := normDeskMode(""); got != deskModeSingle {
		t.Errorf("没给模式就单图，实际 %q", got)
	}
}

func TestDeskOrderIsPlaylistAndShuffleIsReproducible(t *testing.T) {
	old := wallpaperItem{File: filepath.Join("D:", "wp", "a.png"), Created: "2026-10-10T10:00:00Z"}
	mid := wallpaperItem{File: filepath.Join("D:", "wp", "b.png"), Created: "2026-10-10T11:00:00Z"}
	newest := wallpaperItem{File: filepath.Join("D:", "wp", "c.png"), Created: "2026-10-10T12:00:00Z"}

	// 顺序模式：从早到晚（像播放列表），与传进来的次序无关
	seq := deskOrder([]wallpaperItem{newest, mid, old}, false, 0)
	if len(seq) != 3 || !sameFile(seq[0].File, old.File) || !sameFile(seq[2].File, newest.File) {
		t.Errorf("顺序模式应从早到晚排：%v", []string{seq[0].File, seq[1].File, seq[2].File})
	}
	if deskOrder(nil, true, 7) == nil {
		// 空清单不许 panic，也不许回一个能骗过 range 的东西
	}

	// 随机：同一 seed **必得同一顺序**（可复现），且一张不丢、不重
	sh1 := deskOrder([]wallpaperItem{newest, mid, old}, true, 20261010)
	sh2 := deskOrder([]wallpaperItem{mid, old, newest}, true, 20261010)
	if len(sh1) != 3 {
		t.Fatalf("洗牌不许丢图：%v", sh1)
	}
	for i := range sh1 {
		if !sameFile(sh1[i].File, sh2[i].File) {
			t.Errorf("同一 seed 的洗牌必须可复现：%v vs %v", sh1, sh2)
			break
		}
	}
	seen := map[string]bool{}
	for _, it := range sh1 {
		seen[strings.ToLower(it.File)] = true
	}
	if len(seen) != 3 {
		t.Errorf("洗牌不许重复/丢图：%v", sh1)
	}

	// 刚设过的那张，下一个心跳要跳到**下一张**（不是把自己再设一遍）
	paths := []string{old.File, mid.File, newest.File}
	if got := deskPosAfter(paths, old.File); got != 1 {
		t.Errorf("刚设过第一张 ⇒ 下一次该设第二张（下标 1），实际 %d", got)
	}
	if got := deskPosAfter(paths, newest.File); got != 3 {
		t.Errorf("刚设过最后一张 ⇒ 下标该是 3（走完重排一轮），实际 %d", got)
	}
	if got := deskPosAfter(paths, filepath.Join("D:", "wp", "不在序列里.png")); got != 0 {
		t.Errorf("不在序列里 ⇒ 从头开始（0），实际 %d", got)
	}
	if got := deskPosAfter(nil, old.File); got != 0 {
		t.Errorf("空序列 ⇒ 0，实际 %d", got)
	}
	// 路径比对大小写/分隔符不敏感（Windows 上同一个文件可能写成 D:\ 与 d:/）
	if !sameFile(`D:\wp\A.png`, `d:/wp/a.png`) {
		t.Errorf("同一路径的不同写法要认成同一个文件")
	}
}

// ── HTTP：不支持的平台要如实拒绝 ─────────────────────────────────────────

func TestWallpaperDesktopUnsupportedPlatformIsHonest(t *testing.T) {
	s := newTestServer(t)
	s.wallpaperDesktopOK = func() bool { return false }

	code, body := doReq(t, s, "GET", "/api/wallpapers/desktop", nil)
	if code != 200 {
		t.Fatalf("GET 状态应 200（前端要能显示「本平台不支持」），实际 %d：%v", code, body)
	}
	if body["supported"] != false {
		t.Errorf("不支持时 supported 要如实为 false：%v", body["supported"])
	}
	if note, _ := body["note"].(string); !strings.Contains(note, "不支持") {
		t.Errorf("不支持时要把原因说出来，实际 note=%q", note)
	}
	code, body = doReq(t, s, "POST", "/api/wallpapers/desktop/apply",
		map[string]any{"file": filepath.Join("D:", "wp", "x.png"), "mode": "single"})
	if code != 501 {
		t.Errorf("不支持的平台要 501（如实拒绝，不假装设过），实际 %d：%v", code, body)
	}
}

// ── HTTP：单图 ──────────────────────────────────────────────────────────

func TestWallpaperDesktopApplySetsSystemWallpaperAndRemembers(t *testing.T) {
	s := newTestServer(t)
	p := deskFixture(t, s, "wp-desktop-20261010-120000-707.png", "2026-10-10T12:00:00Z")

	var mu sync.Mutex
	var set []string
	s.setDesktopWallpaper = func(x string) error { // 记录器：绝不去动真桌面
		mu.Lock()
		set = append(set, x)
		mu.Unlock()
		return nil
	}
	s.getDesktopWallpaper = func() string { return p } // 系统现在就是这张（读回来的那一头）

	code, body := doReq(t, s, "POST", "/api/wallpapers/desktop/apply",
		map[string]any{"file": p, "mode": "single"})
	if code != 200 {
		t.Fatalf("设为壁纸应 200，实际 %d：%v", code, body)
	}
	mu.Lock()
	got := append([]string(nil), set...)
	mu.Unlock()
	if len(got) != 1 || !sameFile(got[0], p) {
		t.Fatalf("设系统壁纸要恰被调用一次且指向这张：%v", got)
	}
	if mode, _ := body["mode"].(string); mode != deskModeSingle {
		t.Errorf("单图模式该回 mode=single，实际 %q", mode)
	}
	for _, k := range []string{"file", "last_set"} {
		v, _ := body[k].(string)
		if !sameFile(v, p) {
			t.Errorf("响应里 %s 要是这张（%s），实际 %q", k, filepath.Base(p), v)
		}
	}
	if body["running"] != false {
		t.Errorf("单图模式不该在轮换：running=%v", body["running"])
	}
	if body["current_in_library"] != true || body["current_name"] != filepath.Base(p) {
		t.Errorf("系统当前壁纸就是库里的这张，界面要能标出来：current_in_library=%v current_name=%v",
			body["current_in_library"], body["current_name"])
	}
	if note, _ := body["note"].(string); !strings.Contains(note, "设为壁纸") {
		t.Errorf("单图模式的提示要把下一步说清：%q", note)
	}
	// 状态落盘（重启后界面能回显「上次选的是哪张」）
	raw, err := os.ReadFile(desktopStatePath(s.wallpaperRoot()))
	if err != nil {
		t.Fatalf("状态文件没落盘：%v", err)
	}
	var st desktopState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("状态文件读不懂：%v", err)
	}
	if st.Mode != deskModeSingle || !sameFile(st.File, p) || !sameFile(st.LastSet, p) {
		t.Errorf("落盘的状态不对：%+v", st)
	}
	// 再 GET 一次：状态要从盘上读得回来（不许只在内存里）
	code, body = doReq(t, s, "GET", "/api/wallpapers/desktop", nil)
	if code != 200 || body["mode"] != deskModeSingle {
		t.Errorf("GET 状态要能回显 mode=single，实际 %d：%v", code, body)
	}
	if n, _ := body["count"].(float64); int(n) != 1 {
		t.Errorf("桌面分类里有 1 张，实际 count=%v", body["count"])
	}
}

func TestWallpaperDesktopApplyRejectsNonDesktopAndOutsiders(t *testing.T) {
	s := newTestServer(t)

	// ① 手机壁纸（竖版）设桌面壁纸：会被裁 ⇒ 400 + 说清为什么
	mob := filepath.Join(wallpaperKindDir(s.wallpaperRoot(), wallpaperKindMobile), "wp-mobile-20261010-120000-88.png")
	writeFile(t, mob, []byte("PNG-FAKE"))
	code, body := doReq(t, s, "POST", "/api/wallpapers/desktop/apply",
		map[string]any{"file": mob, "mode": "single"})
	if code != 400 {
		t.Fatalf("手机壁纸设成桌面壁纸该 400（竖版会被裁），实际 %d：%v", code, body)
	}
	if msg := errTextOf(body); !strings.Contains(msg, "竖版") {
		t.Errorf("要把「为什么不行」说清楚（竖版会被裁），实际 %q", msg)
	}

	// ② 库外路径：403（防穿越）
	outside := filepath.Join(t.TempDir(), "别的图.png")
	writeFile(t, outside, []byte("PNG-FAKE"))
	code, body = doReq(t, s, "POST", "/api/wallpapers/desktop/apply",
		map[string]any{"file": outside, "mode": "single"})
	if code != 403 {
		t.Errorf("库外路径该 403，实际 %d：%v", code, body)
	}

	// ③ 桌面分类里、但盘上已经不在了：404（不许设一张不存在的图还回 200）
	ghost := filepath.Join(wallpaperKindDir(s.wallpaperRoot(), wallpaperKindDesktop), "wp-desktop-20261010-130000-1.png")
	code, body = doReq(t, s, "POST", "/api/wallpapers/desktop/apply",
		map[string]any{"file": ghost, "mode": "single"})
	if code != 404 {
		t.Errorf("盘上没有这张该 404，实际 %d：%v", code, body)
	}

	// 三种失败都不许留下状态文件（失败就是失败，没设成功过）
	if _, err := os.ReadFile(desktopStatePath(s.wallpaperRoot())); err == nil {
		t.Errorf("设置失败时不许写状态 —— 界面会显示「已设为壁纸」而系统上什么都没变")
	}
}

func TestWallpaperDesktopApplyReportsSetterFailureAndKeepsState(t *testing.T) {
	s := newTestServer(t)
	p := deskFixture(t, s, "wp-desktop-20261010-120000-999.png", "2026-10-10T12:00:00Z")
	s.setDesktopWallpaper = func(string) error {
		return fmt.Errorf("SystemParametersInfoW(SPI_SETDESKWALLPAPER) 失败:拒绝访问")
	}

	code, body := doReq(t, s, "POST", "/api/wallpapers/desktop/apply",
		map[string]any{"file": p, "mode": "single"})
	if code != 500 {
		t.Fatalf("系统接口失败要 500，实际 %d：%v", code, body)
	}
	if msg := errTextOf(body); !strings.Contains(msg, "SPI_SETDESKWALLPAPER") {
		t.Errorf("要把系统那句话**原样**转出来（用户要能拿着它去查），实际 %q", msg)
	}
	if _, err := os.ReadFile(desktopStatePath(s.wallpaperRoot())); err == nil {
		t.Errorf("设失败就不许写状态：界面会显示「已设为壁纸」，而系统上什么都没变")
	}
}

// ── HTTP：幻灯片（平台自己轮换） ─────────────────────────────────────────

func TestWallpaperDesktopSlideshowRotatesAndStops(t *testing.T) {
	s := newTestServer(t)
	a := deskFixture(t, s, "wp-desktop-20261010-100000-11.png", "2026-10-10T10:00:00Z")
	b := deskFixture(t, s, "wp-desktop-20261010-110000-22.png", "2026-10-10T11:00:00Z")
	c := deskFixture(t, s, "wp-desktop-20261010-120000-33.png", "2026-10-10T12:00:00Z")

	var mu sync.Mutex
	var set []string
	s.setDesktopWallpaper = func(x string) error {
		mu.Lock()
		set = append(set, x)
		mu.Unlock()
		return nil
	}
	s.deskTick = 20 * time.Millisecond // 门里不等 30 分钟

	code, body := doReq(t, s, "POST", "/api/wallpapers/desktop/apply",
		map[string]any{"file": a, "mode": "slideshow", "interval_min": 30, "shuffle": false})
	if code != 200 {
		t.Fatalf("开轮换应 200，实际 %d：%v", code, body)
	}
	if body["running"] != true {
		t.Errorf("开轮换后 running 要是 true：%v", body["running"])
	}
	if iv, _ := body["interval_min"].(float64); int(iv) != 30 {
		t.Errorf("间隔要回 30 分钟，实际 %v", body["interval_min"])
	}
	if note, _ := body["note"].(string); !strings.Contains(note, "只在平台运行期间生效") {
		t.Errorf("边界必须事先说清（退出平台就停在最后一张）：%q", note)
	}

	// 顺序模式：a（刚点的那张，已立刻设上）→ b → c → **再回到 a**（走完一轮要重排 ——
	// 重排那条路正是「锁里再拿锁」这类死锁会现形的地方，所以这里必须跨过一轮，别只等到第 3 次）
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(set)
		mu.Unlock()
		if n >= 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	seq := append([]string(nil), set...)
	mu.Unlock()
	if len(seq) < 4 {
		t.Fatalf("轮换没跑起来（或走完一轮就卡住了）：只设了 %d 次（%v）", len(seq), seq)
	}
	for i, want := range []string{a, b, c, a} {
		if !sameFile(seq[i], want) {
			t.Errorf("第 %d 次该设 %s，实际 %s（全序列 %v）", i+1,
				filepath.Base(want), filepath.Base(seq[i]), seq)
		}
	}

	// 停：状态回到 single，而且**真的不再换图**
	code, body = doReq(t, s, "POST", "/api/wallpapers/desktop/stop", nil)
	if code != 200 {
		t.Fatalf("停轮换应 200，实际 %d：%v", code, body)
	}
	if body["running"] != false || body["mode"] != deskModeSingle {
		t.Errorf("停之后该 running=false 且 mode=single，实际 %v / %v", body["running"], body["mode"])
	}
	if note, _ := body["note"].(string); !strings.Contains(note, "设为壁纸") {
		t.Errorf("停之后的提示该指向单图入口：%q", note)
	}
	mu.Lock()
	before := len(set)
	mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	after := len(set)
	mu.Unlock()
	if after != before {
		t.Errorf("停之后还在换图：%d → %d（说明 goroutine 没真退出）", before, after)
	}
	// 停之后不再有状态写入：mode 落盘是 single
	raw, err := os.ReadFile(desktopStatePath(s.wallpaperRoot()))
	if err != nil {
		t.Fatalf("停之后状态文件该在：%v", err)
	}
	var st desktopState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("状态文件读不懂：%v", err)
	}
	if st.Mode != deskModeSingle {
		t.Errorf("停之后的落盘模式该是 single，实际 %q", st.Mode)
	}
}

// ── 接线 ────────────────────────────────────────────────────────────────

func TestDesktopWallpaperWiringIsComplete(t *testing.T) {
	srv := mustServerSrc(t, "server.go")
	for _, want := range []string{
		`m.HandleFunc("GET /api/wallpapers/desktop", s.handleWallpaperDesktop)`,
		`m.HandleFunc("POST /api/wallpapers/desktop/apply", s.handleWallpaperDesktopApply)`,
		`m.HandleFunc("POST /api/wallpapers/desktop/stop", s.handleWallpaperDesktopStop)`,
	} {
		if n := strings.Count(srv, want); n != 1 {
			t.Errorf("server.go 里这条路由应恰出现 1 次，实际 %d 次：%s", n, want)
		}
	}

	main := mustServerSrc(t, "wallpaper_desktop.go")
	win := mustServerSrc(t, "wallpaper_desktop_windows.go")
	other := mustServerSrc(t, "wallpaper_desktop_other.go")
	contract := mustServerSrc(t, "http_contract_test.go")

	// ① 顺序：**先设系统壁纸、成功之后才写状态**（设失败不许写状态）
	apply := funcBody(main, "func (s *Server) handleWallpaperDesktopApply")
	iSet := strings.Index(apply, "s.deskSet(p)")
	iSave := strings.Index(apply, "s.deskUpdate(")
	if iSet < 0 || iSave < 0 || iSet > iSave {
		t.Errorf("handleWallpaperDesktopApply 必须**先设系统壁纸、成功之后才写状态**（set=%d save=%d）—— "+
			"反过来就是「界面说已设为壁纸，系统上什么都没变」", iSet, iSave)
	}

	// ② 注册表**只读**：全包不许出现任何写注册表的调用（平台的轮换不碰 Windows 那套幻灯片配置）
	for _, name := range []string{"wallpaper_desktop.go", "wallpaper_desktop_windows.go"} {
		for _, line := range nonCommentLines(mustServerSrc(t, name)) {
			for _, banned := range []string{"RegSetValueEx", "RegCreateKeyEx", "RegDeleteValue"} {
				if strings.Contains(line, banned) {
					t.Errorf("%s 的代码里出现了写注册表的 %q —— 平台只许调 Windows 的单图接口，"+
						"那套幻灯片配置（SlideshowDirectoryPath1 是 354 字节二进制）一个字节都不许动", name, banned)
				}
			}
		}
	}

	// ③ 平台真值各在一处：Windows 走 user32 的 SystemParametersInfoW；别处如实说「不支持」
	if !strings.Contains(win, `syscall.NewLazyDLL("user32.dll")`) ||
		!strings.Contains(win, "SystemParametersInfoW") || !strings.Contains(win, "spiSetDeskWallpaper") {
		t.Errorf("wallpaper_desktop_windows.go 必须用 Windows 自己的接口 SystemParametersInfoW 设单图")
	}
	if !strings.Contains(win, "spiGetDeskWallpaper") {
		t.Errorf("设完要**读回来对齐**（SPI_GETDESKWALLPAPER）—— 只报「已设置」不知道系统那头是哪张")
	}
	if !strings.Contains(other, "func desktopWallpaperSupported() bool { return false }") {
		t.Errorf("非 Windows 的平台必须如实说「不支持」，不许假装做过")
	}
	// 门本身守住的纪律：测试替身必须默认接上（否则某条用例会动到真桌面）
	if !strings.Contains(contract, "s.setDesktopWallpaper = ") {
		t.Errorf("newTestServer 必须默认注入「什么都不做」的设墙替身 —— 单元测试绝不许改跑测试那台机器的桌面")
	}

	// ④ 播报：设成功、设失败、轮换、跳过（文件被删）都要有日志，别静默
	for _, want := range []string{"已把", "⚠ 设系统桌面壁纸失败", "⚠ 桌面壁纸轮换跳过一张", "⚠ 桌面壁纸状态写失败"} {
		if !strings.Contains(main, want) {
			t.Errorf("wallpaper_desktop.go 少了播报 %q（静默降级是本仓库的硬伤）", want)
		}
	}
}

func TestWallpaperDesktopFrontendWiring(t *testing.T) {
	vue := readFrontend(t, "src/views/WallpapersView.vue")
	store := readFrontend(t, "src/store.js")

	for _, want := range []string{"wallpaperDesktop:", "wallpaperDesktopApply:", "wallpaperDesktopStop:"} {
		if !strings.Contains(store, want) {
			t.Errorf("store.js 没导出 %s", want)
		}
	}
	for _, want := range []string{"api.wallpaperDesktop(", "api.wallpaperDesktopApply(", "api.wallpaperDesktopStop("} {
		if n := strings.Count(vue, want); n < 1 {
			t.Errorf("WallpapersView.vue 里没接 %s（接口写好了没人调 = 没做）", want)
		}
	}
	// 计数断言：单图与轮换**两个入口都要有**（只做一个照样绿是第 48 轮的教训）。
	// 判据咬**按钮标记**而不是那几个字：`停止轮换` 那四个字也出现在 JS 的提示文案里
	// （`已停止轮换（系统停在最后一张）`）—— 只数文字的话，把按钮删了照样绿（2026-10-10 反向验证 D9 咬出）。
	for _, want := range []string{">设为壁纸</el-button>", ">开始轮换</el-button>", ">停止轮换</el-button>"} {
		if n := strings.Count(vue, want); n != 1 {
			t.Errorf("WallpapersView.vue 里入口 %q 应恰出现 1 次，实际 %d 次", want, n)
		}
	}
	// 只认桌面分类：这些入口必须排在 `kind === 'desktop'` 分支里
	// （手机壁纸是竖版，设到横版桌面上会被裁 —— 后端会 400，前端也不该给这个按钮）
	marker := strings.Index(vue, "kind === 'desktop'")
	btn := strings.Index(vue, "设为壁纸")
	if marker < 0 || btn < 0 || btn < marker {
		t.Errorf("「设为壁纸」必须在桌面分类的分支里（marker=%d 按钮=%d）—— "+
			"否则用户会对着一张竖版手机壁纸点它，然后被后端 400", marker, btn)
	}
	// 系统自己那套幻灯片的提醒要显示出来（平台先说清楚，别等用户自己犯疑）
	if !strings.Contains(vue, "native_note") {
		t.Errorf("WallpapersView.vue 要显示 native_note（Windows 自己那套幻灯片可能把平台设的图换掉）")
	}
}
