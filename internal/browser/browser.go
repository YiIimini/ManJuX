// Package browser 用系统默认浏览器打开工作台。
package browser

import (
	"os/exec"
	"strings"
)

// Open 打开 URL(Windows 默认浏览器)。
func Open(url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil
	}
	// rundll32 免弹 cmd 窗口
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
