// library_ops.go 资产库手动运营:抽卡(LLM 随机原创角色+Krea-2 定妆)与自定义上传入库。
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/pipeline"
)

// genOuterTimeout 手动生成类操作(抽卡/自定义新建/补全多视角)的外层 ctx 上限。
//
// 它**不是**生成本身的超时:生成超时由引擎按"真正在执行"的时间判定(定妆图 10 分钟
// 执行上限 + 90 分钟排队上限,见 comfyengine.DecideWait)。这个外层值只是防止
// 请求永久挂着;定太长没意义,定太短则会把"排队"误当失败 ——
// 实测教训:原值 4 分钟,一次抽卡正好排在 4.5 分钟的转身视频后面,被 ctx 直接掐死,
// 报「定妆生成失败(Krea-2):context deadline exceeded」,用户看到的就是"角色生成失败"
// (2026-09-12)。故取一个能覆盖长任务本身 + 排队余地的值。
const genOuterTimeout = 45 * time.Minute

// 抽卡题材池:随机世界观 × 气质,一次一签,抽到什么全看命。
var gachaThemes = []string{
	"都市修仙霓虹夜市", "赛博废土机械义体", "和风妖怪温泉街", "蒸汽朋克空中港",
	"港式黑帮茶餐厅", "校园天台落日", "克苏鲁深潜图书馆", "武侠客栈风雪夜",
	"星际歌剧边缘星港", "末世地铁避难所", "民国上海百乐门", "北欧神话冰原村",
	"古蜀三星堆祭祀", "深海研究站", "架空王朝宫廷夜宴", "废土公路车队",
}
var gachaTraits = []string{
	"外冷内热", "痞气仗义", "温润腹黑", "疯癫天才", "沉默寡言的守护者",
	"元气莽撞", "优雅毒舌", "憨厚大力", "病弱军师", "潇洒浪客",
}

// handleLibraryGacha 抽卡:LLM 随机原创角色 → Krea-2 定妆照 → 入资产库。
func (s *Server) handleLibraryGacha(w http.ResponseWriter, r *http.Request) {
	theme := gachaThemes[rand.Intn(len(gachaThemes))]
	trait := gachaTraits[rand.Intn(len(gachaTraits))]
	g := s.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, g.LLM.Timeout)
	ctx, cancel := context.WithTimeout(r.Context(), genOuterTimeout)
	defer cancel()

	sys := "你是角色设计师。输出严格 JSON(无 markdown 包裹):" +
		`{"name":"中文姓名(2-4字)","description":"中文外貌/服装/气质,80-120字,具体可画",` +
		`"image_prompt":"英文文生图提示词(半身定妆照:面部特征+发型+服装+气质+光线),120词内"}`
	user := fmt.Sprintf("题材:%s。角色气质:%s。设计一个原创主角级角色,要有记忆点。", theme, trait)
	out, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: sys}, {Role: "user", Content: user},
	}, true)
	if err != nil {
		writeErr(w, 500, fmt.Errorf("抽卡失败(LLM):%w", err))
		return
	}
	out = strings.TrimSpace(strings.Trim(out, "`"))
	var card struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		ImagePrompt string `json:"image_prompt"`
	}
	if err := json.Unmarshal([]byte(trimJSON(out)), &card); err != nil ||
		card.Name == "" || card.ImagePrompt == "" {
		writeErr(w, 500, fmt.Errorf("抽卡结果解析失败(LLM 返回异常)"))
		return
	}

	// Krea-2 定妆照(按实体类型取景:抽卡也可能抽出一只器物/妖兽)
	imgPath, err := s.genLibraryPortrait(ctx, card.Name, card.Description, card.ImagePrompt, pipeline.RandomSeed())
	if err != nil {
		writeErr(w, 500, fmt.Errorf("抽卡定妆失败(Krea-2):%w", err))
		return
	}
	entry, err := s.lib.Put("characters", card.Name, card.Description, card.ImagePrompt, imgPath, "", "gacha")
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	s.pipe.ReleaseVRAMIfIdle("抽卡完成")
	writeJSON(w, 200, map[string]any{"entry": entry, "theme": theme, "trait": trait})
}

// handleLibraryCreateCharacter 自定义新建:上传图片直接定妆,或按描述生成。
// 支持 JSON(image_b64/image_path,桌面模式主通道,见 upload_payload.go)
// 与 multipart(浏览器模式兼容):name(必)、description、image_prompt、mode=upload|generate
func (s *Server) handleLibraryCreateCharacter(w http.ResponseWriter, r *http.Request) {
	var name, desc, prompt, imgPath string
	uploaded := false
	var cleanup func()
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()

	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var in struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			ImagePrompt string `json:"image_prompt"`
			imagePayload
		}
		if err := readBody(r, &in); err != nil {
			writeErr(w, 400, err)
			return
		}
		name = strings.TrimSpace(in.Name)
		desc = strings.TrimSpace(in.Description)
		prompt = strings.TrimSpace(in.ImagePrompt)
		if name == "" {
			writeErr(w, 400, fmt.Errorf("角色名必填"))
			return
		}
		if in.ImageB64 != "" || in.ImagePath != "" {
			p, cl, err := in.resolve()
			if err != nil {
				writeErr(w, 400, err)
				return
			}
			imgPath, cleanup, uploaded = p, cl, true
		}
	} else if err := r.ParseMultipartForm(64 << 20); err == nil {
		name = strings.TrimSpace(r.FormValue("name"))
		desc = strings.TrimSpace(r.FormValue("description"))
		prompt = strings.TrimSpace(r.FormValue("image_prompt"))
		if file, hdr, ferr := r.FormFile("image"); ferr == nil {
			defer file.Close()
			tmp, terr := os.CreateTemp("", "mx-char-*"+strings.ToLower(filepath.Ext(hdr.Filename)))
			if terr != nil {
				writeErr(w, 500, terr)
				return
			}
			defer os.Remove(tmp.Name())
			if _, rerr := tmp.ReadFrom(file); rerr != nil {
				tmp.Close()
				writeErr(w, 500, rerr)
				return
			}
			tmp.Close()
			imgPath = tmp.Name()
			uploaded = true
		}
	} else {
		writeErr(w, 400, fmt.Errorf("表单解析失败: %v", err))
		return
	}
	if name == "" {
		writeErr(w, 400, fmt.Errorf("角色名必填"))
		return
	}

	if imgPath == "" {
		if prompt == "" {
			writeErr(w, 400, fmt.Errorf("请上传参考图片,或填写生成提示词"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), genOuterTimeout)
		defer cancel()
		p, err := s.genLibraryPortrait(ctx, name, desc, prompt, pipeline.RandomSeed())
		if err != nil {
			writeErr(w, 500, fmt.Errorf("定妆生成失败(Krea-2):%w", err))
			return
		}
		imgPath = p
	}

	source := "krea2"
	if uploaded {
		source = "upload"
	}
	entry, err := s.lib.Put("characters", name, desc, prompt, imgPath, "", source)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	s.pipe.ReleaseVRAMIfIdle("自定义新建完成")
	writeJSON(w, 200, map[string]any{"entry": entry})
}

// libraryRegen 重抽库资产:用条目自带的提示词重新生成定妆照(随机 seed)并覆盖入库。
//
// 与"抽卡"的区别:抽卡是抽一个**全新角色**(LLM 现编题材与外貌);重抽是**同一个角色
// 换一张** —— 描述与提示词原样不动,只换随机种子,不满意就再抽一次。
//
// 重抽会同时清掉旧的多视角原图与转台图(它们对应的是上一张定妆照,已经失效);
// 清掉之后可点「补全多视角」重建,渲染前也会自动补。
//
// 提示词为空的条目(手动上传/gacha 之前的存量)不再直接拒绝:先让 LLM 依
// 描述/名字补一条英文提示词再抽 —— 「这条不能抽」等于抽卡功能对这类条目不存在,
// 而用户要的正是"点进去就能再抽一张"。
func (s *Server) libraryRegen(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		writeErr(w, 500, fmt.Errorf("资产库未启用"))
		return
	}
	kind, name := r.PathValue("kind"), r.PathValue("name")
	e, _, ok := s.lib.Match(kind, name)
	if !ok {
		writeErr(w, 404, fmt.Errorf("库资产不存在"))
		return
	}
	prompt := strings.TrimSpace(e.ImagePrompt)
	if prompt == "" {
		prompt = s.deriveImagePrompt(r.Context(), name, e.Description)
		if prompt == "" {
			writeErr(w, 400, fmt.Errorf("该条目没有提示词,且无法从描述推导(请先补全中文描述)"))
			return
		}
	}
	seed := pipeline.RandomSeed() // 随机种子:每次抽到不同的图
	tmp, err := s.genLibraryPortrait(r.Context(), name, e.Description, prompt, seed)
	if err != nil {
		writeErr(w, 503, fmt.Errorf("重抽失败(需要本地 ComfyUI):%w", err))
		return
	}
	defer os.Remove(tmp)

	s.lib.ClearRelated(kind, name) // 旧角度图对应上一张定妆照,已失效
	if _, err := s.lib.Put(kind, name, e.Description, prompt, tmp, e.SourceProject, e.Source); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.opLog("🎲 库资产重抽:%s「%s」", kind, name)
	s.pipe.ReleaseVRAMIfIdle("库资产重抽完成")
	updated, _, _ := s.lib.Match(kind, name)
	writeJSON(w, 200, updated)
}

// deriveImagePrompt 没有提示词时,让 LLM 依据中文描述/名字补一条英文定妆照提示词。
// 失败不致命(返回空串由调用方决定文案),但要在日志里留下线索 —— 否则用户只看到
// 「抽不了」而不知道是 LLM 没配还是描述太短。
func (s *Server) deriveImagePrompt(ctx context.Context, name, description string) string {
	g := s.cfg.Get()
	client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, 120)
	if !client.Configured() {
		if strings.TrimSpace(description) != "" {
			return strings.TrimSpace(description)
		}
		return ""
	}
	rctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := client.Chat(rctx, []llm.Message{
		{Role: "system", Content: "你是提示词工程师。把角色设定写成一条英文文生图提示词(半身定妆照:面部特征+发型+服装+气质+光线),120 词内。只输出提示词本身,不要任何说明或引号。"},
		{Role: "user", Content: "角色名:" + name + "\n中文设定:" + description},
	}, false)
	if err != nil {
		s.opLog("⚠ 库资产提示词推导失败(LLM):%v", err)
		if strings.TrimSpace(description) != "" {
			return strings.TrimSpace(description)
		}
		return ""
	}
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(out), "`"))
}

// libraryViews 补全多视角:定妆照 → H3 原地转身视频 → 抽帧 → 转台图,落在库条目
// 目录内并刷新 asset.json 的图片清单。已有角度图时直接返回,不做重复渲染。
//
// 这是"角色在渲染里只有一张正面照"的手动解法;渲染前 backfillAssetViews 也会
// 自动补同样的东西,两者共用 pipeline.GenerateTurnaround 的实现与产物命名。
func (s *Server) libraryViews(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		writeErr(w, 500, fmt.Errorf("资产库未启用"))
		return
	}
	kind, name := r.PathValue("kind"), r.PathValue("name")
	e, master, ok := s.lib.Match(kind, name)
	if !ok {
		writeErr(w, 404, fmt.Errorf("库资产不存在"))
		return
	}
	if kind != "characters" {
		writeErr(w, 400, fmt.Errorf("多视角转台图仅对角色有意义(场景无需转身参考)"))
		return
	}
	if n := len(s.lib.RelatedFiles(kind, name)); n > 0 {
		writeJSON(w, 200, map[string]any{"entry": e, "skipped": true, "views": n,
			"message": fmt.Sprintf("该资产已有多视角图(%d 张),无需重复生成", n)})
		return
	}
	eng, err := s.reg.Get("comfy")
	if err != nil {
		writeErr(w, 503, fmt.Errorf("补全多视角需要本地 ComfyUI:%w", err))
		return
	}
	if err := eng.Available(r.Context()); err != nil {
		writeErr(w, 503, fmt.Errorf("补全多视角需要本地 ComfyUI:%w", err))
		return
	}
	dir, err := s.lib.EntryDir(kind, name)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// 两条路都可能走:图像编辑(每张几秒,首选)或 H3 转身视频(完整单镜渲染,数分钟)
	// + 抽帧拼接。两者都按最坏情况给足上限;不做"假进度",完成后一次性回结果,
	// 期间前端按钮保持 loading。外层上限同样要给排队留余地
	// (见 genOuterTimeout):排在别的长任务后面时,等待时间与任务本身无关。
	ctx, cancel := context.WithTimeout(r.Context(), genOuterTimeout)
	defer cancel()
	prompt := strings.TrimSpace(e.ImagePrompt)
	if prompt == "" {
		prompt = name
	}
	g := s.cfg.Get()
	steps := g.Render.Steps
	if steps <= 0 {
		steps = 8
	}
	// 起始与逐段进度都由 GenerateTurnaround 的 Emit 转出(引擎每 30 秒报一次):
	// 数分钟的操作不能全程无反馈,也不在这里再补一条重复的"开始"日志。
	_, err = pipeline.GenerateTurnaround(ctx, eng, s.ff, pipeline.TurnaroundOptions{
		MasterPath: master,
		Dir:        dir,
		TempVideo:  true, // 视频不如库,库目录只留参考图
		Name:       name,
		Prompt:     prompt,
		// 资产库这条路径没有项目上下文,但**类型与介质都必须判**:
		//   - 类型决定"多视角方法"(器物转台自转 vs 人原地转身),漏传会让一只
		//     铜雀按人形转身提示词去转(实体类型那轮修的就是这个);
		//   - 介质由定妆照提示词反推:动画线的定妆照(带 ANIMATED MATTE RENDER 标记)
		//     必须锁同介质,否则 H3 用自己的默认介质重画成写实版,逐帧校验必然不过。
		Kind:      pipeline.InferEntityKind(name, e.Description, prompt),
		Realistic: pipeline.RealisticForPrompt(prompt),
		Steps:     steps,
		Verify:    pipeline.TurnaroundVerifier(g.VLM),
		Emit:      func(f string, a ...any) { s.opLog("🎥 "+f, a...) },
	})
	if err != nil {
		writeErr(w, 500, fmt.Errorf("多视角生成失败:%w", err))
		return
	}
	// 重新入库刷新图片清单(主图仍是原来的定妆照)
	entry, err := s.lib.Put(kind, name, e.Description, e.ImagePrompt, master, e.SourceProject, e.Source)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	s.opLog("✔ 补全多视角完成:角色「%s」(%d 张图)", name, len(entry.Images))
	s.pipe.ReleaseVRAMIfIdle("补全多视角完成")
	writeJSON(w, 200, map[string]any{"entry": entry})
}

// librarySetMeta 手动编辑中文描述与英文提示词(图片与复用计数不动)。
func (s *Server) librarySetMeta(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		writeErr(w, 500, fmt.Errorf("资产库未启用"))
		return
	}
	var in struct {
		Description string `json:"description"`
		ImagePrompt string `json:"image_prompt"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	e, err := s.lib.SetMeta(r.PathValue("kind"), r.PathValue("name"), in.Description, in.ImagePrompt)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, e)
}

// libraryDescribe AI 生成中文描述与英文提示词:优先用视觉模型**看图**写
// (VLM 配置在设置页,智谱 glm-4.6v-flash 免费),VLM 不可用/失败时退回文本模型
// 依名字与现有描述扩写。结果直接落库并回传,前端可再编辑后保存。
func (s *Server) libraryDescribe(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		writeErr(w, 500, fmt.Errorf("资产库未启用"))
		return
	}
	kind, name := r.PathValue("kind"), r.PathValue("name")
	e, master, ok := s.lib.Match(kind, name)
	if !ok {
		writeErr(w, 404, fmt.Errorf("库资产不存在"))
		return
	}
	g := s.cfg.Get()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	var card struct {
		Description string `json:"description"`
		ImagePrompt string `json:"image_prompt"`
	}
	via := ""

	// ① 视觉模型看图(最准:描述的是这张图里的角色,而不是名字听起来的样子)
	vlm := llm.New(g.VLM.BaseURL, g.VLM.APIKey, g.VLM.Model, 180)
	if vlm.Configured() {
		if dataURL, err := s.imageDataURL(ctx, master); err == nil {
			const sys = `你是角色设定师。看参考图,输出严格 JSON(无 markdown 包裹):` +
				`{"description":"中文外貌/服装/气质描述,80-120字,具体可画,包含发型发色","image_prompt":"英文文生图提示词,半身定妆照(面部特征+发型+服装+气质+光线),120词内"}`
			out, err := vlm.ChatVision(ctx, sys, []llm.VisionContent{
				{Role: "user", Text: "角色名:" + name + "。请据此图输出设定。", Images: []string{dataURL}},
			}, true)
			if err == nil {
				if json.Unmarshal([]byte(trimJSON(out)), &card) == nil && card.ImagePrompt != "" {
					via = "vlm"
				}
			} else {
				s.opLog("⚠ 视觉模型描述失败,退回文本模型:%v", err)
			}
		}
	}
	// ② 文本模型兜底(无视觉能力/未配置 VLM)
	if via == "" {
		client := llm.New(g.LLM.BaseURL, g.LLM.APIKey, g.LLM.Model, 180)
		if !client.Configured() {
			writeErr(w, 400, fmt.Errorf("需要配置 LLM(或 VLM)才能自动生成描述与提示词"))
			return
		}
		out, err := client.Chat(ctx, []llm.Message{
			{Role: "system", Content: "你是角色设定师。输出严格 JSON(无 markdown 包裹):" +
				`{"description":"中文外貌/服装/气质描述,80-120字","image_prompt":"英文文生图提示词,半身定妆照(面部+发型+服装+光线),120词内"}`},
			{Role: "user", Content: "角色名:" + name + "\n现有描述(可为空,可扩写):" + e.Description},
		}, true)
		if err != nil {
			writeErr(w, 500, fmt.Errorf("生成失败:%w", err))
			return
		}
		if err := json.Unmarshal([]byte(trimJSON(out)), &card); err != nil || card.ImagePrompt == "" {
			writeErr(w, 500, fmt.Errorf("生成结果解析失败(模型返回异常)"))
			return
		}
		via = "llm"
	}
	updated, err := s.lib.SetMeta(kind, name, card.Description, card.ImagePrompt)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	s.opLog("🪄 库资产「%s」描述与提示词已生成(%s)", name, map[string]string{"vlm": "视觉模型看图", "llm": "文本模型"}[via])
	writeJSON(w, 200, map[string]any{"entry": updated, "via": via})
}

// imageDataURL 把本地图片压成 data URL 供视觉模型使用。原图 832×1248 PNG 约 2MB,
// 直接 base64 近 3MB —— 缩到 540 宽既省额度也避开部分平台的单图体积上限。
func (s *Server) imageDataURL(ctx context.Context, path string) (string, error) {
	tmp, err := os.CreateTemp("", "mx-desc-*.jpg")
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if _, err := s.ff.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
		"-i", path, "-vf", "scale=540:-2", "-frames:v", "1", "-q:v", "3", tmp.Name()); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(tmp.Name())
	if err != nil || len(raw) == 0 {
		return "", fmt.Errorf("图片压缩失败")
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(raw), nil
}

// genLibraryPortrait 用本地 ComfyUI(Krea-2)生成资产库定妆照,返回临时文件路径。
//
// 提示词在这里按**实体类型**补上取景、忠实度与画风块(与项目内 characterImagePrompt
// 同一套渲染方法):库是跨项目全局资产,库里把铜雀当人出图,复用到任何项目都会跟着错。
func (s *Server) genLibraryPortrait(ctx context.Context, name, description, prompt string, seed int64) (string, error) {
	eng, err := s.reg.Get("comfy")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "mx-lib-*.png")
	if err != nil {
		return "", err
	}
	tmp.Close()
	out := tmp.Name()
	if _, err := eng.GenerateImage(ctx, pipeline.LibraryPortraitPrompt(name, description, prompt), "", seed, 832, 1248, out); err != nil {
		os.Remove(out)
		return "", err
	}
	return out, nil
}

// trimJSON 剥掉 LLM 可能残留的 markdown 围栏与前缀。
func trimJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "}"); j >= 0 {
		s = s[:j+1]
	}
	return s
}
