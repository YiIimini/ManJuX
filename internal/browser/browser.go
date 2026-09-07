// Package browser 负责打开工作台窗口。
// 优先使用 Edge/Chrome 的 App 模式(--app=URL):独立桌面窗口,
// 无地址栏、无标签页、独立任务栏图标 —— 观感即桌面应用;
// 找不到时回退系统默认浏览器。
package browser

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// OpenAppWindow 以桌面应用窗口形态打开 URL(优先 Edge App 模式)。
func OpenAppWindow(url string, width, height int) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil
	}
	if width <= 0 {
		width = 1480
	}
	if height <= 0 {
		height = 920
	}
	size := fmt.Sprintf("%d,%d", width, height)
	for _, exe := range edgeCandidates() {
		if _, err := os.Stat(exe); err != nil {
			continue
		}
		cmd := exec.Command(exe,
			"--app="+url,
			"--window-size="+size,
			"--user-data-dir="+appProfileDir(),
		)
		// SysProcAttr 隐藏可能闪现的控制台(windowsgui 下无影响,保险起见)
		return cmd.Start()
	}
	// 回退:系统默认浏览器
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// Open 用系统默认浏览器打开(回退用)。
func Open(url string) error {
	return OpenAppWindow(url, 0, 0)
}

// edgeCandidates 返回 Edge/Chrome 可执行文件候选路径。
func edgeCandidates() []string {
	var out []string
	for _, p := range []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	} {
		out = append(out, p)
	}
	// LOCALAPPDATA 用户级安装
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		out = append(out,
			filepath.Join(la, `Google\Chrome\Application\chrome.exe`),
		)
	}
	return out
}

// appProfileDir 独立浏览器配置目录:与用户日常浏览器配置隔离,
// 避免借用用户会话/扩展,同时让 App 窗口记住自身大小位置。
func appProfileDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "ManJuX", "appwindow")
}
