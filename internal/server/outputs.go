package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// outputsList 作品库:聚合全部项目的成片(含 2K 版本/素材包状态)。
func (s *Server) outputsList(w http.ResponseWriter, r *http.Request) {
	type OutputItem struct {
		Project   string `json:"project"`
		Title     string `json:"title"`
		Final     string `json:"final"`      // 相对路径 output/final.mp4(可能空)
		Final2K   string `json:"final_2k"`   // 相对路径(可能空)
		SRT       bool   `json:"srt"`
		Pack      bool   `json:"pack"`       // 剪辑素材包是否已生成
		SizeMB    float64 `json:"size_mb"`
		Size2KMB  float64 `json:"size_2k_mb"`
		Shots     int    `json:"shots"`
		ShotsDone int    `json:"shots_done"`
		Duration  float64 `json:"duration"`
		UpdatedAt string `json:"updated_at"`
	}
	out := []OutputItem{}
	for _, p := range s.store.List() {
		item := OutputItem{Project: p.ID, Title: p.Name, UpdatedAt: p.UpdatedAt.Format("2006-01-02 15:04")}
		if plan, err := s.store.LoadPlan(p.ID); err == nil {
			item.Shots = len(plan.Shots)
			item.Title = plan.Title
		}
		if m, err := s.store.LoadManifest(p.ID); err == nil {
			for _, c := range m {
				if c.Status == "done" {
					item.ShotsDone++
				}
			}
		}
		outDir := filepath.Join(s.store.Dir(p.ID), "output")
		if st, err := os.Stat(filepath.Join(outDir, "final.mp4")); err == nil && st.Size() > 0 {
			item.Final = "output/final.mp4"
			item.SizeMB = float64(st.Size()) / 1e6
			if d, err := s.ff.Probe(filepath.Join(outDir, "final.mp4")); err == nil {
				item.Duration = d.Duration
			}
		}
		if st, err := os.Stat(filepath.Join(outDir, "final_2k.mp4")); err == nil && st.Size() > 0 {
			item.Final2K = "output/final_2k.mp4"
			item.Size2KMB = float64(st.Size()) / 1e6
		}
		if _, err := os.Stat(filepath.Join(outDir, "final.srt")); err == nil {
			item.SRT = true
		}
		if _, err := os.Stat(filepath.Join(outDir, "剪辑素材包.zip")); err == nil {
			item.Pack = true
		}
		if item.Final != "" || item.Final2K != "" || item.Shots > 0 {
			out = append(out, item)
		}
	}
	writeJSON(w, 200, out)
}

// projectOutputDelete 删除项目成片产物(final/2k/srt/pack)。
func (s *Server) projectOutputDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.store.Get(id); !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	var in struct {
		What string `json:"what"` // final / final2k / all
	}
	_ = readBody(r, &in)
	outDir := filepath.Join(s.store.Dir(id), "output")
	targets := map[string][]string{
		"final":   {"final.mp4", "final.srt", "final.ass"},
		"final2k": {"final_2k.mp4"},
		"all":     {"final.mp4", "final_2k.mp4", "final.srt", "final.ass", "剪辑素材包.zip"},
	}[in.What]
	if targets == nil {
		writeErr(w, 400, fmt.Errorf("what 须为 final/final2k/all"))
		return
	}
	for _, f := range targets {
		_ = os.Remove(filepath.Join(outDir, f))
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

var _ = json.Marshal
