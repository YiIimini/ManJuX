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
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/textutil"
)

// defaultAPITimeout 云端任务轮询兜底上限(作业未指定 Timeout 时使用)。
const defaultAPITimeout = 20 * time.Minute

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
			return "", fmt.Errorf("%w(云端任务 %s 超过 %v;已请求取消,避免空跑计费)", engine.ErrRenderTimeout, taskID, timeout)
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
		return nil, fmt.Errorf("下载成片失败(已重试): %w", dlErr)
	}
	settled = true // 成片已落到本地,远端任务无需再取消
	return &engine.Result{VideoPath: final, TaskID: taskID, Engine: e.Name()}, nil
}

func (e *Engine) create(ctx context.Context, c config.MiniMaxSettings, body map[string]any) (string, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.BaseURL, "/")+"/v2/video_generation", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
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

func clampDuration(sec int) int {
	if sec < 5 {
		return 5
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
