//go:build !windows

// wallpaper_desktop_other.go 非 Windows 平台的桩：如实说「本平台不支持」，不假装做过。
package server

import (
	"fmt"
	"runtime"
)

// desktopWallpaperSupported 非 Windows ⇒ 不支持（系统桌面壁纸是 Windows 的概念）。
func desktopWallpaperSupported() bool { return false }

// setDesktopWallpaperOS 不支持就报错（上层会把这句话原样交给用户）。
func setDesktopWallpaperOS(path string) error {
	return fmt.Errorf("本平台不支持改系统桌面壁纸（只有 Windows 有；这台是 %s）", runtime.GOOS)
}

// currentDesktopWallpaperOS 读不到就报错，不许回一个空路径让上层以为「读到了空的」。
func currentDesktopWallpaperOS() (string, error) {
	return "", fmt.Errorf("本平台读不到系统桌面壁纸（只有 Windows 有；这台是 %s）", runtime.GOOS)
}

// desktopScreenSize 非 Windows 没有这套概念 ⇒ 0,0（调用方据此把「按我的屏幕」置灰并说明原因）。
func desktopScreenSize() (int, int) { return 0, 0 }

// nativeSlideshowNote 非 Windows 没有这套东西可提醒。
func nativeSlideshowNote() string { return "" }
