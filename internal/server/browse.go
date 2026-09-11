// browse.go 应用内目录浏览(只读):列表指定路径的文件和子目录。
// 不做路径白名单(只读无风险);目录说明等场景点路径即看内容,
// 不依赖系统资源管理器是否弹窗。
package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// handleBrowse GET /api/browse?path=... → {entries:[{name,size,isDir}], total, path}
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		writeErr(w, 400, fmt.Errorf("请提供路径"))
		return
	}
	p = filepath.Clean(p)
	st, err := os.Stat(p)
	if err != nil {
		writeErr(w, 404, fmt.Errorf("路径不存在: %s", p))
		return
	}
	if !st.IsDir() {
		writeErr(w, 400, fmt.Errorf("不是目录: %s", p))
		return
	}
	ents, err := os.ReadDir(p)
	if err != nil {
		writeErr(w, 500, fmt.Errorf("读取失败: %v", err))
		return
	}
	type entry struct {
		Name  string `json:"name"`
		Size  int64  `json:"size"`
		IsDir bool   `json:"isDir"`
	}
	list := make([]entry, 0, len(ents))
	for _, e := range ents {
		size := int64(0)
		if fi, err := e.Info(); err == nil && !e.IsDir() {
			size = fi.Size()
		}
		list = append(list, entry{Name: e.Name(), Size: size, IsDir: e.IsDir()})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].IsDir != list[j].IsDir {
			return list[i].IsDir // 目录在前
		}
		return list[i].Name < list[j].Name
	})
	writeJSON(w, 200, map[string]any{"path": p, "total": len(list), "entries": list})
}
