package asr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 用**假工具**钉住 Transcribe 的调用契约（真机端到端里就是这么炸的）。
//
// 【事故现场】Transcribe 会把子进程的工作目录切成素材所在目录（asr.cmd 的 --out 与结果文件
// 都按 cwd 解析），此时再把**相对路径**当素材传过去，子进程解析出来的就是"相对 cwd 的相对路径"：
//
//	测试传 `clips/shot_003.mp4` → cmd.Dir = `clips` → 工具报 media not found（真机复现过一次）
//
// 修法是**先 Abs 再交给工具**。这条用例不依赖真机 ASR：假工具照 asr.cmd 的用法与失败行为写
// （参数顺序 media --model X --out Y；素材不在就退 2），所以它能在任何机器上跑，而且真的会红。
func TestTranscribePassesAnAbsoluteMediaPath(t *testing.T) {
	if _, err := os.Stat("testdata"); err != nil {
		t.Skip("夹具目录不在，跳过")
	}
	tmp := t.TempDir()
	t.Chdir(tmp) // 用相对路径调用：这正是出问题的那种用法

	clips := filepath.Join(tmp, "clips")
	if err := os.MkdirAll(clips, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clips, "shot_003.mp4"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := writeFakeTool(t, tmp)

	// ① 相对路径必须能跑通（假工具会检查 %1 是否真的存在）。
	r, err := Transcribe(context.Background(), tool, filepath.Join("clips", "shot_003.mp4"), "")
	if err != nil {
		t.Fatalf("相对路径调用失败（Transcribe 没有把素材转成绝对路径?）: %v", err)
	}
	if len(r.Segments) != 1 || !strings.Contains(r.Segments[0].Text, "shoudao") {
		t.Fatalf("解析结果不对: %+v", r)
	}
	if r.Model != "medium" {
		t.Errorf("没指定模型时应回落 medium，实际 %q", r.Model)
	}
	// ② 结果文件落在**素材同级**（合成后也照这个位置找得到）。
	if _, err := os.Stat(filepath.Join(clips, "shot_003.asr.txt")); err != nil {
		t.Errorf("结果文件没落在素材同级: %v", err)
	}
	// ③ 素材不存在 ⇒ 如实报错（不许静默当成"没人说话"）。
	if _, err := Transcribe(context.Background(), tool, filepath.Join("clips", "shot_999.mp4"), "medium"); err == nil {
		t.Error("素材不存在时必须报错")
	}
	// ④ 没给入口 ⇒ 如实报错（调用方据此播报"跳过"）。
	if _, err := Transcribe(context.Background(), "", filepath.Join("clips", "shot_003.mp4"), ""); err == nil {
		t.Error("没有 ASR 入口时必须报错")
	}
}

// writeFakeTool 写一个照 asr.cmd 契约行事的假工具（ASCII，避免 cmd.exe 的编码坑）：
// 素材不存在退 2，否则把一份固定格式的结果写进 --out 指定的文件。
func writeFakeTool(t *testing.T, dir string) string {
	t.Helper()
	// 【2026-10-09 必须改：夹具自己不能随控制台变化】原来那三行是 `echo …> %5`，而末尾那个字符
	// 正好是数字（`mp4` 的 `4`、`4>>` 的 `4`）—— cmd 会把 `4>` 当成**句柄 4 重定向**：同一份夹具
	// 在**有控制台**时落进文件、在**无控制台**（CREATE_NO_WINDOW，平台走的就是这条）时落空。
	// 于是它测的成了"测试环境有没有控制台"，而不是产品行为（改 sysexec 隐藏窗口时就当场咬了）。
	// 改成括号重定向块 `> "文件" ( … )`，并且**真的把 %3（--model 的取值）写进结果** ——
	// 这样 `r.Model == "medium"` 那条断言才算验了"参数确实传到了子进程"（原来写死 medium，等于没验）。
	// 同时只用 ASCII：cmd.exe 按 OEM 代码页读 .cmd，UTF-8 中文会被拆错命令。
	script := strings.Join([]string{
		"@echo off",
		"if not exist \"%~1\" (",
		"  echo ASR-FAIL media not found: %~1",
		"  exit /b 2",
		")",
		"> \"%~5\" (",
		"  echo # shot_003.mp4",
		"  echo # model=%3 lang=zh prob=1.00 dur=8.0s segs=1",
		"  echo [  0.11-  2.55] shoudao.",
		")",
		"exit /b 0",
	}, "\r\n")
	path := filepath.Join(dir, "asr_fake.cmd")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
