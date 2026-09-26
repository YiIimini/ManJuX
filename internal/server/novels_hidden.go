package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// novels_hidden.go 小说库的「隐藏作品」状态。
//
// 【为什么单独一个文件 + sidecar】隐藏是**界面状态**，不是作品的一部分：
// 写进小说目录会在用户的文件区里凭空多出 app 私有文件，还会跟着网盘/备份一起同步走；
// 所以存在 data 目录下的一个 sidecar JSON 里（novels_hidden.json），存**相对小说库根**的路径。
//
// 相对路径而不是绝对路径：用户换机器/换挂载盘（本机双系统，同一个小说库可能挂在
// 不同盘符下）时，绝对路径会全部失配、隐藏状态集体失效；相对根目录的键跟着走。
//
// 键的归一：统一用 '/' 分隔、去掉前后空白 —— Windows 上反斜杠与正斜杠混写会让同一个
// 作品存成两条记录（隐藏了却还显示"未隐藏"）。

const hiddenNovelsFile = "novels_hidden.json"

// novelHiddenKey 作品在 sidecar 里的键（相对小说库根，'/' 分隔；不在根内返回空）。
func (s *Server) novelHiddenKey(p string) string {
	root := s.novelRoot()
	if root == "" || strings.TrimSpace(p) == "" {
		return ""
	}
	if !s.inNovelRoot(p) {
		return ""
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return strings.Trim(rel, "/")
}

// loadHiddenNovels 读隐藏集合；文件缺失/损坏都当空集（隐藏状态丢了只是重新显示，
// 不该让小说库整个打不开）。
func (s *Server) loadHiddenNovels() map[string]bool {
	out := map[string]bool{}
	raw, err := os.ReadFile(s.hiddenNovelsPath())
	if err != nil || len(raw) == 0 {
		return out
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return out
	}
	for _, k := range keys {
		k = strings.Trim(strings.TrimSpace(k), "/")
		if k != "" {
			out[k] = true
		}
	}
	return out
}

// saveHiddenNovels 原子写（临时文件 + rename），排序后落盘方便人肉查。
func (s *Server) saveHiddenNovels(set map[string]bool) error {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	raw, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	path := s.hiddenNovelsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// hiddenNovelsPath sidecar 路径（设置里的 DataDir 下）。
func (s *Server) hiddenNovelsPath() string {
	return filepath.Join(s.cfg.Get().DataDir, hiddenNovelsFile)
}

// setNovelHidden 置/清一部作品的隐藏状态，返回设置后的值。
func (s *Server) setNovelHidden(p string, hidden bool) (bool, error) {
	key := s.novelHiddenKey(p)
	if key == "" {
		return false, fmt.Errorf("路径不在小说库内,或小说库未配置")
	}
	set := s.loadHiddenNovels()
	if hidden {
		set[key] = true
	} else {
		delete(set, key)
	}
	if err := s.saveHiddenNovels(set); err != nil {
		return false, err
	}
	return hidden, nil
}

// handleNovelHidden POST /api/novels/hidden {path, hidden}
//
// 隐藏是**界面状态**:只写 sidecar,不动小说文件。返回 hidden_count 让前端能就地
// 更新"显示已隐藏(N)"的计数,不必再拉一次列表。
func (s *Server) handleNovelHidden(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path   string `json:"path"`
		Hidden bool   `json:"hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]any{"error": "请求体不是合法 JSON"})
		return
	}
	if strings.TrimSpace(in.Path) == "" {
		writeJSON(w, 400, map[string]any{"error": "缺少 path"})
		return
	}
	hidden, err := s.setNovelHidden(in.Path, in.Hidden)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "hidden": hidden, "hidden_count": len(s.loadHiddenNovels()),
	})
}
