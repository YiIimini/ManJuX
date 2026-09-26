package pipeline

import (
	"os"
	"strings"
	"testing"
)

// 调色档自动降档(技能侧《平台侧整改清单》§2.1)在**合成入口**这一层的闸门。
//
// 档位怎么选本身在 project.EffectiveGrade(那边按真实风格串穷举),这里只钉接线的两件事:
// ① 自动降档必须播报,且**只在**自动降档时播报;② 合成入口只在一处解析,并把**结果**交给 compose。

// TestGradeAutoNoteOnlyWhenAutoDowngraded 播报口径。
//
// 两个方向都要守:
//   - 自动降档时**必须**播报 —— 否则用户只看到"这版调色怎么变淡了",既不知道是平台按风格
//     自动降的,也不知道去哪儿改回来;
//   - 不是自动降档时**一条都不许**播报 —— 用户显式选了档位却收到"默认用 X",会以为自己
//     设的档被改掉了。
func TestGradeAutoNoteOnlyWhenAutoDowngraded(t *testing.T) {
	for _, tier := range []string{"soft", "off", "heavy"} {
		if note := gradeAutoNote(tier, false); note != "" {
			t.Errorf("grade=%q 不是自动降档,不该播报,实际 %q", tier, note)
		}
	}
	note := gradeAutoNote("soft", true)
	if note == "" {
		t.Fatal("自动降档必须播报:静默降档等于用户看不见的改动")
	}
	// 播报要说清三件事:为什么降、降到了哪一档、去哪儿改
	for _, want := range []string{"动画电影", "soft", "可在项目参数里改"} {
		if !strings.Contains(note, want) {
			t.Errorf("自动降档的播报必须说清 %q:%q", want, note)
		}
	}
}

// TestAssembleEntryResolvesGradeOnce 结构性闸门:合成入口必须按 plan.Style 解析**一次**调色档,
// 并把解析结果交给 compose —— 既不绕过(直接传 p.Grade),也不在别处再判一次。
//
// 用源码扫描而不是行为测试的原因与 refmanifest 系列闸门一致:这里要防的是"接线漏了/多了一处",
// 而跑一次 runAssemble 需要 ffmpeg + 已渲好的镜头产物,行为测试覆盖不到这条路径。
// 本项目吃过"同一语义两处实现"的亏(project.Sanitize 与 compose.NormalizeGrade 的同口径双写),
// 所以判据只允许有一个出口。
func TestAssembleEntryResolvesGradeOnce(t *testing.T) {
	b, err := os.ReadFile("stages_qc_assemble.go")
	if err != nil {
		t.Fatalf("读不到 stages_qc_assemble.go: %v", err)
	}
	src := string(b)

	if n := strings.Count(src, "EffectiveGrade("); n != 1 {
		t.Errorf("最终调色档必须在**一处**解析(project.EffectiveGrade),实际 %d 处 —— 两处判定必然漂移", n)
	}
	if strings.Contains(src, "Grade: p.Grade") {
		t.Error("合成入口仍把未经解析的原始档位交给 compose:未表态的 auto 会一路落到 compose 的 " +
			"film 兜底,§2.1 的自动降档等于没做")
	}
	for _, want := range []string{"EffectiveGrade(plan.Style)", "gradeAutoNote(grade, gradeAuto)"} {
		if !strings.Contains(src, want) {
			t.Errorf("合成入口缺少接线 %q", want)
		}
	}
}
