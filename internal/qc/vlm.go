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
	"strings"

	"github.com/YiIimini/ManJuX/internal/sysexec"
	"github.com/YiIimini/ManJuX/internal/llm"
)

// VLMReport 视觉审片结论。
type VLMReport struct {
	Pass   bool     `json:"pass"`
	Score  int      `json:"score"` // 0-100
	Issues []string `json:"issues"`
	Advice string   `json:"advice"` // 重渲改进建议(中文,可操作)
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
	for i, pos := range []string{"1", "3"} {
		out := filepath.Join(tmp, fmt.Sprintf("f%d.png", i))
		cmd := sysexec.Hide(exec.CommandContext(ctx, ffmpeg, "-y", "-ss", pos, "-i", videoPath,
			"-frames:v", "1", "-vf", "scale=540:-2", out))
		if b, err := cmd.CombinedOutput(); err != nil {
			_ = b
			continue
		}
		if raw, err := os.ReadFile(out); err == nil && len(raw) > 0 {
			imgs = append(imgs, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(raw))
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
	s := strings.TrimSpace(out)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	if a := strings.Index(s, "{"); a >= 0 {
		if b := strings.LastIndex(s, "}"); b > a {
			s = s[a : b+1]
		}
	}
	var r VLMReport
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return nil, fmt.Errorf("VLM 输出解析失败: %v", err)
	}
	if r.Issues == nil {
		r.Issues = []string{}
	}
	return &r, nil
}
