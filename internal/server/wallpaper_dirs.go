// wallpaper_dirs.go 壁纸的**输出目录**（2026-10-10 用户：「壁纸需用户选择生成后输出的目录」）。
//
// 口径（与门一一对应）：
//
//	① **每个分类一个目录**：手机壁纸落 `mobile` 的目录、桌面壁纸落 `desktop` 的目录。
//	   默认仍是 `<DataDir>/wallpapers/<kind>`；用户改过就用他的 —— **唯一出处** `wallpaperDirOf`。
//	   （为什么按分类而不是"一个库根 + 两分类子目录"：用户盘上现成就有 `data\壁纸\` 这种**平铺**目录，
//	   按分类给目录可以直接把它指过来用，不用先把文件搬进子目录。）
//	② **配置固定留在数据根下**（`<DataDir>/wallpapers/settings.json`）：换库位置**不会**把配置弄丢。
//	③ **改目录只改"以后往哪写"**：`不会`搬动已有文件 —— 老目录里的图留在原地、新清单是空的，
//	   这一点要在界面上说清楚（否则用户会以为"我的图没了"）。
//	④ **能确定性做到的当场做**：路径规整 → 拒绝驱动器根/系统目录 → 建目录 → **写一个临时文件探可写**
//	   → 才落盘。设不上就报错，**不许**把设置写成一个用不了的目录。
//	⑤ 守卫跟着目录走：只读图/删除/设为壁纸都只认"该文件在**这个分类当前的输出目录**里"（`inWallpaperDir`）。
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/fsutil"
)

// 图像侧的硬口径（与 normWallpaperSize 同一份常量；前端要拿它显示"上限/对齐/预算是多少"）。
//
// 【2026-10-11 两处改口径，都是被真机实测逼出来的】
//   - **对齐 32（原来写 16）**：图像侧走的是 `imagegen.go` 的 `snap32`（**向下取整**到 32 的倍数，
//     那里的注释写着「画幅对齐 32（潜空间要求）」）⇒ 平台按 16 承诺就会说谎：填 1200 出的是 1184、
//     填 3440 出的是 3424。实测（同一 prompt/seed，真机）：3440×1440 → 落盘 **3424×1440**、
//     3840×2160 → **3840×2144**、1440×3120 → **1440×3104**、2560×1440 → 原样。
//     所以这里与图像侧**同口径 32**，标签写什么就出什么。
//   - **上限 2048 → 4096，并加一条像素预算**（用户：「桌面壁纸的带鱼屏分辨率默认好低，增加高分辨率的」）：
//     实测 2560×1440 = 42s、3424×1440 = 60s、3840×2144 = 129s、1440×3104 = 60s ⇒ 4K 能跑；
//     但单边放开后 4096×4096（1678 万像素）是没验过的量级，所以再多一道**像素预算**（超了按比例缩，
//     并把"为什么"写进 wallpaperSizeNote）。
const (
	wallpaperMaxSide = 4096 // 单边上限（超了按比例缩，不是硬夹成方图）
	wallpaperMinSide = 512
	wallpaperAlign   = 32 // 必须对齐 32（与 imagegen.snap32 同口径）
	// wallpaperMaxPixels 像素预算：超过就按比例缩（实测跑过的最大是 3840×2144 ≈ 824 万像素 / 129 秒）。
	wallpaperMaxPixels = 8500000
)

// wallpaperSettings 用户改过的输出目录（空串 ⇒ 走默认）。
type wallpaperSettings struct {
	MobileDir  string `json:"mobile_dir,omitempty"`
	DesktopDir string `json:"desktop_dir,omitempty"`
}

// wallpaperSettingsPath 设置文件路径（**永远在数据根下**，与库位置无关）。
func wallpaperSettingsPath(root string) string { return filepath.Join(root, "settings.json") }

// wallpaperDefaultDir 某分类的默认输出目录（纯函数）。
func wallpaperDefaultDir(root, kind string) string {
	return filepath.Join(root, normWallpaperKind(kind))
}

// readWallpaperSettings 读设置（读不到 ⇒ 空；坏文件 ⇒ 回默认并**播报**）。
func (s *Server) readWallpaperSettings() wallpaperSettings {
	var st wallpaperSettings
	b, err := os.ReadFile(wallpaperSettingsPath(s.wallpaperRoot()))
	if err == nil {
		if uerr := json.Unmarshal(b, &st); uerr != nil {
			s.opLog("⚠ 壁纸输出目录设置读不懂（回默认目录）:%v", uerr)
			st = wallpaperSettings{}
		}
	}
	st.MobileDir = strings.TrimSpace(st.MobileDir)
	st.DesktopDir = strings.TrimSpace(st.DesktopDir)
	return st
}

// wallpaperSettingsGet 取内存里的设置（第一次访问读一次盘）。
func (s *Server) wallpaperSettingsGet() wallpaperSettings {
	s.wpMu.Lock()
	defer s.wpMu.Unlock()
	if !s.wpLoaded {
		s.wpSettings = s.readWallpaperSettings()
		s.wpLoaded = true
	}
	return s.wpSettings
}

// wallpaperSettingsUpdate 改设置 + 落盘，返回改完的设置。
// 失败**不许静默**（审计门 TestNoSwallowedPersistenceErrors）：这次改目录可能没记住。
func (s *Server) wallpaperSettingsUpdate(f func(*wallpaperSettings)) wallpaperSettings {
	s.wpMu.Lock()
	if !s.wpLoaded {
		s.wpSettings = s.readWallpaperSettings()
		s.wpLoaded = true
	}
	f(&s.wpSettings)
	st := s.wpSettings
	s.wpMu.Unlock()

	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		s.opLog("⚠ 壁纸输出目录设置序列化失败:%v", err)
		return st
	}
	root := s.wallpaperRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		s.opLog("⚠ 壁纸配置目录建不出来(%s):%v", root, err)
		return st
	}
	if err := fsutil.WriteFileAtomic(wallpaperSettingsPath(root), b); err != nil {
		s.opLog("⚠ 壁纸输出目录设置写失败:%v —— 这次改目录是生效的，只是重启后记不住", err)
	}
	return st
}

// wallpaperDirOf 某分类**当前**的输出目录（唯一出处：用户改过就用他的，否则默认）。
func (s *Server) wallpaperDirOf(kind string) string {
	kind = normWallpaperKind(kind)
	st := s.wallpaperSettingsGet()
	if kind == wallpaperKindDesktop && st.DesktopDir != "" {
		return st.DesktopDir
	}
	if kind == wallpaperKindMobile && st.MobileDir != "" {
		return st.MobileDir
	}
	return wallpaperDefaultDir(s.wallpaperRoot(), kind)
}

// wallpaperDirInfo 给界面看的目录状态（只 Stat，**不建目录** —— GET 不许有副作用）。
func (s *Server) wallpaperDirInfo() map[string]any {
	out := map[string]any{}
	for _, k := range []string{wallpaperKindMobile, wallpaperKindDesktop} {
		dir := s.wallpaperDirOf(k)
		def := wallpaperDefaultDir(s.wallpaperRoot(), k)
		info := map[string]any{
			"path":    dir,
			"default": def,
			"custom":  !strings.EqualFold(filepath.Clean(dir), filepath.Clean(def)),
		}
		if fi, err := os.Stat(dir); err == nil {
			info["exists"] = fi.IsDir()
		} else {
			info["exists"] = false
		}
		out[k] = info
	}
	return out
}

// pathWithin 纯函数：p 在不在 dir 之内（`..` 跳出不算）。
func pathWithin(dir, p string) bool {
	da, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil {
		return false
	}
	pa, err := filepath.Abs(strings.TrimSpace(p))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(da, pa)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// inWallpaperDir 校验路径在**该分类当前的输出目录**内（防穿越；只读图/删除/设为壁纸共用）。
func (s *Server) inWallpaperDir(p, kind string) bool {
	return pathWithin(s.wallpaperDirOf(kind), p)
}

// wallpaperKindOfPath 这个文件属于哪个分类的目录（都不属于 ⇒ 空串）。
func (s *Server) wallpaperKindOfPath(p string) string {
	for _, k := range []string{wallpaperKindMobile, wallpaperKindDesktop} {
		if s.inWallpaperDir(p, k) {
			return k
		}
	}
	return ""
}

// writeProbeFile 往目录里写一个临时文件再删掉 —— 「能不能写」这件事当场验，
// 别等用户点了「生成壁纸」跑完几分钟才炸在落盘那一步。
func writeProbeFile(dir string) error {
	f, err := os.CreateTemp(dir, ".manjux-write-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if _, err := f.WriteString("ok"); err != nil {
		f.Close()       //nolint:errcheck // 探针文件，失败没有补救动作
		os.Remove(name) //nolint:errcheck // 同上
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(name) //nolint:errcheck // 同上
		return err
	}
	return os.Remove(name)
}

// setWallpaperDir 改某分类的输出目录：规整 → 校验 → 建目录 → 探可写 → 落盘。**空串 = 恢复默认**。
//
// 返回生效后的目录；出错时**设置保持原样**（不许写成一个用不了的目录）。
func (s *Server) setWallpaperDir(kind, dir string) (string, error) {
	kind = normWallpaperKind(kind)
	dir = strings.TrimSpace(dir)
	if dir == "" {
		s.wallpaperSettingsUpdate(func(x *wallpaperSettings) {
			if kind == wallpaperKindDesktop {
				x.DesktopDir = ""
			} else {
				x.MobileDir = ""
			}
		})
		return wallpaperDefaultDir(s.wallpaperRoot(), kind), nil
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("请给一个**绝对路径**（例:D:\\壁纸）—— 相对路径会随工作目录变化,不确定")
	}
	clean := filepath.Clean(dir)
	if filepath.Dir(clean) == clean {
		return "", fmt.Errorf("不能把壁纸输出目录设成**驱动器根目录**（%s）—— 生成的图会散在整盘根上", clean)
	}
	if root := strings.TrimSpace(os.Getenv("SystemRoot")); root != "" && pathWithin(root, clean) {
		return "", fmt.Errorf("不能把壁纸输出目录设进**系统目录**（%s）", clean)
	}
	if fi, err := os.Stat(clean); err == nil && !fi.IsDir() {
		return "", fmt.Errorf("%s 是一个文件,不是目录", clean)
	}
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return "", fmt.Errorf("建目录失败:%w", err)
	}
	if err := writeProbeFile(clean); err != nil {
		return "", fmt.Errorf("这个目录**不可写**:%w", err)
	}
	s.wallpaperSettingsUpdate(func(x *wallpaperSettings) {
		if kind == wallpaperKindDesktop {
			x.DesktopDir = clean
		} else {
			x.MobileDir = clean
		}
	})
	return clean, nil
}

// handleWallpaperDir POST /api/wallpapers/dir {kind,dir} —— 改（或恢复默认）某分类的输出目录。
//
// 400 一律带**为什么**（相对路径/驱动器根/系统目录/是个文件/不可写），别让用户猜。
func (s *Server) handleWallpaperDir(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind string `json:"kind"`
		Dir  string `json:"dir"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败：%w", err))
		return
	}
	kind := normWallpaperKind(in.Kind)
	dir, err := s.setWallpaperDir(kind, in.Dir)
	if err != nil {
		s.opLog("⚠ 壁纸输出目录没改成(%s → %q):%v", wallpaperKindLabel(kind), strings.TrimSpace(in.Dir), err)
		writeErr(w, 400, err)
		return
	}
	wasDefault := strings.EqualFold(filepath.Clean(dir), filepath.Clean(wallpaperDefaultDir(s.wallpaperRoot(), kind)))
	s.opLog("🖼 %s的输出目录 = %s（%s）", wallpaperKindLabel(kind), dir,
		map[bool]string{true: "已恢复默认", false: "已改（此前生成的图不会被搬动）"}[wasDefault])
	writeJSON(w, 200, map[string]any{
		"ok":   true,
		"kind": kind,
		"dir":  dir,
		"dirs": s.wallpaperDirInfo(),
		"note": map[bool]string{
			true:  "已恢复默认目录 —— 此前生成在别处的图**不会被搬动**，那一类现在的清单是空的",
			false: "改好了：**以后生成的图落在这个目录**；此前生成的图留在老目录里（不会自动搬过去）",
		}[wasDefault],
	})
}
