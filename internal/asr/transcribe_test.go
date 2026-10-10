package asr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestTranscribeSurvivesGrandchildHoldingPipe 【2026-10-10 真机咬到】ASR 工具是
// `asr.cmd`(cmd.exe) → `python.exe`：**孙进程可能握着 stdout 管道不放**（python 或它再拉起的
// 解码器），而 ctx 到点被杀的只是 cmd.exe ⇒ `CombinedOutput` 会一直等"管道读完"，
// 真机形态是 `internal/pipeline` 整包 `go test` 卡到 600s / 900s（栈停在 io.copyBuffer）。
//
// 判据（照真实形态）：假工具**先写结果文件、再 `start /b` 起一个活 30 秒的孙进程**、然后退出。
// 工具的契约本来就是"写完文件就退"，所以：
//
//	① `Transcribe` 必须**在几秒内**返回（`sysexec.Hide` 的 WaitDelay 兜住孙进程）；
//	② 返回的结果要**真的解析出来**（`ErrWaitDelay` 不算失败：结果从文件读）。
func TestTranscribeSurvivesGrandchildHoldingPipe(t *testing.T) {
	tmp := t.TempDir()
	media := filepath.Join(tmp, "shot_003.mp4")
	if err := os.WriteFile(media, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 假工具：写结果 → 起孙进程（握着 stdout 30 秒）→ 立刻退出。
	script := strings.Join([]string{
		"@echo off",
		"> \"%~5\" (",
		"  echo # model=%3 lang=zh prob=1.00 dur=8.0s segs=1",
		"  echo [  0.11-  2.55] shoudao.",
		")",
		// `/d "%TEMP%"`：孙进程的**工作目录**别落在本用例的临时目录里 —— 它要活 30 秒，
		// 握着那个目录会让 `t.TempDir()` 的清理把用例判失败；stdout 管道照样继承，判据不受影响。
		"start \"\" /b /d \"%TEMP%\" cmd /c \"ping -n 30 127.0.0.1 >nul\"",
		"exit /b 0",
	}, "\r\n")
	tool := filepath.Join(tmp, "asr_holds_pipe.cmd")
	if err := os.WriteFile(tool, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	r, err := Transcribe(context.Background(), tool, media, "medium")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("孙进程握着管道时不该判失败（结果文件已写出）：%v（耗时 %v）", err, elapsed)
	}
	if len(r.Segments) != 1 || !strings.Contains(r.Segments[0].Text, "shoudao") {
		t.Fatalf("结果没解析出来：%+v", r)
	}
	if elapsed > 20*time.Second {
		t.Errorf("等了 %v —— WaitDelay 没生效（孙进程要活 30 秒，正常应 ≤ 10 秒返回）", elapsed)
	}
	t.Logf("孙进程握着管道，%v 返回并解析成功（孙进程本身要活 30 秒）", elapsed)
}

// TestTranscribeKillsTheWholeTreeOnCancel 【2026-10-10 集成测试当场咬出来的一条真缺陷】
//
// 现象：`internal/pipeline` 的 `TestRunRenderDrivesEnginePerShot` 结束时清理临时目录失败 ——
// `clips: The process cannot access the file because it is being used by another process`。
// 根因：ASR 工具链是 `asr.cmd`(cmd.exe) → `python.exe`，而 `exec.CommandContext` 只杀**直接子进程**
// ⇒ 超时/取消之后 python 还活着，占着工作目录与 CPU（生产里的样子就是"点了停止，风扇还在转"）。
//
// 判据（行为级，不靠读源码）：假工具先写结果文件，再起一个**7 秒后写标记文件的孙进程**，
// 然后自己挂住（`ping -n 8`）。我们在 1 秒后取消 ctx ⇒ `Transcribe` 提前返回；
// 之后等 9 秒：**标记文件必须始终不出现**（整棵树被清算）。
// 把 job 那一步拿掉 ⇒ 孙进程活着写到标记 ⇒ 这条门变红（反向验证脚本里就是这么针的）。
func TestTranscribeKillsTheWholeTreeOnCancel(t *testing.T) {
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "grandchild_alive.txt")
	media := filepath.Join(tmp, "shot_009.mp4")
	if err := os.WriteFile(media, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 孙进程：`ping -n 7` ≈ 7 秒后再写标记（工作目录设成 %TEMP%，别让临时目录清理跟它打架）。
	script := strings.Join([]string{
		"@echo off",
		"> \"%~5\" (",
		"  echo # model=%3 lang=zh prob=1.00 dur=8.0s segs=1",
		"  echo [  0.11-  2.55] shoudao.",
		")",
		"start \"\" /b /d \"%TEMP%\" cmd /c \"ping -n 7 127.0.0.1 >nul & echo alive > \"\"" + marker + "\"\"\"",
		"ping -n 8 127.0.0.1 >nul",
		"exit /b 0",
	}, "\r\n")
	tool := filepath.Join(tmp, "asr_tree.cmd")
	if err := os.WriteFile(tool, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_, err := Transcribe(ctx, tool, media, "medium")
	if err == nil {
		t.Fatal("ctx 1 秒就取消、工具要挂 8 秒 ⇒ 必须如实返回失败（不许静默当成验过）")
	}
	// 等够孙进程本该写标记的时间：整棵树收干净了，标记就不会出现。
	deadline := time.Now().Add(9 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(marker); statErr == nil {
			t.Fatalf("取消之后孙进程还活着并写出了标记（%s）—— 只杀了直接子进程（cmd.exe），"+
				"python/孙进程成了孤儿；生产里就是「点了停止，后台还在跑」", marker)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
