package qc

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// face.go 人脸一致性**测量**（insightface buffalo_l 的 512 维嵌入 + 余弦相似度）。
//
// 【为什么要它】平台此前判断"是不是同一个人"只有两条路：VLM 判定（能看懂画风，但每次结论
// 都有随机性）与"撞脸自检"（比的是**资产卡之间**，不是成片镜头）。缺一条**确定性**的
// "同一角色跨镜像不像"的量：把一个角色的定妆照与它在各镜里的画面比一遍，数字摆出来。
//
// 【为什么第一版只测量、不拦人】阈值 0.85 来自开源参考实现的实测记录，但在**本平台
// 国风 3D 写实/动画画风**上还没有跨镜标定数据。实测（2026-09-28，用真实项目素材）只标定了
// 两头：同一张脸 1.000、同一画面里不同的人 0.329 —— 中间那段"同一角色在不同镜头/不同光照下
// 会掉到多少"没有数据。判据不成熟就拦人，只会制造假告警，反而让人不再看告警。
// ⇒ 这一版**只记录、不阻断**：把最低/均值/低于阈值的帧数如实报出来，攒够真实数据再定阈值。
//
// 【三件事必须有，缺一件就记"没验过"而不是"通过"】（本项目反复修的那类"报成功其实没做"）：
// ① 本机 Python；② 装了 buffalo_l；③ 脚本本身。任何一件缺失都返回明确的“未验”状态。
//
// 模型落点由平台的 comfy.root_dir 推导：<root>/ComfyUI/models/insightface（insightface 标准布局
// 是 <model_root>/models/buffalo_l/*.onnx），Python 用 <root>/standalone-env/python.exe。
// 两个都可以用环境变量覆盖（MANJUX_FACE_PYTHON / MANJUX_FACE_MODEL_ROOT）。
//
//go:embed face_similarity.py
var faceScriptFS embed.FS

// FaceReport 一次人脸一致性测量的结果。
type FaceReport struct {
	OK       bool          `json:"ok"`
	Error    string        `json:"error,omitempty"`
	RefFaces int           `json:"ref_faces"`
	Items    []FaceItem    `json:"items"`
	Measured int           `json:"measured"`
	Below    int           `json:"below_threshold"`
	Min      float64       `json:"min"`
	Mean     float64       `json:"mean"`
	Max      float64       `json:"max"`
	Thresh   float64       `json:"threshold"`
	Raw      string        `json:"-"`
	Note     string        `json:"-"` // 平台侧补充的说明（"没验过"的原因等）
	Skipped  bool          `json:"-"`
	Elapsed  time.Duration `json:"-"`
}

// FaceItem 单帧的测量结果。
type FaceItem struct {
	Frame string   `json:"frame"`
	Faces int      `json:"faces"`
	Sim   *float64 `json:"sim"`
	Note  string   `json:"note,omitempty"`
}

// FaceConfig 运行测量所需的环境。
type FaceConfig struct {
	Python    string // python 解释器
	ModelRoot string // insightface 的 root（其下应有 models/buffalo_l/）
}

// FaceConfigFromComfyRoot 从平台的 comfy.root_dir 推导 python 与模型路径（可被环境变量覆盖）。
func FaceConfigFromComfyRoot(comfyRoot string) FaceConfig {
	c := FaceConfig{}
	if py := strings.TrimSpace(os.Getenv("MANJUX_FACE_PYTHON")); py != "" {
		c.Python = py
	} else if strings.TrimSpace(comfyRoot) != "" {
		for _, rel := range []string{`standalone-env\python.exe`, `python_embeded\python.exe`, `venv\Scripts\python.exe`} {
			p := filepath.Join(comfyRoot, rel)
			if _, err := os.Stat(p); err == nil {
				c.Python = p
				break
			}
		}
	}
	if mr := strings.TrimSpace(os.Getenv("MANJUX_FACE_MODEL_ROOT")); mr != "" {
		c.ModelRoot = mr
	} else if strings.TrimSpace(comfyRoot) != "" {
		c.ModelRoot = filepath.Join(comfyRoot, "ComfyUI", "models", "insightface")
	}
	return c
}

// Ready 三件事是否齐（缺哪件说哪件 —— 让"没验过"有一个能排查的原因）。
func (c FaceConfig) Ready() (bool, string) {
	if strings.TrimSpace(c.Python) == "" {
		return false, "没找到 Python 解释器（可从 ComfyUI 环境根推导，或用 MANJUX_FACE_PYTHON 指定）"
	}
	if _, err := os.Stat(c.Python); err != nil {
		return false, "Python 路径不存在: " + c.Python
	}
	sub := filepath.Join(c.ModelRoot, "models", "buffalo_l")
	if _, err := os.Stat(sub); err != nil {
		return false, "没装 buffalo_l（期望在 " + sub + "，可用 MANJUX_FACE_MODEL_ROOT 指定）"
	}
	return true, ""
}

// MeasureFaceConsistency 测量参考图（定妆照）与各帧（成片抽帧）的同一人相似度。
//
// 任何一步失败都返回带 Note 的报告（Skipped=true），**不返回"通过"**。
func MeasureFaceConsistency(ctx context.Context, cfg FaceConfig, ref string, frames []string) *FaceReport {
	if len(frames) == 0 {
		return &FaceReport{OK: false, Skipped: true, Note: "没有可测的帧"}
	}
	if ok, why := cfg.Ready(); !ok {
		return &FaceReport{OK: false, Skipped: true, Note: why}
	}
	script, err := writeFaceScript()
	if err != nil {
		return &FaceReport{OK: false, Skipped: true, Note: "写不出测量脚本: " + err.Error()}
	}
	args := []string{script, "--ref", ref, "--model-root", cfg.ModelRoot, "--frames"}
	args = append(args, frames...)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(cctx, cfg.Python, args...)
	cmd.Env = append(os.Environ(),
		"PYTHONIOENCODING=utf-8", // 不设的话中文路径/中文输出会乱码（实测）
		"INSIGHTFACE_HOME="+cfg.ModelRoot,
	)
	out, err := cmd.Output()
	elapsed := time.Since(start)
	line := lastJSONLine(string(out))
	if line == "" {
		note := "测量脚本没有输出"
		if err != nil {
			note = "测量脚本失败: " + err.Error()
		}
		return &FaceReport{OK: false, Skipped: true, Note: note, Elapsed: elapsed}
	}
	var rep FaceReport
	if jerr := json.Unmarshal([]byte(line), &rep); jerr != nil {
		return &FaceReport{OK: false, Skipped: true, Note: "测量结果解析失败: " + jerr.Error(), Raw: line, Elapsed: elapsed}
	}
	rep.Raw = line
	rep.Elapsed = elapsed
	if !rep.OK {
		rep.Skipped = true
		rep.Note = rep.Error
		if rep.Note == "" {
			rep.Note = "测量未完成"
		}
	}
	return &rep
}

// FaceSummary 给人看的一行结论（含"只测量、不拦人"的声明与阈值提醒）。
func FaceSummary(rep *FaceReport, refName string) string {
	if rep == nil {
		return ""
	}
	if rep.Skipped || !rep.OK {
		// 没验过就要说"没验过"，不能沉默（沉默会被读成通过）
		return fmt.Sprintf("人脸一致性**未验**（%s）—— 不当作通过", rep.Note)
	}
	noFace := 0
	for _, it := range rep.Items {
		if it.Sim == nil {
			noFace++
		}
	}
	msg := fmt.Sprintf("人脸一致性测量：参考「%s」，测到 %d 帧（%d 帧没检出人脸），"+
		"相似度 最低 %.3f / 均值 %.3f / 最高 %.3f，低于 %.2f 的 %d 帧",
		refName, rep.Measured, noFace, rep.Min, rep.Mean, rep.Max, rep.Thresh, rep.Below)
	if rep.Below > 0 {
		msg += " —— 仅供参考：阈值尚未在本画风上跨镜标定，先记录不拦人；若某一帧明显偏低，值得人工看一眼"
	}
	return msg
}

// writeFaceScript 把内嵌的测量脚本落到临时文件（exe 自带，不依赖源码目录）。
func writeFaceScript() (string, error) {
	data, err := faceScriptFS.ReadFile("face_similarity.py")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(os.TempDir(), "manjux-facecheck")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "face_similarity.py")
	// 每次都覆盖：脚本随版本走，不留旧副本
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// lastJSONLine 取最后一行以 { 开头的输出（脚本会打印告警，真正结果在最后一行）。
func lastJSONLine(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		ln := strings.TrimSpace(lines[i])
		if strings.HasPrefix(ln, "{") {
			return ln
		}
	}
	return ""
}
