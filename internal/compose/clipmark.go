package compose

import (
	"fmt"
	"hash/fnv"
	"io"
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
// 判据：标记里记下**处理时那份文件的指纹**；下次先比对，对得上才算处理过。
//
// 【2026-10-08 深度排查加强：指纹必须有**内容**】原来只记"大小 + 修改时间"，能被
// **恰好相同**的覆盖骗过：FAT/exFAT 的 mtime 分辨率是 2 秒、复制会保留时间戳、
// 同长度重渲的产物大小也可能一样 —— 那种情况下标记仍被判"有效"，黑闪切除/音频净化
// 又被静默跳过，等于原缺陷换了一条触发路径。现在加"首 64KB 的 FNV-1a"：
// 真机片段是 1 秒以上的视频，头 64KB 覆盖 moov 声明与 mdat 开头，同长的两份片几乎不可能同哈希。
func clipMarkerValid(marker, path string) bool {
	raw, err := os.ReadFile(marker)
	if err != nil {
		return false
	}
	fp, err := clipFingerprint(path)
	if err != nil {
		return false
	}
	// 标记体是「指纹 + 一句说明」：只比第一个字段（说明改了不该让标记失效）。
	got := strings.Fields(strings.TrimSpace(string(raw)))
	if len(got) == 0 {
		return false
	}
	return got[0] == fp
}

// writeClipMarker 记下当前文件的指纹（大小 + 修改时间 + 内容哈希）。
func writeClipMarker(marker, path, note string) error {
	fp, err := clipFingerprint(path)
	if err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(fp+" "+note+"\n"), 0o644)
}

// clipFingerprintBytes 内容指纹只取开头这么多字节。
const clipFingerprintBytes = 64 << 10

// clipFingerprint 片段指纹：大小 + 修改时间 + 首 clipFingerprintBytes 字节的 FNV-1a。
// 任一取不到（文件不在/读不了）都返回错误 —— 调用方据此判"标记无效、重做一遍"，不静默放行。
func clipFingerprint(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := fnv.New64a()
	buf := make([]byte, clipFingerprintBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && n == 0 {
		// 空文件 / 打不开：不给指纹（标记一律判无效）。
		return "", err
	}
	_, _ = h.Write(buf[:n])
	return fmt.Sprintf("%d|%d|%016x", info.Size(), info.ModTime().UnixNano(), h.Sum64()), nil
}
