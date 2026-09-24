package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/pipeline"
)

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	type phaseOut struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Icon string `json:"icon"`
	}
	type styleOut struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	phases := make([]phaseOut, 0, len(pipeline.PhaseNames))
	for _, p := range pipeline.PhaseNames {
		phases = append(phases, phaseOut{ID: p.ID, Name: p.Name, Icon: p.Icon})
	}
	keys := make([]string, 0, len(pipeline.StylePresets))
	for k := range pipeline.StylePresets {
		keys = append(keys, k)
	}
	sort.Strings(keys) // map 遍历无序 → 固定顺序,前端渲染才稳定
	styles := make([]styleOut, 0, len(keys))
	for _, k := range keys {
		styles = append(styles, styleOut{Key: k, Name: pipeline.StylePresets[k].Name})
	}
	up := engine.DefaultUpscaleOptions()
	writeJSON(w, 200, map[string]any{
		"phases": phases,
		"styles": styles,
		// 放大链路的默认参数连同默认文件名一起下发:前端只回显、不自行记常量,
		// 换权重/换默认 VAE 时改一处即可(此前那个 VAE 文件名只活在用户手打的
		// 输入框里,打错一个字要到 ComfyUI 执行到节点才报错)。
		// default_ltx_vae 取 ltx 那一套而不是 up.LTXVAE —— 默认方式(esrgan)
		// 根本不需要 VAE,DefaultUpscaleOptions() 的 LTXVAE 是空的。
		"upscale": map[string]any{
			"default_type":    up.Type,
			"default_model":   up.ModelName,
			"default_ltx_vae": engine.DefaultLTXUpscaleVAE,
		},
	})
}

// ---------- 设置 ----------

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	g := s.cfg.Get()
	// key 脱敏回显(只给尾 4 位)。三类 key 一个都不能漏 —— 漏掉的那个会以
	// 明文出现在任何能访问本机 8790 端口的响应里。
	g.LLM.APIKey = maskKey(g.LLM.APIKey)
	g.MiniMax.APIKey = maskKey(g.MiniMax.APIKey)
	g.VLM.APIKey = maskKey(g.VLM.APIKey)
	writeJSON(w, 200, g)
}

// maskKey 脱敏回显 API Key(只留尾 4 位)。长度 ≤8 时原样返回:
// 掩码比原文还长没有意义,反而把长度特征暴露得更清楚。
func maskKey(key string) string {
	if len(key) > 8 {
		return "****" + key[len(key)-4:]
	}
	return key
}

// isMaskedKey 判断回传值是否为掩码占位(而非用户新填的真实 key)。
// 前端拿到的是掩码,保存时会把掩码原样回传 —— 若不识别,真实 key 就会被
// 一串星号覆盖,表现为「打开设置页点一下保存,全部依赖突然鉴权失败」。
func isMaskedKey(key string) bool {
	return strings.HasPrefix(key, "****")
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in config.Settings
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	cur := s.cfg.Get()
	// 脱敏占位不覆盖真实 key(与 getSettings 的 maskKey 成对,漏一类即丢 key)
	if isMaskedKey(in.LLM.APIKey) {
		in.LLM.APIKey = cur.LLM.APIKey
	}
	if isMaskedKey(in.MiniMax.APIKey) {
		in.MiniMax.APIKey = cur.MiniMax.APIKey
	}
	if isMaskedKey(in.VLM.APIKey) {
		in.VLM.APIKey = cur.VLM.APIKey
	}
	// 模型文件名类字段传空 = 未改动,保留现值(防部分提交把模型名清空导致 400)
	keepStr(&in.Comfy.UNetRef2VA, cur.Comfy.UNetRef2VA)
	keepStr(&in.Comfy.UNetFL2VA, cur.Comfy.UNetFL2VA)
	keepStr(&in.Comfy.TurboLoRARef2VA, cur.Comfy.TurboLoRARef2VA)
	keepStr(&in.Comfy.TurboLoRAFL2VA, cur.Comfy.TurboLoRAFL2VA)
	keepStr(&in.Comfy.VideoVAE, cur.Comfy.VideoVAE)
	keepStr(&in.Comfy.AudioVAE, cur.Comfy.AudioVAE)
	keepStr(&in.Comfy.CLIP, cur.Comfy.CLIP)
	keepStr(&in.Comfy.ImageUNet, cur.Comfy.ImageUNet)
	keepStr(&in.Comfy.ImageCLIP, cur.Comfy.ImageCLIP)
	keepStr(&in.Comfy.ImageVAE, cur.Comfy.ImageVAE)
	keepStr(&in.Comfy.BaseURL, cur.Comfy.BaseURL)
	keepStr(&in.MiniMax.Model, cur.MiniMax.Model)
	keepStr(&in.LLM.Model, cur.LLM.Model)
	keepStr(&in.LLM.BaseURL, cur.LLM.BaseURL)
	if err := s.cfg.Update(in); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.opLog("💾 全局设置已保存(LLM=%s / 默认引擎=%s / 端口=%d)", in.LLM.Model, in.Render.DefaultEngine, in.ServerPort)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// keepStr 空-string 字段回填现值。
func keepStr(dst *string, cur string) {
	if strings.TrimSpace(*dst) == "" {
		*dst = cur
	}
}

// ---------- 项目 ----------
