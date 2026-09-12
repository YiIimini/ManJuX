// handlers_enhance.go 镜头的两项增强能力入口。
//
//	局部重绘(Mask Fix) —— 只重生成跟踪到的目标区域,其余画面逐帧保留;
//	音频驱动(Audio Drive) —— 用定稿配音驱动重渲,人物口型与节奏跟随音频。
//
// 两者的引擎实现分别在 comfyengine/maskgraph.go 与 render 的音频条件分支里,
// 本文件只负责"取项目数据 → 组装入参 → 调用 → 回写产物"。
package server

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
)

// shotEnhanceCtx 两个端点共用的取数:定位镜头、校验产物、准备源视频与参考图。
type shotEnhanceCtx struct {
	proj     *project.Project
	shot     *project.Shot
	src      string // 源镜头本地绝对路径
	outName  string
	outPath  string
	timeout  time.Duration
	engineCE *comfyengine.Engine
}

func (s *Server) prepareShotEnhance(w http.ResponseWriter, r *http.Request, suffix string) (*shotEnhanceCtx, bool) {
	id := r.PathValue("id")
	shotID, err := strconv.Atoi(r.PathValue("shotID"))
	if err != nil {
		writeErr(w, 400, fmt.Errorf("镜头编号无效"))
		return nil, false
	}
	proj, ok := s.store.Get(id)
	if !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return nil, false
	}
	plan, err := s.store.LoadPlan(id)
	if err != nil {
		writeErr(w, 404, fmt.Errorf("尚无分镜方案"))
		return nil, false
	}
	manifest, err := s.store.LoadManifest(id)
	if err != nil {
		writeErr(w, 404, err)
		return nil, false
	}
	var shot *project.Shot
	for i := range plan.Shots {
		if plan.Shots[i].ID == shotID {
			shot = &plan.Shots[i]
			break
		}
	}
	if shot == nil {
		writeErr(w, 404, fmt.Errorf("镜头 %d 不存在", shotID))
		return nil, false
	}
	m, ok := manifest[strconv.Itoa(shotID)]
	if !ok || m.Status != project.ShotDone || m.File == "" {
		writeErr(w, 400, fmt.Errorf("镜头 %d 尚无已完成产物,请先渲染", shotID))
		return nil, false
	}
	ce, err := s.comfyEngine()
	if err != nil {
		writeErr(w, 503, err)
		return nil, false
	}
	outName := fmt.Sprintf("shot_%03d_%s", shotID, suffix)
	return &shotEnhanceCtx{
		proj:     proj,
		shot:     shot,
		src:      filepath.Join(s.store.Dir(id), m.File),
		outName:  outName,
		outPath:  filepath.Join(s.store.Dir(id), "clips", outName+".mp4"),
		timeout:  time.Duration(s.cfg.Get().Render.ShotTimeout) * time.Minute,
		engineCE: ce,
	}, true
}

// comfyEngine 取本地 ComfyUI 引擎(两项增强能力都依赖它)。
func (s *Server) comfyEngine() (*comfyengine.Engine, error) {
	eng, err := s.reg.Get("comfy")
	if err != nil {
		return nil, fmt.Errorf("未注册本地引擎")
	}
	ce, ok := eng.(*comfyengine.Engine)
	if !ok {
		return nil, fmt.Errorf("引擎类型不支持图像/视频生成")
	}
	return ce, nil
}

// relFile 把绝对路径转成项目内相对路径(与 manifest 的约定一致)。
func (s *Server) relFile(projectID, abs string) string {
	rel, err := filepath.Rel(s.store.Dir(projectID), abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

// handleShotMaskFix POST /api/projects/{id}/shot/{shotID}/mask-fix
// body { prompt?, object_index? } —— 局部重绘:只重生成跟踪到的目标区域。
func (s *Server) handleShotMaskFix(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Prompt      string `json:"prompt"`
		ObjectIndex string `json:"object_index"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	ctx, ok := s.prepareShotEnhance(w, r, "maskfix")
	if !ok {
		return
	}
	rp := ctx.proj.Config.Params.Render
	// 默认复用该镜原提示词:只把崩坏部分的描述改对,其余保持不变,重绘的局部才接得上
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		// 语言标记兜底(幂等):局部重绘/音频驱动同样要有 [Chinese] 标记,否则新出的
		// 片段又会出现听不懂的鸟语(2026-09-12)
		prompt = pipeline.EnsureChineseDialogue(ctx.shot.H3Prompt)
	}
	assets, _ := s.store.LoadAssets(ctx.proj.ID)
	refs, _ := pipeline.AssembleShotRefs(s.store.Dir(ctx.proj.ID), *ctx.shot, assets,
		ctx.proj.Config.Params.Assets.CompactRefsOn())

	s.opLog("✨ 局部重绘:%s 镜 %d 开始", ctx.proj.ID, ctx.shot.ID)
	if _, err := ctx.engineCE.MaskFix(r.Context(), ctx.src, comfyengine.MaskOptions{
		Prompt:      prompt,
		ObjectIndex: in.ObjectIndex,
	}, engine.Job{
		ProjectID: ctx.proj.ID, ShotID: ctx.shot.ID,
		Prompt: prompt, Mode: "ref2va", RefImages: refs,
		Width: rp.Width, Height: rp.Height, Seed: rp.Seed,
		Steps: rp.Steps, Accel: rp.Accel,
		OutputName: ctx.outName, Timeout: ctx.timeout,
	}, rp.Width, rp.Height, ctx.shot.Duration*24, ctx.outPath); err != nil {
		s.opLog("✕ 局部重绘失败:%v", err)
		writeErr(w, 500, fmt.Errorf("局部重绘失败:%w", err))
		return
	}

	// 产物作为**独立文件**保留,不覆盖原镜头产物 —— 由用户比对后再决定是否采用
	rel := s.relFile(ctx.proj.ID, ctx.outPath)
	s.opLog("✔ 局部重绘完成:%s 镜 %d → %s", ctx.proj.ID, ctx.shot.ID, filepath.Base(ctx.outPath))
	// 重活做完把显存交还(队列还有活/管线在跑时内部会自己拦下,见 ReleaseVRAMIfIdle)
	s.pipe.ReleaseVRAMIfIdle("局部重绘完成")
	writeJSON(w, 200, map[string]any{
		"ok": true, "file": rel,
		"url":  "/api/projects/" + ctx.proj.ID + "/files/" + rel,
		"note": "重绘结果作为独立文件产出,原镜头未改动;比对满意后可在故事板中替换",
	})
}

// handleShotAudioDrive POST /api/projects/{id}/shot/{shotID}/audio-drive
// body { audio_path } —— 音频驱动重渲:人物口型与动作节奏跟随给定音频。
func (s *Server) handleShotAudioDrive(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AudioPath string `json:"audio_path"`
		Prompt    string `json:"prompt"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.TrimSpace(in.AudioPath) == "" {
		writeErr(w, 400, fmt.Errorf("请提供配音文件路径(audio_path)"))
		return
	}
	if _, err := filepath.Abs(in.AudioPath); err != nil {
		writeErr(w, 400, fmt.Errorf("配音路径无效"))
		return
	}
	ctx, ok := s.prepareShotEnhance(w, r, "audiodrive")
	if !ok {
		return
	}
	rp := ctx.proj.Config.Params.Render
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		// 语言标记兜底(幂等):局部重绘/音频驱动同样要有 [Chinese] 标记,否则新出的
		// 片段又会出现听不懂的鸟语(2026-09-12)
		prompt = pipeline.EnsureChineseDialogue(ctx.shot.H3Prompt)
	}
	assets, _ := s.store.LoadAssets(ctx.proj.ID)
	refs, _ := pipeline.AssembleShotRefs(s.store.Dir(ctx.proj.ID), *ctx.shot, assets,
		ctx.proj.Config.Params.Assets.CompactRefsOn())

	s.opLog("🎙 音频驱动:%s 镜 %d 开始", ctx.proj.ID, ctx.shot.ID)
	// 音频走 Job.Audios → H3 Ref2V 的 ref_audios 条件(见 comfyengine 的音频参考实现)
	if _, err := ctx.engineCE.Render(r.Context(), engine.Job{
		ProjectID: ctx.proj.ID, ShotID: ctx.shot.ID,
		Prompt: prompt, Mode: "ref2va", RefImages: refs,
		Audios: []string{in.AudioPath},
		Width:  rp.Width, Height: rp.Height, Seed: rp.Seed,
		Steps: rp.Steps, Accel: rp.Accel,
		OutputDir: filepath.Dir(ctx.outPath), OutputName: ctx.outName, Timeout: ctx.timeout,
	}); err != nil {
		s.opLog("✕ 音频驱动失败:%v", err)
		writeErr(w, 500, fmt.Errorf("音频驱动失败:%w", err))
		return
	}
	rel := s.relFile(ctx.proj.ID, ctx.outPath)
	s.opLog("✔ 音频驱动完成:%s 镜 %d → %s", ctx.proj.ID, ctx.shot.ID, filepath.Base(ctx.outPath))
	s.pipe.ReleaseVRAMIfIdle("音频驱动完成")
	writeJSON(w, 200, map[string]any{
		"ok": true, "file": rel,
		"url":  "/api/projects/" + ctx.proj.ID + "/files/" + rel,
		"note": "音频驱动结果作为独立文件产出,原镜头未改动",
	})
}
