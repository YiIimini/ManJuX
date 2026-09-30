package compose

import (
	"strings"
	"testing"
)

// 集封面（2026-09-30 用户：「集的封面图不需要技能侧吧」→「继续做完啊」）：
// 从成片抓帧 + 压集名，三条判据不依赖真的跑 ffmpeg。
func TestCoverTitleAndFilter(t *testing.T) {
	// ① 集名截断（太长会压满画面）
	if got := CoverTitle("第01集·碑上写我死了"); got != "第01集·碑上写我死了" {
		t.Errorf("短集名不该被改：%q", got)
	}
	long := CoverTitle("第01集·这是一个非常非常长的集名用来测试截断行为")
	if r := []rune(long); len(r) > coverTitleMaxRunes {
		t.Errorf("超长集名没截断：%d 字 %q", len(r), long)
	}
	if !strings.HasSuffix(long, "…") {
		t.Errorf("截断后应当带省略号：%q", long)
	}
	// 换行/多余空白要归一（标题会原样画到封面上）
	if got := CoverTitle("  第01集·碑上写我死了 \n "); got != "第01集·碑上写我死了" {
		t.Errorf("空白没归一：%q", got)
	}

	// ② 滤镜串：缩放 + 裁剪到交付尺寸 + drawtext（标题走 textfile，中文不转义）
	vf := coverFilter(1080, 1920, `C:\tmp\title.txt`)
	for _, want := range []string{"scale=1080:1920", "crop=1080:1920", "drawtext=", "textfile=", "fontfile="} {
		if !strings.Contains(vf, want) {
			t.Errorf("滤镜缺 %q：%s", want, vf)
		}
	}
	// Windows 路径里的冒号必须转义，否则 ffmpeg 滤镜解析直接失败
	if !strings.Contains(vf, `C\:/tmp/title.txt`) {
		t.Errorf("路径没转义：%s", vf)
	}
	if strings.Contains(vf, "text='") {
		t.Error("标题应走 textfile，不该把中文塞进 text=（转义太脆）")
	}

	// ③ 尺寸：没给就用成片画幅；成片也没给才退回 1080×1920
	if w, h := pickCoverSize(1080, 1920, 768, 1344); w != 1080 || h != 1920 {
		t.Errorf("给了交付尺寸就该用它：%d×%d", w, h)
	}
	if w, h := pickCoverSize(0, 0, 768, 1344); w != 768 || h != 1344 {
		t.Errorf("没给尺寸应照成片画幅：%d×%d", w, h)
	}
	if w, h := pickCoverSize(0, 0, 0, 0); w != 1080 || h != 1920 {
		t.Errorf("都没有时退回 1080×1920：%d×%d", w, h)
	}

	// ④ 候选取帧比例：开场钩子 / 中段冲突 / 结尾钩子，递增且都在片内
	if len(CoverCandidateRatios) != 3 {
		t.Fatalf("候选取三帧，实得 %d", len(CoverCandidateRatios))
	}
	for i, r := range CoverCandidateRatios {
		if r <= 0 || r >= 1 {
			t.Errorf("第 %d 个取帧比例越界：%v", i+1, r)
		}
		if i > 0 && r <= CoverCandidateRatios[i-1] {
			t.Errorf("取帧比例必须递增")
		}
	}
}
