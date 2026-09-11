// library_ops.go 资产库手动运营:抽卡(LLM 随机原创角色+Krea-2 定妆)与自定义上传入库。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/llm"
)

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
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
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

	// Krea-2 定妆照(竖版半身)
	imgPath, err := s.genLibraryPortrait(ctx, card.ImagePrompt, time.Now().UnixNano()&0x7fffffff)
	if err != nil {
		writeErr(w, 500, fmt.Errorf("抽卡定妆失败(Krea-2):%w", err))
		return
	}
	entry, err := s.lib.Put("characters", card.Name, card.Description, card.ImagePrompt, imgPath, "", "gacha")
	if err != nil {
		writeErr(w, 500, err)
		return
	}
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
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
		defer cancel()
		p, err := s.genLibraryPortrait(ctx, prompt, time.Now().UnixNano()&0x7fffffff)
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
	writeJSON(w, 200, map[string]any{"entry": entry})
}

// genLibraryPortrait 用本地 ComfyUI(Krea-2)生成资产库定妆照,返回临时文件路径。
// libraryRegen 重抽库资产:用条目自带的提示词重新生成定妆照(随机 seed)并覆盖入库。
//
// 与"抽卡"的区别:抽卡是抽一个**全新角色**(LLM 现编题材与外貌);重抽是**同一个角色
// 换一张** —— 描述与提示词原样不动,只换随机种子,不满意就再抽一次。
// 重抽会同时清掉旧的多视角原图与转台图(它们对应的是上一张定妆照,已经失效)。
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
	if strings.TrimSpace(e.ImagePrompt) == "" {
		writeErr(w, 400, fmt.Errorf("该条目没有提示词,无法重抽(上传入库的条目请改用「自定义新建」)"))
		return
	}
	seed := time.Now().UnixNano() & 0x7fffffff // 随机种子:每次抽到不同的图
	tmp, err := s.genLibraryPortrait(r.Context(), e.ImagePrompt, seed)
	if err != nil {
		writeErr(w, 503, fmt.Errorf("重抽失败(需要本地 ComfyUI):%w", err))
		return
	}
	defer os.Remove(tmp)

	s.lib.ClearRelated(kind, name) // 旧角度图对应上一张定妆照,已失效
	if _, err := s.lib.Put(kind, name, e.Description, e.ImagePrompt, tmp, e.SourceProject, e.Source); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.opLog("🎲 库资产重抽:%s「%s」", kind, name)
	updated, _, _ := s.lib.Match(kind, name)
	writeJSON(w, 200, updated)
}

func (s *Server) genLibraryPortrait(ctx context.Context, prompt string, seed int64) (string, error) {
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
	if _, err := eng.GenerateImage(ctx, prompt, "", seed, 832, 1248, out); err != nil {
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
