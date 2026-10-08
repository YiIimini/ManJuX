package compose

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// 配乐在成片里**听得见**、并且**人声下被压低**（官方 qc-checklist STEP 8 的两条语义）。
//
// 【为什么用测量而不是"看代码"】前一轮刚栽过一次：ACE-Step 直出的曲子文件一切正常、
// 听起来等于没有（mean -63 dB）。同样的坑在这里也存在 —— 床底音量 0.22 × 闪避 × 母轨 loudnorm，
// 任何一环取值错一点，配乐就白加了。所以这里直接**量**：
//
//	· 静音段（无台词）里，混音相对纯母轨的增量 ≈ 配乐的可听度 ⇒ 必须够大；
//	· 台词段（0–2 秒）里的增量应**小于**静音段的增量 ⇒ 闪避真的生效。
func TestBGMIsAudibleAndDuckedUnderDialogue(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机没有 ffmpeg，跳过:%v", err)
	}
	dir := t.TempDir()
	voiceOnly := filepath.Join(dir, "voice.wav")
	bgm := filepath.Join(dir, "bgm.wav")
	mixed := filepath.Join(dir, "mixed.wav")

	// 母轨：前 2 秒有台词（440Hz 正弦），后 4 秒静音 —— 两段对比才有意义
	if _, err := f.Run(context.Background(), "-v", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-af", "apad=whole_dur=6", voiceOnly); err != nil {
		t.Skipf("造母轨失败:%v", err)
	}
	// 配乐：整段连续的 220Hz（模拟一条连续的 BGM）
	if _, err := f.Run(context.Background(), "-v", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=220:duration=6", bgm); err != nil {
		t.Skipf("造配乐失败:%v", err)
	}
	// 注意：note 是**成功时的播报**（"已铺单条 BGM 并做人声闪避…"），不是错误 ——
	// 只有"回退原母轨"那种才算没铺上（返回值不等于 mixed 时）
	// 【2026-10-08】第三个返回值是**显式**的"有没有真的用上混音轨"（原来调用方靠文案前缀 🎵 判断，
	// 改一次措辞就会静默丢掉整条 BGM）。
	got, note, mixedOK := mixBGMBed(context.Background(), f, voiceOnly, bgm, 6, mixed)
	if !mixedOK {
		t.Fatalf("混音应当**用上**混音轨（第三个返回值=false 就是在静默丢掉 BGM）；播报:%s", note)
	}
	if got != mixed {
		t.Fatalf("混音应产出新母轨，实际 %q（播报:%s）", got, note)
	}
	if st, serr := os.Stat(mixed); serr != nil || st.Size() == 0 {
		t.Fatalf("混音产物缺失:%v", serr)
	}

	// ① 静音段（2–6 秒）：混音相对纯母轨的增量 = 配乐的可听度
	voiceSilence := meanDB(t, dir, voiceOnly, 2.2, 3.5)
	mixedSilence := meanDB(t, dir, mixed, 2.2, 3.5)
	gainSilence := mixedSilence - voiceSilence

	// ② 台词段（0–2 秒）：同样的增量应更小（闪避）
	voiceTalk := meanDB(t, dir, voiceOnly, 0.3, 1.5)
	mixedTalk := meanDB(t, dir, mixed, 0.3, 1.5)
	gainTalk := mixedTalk - voiceTalk

	t.Logf("静音段增量 %.1f dB（母轨 %.1f → 混音 %.1f）", gainSilence, voiceSilence, mixedSilence)
	t.Logf("台词段增量 %.1f dB（母轨 %.1f → 混音 %.1f）", gainTalk, voiceTalk, mixedTalk)

	// 配乐必须**听得见**：静音段相对纯母轨至少抬 6 dB（否则等于没铺）
	if gainSilence < 6 {
		t.Errorf("静音段里配乐几乎听不见：只抬了 %.1f dB（应 ≥6）—— 床底/闪避参数可能配错", gainSilence)
	}
	// 闪避必须**真的**发生：台词段的配乐增量要明显小于静音段
	if gainTalk >= gainSilence-1.0 {
		t.Errorf("台词下没有闪避：台词段增量 %.1f dB vs 静音段 %.1f dB（应至少低 1 dB）", gainTalk, gainSilence)
	}
}

// meanDB 量某一段的 mean_volume（dB）。volumedetect 的结果打在 stderr 上。
func meanDB(t *testing.T, dir, path string, start, dur float64) float64 {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-hide_banner", "-ss", fmt.Sprintf("%.2f", start),
		"-t", fmt.Sprintf("%.2f", dur), "-i", path, "-af", "volumedetect", "-f", "null", os.DevNull)
	out, _ := cmd.CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, "mean_volume:"); i >= 0 {
			rest := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[i+len("mean_volume:"):]), "dB"))
			v, err := strconv.ParseFloat(rest, 64)
			if err != nil {
				t.Fatalf("解析 mean_volume 失败: %q", line)
			}
			return v
		}
	}
	t.Fatalf("量不到 %s 在 %.2fs 处的响度", filepath.Base(path), start)
	return 0
}
