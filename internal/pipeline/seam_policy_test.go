package pipeline

import (
	"fmt"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 接缝准入判据闸门(甲3,漂移放大器)。
//
// 事故形态:旧实现 `Seam: rp.Seam && caps.Seam, PrevShot: shot.ID-1` —— 只要上一镜的
// latent 还在就无条件接,把**上一镜尾部的 22 帧**钉进本镜头部。换场时钉进来的是另一个
// 地方的画面、换人时是别人的脸,而且误差逐链累积。
//
// 守四件事:① 同场同人才接;② 换场/换人一律断;③ 每 N 镜强制重建锚点;④ 断开时**有播报**。

func seamPlan() *project.Plan {
	return &project.Plan{
		Scenes: []project.Scene{
			{ID: "s1", Name: "临水城前街"},
			{ID: "s2", Name: "老宅内室"},
		},
		Shots: []project.Shot{
			{ID: 1, Scene: "s1", Characters: []string{"char_a", "char_b"}},
			{ID: 2, Scene: "s1", Characters: []string{"char_a"}},
			{ID: 3, Scene: "s1", Characters: []string{"char_b"}},
			{ID: 4, Scene: "s2", Characters: []string{"char_a"}},
			{ID: 5, Scene: "s2", Characters: []string{"char_a", "char_b"}},
			{ID: 6, Scene: "s2", Characters: nil},
			{ID: 7, Scene: "s2", Characters: nil},
		},
	}
}

func TestSeamAllowed(t *testing.T) {
	plan := seamPlan()
	at := func(id int) project.Shot {
		s, ok := shotByID(plan, id)
		if !ok {
			t.Fatalf("方案里没有镜 %d", id)
		}
		return s
	}
	cases := []struct {
		name    string
		shot    int
		prev    int
		every   int
		allowed bool
		inNote  []string
	}{
		{"同场同人 → 接(角色有交集即可)", 2, 1, 0, true, nil},
		{"同场有交集(本镜多出一个角色)→ 接", 5, 4, 0, true, nil},
		{"同场换人(角色集合无交集)→ 断", 3, 2, 0, false, []string{"没有共同出场角色"}},
		{"换场 → 断", 4, 3, 0, false, []string{"不同场景", "临水城前街", "老宅内室"}},
		{"每场重置(0)→ 不额外提重置", 2, 1, 0, true, nil},
		{"每场重置(1)→ 与 0 同义", 2, 1, 1, true, nil},
		{"每 3 镜重置:第 4 镜是新一轮首镜 → 断(即使同场同人)", 4, 3, 3, false,
			[]string{"每 3 镜"}},
		{"每 3 镜重置:第 5 镜不是首镜 → 照常接(同场同人)", 5, 4, 3, true, nil},
		{"每 4 镜重置:第 5 镜是新一轮首镜 → 断", 5, 4, 4, false, []string{"每 4 镜"}},
		{"每 2 镜重置:第 5 镜(5-1=4 是 2 的倍数)→ 断", 5, 4, 2, false, []string{"每 2 镜"}},
		{"两个空镜 → 断(没有共同主体可锚)", 7, 6, 0, false, []string{"没有共同出场角色"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shot, prev := at(tc.shot), at(tc.prev)
			if got := seamAllowed(plan, shot, prev, tc.every); got != tc.allowed {
				t.Fatalf("seamAllowed(镜 %d←镜 %d, every=%d) = %v,期望 %v",
					tc.shot, tc.prev, tc.every, got, tc.allowed)
			}
			note := seamBlockNote(plan, shot, prev, tc.every)
			if tc.allowed {
				if note != "" {
					t.Fatalf("允许接缝时不该有断开播报,实际 %q", note)
				}
				return
			}
			// ④ 断开必须**如实播报**:文案要能让人看出是哪一镜、为什么断
			if note == "" {
				t.Fatal("断开时必须播报,不许静默降级")
			}
			if !strings.Contains(note, "不接缝") {
				t.Errorf("播报要说清「不接缝」:%q", note)
			}
			if !strings.Contains(note, itoa2(tc.shot)) {
				t.Errorf("播报要带镜号 %d:%q", tc.shot, note)
			}
			for _, want := range tc.inNote {
				if !strings.Contains(note, want) {
					t.Errorf("播报里应说明原因 %q,实际 %q", want, note)
				}
			}
		})
	}
}

// TestSeamAllowedIsSingleSourceForReasons 判据与原因必须同源:
// 允许 ⇔ 原因列表为空,不允许 ⇔ 原因非空且与 seamAllowed 一致(不做"判一次、再猜一次原因")。
func TestSeamAllowedIsSingleSourceForReasons(t *testing.T) {
	plan := seamPlan()
	for _, id := range []int{2, 3, 4, 5, 6, 7} {
		shot, _ := shotByID(plan, id)
		prev, _ := shotByID(plan, id-1)
		reasons := seamBlockers(plan, shot, prev, 0)
		if (len(reasons) == 0) != seamAllowed(plan, shot, prev, 0) {
			t.Fatalf("镜 %d:判据与原因不一致(allowed=%v, reasons=%v)",
				id, seamAllowed(plan, shot, prev, 0), reasons)
		}
		if (len(reasons) > 0) != (seamBlockNote(plan, shot, prev, 0) != "") {
			t.Fatalf("镜 %d:有原因却没有播报文案", id)
		}
	}
}

// TestSeamResetLabel 播报口径的人话(0/1 = 每场;>1 = 每 N 镜)。
func TestSeamResetLabel(t *testing.T) {
	if got := seamResetLabel(0); !strings.Contains(got, "每场") {
		t.Errorf("0 应表述为每场重置,实际 %q", got)
	}
	if got := seamResetLabel(1); !strings.Contains(got, "每场") {
		t.Errorf("1 应表述为每场重置,实际 %q", got)
	}
	if got := seamResetLabel(4); !strings.Contains(got, "每 4 镜") {
		t.Errorf("4 应表述为每 4 镜重置,实际 %q", got)
	}
}

// TestSeamResetEveryEntersFingerprint 接缝间隔改了 = 某一镜是不是"接着上一镜渲的"变了,
// 产物必须重出;而**默认值 0 时指纹串必须与旧版逐字一致**(否则升级一版就让存量项目全量失效)。
func TestSeamResetEveryEntersFingerprint(t *testing.T) {
	base := project.RenderParams{Width: 768, Height: 1344, Steps: 8, Seam: true}
	withReset := base
	withReset.SeamResetEvery = 3

	fpBase := renderFingerprint("p", nil, base, "ref2va", 7)
	if fpBase != renderFingerprint("p", nil, base, "ref2va", 7) {
		t.Fatal("同参数两次指纹不一致 —— 续跑判据会永远失配")
	}
	// 旧版指纹串(本字段引入前)逐字复刻:默认值下必须完全一致,否则存量项目全部误判失效。
	legacy := project.FingerprintText("p", "ref2va",
		fmt.Sprint(768, "x", 1344, "s", 8, "seed", int64(7), "seam", true, "", "q1"), "")
	if fpBase != legacy {
		t.Errorf("默认接缝间隔下指纹串必须与旧版逐字一致(否则升级即全量重渲):\n 新 %s\n 旧 %s",
			fpBase, legacy)
	}
	if fpBase == renderFingerprint("p", nil, withReset, "ref2va", 7) {
		t.Error("改了接缝间隔而指纹不变:已渲的镜头会被判「产物有效,跳过」,设置等于没生效")
	}
}
