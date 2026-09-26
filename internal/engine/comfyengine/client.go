// Package comfyengine 实现本地 ComfyUI 渲染引擎。
// 节点结构与参数对齐 ComfyUI 内置 MiniMax-H3 节点
// (comfy_extras/nodes_minimax_h3.py)与 ComfyUI-H3-ConditioningCache 插件。
package comfyengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/textutil"
)

// Client ComfyUI HTTP API 客户端。
type Client struct {
	BaseURL  string
	ClientID string
	http     *http.Client
}

// NewClient 创建客户端。
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		ClientID: "manjux-" + fmt.Sprintf("%d", time.Now().UnixNano()%1e6),
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// Alive 探测服务是否存活。
func (c *Client) Alive(ctx context.Context) bool {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/system_stats", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// Version 返回 ComfyUI 版本摘要(探测页)。
func (c *Client) Version(ctx context.Context) (string, error) {
	raw, err := c.get(ctx, "/system_stats")
	if err != nil {
		return "", err
	}
	var out struct {
		System struct {
			ComfyUIVersion string `json:"comfyui_version"`
			PythonVersion  string `json:"python_version"`
			OS             string `json:"os"`
		} `json:"system"`
		Devices []struct {
			Name      string  `json:"name"`
			VRAMTotal float64 `json:"vram_total"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	ver := "ComfyUI " + out.System.ComfyUIVersion
	if len(out.Devices) > 0 {
		ver += " | GPU " + out.Devices[0].Name
	}
	return ver, nil
}

// HasNode 检查节点类是否存在(object_info)。
func (c *Client) HasNode(ctx context.Context, classType string) bool {
	raw, err := c.get(ctx, "/object_info/"+url.PathEscape(classType))
	if err != nil {
		return false
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return false
	}
	_, ok := out[classType]
	return ok
}

// UploadImage 上传图片到 ComfyUI input,返回服务器端文件名。
func (c *Client) UploadImage(ctx context.Context, localPath string) (name string, err error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("image", filepath.Base(localPath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	w.WriteField("overwrite", "true")
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/upload/image", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("上传失败 HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Name      string `json:"name"`
		Subfolder string `json:"subfolder"`
		Type      string `json:"type"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.Name, nil
}

// Submit 提交工作流,返回 prompt_id。
func (c *Client) Submit(ctx context.Context, graph map[string]any) (string, error) {
	payload := map[string]any{"prompt": graph, "client_id": c.ClientID}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/prompt", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("提交失败 HTTP %d: %s", resp.StatusCode, extractNodeErrors(string(raw)))
	}
	var out struct {
		PromptID string `json:"prompt_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.PromptID == "" {
		return "", fmt.Errorf("提交响应缺少 prompt_id")
	}
	return out.PromptID, nil
}

// extractNodeErrors 从 /prompt 422 响应中提取节点校验错误。
func extractNodeErrors(body string) string {
	var out struct {
		Error      map[string]any `json:"error"`
		NodeErrors map[string]struct {
			Errors []struct {
				Message string `json:"message"`
				Details string `json:"details"`
			} `json:"errors"`
		} `json:"node_errors"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return textutil.Truncate(body, 400)
	}
	var msgs []string
	if e, ok := out.Error["message"]; ok {
		msgs = append(msgs, fmt.Sprint(e))
	}
	for id, ne := range out.NodeErrors {
		for _, e := range ne.Errors {
			msgs = append(msgs, fmt.Sprintf("节点%s: %s", id, e.Message))
		}
	}
	if len(msgs) == 0 {
		return textutil.Truncate(body, 400)
	}
	return strings.Join(msgs, "; ")
}

// HistoryEntry 一次执行的产物与状态。
type HistoryEntry struct {
	Status struct {
		Completed bool              `json:"completed"`
		StatusStr string            `json:"status_str"`
		Messages  []json.RawMessage `json:"messages"`
	} `json:"status"`
	Outputs map[string]map[string]json.RawMessage `json:"outputs"`
}

// History 查询执行历史。
func (c *Client) History(ctx context.Context, promptID string) (*HistoryEntry, error) {
	raw, err := c.get(ctx, "/history/"+url.PathEscape(promptID))
	if err != nil {
		return nil, err
	}
	var out map[string]HistoryEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	e, ok := out[promptID]
	if !ok {
		return nil, nil // 尚在队列/执行中
	}
	return &e, nil
}

// OutputFile 从 history 输出中提取产物文件描述。
type OutputFile struct {
	Filename  string
	Subfolder string
	Type      string
	Animated  bool
}

// ExtractFiles 遍历 outputs 收集**本次执行真正产出的**文件对象(SaveVideo/SaveImage 等)。
//
// 注意:新版 SaveVideo 的 animated 标志是与 images 平级的独立键([true]),
// 而非文件项内字段;两者都要兼容。
//
// **`type == "input"` 的条目必须排除**(2026-09-24 修):任何从 input 读文件的节点
// (LoadVideo/LoadImage/LoadAudio)都会把自己的输入文件报进 outputs,而那是**用户传进去的
// 素材**、不是产物;它同样带 animated 标志,于是"取第一个 animated 文件"会有一半概率
// 把**源片原样**当成结果返回 —— 放大链路实测踩到:界面提示"放大完成",拿到的却是一模一样
// 的原视频(分辨率/字节数都没变)。outputs 是 map,遍历顺序随机,所以这个 bug 是间歇性的。
func (h *HistoryEntry) ExtractFiles() []OutputFile {
	var files []OutputFile
	for _, nodeOut := range h.Outputs {
		// 平级 animated 标志(新版 SaveVideo)
		nodeAnimated := false
		if raw, ok := nodeOut["animated"]; ok {
			var flags []bool
			if json.Unmarshal(raw, &flags) == nil {
				for _, f := range flags {
					if f {
						nodeAnimated = true
					}
				}
			}
		}
		for key, arrRaw := range nodeOut {
			if key == "ui" || key == "animated" {
				continue
			}
			var arr []struct {
				Filename  string `json:"filename"`
				Subfolder string `json:"subfolder"`
				Type      string `json:"type"`
				Format    string `json:"format"`
				Animated  bool   `json:"animated"`
			}
			if err := json.Unmarshal(arrRaw, &arr); err != nil {
				continue
			}
			for _, it := range arr {
				if it.Filename == "" {
					continue
				}
				if it.Type == "input" {
					continue // 输入素材,不是产物
				}
				animated := it.Animated || nodeAnimated || strings.Contains(it.Format, "video")
				files = append(files, OutputFile{it.Filename, it.Subfolder, it.Type, animated})
			}
		}
	}
	return files
}

// Download 下载 output/view 文件到本地。
func (c *Client) Download(ctx context.Context, f OutputFile, destPath string) error {
	q := url.Values{}
	q.Set("filename", f.Filename)
	if f.Subfolder != "" {
		q.Set("subfolder", f.Subfolder)
	}
	if f.Type != "" {
		q.Set("type", f.Type)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/view?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败 HTTP %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	tmp := destPath + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, destPath)
}

// Free 释放显存(卸载模型+清缓存)。失败必须上报——曾把连接失败/非 200
// 全部吞掉,引擎没运行也报「已请求」,用户观感为假功能(2026-09-09)。
func (c *Client) Free(ctx context.Context) error {
	body := bytes.NewReader([]byte(`{"unload_models":true,"free_memory":true}`))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/free", body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("请求 ComfyUI /free 失败:%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ComfyUI /free 返回 %s", resp.Status)
	}
	return nil
}

// Interrupt 中断当前执行。
func (c *Client) Interrupt(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/interrupt", nil)
	if err != nil {
		return
	}
	if resp, err := c.http.Do(req); err == nil {
		resp.Body.Close()
	}
}

// ClearPending 清空待执行队列(不影响运行中任务);用于管线起渲前清掉
// 上次异常退出遗留的提交,防止同一镜头重复渲染堆积。
func (c *Client) ClearPending(ctx context.Context) {
	body := bytes.NewReader([]byte(`{"clear":true}`))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/queue", body)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := c.http.Do(req); err == nil {
		resp.Body.Close()
	}
}

// Queue 返回 (运行中ids, 排队ids)。
func (c *Client) Queue(ctx context.Context) (running, pending []string, err error) {
	raw, err := c.get(ctx, "/queue")
	if err != nil {
		return nil, nil, err
	}
	var out struct {
		QueueRunning [][2]any `json:"queue_running"`
		QueuePending [][2]any `json:"queue_pending"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	extract := func(items [][2]any) []string {
		var ids []string
		for _, it := range items {
			// ComfyUI 0.34 /queue 项为 [序号, "prompt_id", graph]([2]any 截取前两个);
			// 旧版为 [序号, ["prompt_id", …]];兼容 id 在 it[0]/it[1]/it[1][0] 三种形态。
			for _, cand := range []any{it[0], it[1]} {
				if s, ok := cand.(string); ok && s != "" {
					ids = append(ids, s)
					break
				}
				if arr, ok := cand.([]any); ok && len(arr) > 0 {
					if s, ok := arr[0].(string); ok && s != "" {
						ids = append(ids, s)
						break
					}
				}
			}
		}
		return ids
	}
	return extract(out.QueueRunning), extract(out.QueuePending), nil
}

// defaultShotTimeout 单镜渲染兜底上限(作业未显式指定 Timeout 时使用)。
const defaultShotTimeout = 45 * time.Minute

// maxProbeFails 连续探测失败多少次即判定引擎不可达(轮询心跳 3 秒 → 约 18 秒)。
// 取值理由:正常的 ComfyUI 不会连续 6 次(18 秒)都不回应 /history,而偶发丢包或
// 单次 GC 停顿不足以连挂 6 次;判死得太晚就是白等 45~90 分钟。
// 变量而非常量:回归测试要把阈值压到 1,否则这条用例得白等 18 秒。
var maxProbeFails = 6

// defaultImageTimeout 定妆图生成的兜底上限。图像生成没有单镜作业可依,
// 取一个明显短于整镜渲染的值,避免 Krea-2 卡死时白等 45 分钟。
const defaultImageTimeout = 10 * time.Minute

// WaitForDone 轮询直到完成/出错。onProgress 收到 0-100 进度。
// timeout<=0 时回退 defaultShotTimeout。
//
// **计时口径(2026-09-12 修正)**:排队等待不计入执行预算。
// 旧实现把 deadline 定在"提交那一刻"(time.Now().Add(timeout)),于是排在前面的
// 长视频任务(单镜可跑数十分钟)会把后面所有任务的超时预算一起吃掉 —— 出图任务
// 排 10 分钟队就必然被判「渲染超时(单镜上限 10m0s)」,而引擎其实一点没坏。
// 实测:三条管线并发时,资产阶段的角色与场景定妆照接连被杀,镜头因此缺参考图
// 降级 t2va(2026-09-12 用户报障「角色生成失败」「场景也有失败」)。
// 现在只计"真正在执行"的时间(timeout 是卡死防线,不是排队防线),排队另有
// 一个宽松上限 queueCap:排到 queueCap 还没轮到,才判定异常并明确告知在排队。
func (c *Client) WaitForDone(ctx context.Context, promptID string, timeout time.Duration, onProgress func(pct int, note string)) (*HistoryEntry, error) {
	if timeout <= 0 {
		timeout = defaultShotTimeout
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	submitted := time.Now() // 提交时刻(仅用于排队计时与展示)
	var execStart time.Time // 首次观察到"正在执行"的时刻
	missCount := 0          // 连续不在队列且无 history 的次数
	probeFails := 0         // 连续探测(History)失败次数 —— 引擎死掉时靠它判死
	lastNote := ""          // 进度文案去重:同文案不重复上报
	report := func(pct int, note string) {
		if note == lastNote {
			return
		}
		lastNote = note
		if onProgress != nil {
			onProgress(pct, note)
		}
	}
	fmtDur := func(d time.Duration) string {
		s := int(d.Seconds())
		if s < 60 {
			return fmt.Sprintf("%d 秒", s)
		}
		return fmt.Sprintf("%d 分 %02d 秒", s/60, s%60)
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			now := time.Now()
			switch DecideWait(now, submitted, execStart, timeout, queueWaitCap) {
			case WaitExecTimeout:
				return nil, fmt.Errorf("%w(已执行 %v,执行上限 %v;排队等待 %v)",
					engine.ErrRenderTimeout, fmtDur(now.Sub(execStart)), timeout, fmtDur(execStart.Sub(submitted)))
			case WaitQueueTimeout:
				return nil, fmt.Errorf("%w(排队等待 %v 仍未开始执行,排队上限 %v;通常是前面压着更长的视频任务)",
					engine.ErrRenderTimeout, fmtDur(now.Sub(submitted)), queueWaitCap)
			}
			h, err := c.History(ctx, promptID)
			if err != nil {
				// 网络抖动容忍,但"引擎已死"不是抖动(2026-09-26 渲染管线审查):
				// ComfyUI 被 OOM 杀掉/崩溃后 History 与 Queue 会**一路**失败,旧实现
				// 只 continue,于是界面持续显示「排队中/执行中 · 已 N 分」,最长空转
				// 45 分钟(任务还没轮到执行则 90 分钟)才抛超时,之后才走到
				// "释放显存 + 重试"的恢复路径 —— 一次崩溃代价接近一小时机时,而且
				// 把"引擎已死"误导成"在排队"。连续失败即判死,交给上层恢复。
				probeFails++
				if probeFails >= maxProbeFails {
					return nil, fmt.Errorf("%w(连续 %d 次探测 ComfyUI 无响应,约 %s;引擎可能已退出或不可达: %v)",
						engine.ErrRenderTimeout, probeFails, fmtDur(time.Duration(maxProbeFails)*3*time.Second), err)
				}
				if probeFails >= 2 {
					// 文案必须与"排队中"区分开:引擎没回应时说自己"在排队"是假话
					report(2, fmt.Sprintf("ComfyUI 无响应 · 已等 %s", fmtDur(now.Sub(submitted))))
				}
				continue
			}
			probeFails = 0
			if h == nil {
				runIDs, pendIDs, qerr := c.Queue(ctx)
				if qerr == nil && !containsID(runIDs, promptID) && !containsID(pendIDs, promptID) {
					missCount++
					if missCount >= 6 { // 约 18 秒无踪迹:任务被清队/中断,重试而非干等
						return nil, fmt.Errorf("任务已从队列消失且无执行记录(可能被清理),请重试本镜")
					}
				} else {
					missCount = 0
				}
				if qerr == nil {
					if containsID(pendIDs, promptID) {
						// 排队中:如实报"已等多久" —— 用户要能一眼看出慢在排队而不是卡死
						report(2, fmt.Sprintf("排队中 · 已等 %s", fmtDur(now.Sub(submitted))))
					} else if containsID(runIDs, promptID) {
						if execStart.IsZero() {
							execStart = now
						}
						// 执行中:真实计时(每 30 秒更新一跳),不再给假百分比
						s := int(now.Sub(execStart).Seconds())
						if s%30 < 3 {
							report(20+min(60, s/10), fmt.Sprintf("执行中 · 已 %s", fmtDur(now.Sub(execStart))))
						}
					} else {
						report(50, "执行中")
					}
				} else {
					// qerr != nil = 队列也读不到:这时报「排队中」是假话(引擎不可达),
					// 用户要能一眼区分"在排队"与"引擎没回应"(审计 2026-09-26)
					report(2, fmt.Sprintf("ComfyUI 无响应 · 已等 %s", fmtDur(now.Sub(submitted))))
				}
				continue
			}
			if execStart.IsZero() {
				execStart = now // 有 history 记录 = 已经开始执行
			}
			if h.Status.Completed || h.Status.StatusStr == "success" {
				return h, nil
			}
			if h.Status.StatusStr == "error" {
				return h, fmt.Errorf("执行出错: %s", execError(h))
			}
			{
				pct := progressFromMessages(h)
				report(pct, fmt.Sprintf("渲染中 %d%%", pct))
			}
		}
	}
}

// queueWaitCap 排队等待上限:排这么久还没轮到就判定异常(有更长的任务压着或队列卡住)。
// 与执行上限(timeout)分开计,不共用预算 —— 见 WaitForDone 的计时口径说明。
const queueWaitCap = 90 * time.Minute

// WaitDecision 一次轮询的判定结果。
type WaitDecision int

const (
	WaitContinue     WaitDecision = iota // 继续等
	WaitExecTimeout                      // 执行超时(真卡死)
	WaitQueueTimeout                     // 排队超时(一直没轮到)
)

// DecideWait 判定是否该放弃等待。抽成纯函数便于单测 —— 这里的口径直接决定
// "排在长任务后面的任务会不会被误杀",是本次修复的核心,必须有回归测试钉住。
func DecideWait(now, submitted, execStart time.Time, execTimeout, queueCap time.Duration) WaitDecision {
	if !execStart.IsZero() {
		if now.Sub(execStart) > execTimeout {
			return WaitExecTimeout
		}
		return WaitContinue
	}
	if now.Sub(submitted) > queueCap {
		return WaitQueueTimeout
	}
	return WaitContinue
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// progressFromMessages 从 status.messages 提取采样进度。
func progressFromMessages(h *HistoryEntry) int {
	for _, raw := range h.Status.Messages {
		var m []json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil || len(m) < 2 {
			continue
		}
		var ev string
		json.Unmarshal(m[0], &ev)
		if ev != "progress" {
			continue
		}
		var data struct {
			Value int `json:"value"`
			Max   int `json:"max"`
		}
		if err := json.Unmarshal(m[1], &data); err == nil && data.Max > 0 {
			return data.Value * 100 / data.Max
		}
	}
	return 5
}

// execError 从错误消息中提取人话。
func execError(h *HistoryEntry) string {
	for _, raw := range h.Status.Messages {
		var m []json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil || len(m) < 2 {
			continue
		}
		var ev string
		json.Unmarshal(m[0], &ev)
		if ev != "execution_error" && ev != "execution_interrupted" {
			continue
		}
		var data struct {
			NodeMessage      string `json:"node_message"`
			ExceptionMessage string `json:"exception_message"`
			NodeType         string `json:"node_type"`
		}
		if err := json.Unmarshal(m[1], &data); err == nil {
			msg := data.ExceptionMessage
			if msg == "" {
				msg = data.NodeMessage
			}
			if data.NodeType != "" {
				msg = "[" + data.NodeType + "] " + msg
			}
			if msg != "" {
				return textutil.Truncate(msg, 500)
			}
		}
	}
	return "未知执行错误(详见 ComfyUI 控制台)"
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// HTTP 暴露内部 http.Client(引擎内探测用)。
func (c *Client) HTTP() *http.Client { return c.http }

// ComboList 读取节点某 combo 输入的可选值列表。
//
// ComfyUI 的 object_info 里 combo 有**两种形态**,必须都认(实测两者并存于同一版本):
//
//	旧式:  "vae_name":   [ ["a.safetensors","b.safetensors"], {…} ]
//	新式:  "model_name": [ "COMBO", {"multiselect":false,"options":[…]} ]
//
// 旧式取 def[0] 当字符串数组;新式的 def[0] 是字符串 "COMBO",选项在 def[1].options
// 里 —— 只认旧式时新式节点会一律返回 ok=false,而调用方普遍把 false 理解成
// "节点不存在/输入已改名",于是本该报"缺哪个模型文件"的地方变成了一句误导人的
// 节点缺失(实测:`LatentUpscaleModelLoader` 与 `UpscaleModelLoader` 都是新式)。
func (c *Client) ComboList(ctx context.Context, classType, field string) ([]string, bool) {
	raw, err := c.get(ctx, "/object_info/"+url.PathEscape(classType))
	if err != nil {
		return nil, false
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	nodeRaw, ok := out[classType]
	if !ok {
		return nil, false
	}
	var node struct {
		Input struct {
			Required map[string]json.RawMessage `json:"required"`
		} `json:"input"`
	}
	if err := json.Unmarshal(nodeRaw, &node); err != nil {
		return nil, false
	}
	defRaw, ok := node.Input.Required[field]
	if !ok {
		return nil, false
	}
	var def []json.RawMessage
	if err := json.Unmarshal(defRaw, &def); err != nil || len(def) == 0 {
		return nil, false
	}

	// 新式:def[0] 是 "COMBO" 标记,选项挂在 def[1].options
	var tag string
	if err := json.Unmarshal(def[0], &tag); err == nil && tag == "COMBO" {
		if len(def) < 2 {
			return nil, false
		}
		var meta struct {
			Options []string `json:"options"`
		}
		if err := json.Unmarshal(def[1], &meta); err != nil {
			return nil, false
		}
		return meta.Options, true
	}

	// 旧式:def[0] 直接就是字符串数组
	var list []string
	if err := json.Unmarshal(def[0], &list); err != nil {
		return nil, false
	}
	return list, true
}
