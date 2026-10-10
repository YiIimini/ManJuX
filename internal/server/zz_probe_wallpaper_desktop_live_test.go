//go:build windows

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestZZProbeWallpaperDesktopLive 真机探针（`MANJUX_LIVE_PROBE=1` 才跑）。
//
// 干什么：读**当前**系统桌面壁纸 → **原样设回去**（验证「设」这条接口在真机上真的通）→ 再读回来对齐。
//
// 【为什么敢动用户的桌面】设的就是"当前那一张"，**观感零变化**。为了可回退，探针先把
// SPI 可能顺手改掉的三个标量（`WallpaperStyle` / `TileWallpaper` / `BackgroundType`）记下来，
// 事后逐个核对：**真被改了就在日志里点名说清**（如实播报，不静默）。
//
// 【为什么不自动写回】注册表写回是另一件事（本轮的纪律是"注册表只读"，见门
// TestDesktopWallpaperWiringIsComplete）—— 探针只**报告**差异，把决定留给操作的人。
func TestZZProbeWallpaperDesktopLive(t *testing.T) {
	if os.Getenv("MANJUX_LIVE_PROBE") == "" {
		t.Skip("真机探针：MANJUX_LIVE_PROBE=1 才跑（会调 Windows 的设壁纸接口，但设的是当前那张）")
	}
	cur, err := currentDesktopWallpaperOS()
	if err != nil {
		t.Fatalf("读当前桌面壁纸失败:%v", err)
	}
	t.Logf("① 当前系统桌面壁纸：%s", cur)
	if strings.TrimSpace(cur) == "" {
		t.Fatalf("读回来是空路径 —— 探针没法自证（不许假装验过）")
	}
	if fi, err := os.Stat(cur); err != nil {
		t.Logf("   （这张图本身不在盘上了：%v —— 说明 SPI 回的是历史值，照样能验「设」这条链）", err)
	} else {
		t.Logf("   文件在盘上：%d 字节", fi.Size())
	}

	type regVal struct{ key, name string }
	watched := []regVal{
		{`Control Panel\Desktop`, "WallpaperStyle"},
		{`Control Panel\Desktop`, "TileWallpaper"},
		{`Software\Microsoft\Windows\CurrentVersion\Explorer\Wallpapers`, "BackgroundType"},
	}
	before := map[string]uint32{}
	for _, w := range watched {
		if v, err := regDWORD(w.key, w.name); err == nil {
			before[w.key+"|"+w.name] = v
		}
	}

	if err := setDesktopWallpaperOS(cur); err != nil {
		t.Fatalf("设系统桌面壁纸失败:%v", err)
	}
	after, err := currentDesktopWallpaperOS()
	if err != nil {
		t.Fatalf("设完之后读回来失败:%v", err)
	}
	t.Logf("② 设完读回：%s", after)
	if !strings.EqualFold(filepath.Clean(after), filepath.Clean(cur)) {
		t.Errorf("设的是同一张，读回来却是 %s（原值 %s）", after, cur)
	}
	for _, w := range watched {
		old, had := before[w.key+"|"+w.name]
		if !had {
			continue
		}
		if v, err := regDWORD(w.key, w.name); err == nil && v != old {
			t.Errorf("SPI 动了注册表：HKCU\\%s\\%s 从 %d 变成 %d —— 这会影响用户自己的背景设置，必须点名", w.key, w.name, old, v)
		}
	}
	if note := nativeSlideshowNote(); note != "" {
		t.Logf("③ 系统自己那套幻灯片：%s", note)
	} else {
		t.Logf("③ 系统自己那套幻灯片：没配（注册表里没有那个键）")
	}
}
