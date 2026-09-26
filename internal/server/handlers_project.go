package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
)

// rescanProjects 重扫项目目录(POST /api/projects/rescan)。
//
// 技能端(ManJuNovel)把成套项目包直接写进 projects/<书名>/<集名>/,而 Store 只在
// 启动时扫一次盘 —— 这个端点让"技能端出脚本 → 平台端点一下就能看见",不必重启应用。
// 只新增、不删除(语义边界见 project.Store.Rescan 注释)。
func (s *Server) rescanProjects(w http.ResponseWriter, r *http.Request) {
	added, total := s.store.Rescan()
	if len(added) > 0 {
		s.opLog("🔄 重扫项目目录:新增 %d 个(技能端产出或手工放入)—— %s",
			len(added), strings.Join(added, "、"))
		for _, id := range added {
			if plan, err := s.store.LoadPlan(id); err == nil {
				s.opLog("    %s:%d 镜 / %d 角色 / %d 场景(方案已就位,可跳过 script 直接渲染)",
					id, len(plan.Shots), len(plan.Characters), len(plan.Scenes))
			} else {
				s.opLog("    %s:尚无 plan.json(需先导入方案或跑分镜阶段)", id)
			}
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "added": added, "total": total})
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	type item struct {
		*project.Project
		HasPlan bool   `json:"has_plan"`
		Done    int    `json:"shots_done"`
		Total   int    `json:"shots_total"`
		Cover   string `json:"cover"`      // 项目封面(角色定妆照/场景图)
		Chars   int    `json:"characters"` // 方案内角色数(首页统计用)
		Scenes  int    `json:"scenes"`     // 方案内场景数
	}
	out := []item{}
	for _, p := range s.store.List() {
		it := item{Project: p, Cover: s.projectCover(p.ID)}
		if plan, err := s.store.LoadPlan(p.ID); err == nil {
			it.HasPlan = true
			it.Total = len(plan.Shots)
			it.Chars = len(plan.Characters)
			it.Scenes = len(plan.Scenes)
			if m, err := s.store.LoadManifest(p.ID); err == nil {
				for _, c := range m {
					if c.Status == project.ShotDone {
						it.Done++
					}
				}
			}
		}
		if run := s.pipe.Running(p.ID); run != nil {
			it.Running = true
			it.Phase = run.Phase
		}
		out = append(out, it)
	}
	writeJSON(w, 200, out)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	cfg := project.ProjectConfig{Params: project.DefaultStageParams()}
	// 全局设置的默认引擎优先(auto=本地优先自动降级;api=纯云端;comfy=纯本地)
	if e := s.cfg.Get().Render.DefaultEngine; e == "auto" || e == "comfy" || e == "api" {
		cfg.Params.Render.Engine = e
	}
	p, err := s.store.Create(in.Name, cfg)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.opLog("＋ 已创建项目「%s」", in.Name)
	writeJSON(w, 200, p)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.store.Get(id)
	if !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	if run := s.pipe.Running(id); run != nil {
		p.Running = true
		p.Phase = run.Phase
	}
	out := map[string]any{"project": p}
	if plan, err := s.store.LoadPlan(id); err == nil {
		out["plan"] = plan
	}
	// 每个角色的**生效实体类型**由后端下发(审计 P1-2):前端原先各自保留一份
	// 关键词推断表,而那份表已经和后端漂移过 —— 界面显示"器物"、渲染按"人"走,
	// 或者相反,用户看到的就是"改了类型没生效"。类型决定整套渲染方法
	// (定妆照取景/多视角方式/镜头身份锁),不允许有两个真源。
	out["char_kinds"] = planCharKinds(id, s)
	if assets, err := s.store.LoadAssets(id); err == nil {
		out["assets"] = assets
	}
	if m, err := s.store.LoadManifest(id); err == nil {
		out["manifest"] = m
	}
	if source, err := s.store.LoadSource(id); err == nil {
		out["source"] = source
	}
	// qc 报告
	qcRaw, err := os.ReadFile(filepath.Join(s.store.Dir(id), "output", "qc_report.json"))
	if err == nil {
		var qcAny any
		if json.Unmarshal(qcRaw, &qcAny) == nil {
			out["qc"] = qcAny
		}
	}
	// 成片状态(含 2K 定稿)
	outReady := map[string]any{"ready": false, "final": "output/final.mp4"}
	if st, err := os.Stat(filepath.Join(s.store.Dir(id), "output", "final.mp4")); err == nil && st.Size() > 0 {
		outReady["ready"] = true
		outReady["size"] = st.Size()
	}
	outReady["final_2k"] = "output/final_2k.mp4"
	if st, err := os.Stat(filepath.Join(s.store.Dir(id), "output", "final_2k.mp4")); err == nil && st.Size() > 0 {
		outReady["ready_2k"] = true
	}
	out["output"] = outReady
	writeJSON(w, 200, out)
}

// planCharKinds 角色的**生效实体类型**映射(id → kind),供前端显示与选择器使用。
//
// 为什么不把推断结果写回方案:方案里的 `kind` 是用户显式指定的东西,
// "未指定则推断"是渲染时的兜底语义。把兜底结果持久化,等于把一次关键词命中
// 固化成了用户的决定 —— 之后改描述也不会改变类型,反而更难理解。
func planCharKinds(projectID string, s *Server) map[string]string {
	plan, err := s.store.LoadPlan(projectID)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(plan.Characters))
	for _, ch := range plan.Characters {
		out[ch.ID] = string(pipeline.ResolveEntityKind(ch))
	}
	return out
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,请先停止"))
		return
	}
	if err := s.store.Delete(id); err != nil {
		writeErr(w, 400, err)
		return
	}
	// 同步清理条件缓存(cond_cache/{projectID}/ 目录整删,2026-09-11)
	if eng, err := s.reg.Get("comfy"); err == nil {
		if ce, ok := eng.(*comfyengine.Engine); ok {
			ce.CleanupProjectCache(id)
		}
	}
	s.opLog("🗑 已删除项目「%s」(方案/资产/镜头/成片/条件缓存一并移除)", id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) getSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	text, err := s.store.LoadSource(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]string{"text": text})
}

func (s *Server) putSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.store.Get(id); !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.ContainsRune(in.Text, '\uFFFD') {
		writeErr(w, 400, fmt.Errorf("文本含乱码字符(U+FFFD):请通过工作台界面提交,确保 UTF-8 编码"))
		return
	}
	if err := s.store.SaveSource(id, in.Text); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	plan, err := s.store.LoadPlan(id)
	if err != nil {
		writeErr(w, 404, fmt.Errorf("尚未生成分镜方案"))
		return
	}
	writeJSON(w, 200, plan)
}

// putParams 阶段参数调优(整体替换,服务端 Sanitize 钳制)。
func (s *Server) putParams(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,不能调参"))
		return
	}
	p, ok := s.store.Get(id)
	if !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	var in project.StageParams
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	// 拒绝含 U+FFFD 的输入(GBK 直传等编码事故),防脏数据入库
	if strings.ContainsRune(fmt.Sprint(in), '\uFFFD') {
		writeErr(w, 400, fmt.Errorf("参数含乱码字符(U+FFFD):请通过工作台界面提交,确保 UTF-8 编码"))
		return
	}
	in.Sanitize()
	p.Config.Params = in
	if err := s.store.SaveMeta(p); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, p.Config.Params)
}

func (s *Server) runProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Phase  string `json:"phase"`
		Only   []int  `json:"only"`
		Engine string `json:"engine"` // 单次引擎覆盖(auto/comfy/api,空=项目配置)
		Seed   int64  `json:"seed"`   // 单镜微调种子(>0 生效:seed+N*101)
	}
	_ = readBody(r, &in)
	if in.Phase == "" {
		in.Phase = pipeline.PhaseAll
	}
	if err := s.pipe.Start(id, in.Phase, in.Only, in.Engine, in.Seed); err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "phase": in.Phase})
}

// upgrade2K 云端 2K 定稿:body {shot: N}(0=成片)。
func (s *Server) upgrade2K(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,请先停止"))
		return
	}
	var in struct {
		Shot int `json:"shot"`
	}
	_ = readBody(r, &in)
	if err := s.pipe.StartUpgrade2K(id, in.Shot); err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) stopProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.pipe.Stop(id) {
		// 后端已结束而前端状态未同步(常见于刚跑完就点停止):不报错,
		// 返回 stopped=false 让前端立即刷新状态即可。
		writeJSON(w, 200, map[string]any{"ok": true, "stopped": false})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "stopped": true})
}

// ---------- 资产上传 ----------
