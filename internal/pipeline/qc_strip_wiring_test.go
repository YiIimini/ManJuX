package pipeline

import (
	"strings"
	"testing"
)

// TestQCAccountsForPlatformBlackFlashStrip 接线：质检的时长对账必须知道"平台自己剪掉了多少秒黑闪"。
//
// 【为什么要有（2026-10-11）】黑闪切除的默认**单段上限**抬到 0.6 秒之后，一次就能剪掉 0.58 秒
// （真机镜 11 的 0.5417s、镜 15 的 0.5833s 就是这么被剪的）。质检若只拿实测时长与本镜计划比，
// 就会把**平台自己的剪辑**报成"产物被截断、请重渲该镜" —— 用户白烧一次 GPU（几分钟），
// 而且这是平台自己造的坑（AGENTS：明知平台自己在造坑就必须修掉，不能只播报）。
//
// 判据咬**调用那一行**（不咬"StrippedSec"这几个字）：值必须来自唯一出处
// compose.StrippedBlackSec，且问的必须是**这一镜的产物路径**（full），不是项目目录、也不是空串。
func TestQCAccountsForPlatformBlackFlashStrip(t *testing.T) {
	src := readSrc(t, "stages_qc_assemble.go")
	line := ""
	for _, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "StrippedSec:") {
			line = strings.TrimSpace(l)
			break
		}
	}
	if line == "" {
		t.Fatal("质检没把「平台自己剪掉的黑闪秒数」传进来 ⇒ 会把自己剪的报成\"产物被截断\"")
	}
	if !strings.Contains(line, "compose.StrippedBlackSec(") {
		t.Errorf("剪掉几秒必须走唯一出处 compose.StrippedBlackSec（从片段自己的 .noblack 标记读），实得：%s", line)
	}
	if !strings.Contains(line, "full)") {
		t.Errorf("要问的是**这一镜的产物**剪掉了多少（要传 full 那条路径），实得：%s", line)
	}
}
