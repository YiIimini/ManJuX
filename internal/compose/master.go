package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// master.go 「768p 母版」—— 云端整片 2K 定稿的**唯一合法源**。
//
// ## 为什么必须有这一份（2026-09-30 深度分析，结合官方口径）
//
// 官方模型卡写得很清楚：2K 只能由 **H3-Regenerate-2K**（in-context 重新生成）得到，
// 而且**该模块未开源**、只能调开放平台 API；它要求的输入是"H3 768p 的产物 + 原始上下文"
// （短边 768、含音轨）。平台侧的 `h3api.Validate2KBase` 正是照这条契约写的。
//
// 可平台**默认会把它自己那份成片放大到 1080×1920**（`deliver.go` 的交付档），
// 于是"拿 `output/final.mp4` 去升 2K"**必然**被校验拦下（短边 1080 ≠ 768）——
// 整片 2K 这条路**从来没有成功过**，而界面上那个按钮看起来是好的。
//
// 拼好的母版（`master`）恰好就是官方要的那份源：**与渲染同规格**（768×1344）+ 音轨，
// 且它在交付缩放/调色/字幕之前。原先它只活在临时工作目录里、合成一成功就被删掉 ——
// 这里把它另存成 `output/master_768p.mp4`，成本是一次**流复制**（几秒、几 MB）。
//
// 判据见 `master_test.go`（真 ffmpeg 跑一遍：母版短边 = 渲染短边、有音轨、可被 2K 预校验接受）。

// MasterFileName 交付目录里那份 768p 母版的文件名。
//
// 导出给 `pipeline/upgrade2k.go` 用 —— 两处各写一个字符串字面量就是"改一处漏一处"的老坑。
const MasterFileName = "master_768p.mp4"

// MasterPath 某项目 output 目录下母版的完整路径（不保证存在）。
func MasterPath(outDir string) string { return filepath.Join(outDir, MasterFileName) }

// publishMaster 把拼好的母版落成 output 里一份独立文件，返回 (路径, 播报文案)。
//
// 视频流**直接复制**（母版本身就是同规格 x264，重编码只会白掉一代）；只把音频规整成
// aac 48kHz 立体声、并写成标准 mp4 —— 云端 regeneration 要的是一个规规矩矩的 mp4。
//
// 失败**不阻断合成**（2K 是可选项），但要如实播报：静默少一个文件的话，用户下次点
// 「2K 定稿」只会看到"源视频不存在"，根本联想不到是合成那一步没写出来。
func publishMaster(ctx context.Context, f *ff.FF, masterPath, outDir string) (string, string) {
	out := MasterPath(outDir)
	tmp := out + ".part"
	// `.part` 必须显式给 `-f mp4`（扩展名推不出容器），与终混那里的理由同一条。
	args := []string{"-y", "-i", masterPath,
		"-c:v", "copy",
		"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2",
		"-movflags", "+faststart", "-f", "mp4", tmp}
	if _, err := f.Run(ctx, args...); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Sprintf("⚠ 768p 母版没写出来（%v）—— 云端「整片 2K 定稿」需要一个 H3 768P 规格的源，"+
			"没有它就只剩「按镜升 2K」这一条路（成片默认已被放大到 1080p，regeneration 不收）", err)
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Sprintf("⚠ 768p 母版落盘失败（%v）—— 同上：整片 2K 需要它", err)
	}
	return out, fmt.Sprintf("✔ 已保留 768p 母版 %s（云端 2K 定稿的合法源：与渲染同规格、含音轨；可删，删了整片 2K 就不可用）",
		MasterFileName)
}
