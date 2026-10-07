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
//
// 【2026-10-07 起，上面那四条纪律里有三条有了**机器判据**】见 brief_viral.go：
// 爆点必须真的演在第 1 场正文最前面、全集至少 2 个大爽点、每一场都要逐节写全。
// 「放大反差」「砍冗余的取舍」要靠人看，**不进机器门**（进了就是天天误报）。
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
	// 【2026-10-06 审计：**长度够 ≠ 已经是剧本**】原来只判"≥800 字就跳过"，于是两种
	// 情形会静默地把分镜坑掉：
	//   ① 拆集那一步的 LLM 写超了（它被要求 400-700 字的**剧情节拍 brief**，模型常态超发），
	//      于是一份"节拍清单"被当成剧本长驻，分镜缺「目的/冲突/爽点」全部依据；
	//   ② 生成的剧本缺节 —— 而缺节的剧本**不是剧本**（分镜照旧少依据）。
	// 两种情况在用户侧的可见后果一样：按告警说的"重跑本阶段"，却被长度判据直接跳过，
	// **什么都不会发生**（给的动作做不到，比不告警更糟）。
	// 现在的判据：长度够 **且结构完整** 才算"已经是剧本"（见 briefIsUsable）；缺结构就重做这一集。
	if brief, ok := episodeBriefOf(src); ok {
		if briefIsUsable(brief) {
			return // 已经是一份像样的本集剧本（结构完整 + 爆款纪律都过）
		}
		// 不达标就重写这一集 —— 但**先把旧稿备份出去**：它可能是作者手写或技能侧给的稿子，
		// 直接盖掉既丢内容、也让人无法对照"原来差哪几条"。
		//
		// 【2026-10-07 修】备份门槛原先是「缺节 **且** ≥800 字」—— 两个条件都是**借来的**：
		// 一份 600 字但真有内容的旧稿（模型少写）会被静默覆盖，连备份都没有。
		// 现在只问"旧稿里有没有内容"（briefLooksEmpty），与"为什么重写"解耦；
		// 判据换成 briefContractIssues（结构 + 爆款纪律），播报里逐条列出到底差哪几条。
		if issues := briefContractIssues(brief); len(issues) > 0 && !briefLooksEmpty(brief) {
			backup := filepath.Join(dir, "source-brief-prev.txt")
			if err := os.WriteFile(backup, []byte(brief), 0o644); err != nil {
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
					Message: fmt.Sprintf("⚠ 本集旧稿有 %d 条不合剧本契约（%s），但**备份不出去**(%v) —— 为免销毁内容，本次不重写；"+
						"请检查项目目录的写入权限后重跑", len(issues), briefIssueSummary(issues, 2), err)})
				return
			}
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
				Message: fmt.Sprintf("🔁 本集旧稿有 %d 条不合剧本契约：%s —— 旧稿已备份到 source-brief-prev.txt，本次按契约**重写这一集剧本**",
					len(issues), briefIssueSummary(issues, 3))})
		}
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
		"直接切入高潮名场面(绝境受辱、生死危机、惊天揭秘、逆袭打脸、天降机缘)。"+
		"**这句台词或动作要写成第 1 场正文的最前面 2-3 行** —— 分镜第一镜就得拍到它;"+
		"写成「某年某地、某个不该存在的××睁开了眼」这种旁白概述不算爆点。\n"+
		"2. 爽点密度:每 80 字左右一个小爽点,每 200 字左右一个大爽点;主角全程不憋屈、不拖沓,"+
		"逆袭/打脸/揭秘/升阶的节奏拉满,给足即时快感。"+
		"每场的「爽点:」节必须写明是「小爽点」还是「大爽点」,**全集至少 2 个大爽点**,写清具体是哪一下。\n"+
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
		"【开场爆点】一句话:3 秒内抛出极致冲突/绝境/反转/悬念(无铺垫、无旁白起手);"+
		"**这句台词或动作必须出现在第 1 场正文的最前面 2-3 行**(分镜第一镜就拍它)。\n"+
		"【场 1|地点|时间|内外】\n"+
		"  (**每一场**都要把这五节 + 正文逐节写全 —— 缺一节,分镜就少一份依据)\n"+
		"  目的:这场戏要让观众得到什么(一句话)\n"+
		"  冲突:谁和谁、争什么\n"+
		"  转折:在哪里翻(哪一句台词/哪一个动作)\n"+
		"  爽点:写明「小爽点——…」或「大爽点——…」并说清具体是哪一下"+
		"(**全集至少 2 个大爽点**:逆袭/打脸/揭秘/升阶,写成能拍的动作或台词)\n"+
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
	// **生成 → 机器判定 → 喂回去重写**（本仓对模型的一贯口径，见 internal/director/repair.go）。
	//
	// 判据是**机器能判定**的东西 —— 结构缺节（missingBriefSections）**加上**爆款纪律
	// （briefViralIssues：爆点有没有真的演在第 1 场正文最前面、大爽点够不够、每场有没有逐节写全）
	// 合起来看 briefContractIssues。所以违规不该只留一句告警：把违规连同上一稿一起喂回去，
	// 让它改**一轮**。只做一轮 —— 预算失控比留着缺陷更糟
	// （用户会为了一集反复重跑，每次多一轮调用既慢又容易触发限流）。
	if issues := briefContractIssues(out); len(issues) > 0 && !briefLooksEmpty(out) {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: fmt.Sprintf("⚠ 本集剧本有 %d 条不合契约：%s —— 喂回去**补写一轮**（剧本少一份依据，分镜就少一份依据）",
				len(issues), briefIssueSummary(issues, 3))})
		// 指令必须写成**可执行动作**（本仓对模型的一贯做法）：不写"要有爆点"，而写
		// "把这一句放进第 1 场正文的最前面"；不写"爽点要密"，而写"至少 2 个大爽点、写明标签"。
		ask := fmt.Sprintf("你上一稿有 %d 条不合剧本契约，逐条列在下面：\n%s\n\n"+
			"请**逐条改掉**，并重新输出**完整剧本全文**（不要解释、不要前言后语、不要只列出改动）：\n"+
			"· 开场 3 秒爆点：【开场爆点】里的那一句（台词或动作）必须出现在**第 1 场正文的最前面 2-3 行** —— "+
			"不许让正文从环境描写、外形描写或旁白起手（分镜第一镜就得拍到它）。\n"+
			"· 爽点：每场的「爽点:」节写明「小爽点——…」或「大爽点——…」；全集至少 2 个大爽点（逆袭/打脸/揭秘/升阶），"+
			"每个都写成能拍的动作或台词。\n"+
			"· 结构：节标题逐字照抄（【开场爆点】、【关键道具与信息】、【本集钩子】，以及每场下面的"+
			"「目的:/冲突:/转折:/爽点:/钩子:」—— 冒号写成半角或全角都可以，不影响判定），场号连续，"+
			"场次头写成「【场 N|地点|时间|内外】」，**每一场都要把这五节 + 正文写全**。\n"+
			"· 台词逐字保留（分镜会原样进配音），中文，%d-%d 字。",
			len(issues), briefIssueBullets(issues), epScriptMinRunes, epScriptMaxRunes)
		if fixed, ferr := client.Chat(ctx, []llm.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: user},
			{Role: "assistant", Content: out},
			{Role: "user", Content: ask},
		}, false); ferr == nil && strings.TrimSpace(fixed) != "" {
			after := briefContractIssues(fixed)
			// **只在真的变好且没改坏时才采纳**（违规条数变少、且不是空稿）：与 repair.go 的
			// adoptRepaired 同一条纪律 —— 自纠不许把产物弄坏。
			if adoptRepairedBrief(issues, fixed) {
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
					Message: fmt.Sprintf("✔ 补写一轮：不合契约从 %d 条降到 %d 条（剩：%s）",
						len(issues), len(after), orDefault(briefIssueSummary(after, 2), "无"))})
				out = fixed
			} else {
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
					Message: fmt.Sprintf("⚠ 补写一轮后不合契约的条数没有变少（仍 %d 条）—— 保留原稿（改坏的不采纳）", len(after))})
			}
		}
	}
	if err := os.WriteFile(srcPath, []byte(replaceEpisodeBrief(src, strings.TrimSpace(out))), 0o644); err != nil {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", Message: "⚠ 剧本回写失败:" + err.Error()})
		return
	}
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
		Message: fmt.Sprintf("✔ 本集剧本已改编并回填(%d 字)", len([]rune(strings.TrimSpace(out))))})
	// **结构自检的收尾播报**（2026-09-29 用户：「内容转剧本不给力，导致分镜脚本不给力」）：
	// 剧本少一节，分镜就少一份依据。上面已经补写过一轮，走到这里就是"补完仍缺"或"空稿"。
	// 这里的话必须与**代码真会做的事**一致（2026-10-06 审计：原来说"建议重跑本阶段"，
	// 而重跑会被长度判据直接跳过 —— 建议做不到，比不报还糟；现在重跑确实会重写这一集）。
	if briefLooksEmpty(out) {
		// **空剧本**：这一步本身没跑成（批量产线还没轮到这一集，或中途被打断）。
		// 报"缺 5 个必需节"会把人绕进"去补哪几节"，而实际情况是"这一集还没有剧本"。
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: "⚠ 本集剧本是**空的**（编剧会话没有产出）—— 多半是这一步还没轮到它、或中途被打断；" +
				"重跑批量产线（它会先补剧本再进管线）即可，不需要手工补节"})
		return
	}
	if issues := briefContractIssues(out); len(issues) > 0 {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: fmt.Sprintf("⚠ 本集剧本补写一轮后仍有 %d 条不合契约：%s —— 分镜会因此少一份依据（重点平摊、机位只能自己猜）。"+
				"**重跑本阶段会重写这一集**（旧稿会备份成 source-brief-prev.txt）；也可以直接手改 source.txt 的那几处后重跑",
				len(issues), briefIssueSummary(issues, 3))})
	}
}

// briefLooksEmpty 剧本是不是**根本没内容**（不是"写歪了"）。
//
// 【为什么要区分（2026-09-30 用户被这条告警绕住了：「这种啥问题？」）】原来不管是
// "剧本是空的"还是"有内容但少几节"，都报同一句「缺 5 个必需节」—— 而真实情形常常是
// **批量产线还没轮到这一集**（或中途被打断），`source.txt` 里 `【本集剧情】` 下面是空的。
// 那时该说的是"这一步没跑成、重跑一次就好"，而不是让人去补五节。判据取 80 字：
// 一部短剧的一集剧本不可能只有几十字。
// briefIsUsable 这一份剧情段**能不能当剧本用**（2026-10-06 审计；2026-10-07 加爆款纪律）。
//
// 判据三条，缺一不可：**字数达标** + **结构完整** + **爆款纪律**（briefViralIssues）。
// 只看字数是老判据，它把两份"不是剧本的东西"放了过去：
//
//	· 拆集那一步的**剧情节拍 brief**（被要求 400-700 字，模型常态超发到 800+）——
//	  它是事件清单，没有「目的/冲突/爽点/钩子」，分镜拿它只能自己猜哪一下是重点；
//	· 生成了但缺节的剧本 —— 缺哪节就是缺哪份依据。
//
// 两者都会让 `【本集剧情】` 长驻一份"看着有内容"的稿子，而分镜一直少依据；
// 更要紧的是：告警让人"重跑本阶段"，而重跑正是被这个判据挡住的 ⇒ 建议做不到。
//
// 【为什么把爆款纪律也算"不可用"】用户 2026-10-07：「总感觉剧本提取文章内容不是按照爆款短剧
// 要求来的」。那四条纪律里能确定性判的（爆点有没有真的演在第 1 场正文最前面、大爽点够不够、
// 每场有没有逐节写全）都是**这份稿子合不合格**的一部分 —— 不合格就该按契约重做这一集
// （旧稿先备份），而不是"照跑，只留一条告警"。
func briefIsUsable(brief string) bool {
	if len([]rune(strings.TrimSpace(brief))) < epScriptMinRunes {
		return false
	}
	return len(briefContractIssues(brief)) == 0
}

// adoptRepairedBrief 补写一轮的结果能不能采纳：**不合契约的条数必须真的变少，且不能变成空稿**。
//
// 与 `director.adoptRepaired` 同一条纪律（自纠不许把产物弄坏）：模型"改一遍"完全可能
// 改得更差（少了两节、把台词删了、只回一句"好的"）。判据写死在这里，别人问"补写到底
// 有没有用"时看这一个函数即可。
//
// 判据与生成后的自检**同一份**（briefContractIssues：结构 + 爆款纪律）——
// 否则会出现"按结构判变好了、按爆款判更差了"这种自相矛盾的采纳。
func adoptRepairedBrief(before []string, fixed string) bool {
	if briefLooksEmpty(fixed) {
		return false // 空稿 = 这一轮没产出，绝不能顶掉原稿
	}
	return len(briefContractIssues(fixed)) < len(before)
}

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
	"目的", "冲突", "转折", "爽点", "钩子",
}

// briefHasMarker 剧本里有没有这个节标记（**冒号全角/半角都认**）。
//
// 【为什么必须容忍全角冒号】（2026-10-07 真机实测，代价很大）
// 中文模型写「目的：」是常态 —— 那一集的 source.txt 里「目的：」4 处、「目的:」**0** 处，
// 而这套判据原来只认半角 ⇒ 报「缺 5 个必需节：目的、冲突、转折、爽点、钩子」。一个冒号宽度
// 引来三件事：
//
//	① 假告警（那五节其实都在，内容一个字不缺）；
//	② 自纠"补写一轮"**永远补不上**（模型下一轮还写全角）⇒ 白烧一次 LLM；
//	③ 最要紧：跳过判据 `briefIsUsable` 用的就是它 —— 于是**每跑一次这一集都会重写剧本**，
//	   把改过的稿子覆盖掉（只在 source-brief-prev.txt 留一份备份）。
//
// 判据只放宽"冒号宽度"这一维，不放宽"有没有这一节"：仍然要求「名字 + 冒号」相邻出现，
// 所以散文里随便提到「目的」二字不会被误判成"这节写了"。
func briefHasMarker(brief, name string) bool {
	for _, sep := range []string{":", "：", " :", " ："} {
		if strings.Contains(brief, name+sep) {
			return true
		}
	}
	return false
}

// missingBriefSections 剧本里缺哪些必需节（返回人能读懂的名字）。
func missingBriefSections(brief string) []string {
	var missing []string
	for _, m := range briefSectionMarkers {
		// 【…】类的节名按字面找；「目的/冲突/…」这类要求"名字 + 冒号"（两种冒号都认）
		if strings.HasPrefix(m, "【") {
			if !strings.Contains(brief, m) {
				missing = append(missing, strings.Trim(m, "【】:"))
			}
			continue
		}
		if !briefHasMarker(brief, m) {
			missing = append(missing, m)
		}
	}
	// 至少要有一场戏的场次头（【场 1|…】这种写法，允许全角/半角竖线与空格差异）
	if !regexp.MustCompile(`【场\s*\d+\s*[|｜]`).MatchString(brief) {
		missing = append(missing, "场次头(【场 N|地点|时间|内外】)")
	}
	return missing
}

// briefEndMarkers 剧情段在 source.txt 里的**终止标记**（平台自己写在这一段之后的节）。
//
// 【为什么不能用"第一个空行"当边界】（2026-10-07 真机实测，代价最大的一处）
// 原实现是 `strings.Index(rest, "\n\n")` —— 而**模型写的剧本本来就有空行**：
// 真机第01集 source.txt 的【本集剧情】下面是
//
//	【本集剧情】【开场爆点】“重新检测到生命反应。”——灰纪元第八万年，…
//	                      ← 这里就有一个空行
//	【场 1|归墟环·维护舱|灰纪元第八万年·第三千四百六十二标准日|内】
//
// 于是 episodeBriefOf 只取回 **51 字**（那一句爆点），而整段剧本是 2090 字。三件事一起发生：
//
//	① 完整性判据 `briefIsUsable` 永远判"这份剧本不可用"（字数与结构都按 51 字那一段算）；
//	② 于是**每跑一次这一集都重新调 LLM 改编剧本**、把上一次的稿子覆盖掉；
//	③ 连备份都轮不到 —— 备份分支的门槛里写着 `len(brief) >= epScriptMinRunes`，51 字过不去，
//	   所以是**静默覆盖**（用户"总感觉剧本提取文章内容不是按爆款要求来的"里有一半是这个：
//	   每轮都是新编的，内容当然飘；而且写回时同样按"第一个空行"切，旧稿会残留在文件里）。
//
// 现在的边界取**平台自己写的节标记**（拆集那一步写的是 `\n\n【本集原文】(素材：…)`），
// 与"模型爱在哪里空行"无关。
// 标记**不带换行符**：读的时候落在【上、由 TrimSpace 收尾；写回时由调用方补一个空行，
// 免得吃掉原来那一行分隔。
var briefEndMarkers = []string{"【本集原文】", "【本集剧情】", "【前情提要】"}

// briefSectionEnd 剧情段在 src[from:] 里的结束下标（不含终止标记）；无标记 ⇒ 到文末。
func briefSectionEnd(rest string) int {
	end := -1
	for _, m := range briefEndMarkers {
		if i := strings.Index(rest, m); i >= 0 && (end < 0 || i < end) {
			end = i
		}
	}
	if end < 0 {
		return len(rest)
	}
	return end
}

// episodeBriefOf 取 source.txt 里【本集剧情】那一段（到下一个平台节标记为止）。
func episodeBriefOf(src string) (string, bool) {
	const mark = "【本集剧情】"
	i := strings.Index(src, mark)
	if i < 0 {
		return "", false
	}
	rest := src[i+len(mark):]
	return strings.TrimSpace(rest[:briefSectionEnd(rest)]), true
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
