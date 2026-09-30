package pipeline

import (
	"regexp"
	"strings"
)

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
//
// 2026-09-24 第二轮(用户报「铜雀被渲染成人物」)把两块各自再切成**通用介质 + 类型条款**:
// 通用部分(线稿/平涂/哑光/原创设计)对所有实体都成立;而"五官/肤色"只对人类成立 ——
// 一件器物被要求"简化五官、哑光粉质肤色"就是在逼模型给它长一张脸。类型条款见
// entity_prompts.go 的 entityProfiles(每类各自的渲染方法)。

// MediumMarker 图像侧介质块的唯一标记串。
//
// 两处用途:① "已经带过就不再叠"的判据;② **反推一张定妆照走的是电影线还是用户上传的写实图**
// (RealisticForPrompt)—— 没有项目上下文的调用方(资产库「补全多视角」)只能这么判断。
//
// **口径迁移(2026-09-27 用户报障)**:用户问「平台端小说 LLM 解析渲染的内容贴合原文,
// 但为什么是 2D 漫画风格」。查下来两块都在推 2D —— ① 本文件的介质块写死了
// "2D/2.5D animation, hand-drawn clean line art, flat cel-shaded colour";② 平台 LLM 直出
// 的提示词(director)明确要求"必须是动画风格(donghua/anime/cel-shaded),绝不写实"。
// 而技能侧《立项.json》的「渲染风格串」要的是
// "a photoreal 3D surface with IDEALISED, SEMI-STYLISED faces … This is a Chinese period film,
// not a western fantasy, not cyberpunk neon, not japanese anime" —— 两套打架,出图取折中,
// 落在"2D 平涂漫画"。用户 2026-09-26 的规则是「一切创作产物的电影级画面风格必须是国风系」,
// 所以介质块整体迁到**国风电影级 3D 写实 + 半风格化理想脸**(表面写实、脸理想化,
// 既不是真人照片也不是日式 2D 卡通;不影射真人的法务约束原样保留)。
const MediumMarker = "MATTE CINEMATIC RENDER"

// legacyMediumMarker 迁移前的标记串。资产库里**已经存过**的定妆照提示词带的是它,
// 反推判据必须同时认 —— 否则存量资产会被判成"用户上传的写实图",多视角补齐会走错介质。
const legacyMediumMarker = "ANIMATED MATTE RENDER"

// RenderMediumMarker **渲染侧**介质块的唯一标记串。
//
// 为什么和 MediumMarker 分开:两块内容不可互换(image 块是纯正面描述、一个否定词都
// 不许有;render 块才允许写 NEGATIVE),标记串也必须分开 —— 否则"这张定妆照是不是
// 电影线"会被渲染侧的标记误判成真。两侧的标记都由测试钉住(mediumMarker 一致性)。
//
// 它的第二个用途是闸门:所有"会出像素"的路径(主渲染/单镜2K/成片2K/局部重绘/音频驱动)
// 的提示词都必须含它 —— 成片 2K 曾用一句裸字面量绕过全部约束(见 final2k.go 的说明)。
const RenderMediumMarker = "CINEMATIC MATTE LOOK"

// legacyRenderMediumMarker 迁移前的渲染侧标记串(同上,存量产物兼容)。
const legacyRenderMediumMarker = "MATTE ANIMATED LOOK"

// animatedMediumImageBlock Krea-2 侧的**通用介质块**(不含任何身体部位词)。
//
// 只能用"它是什么"来描述:电影级 3D 写实表面 + 真实材质响应 + 柔和动机光 + 自然颗粒 +
// 浅景深 + 哑光。**一个否定词都不写**(Krea-2 是 CFG 1.0 的蒸馏模型,否定词不生效,
// 还会把名词当指令画出来 —— 见本文件顶部 uniformCivilianClarify 的实测记录)。
func animatedMediumImageBlock() string {
	return "\n\n" + MediumMarker + " (mandatory): a cinematic photoreal 3D render — real materials with " +
		"visible craft (aged timber, paper, silk, brocade, brass, lacquer, glazed clay, wet stone), true light " +
		"response on every surface, soft motivated key light with a gentle falloff, natural film grain, " +
		"shallow depth of field, and an overall matte finish whose highlights stay wide, dull and low in " +
		"contrast. The subject is an original design. Surfaces stay blank and unmarked, the backdrop is a " +
		"plain seamless sweep, and the picture holds nothing but the subject itself." +
		// 【2026-09-28 用户报】「说了角色定妆照和多视图里不能生成文字」。
		//
		// **注意这里写的是正向句，不是 "no text"** —— 图片侧跑的是蒸馏模型（CFG 1.0），
		// 实测它对否定词免疫、却会把名词当指令画出来（"no epaulettes" 直接画出肩章，
		// 见 uniformCivilianClarify 的记录与 artstyle_test.go 的门）。所以"不许出现文字"
		// 在图片侧**不能直译**："no lettering / no watermark" 只会把 lettering 和 watermark
		// 画进去。这里用正向的 blank / unmarked / plain seamless / nothing but the subject
		// 表达同一个意思。
		//
		// 另一半对策是**输入侧**：汉字注解会被模型当"要画的字"照抄（乱码字形），
		// 所以人物卡也要在装配时清汉字 —— 见 PortraitPrompt 里那条 StripCJK。
		"(中文对照:电影级 3D 写实渲染——真实材质与工艺痕迹(旧木/纸/丝/锦/铜/漆/陶/湿石)、" +
		"真实受光响应、柔和动机光与平缓衰减、自然胶片颗粒、浅景深,整体哑光、高光宽而暗哑;主体为原创设计。" +
		"画面表面保持**空白无标记**、背景为**无缝纯净**的一整片,画面里只有主体本身。)"
}

// ChibiMediumMarker Q 版介质块的**标记**（幂等判据 + 机检判据）。
//
// 平台每个介质块都有标记（`MediumMarker` / `RenderMediumMarker`），Q 版块也必须有 ——
// 否则"这段提示词里到底有没有介质块"没法机检，而 `proportion_clause_test.go` 那两条
// 「让位只让比例、介质条款必须留下」的门正是靠标记判的（加这个块的当天就被它们咬住了）。
const ChibiMediumMarker = "CUTE 3D STYLISED RENDER"

// animatedChibiImageBlock **Q 版（呆萌化身）自己的介质块**。
//
// 【2026-09-28 用户要求】「Q 版形象需要**娇小呆萌可爱**」。
//
// 它**不能**用 `animatedMediumImageBlock()` —— 那块 2026-09-28 已被改成
// "电影级 3D 写实"（为了把人物从"半风格化"拉齐到与器物/场景同档）。Q 版跟着用，
// 画出来就是"写实的小人"：既不娇小也不呆萌，与用户要的**完全相反**。
//
// Q 版是**同一个人的另一种画法**（观众靠它认"这段是心里话 / 系统在说话"），
// 所以它要的是**圆润、讨喜、玩具感**的 3D 渲染 —— 与写实介质块并列的**第二套**，
// 不是"写实块的一个变体"。
//
// 只用正向词（图片侧是蒸馏模型，对否定词免疫、却会把名词当指令画出来）。
func animatedChibiImageBlock() string {
	return "\n\n" + ChibiMediumMarker + " (mandatory): a cute stylised 3D render — a small, adorable " +
		"mascot-like figure with a rounded silhouette, a big head on a small body, big expressive eyes, " +
		"soft matte toy-like surfaces, chunky simplified shapes and a gentle rim light." +
		"(中文对照:可爱风格化 3D 渲染——**娇小呆萌**的吉祥物式小人:圆润轮廓、大头小身、大眼睛、 " +
		"柔和哑光的玩具质感、块面化简化造型、温和轮廓光。)"
}

// animatedFaceImageBlock Krea-2(人物定妆照/重抽)用的画风块 = 通用介质 + **人类专属条款**。
//
// 【2026-09-28 用户口径变更：要写实，不要"简化五官"】
// 用户原话：「人物渲染的风格还没有道具和场景的风格好，一股陈旧风格！！写实风格不行吗？」
// 病根就是原先那句 —— 道具/场景/灵宠/妖兽/Q 版**全部只用通用介质块**（"cinematic photoreal
// 3D render"），唯独**人**被额外加了 "idealised and **semi-stylised** … **simplified**,
// readable features"。同一个项目里器物与场景是电影级写实，人却被拉回"简化五官"，
// 看着就是旧的、廉价的；而且这条在 2026-09-27 已被实测证实有害 —— 用户报「周廷和主角脸相似」，
// 定位到的力正是 "simplified features 往**一个模板**上拉"（见 PortraitPrompt 尾部的 FaceID 注释）。
//
// 现在保留正向词 `idealised`（本块本来就有，且 artstyle_test.go 钉着不许删），
// 去掉 "semi-stylised / simplified"，改为**与器物、场景同一档的电影级写实**：
// 真实皮肤着色、次表面散射、织物真实受光、同一层颗粒。
//
// **只能用正向句**：图片侧是蒸馏模型（CFG 1.0），对否定词免疫却会把名词当指令画出来，
// 所以"不要真人脸"这类排除句**不写在这里**，它们进的是 H3 那头（animatedMediumRenderBlock）。
func animatedFaceImageBlock() string {
	return animatedMediumImageBlock() + "\n- Human characters are fully rendered with the same cinematic " +
		"photoreal 3D craft as the props and the sets: idealised features with real skin shading, " +
		"subsurface scattering, true light response on skin and fabric, natural film grain and " +
		"shallow depth of field." +
		"(中文对照:人物与器物/场景**同一档电影级 3D 写实**——是理想化但**完整渲染**的角色:" +
		"真实皮肤着色与次表面散射、皮肤与织物的真实受光、全片同一层自然颗粒、浅景深。)"
}

// animatedMediumRenderBlock H3 侧的**通用介质块**:画风 + 质感 + 通用排除句。
//
// 通用排除句里保留"真人照片脸/演员/影射真实人物"三条 —— 它们对任何实体都成立
// (一件器物也不该长着一张明星脸)。另外**显式排除 2D 卡通与手绘动画线稿**:
// 这是 2026-09-27 那次"为什么是 2D 漫画风格"的直接对策 —— 不说清,模型会往
// 训练集里最省事的平涂卡通回落。
//
// 【2026-09-30 用户：「道具生成需要合理啊，不要不同的文章内容输出的道具都是一个样的风格，
// 请根据文章风格进行对应渲染」】这一块原来把**年代与世界观写死成国风**
// （"guofeng period Chinese" / "the visual language is Chinese period cinema"）——
// 于是不管作品是古装、民国、现代都市还是赛博，道具/器物都往国风上靠 ✗。
// 介质块只该锁**渲染工艺**（写实材质、哑光、软光、非真人脸、非平涂卡通），
// **年代与世界观由 STYLE 那一行决定**（`styleExpandFor` 把项目风格词写进提示词）。
func animatedMediumRenderBlock() string {
	return "\n\n" + RenderMediumMarker + " (mandatory, applies to every frame of this video):\n" +
		"- The whole video is a cinematic photoreal 3D render: real material response on every surface, " +
		"a motivated key light with a gentle falloff, natural film grain, shallow depth of field and true-to-life surface detail.\n" +
		"- Every surface reads MATTE: flat even shading, with each highlight staying wide, dull and low in contrast.\n" +
		"- Everything stays dry and even throughout.\n" +
		"- Lighting is soft and low-contrast with a gentle falloff, so nothing ever carries a hard hot spot.\n" +
		// 年代/世界观**照 STYLE 那一行**（不写死国风）：同一个介质块要能服务古装、民国、
		// 现代都市、科幻等不同作品，器物才不会都长成一个样子。
		"- The ERA, the WORLD and the material culture come from the STYLE line of this task and from the written brief — " +
		"every prop, garment and surface must belong to THAT world (a modern setting has modern materials, a period setting has period ones). " +
		"Never default to a generic Chinese-period look when the STYLE line says otherwise.\n" +
		"- Every design in this video is ORIGINAL.\n" +
		"NEGATIVE (must not happen): a photographic or 3D-scanned human face, a live-action actor's face, any resemblance to a real person, " +
		"celebrity or public figure, a glossy plastic or wax finish, flat cel-shaded cartoon rendering, and hand-drawn anime or manga line art."
}

// humanFaceRenderClause 人类专属的渲染条款(脸/皮肤),接在通用介质块之后。
func humanFaceRenderClause() string {
	return "\n- Every human face is idealised and semi-stylised — a clean graphic facial structure with simplified, " +
		"readable features — rendered with real skin texture and the same fine film grain as the rest of the frame.\n" +
		// 【2026-09-29 用户：「这TM国风，阿枝、霍延、钟离给我渲染外国人的脸，卧槽」】
		//
		// 全套提示词里**没有一句说过"这是中国人"** —— 介质块写的是"国风"（指美术风格），
		// 身份块写的是"理想化半风格化面孔"，而亚洲/中国面孔这件事**从来没被点名**。
		// 出图模型在没被要求时会往"欧美面孔"的默认先验走（实测阿枝/霍延/钟离全成了外国人）。
		// 这条必须写死：**这是中国人的戏**，面孔按东亚结构画。
		"- ETHNICITY (mandatory): every character in this production is Chinese — East Asian facial structure " +
		"(wide-flat facial planes, a softer jawline, almond-shaped or epicanthic-folded eyes, straight jet-black " +
		"hair, warm light-tan East Asian skin tone), exactly as the brief describes that person; each face must " +
		"read as a Chinese period-drama performer, with no Caucasian / European facial structure and no Western " +
		"features anywhere in the cast.\n" +
		"- Skin specifically: matte and even, with no oily or greasy skin, no wet or sweaty highlights, and no hard specular on the forehead, nose bridge or cheekbones.\n" +
		"(中文对照:人物:理想化半风格化面孔(结构清晰、五官简化可读),皮肤有真实质感与与全片一致的细颗粒;" +
		"肤色哑光均匀,额头/鼻梁/颧骨的高光宽而暗哑;全片不许出现油光、汗湿反光、塑料蜡质感、真人照片脸或任何影射真实人物/演员/公众人物的长相。)"
}

// animatedFaceRenderBlock H3(渲染出片/2K 重生成/局部重绘/音频驱动)用的画风块:
// 通用介质块 + 人类条款(默认/人类镜头的组合形态)。
//
// 这里才允许点名"不要什么":油光、湿汗高光、塑料蜡质、真人照片脸、演员明星相似度。
func animatedFaceRenderBlock() string {
	return animatedMediumRenderBlock() + humanFaceRenderClause()
}

// 开关本身在 project.ScriptParams(StylizedFaces / FacesOn),未表态即开 ——
// 理由见那里的注释:存量项目正是报障的那批,默认关掉等于新约束对它们不可见。

// TurnaroundMediumBlock 转身/转台视频的**介质锁**。
//
// 为什么必须单独给这一层(2026-09-24 用户报「角色多视角老是生成错误」的根因):
// 转身视频是**另一次渲染调用**,它不吃分镜那套 styleAndMediumBlock,而
// TurnaroundPrompt 里只有一句轻描淡写的 "same art style"。实测(H3 Ref2V)结果:
//   - 赛璐璐动漫角色 → 被重画成写实真人(换成素色和服、迷彩纹样丢失);
//   - 插画风青铜雀   → 被重画成写实 3D 机械铜鸟(铆钉/玻璃眼/金属板);
//
// 于是逐帧身份校验**正确地**判定"不是同一主体"并丢弃 —— 三个角色连续失败,
// 用户看到的就是"多视角老是生成错误"。判定没错,缺的是介质锁。
//
// realistic=true(用户显式关了「角色动漫化」要拍真人剧)时返回空串:那种情况下
// 强塞动画介质才是错的。
func TurnaroundMediumBlock(kind EntityKind, realistic bool) string {
	if realistic {
		return ""
	}
	switch NormalizeEntityKind(string(kind)) {
	case KindHuman, KindAndroid:
		return animatedFaceRenderBlock() // 通用介质 + 人脸条款
	case KindChibi:
		// **Q 版不吃人脸条款**（2026-09-29 用户：「Q版形象是那种Q版手办动漫形象的…
		// 不是写实的那种，不可爱」）：写实人脸条款要求肤色、毛孔、红晕，等于把 Q 版
		// 拉回写实小人。改用 Q 版自己那套（可爱风格化 3D、玩具质感）。
		return animatedChibiImageBlock()
	default:
		// 器物/灵宠/妖兽/无实体:只给通用介质(给器物写"肤色哑光"等于逼它长一张脸)
		return animatedMediumRenderBlock()
	}
}

// RealisticForPrompt 反推"这张定妆照该用电影介质还是用户自己上传的写实图介质" —— 给**没有项目
// 上下文**的调用方用(资产库「补全多视角」拿不到项目的 stylized_faces)。
//
// 判据是定妆照提示词里有没有我们自己的介质标记串:有 = 它是本应用画的;没有 = 多半是用户
// 上传的真人照片/写实图(那种情况下强塞本应用的介质同样是错的)。
//
// **两个标记串都要认**:资产库里迁移前存下的卡带的是旧的 `ANIMATED MATTE RENDER`,
// 只认新串会把存量资产判成"用户上传图",多视角补齐就会走错介质。
func RealisticForPrompt(imagePrompt string) bool {
	if strings.Contains(imagePrompt, MediumMarker) || strings.Contains(imagePrompt, legacyMediumMarker) {
		return false
	}
	return true
}

// photorealRequested 风格文本里是否明确要真人/写实画风。
//
// 用途只有一个:开启了「动漫化」时,把这种自相矛盾的风格请求**告知用户**
// (要真人剧请关掉开关)。**不再用它改写提示词** —— 关键词表永远追不上 LLM 的措辞
// (实测本机真实项目的风格是 "gritty rural Chinese period realism",一个关键词都不命中,
// 而它显然是要写实),所以提示词侧改用**无条件介质覆盖**(见 anchorPrompt 的
// MEDIUM OVERRIDE):不管风格怎么写,"人物画法以介质段为准"。
// StripNegations 剥掉风格串里的**否定式**条款(", NO GLOSS" / "no wet highlights" /
// "without X" / "avoid X"),保留其余正面描述。
//
// 为什么必须剥(2026-09-25 用户报「动漫化角色还很丑」):上游(技能端)写来的风格串
// 常常自带一串否定 —— 本机那部《全城榜一》方案里就写着
// "…MATTE POWDERY SKIN WITH A DULL EVEN FINISH, **NO GLOSS, NO WET HIGHLIGHTS,
// NO PHOTOREAL DETAIL**"。而 Krea-2 是蒸馏模型(CFG 1.0):**负面词不但不生效,还会把
// 名词当指令画出来**(本文件顶部记录过实测:"no epaulettes" 画出了肩章)。
// 于是"不要油光"被读成"油光"、"不要写实细节"被读成"写实细节" —— 定妆照上那一片片
// 发灰的污渍感、脏兮兮的平涂阴影,正是这么来的。
//
// 只用于**图像侧**(Krea-2);视频侧(H3)允许散文式排除句,不要在这里替它决定。
func StripNegations(style string) string {
	var keep []string
	for _, seg := range strings.Split(style, ",") {
		s := strings.TrimSpace(seg)
		if s == "" {
			continue
		}
		low := strings.ToLower(s)
		neg := false
		for _, pre := range []string{"no ", "not ", "without ", "avoid ", "never ", "do not ", "don't ", "free of ", "zero "} {
			if strings.HasPrefix(low, pre) {
				neg = true
				break
			}
		}
		// 段内的否定短语(如 "skin with a dull finish NO GLOSS")也去掉该短语
		if !neg {
			if i := reNegPhrase.FindStringIndex(s); i != nil {
				s = strings.TrimSpace(s[:i[0]])
				if s == "" {
					continue
				}
			}
		}
		if neg {
			continue
		}
		keep = append(keep, s)
	}
	return strings.Join(keep, ", ")
}

// reNegPhrase 段内否定短语(整词边界,避免削到 "notebook" 这类词)。
var reNegPhrase = regexp.MustCompile(`(?i)\b(no|not|without|avoid|never)\b`)

// photorealRequested 风格文本里是否明确要真人/写实画风。
//
// 判据前先剥否定式:**"NO PHOTOREAL DETAIL" 里的 photoreal 不算要写实**
// (2026-09-25 用户拿着一条"风格里含真人/写实请求"的告警来问 —— 他的风格串里唯一的
// 写实字样就是那个 NO 后面的,属于纯误报;更糟的是它还让 styleExpand 走了"覆盖"分支,
// 把技能端指定的画风术语丢掉换成平台罐头词)。
func photorealRequested(style string) bool {
	s := strings.ToLower(StripNegations(style))
	for _, k := range []string{"真人", "写实", "实拍", "realis", "photoreal", "live-action", "live action", "liveaction", "photo-real"} {
		if strings.Contains(s, k) || strings.Contains(StripNegations(style), k) {
			return true
		}
	}
	return false
}

// StripMicroTextForImage 把提示词里的**微雕文字/极小铭文**改成"模糊磨旧的痕迹"。
//
// 依据(2026-09-25 实测本机定妆照):上游提示词写着
//
//	a thumb-nail-sized brass clip … engraved with a tiny worn '禾' character whose
//	second stroke is almost rubbed away
//
// —— 拇指盖大的铜扣上刻一个几乎磨没的汉字,在 832×1248 的半身幅里根本没有足够像素。
// 模型不会"省略",而是**糊成一团灰渍**(实测那张定妆照胸口与腕部正是一片片发灰的污渍)。
// 这与视频侧"绝不自己发明汉字"是同一条道理的图像版:认不出的字只会变成噪点。
func StripMicroTextForImage(prompt string) string {
	out := reInscribedGlyph.ReplaceAllString(prompt, "$1 with a faint worn mark")
	out = reQuotedGlyph.ReplaceAllString(out, "a faint worn mark")
	return out
}

var (
	// engraved/carved/… with …… 引号里 1-6 个字符(含汉字)
	reInscribedGlyph = regexp.MustCompile(`(?i)\b(engraved|carved|printed|written|stamped|painted|etched|marked|inscribed)[^,.;]{0,60}?["'“”‘’][^"'“”‘’]{1,6}["'“”‘’]`)
	// '禾' character / "字" letter 这类写法
	reQuotedGlyph = regexp.MustCompile(`["'“”‘’][^"'“”‘’]{1,6}["'“”‘’]\s*(?i:character|characters|word|letter|text|glyph)s?`)
)

// PortraitPoseClause 人像幅面的**姿态纪律**。
//
// 依据:同一张定妆照里,模型在"upper-body framing, front-facing"且提示词没写姿态时
// 自作主张画了**双臂抱胸** —— 半身幅里手只占很少像素,于是画成一只连指手套(实测)。
func PortraitPoseClause(kind EntityKind) string {
	switch NormalizeEntityKind(string(kind)) {
	case KindHuman, KindAndroid, KindChibi:
		return " Pose: calm and simple, arms relaxed; keep the hands out of frame or uncomplicated" +
			" (this is a reference sheet, not a pose study — complex hand poses render as blobs at this framing)."
	default:
		return ""
	}
}
