package qc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/llm"
)

// landmark.go **空间锚点核查**：成片里那些"连续性地标"到底在不在该在的位置。
//
// 来源（官方两处点名，此前平台只做了输入侧、没做成片侧）：
//   - `skills/3d-animation-short-generator/references/qc-checklist.md` STEP 9 终检清单第 2 条：
//     "Scene continuity (verify against the `Reference Anchors` column — did every landmark land
//     where the table said it would?)"
//   - 同目录 `fallback-policy.md`：漂移的处置是 "re-render with a strengthened prompt that quotes
//     the exact Reference Anchors block"；并明确 "do not silently mix corrected and uncorrected
//     clips into assembly"。
//
// 平台现状：场景卡的 `landmarks`（连续性地标）只在**契约门**里被要求"写了没有"，成片里
// 从来不核对 —— 于是"同一场景两镜里门槛在东、下一镜跑到西边"这类**空间漂移**一路无声进成片。
// 这一条就是补那个洞：抽查该场景的镜头，让 VLM 回答"这几样地标在不在画面里"。
//
// 判据写得保守：只有 **VLM 明确说"该地标不在这帧里"** 才计入缺失；含糊/看不清一律算"没验到"，
// 不当成缺失（宁可漏，也不要制造假告警 —— 假告警会让人不再看真告警）。
type LandmarkVerdict struct {
	// Missing 这一帧里**明确缺失**的地标（原样来自场景卡的 landmarks 短语里的一份）。
	Missing []string
	// Seen 这一帧里**明确在场**的地标。
	Seen []string
	// Note VLM 给的一句话说明（给人看）。
	Note string
	// Raw 原始回包（排障用）。
	Raw string
}

// DetectLandmarks 判断这一帧里场景卡声明的地标哪些不在。
//
// framePath 传一张已抽出的帧（PNG/JPG）；landmarks 是场景卡那一行（如「门槛与东侧立柱」）。
// 没有可判内容（landmarks 为空 / 帧读不到）时返回 (nil, nil)：调用方按"没验"处理。
func DetectLandmarks(ctx context.Context, client VisionClient, framePath, landmarks string) (*LandmarkVerdict, error) {
	landmarks = strings.TrimSpace(landmarks)
	if client == nil || landmarks == "" || strings.TrimSpace(framePath) == "" {
		return nil, nil
	}
	dataURL, err := imageDataURL(framePath)
	if err != nil {
		return nil, err
	}
	out, err := client.ChatVision(ctx, landmarkSystemPrompt(), []llm.VisionContent{{
		Role:   "user",
		Text:   landmarkUserPrompt(landmarks),
		Images: []string{dataURL},
	}}, true)
	if err != nil {
		return nil, err
	}
	return parseLandmarks(out, landmarks)
}

func landmarkSystemPrompt() string {
	return "You are a film continuity supervisor. You look at ONE frame from a shot and report which of the " +
		"declared fixed landmarks of that location are actually visible in it. " +
		"Answer with json only: {\"seen\":[\"...\"],\"missing\":[\"...\"],\"note\":\"...\"}. " +
		"Rules: put a landmark in \"missing\" ONLY when you are sure it is not in this frame at all " +
		"(it is a fixed landmark of the place, so if this frame simply does not show that part of the room, " +
		"say so in the note and put it in neither list). Judge by what is visible, never by what sounds plausible."
}

func landmarkUserPrompt(landmarks string) string {
	return "The location's declared fixed landmarks (verbatim from the scene card): " + landmarks + "\n\n" +
		"Which of them are visible in this frame? Report json only."
}

func parseLandmarks(raw, landmarks string) (*LandmarkVerdict, error) {
	s := stripJSONFence(raw)
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("地标核查回包不是 JSON:%s", firstLine(raw))
	}
	var got struct {
		Seen    []string `json:"seen"`
		Missing []string `json:"missing"`
		Note    string   `json:"note"`
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &got); err != nil {
		return nil, fmt.Errorf("地标核查回包解析失败:%w", err)
	}
	// 只认**在场景卡地标清单里出现过**的词，防 VLM 自造条目（同"说话人只认本方案角色"那条纪律）
	inList := func(word string) bool {
		w := strings.TrimSpace(word)
		if w == "" {
			return false
		}
		return strings.Contains(landmarks, w) || strings.Contains(w, landmarks)
	}
	v := &LandmarkVerdict{Note: strings.TrimSpace(got.Note), Raw: raw}
	for _, m := range got.Missing {
		if inList(m) {
			v.Missing = append(v.Missing, strings.TrimSpace(m))
		}
	}
	for _, k := range got.Seen {
		if inList(k) {
			v.Seen = append(v.Seen, strings.TrimSpace(k))
		}
	}
	// 同一项两边都报 ⇒ 以"没验到"处理（不制造假告警）
	if len(v.Missing) > 0 && len(v.Seen) > 0 {
		var kept []string
		for _, m := range v.Missing {
			dup := false
			for _, k := range v.Seen {
				if m == k {
					dup = true
				}
			}
			if !dup {
				kept = append(kept, m)
			}
		}
		v.Missing = kept
	}
	return v, nil
}

// LandmarkWarnText 缺失地标时的告警（含官方 fallback 的处置建议）。
func LandmarkWarnText(shotID int, sceneName, landmarks string, v *LandmarkVerdict) string {
	if v == nil || len(v.Missing) == 0 {
		return ""
	}
	extra := ""
	if strings.TrimSpace(v.Note) != "" {
		extra = "（VLM 说明：" + v.Note + "）"
	}
	return fmt.Sprintf("镜头 %d 的**空间锚点**对不上场景卡：地标「%s」在画面里找不到「%s」%s —— "+
		"官方终检要求逐镜核对地标是否落在表上写的位置；处置（官方 fallback）：**重出该镜**，"+
		"并在提示词里逐字引用场景卡的地标与光态（平台已把场景卡带进参考图，但地标的屏幕位置必须靠文字钉住）；"+
		"不要把它当成'修好的镜头'混进成片",
		shotID, sceneName, strings.Join(v.Missing, "、"), extra)
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// SampleFrameAt 从视频里抽**一帧**（第 at 秒）到 outDir，返回文件路径。
//
// 与 textdetect.go 的 sampleTextFrames 同一套写法（`-ss` 放在 `-i` 前、缩放、并单独判"没产物"）——
// 抽不出帧时**报错而不是当成通过**（这是本项目反复修的那类"报成功其实没做"）。
// 调用方负责清理 outDir。
func SampleFrameAt(ctx context.Context, f *ff.FF, video string, at float64, outDir, name string) (string, error) {
	if f == nil {
		return "", ff.Missing("ffmpeg")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(outDir, name)
	if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
		"-ss", fmt.Sprintf("%.3f", at), "-i", video,
		"-frames:v", "1", "-vf", "scale=720:-2", "-q:v", "3", out); err != nil {
		return "", fmt.Errorf("抽帧失败(%.2fs):%w", at, err)
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		return "", fmt.Errorf("抽帧无产物(%.2fs)", at)
	}
	return out, nil
}
