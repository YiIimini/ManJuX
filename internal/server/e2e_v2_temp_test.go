package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 临时端到端（一次性，跑完删除）：真实 40 集 v2 系列包经真实端点入库 + 复核语音层。
func TestE2ERealSeriesV2Temp(t *testing.T) {
	src := filepath.Join(os.Getenv("TEMP"), "mjx-v2")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("无真产物夹具：%v", err)
	}
	s := newTestServer(t)
	code, out := doReq(t, s, "POST", "/api/projects/import-package", map[string]any{"path": src})
	if code != 200 {
		t.Fatalf("v2 系列包应 200，实际 %d(%v)", code, out)
	}
	if n, _ := out["imported"].(float64); n != 40 {
		t.Fatalf("应导入 40 集，实际 %v(%v)", out["imported"], out)
	}
	res, _ := out["results"].([]any)
	osLines, voices, kinds := 0, map[string]bool{}, map[string]int{}
	shots := 0
	for _, r0 := range res {
		m, _ := r0.(map[string]any)
		id, _ := m["id"].(string)
		plan, err := s.store.LoadPlan(id)
		if err != nil {
			t.Fatalf("%s 读不回：%v", id, err)
		}
		if f := project.PlanFails(project.ValidatePlan(plan)); len(f) > 0 {
			t.Errorf("入库后复核 %s 有 fail：%v", id, project.PlanIssuesText(f))
		}
		for _, c := range plan.Characters {
			voices[c.Voice] = true
			kinds[c.Kind]++
		}
		for _, sh := range plan.Shots {
			shots++
			for _, l := range sh.Dialogue {
				if l.OS {
					osLines++
				}
				// 画内说话人必须在画面里
				if !l.OS && !project.IsVoiceoverSpeaker(l.Speaker) {
					in := false
					for _, c := range plan.Characters {
						if c.Name == l.Speaker {
							for _, cid := range sh.Characters {
								if cid == c.ID {
									in = true
								}
							}
						}
					}
					if !in {
						t.Fatalf("%s 镜%d：说话人 %s 不在画面里（会被随便找个人动嘴）",
							id, sh.ID, l.Speaker)
					}
				}
			}
			// 画外音镜必须带上嘴唇闭合约束（技能端写或平台后处理补，二者必居其一）
			if project.HasVoiceover(sh) && !strings.Contains(sh.H3Prompt, project.VoiceoverClauseMarker) {
				t.Fatalf("%s 镜%d：画外音镜缺少嘴唇闭合约束", id, sh.ID)
			}
		}
	}
	t.Logf("真产物 v2 入库 OK：40 集 / %d 镜 / 画外音 %d 条 / 声线 %d 种 / 类型 %v",
		shots, osLines, len(voices), kinds)
}
