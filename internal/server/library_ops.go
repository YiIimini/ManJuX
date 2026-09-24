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

	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/library"
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
	// uploadedKind 看图判定的实体类型(空 = 没判出来,退回按名称/描述推断)
	uploadedKind := ""
	// wantWhiten 上传图是否白底化(默认**开**:用户 2026-09-24 定调"上传后也走
	// Qwen-Image-Edit 生成白底角色");显式传 whiten=false 可跳过。
	wantWhiten := true
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
			Whiten      *bool  `json:"whiten"` // nil = 默认白底化
			imagePayload
		}
		if err := readBody(r, &in); err != nil {
			writeErr(w, 400, err)
			return
		}
		name = strings.TrimSpace(in.Name)
		desc = strings.TrimSpace(in.Description)
		prompt = strings.TrimSpace(in.ImagePrompt)
		if in.Whiten != nil {
			wantWhiten = *in.Whiten
		}
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
		if v := strings.TrimSpace(r.FormValue("whiten")); v != "" {
			wantWhiten = v != "0" && strings.ToLower(v) != "false"
		}
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
	note := ""
	if uploaded {
		source = "upload"
		// 白底化(用户 2026-09-24 要求):上传图背景五花八门,而这张主图会被当作
		// 参考图喂进渲染(锁脸锚点),花背景会把布景/光线一起带进成片。
		// 走 Qwen-Image-Edit-2511 图生图把背景换成纯白 —— 只改背景、不动主体。
		//
		// 失败绝不阻断上传:拿不到编辑链路(权重没下齐/ComfyUI 没起)时保留原图入库,
		// 并把原因写进 note 让界面如实告诉用户,而不是假装白底化了。
		if wantWhiten {
			ctx, cancel := context.WithTimeout(r.Context(), genOuterTimeout)
			out, werr := s.whitenUploaded(ctx, name, desc, imgPath)
			cancel()
			if werr != nil {
				note = "已按原图入库(白底化未完成:" + werr.Error() + ")"
			} else if out != "" {
				imgPath = out
				source = "upload-whitebg" // 来源如实记录:白底化过的上传图
			}
		}
	}
	// 上传图自动补「中文描述 + 英文提示词」(用户 2026-09-24:「上传图片直接成角色
	// 定妆照,没有生成对应的中英文提示词」)。提示词不是装饰 —— 重抽一张、换背景、
	// 补多视角、渲染锚定都靠它,空着等于这些能力对该条目全都不存在。
	//
	// 顺序放在白底化**之后**:描述的是最终入库的那张图(白底、主体居中),而不是
	// 用户随手拍的那张;用户自己填了描述就保留他的,只补提示词。
	descNote := ""
	if uploaded {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		d, p, via, ek, derr := s.describePortrait(ctx, name, desc, imgPath)
		cancel()
		if derr != nil {
			descNote = "未生成描述与提示词(" + derr.Error() + ")"
		} else {
			if strings.TrimSpace(desc) == "" {
				desc = d
			}
			prompt = p
			// 类型由**看图**判定:调用方随后落库(多视角/渲染都按它走)
			if ek != "" {
				uploadedKind = ek
			}
			descNote = "描述与提示词已生成(" + map[string]string{"vlm": "看图", "llm": "文本"}[via] + ")"
		}
	}
	if note != "" && descNote != "" {
		note += ";"
	}
	note += descNote

	entry, err := s.lib.Put("characters", name, desc, prompt, imgPath, "", source)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if uploadedKind != "" {
		if e2, kerr := s.lib.SetEntityKind("characters", name, uploadedKind); kerr == nil {
			entry = e2
			if note != "" {
				note += ";"
			}
			note += "类型判定:" + pipeline.EntityKindLabel(pipeline.EntityKind(uploadedKind))
		}
	}
	s.pipe.ReleaseVRAMIfIdle("自定义新建完成")
	writeJSON(w, 200, map[string]any{"entry": entry, "note": note})
}

// whitenUploaded 用 Qwen-Image-Edit-2511 把上传图的背景换成纯白,返回产物路径。
//
// 编辑链路未就绪时返回明确错误(调用方据此保留原图并如实告知)——**不偷懒用
// Krea-2 文生图重画**:那会把用户上传的人重新设计一遍,是"改内容"不是"换背景"。
func (s *Server) whitenUploaded(ctx context.Context, name, desc, imgPath string) (string, error) {
	eng, err := s.reg.Get("comfy")
	if err != nil {
		return "", fmt.Errorf("需要本地 ComfyUI")
	}
	ee, ok := eng.(*comfyengine.Engine)
	if !ok {
		return "", fmt.Errorf("当前引擎不支持图像编辑")
	}
	if err := eng.Available(ctx); err != nil {
		return "", err
	}
	if missing := ee.EditAssets(ctx); len(missing) > 0 {
		return "", fmt.Errorf("图像编辑模型未就绪(%s)", strings.Join(missing, ";"))
	}
	out := imgPath + ".whitebg.png"
	if _, err := ee.EditImage(ctx, comfyengine.ImageEditOptions{
		RefImages: []string{imgPath},
		Prompt:    pipeline.WhiteBgPrompt(name, desc),
		Negative:  pipeline.WhiteBgNegative(),
		UseTurbo:  true,  // 4 步 Lightning:换背景这种小改动够用
		UseAngles: false, // 转角度 LoRA 与"换背景"无关
	}, out); err != nil {
		return "", err
	}
	if st, serr := os.Stat(out); serr != nil || st.Size() == 0 {
		return "", fmt.Errorf("白底化未产出有效图片")
	}
	return out, nil
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
		// 生效实体类型:① 库里已记的**看图判定** → ② 现判一次(有 VLM 时)并落库
		// → ③ 按名称/描述推断。用户报的正是缺①②时的后果:上传一张器物图,名字
		// 像人名,于是按"人"去做原地转身 —— 器物本该转台自转。
		Kind:      s.resolveEntityKind(r.Context(), kind, name, e, prompt),
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
// describePortrait 让模型看图写「中文描述 + 英文提示词」(VLM 看图优先,文本模型兜底)。
// 返回 (描述, 提示词, 走哪条路, 错误);**纯逻辑,不落盘** —— 调用方决定写回哪里。
//
// 抽出来的原因(用户 2026-09-24:「上传图片直接成定妆照,没有生成对应的中英文提示词」):
// 这段逻辑原先只长在「重新生成描述」这个**手动按钮**后面,于是上传入库的条目
// 描述与提示词都是空的 —— 而提示词正是后续"重抽一张""换背景""补多视角"的依据,
// 空着等于这些能力对该条目全都不存在。现在上传时也用同一条逻辑自动补。
func (s *Server) describePortrait(ctx context.Context, name, desc, imgPath string) (string, string, string, string, error) {
	g := s.cfg.Get()
	var card struct {
		Description string `json:"description"`
		ImagePrompt string `json:"image_prompt"`
		// EntityKind 由**看图**判定(用户 2026-09-24:「多角度图没有判断上传图是人
		// 还是其他的」)。搭在同一次视觉调用里问,不额外花钱;文本兜底也会给一个
		// 猜测值,但只有 VLM 看的才算数(见返回值的 via)。
		EntityKind string `json:"entity_kind"`
	}
	via := ""
	// ① 视觉模型看图(最准:描述的是这张图里的角色,而不是名字听起来的样子)
	vlm := llm.New(g.VLM.BaseURL, g.VLM.APIKey, g.VLM.Model, 180)
	if vlm.Configured() && strings.TrimSpace(imgPath) != "" {
		if dataURL, err := s.imageDataURL(ctx, imgPath); err == nil {
			const sys = `你是角色设定师。看参考图,输出严格 JSON(无 markdown 包裹):` +
				`{"description":"中文外貌/服装/气质描述,80-120字,具体可画,包含发型发色",` +
				`"image_prompt":"英文文生图提示词,半身定妆照(面部特征+发型+服装+气质+光线),120词内",` +
				`"entity_kind":"这张图里的主体属于哪一类:human(人)/object(器物·机关·傀儡·雕像·灯笼这类非生命体)/spirit_pet(小型灵物·宠物)/beast(大型或凶猛的非人生物)/incorporeal(鬼魂·灵体·无形体)/chibi(Q版大头身)。只填这一个英文键值。"}`
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
			return "", "", "", "", fmt.Errorf("需要配置 LLM(或 VLM)才能自动生成描述与提示词")
		}
		out, err := client.Chat(ctx, []llm.Message{
			{Role: "system", Content: "你是角色设定师。输出严格 JSON(无 markdown 包裹):" +
				`{"description":"中文外貌/服装/气质描述,80-120字","image_prompt":"英文文生图提示词,半身定妆照(面部+发型+服装+光线),120词内","entity_kind":"human/object/spirit_pet/beast/incorporeal/chibi 之一"}`},
			{Role: "user", Content: "角色名:" + name + "\n现有描述(可为空,可扩写):" + desc},
		}, true)
		if err != nil {
			return "", "", "", "", fmt.Errorf("生成失败:%w", err)
		}
		if err := json.Unmarshal([]byte(trimJSON(out)), &card); err != nil || card.ImagePrompt == "" {
			return "", "", "", "", fmt.Errorf("生成结果解析失败(模型返回异常)")
		}
		via = "llm"
	}
	if strings.TrimSpace(card.Description) == "" {
		card.Description = desc
	}
	// 归一化 + 校验:模型可能回 "object(器物·机关)" 这类带解释的串,或回一个
	// 不存在的类型 —— 认不出就不记(调用方退回文本推断),不硬塞一个错类型。
	ek := ""
	if k := pipeline.NormalizeEntityKind(card.EntityKind); k != "" {
		ek = string(k)
	}
	return card.Description, card.ImagePrompt, via, ek, nil
}

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
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	d, p, via, ek, derr := s.describePortrait(ctx, name, e.Description, master)
	if derr != nil {
		writeErr(w, 400, derr)
		return
	}
	card := struct {
		Description string `json:"description"`
		ImagePrompt string `json:"image_prompt"`
	}{d, p}
	updated, err := s.lib.SetMeta(kind, name, card.Description, card.ImagePrompt)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// 手动"重新生成描述"同样带类型判定(看图更准),判出来就落库
	if ek != "" {
		if e2, kerr := s.lib.SetEntityKind(kind, name, ek); kerr == nil {
			updated = e2
		}
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

// resolveEntityKind 库条目的**生效实体类型**:看图判定优先,文本推断兜底。
//
// 为什么不能只看名字/描述(用户 2026-09-24:「多角度图没有判断上传的图片是人还是
// 其他的」):上传图的描述可能是空的,名字又常常像人名 —— 文本推断会把一张铜雀判成
// "人",于是多视角走原地转身(器物本该转台自转),渲染的身份锁也用错条款。
//
// 只在**需要时**判一次(有 VLM、且库里没记过),判出来就落库:后续补多视角/重抽
// 直接用,不会每次都花钱,也不会同一张图两次判出不同类型。
func (s *Server) resolveEntityKind(ctx context.Context, category, name string, e *library.Entry, prompt string) pipeline.EntityKind {
	if e != nil && strings.TrimSpace(e.EntityKind) != "" {
		if k := pipeline.NormalizeEntityKind(e.EntityKind); k != "" {
			return k
		}
	}
	// 文本推断先算一份,作为"判不出来"时的兜底
	fallback := pipeline.InferEntityKind(name, e.Description, prompt)
	g := s.cfg.Get()
	vlm := llm.New(g.VLM.BaseURL, g.VLM.APIKey, g.VLM.Model, 120)
	if !vlm.Configured() || s.lib == nil {
		return fallback
	}
	_, master, ok := s.lib.Match(category, name)
	if !ok {
		return fallback
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	dataURL, err := s.imageDataURL(cctx, master)
	if err != nil {
		return fallback
	}
	const sys = `看参考图,只回答一个英文键值(不要 JSON、不要解释):` +
		`human | object | spirit_pet | beast | incorporeal | chibi。` +
		`判据是这张图里的主体**事实上是什么**,不是它有没有名字、会不会说话:` +
		`器物/机关/傀儡/雕像/灯笼 → object;小型灵物宠物 → spirit_pet;` +
		`大型或凶猛的非人生物 → beast;鬼魂/灵体/无形体 → incorporeal;Q版大头身 → chibi。`
	out, cerr := vlm.ChatVision(cctx, sys, []llm.VisionContent{
		{Role: "user", Text: "这是角色「" + name + "」的参考图。它属于哪一类?", Images: []string{dataURL}},
	}, false)
	if cerr != nil {
		s.opLog("⚠ 类型判定(看图)失败,按描述推断:%v", cerr)
		return fallback
	}
	k := pipeline.NormalizeEntityKind(out)
	if k == "" {
		return fallback
	}
	if _, err := s.lib.SetEntityKind(category, name, string(k)); err == nil {
		s.opLog("🏷 库资产「%s」类型由看图判定为:%s", name, pipeline.EntityKindLabel(k))
	}
	return k
}
