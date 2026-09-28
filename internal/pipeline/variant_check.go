package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/qc"
)

// variant_check.go 跨期造型档的**两向复核**（用户 2026-09-28：
// 「主角不同时期的人物角色会存在差异，但是面板相似度不能走样」）。
//
// 这句话把判据一分为二，缺任何一半都是错：
//
//	① **该变的地方必须真变** —— 一个十九岁的少年和一个三十一岁的守碑人，不能是同一张图
//	   （造型档走 img2img denoise 0.6 派生，denoise 被调低或提示词没写清时，派生图会几乎等于主卡，
//	   用户看到的"不同时期"其实一模一样）；
//	② **不该变的地方一个字都不许变** —— 骨相、眉形、眼形与瞳色、鼻唇形、肤色族、身量族、
//	   以及全部签名记号（疤/烙印/痣/缺牙）。这些丢了，观众认不出是同一个人 = 角色被**悄悄换角**。
//
// 两个方向用两种工具，各打各的：
//   - ① 用 **SSIM**（ffmpeg，确定性、免费）：派生图与主卡几乎逐像素相同 ⇒ 报"该变的没变"。
//     不用 VLM 判，是因为 VLM 对"同一个人的两张不同图"必然回"同一个人"，问不出"变没变"。
//   - ② 用 **VLM 同人判定**（与撞脸自检同一套 qc.DetectSamePerson）：判定为"不是同一个人" ⇒ 报
//     "跨期漂成了另一个人"。这一条与撞脸自检的方向**正好相反**，所以不能共用同一次判定：
//     撞脸怕的是"太像"，跨期怕的是"不像"。
const (
	// variantSameSSIM 派生图与主卡的 SSIM 高于此值 ⇒ 认定"该变的没变"。
	//
	// 0.90 的依据：实测同一角色换装的派生图 SSIM 落在 0.6–0.8（构图与光照都变了），
	// 而"几乎没动"的派生（denoise 过低/提示词没写变化）会到 0.95 以上。0.90 把两者分开，
	// 且**宁可不报**（误报会让人不再看这条告警）。
	variantSameSSIM = 0.90
)

// checkVariantIdentity 对**造型档/跨期卡**做两向复核，返回(是否已换种子重出, 说明)。
//
// 只在 kind=="characters" 且这张卡确实是"造型档"（方案里存在它的基名卡）时才做：
// 不是造型档的卡走撞脸自检那条路，两条各管一事。
func (c *Coordinator) checkVariantIdentity(ctx context.Context, proj *project.Project,
	assets map[string]project.Asset, kind, id, name, path string,
	gen assetGenerator) (string, bool) {

	if kind != "characters" || ctx.Err() != nil || strings.TrimSpace(path) == "" {
		return path, false
	}
	if !proj.Config.Params.QC.Enabled {
		return path, false
	}
	// 找主卡：同族的**非造型档**那张（口径与派生路径完全一致 —— 复用 familyMasterInAssets，
	// 不另写一套"谁是主卡"的判断，否则派生用一张、复核用另一张，两边永远对不上账）
	idKey, nameKey := project.CharFamilyKeys(id, name)
	if idKey == "" && nameKey == "" {
		return path, false
	}
	base, ok := familyMasterInAssets(assets, "char:"+id, idKey, nameKey)
	if !ok || strings.TrimSpace(base.File) == "" || base.File == path {
		return path, false // 不是造型档（或主卡还没有图）：两条复核都无从谈起
	}
	// 主卡的图落在项目目录里（assets 里存的是相对路径）
	basePath := filepath.Join(c.store.Dir(proj.ID), base.File)
	if !fileReadable(basePath) {
		return path, false
	}

	// ── ① 该变的没变（SSIM）──────────────────────────────────────────────
	if c.ff != nil {
		if score, err := c.imageSSIM(ctx, basePath, path); err == nil && score >= variantSameSSIM {
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
				"⚠ 造型档「%s」与主卡**几乎逐像素相同**(SSIM %.3f)——不同时期该有的差异没体现出来:"+
					"年龄感/发型/服装/状态至少要有一样真的变了，否则观众看不出这是两个时期（检查派生 denoise 与卡片的时期描述）",
				name, score)
		}
	}

	// ── ② 不该变的变了（VLM 同人判定，期望"是同一个人"）────────────────────
	client := TurnaroundVerifier(c.cfg.Get().VLM)
	if client == nil {
		return path, false
	}
	if c.vlmSkipReason(proj) != "" {
		return path, false
	}
	v, err := qc.DetectSamePerson(ctx, client, basePath, path)
	if err != nil {
		if !llm.IsRateLimited(err) {
			c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
				"⚠ 跨期一致性未判定(不当作通过):与主卡比对失败:%v", err)
		} else {
			c.vlmNote(proj, err)
		}
		return path, false
	}
	if v.Same {
		return path, false // 该认得出还是同一个人 ✓（这是期望结果，不播报，免得刷屏）
	}
	// 判成"不是同一个人" ⇒ 骨相或签名记号被画丢了。**不自动重出**（这里不是"撞脸"那种可自动修的错），
	// 先如实告警并给出可执行的下一步：把它拉回主卡的骨相与记号，只保留时期差异。
	c.emit(proj.ID, PhaseAssets, 0, "warn", 0,
		"⚠ 造型档「%s」被判为**和主卡不是同一个人**(%s)——跨期只能改年龄感/发型/服装/状态，"+
			"骨相(脸型、眉、眼与瞳色、鼻唇)、肤色族与**全部签名记号**必须逐字沿用主卡的 IDENTITY LOCK；"+
			"建议重出这一张(带主卡为参考),不要让它漂成另一个角色",
		name, oneLineNote(v.Note))
	return path, false
}

// imageSSIM 两张图的相似度（0–1，1 = 完全一样）。
//
// 先各自缩到 512×512 再比：SSIM 要求同尺寸，而主卡与派生卡的像素尺寸可能不同
// （造型档走图生图，尺寸随参考图走；换过引擎/画幅的老项目还会不一样）。
// 统一到 512 只是为了让**结构**可比，不比像素尺寸——这正是"这张图变没变"要问的东西。
func (c *Coordinator) imageSSIM(ctx context.Context, a, b string) (float64, error) {
	if c.ff == nil {
		return 0, fmt.Errorf("没有 ffmpeg")
	}
	filter := "[0:v]scale=512:512:force_original_aspect_ratio=decrease,pad=512:512:(ow-iw)/2:(oh-ih)/2,format=gray[aa];" +
		"[1:v]scale=512:512:force_original_aspect_ratio=decrease,pad=512:512:(ow-iw)/2:(oh-ih)/2,format=gray[bb];" +
		"[aa][bb]ssim"
	out, err := c.ff.Run(ctx, "-i", a, "-i", b, "-lavfi", filter, "-f", "null", "-")
	if err != nil && strings.TrimSpace(out) == "" {
		return 0, err
	}
	return parseSSIM(out)
}

// parseSSIM 从 ffmpeg 的 ssim 输出里取 All 分数。
func parseSSIM(out string) (float64, error) {
	i := strings.Index(out, "All:")
	if i < 0 {
		return 0, fmt.Errorf("ssim 输出里没有 All 分数:%s", lastLine(out))
	}
	rest := strings.TrimSpace(out[i+len("All:"):])
	end := strings.IndexAny(rest, " \t\r\n")
	if end > 0 {
		rest = rest[:end]
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
	if err != nil {
		return 0, fmt.Errorf("ssim 分数解析失败(%q):%w", rest, err)
	}
	return v, nil
}

func lastLine(s string) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	if len(ls) == 0 {
		return ""
	}
	return strings.TrimSpace(ls[len(ls)-1])
}

func oneLineNote(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len([]rune(s)) > 120 {
		return string([]rune(s)[:120]) + "…"
	}
	return s
}
