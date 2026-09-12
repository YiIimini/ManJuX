// novels.go 小说库:扫描设置目录中的小说(书=子文件夹,优先「全本」,次选合并「正文」
// 分卷章节;封面取「封面」文件夹内图片),支持弹窗阅读详情与一键视频创作。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/YiIimini/ManJuX/internal/pipeline"
)

func (s *Server) handleNovelProduce(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
		Max  int    `json:"max_episodes"`
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
		// 保证章界(切块/CHAPTERS/【本集原文】附着全依赖章头)
		readMerged := func() (string, int) {
			var paths []string
			_ = filepath.WalkDir(in.Path, func(p string, e os.DirEntry, err error) error {
				if err != nil || e.IsDir() || !hasTxtExt(p) {
					return nil
				}
				rel := strings.ToLower(p)
				if strings.Contains(rel, "素材") || strings.Contains(rel, "设定") || strings.Contains(rel, "docs") || strings.Contains(rel, "封面") || strings.Contains(rel, "全本") {
					return nil
				}
				paths = append(paths, p)
				return nil
			})
			sort.Strings(paths)
			var sb strings.Builder
			for _, p := range paths {
				b, _ := os.ReadFile(p)
				sb.WriteString(ensureChapterHead(filepath.Base(p), string(b)))
				sb.WriteString("\n\n")
			}
			return sb.String(), len(paths)
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
	// 读取立项.json 的 art_style 作为视频渲染风格(2026-09-11 用户定调)
	artStyle := ""
	if fi.IsDir() {
		if raw, e := os.ReadFile(filepath.Join(in.Path, "立项.json")); e == nil {
			var proj json.RawMessage
			var m map[string]any
			if json.Unmarshal(raw, &proj) == nil && json.Unmarshal(raw, &m) == nil {
				if v, ok := m["art_style"].(string); ok && strings.TrimSpace(v) != "" {
					artStyle = strings.TrimSpace(v)
				}
			}
		}
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
					_ = os.MkdirAll(filepath.Dir(dst), 0o755)
					_ = os.WriteFile(dst, b, 0o644)
				}
			}
		}
		_, _, err := s.splitNovelToProjects(splitCtx, name, text, maxEp, artStyle, func(series string, newEps []string) {
			seriesFinal = series
			if prodP == nil {
				prodP = prodStart(series, splitCancel) // 注册生产者(含 cancel:停止按钮可中断拆集)
			}
			applyCover(newEps)
			if !started {
				started = true
				if s.startBatch(series, newEps) {
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
	novelProducers.m[series] = p
	novelProducers.Unlock()
	return p
}
func prodDone(series string, p *novelProducer) {
	novelProducers.Lock()
	delete(novelProducers.m, series)
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
