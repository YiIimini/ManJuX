//go:build !windows

package asr

import "os/exec"

// 非 Windows：没有"关句柄即清算整棵树"的作业对象，退回普通启动（语义与 CommandContext 一致）。
// 本平台的运行目标就是 Windows（windowsgui + 本地 ComfyUI/H3），这里只是为了让 `go vet ./...`
// 在别的平台上也能编过，不假装提供了等价能力。

type procJob struct{}

func (j *procJob) Close() {}

func startInJob(cmd *exec.Cmd) (*procJob, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &procJob{}, nil
}
