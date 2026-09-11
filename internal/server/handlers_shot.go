package server

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/browser"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/winctl"
)

func (s *Server) putShot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,请先停止"))
		return
	}
	var in struct {
		ShotID   int    `json:"shot_id"`
		H3Prompt string `json:"h3_prompt"`
		Duration int    `json:"duration"`
		Mode     string `json:"mode"`
	}
	if err := readBody(r, &in); err != nil || in.ShotID <= 0 {
		writeErr(w, 400, fmt.Errorf("参数错误(shot_id 必填)"))
		return
	}
	plan, err := s.store.LoadPlan(id)
	if err != nil {
		writeErr(w, 404, fmt.Errorf("尚未生成分镜方案"))
		return
	}
	for i := range plan.Shots {
		if plan.Shots[i].ID != in.ShotID {
			continue
		}
		if strings.TrimSpace(in.H3Prompt) != "" {
			plan.Shots[i].H3Prompt = in.H3Prompt
		}
		if in.Duration >= 4 && in.Duration <= 10 {
			plan.Shots[i].Duration = in.Duration
		}
		if in.Mode == "ref2va" || in.Mode == "t2va" {
			plan.Shots[i].Mode = in.Mode
		}
		if err := s.store.SavePlan(id, plan); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	writeErr(w, 404, fmt.Errorf("镜头不存在: %d", in.ShotID))
}

// deleteShot 删除单个分镜:方案移除该镜 + 清理渲染产物与清单项;
// 成片/质检报告/素材包为派生品一并作废(镜头序列已变),其它镜头不受影响。
func (s *Server) deleteShot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,请先停止"))
		return
	}
	shotID, err := strconv.Atoi(r.PathValue("shotID"))
	if err != nil || shotID <= 0 {
		writeErr(w, 400, fmt.Errorf("无效的镜头号"))
		return
	}
	plan, err := s.store.LoadPlan(id)
	if err != nil {
		writeErr(w, 404, fmt.Errorf("尚未生成分镜方案"))
		return
	}
	idx := -1
	for i := range plan.Shots {
		if plan.Shots[i].ID == shotID {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeErr(w, 404, fmt.Errorf("镜头不存在: %d", shotID))
		return
	}
	plan.Shots = append(plan.Shots[:idx], plan.Shots[idx+1:]...)
	if err := s.store.SavePlan(id, plan); err != nil {
		writeErr(w, 500, err)
		return
	}
	// 渲染产物与清单项
	key := strconv.Itoa(shotID)
	if m, err := s.store.LoadManifest(id); err == nil {
		if c, ok := m[key]; ok {
			if c.File != "" {
				_ = os.Remove(filepath.Join(s.store.Dir(id), filepath.FromSlash(c.File)))
			}
			delete(m, key)
			_ = s.store.SaveManifest(id, m)
		}
	}
	// 派生品作废(成片/2K 定稿/质检报告/剪辑素材包)
	outDir := filepath.Join(s.store.Dir(id), "output")
	if ents, err := os.ReadDir(outDir); err == nil {
		for _, e := range ents {
			_ = os.RemoveAll(filepath.Join(outDir, e.Name()))
		}
	}
	s.opLog("🗑 已删除镜头 %d(项目「%s」,成片已作废待重合成)", shotID, id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// importPlan 导入既有分镜方案(JSON,新建项目时上传)。
func (s *Server) importPlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中"))
		return
	}
	var in struct {
		Plan *project.Plan `json:"plan"`
	}
	if err := readBody(r, &in); err != nil || in.Plan == nil || len(in.Plan.Shots) == 0 {
		writeErr(w, 400, fmt.Errorf("无效的分镜方案(需含 shots 数组)"))
		return
	}
	if err := s.store.SavePlan(id, in.Plan); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "shots": len(in.Plan.Shots)})
}

// comfyVersions ComfyUI 版本管理器报告(online=1 联网对比,较慢)。
func (s *Server) comfyVersions(w http.ResponseWriter, r *http.Request) {
	eng, _ := s.reg.Get("comfy")
	ce, ok := eng.(*comfyengine.Engine)
	if !ok {
		writeErr(w, 500, fmt.Errorf("引擎类型错误"))
		return
	}
	online := r.URL.Query().Get("online") == "1"
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	rep, err := ce.Versions(ctx, online)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if online {
		// 在线对比结果进运行日志:成功给摘要(落后几项),失败明确说原因,
		// 不再静默(2026-09-09 用户反馈「超时也不提示、日志也没有」)
		rows := append(append([]comfyengine.VersionItem{}, rep.Core...), rep.Nodes...)
		behind, unknown := 0, 0
		for _, it := range rows {
			switch it.Status {
			case "behind":
				behind++
			case "unknown":
				unknown++
			}
		}
		core := "本体未装"
		if len(rep.Core) > 0 {
			c := rep.Core[0]
			if c.Remote != "" || c.RemoteCommit != "" {
				core = "本体在线 " + c.Remote
				if c.RemoteCommit != "" {
					core += "@" + c.RemoteCommit
				}
			} else {
				core = "本体在线查询失败"
			}
		}
		// 版本/网络状态只落文件日志与管理页展示(用户 2026-09-09 定调):
		// 运行日志是应用内部操作与管线渲染的事件流,不放远程网络类噪音
		// (在线查询失败曾每 3s 补查一轮刷屏)。
		if behind == 0 && unknown == 0 {
			log.Printf("[version] 版本检查完成:%s,全部最新", core)
		} else {
			log.Printf("[version] 版本检查完成:%s,落后 %d 项%+s", core, behind, suffix(unknown))
		}
	}
	writeJSON(w, 200, rep)
}

// suffix unknown 计数补充文案。
func suffix(unknown int) string {
	if unknown > 0 {
		return fmt.Sprintf(",%d 项未获取到(GitHub 限流或网络不通,稍后自动重试)", unknown)
	}
	return ""
}

// comfyOpenWeb 用系统默认浏览器打开 ComfyUI WEB 界面。
func (s *Server) comfyOpenWeb(w http.ResponseWriter, r *http.Request) {
	eng, _ := s.reg.Get("comfy")
	ce, ok := eng.(*comfyengine.Engine)
	if !ok {
		writeErr(w, 500, fmt.Errorf("引擎类型错误"))
		return
	}
	url := ce.Cfg().BaseURL + "/"
	if err := browser.OpenDefault(url); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "url": url})
}

// comfyOpenAppWin 用独立 App 窗口打开 ComfyUI WEB(Chrome/Edge --app 模式,
// 独立任务栏图标无地址栏,观感即桌面窗口;webview 内嵌 iframe 会被
// 混合内容安全策略拦截,已废弃)。
func (s *Server) comfyOpenAppWin(w http.ResponseWriter, r *http.Request) {
	eng, _ := s.reg.Get("comfy")
	ce, ok := eng.(*comfyengine.Engine)
	if !ok {
		writeErr(w, 500, fmt.Errorf("引擎类型错误"))
		return
	}
	url := ce.Cfg().BaseURL + "/"
	if err := browser.OpenAppWindow(url, 1560, 940); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "url": url})
}

// regenAsset 单资产重生成(Krea-2 换 seed)。
func (s *Server) regenAsset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,请先停止"))
		return
	}
	var in struct {
		AssetID string `json:"asset_id"`
	}
	if err := readBody(r, &in); err != nil || in.AssetID == "" {
		writeErr(w, 400, fmt.Errorf("asset_id 不能为空"))
		return
	}
	if err := s.pipe.RegenAsset(id, in.AssetID); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- 资产库 ----------

func (s *Server) listLibrary(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		writeErr(w, 500, fmt.Errorf("资产库未启用"))
		return
	}
	writeJSON(w, 200, s.lib.List())
}

func (s *Server) libraryImage(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		http.Error(w, "library disabled", 500)
		return
	}
	// ?file= 指定具体图片(多视角原图/转台图);缺省取主图
	img, err := s.lib.ImagePathByName(r.PathValue("kind"), r.PathValue("name"), r.URL.Query().Get("file"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, img)
}

// libraryDetail 单个库资产详情:含中文描述、英文提示词与**全部图片**
// (主图 + 多视角原图 + 转台图),供前端详情弹窗展示与抽卡等管理操作。
func (s *Server) libraryDetail(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		writeErr(w, 500, fmt.Errorf("资产库未启用"))
		return
	}
	e, _, ok := s.lib.Match(r.PathValue("kind"), r.PathValue("name"))
	if !ok {
		writeErr(w, 404, fmt.Errorf("库资产不存在"))
		return
	}
	// 老条目(多图入库之前落盘的)没有 Images 字段,就地补成"只有主图",
	// 免前端拿到空数组而一张图都显示不出来。
	if len(e.Images) == 0 && e.Image != "" {
		e.Images = []string{e.Image}
	}
	writeJSON(w, 200, e)
}

func (s *Server) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		writeErr(w, 500, fmt.Errorf("资产库未启用"))
		return
	}
	if err := s.lib.Delete(r.PathValue("kind"), r.PathValue("name")); err != nil {
		writeErr(w, 400, err)
		return
	}
	s.opLog("🗑 已删除资产库条目「%s」(%s)", r.PathValue("name"), r.PathValue("kind"))
	writeJSON(w, 200, map[string]any{"ok": true})
}

// quitApp 界面确认后的强制退出(停管线 → 优雅关窗)。
func (s *Server) quitApp(w http.ResponseWriter, r *http.Request) {
	for _, p := range s.store.List() {
		s.pipe.Stop(p.ID)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	go func() {
		// 等 300ms 让响应送达。不能等 r.Context().Done():桌面(Wails)模式下
		// AssetServer 的请求由 http.NewRequest 构造,context 是 Background,
		// 永不 cancel —— 曾致「直接退出」点击无反应(浏览器模式正常、桌面死等)。
		time.Sleep(300 * time.Millisecond)
		// 桌面模式走 winctl(置放行标志再 Quit,防 OnBeforeClose 反复拦截)
		winctl.Default.ForceQuit()
		if s.quit != nil {
			s.quit()
		}
		time.Sleep(2 * time.Second) // 兜底:Quit 未生效则强杀
		os.Exit(0)
	}()
}

// ---------- 静态前端 ----------

func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	sub, _ := fs.Sub(webFS, "webdist")
	upath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if upath == "" {
		upath = "index.html"
	}
	f, err := sub.Open(upath)
	if err != nil {
		// SPA fallback
		upath = "index.html"
		f, err = sub.Open(upath)
		if err != nil {
			http.Error(w, "frontend not built", http.StatusNotFound)
			return
		}
	}
	defer f.Close()
	if strings.HasSuffix(upath, ".html") {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	}
	http.ServeFileFS(w, r, sub, upath)
}

func sanitizeFilename(name string) string {
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r >= 0x2e80: // CJK 及全角
			sb.WriteRune(r)
		case r == '_', r == '-', r == '.':
			sb.WriteRune(r)
		}
	}
	return strings.Trim(sb.String(), ".")
}
