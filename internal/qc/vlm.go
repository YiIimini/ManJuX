// VLM 智能审片:抽帧 → 视觉大模型判分(画面质量/主体/构图/崩坏)。
// 可选能力(项目 QC 参数 EnableVLM);失败不影响机械质检结论。
package qc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/sysexec"
)

// VLMReport 视觉审片结论。
type VLMReport struct {
	Pass   bool     `json:"pass"`
	Score  int      `json:"score"` // 0-100
	Issues []string `json:"issues"`
	Advice string   `json:"advice"` // 重渲改进建议(中文,可操作)

	// FramesSeen 模型**实际看到**的帧数（2026-09-28 审计 A14）。
	//
	// 它与"抽取目标"（2 帧：1 秒与 3 秒）可能不同：抽帧失败时旧实现只是 `continue`，
	// 于是**只看到一帧照样给分**，而报告里没有任何地方写着这件事 —— 用户拿分数决定
	// 这一镜要不要重渲，却不知道结论只基于一秒处的画面。
	FramesSeen int `json:"frames_seen"`
}

const vlmSystem = `You are a strict AI short-video shot reviewer. Given frames from one generated shot, judge:
1) picture quality (clarity, artifacts, distortion), 2) subject integrity (limbs/face anomalies),
3) composition (subject visible, not cropped), 4) obvious corruption (black/blank frames, heavy noise).
Return STRICT JSON only: {"pass": true/false, "score": 0-100, "issues": ["..."], "advice": "中文一句话改进建议(如何修改提示词重渲), 通过则留空"}
Threshold: score>=60 且无严重崩坏才 pass。Issues 用中文。`

// VLMJudge 抽 2 帧送 VLM 判分。shotBrief 为镜头概要(景别/动作/角色)辅助判断。
func VLMJudge(ctx context.Context, client *llm.Client, ffmpeg, videoPath string, shotBrief string) (*VLMReport, error) {
	tmp, err := os.MkdirTemp("", "manjux-vlm-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	var imgs []string
	frameErrs := 0
	for i, pos := range []string{"1", "3"} {
		out := filepath.Join(tmp, fmt.Sprintf("f%d.png", i))
		cmd := sysexec.Hide(exec.CommandContext(ctx, ffmpeg, "-y", "-ss", pos, "-i", videoPath,
			"-frames:v", "1", "-vf", "scale=540:-2", out))
		if b, err := cmd.CombinedOutput(); err != nil {
			// 【2026-09-28 审计 A14】原来这里 `continue` 得干干净净 —— 抽帧失败**不留痕迹**，
			// 而下面的报告只写"分数"：**两帧只抽出一帧照样给分**，用户按那个分数判断这一镜
			// 过没过，却不知道模型只看到了一帧（1 秒处）就下了结论。
			_ = b
			frameErrs++
			continue
		}
		if raw, err := os.ReadFile(out); err == nil && len(raw) > 0 {
			imgs = append(imgs, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(raw))
		} else {
			frameErrs++
		}
	}
	if len(imgs) == 0 {
		return nil, fmt.Errorf("抽帧失败")
	}

	out, err := client.ChatVision(ctx, vlmSystem, []llm.VisionContent{
		{Role: "user", Text: "镜头概要:" + shotBrief + "\n请判分。", Images: imgs},
	}, true)
	if err != nil {
		return nil, err
	}
	// 剥围栏走**共用实现**（2026-09-29 代码健康审计 P1-10），理由见 identity.go 同处。
	s := stripJSONFence(out)
	var r VLMReport
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return nil, fmt.Errorf("VLM 输出解析失败: %v", err)
	}
	if r.Issues == nil {
		r.Issues = []string{}
	}
	// **实际看了几帧要写进报告**（审计 A14）：少于目标帧数时如实告知，
	// 并把它变成一条 issue —— 用户是按这份报告决定重渲的，不能让他以为模型看了全片。
	r.FramesSeen = len(imgs)
	if frameErrs > 0 || len(imgs) < 2 {
		r.Issues = append(r.Issues, fmt.Sprintf(
			"质检只看到 %d 帧（目标 2 帧，%d 帧抽帧失败）——结论基于不完整的画面，建议先确认片段文件可正常解码",
			len(imgs), frameErrs))
	}
	return &r, nil
}
