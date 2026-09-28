// novels.go 小说库:扫描设置目录中的小说(书=子文件夹,优先「全本」,次选合并「正文」
// 分卷章节;封面取「封面」文件夹内图片),支持弹窗阅读详情与一键视频创作。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/fsutil"
	"github.com/YiIimini/ManJuX/internal/llm"
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
	if s.startProdFn != nil {
		return s.startProdFn(prefix, eps)
	}
	return s.startBatch(prefix, eps)
}

// startBatch 启动批量产线(供 produce 与手动接口共用)。
//
// 集数照常**全部入队**(用户 2026-09-28 第二版口径:一路顺下去、不等人点),防"一上来全解析"
// 的点不在这里,而在**轮到那一集才解析它**(见 novelBatch 的逐集串行铁律与 ensureEpisodeBrief)。
func (s *Server) startBatch(prefix string, eps []string) bool {
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
	batch.prefix, batch.projects, batch.cur, batch.stop, batch.running = prefix, uniq, 0, false, true
	batch.mu.Unlock()
	go s.runNovelBatch(prefix, uniq)
	return true
}

// epBriefMinRunes / epBriefMaxRunes 剧情 brief 的目标字数（与拆集默认 400–700 对齐；
// 低于 minRunes 视为"没写过"，触发解析）。
const (
	epBriefMinRunes = 400
	epBriefMaxRunes = 700
)

// ensureEpisodeBrief 轮到这一集时**才**给它补写剧情 brief（流水线的一步）。
//
// 为什么从"导入时批量补"改成"轮到才补"（用户 2026-09-28）：
// 批量补 = 点一次导入就把整本书的集全部丢给 LLM（每集最多 1.2 万字原文），既慢又挤占产线；
// 而用户要的是**流水线**：到第 N 集就解析第 N 集、接着渲它、完了再第 N+1 集。
//
// 自包含：读这一集项目自己的 `source.txt`（里面已经有【本集原文】），不依赖拆集时的内存状态
// —— 产线可能跨重启、跨入口被拉起，靠闭包捕获上下文会丢。
// 已有像样 brief 的集（技能侧交付 or 上次跑过）直接跳过，不重复烧 token。
func (s *Server) ensureEpisodeBrief(projID string) {
	dir := s.store.Dir(projID)
	srcPath := filepath.Join(dir, "source.txt")
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return
	}
	src := string(raw)
	if brief, ok := episodeBriefOf(src); ok && len([]rune(brief)) >= epBriefMinRunes {
		return // 已经有像样的剧情 brief
	}
	orig := episodeOrigOf(src)
	if len([]rune(orig)) < 200 {
		return // 没有原文可解析（技能侧包/空壳），不硬造
	}
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
		Message: fmt.Sprintf("🧭 轮到这一集了,先做它的 LLM 解析(剧情节拍 brief):%s", filepath.Base(dir))})
	g := s.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	if !client.Configured() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(g.LLM.Timeout)*time.Second)
	defer cancel()
	sys := fmt.Sprintf("你在为一集短视频写「剧情节拍 brief」(中文,%d-%d 字):按该集原文的先后顺序逐拍列出事件、出场人物、"+
		"关键台词(逐字保留重要对白)、情绪转折与结尾悬念;只写这一集的内容,不要扩写、不要引用其它集。",
		epBriefMinRunes, epBriefMaxRunes)
	user := "【本集原文】\n" + clampRunes(orig, 12000)
	out, err := client.Chat(ctx, []llm.Message{{Role: "system", Content: sys}, {Role: "user", Content: user}}, false)
	if err != nil || strings.TrimSpace(out) == "" {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: fmt.Sprintf("⚠ 本集 brief 解析失败(%v),先用原文节拍继续(不阻断)", err)})
		return
	}
	if err := os.WriteFile(srcPath, []byte(replaceEpisodeBrief(src, strings.TrimSpace(out))), 0o644); err != nil {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", Message: "⚠ brief 回写失败:" + err.Error()})
		return
	}
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
		Message: fmt.Sprintf("✔ 本集 brief 已解析并回填(%d 字)", len([]rune(strings.TrimSpace(out))))})
}

// episodeBriefOf 取 source.txt 里【本集剧情】那一段。
func episodeBriefOf(src string) (string, bool) {
	const mark = "【本集剧情】"
	i := strings.Index(src, mark)
	if i < 0 {
		return "", false
	}
	rest := src[i+len(mark):]
	if j := strings.Index(rest, "\n\n"); j >= 0 {
		return strings.TrimSpace(rest[:j]), true
	}
	return strings.TrimSpace(rest), true
}

// episodeOrigOf 取 source.txt 里【本集原文】那一段（没有标记时退回全文）。
func episodeOrigOf(src string) string {
	const mark = "【本集原文】"
	i := strings.Index(src, mark)
	if i < 0 {
		return src
	}
	return strings.TrimSpace(src[i+len(mark):])
}

// splitNovelToProjects 拆集核心(与 /api/novel-split 共用):返回(系列名, 子项目列表)。
// artStyle 来自立项.json 的 art_style 字段(空=由 LLM 自定风格)。
