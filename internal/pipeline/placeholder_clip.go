package pipeline

import (
	"context"
	"fmt"
	"github.com/YiIimini/ManJuX/internal/project"
	"os"
	"strings"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// placeholder_clip.go 渲染失败镜的**占位片段**（官方 fallback 阶梯第 4 级的落地）。
//
// 官方出处（`skills/3d-animation-short-generator/references/fallback-policy.md`）：
//
//	"After three failed attempts on the same shot: pause and ask the user with a choice card:
//	 … Skip the shot and add a `placeholder: missing clip` note for downstream review."
//
// 平台的现实约束：用户不一定在机器前，管线要能跑完；但**绝不能把缺失藏起来**——
//
//	· 直接跳过这一镜 ⇒ 成片**悄悄变短**，后面每一镜的画面与字幕/音轨整体前移（对位全乱），
//	  而且看日志才知道少了东西；
//	· 生成黑场占位 ⇒ **时长与顺序不变**，字幕后期的对位仍然成立，缺口在成片里**看得见**、
//	  在清单里**查得到**（官方要的 `placeholder: missing clip` 就是这个意思）。
//
// 所以这里选后者：黑场 + 静音，长度等于该镜计划时长，帧率 24（与全片同口径）。

// writePlaceholderClip 生成一个黑场静音占位片段（长度 = 该镜计划时长）。
func writePlaceholderClip(ctx context.Context, f *ff.FF, out string, durationSec int) error {
	if f == nil {
		return ff.Missing("ffmpeg")
	}
	if durationSec <= 0 {
		durationSec = 4
	}
	if err := os.MkdirAll(dirOf(out), 0o755); err != nil {
		return err
	}
	// 黑视频 + 静音音轨：静音是必须的 —— 成片要按"镜头时长"对齐音轨与字幕，
	// 没有音轨的话 compose 会走"缺音轨"的兜底路径（多一条告警，且对位判据退化）。
	_, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("color=c=black:s=768x1344:r=24:d=%d", durationSec),
		"-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
		"-shortest", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "128k", out)
	if err != nil {
		return err
	}
	if st, serr := os.Stat(out); serr != nil || st.Size() == 0 {
		return fmt.Errorf("占位片段写入失败(无产物)")
	}
	return nil
}

// PlaceholderName 失败镜占位片段的**文件名**（相对片段目录，含扩展名）。
//
// 【为什么单独一个名字、而不是直接顶掉真产物】（2026-10-06 审计）：
//
//	· 真产物的名字是 `<outName>.mp4`（引擎侧 `OutputName+".mp4"`，见 comfyengine.engine.go）。
//	  占位若用同一个名字，重渲失败就会**覆盖掉上一次还躺在盘上的那条片子** —— 而那条
//	  用户可能还要回看/对比（同一镜的"上一版"）。
//	· 独立名字之后，渲染成功时把占位删掉即可（见 renderStage），失败时两条并存，
//	  清单的 File 列指向哪一个 = 这一镜当前的有效版本，一眼可查。
//
// 扩展名必须是 .mp4：占位是 x264 封装的真视频，合成阶段按路径取用，播放器/工具按扩展名认。
func PlaceholderName(outName string) string {
	return outName + "_placeholder.mp4"
}

// PlaceholderNote 给用户看的说明（说清这一镜发生了什么、以及它现在**看得见**）。
func PlaceholderNote(shotID, durationSec int, cause string) string {
	cause = strings.TrimSpace(cause)
	if cause == "" {
		cause = "渲染失败"
	}
	return fmt.Sprintf("镜 %d 已按官方降级阶梯写为**占位片段**（%d 秒黑场＋静音，清单标记 %s）——"+
		"原因：%s。为什么不是直接跳过：跳过会让成片悄悄变短、后面每一镜的画面与字幕整体前移；"+
		"占位保住了时长与顺序，缺口在成片里看得见、在清单里查得到。**重出这一镜后必须重合成**",
		shotID, durationSec, project.PlaceholderMarker, cause)
}

// dirOf 取路径的目录部分（filepath.Dir 的一层薄封装，便于本文件独立单测）。
func dirOf(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i > 0 {
		return p[:i]
	}
	return "."
}
