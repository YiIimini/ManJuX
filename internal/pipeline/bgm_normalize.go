package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// bgm_normalize.go 配乐响度归一 + **前后测量**。
//
// 【为什么要它：实测驱动，别删】
// 2026-09-28 第一次真跑 ACE-Step 出来的 60 秒曲子：文件 2.03 MB、stereo 48 kHz、时长准确 ——
// 一切看着都对，**但实际响度是 mean -63.2 dB / max -53.5 dB**（正常混音在 -30～-10）。
// 也就是说：链路全通、文件能用、听起来等于没有声音。铺进成片会被已经 loudnorm 过的母轨
// 彻底压住 ⇒ 用户花了 13.69 GB 的模型和一次生成，得到"加了配乐但听不出来"。
//
// 这就是"文件存在 ≠ 能用"的典型：只有**真跑 + 测量**才能发现，看代码永远看不出来。
//
// 做法：按与**母轨同一套** loudnorm 口径（I=母轨目标、TP=-1.5、LRA=11，见 compose.loudnormFilter）
// 把配乐拉到同一量级，然后**再测一次**确认真的拉上来了；拉不上来就如实报错，
// 不把一个听不见的文件塞进成片（也不静默放过）。
//
// 为什么归一放在管线而不是引擎里：归一要 ffmpeg，而引擎包不带 ffmpeg（`ff.FF` 在管线手上）。
// 更重要的是：**"这条配乐能不能用"是产线的判断**，引擎只负责把它生成出来。

// bgmTargetLUFS 配乐归一到与母轨同量级的响度。
//
// 取 -16 而不是母轨目标本身：配乐在成片里是**床底**（compose.bgmBedVolume=0.22 ≈ -13 dB，
// 再叠 sidechain 闪避），所以它不需要、也不该和母轨一样响；-16 LUFS 是一个"铺下去听得见、
// 又不抢台词"的位置。这个数是**工程取值**（不是创作口径），改它只影响配乐音量。
const bgmTargetLUFS = -16

// NormalizeBGM 把配乐响度拉到可用量级，返回(归一前 mean dB, 归一后 mean dB)。
//
// 两步：① 用 volumedetect 测当前 mean_volume；② 用 loudnorm 拉齐后再测一次。
// 归一后就地覆盖原文件（库里存的就是归一后的版本 —— 复用时不至于再踩一次）。
func normalizeBGM(ctx context.Context, f *ff.FF, path string) (before, after float64, err error) {
	if f == nil {
		return 0, 0, fmt.Errorf("没有 ffmpeg")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		return 0, 0, fmt.Errorf("配乐文件不存在: %w", statErr)
	}
	before, err = measureMeanVolume(ctx, f, path)
	if err != nil {
		return 0, 0, fmt.Errorf("测量原始响度失败: %w", err)
	}
	// 已经够响 ⇒ 不动它（重编码一次没意义，还会掉一点质量）
	if before > bgmTargetLUFS-6 {
		return before, before, nil
	}
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, ".bgm_norm"+filepath.Ext(path))
	defer os.Remove(tmp)
	// **用测得的差值直接算增益**，而不是交给 loudnorm 自适应。
	//
	// 为什么（实测）：loudnorm 对"极轻素材"经常**拉不动**（实测把 -81 dB 的输入处理成 -82 dB，
	// 等于没动）—— 它是有动态范围优化的响度归一，不是"给多少增益"的放大器。而配乐这里要的
	// 恰恰是确定性的抬升：测到 -63 dB、目标 -16 dB ⇒ 加 47 dB，一加一个准、可验证。
	// （母轨那边用 loudnorm 是对的：它要的是"精确对表 + 动态控制"；这里只要"听得见"。）
	gain := bgmTargetLUFS - before
	if gain > 60 {
		gain = 60 // 极端情况（近似静音）不硬抬：抬起来也只是噪声，下面会如实报"仍不可用"
	}
	filter := fmt.Sprintf("volume=%.1fdB", gain)
	if _, rerr := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
		"-i", path, "-af", filter, "-ar", "48000", tmp); rerr != nil {
		return before, before, fmt.Errorf("增益失败: %w", rerr)
	}
	if st, serr := os.Stat(tmp); serr != nil || st.Size() == 0 {
		return before, before, fmt.Errorf("增益处理无产物")
	}
	if err := os.Rename(tmp, path); err != nil {
		return before, before, fmt.Errorf("替换原文件失败: %w", err)
	}
	after, err = measureMeanVolume(ctx, f, path)
	if err != nil {
		return before, before, fmt.Errorf("测量归一后响度失败: %w", err)
	}
	return before, after, nil
}

// measureMeanVolume 用 volumedetect 取 mean_volume（dB）。
//
// volumedetect 把结果打在 **stderr** 上（不是 stdout），解析时两路都要看 —— 只看 stdout
// 会得到"测不到响度"，而测不到时的默认值如果是 0，就会让"极轻的曲子"看起来正常。
func measureMeanVolume(ctx context.Context, f *ff.FF, path string) (float64, error) {
	// 用 Run：它捕获的正是 **stderr**，而 volumedetect 的结果打在 stderr 上（见 ff.Run 的实现）。
	out, err := f.Run(ctx, "-hide_banner", "-i", path, "-af", "volumedetect", "-f", "null", os.DevNull)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, "mean_volume:")
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(line[i+len("mean_volume:"):])
		rest = strings.TrimSuffix(rest, "dB")
		v, perr := strconv.ParseFloat(strings.TrimSpace(rest), 64)
		if perr != nil {
			return 0, fmt.Errorf("解析 mean_volume 失败: %q", line)
		}
		return v, nil
	}
	return 0, fmt.Errorf("volumedetect 没有输出 mean_volume（音频可能损坏或格式不支持）")
}

// BGMLevelUsable 这条配乐的响度是否**可用**（铺进成片能听出来）。
//
// 判据是工程取值：mean_volume 低于 -30 dB 基本等于听不见（实测直出是 -63 dB）。
func BGMLevelUsable(meanDB float64) bool { return meanDB > -30 }
