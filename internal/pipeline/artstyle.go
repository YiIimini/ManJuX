package pipeline

import "strings"

// artstyle.go 角色画风的**硬约束**:动漫化(非真人脸)+ 哑光(去"油腻")+ 不影射真人。
//
// 为什么单独成文件、且分成 image / render 两块(2026-09-24 用户四项报障之①④):
//
//	①「渲染的视频人物油腻」—— 油腻不是"皮肤太亮"这么简单,它是三件事叠出来的:
//	   真人照片先验(毛孔/皮脂高光)+ 模型的美颜先验(光滑无瑕)+ 提示词里的强光描述
//	   (侧逆光/轮廓光把额头鼻梁打成高光斑)。所以要在**两头**同时压:定妆照(Krea-2)
//	   不能画出油光,渲染(H3)不能把油光加上去 —— 两处各写一份必然漂移,于是统一到
//	   本文件,由 image/render 两个出口分别取用。
//	④「角色不要真人脸,动漫化,避免侵权」—— 动漫化与去油腻是同一枚硬币:赛璐璐平涂
//	   + 手绘线稿 + 哑光平涂肤色,天然就没有皮脂高光;而"不要真人脸"还多一层法务含义,
//	   必须显式禁止影射任何真实人物/演员/公众人物。
//
// **两块内容不可互换**(踩过的坑):Krea-2 是蒸馏模型(CFG 1.0),负面词不但不生效,
// 还会把名词当指令画出来(见 uniformCivilianClarify 的实测记录:"no epaulettes" 画出了
// 肩章)。所以:
//   - image 块 = **纯正面描述**,一个否定词、一个"photo/照片/毛孔"类名词都不许出现;
//   - render 块 = H3 侧,官方指南允许散文式排除句,才写 "NEGATIVE (must not happen)"。
//
// 两块都**只描述渲染表面与画风**,绝不写五官形状(形状类词汇一律留在角色卡里)——
// 这是 2026-09-12 那次"风格块把圆脸大眼塞给所有角色"事故的教训。

// animatedFaceImageBlock Krea-2(定妆照/Q 版/重抽)用的画风块:**纯正面**。
//
// 只能用"它是什么"来描述:手绘线稿 + 简化的图形化五官 + 赛璐璐平涂 + 宽而软的平涂
// 阴影 + 哑光粉质肤色 + 柔和低对比散射光 + 原创动画设计。一个否定词都不写。
func animatedFaceImageBlock() string {
	return "\n\nANIMATED MATTE CHARACTER (mandatory): " +
		"2D/2.5D animation character design — hand-drawn clean line art, simplified graphic facial features, " +
		"flat cel-shaded colour with wide soft shadow shapes, matte powdery complexion with a dull even finish, " +
		"soft diffuse low-contrast light that falls gently across the form. " +
		"The subject is an original animation design." +
		"(中文对照:动画化角色——手绘线稿 + 简化图形化五官 + 赛璐璐平涂与宽软的平涂阴影 + " +
		"哑光粉质肤色(整体呈均匀的暗哑质感),柔和低对比散射光;角色为原创动画设计。)"
}

// animatedFaceRenderBlock H3(渲染出片/2K 重生成/局部重绘/音频驱动)用的画风块:
// 正面约束 + 散文式排除句(官方指南允许,且实测 H3 吃得进)。
//
// 这里才允许点名"不要什么":油光、湿汗高光、塑料蜡质、真人照片脸、演员明星相似度。
func animatedFaceRenderBlock() string {
	return "\n\nMATTE ANIMATED LOOK (mandatory, applies to every frame of this video):\n" +
		"- Every on-screen character is a stylized animated character: hand-drawn line-art face, simplified graphic " +
		"features, flat cel-shaded colour with wide soft shadow shapes — the look of a 2D/2.5D donghua, not a human being on camera.\n" +
		"- Skin reads MATTE: a flat even complexion under soft diffuse shading. Any highlight on the forehead, nose bridge " +
		"or cheekbones stays dull, wide and low in contrast.\n" +
		"- The surfaces stay dry and powdery throughout: keep the finish dull and even.\n" +
		"- Lighting is soft, even and low-contrast with a gentle falloff, so no face ever carries a hard hot spot.\n" +
		"- Every face is an ORIGINAL animation design.\n" +
		"NEGATIVE (must not happen): oily or greasy skin, wet or sweaty highlights, a glossy plastic or wax finish, " +
		"a photographic or 3D-scanned human face, a live-action actor's face, and any resemblance to a real person, " +
		"celebrity or public figure.\n" +
		"(中文对照:动画化 + 哑光硬约束——手绘线稿、五官简化、赛璐璐平涂;肤色哑光均匀,额头/鼻梁/颧骨的高光宽而暗哑;" +
		"全片不许出现油光、汗湿反光、塑料蜡质感、真人照片脸或任何影射真实人物/演员/公众人物的长相。)"
}

// 开关本身在 project.ScriptParams(StylizedFaces / FacesOn),未表态即开 ——
// 理由见那里的注释:存量项目正是报障的那批,默认关掉等于新约束对它们不可见。

// photorealRequested 风格文本里是否明确要真人/写实画风。
//
// 用途只有一个:开启了「动漫化」时,把这种自相矛盾的风格请求**告知用户**
// (要真人剧请关掉开关)。**不再用它改写提示词** —— 关键词表永远追不上 LLM 的措辞
// (实测本机真实项目的风格是 "gritty rural Chinese period realism",一个关键词都不命中,
// 而它显然是要写实),所以提示词侧改用**无条件介质覆盖**(见 anchorPrompt 的
// MEDIUM OVERRIDE):不管风格怎么写,"人物画法以介质段为准"。
func photorealRequested(style string) bool {
	s := strings.ToLower(style)
	for _, k := range []string{"真人", "写实", "实拍", "realis", "photoreal", "live-action", "live action", "liveaction", "photo-real"} {
		if strings.Contains(s, k) || strings.Contains(style, k) {
			return true
		}
	}
	return false
}
