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
	"regexp"
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

// llmTimeout 把设置里的 LLM 超时（秒）换算成 Duration，**带兜底**。
//
// 【2026-09-29 运行时审计 P2-8】`context.WithTimeout(ctx, 0)` 的语义是
// "**立即已过期**"，不是"不限时" —— 而设置里的 `llm.timeout_seconds` 可能是 0 或负数
// （前端有 `:min="60"` 挡着，但 `PUT /api/settings` 对数值一个都不校验，
// 手改 `data/settings.json` 在本仓是常态）。后果很隐蔽：那次「小说转剧本」会话
// **瞬间**失败，整本书每一集都退化成"用小说原文直接分镜"，只有一条 warn，
// 批量产线照跑 —— 用户看到的是"跑得挺快、就是分镜不对劲"。
//
// `llm.New()` 自己早就有 `<=0 → 600s` 兜底，直接用配置值算 ctx 的地方没有；
// 现在统一走这里，两处口径一致。
func llmTimeout(sec int) time.Duration {
	if sec <= 0 {
		sec = 600 // 与 llm.New 的默认值同源
	}
	return time.Duration(sec) * time.Second
}

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

// epScriptMinRunes / epScriptMaxRunes 本集**剧本**的目标字数。
//
// 【2026-09-28 用户口径：这一步是「小说转剧本」，不是「写个 brief」】
// 原来这里产出的是"剧情节拍 brief"（400-700 字，只列事件与台词要点）；作者要的是
// **改编剧本** —— 按他的《一、网文小说转剧本提示词指令》：开篇 3 秒爆点、爽点密度、
// 段段留钩、砍冗余、放大「绝境→逆袭」的反差。brief 只是事件清单，剧本才是分镜真正
// 要拍的东西：分镜要知道"这句话怎么说、这个动作怎么演"，不是"这里发生了什么事"。
//
// 低于 minRunes 视为"没写过"，触发解析（判据与产物语义一起改，别只改一半）。
const (
	epScriptMinRunes = 800
	epScriptMaxRunes = 1600
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
	if brief, ok := episodeBriefOf(src); ok && len([]rune(brief)) >= epScriptMinRunes {
		return // 已经有像样的本集剧本
	}
	orig := episodeOrigOf(src)
	if len([]rune(orig)) < 200 {
		return // 没有原文可解析（技能侧包/空壳），不硬造
	}
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
		Message: fmt.Sprintf("🎬 轮到这一集了,先把原文**改编成剧本**(专职编剧会话):%s", filepath.Base(dir))})
	g := s.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	if !client.Configured() {
		return
	}
	// **超时必须兜底**（2026-09-29 运行时审计 P2-8）。
	//
	// `time.Duration(0)*time.Second` 交给 `WithTimeout` 不是"不限时"，而是**立即已过期**
	// —— 于是这次「小说转剧本」会话瞬间失败，整本书每一集都退化成"用小说原文直接分镜"，
	// 只留一条 warn，批量产线照跑。用户看到的是"跑得挺快、就是分镜不对劲"。
	//
	// 可达性：前端有 `:min="60"` 挡着，但 `PUT /api/settings` 对**数值一个都不校验**
	// （只对字符串做回填），而手改 `data/settings.json` 在本仓是常态（盘上有 .bak 为证）。
	// `llm.New()` 自己有 `<=0 → 600` 兜底，这两处直接用配置值算 ctx 的没有 —— 现在统一。
	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout(g.LLM.Timeout))
	defer cancel()
	// 【2026-09-28 用户口径】这是**专职的「小说转剧本」会话**：先给它角色与规矩，
	// 再给原文 —— 逐条口径来自作者的《一、网文小说转剧本提示词指令》，不是我自己编的清单。
	// （作者的原话：每一步新建会话并先给到 LLM，让它知道接下来它该干什么。）
	sys := fmt.Sprintf("你是爆款短视频(AI 漫剧)的**专职编剧**。把用户给的小说原文,改编成"+
		"**可以直接拍成一集短视频的剧本**。\n"+
		"\n"+
		"一、爆款开篇(必执行)\n"+
		"1. 开篇 3 秒爆点:第一句就必须抛出极致冲突/绝境/反转/悬念 —— 无铺垫、无旁白起手,"+
		"直接切入高潮名场面(绝境受辱、生死危机、惊天揭秘、逆袭打脸、天降机缘)。\n"+
		"2. 爽点密度:每 80 字左右一个小爽点,每 200 字左右一个大爽点;主角全程不憋屈、不拖沓,"+
		"逆袭/打脸/揭秘/升阶的节奏拉满,给足即时快感。\n"+
		"3. 段段留钩:每一段结尾必须留悬念/伏笔/反转预告,杜绝平淡收尾,让人看完这段必看下一段。\n"+
		"4. 合规零风险:不写血腥、暴力、色情、恐怖、惊悚、低俗;冲突与惩戒一律用温和方式呈现。\n"+
		"\n"+
		"二、改编纪律\n"+
		"1. 砍掉冗余铺垫、无关支线、冗长对话;保留原文核心主线,开篇直接切入核心冲突。\n"+
		"2. 放大情绪反差:强化主角「绝境/屈辱」与「逆袭/碾压」的对比,让人瞬间共情。\n"+
		"3. 反转密集不套路:每次冲突都给一个意外反转,全程高能。\n"+
		"4. 段落结尾留终极钩子(惊天秘密、强敌来袭、逆天机缘、身世谜团)。\n"+
		"\n"+
		"三、人物与细节\n"+
		"1. 主角气场拉满、智商在线、行动果决 —— 不要懦弱犹豫的人设。\n"+
		"2. 对立角色标签化,冲突感强,反衬主角的逆袭爽感。\n"+
		"3. 补上关键场景/道具/伏笔细节,贴合剧情、不冗余。\n"+
		"\n"+
		"四、写法(记住:这是给**分镜**用的稿子)\n"+
		"短句为主、力道十足、画面感强;段落简短、逻辑清晰;"+
		"每个事件都要能被拍成**一个动作或一句台词** —— 写「他把婚书摔在桌上」,"+
		"而不是「气氛变得紧张」。重要对白逐字保留(分镜会原样进配音)。\n"+
		"\n"+
		// 【2026-09-29 用户:「说明内容转剧本不给力，然后导致分镜脚本不给力」】
		// 上一版只要求"写一段散文剧本"，于是交出来的是一整段画面与对白、没有任何
		// **结构标注** —— 分镜拿着它只能自己猜"哪场戏是重点、哪一下是爽点、机位从哪边来"，
		// 猜出来的就是"机位乱跳、重点平摊"。这一节把输出**钉成结构**，
		// 每条都来自作者的《一、网文小说转剧本提示词指令》（爆点/爽点密度/段段留钩/人物与道具细节）
		// 加上分镜真正需要的"场次与功能"标注。
		"五、**输出格式(必须逐节写出,节标题逐字照抄 —— 分镜会按这些节取信息)**\n"+
		"【开场爆点】一句话:3 秒内抛出极致冲突/绝境/反转/悬念(无铺垫、无旁白起手)。\n"+
		"【场 1|地点|时间|内外】\n"+
		"  目的:这场戏要让观众得到什么(一句话)\n"+
		"  冲突:谁和谁、争什么\n"+
		"  转折:在哪里翻(哪一句台词/哪一个动作)\n"+
		"  爽点:小爽点或大爽点 —— 具体是哪一下\n"+
		"  钩子:这场结尾留下的悬念/伏笔\n"+
		"  正文:该场的动作与台词(短句、画面感、台词逐字)\n"+
		"【场 2|地点|时间|内外】 …(每场一节,场号连续)\n"+
		"【关键道具与信息】\n"+
		"  - 道具名|谁拿着或**在什么位置**(如「蹲在碑生左肩上」「挂在腰侧」)|在本集的作用\n"+
		"  - 信息点|在第几场第一次揭示\n"+
		"【本集钩子】最后一段留的终极钩子(一句)\n"+
		"\n"+
		"只写这一集的内容:不扩写、不引用其它集、不解释你在做什么、不要任何前言后语。\n"+
		"直接输出改编后的剧本全文(中文,%d-%d 字)。",
		epScriptMinRunes, epScriptMaxRunes)
	user := "【本集原文】\n" + clampRunes(orig, 12000)
	out, err := client.Chat(ctx, []llm.Message{{Role: "system", Content: sys}, {Role: "user", Content: user}}, false)
	if err != nil || strings.TrimSpace(out) == "" {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: fmt.Sprintf("⚠ 本集剧本改编失败(%v),先用原文继续(不阻断)", err)})
		return
	}
	if err := os.WriteFile(srcPath, []byte(replaceEpisodeBrief(src, strings.TrimSpace(out))), 0o644); err != nil {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", Message: "⚠ 剧本回写失败:" + err.Error()})
		return
	}
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
		Message: fmt.Sprintf("✔ 本集剧本已改编并回填(%d 字)", len([]rune(strings.TrimSpace(out))))})
	// **结构自检**（2026-09-29 用户：「内容转剧本不给力，导致分镜脚本不给力」）：
	// 剧本少一节，分镜就少一份依据 —— 而这件事此前是**静默**的（剧本照样回填、产线照跑，
	// 只是分镜凭空猜）。缺节就如实报出来，让人重跑这一步。
	if briefLooksEmpty(out) {
		// **空剧本**：这一步本身没跑成（批量产线还没轮到这一集，或中途被打断）。
		// 报"缺 5 个必需节"会把人绕进"去补哪几节"，而实际情况是"这一集还没有剧本"。
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: "⚠ 本集剧本是**空的**（编剧会话没有产出）—— 多半是这一步还没轮到它、或中途被打断；" +
				"重跑批量产线（它会先补剧本再进管线）即可，不需要手工补节"})
		return
	}
	if missing := missingBriefSections(out); len(missing) > 0 {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: fmt.Sprintf("⚠ 本集剧本缺 %d 个必需节：%s —— 分镜会因此少一份依据（重点平摊、机位只能自己猜）；"+
				"建议重跑本阶段。缺哪节补哪节即可", len(missing), strings.Join(missing, "、"))})
	}
}

// briefLooksEmpty 剧本是不是**根本没内容**（不是"写歪了"）。
//
// 【为什么要区分（2026-09-30 用户被这条告警绕住了：「这种啥问题？」）】原来不管是
// "剧本是空的"还是"有内容但少几节"，都报同一句「缺 5 个必需节」—— 而真实情形常常是
// **批量产线还没轮到这一集**（或中途被打断），`source.txt` 里 `【本集剧情】` 下面是空的。
// 那时该说的是"这一步没跑成、重跑一次就好"，而不是让人去补五节。判据取 80 字：
// 一部短剧的一集剧本不可能只有几十字。
func briefLooksEmpty(brief string) bool {
	return len([]rune(strings.TrimSpace(brief))) < 80
}

// briefSectionMarkers 剧本必须有的节（逐字照抄的输出契约的一部分）。
//
// 为什么要有这一道：剧本是**分镜唯一的输入**。少了「目的/冲突/爽点」这一节，
// 分镜就不知道哪一下是重点（成片重点平摊＝压不住场）；少了「关键道具与信息」，
// 道具的位置关系就丢（实测「Q版总蹲在碑生左肩上」在成片里变成了单独蹲在石台上）。
var briefSectionMarkers = []string{
	"【开场爆点】", "【关键道具与信息】", "【本集钩子】",
	"目的:", "冲突:", "转折:", "爽点:", "钩子:",
}

// missingBriefSections 剧本里缺哪些必需节（返回人能读懂的名字）。
func missingBriefSections(brief string) []string {
	var missing []string
	for _, m := range briefSectionMarkers {
		if !strings.Contains(brief, m) {
			missing = append(missing, strings.Trim(m, "【】:"))
		}
	}
	// 至少要有一场戏的场次头（【场 1|…】这种写法，允许全角/半角竖线与空格差异）
	if !regexp.MustCompile(`【场\s*\d+\s*[|｜]`).MatchString(brief) {
		missing = append(missing, "场次头(【场 N|地点|时间|内外】)")
	}
	return missing
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
