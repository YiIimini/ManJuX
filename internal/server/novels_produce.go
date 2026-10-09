// novels.go 小说库:扫描设置目录中的小说(书=子文件夹,优先「全本」,次选合并「正文」
// 分卷章节;封面取「封面」文件夹内图片),支持弹窗阅读详情与一键视频创作。
package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/YiIimini/ManJuX/internal/pipeline"
)

func (s *Server) handleNovelProduce(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
		Max  int    `json:"max_episodes"`
		// 【2026-10-09】导入制作时的**渲染风格选择**（前端「视频制作」对话框）：
		// 勾了预设/写了附加要求 ⇒ 它成为本剧的权威风格串（覆盖立项 art_style）；
		// 都不给 ⇒ 保持老行为（立项 art_style 优先，其次由分镜会话自定）。
		Styles    []string `json:"styles"`
		StyleHint string   `json:"style_hint"`
	}
	if err := readBody(r, &in); err != nil || strings.TrimSpace(in.Path) == "" {
		writeErr(w, 400, fmt.Errorf("请提供小说路径"))
		return
	}
	if !s.inNovelRoot(in.Path) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
		return
	}
	fi, err := os.Stat(in.Path)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	// 书名与正文
	var name, text string
	var coverSrc string
	if fi.IsDir() {
		it := s.scanNovelDir(in.Path, filepath.Base(in.Path))
		name, coverSrc = it.Name, it.Cover
		// 合并正文章节(分册文件);内容无章头的册从文件名注入章头,
		// 保证章界(切块/CHAPTERS/【本集原文】附着全依赖章头)。
		// 「哪些算正文册」与「按什么顺序」由 collectManuscript 唯一决定 ——
		// 2026-09-29 实测这里原来按路径全收,同时踩了 README 混入与卷序打乱两个坑
		// (详见 collectManuscript 的说明)。
		paths, _ := collectManuscript(in.Path)
		readMerged := func() (string, int) {
			var sb strings.Builder
			read := 0
			var unread []string
			for _, p := range paths {
				// 【2026-09-28 审计 A10】原来是 `b, _ := os.ReadFile(p)` —— 读不出的章
				// **只写一个章头、正文整段消失**，而册数照按 `len(paths)` 报（"64 册"）。
				// 用户拿到的是一本缺章的书，界面却在说"64 册已读入"，事后无从查起。
				b, err := os.ReadFile(p)
				if err != nil {
					unread = append(unread, filepath.Base(p))
					continue
				}
				sb.WriteString(ensureChapterHead(filepath.Base(p), string(b)))
				sb.WriteString("\n\n")
				read++
			}
			if len(unread) > 0 {
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
					Message: fmt.Sprintf("⚠ 有 %d 册**读不出来**（%s…）——它们的内容不在本次解析里；"+
						"报出的册数是**成功读入**的 %d 册（目录里共有 %d 册），请先确认这些文件没被占用/损坏",
						len(unread), strings.Join(unread[:min(3, len(unread))], "、"), read, len(paths))})
			}
			return sb.String(), read
		}
		if fbs, _ := filepath.Glob(filepath.Join(in.Path, "全本", "*")); len(fbs) > 0 && hasTxtExt(fbs[0]) {
			b, err := os.ReadFile(fbs[0])
			if err != nil {
				writeErr(w, 500, err)
				return
			}
			full := NormalizeNovelText(b)
			merged, nFiles := readMerged()
			// 数据源:**优先「正文」一章一档**(2026-09-12 用户定调「为什么还是解析的
			// 全本?不是有正文目录里每章一个文档的」)。分章文件的章号与标题由**文件名**
			// 给定,不依赖正文里的章头写法,也不受 markdown 规范化/目录区干扰 —— 这是
			// 「一章一集」最可靠的地基(全本曾因章头识别失准引发缺章误判与误删分集)。
			// 仅当分章文件不足(<8 册)时才回落全本。
			if nFiles >= 8 {
				text = NormalizeNovelText([]byte(merged))
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
					Message: fmt.Sprintf("📖 数据源:正文分章合并(%d 册,一章一档;章号与标题取自文件名)", nFiles)})
			} else {
				text = full
				s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
					Message: fmt.Sprintf("📖 数据源:全本文件(正文分章仅 %d 册,不足一章一档)", nFiles)})
			}
		} else {
			text, _ = readMerged()
		}
	} else {
		name = strings.TrimSuffix(filepath.Base(in.Path), filepath.Ext(in.Path))
		b, err := os.ReadFile(in.Path)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		text = string(b)
	}
	// 文本预处理:不管源文件什么编码/格式,统一转干净 UTF-8 纯文本
	// (编码检测/BOM/不可见字符/智能引号/Markdown 装饰/空行压缩)
	text = NormalizeNovelText([]byte(text))
	if len([]rune(text)) < 200 {
		writeErr(w, 400, fmt.Errorf("小说正文过短(%d 字,预处理后)", len([]rune(text))))
		return
	}
	// 人物外形锚表(技能侧写在 `<书名>/设定集/` 下):**交给拆集,由它拼进每一集的 source**。
	//
	// 为什么是"交给拆集"而不是"拼在这里的 text 上"(2026-09-27 真机实证后的修正):
	// 这份 `text` 只喂**拆集**(写 brief),而每集项目的 source.txt 是
	// `《系列》…【本集剧情】brief ＋【本集原文】` —— 锚表拼在 text 上根本到不了
	// 脚本阶段的 LLM,角色卡照旧现编(实测第 01 集把 19 岁/低束发/灰白短打编成
	// 24 岁/高髻/灰褐色短打)。详见 episodeSourceWithSheet 的说明。
	// 同时不再拼进 text:拆集要的是干净正文(章界识别 + 每集 brief 的上下文),
	// 锚表进每一集输入前先"读完 + 限长",限长说明**如实播报**(不许静默少喂几个角色)。
	sheet, sheetCut := readCharacterSheet(in.Path)
	if sheet != "" {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
			Message: fmt.Sprintf("🧬 已读取人物外形锚表(%d 字):将拼进每一集的分镜输入,角色外形以它为准", len([]rune(sheet)))})
	}
	if sheetCut != "" {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", Message: sheetCut})
	}
	// 小说自带的"视频风格"字段：**扫全**（立项.json 的 art_style / 渲染风格串 / 画风 + 设定集里的
	// 视觉风格卡），而不是只读 art_style —— 2026-10-09 用户：「另外导入的小说也需要解析是否存在
	// 视频风格相关字段」。真机那本书四个字段都在，而平台原来只看 art_style 一个。
	scan := novelStyleScan{}
	if fi.IsDir() {
		scan = scanNovelStyle(in.Path)
	}
	artStyle := scan.Style
	switch {
	case scan.Found():
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
			Message: fmt.Sprintf("🎨 已从这本书里解析到视频风格字段(%s):%s", scan.Field, clampRunes(scan.Style, 80))})
	default:
		// 扫不到就**如实说**（用户才知道"导入时该自己选一个"，而不是以为平台取了书的风格）
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
			Message: "🎨 这本书里没有解析到视频风格字段(立项.json 的 art_style/渲染风格串/画风 与 设定集/视觉风格卡-*.md 都没有):" +
				"将在「视频制作」里按你选的风格渲染;不选则由分镜会话自定"})
	}
	if scan.Card != "" {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
			Message: fmt.Sprintf("🎨 另发现风格卡 设定集/%s(%d 字):本书的画面口径以它为准", scan.Card, scan.Chars)})
	}

	// 【2026-10-09 用户：「小说库导入视频制作时，用户需要选择对应的渲染风格，目前所有视频渲染风格
	// 都是一样的」】这一步原来**没有选择入口**：风格写死取立项 art_style。现在用户在「视频制作」
	// 对话框里勾的预设/附加要求**优先**，而且是**确定性**的 —— 见 novels_style.go：
	// 拼成权威串 → 写进每集输入的平台自有行「全局风格:」→ director.enforceStyleLock 逐镜覆写
	// plan.Style。也就是说"选了风格"不会只当提示词里的一句话交给模型自觉。
	style := resolveImportStyle(artStyle, in.Styles, in.StyleHint)
	if len(style.Unknown) > 0 {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: fmt.Sprintf("⚠ 你选的渲染风格里有平台不认的项(%s):已忽略,其余照选",
				strings.Join(style.Unknown, "、"))})
	}
	if len(style.Conflicts) > 0 {
		// 介质互相矛盾的组合：**仍然照用户选的做**（用户拍板优先），但必须如实点名 ——
		// 平台自己的规则写着风格串不许含互斥词组，"负向禁令与正向词打架"是真正的毒药。
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: fmt.Sprintf("⚠ 你选的渲染风格**介质互相矛盾**(%s)：一个画面不可能同时是这几样，"+
				"画面会被拉成四不像（如塑料 CG 脸）。平台照你选的做，建议只留一项后重跑。",
				strings.Join(style.Conflicts, "；"))})
	}
	// 查重:同书系列/同名项目已存在
	if s.novelProduced(name) {
		// 已有系列:放行进入补缺模式——重拆按集号去重,已有分集不动,只建缺失;
		// 中间缺章由 autoGapFillTrim 自动补(删插入点后分集+尾部重拆续接)
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
			Message: "《" + name + "》已有系列,进入补缺模式:已有分集保持不动,仅补建缺失的集"})
	}
	// 后台异步:拆集→建项目→封面→自动批量。立即返回,弹窗不等待 LLM(2-6 分钟);
	// 进度看全局日志(30 秒心跳),分集项目创建后首页实时出现。
	cover := coverSrc
	maxEp := in.Max
	go func() {
		// 注册键必须与拆集内部键一致:splitNovelToProjects 会做 safeSeries 规整
		// (全角逗号等→「·」),否则失败态记录会挂在另一个键上、界面上查不到。
		name = safeSeries(name)
		// 不再注册「AI 拆集中」运行态占位:一章一档下建集是秒级的(实测 3 秒建 64 集),
		// 占位卡只会一闪而过甚至挡住真正的书卡(2026-09-12 用户定调「AI 拆集中的占位
		// 不需要了」)。失败态仍会记录并可见。
		// 可取消上下文:停止按钮可中断 LLM 拆集调用(2026-09-11 用户定调)
		splitCtx, splitCancel := context.WithCancel(context.Background())
		defer splitCancel()
		// 流水线化(2026-09-10 用户定调):首段拆集就绪即启动批量产线,
		// LLM 静默持续分段解析补集,产线消费到哪追到哪,日志双流同步。
		var started bool
		var seriesFinal string
		var prodP *novelProducer
		applyCover := func(newEps []string) {
			if cover == "" {
				return
			}
			ext := strings.ToLower(filepath.Ext(cover))
			for _, ep := range newEps {
				dst := filepath.Join(s.store.Dir(ep), "assets", "cover"+ext)
				if b, e := os.ReadFile(cover); e == nil {
					_ = os.MkdirAll(filepath.Dir(dst), 0o755) // 目录已存在是常态，失败会在下面 WriteFile 上暴露
					// 【2026-09-29 审计】原来是 `_ = os.WriteFile(...)` —— 封面没拷进去时
					// 该集在界面上就是"没有封面"，而拆集流程照报成功（用户以为设了封面）。
					if werr := os.WriteFile(dst, b, 0o644); werr != nil {
						s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn", ProjectID: ep,
							Message: fmt.Sprintf("⚠ 分集《%s》的封面没拷进去(%v)——该集在列表里会显示成没有封面", ep, werr)})
					}
				}
			}
		}
		_, _, err := s.splitNovelToProjects(splitCtx, name, text, maxEp, style, sheet, func(series string, newEps []string) {
			seriesFinal = series
			if prodP == nil {
				prodP = prodStart(series, splitCancel) // 注册生产者(含 cancel:停止按钮可中断拆集)
			}
			applyCover(newEps)
			if !started {
				started = true
				if s.startProd(series, newEps) {
					s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
						Message: "🚀 首段拆集就绪,批量产线提前启动(拆集继续后台补集,产线实时消费)"})
				} else {
					s.batchAppend(series, newEps) // 已有产线在跑:直接入队
				}
			} else {
				s.batchAppend(series, newEps)
			}
		})
		if prodP != nil {
			prodDone(seriesFinal, prodP) // 拆集结束:产线跑完已知集自然收尾
		}
		if err != nil {
			s.bus.Publish(pipeline.Event{Phase: "novel", Level: "error",
				Message: "《" + name + "》拆集失败:" + err.Error()})
			jobSet(name, "failed", 0, "拆集失败:"+err.Error())
			return
		}
	}()
	writeJSON(w, 200, map[string]any{"ok": true, "submitted": true,
		"message": "《" + name + "》已提交:按章节文件直接建集(秒级,一章一集)→自动开始批量产线;各集剧情 brief 后台并行补写,全程日志可观测"})
}

// ---------- 流式拆集生产者注册表(产线据此判断还有没有新分集要来) ----------
var novelProducers = struct {
	sync.Mutex
	m map[string]*novelProducer // series → 生产者(done+cancel)
}{m: map[string]*novelProducer{}}

// novelProducer 流式拆集生产者:done=结束信号,cancel=取消拆集 LLM 调用。
type novelProducer struct {
	done   chan struct{}
	cancel context.CancelFunc
}

func prodStart(series string, cancel context.CancelFunc) *novelProducer {
	p := &novelProducer{done: make(chan struct{}), cancel: cancel}
	novelProducers.Lock()
	// 同一个 series 再产一次时**先给旧条目发撤销**（2026-09-29 运行时审计 P2-6）：
	// 旧条目若还在，它的 prodDone 会按 series 无条件删掉**新**条目（见下），
	// 让 `prodAlive` 假 false → 产线判"拆集结束、自然收尾"提前收尾，
	// 而第二批集还在后台生成 → 新集永远不会被消费。先撤销旧的，把它挤出去。
	if old, ok := novelProducers.m[series]; ok && old != p {
		select {
		case <-old.done:
		default:
			close(old.done)
		}
	}
	novelProducers.m[series] = p
	novelProducers.Unlock()
	return p
}

// prodDone 摘掉生产者登记。
//
// **必须校验身份**（2026-09-29 运行时审计 P2-6）：同一本书被点两次（双击 / 两个入口 /
// 重试）时，第一个生产者的 `prodDone` 会把**第二个**的条目删掉 ——
// 与 `Coordinator.finish` 是同一个病（那处已改）。
// `delete(m, series)` 无条件执行时，`prodAlive` 假 false 会让批量产线
// **提前收尾**，第二批集永远不被消费；反过来条目残留则让产线 4 秒一轮静默空转
// （`waitAnnounced` 让日志只报一次，连线索都没有），只能靠手动停止或下次启动回收。
func prodDone(series string, p *novelProducer) {
	novelProducers.Lock()
	if cur, ok := novelProducers.m[series]; ok && cur == p {
		delete(novelProducers.m, series)
	}
	novelProducers.Unlock()
	select {
	case <-p.done:
	default:
		close(p.done)
	}
}
func prodAlive(series string) bool {
	novelProducers.Lock()
	defer novelProducers.Unlock()
	_, ok := novelProducers.m[series]
	return ok
}

// prodCancelAll 取消所有拆集生产者(停止按钮调用,终止 LLM 拆集)。
func prodCancelAll() {
	novelProducers.Lock()
	defer novelProducers.Unlock()
	for _, p := range novelProducers.m {
		if p.cancel != nil {
			p.cancel()
		}
	}
}

// batchAppend 向运行中的批量产线追加新分集(流式拆集逐段推送)。
//
// **按集名去重**(2026-09-12 用户实证「那来的 128 集」):入队不去重时,同一批集被
// 通知两次就会排两遍(64 → 128),产线会把整本书跑两轮 —— 白烧几个小时 GPU。
// 去重保序(首次出现者保留),被丢掉的重复项如实播报,便于查出调用方问题。
func (s *Server) batchAppend(prefix string, newEps []string) {
	batch.mu.Lock()
	defer batch.mu.Unlock()
	if !batch.running || batch.prefix != prefix {
		return
	}
	before := len(batch.projects)
	batch.projects, _ = dedupEps(append(batch.projects, newEps...))
	added := len(batch.projects) - before
	dropped := len(newEps) - added
	if dropped > 0 {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
			Message: fmt.Sprintf("⚠ 批量产线入队去重:丢弃 %d 个重复入队的集(当前共 %d 集)", dropped, len(batch.projects))})
	}
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "info",
		Message: fmt.Sprintf("📥 拆集新段就绪 +%d 集,已加入批量产线队列(当前共 %d 集)", added, len(batch.projects))})
}

// dedupEps 按集名去重并保序;返回(去重后列表, 丢弃个数)。
func dedupEps(in []string) ([]string, int) {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	dropped := 0
	for _, e := range in {
		if seen[e] {
			dropped++
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out, dropped
}

// ---------- 进行中拆集任务注册表(首页占位卡数据源) ----------

// characterSheetMaxRunes 外形锚表注入上限(字)。
//
// 【2026-10-07 真机实测改口径】原来是 2500 —— 那个值把《名册之外，八万零一号》的表
// (实测 **6202 字 / 15 个角色**)砍掉约六成：砍点落在「断桅」条目中间(`- 身形：瘦，背驼，走路`
// 之后直接接`【本集剧情】`)，其后的苔生/拾叁/回波/关棠/剥线刀/毕申/白手套/议长 8 条
// **从未进过任何一集的分镜输入** —— 这些人的定妆照只能靠模型现编脸(与「非主角随机脸」同源)。
// 锚表是外形唯一权威，它进不全 = 硬约束对这些人根本不存在，所以上限按"真实表量级"放宽；
// 仍然保留上限，是为了防"有人把整本设定集塞成锚表"把 token 烧光(设定集实测 6 万字)。
// 超上限时**不许腰斩某个人**：退到最近的小节起点整条丢，并如实点名丢了谁(见 trimCharacterSheet)。
const characterSheetMaxRunes = 12000

// characterSheetHeader 注入时的抬头:明确它是硬约束,而不是"可供参考的背景资料"。
const characterSheetHeader = "【人物外形锚表 · 硬约束】以下每个角色的外形必须逐项照抄" +
	"(脸型 / 眉眼 / 鼻唇 / 肤色 / 独特记号 / 发型三要素 / 首套服装),不得改写、不得美化、不得把两个人的特征合并;" +
	"正文里没有写到的外貌一律以此表为准:\n"

// episodeSourceWithSheet 把外形锚表拼进**一集的 source.txt**(幂等;空表原样返回)。
//
// 【为什么必须拼在"每集"而不是"拆集输入"上】(2026-09-27 真机实证)
// 锚表最早接在 `handleNovelProduce` 的 `text` 上 —— 那份 text 只喂给**拆集**(写 brief),
// 而每集项目的 `source.txt` 是 `《系列》…【本集剧情】brief ＋【本集原文】`(见 createSeg),
// **锚表不在里面**。于是脚本阶段的 LLM 看到的仍然是一份没有外貌信息的原文,
// 角色卡照旧现编:实测《求你们骂我》第 01 集的项目里,沈砚被判成
// 「二十四岁 / 头顶高髻 / 灰褐色短褐 / 左眉尾疤」,而锚表写的是
// 「十九岁 / 低束发、中短及下颌 / 洗得发白的灰白粗布短打 / 左手腕墨色纹路 + 右前臂新刀口」——
// **定妆照按"现编的那个人"出图,全片的身份锚从一开始就是错的**。
//
// 所以锚表必须落在**产线的每一集**上:脚本阶段读什么,锚表就得在什么里面。
// 幂等(带过抬头就不再拼接)是为了"补缺模式重跑""已有集回填"都不会叠两份。
func episodeSourceWithSheet(src, sheet string) string {
	sheet = strings.TrimSpace(sheet)
	if sheet == "" || src == "" || strings.Contains(src, characterSheetHeader) {
		return src
	}
	// 放在标题/全局风格之后、【前情提要】与【本集剧情】之前:硬约束要早于剧情出现,
	// 但又不能顶掉"这是什么剧的第几集"这条抬头(它是分集上下文的第一句)。
	if i := strings.Index(src, "\n\n"); i > 0 {
		return src[:i+2] + characterSheetHeader + sheet + "\n\n" + src[i+2:]
	}
	return characterSheetHeader + sheet + "\n\n" + src
}

// readCharacterSheet 读 <书名>/设定集/ 下的「人物外形锚表」,**只返回表体**。
//
// 第二个返回值是**限长说明**(没截断时为空串,由调用方如实播报):锚表超过
// characterSheetMaxRunes 时不再腰斩某个人,而是退到最近的小节起点、把后面的人**整条**丢掉,
// 并点名丢了谁 —— 因为"半个人的外形"比"没这个人"更坏:模型会照半条描述现编另外半张脸。
//
// 抬头由 episodeSourceWithSheet 统一加(唯一注入点)。这里原来自己拼了一遍抬头,
// 而它的唯一消费方 episodeSourceWithSheet 的幂等判据正是"src 里有没有抬头" ——
// 于是每集的 source.txt 里都印着**两份抬头**(实测《名册之外，八万零一号》第 01 集),
// 白烧 token 之外,还把"硬约束"这件事说成了两遍、看起来像拼接坏了。
//
// 找不到就返回空串(不报错):绝大多数存量书还没有这张表,没有它走原来的路 ——
// 由 LLM 从正文提炼(质量会差,但不该让整个生产流程失败)。
func readCharacterSheet(dir string) (string, string) {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return "", ""
	}
	var cands []string
	for _, pat := range []string{
		filepath.Join(dir, "设定集", "*人物外形*.md"),
		filepath.Join(dir, "设定*", "*人物外形*"),
	} {
		if m, _ := filepath.Glob(pat); len(m) > 0 {
			cands = m
			break
		}
	}
	if len(cands) == 0 {
		return "", ""
	}
	sort.Strings(cands)
	b, err := os.ReadFile(cands[0])
	if err != nil {
		return "", ""
	}
	s := strings.TrimSpace(string(NormalizeNovelText(b)))
	if s == "" {
		return "", ""
	}
	return trimCharacterSheet(s)
}

// sheetEntryHead 锚表里一个小节的标题行:`霜见（定位：第二主角／通信手。…）`。
//
// 判据是"行首 ≤12 个字 + （定位：/（定位:" —— 与 project.reCharHead 同源。`### ` 已在
// NormalizeNovelText 里剥掉了(见 textnormalize.go 的 stripMarkdown)。这里只做**边界定位**,
// 谁是角色、写到哪一节仍归 project.ParseCharAnchor(唯一解析入口)。
var sheetEntryHead = regexp.MustCompile(`(?m)^[^（\n]{1,12}（定位[:：]`)

// trimCharacterSheet 把表体限到 characterSheetMaxRunes 字以内,并给出**如实的截断说明**。
//
// 优先级:① 退到最近的小节起点(整条丢,不腰斩人)② 没有小节时退到最后一个空行
// ③ 都没有才按字符硬切(中文一个字 3 字节,按 rune 切不会切坏 UTF-8)。
func trimCharacterSheet(s string) (string, string) {
	rs := []rune(s)
	if len(rs) <= characterSheetMaxRunes {
		return s, ""
	}
	head := string(rs[:characterSheetMaxRunes])
	cut := -1
	for _, m := range sheetEntryHead.FindAllStringIndex(head, -1) {
		if m[0] > 0 {
			cut = m[0] // 最后一个**装得下**的小节起点
		}
	}
	if cut <= 0 {
		if i := strings.LastIndex(head, "\n\n"); i > 0 {
			cut = i + 1
		}
	}
	if cut <= 0 {
		return head, fmt.Sprintf("⚠ 外形锚表超上限(%d 字 > %d 字)且无可切边界:已按字符硬切,末尾约 %d 字未进每一集的输入",
			len(rs), characterSheetMaxRunes, len(rs)-characterSheetMaxRunes)
	}
	kept := strings.TrimRight(head[:cut], " \t\r\n")
	dropped := s[len(kept):]
	rest := fmt.Sprintf("末尾 %d 字未进每一集的输入", len([]rune(dropped)))
	if names := sheetEntryNames(dropped); len(names) > 0 {
		rest = fmt.Sprintf("末尾 %d 字未进每一集的输入,被整条略过的角色:%s", len([]rune(dropped)), strings.Join(names, "、"))
	}
	return kept, fmt.Sprintf("⚠ 外形锚表超上限(%d 字 > %d 字):%s —— 锚表是外形唯一权威,这些人这一集只能靠模型现编外形(脸会漂);"+
		"请把锚表压到 %d 字以内,或按角色拆成多份表", len(rs), characterSheetMaxRunes, rest, characterSheetMaxRunes)
}

// sheetEntryNames 列出这段文本里的小节名(用于点名"谁被截掉了")。
func sheetEntryNames(s string) []string {
	var out []string
	for _, m := range sheetEntryHead.FindAllString(s, -1) {
		n := strings.TrimSpace(m)
		for _, suf := range []string{"（定位：", "（定位:"} {
			n = strings.TrimSuffix(n, suf)
		}
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}
