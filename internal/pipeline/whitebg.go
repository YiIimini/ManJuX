package pipeline

import (
	"fmt"
	"strings"
)

// 上传图 → 白底角色图(资产库「自定义上传」的规范化)。
//
// 为什么走 Qwen-Image-Edit-2511:上传的图来自各种地方(手机拍的剧照、网图、
// 视频截图、AI 生成图),背景五花八门 —— 而资产库这张主图会被当作**参考图**喂进
// 渲染(锁脸锚点),花背景会把布景/光线一起带进成片。用**图像编辑**模型把背景换成
// 纯白,是"只改背景、不动主体"的最省事路径:身份/姿势/服装/画风都按像素保住,
// 不像文生图那样重新设计一遍。
//
// 与多视角编辑路径(viewedit.go)的区别:那条是**改视角**,这条是**换背景**;
// 两者共用同一个引擎能力(comfyengine.EditImage),但提示词刻意分开写 ——
// 混在一起会让"转角度"和"换背景"互相污染。

// WhiteBgPrompt 白底化指令(唯一实现点)。
//
// 关键约束与理由:
//   - **不动介质**:用户上传的是真人照片就还他真人照片,是插画就还他插画 ——
//     这里**不加**动画介质块(那会把真人照片改成赛璐璐,是另一种"改内容")。
//     所以它不在"五条出像素路径的介质锁"名单里,而是靠下面这句"同介质"锁住。
//   - **不动主体**:点名脸/发型/服装/配色/姿势全部照抄,避免模型顺手美颜或重画;
//   - **不加道具/不写字**:白底棚拍常见的"补一个背景物件/水印"是这里最容易出的错;
//   - **留边距**:主体四周留白,免得贴边被裁。
func WhiteBgPrompt(name, desc string) string {
	var sb strings.Builder
	sb.WriteString("Replace the background of image 1 with a clean pure white (#FFFFFF) seamless background. ")
	sb.WriteString("Keep the subject EXACTLY as it is: same face, same facial features, same hairstyle, same costume, " +
		"same colours, same pose, same expression and the same body proportions as image 1. ")
	sb.WriteString("This is a BACKGROUND CHANGE only: do NOT restyle, do NOT re-render in another art style or medium " +
		"(if image 1 is a photograph keep it a photograph; if it is an illustration keep it an illustration), " +
		"do NOT beautify, slim, age or de-age the subject, do NOT add or remove any object, prop or person, " +
		"do NOT add text, watermark, logo or frame, and do NOT add a cast shadow on the background. ")
	sb.WriteString("Keep the same lighting on the subject and the same framing; the subject stays centred with a little " +
		"margin around it so nothing is cropped.")
	if d := compactIdentity(name + " " + desc); strings.TrimSpace(d) != "" && len([]rune(d)) < 200 {
		sb.WriteString(fmt.Sprintf("\n(The subject is: %s — but image 1 remains the only authority on its appearance.)", d))
	}
	return sb.String()
}

// WhiteBgNegative 白底化的负面提示(CFG=1 的 4 步档会被忽略,非蒸馏档才生效)。
func WhiteBgNegative() string {
	return "another person, different face, different hairstyle, different costume, changed art style, " +
		"photorealistic version of a drawing, cartoon version of a photo, extra objects, extra person, " +
		"text, watermark, logo, frame, border, heavy shadow on the background, gradient background, " +
		"coloured background, cluttered background"
}
