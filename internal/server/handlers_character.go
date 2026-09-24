package server

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/pipeline"
)

// putCharacter 编辑一张角色卡(名称/描述/定妆照提示词/**实体类型**)。
//
// 为什么必须有这个口子(2026-09-24 用户报「阿凿是一只铜雀却渲染成人物」):
// 类型判定是"LLM 填 + 代码推断"两道,都可能错(实测铜雀被当成半身人像渲染);
// 错了不能只能靠"重跑分镜"碰运气 —— 用户必须能在界面上把这一张卡改成"物",
// 然后重新生成定妆照。改完立刻生效:取景、多视角方式、镜头身份锁全部跟着换。
func (s *Server) putCharacter(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,请先停止"))
		return
	}
	var in struct {
		CharID      string `json:"id"`
		Name        string `json:"name"`
		Kind        string `json:"kind"`
		Description string `json:"description"`
		ImagePrompt string `json:"image_prompt"`
	}
	if err := readBody(r, &in); err != nil || strings.TrimSpace(in.CharID) == "" {
		writeErr(w, 400, fmt.Errorf("参数错误(角色 id 必填)"))
		return
	}
	kind := pipeline.NormalizeEntityKind(in.Kind)
	if kind == "" {
		writeErr(w, 400, fmt.Errorf("实体类型无效:%s(可选:%s)", in.Kind, kindKeys()))
		return
	}
	plan, err := s.store.LoadPlan(id)
	if err != nil {
		writeErr(w, 404, fmt.Errorf("尚未生成分镜方案"))
		return
	}
	for i := range plan.Characters {
		ch := &plan.Characters[i]
		if ch.ID != in.CharID {
			continue
		}
		// 类型真的变了 → 按旧类型出的角度图必须作废:它们是"这个角色的第二张参考",
		// 留着会把渲染往旧类型拉(实测场景:铜雀的人像定妆照 + 人形转身图会被一起喂回)。
		oldKind := pipeline.ResolveEntityKind(*ch)
		cleared := 0
		if oldKind != kind {
			if assets, aerr := s.store.LoadAssets(id); aerr == nil {
				if a, ok := assets["char:"+ch.ID]; ok && a.File != "" {
					cleared = pipeline.ClearDerivedViews(filepath.Join(s.store.Dir(id), a.File))
				}
			}
		}
		ch.Kind = string(kind)
		if n := strings.TrimSpace(in.Name); n != "" {
			ch.Name = n
		}
		if d := strings.TrimSpace(in.Description); d != "" {
			ch.Description = d
		}
		if p := strings.TrimSpace(in.ImagePrompt); p != "" {
			ch.ImagePrompt = p
		}
		if err := s.store.SavePlan(id, plan); err != nil {
			writeErr(w, 500, err)
			return
		}
		s.opLog("✎ 角色类型已更新:%s「%s」→ %s(作废旧角度图 %d 张)", id, ch.Name, pipeline.EntityKindLabel(kind), cleared)
		writeJSON(w, 200, map[string]any{
			"ok":            true,
			"kind":          string(kind),
			"name":          pipeline.EntityKindLabel(kind),
			"cleared_views": cleared,
			// 改类型只影响**之后**的渲染:已生成的定妆照不会自己变 —— 必须让用户知道
			// 下一步该点「重新生成」,否则他会以为改了没用。
			"note": "类型已保存;按旧类型出的角度图已作废。定妆照仍是旧的,请对这张卡点「重新生成」重出定妆照,多视角随后会自动按新类型重建",
		})
		return
	}
	writeErr(w, 404, fmt.Errorf("角色不存在: %s", in.CharID))
}

func kindKeys() string {
	keys := make([]string, 0, len(pipeline.EntityKinds))
	for _, k := range pipeline.EntityKinds {
		keys = append(keys, string(k))
	}
	return strings.Join(keys, "/")
}
