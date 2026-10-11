// wallpaper_desktop.go 桌面壁纸 = **真正设成本机系统桌面壁纸**（2026-10-10 用户：
// 「桌面壁纸可以管理设置当前系统桌面壁纸（幻灯片、单图等相关功能）」）。
//
// 两条腿，一条完全确定、一条是平台自己的：
//
//	① **单图**：`SystemParametersInfoW(SPI_SETDESKWALLPAPER)`（Windows 自己的接口，见
//	   wallpaper_desktop_windows.go）。设完**读回来对齐**（`SPI_GETDESKWALLPAPER`）——
//	   不许只报「已设置」却不知道系统那一头现在到底是哪张。
//	② **幻灯片**：由**平台自己轮换**（间隔 + 顺序/随机，见 deskRotateOnce），
//	   **不写 Windows 那套幻灯片配置**。
//
// 【为什么幻灯片不写系统那套】真机取证（2026-10-10，本机 Win11 build 26200）：
//
//	`HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Wallpapers\SlideshowDirectoryPath1`
//	实测是 **354 字节二进制**（外层像 base64，解开后是 shell item 结构：hex 头 80500503…，
//	里面没有可读的路径），**不是文档化的路径格式**。照猜去写就是把用户自己的幻灯片配置弄坏 ——
//	而真机里那套配置**正在用**：`BackgroundType=2`、`Interval=1800000`（30 分钟）、`Shuffle=1`、
//	历史壁纸 `BackgroundHistoryPath0..3` 指着用户自己的 data\壁纸\ 目录。
//
// 所以平台的轮换**只调 Windows 的单图接口**，一个字节都不动那套配置。边界如实说：
// **轮换只在平台运行期间生效，退出平台就停在最后一张**（要系统级幻灯片就得用 Windows 自己的设置）。
// 另外：系统自己那套幻灯片若还开着，它可能按自己的节奏把平台设的图换掉 ——
// 所以状态里带一条 `native_note`（**只读**注册表，见 nativeSlideshowNote），把这件事先说出来。
//
// 【为什么单图只认桌面分类】手机壁纸是竖版（9:16），设到 16:9 桌面上必然被裁掉两侧或被拉伸。
// 与其让用户点完才发现「不是我要的样子」，不如当场说明白（400 + 告诉他在哪儿出横版）。
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/fsutil"
)

const (
	deskModeSingle    = "single"    // 就这一张
	deskModeSlideshow = "slideshow" // 平台自己按间隔轮换

	// deskStateFile 状态文件放在壁纸库**根**上（不在 desktop/ 分类目录里，
	// 免得被 listWallpapers 当成一张图读出来）。
	deskStateFile = "desktop.json"

	deskDefaultIntervalMin = 30
	deskMinIntervalMin     = 1
	deskMaxIntervalMin     = 1440 // 一天
)

// normDeskMode 只认两个模式；其余（含空）归到单图。
func normDeskMode(m string) string {
	if strings.EqualFold(strings.TrimSpace(m), deskModeSlideshow) {
		return deskModeSlideshow
	}
	return deskModeSingle
}

// normDeskInterval 间隔（分钟）夹到 [1,1440]；<=0（没给）⇒ 30 分钟。
// 门 TestNormDeskInterval 直接喂这个函数。
func normDeskInterval(min int) int {
	if min <= 0 {
		return deskDefaultIntervalMin
	}
	if min < deskMinIntervalMin {
		return deskMinIntervalMin
	}
	if min > deskMaxIntervalMin {
		return deskMaxIntervalMin
	}
	return min
}

// deskOrderLabel 顺序偏好的人话名（播报与前端同一处）。
func deskOrderLabel(shuffle bool) string {
	if shuffle {
		return "随机"
	}
	return "顺序（先出早做的）"
}

// desktopState 用户选了什么（落盘，重启后界面能回显；但**不会**自动接着轮，见文件头边界）。
type desktopState struct {
	Mode        string `json:"mode"`         // single / slideshow
	File        string `json:"file"`         // 选中的那张（幻灯片模式下＝起点）
	IntervalMin int    `json:"interval_min"` // 幻灯片间隔（分钟）
	Shuffle     bool   `json:"shuffle"`      // 幻灯片顺序偏好
	// Picks **只让这些参与轮换**（绝对路径；空 = 桌面分类全部）。用户要的就是"随机只在我喜欢的那几张里换"。
	Picks   []string `json:"picks,omitempty"`
	LastSet string   `json:"last_set"` // 最近一次真正设上去的那张
	Applied string   `json:"applied"`  // RFC3339
}

// desktopStatePath 状态文件路径。
func desktopStatePath(root string) string { return filepath.Join(root, deskStateFile) }

// readDesktopState 从盘上读状态（读不到 ⇒ 默认值；坏文件 ⇒ 回默认值并**播报**）。
func (s *Server) readDesktopState() desktopState {
	st := desktopState{Mode: deskModeSingle, IntervalMin: deskDefaultIntervalMin}
	b, err := os.ReadFile(desktopStatePath(s.wallpaperRoot()))
	if err == nil {
		if uerr := json.Unmarshal(b, &st); uerr != nil {
			// 坏文件不当成「没有」：读不懂就回默认值，并把这件事说出来（不许静默）
			s.opLog("⚠ 桌面壁纸状态文件读不懂（回默认值）:%v", uerr)
			st = desktopState{Mode: deskModeSingle, IntervalMin: deskDefaultIntervalMin}
		}
	}
	st.Mode = normDeskMode(st.Mode)
	st.IntervalMin = normDeskInterval(st.IntervalMin)
	st.File = strings.TrimSpace(st.File)
	st.LastSet = strings.TrimSpace(st.LastSet)
	return st
}

// deskSave 落盘。失败**不致命**（这次设置已经生效了），但**不许静默**：
// 审计门 TestNoSwallowedPersistenceErrors 抓的就是「流程照跑、日志照报成功」。
func (s *Server) deskSave(st desktopState) {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		s.opLog("⚠ 桌面壁纸状态序列化失败:%v", err)
		return
	}
	root := s.wallpaperRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		s.opLog("⚠ 桌面壁纸目录建不出来(%s):%v", root, err)
		return
	}
	if err := fsutil.WriteFileAtomic(desktopStatePath(root), b); err != nil {
		s.opLog("⚠ 桌面壁纸状态写失败:%v —— 这次设置是生效的，只是记不住（重启界面不会回显）", err)
	}
}

// deskState 取内存里的状态（第一次访问时从盘上读一次）。
func (s *Server) deskState() desktopState {
	s.deskMu.Lock()
	defer s.deskMu.Unlock()
	if !s.deskLoaded {
		s.desk = s.readDesktopState()
		s.deskLoaded = true
	}
	return s.desk
}

// deskUpdate 改状态（f 里只改字段）+ 落盘，返回改完的状态。
func (s *Server) deskUpdate(f func(*desktopState)) desktopState {
	s.deskMu.Lock()
	if !s.deskLoaded {
		s.desk = s.readDesktopState()
		s.deskLoaded = true
	}
	f(&s.desk)
	st := s.desk
	s.deskMu.Unlock()
	s.deskSave(st)
	return st
}

// ── 顺序（纯函数，门直接喂） ─────────────────────────────────────────────

// sameFile 两个路径指不指同一个文件（Windows 盘符/大小写不敏感 ⇒ 一律不敏感地比）。
func sameFile(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// deskOrder 轮换顺序：先按**加入时间从早到晚**排（像播放列表），随机模式再用 seed 做一次
// **确定性洗牌**（同一 seed 必得同一顺序 ⇒ 门能直接喂、结果可复现）。
func deskOrder(files []wallpaperItem, shuffle bool, seed int64) []wallpaperItem {
	out := append([]wallpaperItem(nil), files...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created < out[j].Created })
	if !shuffle || len(out) < 2 {
		return out
	}
	r := seed
	if r == 0 {
		r = 20261010
	}
	next := func(n int) int {
		r = r*6364136223846793005 + 1442695040888963407 // LCG：溢出按 Go 定义回绕
		v := r >> 33
		if v < 0 {
			v = -v
		}
		return int(v % int64(n))
	}
	for i := len(out) - 1; i > 0; i-- {
		j := next(i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// deskPosAfter 序列里「刚设过的那张」之后的下标（不在序列里 / 序列为空 ⇒ 0）。
// 用途：点「设为壁纸」时那张会**立刻**设上去，轮换的第一个心跳不该再设一遍同一张。
func deskPosAfter(seq []string, cur string) int {
	for i, f := range seq {
		if sameFile(f, cur) {
			return i + 1
		}
	}
	return 0
}

// ── 设系统壁纸（出口可注入） ─────────────────────────────────────────────

// deskSet 设系统壁纸的**唯一出口**。
//
// 为什么留成可注入的：这件事有**真副作用**（会改跑代码那台机器的桌面），
// 所以单元测试里换成记录器/抛错器 —— 测试绝不去动真桌面壁纸（见 newTestServer）。
func (s *Server) deskSet(path string) error {
	if f := s.setDesktopWallpaper; f != nil {
		return f(path)
	}
	return setDesktopWallpaperOS(path)
}

// deskCurrent 读回「系统现在的桌面壁纸」（读不到就回空串，调用方如实说不知道）。
func (s *Server) deskCurrent() string {
	if f := s.getDesktopWallpaper; f != nil {
		return f()
	}
	p, err := currentDesktopWallpaperOS()
	if err != nil {
		return ""
	}
	return p
}

// ── 轮换（幻灯片） ───────────────────────────────────────────────────────

// deskSupported 本平台能不能改系统桌面壁纸（生产＝平台真值；测试可覆盖 —— 见 wallpaperDesktopOK）。
func (s *Server) deskSupported() bool {
	if f := s.wallpaperDesktopOK; f != nil {
		return f()
	}
	return desktopWallpaperSupported()
}

// deskRotateInterval 一次心跳的间隔：测试给了 deskTick 就按它走（否则门要等 30 分钟）。
func (s *Server) deskRotateInterval() time.Duration {
	if s.deskTick > 0 {
		return s.deskTick
	}
	return time.Duration(normDeskInterval(s.deskState().IntervalMin)) * time.Minute
}

// deskSeqBuild 排一条轮换序列（= **参与轮换的那批**按顺序偏好排好，取路径）。
//
// 参与集合＝`st.Picks`（用户在缩略图上勾的那些）；**空 = 桌面分类全部**（老行为，兼容）。
// Picks 里已经不在盘上的项**不进序列**（由 `deskPicksFilter` 挑出来、状态里如实点名；不在这里刷日志）。
func (s *Server) deskSeqBuild(seed int64) []string {
	st := s.deskState()
	cand, _ := deskPicksFilter(s.listWallpapers(wallpaperKindDesktop), st.Picks)
	files := deskOrder(cand, st.Shuffle, seed)
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.File)
	}
	return out
}

// deskPicksFilter 按"只让勾过的参与"过滤清单 —— 纯函数（门直接喂它，不碰磁盘）。
//
// 三态：① `picks` 为空 ⇒ **全部**参与（与老行为一致）；② 勾过的项按清单顺序留下；
// ③ 勾过但已不在盘上/已不属于这一类的 ⇒ 进 `missing`（**如实点名**，不是静默丢掉）。
func deskPicksFilter(all []wallpaperItem, picks []string) ([]wallpaperItem, []string) {
	if len(picks) == 0 {
		return all, nil
	}
	key := func(p string) string { return strings.ToLower(filepath.Clean(p)) }
	want := make(map[string]bool, len(picks))
	for _, p := range picks {
		want[key(p)] = true
	}
	var out []wallpaperItem
	kept := make(map[string]bool, len(all))
	for _, it := range all {
		if want[key(it.File)] {
			out = append(out, it)
			kept[key(it.File)] = true
		}
	}
	var missing []string
	for _, p := range picks {
		if !kept[key(p)] {
			missing = append(missing, filepath.Base(p))
		}
	}
	return out, missing
}

// deskStartRotation 起轮换：**先停旧的**（保证同一时刻只有一条 goroutine 在换图）。
func (s *Server) deskStartRotation() {
	s.deskStopRotation()
	seq := s.deskSeqBuild(time.Now().UnixNano())
	// 刚点「设为壁纸」的那张已经立刻设上去了 ⇒ 序列从它的**下一张**接着走
	pos := deskPosAfter(seq, s.deskState().File)
	s.deskMu.Lock()
	s.deskSeq, s.deskPos = seq, pos
	stop, done := make(chan struct{}), make(chan struct{})
	s.deskStop, s.deskDone, s.deskRun = stop, done, true
	s.deskMu.Unlock()
	if len(seq) == 0 {
		// 一张桌面壁纸都没有：如实说清楚，别留一条空转的 goroutine
		s.opLog("⚠ 桌面壁纸轮换没起来：桌面分类里一张图都没有")
		return
	}
	iv := s.deskRotateInterval()
	go func() {
		defer close(done)
		t := time.NewTicker(iv)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				s.deskRotateOnce()
			}
		}
	}()
}

// deskStopRotation 停轮换（幂等；关掉通道后**等它真的退出**再返回）。
func (s *Server) deskStopRotation() {
	s.deskMu.Lock()
	stop, done := s.deskStop, s.deskDone
	s.deskStop, s.deskDone, s.deskRun = nil, nil, false
	s.deskMu.Unlock()
	if stop != nil {
		close(stop)
		if done != nil {
			<-done
		}
	}
}

// deskRunning 轮换现在在不在跑。
func (s *Server) deskRunning() bool {
	s.deskMu.Lock()
	defer s.deskMu.Unlock()
	return s.deskRun
}

// deskRotateOnce 换一张（轮换的一次心跳；门也直接调它，不用等 ticker）。
//
// 序列走完就**重排**（随机模式下等于换一轮新的顺序）；盘上一张图都没有 ⇒ 如实报错，不改系统壁纸。
//
// 【死锁纪律】重排要拿 `deskMu`（`deskSeqBuild` 里要读状态），所以**必须在锁外**做 ——
// Go 的 mutex 不可重入，锁里再调 `deskSeqBuild` 就是自己把自己锁死
// （2026-10-10 实测：这一条被反向验证 D3 咬出来，`go test` 卡到 600 秒超时才报栈）。
func (s *Server) deskRotateOnce() (string, error) {
	s.deskMu.Lock()
	need := s.deskPos >= len(s.deskSeq)
	s.deskMu.Unlock()
	if need {
		seq := s.deskSeqBuild(time.Now().UnixNano())
		s.deskMu.Lock()
		s.deskSeq, s.deskPos = seq, 0
		s.deskMu.Unlock()
	}
	s.deskMu.Lock()
	seq, pos := s.deskSeq, s.deskPos
	next := ""
	if pos < len(seq) {
		next = seq[pos]
	}
	s.deskPos = pos + 1
	s.deskMu.Unlock()

	if next == "" {
		return "", fmt.Errorf("桌面壁纸轮换：桌面分类里没有可用的图")
	}
	if _, err := os.Stat(next); err != nil {
		// 轮换时那张被删了：如实播报，跳过它（不算整个轮换失败）
		s.opLog("⚠ 桌面壁纸轮换跳过一张（文件不在盘上）:%s", filepath.Base(next))
		return next, err
	}
	if err := s.deskSet(next); err != nil {
		s.opLog("⚠ 桌面壁纸轮换设图失败(%s):%v", filepath.Base(next), err)
		return next, err
	}
	s.deskUpdate(func(x *desktopState) {
		x.LastSet = next
		x.Applied = time.Now().Format(time.RFC3339)
	})
	s.opLog("🖼 桌面壁纸轮换:%s", filepath.Base(next))
	return next, nil
}

// ── 状态播报 ─────────────────────────────────────────────────────────────

// wallpaperDesktopStatus GET /api/wallpapers/desktop 的形状。
type wallpaperDesktopStatus struct {
	Supported        bool     `json:"supported"`     // 本平台能不能改系统桌面壁纸
	Picks            []string `json:"picks"`         // 参与轮换的那几张（空 = 全部；前端勾选即写这里）
	PicksCount       int      `json:"picks_count"`   // 其中**现在还在盘上**的张数
	PicksMissing     []string `json:"picks_missing"` // 勾过但已不在盘上的（如实点名，不静默）
	PicksNote        string   `json:"picks_note,omitempty"`
	Mode             string   `json:"mode"`     // single / slideshow
	File             string   `json:"file"`     // 选中的那张
	LastSet          string   `json:"last_set"` // 最近一次真设上去的那张
	IntervalMin      int      `json:"interval_min"`
	Shuffle          bool     `json:"shuffle"`
	OrderLabel       string   `json:"order_label"`
	Running          bool     `json:"running"` // 平台轮换现在在不在跑
	Current          string   `json:"current"` // 系统现在的桌面壁纸（读不到＝空）
	CurrentInLibrary bool     `json:"current_in_library"`
	CurrentName      string   `json:"current_name,omitempty"`
	Count            int      `json:"count"` // 桌面分类里有多少张
	Note             string   `json:"note,omitempty"`
	NativeNote       string   `json:"native_note,omitempty"` // 系统自己那套幻灯片的提醒（只读来的）
}

// deskStatus 组一份状态（唯一出处；GET / apply / stop 三个响应共用）。
// deskStatus 组一份状态（唯一出处；GET / apply / stop / picks 四个响应共用）。
func (s *Server) deskStatus() wallpaperDesktopStatus {
	st := s.deskState()
	cur := s.deskCurrent()
	all := s.listWallpapers(wallpaperKindDesktop)
	eff, missing := deskPicksFilter(all, st.Picks)
	out := wallpaperDesktopStatus{
		Supported:   s.deskSupported(),
		Mode:        st.Mode,
		File:        st.File,
		LastSet:     st.LastSet,
		IntervalMin: normDeskInterval(st.IntervalMin),
		Shuffle:     st.Shuffle,
		OrderLabel:  deskOrderLabel(st.Shuffle),
		Running:     s.deskRunning(),
		Current:     cur,
		Count:       len(all),
		Picks:       st.Picks,
		PicksCount:  len(eff),
		PicksNote:   deskPicksNote(len(st.Picks), len(eff), missing),
	}
	out.PicksMissing = append([]string{}, missing...)
	if out.PicksMissing == nil {
		out.PicksMissing = []string{}
	}
	if out.Picks == nil {
		out.Picks = []string{}
	}
	if cur != "" && s.wallpaperKindOfPath(cur) == wallpaperKindDesktop {
		out.CurrentInLibrary = true
		out.CurrentName = filepath.Base(cur)
	}
	out.Note = deskNote(out)
	out.NativeNote = nativeSlideshowNote()
	return out
}

// deskNote 给用户看的一句话：现在会发生什么、边界是什么。
func deskNote(st wallpaperDesktopStatus) string {
	if !st.Supported {
		return fmt.Sprintf("本平台不支持改系统桌面壁纸（只有 Windows 有系统桌面壁纸；这台是 %s）", runtime.GOOS)
	}
	if st.Mode == deskModeSlideshow && st.Running {
		return fmt.Sprintf("平台自己在轮换：每 %d 分钟换一张（%s，%s）—— 只在平台运行期间生效，退出平台就停在最后一张（Windows 自己那套幻灯片配置平台一个字节都没动）",
			st.IntervalMin, st.OrderLabel, deskPicksShort(st))
	}
	if st.Mode == deskModeSlideshow {
		return fmt.Sprintf("上次选的是幻灯片（每 %d 分钟、%s，%s），但平台重启后不会自己接着轮 —— 点「开始轮换」即可继续",
			st.IntervalMin, st.OrderLabel, deskPicksShort(st))
	}
	return "点某张图上的「设为壁纸」立即生效；想让它自己换，就用「幻灯片轮换」（间隔 + 顺序/随机；缩略图上勾选＝只在这些里换）"
}

// ── HTTP ────────────────────────────────────────────────────────────────

// handleWallpaperDesktop GET /api/wallpapers/desktop —— 系统桌面壁纸现状。
func (s *Server) handleWallpaperDesktop(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.deskStatus())
}

// handleWallpaperDesktopApply POST /api/wallpapers/desktop/apply
// {file, mode, interval_min, shuffle} —— 把库里某张（单图）或一串（幻灯片）设成系统桌面壁纸。
//
// 失败纪律：**设置没成功就不许写状态**（`desktop.json` 保持原样）—— 否则界面会显示
// 「已设为壁纸」，而系统上什么都没变。
func (s *Server) handleWallpaperDesktopApply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		File        string `json:"file"`
		Mode        string `json:"mode"`
		IntervalMin int    `json:"interval_min"`
		Shuffle     bool   `json:"shuffle"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败：%w", err))
		return
	}
	if !s.deskSupported() {
		writeErr(w, 501, fmt.Errorf("本平台不支持改系统桌面壁纸（只有 Windows 有；这台是 %s）", runtime.GOOS))
		return
	}
	p := strings.TrimSpace(in.File)
	// 先认"这张属于哪一类"（输出目录可能被用户改过 ⇒ 不能拿旧路径拼）：
	// 不在任何一类目录里 = 越界；在手机那一类里 = 竖版设横版桌面会被裁（400 说清）。
	kind := s.wallpaperKindOfPath(p)
	if kind == "" {
		writeErr(w, 403, fmt.Errorf("路径不在壁纸的输出目录里（若你改过输出目录，老目录里的图就不再属于这一类）"))
		return
	}
	if kind != wallpaperKindDesktop {
		writeErr(w, 400, fmt.Errorf("只能把「桌面壁纸」设成系统桌面壁纸：手机壁纸是竖版(9:16)，设到横版桌面上会被裁掉两侧。"+
			"想在桌面上用，就在桌面分类里出一张（「生成壁纸」可以勾上同时生成手机 + 桌面）"))
		return
	}
	if _, err := os.Stat(p); err != nil {
		writeErr(w, 404, fmt.Errorf("这张壁纸不在盘上:%w", err))
		return
	}
	if err := s.deskSet(p); err != nil {
		s.opLog("⚠ 设系统桌面壁纸失败(%s):%v", filepath.Base(p), err)
		writeErr(w, 500, fmt.Errorf("设置系统桌面壁纸失败:%w", err))
		return
	}
	mode := normDeskMode(in.Mode)
	st := s.deskUpdate(func(x *desktopState) {
		x.Mode, x.File, x.LastSet = mode, p, p
		if mode == deskModeSlideshow {
			if in.IntervalMin > 0 {
				x.IntervalMin = normDeskInterval(in.IntervalMin)
			}
			x.Shuffle = in.Shuffle
		}
		x.Applied = time.Now().Format(time.RFC3339)
	})
	if st.Mode == deskModeSlideshow {
		s.deskStartRotation()
	} else {
		s.deskStopRotation()
	}
	s.opLog("🖼 已把 %s 设成系统桌面壁纸（%s）", filepath.Base(p),
		map[bool]string{true: "幻灯片，" + deskOrderLabel(st.Shuffle), false: "就这一张"}[st.Mode == deskModeSlideshow])
	writeJSON(w, 200, s.deskStatus())
}

// handleWallpaperDesktopStop POST /api/wallpapers/desktop/stop —— 停轮换（系统停在最后一张）。
func (s *Server) handleWallpaperDesktopStop(w http.ResponseWriter, r *http.Request) {
	s.deskStopRotation()
	st := s.deskUpdate(func(x *desktopState) { x.Mode = deskModeSingle })
	s.opLog("🖼 桌面壁纸轮换已停（系统里停在最后一张%s）",
		map[bool]string{true: "：" + filepath.Base(st.LastSet), false: ""}[st.LastSet != ""])
	writeJSON(w, 200, s.deskStatus())
}

// ── 轮换范围：只在我勾的那几张里换（2026-10-11 用户：「随机桌面壁纸用户不能选择喜欢的多个壁纸？」）──

// deskPicksShort 轮换范围的一句话（放进 deskNote 的括号里）。
func deskPicksShort(st wallpaperDesktopStatus) string {
	if len(st.Picks) == 0 {
		return fmt.Sprintf("全部 %d 张", st.Count)
	}
	return fmt.Sprintf("只在你勾的 %d 张里换", st.PicksCount)
}

// deskPicksNote 轮换范围的长说明（含"勾过但不在盘上"的点名）。
func deskPicksNote(picked, eff int, missing []string) string {
	base := "轮换范围：桌面分类**全部**壁纸（只想在自己喜欢的那几张里换 ⇒ 在缩略图上勾选）"
	if picked > 0 {
		base = fmt.Sprintf("轮换范围：**你勾选的 %d 张**（其中 %d 张现在还在盘上）", picked, eff)
	}
	if len(missing) > 0 {
		base += fmt.Sprintf("；这些勾过的已不在盘上，轮换会跳过：%s", strings.Join(missing, "、"))
	}
	return base
}

// handleWallpaperDesktopPicks POST /api/wallpapers/desktop/picks {files:[...]}
//
// 「顺序/随机换图只在我挑的那几张里换」；**空数组 = 清空选择**（回到"全部参与"）。
//
// 纪律三条：① 只收**桌面分类里且在盘上**的图（越界 / 手机分类 / 不在盘上 ⇒ 400 点名，状态一个字节不动）；
// ② 选择变了而轮换正在跑 ⇒ **立刻按新集合重排**（否则用户要等到下一轮才看见效果）；
// ③ 轮换时序列里出现已删掉的图 ⇒ 由 deskRotateOnce 如实播报并跳过（这里不预筛盘上的变化）。
func (s *Server) handleWallpaperDesktopPicks(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Files []string `json:"files"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败：%w", err))
		return
	}
	var picks []string
	for _, p := range in.Files {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		switch k := s.wallpaperKindOfPath(p); {
		case k == "":
			writeErr(w, 403, fmt.Errorf("「%s」不在壁纸的输出目录里（若你改过输出目录，老目录里的图就不再属于这一类）", filepath.Base(p)))
			return
		case k != wallpaperKindDesktop:
			writeErr(w, 400, fmt.Errorf("「%s」是手机壁纸（竖版），不能进桌面轮换 —— 轮换只换桌面壁纸", filepath.Base(p)))
			return
		}
		if _, err := os.Stat(p); err != nil {
			writeErr(w, 404, fmt.Errorf("「%s」不在盘上：%w", filepath.Base(p), err))
			return
		}
		dup := false
		for _, q := range picks {
			if sameFile(q, p) {
				dup = true
				break
			}
		}
		if !dup {
			picks = append(picks, p)
		}
	}
	st := s.deskUpdate(func(x *desktopState) { x.Picks = picks })
	if st.Mode == deskModeSlideshow && s.deskRunning() {
		s.deskStartRotation() // 立刻按新集合重排（stop 在锁外做完再重建）
	}
	if len(picks) == 0 {
		s.opLog("🖼 桌面壁纸轮换范围改回**全部**桌面壁纸（清空了选择）")
	} else {
		s.opLog("🖼 桌面壁纸轮换只在勾选的 %d 张里换", len(picks))
	}
	writeJSON(w, 200, s.deskStatus())
}
