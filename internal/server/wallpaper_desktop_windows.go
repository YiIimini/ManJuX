//go:build windows

// wallpaper_desktop_windows.go Windows 侧的两个系统调用：
//
//	① 单图：`SystemParametersInfoW(SPI_SETDESKWALLPAPER / SPI_GETDESKWALLPAPER)`
//	   —— Windows 自己的接口，改的是「当前桌面壁纸」，**不动**幻灯片那套配置。
//	② 只读注册表：把 Windows 自己那套幻灯片配置（`Interval` / `Shuffle`）读出来，
//	   好在界面上先提醒用户「系统自己可能按它的节奏换图」。**只读，一个字节都不写**。
//
// 为什么不用 Windows 的 COM 接口 `IDesktopWallpaper`（它确实有 `SetSlideshow`）：
// 那要自己搭 COM 虚表调用（本仓库不引新依赖），而**平台侧的轮换**已经能满足
// 「幻灯片」这件事，且行为完全可测（见 wallpaper_desktop.go 的 deskRotateOnce）。
// 真要走系统级幻灯片，得先有把握写对 `SlideshowDirectoryPath1` 那个二进制 —— 见文件头。
package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	spiGetDeskWallpaper = 0x0073 // SPI_GETDESKWALLPAPER
	spiSetDeskWallpaper = 0x0014 // SPI_SETDESKWALLPAPER
	spifUpdateIniFile   = 0x0001 // SPIF_UPDATEINIFILE
	spifSendChange      = 0x0002 // SPIF_SENDCHANGE

	// deskWallpaperBuf SPI_GETDESKWALLPAPER 要求 32767 个 UTF-16 字符的缓冲（MSDN 给的数）
	deskWallpaperBuf = 32767
)

var (
	deskUser32         = syscall.NewLazyDLL("user32.dll")
	procSysParamsInfoW = deskUser32.NewProc("SystemParametersInfoW")
)

// desktopScreenSize 主屏分辨率（GetSystemMetrics：SM_CXSCREEN=0 / SM_CYSCREEN=1）。
//
// 用途：「按我的屏幕出一张」——桌面壁纸最该贴的就是屏幕比例。这里是**物理像素**（不含 DPI 缩放），
// 与"壁纸要多大"这件事对得上。读不到（远程/无头会话）⇒ 0,0，调用方如实说"读不到屏幕"。
func desktopScreenSize() (int, int) {
	procGetSystemMetrics := deskUser32.NewProc("GetSystemMetrics")
	cx, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
	cy, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN
	return int(cx), int(cy)
}

// desktopWallpaperSupported 本平台能不能改系统桌面壁纸。
func desktopWallpaperSupported() bool { return true }

// setDesktopWallpaperOS 把某个图片文件设成当前桌面壁纸。
//
// 只做三件事：校验文件在盘上 → 转 UTF-16 → 调接口。**不碰注册表、不碰幻灯片配置**。
func setDesktopWallpaperOS(path string) error {
	p := strings.TrimSpace(path)
	if p == "" {
		return fmt.Errorf("没给图片路径")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("路径解析失败:%w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("要设成壁纸的图不在盘上:%w", err)
	}
	w, err := syscall.UTF16PtrFromString(abs)
	if err != nil {
		return fmt.Errorf("路径转 UTF-16 失败:%w", err)
	}
	r, _, callErr := procSysParamsInfoW.Call(spiSetDeskWallpaper, 0,
		uintptr(unsafe.Pointer(w)), spifUpdateIniFile|spifSendChange)
	if r == 0 {
		if callErr == nil || callErr == syscall.Errno(0) {
			callErr = syscall.EINVAL
		}
		return fmt.Errorf("SystemParametersInfoW(SPI_SETDESKWALLPAPER) 失败:%w", callErr)
	}
	return nil
}

// currentDesktopWallpaperOS 读当前桌面壁纸（只读；读不到就报错，调用方如实说不知道）。
func currentDesktopWallpaperOS() (string, error) {
	buf := make([]uint16, deskWallpaperBuf)
	r, _, callErr := procSysParamsInfoW.Call(spiGetDeskWallpaper, deskWallpaperBuf,
		uintptr(unsafe.Pointer(&buf[0])), 0)
	if r == 0 {
		if callErr == nil || callErr == syscall.Errno(0) {
			callErr = syscall.EINVAL
		}
		return "", fmt.Errorf("SystemParametersInfoW(SPI_GETDESKWALLPAPER) 失败:%w", callErr)
	}
	return syscall.UTF16ToString(buf), nil
}

// regDWORD 读一个 HKCU 下的 DWORD（只读）。
func regDWORD(subKey, name string) (uint32, error) {
	sk, err := syscall.UTF16PtrFromString(subKey)
	if err != nil {
		return 0, err
	}
	var h syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, sk, 0, syscall.KEY_READ, &h); err != nil {
		return 0, err
	}
	defer syscall.RegCloseKey(h) //nolint:errcheck // 关句柄失败没有补救动作
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	var typ uint32
	var buf [4]byte
	size := uint32(len(buf))
	if err := syscall.RegQueryValueEx(h, n, nil, &typ, &buf[0], &size); err != nil {
		return 0, err
	}
	return uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24, nil
}

// nativeSlideshowNote 系统自己那套幻灯片还在不在（**只读**）。
//
// 为什么要有它：平台的幻灯片是「平台自己轮换」，而系统那套若还开着，可能按**它自己**的
// 间隔把平台设的图换掉 —— 与其等用户看着桌面自己犯疑，不如平台先说清楚。
// 判据只认一件事：`Control Panel\Personalization\Desktop Slideshow\Interval` 在不在
// （那个键是 Windows 配过幻灯片才会有的）。读不到 ⇒ 返回空串（**不猜**）。
func nativeSlideshowNote() string {
	key := `Control Panel\Personalization\Desktop Slideshow`
	iv, err := regDWORD(key, "Interval")
	if err != nil {
		return ""
	}
	sh, _ := regDWORD(key, "Shuffle")
	order := "顺序"
	if sh == 1 {
		order = "随机"
	}
	mins := int(iv / 60000)
	if mins <= 0 {
		mins = 30
	}
	return fmt.Sprintf("注意：Windows 自己那套幻灯片还配着（每 %d 分钟、%s）——"+
		"它可能按自己的节奏把平台设的图换掉。平台不碰那套配置；要它别抢，去 设置 → 个性化 → 背景 里关掉幻灯片",
		mins, order)
}
