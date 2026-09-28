// novels.go 小说库:扫描设置目录中的小说(书=子文件夹,优先「全本」,次选合并「正文」
// 分卷章节;封面取「封面」文件夹内图片),支持弹窗阅读详情与一键视频创作。
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/fsutil"
	"github.com/YiIimini/ManJuX/internal/pipeline"
)

type novelJob struct {
	Series  string `json:"series"`
	Created int    `json:"created"` // 已建分集数
	State   string `json:"state"`   // running / done / failed
	Message string `json:"message"`
	At      int64  `json:"at"`
}

var novelJobs = struct {
	sync.Mutex
	m map[string]*novelJob
}{m: map[string]*novelJob{}}

// jobsPath 拆集任务状态的落盘文件;为空则只在内存中维护(未初始化/测试场景)。
var jobsPath string

// SetJobsPath 指定落盘文件并载入上次残留的任务状态(server 构造时调用一次)。
func SetJobsPath(path string) {
	jobsPath = path
	loadNovelJobs()
}

// loadNovelJobs 载入上次进程留下的任务状态。
//
// 进程重启意味着所有「运行中」的拆集都已中断 —— producer 的 channel 随进程
// 消失,无法恢复。因此把 running 统一降级为 failed 并写明原因,而不让它以
// running 的姿态继续显示:否则首页占位卡会重新显示「AI 拆集中」,用户既看不
// 到进度也不知道发生了什么,只能等 20 分钟的孤儿清理(见 handleNovelJobs)兜底。
//
// 已建成的分集本身是落在磁盘上的,不受影响,重新提交即可续拆。
func loadNovelJobs() {
	if jobsPath == "" {
		return
	}
	raw, err := os.ReadFile(jobsPath)

	novelJobs.Lock()
	defer novelJobs.Unlock()
	// 重启语义:以磁盘为准**重建**。清空必须无条件执行 —— 若只在读到文件时才清,
	// 残留的内存状态会在下一次 persist 时被写回磁盘,变成"清理过的条目又复活"。
	// SetJobsPath 在进程启动时调用,此刻本就该是空表。
	novelJobs.m = map[string]*novelJob{}
	if err != nil {
		return // 首次运行无此文件
	}
	var saved map[string]*novelJob
	if err := json.Unmarshal(raw, &saved); err != nil {
		log.Printf("[novels] 拆集任务状态解析失败(忽略):%v", err)
		return
	}
	interrupted := 0
	for k, j := range saved {
		if j == nil {
			continue
		}
		if j.State == "running" {
			j.State = "failed"
			j.Message = "上次拆集被中断(应用重启),可重新提交;已建分集保留"
			j.At = time.Now().Unix() // 重置计时,从本次启动起算保留窗口
			interrupted++
		}
		novelJobs.m[k] = j
	}
	if len(saved) > 0 {
		log.Printf("[novels] 载入 %d 条拆集任务状态(运行中的 %d 条已标记为中断)", len(saved), interrupted)
	}
}

// persistJobsLocked 落盘当前任务表(调用方须持有 novelJobs 锁)。
func persistJobsLocked() {
	if jobsPath == "" {
		return
	}
	raw, err := json.MarshalIndent(novelJobs.m, "", "  ")
	if err != nil {
		return
	}
	if err := fsutil.WriteFileAtomic(jobsPath, raw); err != nil {
		log.Printf("[novels] 拆集任务状态落盘失败:%v", err)
	}
}

func jobSet(series, state string, created int, msg string) {
	novelJobs.Lock()
	defer novelJobs.Unlock()
	if state == "" { // 终态清理
		delete(novelJobs.m, series)
		persistJobsLocked()
		return
	}
	novelJobs.m[series] = &novelJob{Series: series, Created: created, State: state, Message: msg, At: time.Now().Unix()}
	persistJobsLocked()
}

// handleNovelDelete DELETE /api/novels?path= 删除小说库条目(仅允许一级:书目录或单文件)。
func (s *Server) handleNovelDelete(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		writeErr(w, 400, fmt.Errorf("请提供小说路径"))
		return
	}
	if !s.inNovelRoot(p) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
		return
	}
	root := filepath.Clean(s.novelRoot())
	if filepath.Clean(p) == root {
		writeErr(w, 403, fmt.Errorf("不能删除小说库根目录"))
		return
	}
	if filepath.Clean(filepath.Dir(p)) != root {
		writeErr(w, 400, fmt.Errorf("仅支持删除小说库中的一级条目(整本书或单文件)"))
		return
	}
	fi, err := os.Stat(p)
	if err != nil {
		writeErr(w, 404, fmt.Errorf("小说不存在"))
		return
	}
	if fi.IsDir() {
		err = os.RemoveAll(p)
	} else {
		err = os.Remove(p)
	}
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	s.bus.Publish(pipeline.Event{Phase: "system", Level: "info",
		Message: "🗑 已从小说库删除《" + filepath.Base(p) + "》(磁盘文件)"})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleNovelJobsDelete DELETE /api/novel-jobs?series= 移除拆集任务占位(首页失败占位卡清理)。
func (s *Server) handleNovelJobsDelete(w http.ResponseWriter, r *http.Request) {
	series := r.URL.Query().Get("series")
	if series == "" {
		writeErr(w, 400, fmt.Errorf("请提供 series"))
		return
	}
	novelJobs.Lock()
	_, ok := novelJobs.m[series]
	delete(novelJobs.m, series)
	persistJobsLocked()
	novelJobs.Unlock()
	if !ok {
		writeErr(w, 404, fmt.Errorf("任务不存在或已结束"))
		return
	}
	s.bus.Publish(pipeline.Event{Phase: "system", Level: "info",
		Message: "🗑 已移除拆集失败占位卡《" + series + "》"})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleNovelJobs GET /api/novel-jobs:进行中/刚完成的拆集任务(失败保留 10 分钟)。
func (s *Server) handleNovelJobs(w http.ResponseWriter, r *http.Request) {
	novelJobs.Lock()
	defer novelJobs.Unlock()
	now := time.Now().Unix()
	out := []*novelJob{}
	pruned := false
	for k, j := range novelJobs.m {
		if j.State == "failed" && now-j.At > 600 {
			delete(novelJobs.m, k)
			pruned = true
			continue
		}
		// running 兜底:拆集 LLM 通常 2-6 分钟;20 分钟无任何推进(键错位/
		// 进程重启遗留等)按孤儿清掉,占位卡不再永久滞留
		if j.State == "running" && now-j.At > 1200 {
			delete(novelJobs.m, k)
			pruned = true
			continue
		}
		if j.State == "done" && now-j.At > 120 { // 完成态短留(真卡已就位)
			delete(novelJobs.m, k)
			pruned = true
			continue
		}
		out = append(out, j)
	}
	if pruned {
		persistJobsLocked() // 清理结果同步落盘,避免重启后"复活"已清理的条目
	}
	writeJSON(w, 200, map[string]any{"jobs": out})
}

// startProd 是"拉起批量产线"的唯一入口(导入自动开跑 / 手动开始 / 拆集边拆边跑都走它)。
//
// 生产装配下 startProdFn 为 nil,直达真实 startBatch;单元测试把它换成记录器 ——
// 真实的 startBatch 会拉起整条产线(读项目、跑 ComfyUI、结束调 quitApp),那是进程级副作用,
// 不该在单元测试里发生(原因详见 Server.startProdFn 字段注释)。
func (s *Server) startProd(prefix string, eps []string) bool {
	return s.startProdMode(prefix, eps, false)
}

// startProdSingle 单集模式：只跑入队的那几集，跑完收工（导入自动开跑走这条 —— 用户 2026-09-28 指示）。
func (s *Server) startProdSingle(prefix string, eps []string) bool {
	return s.startProdMode(prefix, eps, true)
}

func (s *Server) startProdMode(prefix string, eps []string, single bool) bool {
	if s.startProdFn != nil {
		return s.startProdFn(prefix, eps)
	}
	return s.startBatch(prefix, eps, single)
}

// startBatch 启动批量产线(供 produce 与手动接口共用)。
func (s *Server) startBatch(prefix string, eps []string, single bool) bool {
	if len(eps) == 0 {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "error",
			Message: "✕ 批量产线未启动:《" + prefix + "》没有可处理的分集(拆集 0 集,见上方日志)"})
		return false
	}
	batch.mu.Lock()
	if batch.running {
		batch.mu.Unlock()
		return false
	}
	// 入口去重:同一集只排队一次(重复入队会让整本书跑两轮,白烧 GPU)
	uniq, dropped := dedupEps(eps)
	if dropped > 0 {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: fmt.Sprintf("⚠ 批量产线启动去重:丢弃 %d 个重复的集", dropped)})
	}
	batch.prefix, batch.projects, batch.cur, batch.stop, batch.running, batch.single = prefix, uniq, 0, false, true, single
	batch.mu.Unlock()
	go s.runNovelBatch(prefix, uniq)
	return true
}

// splitNovelToProjects 拆集核心(与 /api/novel-split 共用):返回(系列名, 子项目列表)。
// artStyle 来自立项.json 的 art_style 字段(空=由 LLM 自定风格)。
