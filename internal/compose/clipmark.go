package compose

import (
	"fmt"
	"os"
	"strings"
)

// clipmark.go 片段**后处理标记**的判据：标记必须与它标的那份文件对得上。
//
// 【为什么单独抽出来（2026-10-08 真机踩到）】两个后处理（音频净化、黑闪切除）都用
// 「同名标记文件在 = 处理过了」做幂等。但片段是**会被重渲覆盖**的 —— 重渲之后标记还在，
// 后处理就被静默跳过：真机实测 shot_001.mp4 重渲后盘上留着 2 处 0.125s 黑闪，
// 而 `.noblack` 标记还在（说明书写过"处理过了"），于是合出来的成片里黑闪照旧。
//
// 判据：标记里记下**处理时那份文件的大小与修改时间**；下次先比对，对得上才算处理过。
// 文件被覆盖（大小或 mtime 变了）⇒ 标记失效 ⇒ 重新做一遍。两个后处理共用这一条。
func clipMarkerValid(marker, path string) bool {
	raw, err := os.ReadFile(marker)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	// 标记体是「指纹 + 一句说明」：只比第一个字段（说明改了不该让标记失效）。
	got := strings.Fields(strings.TrimSpace(string(raw)))
	if len(got) == 0 {
		return false
	}
	return got[0] == clipFingerprint(info.Size(), info.ModTime().UnixNano())
}

// writeClipMarker 记下当前文件的指纹（大小 + 修改时间）。
func writeClipMarker(marker, path, note string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	body := clipFingerprint(info.Size(), info.ModTime().UnixNano()) + " " + note + "\n"
	return os.WriteFile(marker, []byte(body), 0o644)
}

func clipFingerprint(size int64, mtimeUnixNano int64) string {
	return fmt.Sprintf("%d|%d", size, mtimeUnixNano)
}

// staleMarker 拼一句"标记过期"的诊断话（打日志用，别静默）。
func staleMarker(what, path string) string {
	return fmt.Sprintf("%s 的标记与片段对不上（片段被重渲/覆盖过），本次重做", what)
}
