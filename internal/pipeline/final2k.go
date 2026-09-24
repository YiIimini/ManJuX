package pipeline

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 成片 2K 定稿的提示词与参考图(审计 P0-7)。
//
// 【为什么单独一个文件】"会出像素"的路径共五条:主渲染、单镜 2K、**成片 2K**、
// 局部重绘、音频驱动。前四条里的三条(单镜 2K/局部重绘/音频驱动)都走
// ShotRenderPrompt,而**成片 2K** 曾经用的是一句裸字面量提示词:
//
//	"Upscale and refine this video to 2K resolution with In-context Regeneration;
//	 keep all subjects, motion, timing, dialogue and audio identical;
//	 enhance textures, skin detail and micro-structures."
//
// 后果不是"画质没提升",而是**方向性错误**:一整部哑光赛璐璐的片子被送去云端
// 按"写实皮肤先验"重画一遍 —— 付费把全片改成油光写实,而且 `refs` 声明了却从未赋值,
// 连身份锚定都没有。所以这条路径必须和其余四条共用同一套介质锁。

// 成片 2K 的参考图上限。
//
// 为什么有上限:请求体里已经内嵌了整条 base_video 的 base64(源片可达 50MB),
// 再加十几张参考图只会撑大请求而无收益。身份锚定靠的是"主要角色的锐利正面照",
// 短剧主要角色通常 3-4 人;这里按出场顺序取前 4 个,够用且可预期。
const final2KMaxRefs = 4

// final2KPrompt 成片 2K 的提示词 —— 与其余四条出像素路径**共用同一套介质锁**
// (styleAndMediumBlock 里的 MEDIUM OVERRIDE + animatedFaceRenderBlock)。
//
// facesOn 与项目开关同源(script.stylized_faces;未表态 = 开):关掉开关要拍真人剧的
// 项目,这里就不许强塞动画介质 —— 那是另一种方向性错误。
func final2KPrompt(proj *project.Project, plan *project.Plan) string {
	facesOn := true
	if proj != nil {
		facesOn = proj.Config.Params.Script.FacesOn()
	}
	var sb strings.Builder
	sb.WriteString("Upscale and refine this entire video to 2K resolution with In-context Regeneration. " +
		"Keep every subject, face, costume, colour, motion, timing, dialogue and the audio track identical — " +
		"this is a resolution pass, not a re-shoot.\n")
	if facesOn {
		// 关键的一句:把"增强细节"翻译成动画语境下的"锐化线稿与平涂边缘",
		// 而不是"补皮肤毛孔/高光"。旧文案里的 enhance ... skin detail 正是把
		// 动画脸改成油光写实脸的指令源。
		sb.WriteString("REFINE, DO NOT REDESIGN (mandatory): keep the flat cel-shaded fills and the hand-drawn line art " +
			"exactly as they are; sharpen the line art and the cel edges, keep large flat areas clean, even and matte. " +
			"Never add pores, oily or greasy highlights, specular shine, photographic micro-texture or film grain, " +
			"and never turn a character into a photographed human being.\n" +
			"(中文对照:本次是分辨率提升,不是重画 —— 保持赛璐璐平涂与手绘线稿不变,只锐化线稿与平涂边缘;" +
			"严禁补毛孔、油光高光、镜面反光、照片级微纹理或胶片颗粒,严禁把角色画成被拍摄的真人。)\n")
	} else {
		sb.WriteString("REFINE, DO NOT REDESIGN: improve sharpness and clarity naturally, keeping the source's own " +
			"look, grading and grain character; do not stylise, do not repaint.\n")
	}
	// 风格 + 介质覆盖(与分镜路径同一段实现,不另写一份)
	sb.WriteString(styleAndMediumBlock(plan, facesOn))
	return sb.String()
}

// final2KRefs 成片 2K 的身份锚定参考图:按镜头出场顺序取主要角色的定妆照。
//
// 为什么只取定妆照、不取转台图:定妆照是这张脸最锐的锚点(转台图是 hstack 拼合图,
// 缩放后每格只剩约 1/4 像素 —— 与 AssetsParams.CompactRefs 里记录的同一条实测结论)。
// 成片级请求只做"别把脸换掉",锐利锚点比多角度更有用。
func final2KRefs(projectDir string, plan *project.Plan, assets map[string]project.Asset) []engine.RefImage {
	if plan == nil {
		return nil
	}
	seen := map[string]bool{}
	var refs []engine.RefImage
	for _, s := range plan.Shots {
		if s.Mode != "ref2va" {
			continue
		}
		for _, cid := range s.Characters {
			if seen[cid] || len(refs) >= final2KMaxRefs {
				continue
			}
			a, ok := assets["char:"+cid]
			if !ok || strings.TrimSpace(a.File) == "" {
				continue
			}
			path := filepath.Join(projectDir, a.File)
			if _, err := os.Stat(path); err != nil {
				continue // 资产缺失就不锚定它,不阻塞成片升级
			}
			seen[cid] = true
			refs = append(refs, engine.RefImage{Kind: "character", Path: path})
		}
		if len(refs) >= final2KMaxRefs {
			break
		}
	}
	return refs
}
