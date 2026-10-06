// Package h3api 实现 MiniMax H3 云端 API 引擎。
// 协议(官方文档):POST /v2/video_generation 创建任务(异步),
// GET /v2/query/video_generation/{task_id} 轮询(建议 10s),
// 成功后 task.content.url 下载成片。
package h3api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/textutil"
)

// defaultAPITimeout 云端任务轮询兜底上限(作业未指定 Timeout 时使用)。
const defaultAPITimeout = 20 * time.Minute

// cloudFramesPerSecond 云端时长(整数秒)换算成本地帧数口径用的帧率。
//
// 与提交侧同一个 24(见 Render 里的 wantSec := job.Length / 24,以及管线按"时长×24"
// 组装 Job.Length):engine.Result.ActualFrames 要和本地引擎的帧数**同一把尺子**,
// 否则"云端 5 秒 vs 本地 107 帧"这种对比又要人肉换算。
const cloudFramesPerSecond = 24

// Engine 云端 H3 引擎。
type Engine struct {
	cfg func() config.MiniMaxSettings
	// pollEvery 轮询间隔;0 = 官方建议的 10s。测试把它调小,免得一条断言等 10 秒。
	pollEvery time.Duration
}

// New 创建引擎。
func New(cfg func() config.MiniMaxSettings) *Engine { return &Engine{cfg: cfg} }

// apiError 云端 HTTP 错误(可判别)。
//
// 为什么需要一个类型(2026-09-24 审计 P0-6):轮询原先对**任何**非 200 都 `continue`
// ("网络抖动容忍"),401/403 也照吞 —— 一个无效 Key 会白等 20 分钟才以"超时"收场,
// 而管线拿这个哨兵判定"显存挤压"、**再跑一轮**,于是重新 create 一个云端任务:
// 一次鉴权失败 = 两个计费任务 + 约 40 分钟。
//
// 【云端一律不包装 engine.ErrRenderTimeout(乙8,2026-09-26 待决项)】
// 那个哨兵的含义是"可原地重试",而管线对它的处置是"释放显存后重渲一次"
// (pipeline/stages_render.go 的重试分支)。这条策略本地成立(本地算力,重试不花钱),
// 在云端却等于**再提交一个计费任务**,而"释放显存"这个理由在云端根本不存在
// (CapsOf("api") 是零值,caps.VRAM=false,重试前什么也没释放)。
// 所以云端失败路径**一条都不包**该哨兵:失败即停、如实报错,把"要不要再花一次额度"
// 的决定权交回用户(见 noAutoRetryNote)。
type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Msg)
}

// fatal 是否属于"重试也没用"的错误(鉴权/参数/模型名):必须立刻失败。
func (e *apiError) fatal() bool {
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest, http.StatusNotFound:
		return true
	}
	return false
}

// awaitTask 提交之后的**统一等待逻辑**:轮询 → 成败判定,返回成片 url。
// Render 与 Regenerate2K 共用(两处曾各自踩同一组坑,见 apiError 的说明)。
func (e *Engine) awaitTask(ctx context.Context, c config.MiniMaxSettings, taskID string, timeout time.Duration, onProgress func(pct int, note string)) (string, error) {
	if timeout <= 0 {
		timeout = defaultAPITimeout
	}
	poll := e.pollEvery
	if poll <= 0 {
		poll = 10 * time.Second
	}
	// 到点立刻返回,不必等下一次 tick:旧实现只在 tick 之后检查 deadline,
	// 超时最多晚 10 秒才被发现(测试里更明显)。
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	tick := time.NewTicker(poll)
	defer tick.Stop()

	consecutive := 0
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			// 不包装 engine.ErrRenderTimeout(见 apiError 的说明):云端重试 = 再花一次额度。
			return "", fmt.Errorf("云端任务 %s 超时(等待上限 %v;已请求取消远端任务,避免继续空跑计费);%s",
				taskID, timeout, noAutoRetryNote)
		case <-tick.C:
		}
		st, url, err := e.query(ctx, c, taskID)
		if err != nil {
			var ae *apiError
			if errors.As(err, &ae) && ae.fatal() {
				// 鉴权/参数类错误**不包装**成 ErrRenderTimeout:管线据此判断"要不要
				// 释放显存重试一轮",把 401 报成超时会直接导致第二个计费任务。
				return "", fmt.Errorf("云端拒绝请求(%w):请检查 MiniMax API Key、模型名与账号额度", ae)
			}
			consecutive++
			if consecutive >= 3 {
				return "", fmt.Errorf("云端查询连续失败 %d 次(任务 %s):%w", consecutive, taskID, err)
			}
			if onProgress != nil {
				onProgress(40, fmt.Sprintf("云端查询失败(%d/3),继续重试:%v", consecutive, err))
			}
			continue
		}
		consecutive = 0
		switch st {
		case "succeed", "succeeded", "success":
			if url == "" {
				return "", fmt.Errorf("任务成功但缺少成片 url: %s", taskID)
			}
			return url, nil
		case "failed", "cancelled":
			return "", fmt.Errorf("云端任务失败(%s): %s", st, taskID)
		default: // queued / running / processing
			if onProgress != nil {
				onProgress(40, "云端生成中("+st+")")
			}
		}
	}
}

// noAutoRetryNote 云端失败的统一播报文案(同时进错误文本与进度事件)。
//
// 为什么必须写出来:用户看到一次云端失败,第一反应是"它是不是又偷偷花了一次额度"。
// 这条链路里重试的代价是钱,所以"没有自动重试"和错误本身一样是需要告知的事实
// (乙8 选项 A:不自动重提,失败即停、如实报错)。
const noAutoRetryNote = "⚠ 云端失败**不会自动重提**(重提 = 再消耗一次额度):本次没有重新提交,请确认原因后手动重渲本镜"

// noteNoAutoRetry 把"未自动重试"写进进度事件流(进 UI 的运行日志)。
//
// 百分比给 0:失败不是进度,这里只是借事件通道播报,别让 0 被读成"卡在起点"。
func noteNoAutoRetry(onProgress func(int, string)) {
	if onProgress != nil {
		onProgress(0, noAutoRetryNote)
	}
}

// Name 引擎名。
func (e *Engine) Name() string { return "api" }

// Available 检查 API Key 已配置(轻量,不真调 API)。
func (e *Engine) Available(ctx context.Context) error {
	c := e.cfg()
	if c.APIKey == "" {
		return fmt.Errorf("MiniMax API Key 未配置:请在工作台「设置」中填写")
	}
	return nil
}

// Render 提交任务 → 轮询 → 下载。
// ── interface.Engine 的通用可选能力:云端引擎没有本地显存/队列概念 ──

// FreeVRAM 云端无本地显存可释放。
func (e *Engine) FreeVRAM(ctx context.Context) error { return nil }

// ClearQueue 云端任务由服务端排队,本地无队列可清。
func (e *Engine) ClearQueue(ctx context.Context) {}

// Interrupt 云端任务的取消在 Render 内部尽力完成,此处无需额外动作。
func (e *Engine) Interrupt(ctx context.Context) {}

// GenerateImage 云端引擎不提供本地图像生成:定妆照走本地 Krea-2。
func (e *Engine) GenerateImage(ctx context.Context, prompt, neg string, seed int64, w, h int, outPath string) (string, error) {
	return "", fmt.Errorf("云端引擎不支持本地图像生成,请改用本地 ComfyUI 引擎")
}

// Upscale 2× 放大是本地后处理链路(依赖 ComfyUI 的放大节点与权重),
// 云端引擎不实现 —— 与 GenerateImage 同理,给出可执行的指引而不是静默失败。
func (e *Engine) Upscale(ctx context.Context, srcVideoPath string, opt engine.UpscaleOptions, length int, fps float64, outName, outPath string, timeout time.Duration) (string, error) {
	return "", fmt.Errorf("2× 放大是本地后处理,请改用本地 ComfyUI 引擎")
}

func (e *Engine) Render(ctx context.Context, job engine.Job) (*engine.Result, error) {
	c := e.cfg()
	if err := e.Available(ctx); err != nil {
		return nil, err
	}
	_ = os.MkdirAll(job.OutputDir, 0o755)
	// 注:产物复用/断点续跑由管线层按输入指纹(manifest)权威判定,
	// 引擎层不做本地缓存,避免绕过指纹校验复用旧文件
	final := filepath.Join(job.OutputDir, job.OutputName+".mp4")

	// seed:云端**不支持**(H3 的 /v2/video_generation 请求体只有 model/content/
	// duration/resolution/ratio —— 官方文档 API reference,社区实测亦写明
	// "There is no seed parameter. Generations are not reproducible",
	// 见 awesome-minimax-h3 的 minimax-video/references/api-reference.md)。
	// 而"固定种子"与单镜 seed 是平台上真会生效的参数,切到云端后静默失效、零反馈
	// (审计 M32)。能力提示清单(caps_params.go 的 unsupportedParamNote)只覆盖
	// Seam/CondCache,所以这里至少把"这一镜的 seed 没生效"播报出去。
	if job.Seed != 0 && job.OnProgress != nil {
		job.OnProgress(2, fmt.Sprintf(
			"⚠ 云端引擎不支持 seed(请求体无该字段,产物不可复现):本镜的 seed=%d 不生效 —— 要按 seed 复现请改用本地引擎",
			job.Seed))
	}

	content := []map[string]any{
		{"type": "text", "text": job.Prompt},
	}
	for _, ref := range job.RefImages {
		dataURI, err := fileToDataURI(ref.Path)
		if err != nil {
			return nil, fmt.Errorf("参考图读取失败(%s): %w", filepath.Base(ref.Path), err)
		}
		role := "reference_image"
		if ref.Kind == "first_frame" {
			role = "first_frame"
		}
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": dataURI},
			"role":      role,
		})
	}
	// 时长:云端只吃整数秒,而它的合法区间比计划侧(PlanMinShotSeconds=4)更窄 ——
	// clampDuration 会把一条 4 秒镜**静默**抬成 5 秒。这不是小事:同一条 plan 走云端
	// 每一条 4 秒镜都变长(整集比计划长),走本地却是 AlignLength 后的 4.46 秒,
	// 两条路的产物时长不一致,而日志/事件里一个字都没有(审计:云端时长静默抬升)。
	//
	// 为什么不去掉钳制直接原样提交:官方文档把 duration 写成一小组取值
	// (https://platform.minimax.io/docs/api-reference/video-generation-v2-create ,
	// 页面为客户端渲染的取值表,无法逐字复核),社区整理的 H3 API 参考记官方枚举为
	// 5/10/15、同时实测"任意整数都被接受"(https://github.com/joeVenner/awesome-minimax-h3
	// 的 minimax-video/references/api-reference.md)—— 两处口径不一致,而猜错的代价是
	// 一次 400 直接让整镜失败(400 属 fatal,不会重试,也不计费回滚)。
	// 所以保留保守区间,但把"被改写了"这件事如实播报出去,由用户决定是否改用本地引擎。
	wantSec := job.Length / 24
	sec, durationAdjusted := submitDuration(job.Length)
	if durationAdjusted && job.OnProgress != nil {
		if wantSec <= 0 {
			job.OnProgress(2, fmt.Sprintf(
				"⚠ 本镜未给出渲染帧数(Length=%d),云端按最短 %d 秒提交:成片时长不受镜头计划约束", job.Length, sec))
		} else {
			job.OnProgress(2, fmt.Sprintf(
				"⚠ 云端时长受限:本镜计划 %d 秒(%d 帧),云端按 %d 秒生成 —— 与本地引擎的帧数对齐口径不同,两条路产物时长会不一致",
				wantSec, job.Length, sec))
		}
	}
	body := map[string]any{
		"model":      c.Model,
		"content":    content,
		"duration":   sec,
		"resolution": orDefault(c.Resolution, "768P"),
		// ratio 始终显式传:ref2va 下云端不按参考图自动取竖横,缺省会落 16:9
		"ratio": ratioFor(job.Width, job.Height),
	}

	taskID, err := e.create(ctx, c, body)
	if err != nil {
		noteNoAutoRetry(job.OnProgress)
		return nil, err
	}
	if job.OnProgress != nil {
		job.OnProgress(3, "云端任务已提交 "+taskID)
	}
	// 提交之后**任何**一条退出路径都必须让远端停下来:否则超时/下载失败时云端
	// 继续跑并计费(旧实现只在 ctx.Done() 分支取消,与 docs/架构.md 宣称的
	// "取消防空跑计费"相反 —— 审计 P0-6)。
	settled := false
	defer func() {
		if !settled {
			e.cancel(c, taskID)
		}
	}()

	url, err := e.awaitTask(ctx, c, taskID, job.Timeout, job.OnProgress)
	if err != nil {
		noteNoAutoRetry(job.OnProgress)
		return nil, err
	}
	if job.OnProgress != nil {
		job.OnProgress(90, "下载成片")
	}
	var dlErr error
	for attempt := 0; attempt < 2; attempt++ {
		if dlErr = download(ctx, url, final); dlErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	if dlErr != nil {
		noteNoAutoRetry(job.OnProgress)
		return nil, fmt.Errorf("下载成片失败(已重试): %w", dlErr)
	}
	settled = true // 成片已落到本地,远端任务无需再取消
	return &engine.Result{
		VideoPath: final, TaskID: taskID, Engine: e.Name(),
		// 实际时长口径回传(乙6 选项 C:两条路产物长度不同但都如实):
		// 云端按 sec 秒出片,换算成与本地同一把尺子(24fps)的帧数;
		// DurationAdjusted 就是提交时算出的"时长被云端区间改写"。
		ActualFrames:     sec * cloudFramesPerSecond,
		DurationAdjusted: durationAdjusted,
	}, nil
}

// blobRef 一个大字段：值是要当 data URI 发出去的那个**文件**，以及它在 body 里的路径。
//
// path 用点分表示法：数组下标写 `[N]`，`[]` 表示"该数组的每一个元素"。
// 实盘形态是
//
//	content[].video_url.url      （再生成：源视频，数组里那一项）
//	content[1].image_url.url     （参考图：数组第 1 项之后依次是各张参考图）
//
// 为什么要有 `[N]`：`content` 是一个**有序切片**（第 0 项是文本、后面依次是参考图
// 与源视频），每一张参考图是**不同的元素**，用 `[]` 通配会把同一个占位串配到多处。
type blobRef struct {
	Path string // body 里的位置
	File string // 本地文件
}

// dataURIPlaceholder 生成一个**在请求体里唯一**的占位串。
//
// 思路（2026-09-29 性能审计 #7 的实现）：不手写 JSON 序列化器（那要自己保证转义、
// 嵌套、数组都对 —— 风险远大于收益），而是
//
//	① 把 body 里那几个大字段的值先换成占位串；
//	② `json.Marshal(body)` —— 这一次的产物只有几十 KB（大字段的位置上是个占位串）；
//	③ 按占位串把 raw 切开，逐块写出，**中间那段用流式 base64 写文件**。
//
// 转义与结构全由标准库保证，而降下来的正是那 67MB base64 + 67MB 拼接 + 70MB marshal。
//
// ⚠️ **占位串必须是 json.Marshal 不会改写的字符**：第一版用了 `\x00`，
// 而 JSON 会把它转义成 `\u0000` —— 于是 `bytes.Index` 在 raw 里**找不到**那个串，
// 请求体被原样发出去（带着字面占位串）。这个坑是被 `TestRegenerate2KSharesPollingPolicy`
// 咬出来的（它断言"403 之后要取消远端"，而实际连创建阶段就失败了）。
func dataURIPlaceholder(i int) string { return fmt.Sprintf("@@MJBLOB%d@@", i) }

// setBlobPlaceholder 把 path 指向的值替换成占位串，并返回"值原本是什么类型"以便还原。
//
// 只支持一层 `[]`（该字段是数组的每个元素）—— 本文件两处用法都是这个形状，
// 更深的嵌套若将来出现，这里会返回错误而不是**静默不替换**（那会让请求体里
// 出现一个字面占位串，服务端报一个看不懂的错）。
func setBlobPlaceholder(body map[string]any, ref blobRef, val string) error {
	segs := strings.Split(ref.Path, ".")
	cur := any(body)
	for i, seg := range segs {
		last := i == len(segs)-1
		// 解析 `key` / `key[]` / `key[N]`
		key, idx, hasIdx, all := parseSeg(seg)
		if all {
			return fmt.Errorf("blob 路径 %s 用了 [] 通配 —— 数组里的每一项是不同的字段，"+
				"请写成 content[0] / content[1] 这样的下标（见 blobRef 的说明）", ref.Path)
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return fmt.Errorf("blob 路径 %s 的第 %d 段不是对象", ref.Path, i+1)
		}
		v, ok := m[key]
		if !ok {
			return fmt.Errorf("blob 路径 %s 在请求体里不存在（字段 %s）", ref.Path, key)
		}
		if hasIdx {
			list, ok := v.([]map[string]any)
			if !ok {
				if anyList, ok2 := v.([]any); ok2 {
					if idx < 0 || idx >= len(anyList) {
						return fmt.Errorf("blob 路径 %s 的下标 %d 越界（数组长度 %d）", ref.Path, idx, len(anyList))
					}
					if last {
						return fmt.Errorf("blob 路径 %s 不能以下标结尾（没有可替换的叶子字段）", ref.Path)
					}
					cur = anyList[idx]
					continue
				}
				return fmt.Errorf("blob 路径 %s 的 %s 不是数组", ref.Path, key)
			}
			if idx < 0 || idx >= len(list) {
				return fmt.Errorf("blob 路径 %s 的下标 %d 越界（数组长度 %d）", ref.Path, idx, len(list))
			}
			if last {
				return fmt.Errorf("blob 路径 %s 不能以下标结尾（没有可替换的叶子字段）", ref.Path)
			}
			cur = list[idx]
			continue
		}
		if last {
			m[key] = val
			return nil
		}
		cur = v
	}
	return nil
}

// parseSeg 解析一段路径：`key`、`key[]`、`key[3]`。
func parseSeg(seg string) (key string, idx int, hasIdx, all bool) {
	i := strings.IndexByte(seg, '[')
	if i < 0 {
		return seg, 0, false, false
	}
	key = seg[:i]
	inner := strings.TrimSuffix(seg[i+1:], "]")
	if strings.TrimSpace(inner) == "" {
		return key, 0, false, true // `[]` 通配（上面会明确拒绝）
	}
	n, err := strconv.Atoi(strings.TrimSpace(inner))
	if err != nil {
		return key, 0, false, true // 解析不了就按通配处理（同样会被拒绝并给出提示）
	}
	return key, n, true, false
}

// writeBodyWithBlobs 按占位串把请求体分块写出，中间的大字段流式 base64。
//
// ⚠️ **查找的是"带引号的占位串"**：`json.Marshal` 会把字符串值写成 `"@@MJBLOB0@@"`，
// 而我们要替换的正是**整个 JSON 值**（含那对引号）—— `writeFileDataURI` 自己会写出
// 首尾引号。只替换裸占位串会把引号留下来，产物变成 `"url":""data:...`（语法错）。
// 这个坑是被 `TestStreamingBodyMatchesNaiveMarshal` 咬出来的（它逐字节比对两条路）。
// dataURIPrefix 源视频 data URI 的媒体前缀。
//
// **算长度的地方与写字节的地方必须共用它**：两处各写一份时，改了其中一处就会让
// ContentLength 与实际字节数不符 —— 那种错在开发机上表现为
// `提交云端任务失败: http: ContentLength=… with Body length …`，一眼看不出是前缀变了。
const dataURIPrefix = "data:video/mp4;base64,"

// blobBodyLen 一个 blob 在请求体里占的字节数（含 JSON 值两侧的引号）。
//
// 能算准的原因是写出去的形态是**确定的**：`"` + dataURIPrefix + base64(StdEncoding) + `"`，
// 而 base64 字母表里没有需要 JSON 转义的字符、前缀也全是 ASCII ——
// 所以"JSON 转义后的长度"就等于原始长度（不需要再走一遍转义）。
func blobBodyLen(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("读 blob 文件失败(%s): %w", path, err)
	}
	return int64(2 + len(dataURIPrefix) + base64.StdEncoding.EncodedLen(int(st.Size()))), nil
}

// streamingBodyLen 流式请求体的**总字节数**（占位串都换成真 data URI 之后）。
//
// 为什么非得算准：ContentLength 给 0（或不给）时，net/http 对 io.PipeReader 会退化成
// `Transfer-Encoding: chunked`；而带 blobs 的唯一生产调用方是**付费的 2K 定稿**，
// 云端网关接不接受 chunked 在本机验不了 —— 一旦不接受，每次 2K 都失败在一个
// 与真实原因无关的报错上（2026-09-30 深度审计查出的就是这条死分支）。
func streamingBodyLen(raw []byte, refs []blobRef) (int64, error) {
	total := int64(len(raw))
	for i, ref := range refs {
		quoted, _ := json.Marshal(dataURIPlaceholder(i))
		if !bytes.Contains(raw, quoted) {
			return 0, fmt.Errorf("占位串 %d 没出现在请求体里（路径 %s）", i, ref.Path)
		}
		n, err := blobBodyLen(ref.File)
		if err != nil {
			return 0, err
		}
		total += n - int64(len(quoted))
	}
	return total, nil
}

func writeBodyWithBlobs(w io.Writer, raw []byte, refs []blobRef) error {
	for i, ref := range refs {
		quoted, _ := json.Marshal(dataURIPlaceholder(i)) // 得到 "…" 形式（带引号）
		idx := bytes.Index(raw, quoted)
		if idx < 0 {
			return fmt.Errorf("占位串 %d 没出现在请求体里（路径 %s）", i, ref.Path)
		}
		if _, err := w.Write(raw[:idx]); err != nil {
			return err
		}
		if err := writeFileDataURI(w, ref.File); err != nil {
			return err
		}
		raw = raw[idx+len(quoted):]
	}
	_, err := w.Write(raw)
	return err
}

// writeFileDataURI 把 `"data:video/mp4;base64,<文件内容>"` 流式写出去。
//
// 峰值内存只有 base64 编码器的内部缓冲（几十 KB），而不是"整片 + base64 + 拼接"三份
// —— 这是本次改动的全部目的。
func writeFileDataURI(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.WriteString(w, `"data:video/mp4;base64,`); err != nil {
		return err
	}
	enc := base64.NewEncoder(base64.StdEncoding, w)
	if _, err := io.Copy(enc, f); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	_, err = io.WriteString(w, `"`)
	return err
}

// create 通用请求（把 body 整体 marshal 成 JSON）。
//
// 带**大字段**（成片/参考图的 data URI）的调用请用 `createStreaming` —— 见那里的说明。
func (e *Engine) create(ctx context.Context, c config.MiniMaxSettings, body map[string]any) (string, error) {
	return e.createStreaming(ctx, c, body, nil)
}

// createStreaming 发一次创建请求。
//
// ## blobs：让大字段**边读边写**，不整读进内存（2026-09-29 性能审计 #7）
//
// 原来的写法是 `fileToVideoDataURI` 先 `os.ReadFile` 整片 → base64 成 string →
// 拼成 `data:video/mp4;base64,…` → 塞进 body → `json.Marshal(body)`。
// 对一份 **50MB** 的成片，这条链的峰值是这样叠起来的（审计给的推算）：
//
//	读 50MB + base64 67MB + 拼接 67MB + marshal ≈70MB  ⇒ **峰值 200~260MB 单次分配**
//
// 在"系统只剩 8GB"的现场（本仓注释里记过那个数值）这是本进程最大的一次性分配。
//
// 做法见 `dataURIPlaceholder` 的说明：先把大字段的值换成占位串再 marshal（产物只有
// 几十 KB），然后按占位串分块写出、中间那段用流式 base64。
// **不带 blobs 时走原路**（`json.Marshal` + `bytes.Reader`），行为与从前完全一致。
func (e *Engine) createStreaming(ctx context.Context, c config.MiniMaxSettings,
	body map[string]any, blobs []blobRef) (string, error) {

	// 【2026-09-30 深度审计】这一段原来有个**遮蔽 bug**：blob 分支里写的是
	// `raw, err := json.Marshal(body)` —— 新建了一个内层 `raw`，外层的还是 nil，
	// 于是下面那句 `if len(raw) > 0 { req.ContentLength = … }` 在**带 blobs 的那条路
	// 永远不成立** ⇒ io.PipeReader + ContentLength==0 ⇒ net/http 自动退化成
	// `Transfer-Encoding: chunked`。而带 blobs 的唯一生产调用方正是**付费的 2K 定稿**
	// （`regen.go`: 整片源视频 ~50MB）—— 云端网关接不接受 chunked，本机验不了。
	//
	// 现在两件事一起做对：① 变名（`enc`）杜绝遮蔽；② ContentLength **算准**（不是删掉）。
	// 长度是可精确计算的（前缀 + base64 填充都在掌握里），见 streamingBodyLen。
	var reader io.Reader
	var contentLen int64
	if len(blobs) == 0 {
		raw, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		reader = bytes.NewReader(raw)
		contentLen = int64(len(raw))
	} else {
		// ① 换占位串（改的是本地 map，调用方已经不再需要它）
		for i, b := range blobs {
			if err := setBlobPlaceholder(body, b, dataURIPlaceholder(i)); err != nil {
				return "", err
			}
		}
		// ② 序列化（产物只有几十 KB —— 大字段的位置上是占位串）
		enc, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		// ③ 长度：把每个占位串换成真实 data URI 之后的字节数（streamingBodyLen 的注释说明怎么算的）
		n, err := streamingBodyLen(enc, blobs)
		if err != nil {
			return "", err
		}
		contentLen = n
		// ④ 边写边编码
		pr, pw := io.Pipe()
		go func() {
			pw.CloseWithError(writeBodyWithBlobs(pw, enc, blobs))
		}()
		reader = pr
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.BaseURL, "/")+"/v2/video_generation", reader)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	// 显式给长度：既是真实字节数，也让 net/http 不必退化成 chunked。
	// 写出的字节数与它对不上时，客户端自己会报
	// `http: ContentLength=N with Body length M` —— 那正是我们想要的（静默少发/多发才是灾难）。
	req.ContentLength = contentLen
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("提交云端任务失败: %w", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", &apiError{Status: resp.StatusCode, Msg: textutil.Truncate(string(out), 400)}
	}
	var r struct {
		TaskID   string `json:"task_id"`
		BaseResp *struct {
			StatusMsg string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", err
	}
	if r.TaskID == "" {
		msg := ""
		if r.BaseResp != nil {
			msg = r.BaseResp.StatusMsg
		}
		return "", fmt.Errorf("响应缺少 task_id: %s %s", textutil.Truncate(string(out), 200), msg)
	}
	return r.TaskID, nil
}

func (e *Engine) query(ctx context.Context, c config.MiniMaxSettings, taskID string) (status, url string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(c.BaseURL, "/")+"/v2/query/video_generation/"+taskID, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", &apiError{Status: resp.StatusCode, Msg: textutil.Truncate(string(out), 300)}
	}
	var r struct {
		Task struct {
			Status  string `json:"status"`
			Content struct {
				URL string `json:"url"`
			} `json:"content"`
		} `json:"task"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", "", err
	}
	return strings.ToLower(r.Task.Status), r.Task.Content.URL, nil
}

func (e *Engine) cancel(c config.MiniMaxSettings, taskID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		strings.TrimRight(c.BaseURL, "/")+"/v2/video_generation/"+taskID, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	if resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req); err == nil {
		resp.Body.Close()
	}
}

func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载成片失败 HTTP %d", resp.StatusCode)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

func fileToDataURI(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(raw) > 30<<20 {
		return "", fmt.Errorf("图片超过 30MB 上限")
	}
	mime := "image/png"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".webp":
		mime = "image/webp"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

// MinDurationSeconds 云端 duration 的硬下限(秒)。
//
// 官方文档把 duration 写成一小组取值(5/10/15 量级),社区实测"任意整数都被接受";
// 两处口径不一致时取保守值,低于它一律抬到它(见 clampDuration,行为由 h3api_test.go
// 的 clampDuration 表钉住)。
//
// 导出它,是为了让"计划侧该生成多长的镜"有处可比:LLM 直出的单镜下限
// (director.GenMinShotSeconds)必须不低于它,否则生成出来的时长会被云端静默改写,
// 计划与成片时长不一致。断言在 internal/project/plan_contract_ext_test.go。
const MinDurationSeconds = 5

func clampDuration(sec int) int {
	if sec < MinDurationSeconds {
		return MinDurationSeconds
	}
	if sec > 15 {
		return 15
	}
	return sec
}

// submitDuration 本次提交给云端的时长(秒),以及它是否被云端区间改写过。
//
// 抽成纯函数是为了让"计划 4 秒 → 云端 5 秒"这条可被直接断言 —— 它此前埋在
// body 字面量里,只有真发一次请求(花一次额度)才看得见效果。
func submitDuration(lengthFrames int) (sec int, adjusted bool) {
	want := lengthFrames / 24
	sec = clampDuration(want)
	return sec, sec != want
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// ratioFor 由宽高推导 ratio(t2va 必填,不能为 adaptive)。
func ratioFor(w, h int) string {
	if w <= 0 || h <= 0 {
		return "9:16"
	}
	r := float64(w) / float64(h)
	switch {
	case r > 1.6:
		return "16:9"
	case r > 1.1:
		return "4:3"
	case r > 0.9:
		return "1:1"
	case r > 0.62:
		return "3:4"
	default:
		return "9:16"
	}
}

// GenerateMusic 云端引擎不提供配乐生成（安全空实现，与 GenerateImage/Upscale 同一政策）：
// 返回带指引的错误，让调用方如实播报"本机没有配乐生成能力"，而不是静默无声。
func (e *Engine) GenerateMusic(ctx context.Context, job engine.MusicJob) (string, error) {
	return "", fmt.Errorf("当前引擎(%s)不支持配乐生成：请在设置里把引擎切到本地 ComfyUI，或手工指定配乐文件", e.Name())
}
