package compose

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// bgm.go 成片的**单条 BGM 铺底 + 人声闪避**（官方 qc-checklist STEP 8 的直接落地）。
//
// 官方原文（`skills/3d-animation-short-generator/references/qc-checklist.md` STEP 8）：
//
//	"Then generate one continuous BGM track that matches the story mood and embed it into the
//	 assembled video." / "Duck BGM under dialogue, non-language reactions, and important SFX." /
//	"Do not generate BGM per shot."
//
// 而平台此前恰恰是**逐镜带配乐** —— 每个片段里 H3 自己生成的那段 `non_diegetic_music` 随片段
// 一起进成片，30+ 个片段拼起来就是 30+ 段各弹各的曲子，每个接缝都在换调（听感"不对"的一大来源）。
// 现在口径改为：**片段里不写配乐（全部 N/A）**，由这一步在**终混**位置铺一条连续 BGM。
//
// 实现上刻意独立成一步（不改动既有的母轨/loudnorm 图）：
//
//	母轨（台词＋环境声，已 loudnorm）
//	  + BGM（循环/裁剪到片长，先压到床底音量）
//	  → sidechaincompress：**人声一侧压 BGM 一侧**（说话时自动闪避，不说话时抬起来）
//	  → 输出新的母轨
//
// 这样 BGM 只在"设了参数"时才参与，没设就完全走原路（零风险）。
const (
	// bgmBedVolume BGM 在无人声时的相对音量（母轨已 loudnorm 到目标响度，这里只做"床底"）。
	bgmBedVolume = 0.22
	// bgmDuckRatio 人声压下去的比例（sidechaincompress 的 ratio；14 是"明显让位"又不至于断掉）。
	bgmDuckRatio = 14
)

// BGMNote 没设 BGM 时给用户的一句实话（成片没有配乐）。
const BGMNote = "🎵 成片未铺 BGM（项目参数 audio.bgm 未设置）—— 官方要求成片是**一条连续 BGM**，" +
	"片段里也不该各带各的配乐；请给该集配一条音乐文件后重跑「合片」，或把 audio.bgm 指向它"

// mixBGMBed 把一条 BGM 铺到成片母轨上（人声处自动闪避），返回新母轨路径与一句播报。
//
// 参数：master=已处理的母轨（wav/flac 等）、bgm=音乐文件、dur=成片时长（秒）。
// 失败时**返回原母轨**（配乐是加分项，不该让整集成片失败）。
// 返回 (混音后的音轨, 给人看的说明, 是否真的用上了混音轨)。
//
// 【2026-10-08 深度排查】第三位是**显式**的成功标志：原来调用方靠"说明是不是以 🎵 开头"判断，
// 等于把行为挂在播报文案上 —— 改一次文案就会静默丢掉整条 BGM（master 不换、日志还说铺上了）。
// 失败时**返回原母轨**（配乐是加分项，不该让整集成片失败）。
func mixBGMBed(ctx context.Context, f *ff.FF, master, bgm string, dur float64, out string) (string, string, bool) {
	if f == nil || strings.TrimSpace(bgm) == "" {
		return master, "", false
	}
	if st, err := os.Stat(bgm); err != nil || st.IsDir() {
		return master, fmt.Sprintf("⚠ 设了 audio.bgm 但读不到文件（%s）：这条 BGM 被跳过，成片无配乐", bgm), false
	}
	if dur <= 0 {
		return master, "", false
	}
	// ① BGM 裁/循环到片长 → 压到床底音量；② 用母轨当侧链，人声一出就把 BGM 压下去。
	filter := fmt.Sprintf(
		"[1:a]aformat=sample_rates=48000:channel_layouts=stereo,aloop=loop=-1:size=2147483647,"+
			"atrim=0:%.3f,volume=%.2f,asetpts=N/SR/TB[bed];"+
			"[0:a]aformat=sample_rates=48000:channel_layouts=stereo,asplit=2[voice][sc];"+
			"[bed][sc]sidechaincompress=threshold=0.05:ratio=%d:attack=20:release=400[ducked];"+
			"[voice][ducked]amix=inputs=2:duration=first:dropout_transition=0:normalize=0[out]",
		dur, bgmBedVolume, bgmDuckRatio)
	args := []string{"-y", "-i", master, "-i", bgm,
		"-filter_complex", filter, "-map", "[out]", "-c:a", "pcm_s16le", out}
	if _, err := f.Run(ctx, args...); err != nil {
		return master, fmt.Sprintf("⚠ BGM 铺底失败（%v）：成片按无配乐输出", err), false
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		return master, "⚠ BGM 铺底产物为空：成片按无配乐输出", false
	}
	return out, fmt.Sprintf("🎵 已铺单条 BGM 并做**人声闪避**（床底音量 %.2f，闪避比例 %d）—— "+
		"官方要求成片是一条连续 BGM，而不是每镜各带一段", bgmBedVolume, bgmDuckRatio), true
}
