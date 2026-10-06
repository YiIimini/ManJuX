package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/pipeline"
)

// projectCover 项目封面:自定义封面(assets/cover.*) > 首个角色定妆照 > 首个场景图。
func (s *Server) projectCover(id string) string {
	if ents, err := os.ReadDir(filepath.Join(s.store.Dir(id), "assets")); err == nil {
		for _, e := range ents {
			n := strings.ToLower(e.Name())
			if strings.HasPrefix(n, "cover.") {
				switch strings.TrimPrefix(n, "cover.") {
				case "png", "jpg", "jpeg", "webp":
					return "assets/" + e.Name()
				}
			}
		}
	}
	for _, sub := range []string{"characters", "scenes"} {
		dir := filepath.Join(s.store.Dir(id), "assets", sub)
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			n := strings.ToLower(e.Name())
			for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp"} {
				if strings.HasSuffix(n, ext) {
					rel, _ := filepath.Rel(s.store.Dir(id), filepath.Join(dir, e.Name()))
					return filepath.ToSlash(rel)
				}
			}
		}
	}
	return ""
}

// outputsList 作品库:聚合全部项目的成片(含 2K 版本/素材包状态)。
func (s *Server) outputsList(w http.ResponseWriter, r *http.Request) {
	type OutputItem struct {
		Project   string  `json:"project"`
		Title     string  `json:"title"`
		Cover     string  `json:"cover"`    // 项目封面(角色定妆照/场景图相对路径)
		Final     string  `json:"final"`    // 相对路径 output/final.mp4(可能空)
		Final2K   string  `json:"final_2k"` // 相对路径(可能空)
		SRT       bool    `json:"srt"`
		Pack      bool    `json:"pack"` // 剪辑素材包是否已生成
		SizeMB    float64 `json:"size_mb"`
		Size2KMB  float64 `json:"size_2k_mb"`
		Shots     int     `json:"shots"`
		ShotsDone int     `json:"shots_done"`
		Duration  float64 `json:"duration"`
		UpdatedAt string  `json:"updated_at"`
	}
	out := []OutputItem{}
	for _, p := range s.store.List() {
		item := OutputItem{Project: p.ID, Title: p.Name, Cover: s.projectCover(p.ID), UpdatedAt: p.UpdatedAt.Format("2006-01-02 15:04")}
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
	// 【2026-09-30 深度审计】原来是 `_ = readBody(r, &in)`：body 坏了 ⇒ what 空 ⇒
	// 报的是"what 须为 final/final2k/all"，理由对不上（真正原因是 body 读不出来）。
	if err := readBodyOptional(r, &in); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体读不出来(%v)", err))
		return
	}
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
	// 【2026-09-30 深度审计】原来只要 `os.Stat` 成功就 `removed++`，而 `os.Remove` 的返回值
	// 被丢掉 —— Windows 上 final.mp4 正被播放器/编辑器打开时删除会失败（共享冲突），
	// 于是日志与事件流照样报「🗑 已删除成片产物 N 个文件」，作品库那张卡也还在：
	// 用户看到"删了"和"还在"同时成立，只能得出"这软件在骗我"。删不掉要**如实报**。
	removed := 0
	var failed []string
	for _, f := range targets {
		p := filepath.Join(outDir, f)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := os.Remove(p); err != nil {
			failed = append(failed, f)
			continue
		}
		removed++
	}
	msg := fmt.Sprintf("🗑 已删除成片产物 %d 个文件(项目「%s」)", removed, id)
	if len(failed) > 0 {
		msg += fmt.Sprintf("；⚠ %d 个没删掉（%s）—— 多半是被别的程序占着（播放器/剪辑软件开着它），关掉再删",
			len(failed), strings.Join(failed, "、"))
	}
	s.bus.Publish(pipeline.Event{Phase: "system", Level: "info", Message: msg})
	// ok 只在**真的全删掉**时为真：界面据此决定要不要刷新"已删除"的观感，
	// 而"只删掉一半"必须让调用方能分辨出来（前端据 failed 提示原因）。
	writeJSON(w, 200, map[string]any{"ok": len(failed) == 0, "removed": removed, "failed": failed})
}

var _ = json.Marshal
