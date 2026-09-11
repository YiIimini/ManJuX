package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

func anchorPrompt(plan *project.Plan, shot *project.Shot) string {
	var anchors []string
	for _, cid := range shot.Characters {
		for _, ch := range plan.Characters {
			if ch.ID == cid && strings.TrimSpace(ch.Description) != "" {
				anchors = append(anchors, fmt.Sprintf("%s(全程同一个人,外貌严格一致):%s", ch.Name, strings.TrimSpace(ch.Description)))
			}
		}
	}
	for _, sc := range plan.Scenes {
		if sc.ID == shot.Scene && strings.TrimSpace(sc.Description) != "" {
			anchors = append(anchors, fmt.Sprintf("场景「%s」(同一地点):%s", sc.Name, strings.TrimSpace(sc.Description)))
		}
	}
	if len(anchors) == 0 {
		return shot.H3Prompt
	}
	head := "【角色/场景一致性锚定(全片统一,以下外貌描述优先级最高,任何画面细节与之冲突时以此为准)】\n" +
		strings.Join(anchors, "\n")
	if strings.TrimSpace(plan.Style) != "" {
		head += "\n【全片视觉风格】" + strings.TrimSpace(plan.Style)
	}
	return head + "\n\n" + shot.H3Prompt
}

// genAssetImage Krea-2 生成单张资产图(角色竖幅半身/场景按画幅方向)。
// bookRoster 收集系列兄弟集(同父目录)已确立的角色名+形象设定与场景名,
// 返回注入分镜提示的锚定文案与角色数。跨集角色一致性的根因修复:同集内
// 镜头间角色稳定(定妆照锚定有效),但每集分镜由 LLM 独立撰写,同一角色的
// 形象描述措辞漂移(穿着/毛色/年龄细节不一致),文字把画面拉离定妆照
// (2026-09-09 用户实证「貅兜兜跨集飘逸」)。单集项目返回空。
func (c *Coordinator) bookRoster(projectID string) (string, int) {
	epDir := c.store.Dir(projectID)
	series := filepath.Dir(epDir)
	if filepath.Base(series) == "projects" {
		return "", 0
	}
	type charInfo struct{ name, desc string }
	chars := []charInfo{}
	scenes := []string{}
	seenC, seenS := map[string]bool{}, map[string]bool{}
	eps, err := os.ReadDir(series)
	if err != nil {
		return "", 0
	}
	for _, e := range eps {
		if !e.IsDir() || filepath.Join(series, e.Name()) == epDir {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(series, e.Name(), "plan", "plan.json"))
		if err != nil {
			continue
		}
		var plan struct {
			Characters []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"characters"`
			Scenes []struct {
				Name string `json:"name"`
			} `json:"scenes"`
		}
		if json.Unmarshal(raw, &plan) != nil {
			continue
		}
		for _, ch := range plan.Characters {
			n := strings.TrimSpace(ch.Name)
			if n == "" || seenC[n] {
				continue
			}
			seenC[n] = true
			if len(chars) < 8 {
				d := strings.TrimSpace(ch.Description)
				if r := []rune(d); len(r) > 110 {
					d = string(r[:110]) + "…"
				}
				chars = append(chars, charInfo{n, d})
			}
		}
		for _, sc := range plan.Scenes {
			n := strings.TrimSpace(sc.Name)
			if n != "" && !seenS[n] {
				seenS[n] = true
				if len(scenes) < 10 {
					scenes = append(scenes, n)
				}
			}
		}
	}
	if len(chars) == 0 && len(scenes) == 0 {
		return "", 0
	}
	var sb strings.Builder
	sb.WriteString("【系列角色一致性·必须遵守】以下角色已在前几集登场并定妆,本集若出现这些角色:①必须严格沿用原名(不得增减字/换同音字/另起别名);②角色表描述与每镜 H3 提示词中的形象必须与下面给出的设定逐项一致(毛色/服饰/年龄/特征细节不得改动):\n")
	for i, ch := range chars {
		fmt.Fprintf(&sb, "%d. %s:%s\n", i+1, ch.name, ch.desc)
	}
	if len(scenes) > 0 {
		sb.WriteString("已确立的场景名(再次出现时沿用原名):")
		for _, n := range scenes {
			sb.WriteString("「" + n + "」")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("本集新登场的人物/场景属正常剧情推进,可另起新名与新设定,但不得与上述既有角色混淆或顶替。")
	return sb.String(), len(chars)
}

func styleExpand(style string) string {
	if strings.TrimSpace(style) == "" {
		return ""
	}
	s := strings.ToLower(style)
	has3D := strings.Contains(s, "3d") || strings.Contains(s, "bjd") || strings.Contains(style, "虚拟人") || strings.Contains(style, "虚拟偶像")
	hasAnime := strings.Contains(style, "国漫") || strings.Contains(style, "动漫") || strings.Contains(style, "动画") || strings.Contains(s, "anime") || strings.Contains(s, "donghua")
	hasComic := strings.Contains(style, "美漫") || strings.Contains(s, "comic")
	hasInk := strings.Contains(style, "水墨") || strings.Contains(style, "国画")
	hasReal := strings.Contains(style, "真人") || strings.Contains(style, "写实电影") || strings.Contains(s, "photoreal")
	hasQ := strings.Contains(style, "q版") || strings.Contains(style, "呆萌")
	var terms []string
	stylized := true
	switch {
	case has3D:
		terms = append(terms, "next-generation 3D CG character render, BJD ball-jointed doll aesthetic, smooth poreless synthetic skin, large glossy stylized eyes, collectible virtual figure look")
		if hasAnime {
			terms = append(terms, "Chinese donghua animation styling, semi-realistic cel-and-CG hybrid shading")
		}
	case hasAnime:
		terms = append(terms, "Chinese donghua (animation) character design, anime-influenced stylized features, clean cel shading")
	case hasComic:
		terms = append(terms, "American comic-book illustration, bold ink outlines, saturated flat colors, halftone dots")
	case hasInk:
		terms = append(terms, "Chinese ink-wash painting style, expressive brush outlines, negative space")
	case hasReal:
		terms = append(terms, "photorealistic cinematic portrait, realistic skin texture") // 真人剧是合法风格
		stylized = false
	default:
		return "" // 无已知关键词:不强行改写用户自定义风格
	}
	if hasQ {
		terms = append(terms, "cute chibi proportions")
	}
	tail := ". Render strictly in this exact art style."
	if stylized {
		tail = ". The output must read as a stylized character render in this exact art style."
	}
	return "\n\nRENDER STYLE (mandatory): " + strings.TrimSpace(style) + " — " + strings.Join(terms, "; ") + tail
}

// castContrastBlock 同剧班底对比块:列出本剧其它角色的关键形象,追加到
// Krea-2 提示词尾部,强制与同剧角色拉开面部差异(2026-09-11 用户定调
// 「不同角色脸不能相似」)。实证:LLM 已写了 fat/scar/narrow face 等
// 差异化特征,但逐字相同的巨型风格前缀+模型帅哥先验把特征吞掉(肥胖
// →清瘦、疤→无疤)。生成时给模型看全剧班底,它才知道"要和谁不同"。
// 注入的提示词随资产入库,库内重生成/跨集复用自带对比上下文。
func castContrastBlock(plan *project.Plan, selfName string) string {
	if plan == nil {
		return ""
	}
	self := strings.TrimSpace(selfName)
	var parts []string
	for _, ch := range plan.Characters {
		n := strings.TrimSpace(ch.Name)
		if n == "" || n == self {
			continue
		}
		d := strings.TrimSpace(ch.Description)
		if r := []rune(d); len(r) > 60 {
			d = string(r[:60]) + "…"
		}
		parts = append(parts, fmt.Sprintf("%s(%s)", n, d))
		if len(parts) >= 6 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n\nCAST DISTINCTNESS — other cast members of this series: " + strings.Join(parts, "; ") +
		". The character depicted here (「" + self + "」) must be clearly distinguishable from every one of them: " +
		"different face shape, age, build, skin tone and unique markers; these identity traits MUST dominate the image; " +
		"never average them into a generic handsome/pretty face."
}

// backfillAssets 渲染前自动补齐缺失的参考图:资产阶段未跑/未生成全时,
// 就地用本地 Krea-2 生成,绝不静默降级纯文生(2026-09-08 逻辑重构:
// 「定妆照没生成就渲染」被用户点名的反直觉行为根治)。
// 资产生成只依赖本地 ComfyUI,与本次渲染引擎选择无关(云端渲染也本地补图)。
var StylePresets = map[string]struct{ Name, Prompt string }{
	"urban_xianxia": {"都市仙侠霓虹", "CHINESE URBAN-XIANXIA, NEON-NOIR WET-STREET LIGHTING, CINEMATIC ANIMATION, GOD RAYS THROUGH SKYSCRAPERS"},
	"guoman_2d":     {"国漫二维动画", "CHINESE 2D ANIME STYLE, CLEAN LINEWORK, VIBRANT CEL SHADING, DONGHUA QUALITY"},
	"live_action":   {"真人实拍质感", "PHOTOREALISTIC LIVE-ACTION CINEMATOGRAPHY, 35MM FILM, SHALLOW DEPTH OF FIELD, NATURAL SKIN TEXTURE"},
	"pixar_3d":      {"3D 皮克斯", "PIXAR-STYLE 3D RENDER, SOFT GLOBAL ILLUMINATION, EXPRESSIVE STYLIZED CHARACTERS, SUBSURFACE SCATTERING"},
	"ink_wash":      {"水墨国风", "CHINESE INK WASH PAINTING STYLE, BRUSH TEXTURE, NEGATIVE SPACE, MISTY MOUNTAINS, TRADITIONAL AESTHETIC"},
	"cyberpunk":     {"赛博朋克", "CYBERPUNK, RAIN-SOAKED STREETS, HOLOGRAPHIC ADS, CHROME AND NEON, BLADE RUNNER MOOD"},
	"ghibli":        {"吉卜力手绘", "GHIBLI-INSPIRED HAND-PAINTED, WARM WATERCOLOR BACKGROUNDS, SOFT WIND AND LIGHT, WHIMSICAL SERENITY"},
	"film_noir":     {"黑色电影", "FILM NOIR, HIGH CONTRAST BLACK AND WHITE WITH ONE ACCENT COLOR, VENETIAN BLIND SHADOWS, SMOKEY"},
	"hongkong":      {"港片胶片", "HONG KONG CINEMA 1990s, SATURATED NEON, TEAL-ORANGE GRADE, ANAMORPHIC FLARES"},
	"claymation":    {"黏土定格", "CLAYMATION STOP-MOTION STYLE, FINGERPRINT TEXTURE, TACTILE HANDMADE CHARM"},
}

// composeStyleHint 风格预设(可组合)拼接 + 用户自由文本。
func composeStyleHint(styles []string, freeHint string) string {
	var parts []string
	for _, k := range styles {
		if p, ok := StylePresets[k]; ok {
			parts = append(parts, p.Prompt)
		}
	}
	out := strings.Join(parts, " + ")
	if strings.TrimSpace(freeHint) != "" {
		if out != "" {
			out += "; "
		}
		out += strings.TrimSpace(freeHint)
	}
	return out
}
