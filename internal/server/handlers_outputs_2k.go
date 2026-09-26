package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 单镜云端 2K 产物的导出入口(待决项清单 乙7,选项 A:只导出)。
//
// 现状(改动前):upgrade2k.go 把单镜 2K 落成 <项目>/clips/2k/shot_NNN_2k.mp4,
// 而**没有任何消费方** —— 合成阶段只按 manifest.File 收片,这个文件既不进镜头清单、
// 也不参与合成,界面上同样没有入口。用户为它花了云额度,却在应用里找不到它:
// 只有懂行的人才想得到去翻项目目录。
//
// 这里补的是"看得见、拿得走"这一半,**不改合成取片语义** —— 成片里这一镜仍是
// 768p(选项 B「让 2K 进成片」要等用户看过 2K 观感再定,涉及混分辨率拼接)。
//
// 为什么单独一条路由而不是复用 /api/projects/{id}/files/{path...}:
// 那条通配路由面向"项目内任意文件",取回路径由前端拼;这条的名字来自 URL,是
// **对外可点、可分享形状**的下载口,只应允许 clips/2k 下那一类文件。白名单正则
// 把它钉死成一等公民,顺便让界面上要展示的字段(镜号/体积/时间)由后端算好。

// shot2kFileRe 单镜 2K 产物的文件名判据。
//
// 用**白名单形状**而不是"拼好路径再查 ../":
//
//	① 黑名单挡穿越是"漏一种写法就漏一个洞"的游戏 —— Windows 上 `..\`、URL 编码的
//	   %2e%2e%5c、盘符、NTFS 数据流冒号各有各的写法;
//	② 白名单之外的名字根本**没有拼路径的机会**,形状不对直接 400。
//
// 形状与 pipeline/upgrade2k.go 的落盘命名严格对应(`shot_%03d_2k.mp4`),
// 两边一起改才生效 —— 有测试钉住(TestShot2KOutputsRejectsTraversal)。
var shot2kFileRe = regexp.MustCompile(`^shot_[0-9]{3}_2k\.mp4$`)

// shot2kDirName 产物所在目录(相对项目根)。与 upgrade2k.go 的落盘位置一致。
var shot2kDirName = filepath.Join("clips", "2k")

// shot2kOutputItem 列表项:界面直接可用的字段(前端不再自己解析文件名)。
type shot2kOutputItem struct {
	Name      string  `json:"name"`       // shot_003_2k.mp4
	Rel       string  `json:"rel"`        // clips/2k/shot_003_2k.mp4(相对项目根)
	Shot      int     `json:"shot"`       // 镜号(从文件名解析)
	SizeMB    float64 `json:"size_mb"`    // 体积(MB)
	UpdatedAt string  `json:"updated_at"` // 落盘时间 yyyy-MM-dd HH:mm
}

// listShot2KOutputs 列出某项目已生成的单镜 2K 产物。
//
// 目录不存在 / 一个文件都没有 → **200 + 空数组**,不是 404:这不是错误,
// 只是"还没生成过"(界面据此显示引导文案,而不是弹一句报错)。
func (s *Server) listShot2KOutputs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.store.Get(id); !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	out := []shot2kOutputItem{}
	ents, err := os.ReadDir(filepath.Join(s.store.Dir(id), shot2kDirName))
	if err != nil {
		writeJSON(w, 200, out)
		return
	}
	for _, e := range ents {
		if e.IsDir() || !shot2kFileRe.MatchString(e.Name()) {
			// 目录里可能混着半截文件(.part)与其它东西 —— 只列白名单形状的成品
			continue
		}
		st, serr := e.Info()
		if serr != nil || st.Size() <= 0 {
			continue // 0 字节 = 没下完/写坏了,别把它摆出来让人下载
		}
		shot := 0
		if n, cerr := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(e.Name(), "shot_"), "_2k.mp4")); cerr == nil {
			shot = n
		}
		out = append(out, shot2kOutputItem{
			Name:      e.Name(),
			Rel:       filepath.ToSlash(filepath.Join(shot2kDirName, e.Name())),
			Shot:      shot,
			SizeMB:    float64(st.Size()) / 1e6,
			UpdatedAt: st.ModTime().Format("2006-01-02 15:04"),
		})
	}
	// 按镜号升序:用户是"按镜头找文件",而不是按文件名或落盘时间找
	sort.Slice(out, func(i, j int) bool { return out[i].Shot < out[j].Shot })
	writeJSON(w, 200, out)
}

// serveShot2KOutput 按文件名取回单个 2K 产物(预览/下载)。
//
// 发送方式沿用既有的项目文件服务(serveProjectFile 的 http.ServeFile),
// 因此视频拖动播放的 Range 支持与缓存头行为与其它产物完全一致 —— 前端不必
// 为这一个入口写第二套播放逻辑。
func (s *Server) serveShot2KOutput(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !shot2kFileRe.MatchString(name) {
		// 形状不符(含任何路径分隔符/上跳/其它扩展名)一律 400:白名单之外不拼路径
		http.Error(w, "bad name", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	if _, ok := s.store.Get(id); !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	full := filepath.Join(s.store.Dir(id), shot2kDirName, name)
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, full)
}
