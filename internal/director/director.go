// Package director 负责「LLM 直出分镜方案」:输入源文本,输出结构化
// 角色/场景/分镜 JSON,其中每镜包含完整的 H3 提示词(六段式/三段式)。
//
// 设计原则(对齐 MiniMax-H3 官方 Prompt Writing Guide 与知识库实践):
//   - 提示词 100% 由大模型直出,执行层不做拼接改写,只做提交与质检;
//   - 英文结构化描述 + 中文台词原样保留([Chinese] 标注);
//   - H3 直出音视频(对白/旁白/环境音/配乐 32kHz 立体声内嵌),无 TTS。
package director

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
)

// SystemPrompt 直出系统提示(知识库 H3 官方规范全量内嵌)。
// SystemPrompt 拆段定义(拼接还原,内容与单轮路径完全一致):
var sysHeader = promptText("sysHeader__YOU_ARE_AN_ELITE")

var sysOutputFull = promptText("sysOutputFull__OUTPUT")

// sysPropRules 关键道具卡（用户口径：**关键物品必须生成道具**，且跨镜不许漂）。
//
// 【2026-09-28 实测病根】某个真实项目的第一集 plan **连 props 字段都没有**：
// 25 个镜头提到碑刀/烙铁/名册，`prop_ids` 全空 —— 于是每一镜都由模型**自己发明**那把刀，
// 长短、缠柄、朱砂线一镜一个样，观众看到的就是"前后镜漂移"。而根因在平台这一侧：
// **直出 schema 里从来没有 props**，LLM 不可能凭空提取它。
//
// 这一段把"什么该收成道具、每件要写什么、怎么保证跨镜一致"讲清楚；跨镜一致靠的是
// 装配层（`pipeline/prop_anchor.go`：把卡片原文逐字锚进引用它的每一镜），不靠 LLM 每镜自觉复述。
var sysPropRules = promptText("sysPropRules__PROP_CARDS_THE_SERIES") + "`id: \"prop_xxx\"`" + promptText("sysPropRules__PROP_CARDS_THE_SERIES_p2") + "`prop_ids`" + promptText("sysPropRules__PROP_CARDS_THE_SERIES_p3")

// sysShotRulesTpl 分镜规则段**模板**:时长数字全部用 {{MIN}}/{{MAX}}/{{CPS}} 占位,
// 由 renderShotRules() 从常量渲染 —— 这段文案里**不许**再出现写死的时长数字。
//
// 为什么改成模板(2026-09-26 平台侧整改清单 §1.3):此前这里写死 "Each shot 4-8 seconds"
// 与 "min 4, max 8",而计划契约是 4-10、云端引擎的下限是 5 秒 —— 三处口径各自漂:
// LLM 直出路径稳定产出 4 秒镜(云端再静默抬成 5 秒,成片比计划长),而 4-8 又比契约的
// 4-10 更窄,长台词镜被迫拆得比需要的更碎。现在文案里的时长与语速都由常量渲染,
// 并有断言盯着(internal/project/plan_contract_ext_test.go 与本包 prompt_duration_test.go)。
//
// 区间取 GenMinShotSeconds-MaxShotSeconds(=5-10)而不是更窄的 5-8:上限就是契约上限,
// 不再用一个比契约更窄的硬区间逼 LLM 多拆镜;"宁可拆短"这个**负载偏好**由下面那句
// "costs grow faster than linear … split a long beat" 表达(它是实测结论,见 CHANGELOG
// 「单镜负载引导」),不靠一个跟契约打架的数字。
var sysShotRulesTpl = promptText("sysShotRulesTpl__SHOT_PLAN_RULES") + "`vo_kind=inner`" + promptText("sysShotRulesTpl__SHOT_PLAN_RULES_p2")

// sysShotRules 渲染后的分镜规则段(单轮 SystemPrompt 与分批 batchSystemPrompt **共用同一段**,
// 不各写一份 —— 写两份就又是一处会漂的口径)。
var sysShotRules = renderShotRules()

// renderShotRules 把时长/语速常量渲染进规则文案(纯函数,便于直接断言)。
//
// 用占位符替换而不是 fmt.Sprintf 的位置参数:这一段有 7 处数字,位置参数一错位
// 就会把"下限"印成"上限",而字符串本身看不出错 —— 名字替换不会错位。
func renderShotRules() string {
	return strings.NewReplacer(
		"{{MIN}}", strconv.Itoa(GenMinShotSeconds),
		"{{MAX}}", strconv.Itoa(MaxShotSeconds),
		"{{CPS}}", strconv.FormatFloat(CharsPerSecond, 'g', -1, 64),
	).Replace(sysShotRulesTpl)
}

// sysViewRules 视角/机位段（2026-09-27 技能侧整改单 §1.1）。
//
// 【为什么单列一段】复核证据：`camera` 100% 由 `shot_size` 决定、只有 2 个取值、**零机位角度**，
// 最长连续 21 镜同景别 —— 观众看到的是"用同一台机器拍完整集"。根因是产出侧从没被要求过写
// 机位角度（只有运镜），而运镜词表里根本没有"从哪看、看多高"。
//
// 9 条词表逐字来自官方 `skills/h3-prompt-writing`，经技能侧
// `references/分镜脚本-多引擎输出规范.md` §二 翻译；**逐字**是硬要求 ——
// 这些是模型侧被训练过的描述短语，改写等于换词表，而且平台侧的契约门按字符串判。
//
// `view` 字段与提示词里的串必须**逐字一致**：字段是自检/审计的入口，提示词是渲染读的东西，
// 两者不一致时自检面板就在骗人（契约里有一条专门判这个）。
var sysViewRules = promptText("sysViewRules__VIEWPOINT_CAMERA_ANGLE_WRITE")

// sysContinuityRules 尾帧状态与跨镜连贯段（2026-09-27 技能侧整改单 §1.2）。
//
// 【为什么单列一段】复核证据：平台侧只有 seam 开关（把上一镜尾帧当下一镜首帧喂进模型），
// 却**没有任何"尾帧长什么样"的写法约束** —— 于是"接上一镜"全靠模型自己猜，接不上时
// 观众看到的是"人突然换了姿势/道具消失/光线换了"。
//
// 三档写法对应三种真实的剪辑关系（同片段内 / 同场景跨片段 / 跨场景）。跨场景**明确不要求**
// 动作连贯：硬接动作只会让新场景的第一镜莫名其妙地接着上一个地点的姿势。
// sysBlockingRules 占位一致性：用户 2026-09-28 指定的口径（源自《【额外】仿真人10S分镜头指令》的
// 「占位一致性规则」），但**按 H3 的格式重写** —— 那套文件是即梦/Seedance 的字段式写法
// （【关键道具】【人物】独立成行），H3 只有一段散文，所以这三样必须写进 detailed_description 的句子里。
//
// 为什么值得单列一段：H3 逐镜独立生成，没有"上一镜记忆"，跨镜一致**全靠每一镜自己写全**。
// 缺了它，道具的持有手、角色的相对站位、特效的落点每一镜都会漂。
var sysBlockingRules = promptText("sysBlockingRules__BLOCKING_PROP_HANDLING_WRITE")

var sysContinuityRules = promptText("sysContinuityRules__SHOT_END_STATE_CROSS")

// sysShotTableRules 官方「标准镜头信息表」段（六列 + STEP 5.5 自检）。
//
// 依据 MiniMax-AI/MiniMax-H3 `skills/3d-animation-short-generator/references/shot-table-spec.md`。
// 为什么并进来：平台的 plan.json **就是**那张镜头表（六列 ↔ 字段对照见
// internal/project/shot_table.go），官方把这张表定为强制步骤、且配了六条自检。
// 其中三条（钩子密度、单镜角色数、空间锚点继承）是模型侧自己能保证的，写进提示词；
// 另三条（时长上限、每秒钟指令覆盖、跨镜连贯链）由契约的硬区间与尾帧状态判据承担。
//
// 最容易被忽略的是**钩子类型**：没有它，"这一集是不是平的"只能靠感觉说；
// 有了它，密度（每三镜至少一个 reveal/reversal/callback）与两端强钩子都是可判的。
var sysShotTableRules = promptText("sysShotTableRules__SHOT_TABLE_THE_OFFICIAL")

var sysH3Format = promptText("sysH3Format__H3_PROMPT_FORMAT_CRITICAL") + "`<d>`" + ` tag is an instruction to VOICE that text. Never write a placeholder tag — ` + "`<d>[Chinese]…</d>`" + `, ` + "`<d></d>`" + `, ` + "`<d>（无）</d>`" + promptText("sysH3Format__H3_PROMPT_FORMAT_CRITICAL_p2") + "`<d>`" + promptText("sysH3Format__H3_PROMPT_FORMAT_CRITICAL_p3")

var sysHard = promptText("sysHard__HARD_CONSTRAINTS")

// sysCastRules 选角差异化(2026-09-11 用户定调「不同角色脸不能相似」):
// 实证 LLM 写了 fat/scar/narrow face 等差异化特征,但 image_prompt 以巨型
// 统一风格前缀开头、身份特征挤尾部,Krea-2 渲染时全部丢弃(肥胖→清瘦、
// 疤→无疤,三张脸收敛成同一款标准帅哥)。规则:身份特征前置主导渲染,
// 全剧角色脸型/年龄/体格/记号互不相同,原文写明的外貌禁止美化抹平。
var sysCastRules = promptText("sysCastRules__CAST_DISTINCTNESS_CRITICAL_CHARACTERS")

// sysSceneRules 场景卡 image_prompt 的写作规则。
//
// 【为什么要有(2026-09-27 补的缺口)】角色卡有一整套规则(sysCastRules:身份前置、
// 发型三要素、原创底线、取景交给渲染器),场景卡却只有 schema 里一句
// "ENGLISH establishing-shot image prompt for this location" —— 场景图长什么样全看
// LLM 当天怎么写。实测《求你们骂我》的场景卡里写着
// "a scattered crowd of villagers in grey and brown cloth",而平台出场景图时按 KindScene
// 的取景纪律追加 "completely deserted and unpeopled"(场景图必须是**空场**:画面里的人
// 一律由角色定妆照负责) —— 同一段提示词里两套要求,模型折中,场景板上就站了人。
//
// 三条:①只写地点本身;②**不写人**(空场是硬要求);③取景/画幅由渲染器加。
// sysDynamicShotRules 分镜必须**显式解除**角色卡上的静态约束（技能侧《平台侧生成规范》§6.9）。
//
// 【为什么单列一段】角色卡与三视图板按规范要求"**无飘动、无动态动作**"（卡是静态图，飘动的衣料
// 会让模型以为在拍视频），而**分镜恰恰相反**：衣料要飘、人物要动、卡上那句"静态微光/静态流光"
// 在视频里可以流动起来。两边的约束方向相反，而先前平台的分镜提示词**一句都没提** ——
// 模型会从参考图（静态卡）里继承那份"站定不动"的气质，成片就是一屋子人僵着说话。
// 这条影响的是**每一镜的画面**，属于"看不见但看得见"的那类默认值：不写出来就自动站错边。
var sysDynamicShotRules = promptText("sysDynamicShotRules__THE_SHOT_MUST_MOVE")

// sysSceneRules 场景卡规则。
var sysSceneRules = promptText("sysSceneRules__SCENE_CARDS_THE_LOCATION")

// animeFaceLock 角色画风锁(注入到用户附加要求最前面)。
//
// 三个"必须"对应三件不同的事:①`style` 字段与每张 image_prompt、每镜 H3 提示词
// 用同一种画风(否则定妆照与视频又是两套);②脸必须是**理想化的半风格化电影脸**
// (结构与五官可读、皮肤有真实质感但不油不亮,不是 2D 平涂卡通、也不是真人照片);
// ③原创设计,不影射任何真实人物 —— 这一条是避免侵权的落点,必须写在分镜层,
// 不能只靠画图模型自觉。
//
// **口径迁移(2026-09-27 用户报障)**:原话是"必须是 2D/2.5D donghua 动画风格
// (clean line art, cel-shaded flat colour, matte powdery skin)" —— 这条**明文要求 LLM
// 写 2D 漫画风**,是"平台端小说 LLM 解析渲染出来是 2D 漫画风格"的直接原因;
// 而技能侧《立项.json》要的是 photoreal 3D + 国风电影,两套打架。用户 2026-09-26 规则
// 「一切创作产物的电影级画面风格必须是国风系」→ 统一到国风电影级 3D 写实 + 半风格化脸。
// 变量名保留(调用点与测试都在用),语义已从"动画脸锁"变成"电影脸锁"。
var animeFaceLock = promptText("animeFaceLock__CHARACTER_STYLE_LOCK_MANDATORY")

// SystemPrompt 单轮直出(完整 schema + 全部规则)。
//
// 是 var 不是 const:分镜规则段由一个值 = renderShotRules()(见 sysShotRules),
// 时长数字全部从常量渲染 —— 常量对了、文案写死照样会漂,所以文案本身也得是算出来的。
// sysFaceVocabRules 五官词表 + 敏感词安全替代（技能侧《平台侧生成规范》§3.2/§2.4，**逐字**吸收）。
//
// 【为什么要单列一段】这两块都是"**改一个词就等于换一个词表**"的东西：
//
//	① 五官词表：模型侧被训练过的是**具体类型名**（鹅蛋脸/丹凤眼/远山眉…）。平台此前只要求
//	   "写清脸型眼型"，LLM 于是爱写 `large bright eyes`、`精致五官`、`绝美` 这类**尺寸词与抽象词**
//	   —— 那正是"全剧一张模板脸"的根源（同一天还报过"周廷和主角脸相似"）。词表把"挑一个具体类型"
//	   变成硬要求，且**同类角色不许都用同一种**。
//	② 敏感词禁止表：赤足/深V/低胸/超模身材/大眼长腿/半透/毛孔可见… 云端会拦，本地也照写
//	   —— 免得换后端才发现。规范给了**安全替代写法**（高领/小立领、立体剪裁、写成头身比而不是"大长腿"），
//	   照抄即可，不是"自己发明一套更保守的说法"。
var sysFaceVocabRules = promptText("sysFaceVocabRules__FACE_VOCABULARY_SAFE_WORDING")

var SystemPrompt = sysHeader + "" + sysOutputFull + "" + sysShotRules + "" + sysViewRules + "" + sysContinuityRules + "" + sysShotTableRules + "" + sysDynamicShotRules + "" + sysHighlightRules + "" + sysBlockingRules + "" + sysH3Format + "" + sysHard + "" + sysCastRules + "" + sysFaceVocabRules + "" + sysPropRules + "" + sysSceneRules

// UserPromptFmt 用户提示模板。
var UserPromptFmt = promptText("UserPromptFmt__SOURCE_TEXT_CHINESE")

// Options 直出选项。
type Options struct {
	MaxShots     int    // 期望镜头数;0=内容驱动(不预设,LLM 按原文节拍自行决定)
	TotalSeconds int    // 期望总时长(秒);0=由镜头数推算
	ExtraHint    string // 用户附加要求(风格/题材)
	Chibi        bool   // 生成 Q 版形象(内心活动用;分镜提示词指引 Q 版 cameo)
	// AnimeFaces 角色动漫化 + 非真人脸(项目 script.stylized_faces,未表态即开)。
	// 开启时在提示词最前面钉一条角色画风锁 —— 分镜这一层不定,后面两头(定妆照
	// 的 image 块、视频的 render 块)都是在给一个已经写成"写实电影感"的方案打补丁。
	AnimeFaces bool
	// ShotLimit 镜头数**安全网上限**(设置页 split.shot_limit;0 = 用 project.MaxShotsCeiling)。
	//
	// 用途只有一个:内容驱动(不指定镜头数)时,LLM 失控吐出几百个镜头要有个闸。
	// 此前这个设置项只有界面、没有读取点(审计 P1-1),而代码里写死 MaxShotsCeiling,
	// 于是"我把上限调到 30"这句话在系统里完全没有效果 —— 典型的假开关。
	ShotLimit int
	// OnBatch 分批生成进度(批 i/n,负责镜号区间);仅分批路径触发。
	OnBatch func(batch, total, fromShot, toShot int)
	// OnNote 非致命提示(降级/兜底时用)。用于把"方案不完整但我们继续了"这类
	// 事实说给用户听 —— 静默降级等于骗人,直接报错又会让整条产线停在一集上。
	OnNote func(format string, args ...any)
	// SettingBrief 设定集摘要（`project.SettingBook.SettingBrief`，2026-09-28）。
	//
	// 为什么放在这里而不是只在会话层：**两条路都要吃它** —— 单轮/分批直出（老路）
	// 同样需要那些硬锚（数字锚、外形锚），否则"现在是哪一年"照样集集不同。
	// 传空串就是"这部戏没有设定集"，行为与从前完全一致。
	SettingBrief string
}

// segment 分批切分计划(轮1 产物,不落盘)。
type segment struct {
	From  int    `json:"from"`
	To    int    `json:"to"`
	Brief string `json:"brief"`
}

// planHead 规划轮产物（"轻量头部"：剧名/角色/场景/道具/切分计划）。
//
// 【2026-09-28 实测抓到的静默丢字段】这个结构体原先**没有 Props** —— 而规划轮是唯一
// 产出道具卡的地方（分镜轮 batchShotOut 只回 shots）。于是模型在分镜里老老实实写了
// `prop_ids`（实测 35 镜里 19 镜带 `prop_zaodao / prop_shidao / prop_taijia / prop_gangzi`
// 这种真道具 id），道具卡却**在解码这一步被无声丢掉**，落盘方案里连 `props` 键都没有。
// 后果：契约的"prop_ids 悬空 ⇒ fail"必然触发，道具一张图也不会出 —— 用户看到的正是
// 「关键物品生成道具啊，前后镜都漂移了」。
//
// 教训（写进注释免得再犯）：**给提示词加字段时，必须同时给解码结构体加字段** ——
// 只改骨架文字，模型写的东西会在解析处消失，而且不报错。
type planHead struct {
	Title      string              `json:"title"`
	Logline    string              `json:"logline"`
	Style      string              `json:"style"`
	Characters []project.Character `json:"characters"`
	Scenes     []project.Scene     `json:"scenes"`
	Props      []project.Prop      `json:"props"`
	// CoreBeats 核心提炼（核心冲突 + 2-3 个大爽点）。**必须与 Plan 同步加**——
	// 这一条正是上面那句教训的第二次应验：加提示词字段不加解码字段，数据会在解析处静默消失。
	CoreBeats []string  `json:"core_beats"`
	Segments  []segment `json:"segments"`
}

// batchShotOut 分镜轮产物。
type batchShotOut struct {
	Shots []project.Shot `json:"shots"`
}

const batchThresholdShots = 6    // 超过 → 分批
const batchThresholdRunes = 1500 // 源文本超长(字)→ 分批

// batchConcurrency 分批出镜的并发度。
//
// 各批之间**没有依赖**（段落边界与各段梗概在规划轮就定好了，见 generateBatched 的说明），
// 串行只是历史写法 —— 实测那一集 36 镜 6 批，光这一段的耗时就是 6 × 单批。
// 上限取 4：一集通常 5-8 批，再高的收益有限，反而容易把接口压到限流（限流一开全线变慢）。
const batchConcurrency = 4
const batchMaxShots = 5 // 每批最多镜数

// defaultContentDrivenShots 内容驱动(MaxShots=0)时的内部兜底镜头数。它仅用于
// 分批阈值判断与每段秒数换算,不会被写进提示词 —— 提示词走
// buildSingleUserPrompt 的内容驱动分支,不预设镜头数。
const defaultContentDrivenShots = 12

// Generate 调用 LLM 直出分镜方案并做结构校验与修复。
//
// 双路径:小任务(≤6 镜且文本 ≤1500 字)单轮直出;大任务走「规划轮 + 分批
// 出镜轮」——单次生成量随批拆分,推理模型单轮压回 1-3 分钟,远离超时与
// max_tokens 截断(2026-09-08 长文超时治理)。
// Generate 分镜入口。sourceText 若含【全局风格】,提取并锁定——跨集风格
// 一致的代码级保障(不依赖 LLM 服从指令,直接覆写)。
func Generate(ctx context.Context, client *llm.Client, sourceText string, opt Options) (*project.Plan, error) {
	// 提取 sourceText 中的全局风格行,锁进 ExtraHint 强制 LLM 沿用
	if locked := extractGlobalStyle(sourceText); locked != "" {
		lock := "STYLE LOCK (MANDATORY): The global visual style for this series is: " + locked +
			". You MUST use this EXACT style in the 'style' field and in ALL character image_prompts. " +
			"Do NOT invent a different style. Character portraits must be in the same art style as the video."
		if strings.TrimSpace(opt.ExtraHint) != "" {
			opt.ExtraHint = lock + "\n" + opt.ExtraHint
		} else {
			opt.ExtraHint = lock
		}
	}
	// 角色画风锁(2026-09-24 用户定调「角色不要真人脸,动漫化,避免侵权」):
	// 放在 ExtraHint **最前面**,优先级高于用户自己写的风格附加要求 —— 分镜这一层
	// 定不下来,后面定妆照/视频两头都是在给"已写成写实电影感"的方案打补丁。
	if opt.AnimeFaces {
		lock := animeFaceLock
		if strings.TrimSpace(opt.ExtraHint) != "" {
			opt.ExtraHint = lock + "\n" + opt.ExtraHint
		} else {
			opt.ExtraHint = lock
		}
	}
	// 内容驱动语义:MaxShots=0 表示「镜头数由原文节拍决定」,不能简单换成
	// 一个具体数字了事 —— 否则提示词里那句 "(%d shots)" 又把数量写死了,与
	// 用户「该多少就多少」的定调相悖。这里先记下原始意图供提示词分支使用,
	// 下面的兜底值只服务分批阈值判断与每段秒数换算。
	contentDriven := opt.MaxShots <= 0
	secondsGiven := opt.TotalSeconds > 0
	if opt.MaxShots <= 0 {
		opt.MaxShots = defaultContentDrivenShots
	}
	if opt.TotalSeconds <= 0 {
		opt.TotalSeconds = opt.MaxShots * 7
	}
	// 两条产出路径**共用**的收尾：归一化 → **自纠一轮**。
	//
	// 【2026-09-28 修一个真缺口】自纠轮（治"一镜到底"这类机器可判定的违规）此前只挂在
	// 单轮直出那条路上，而**分批路径在它之前就 return 了** —— 于是分批生成的方案（正好是长集数、
	// 也正是用户实际在跑的那条路）**从来没跑过自纠**。实证：真方案 35/35 镜一镜到底，
	// 而提示词里明明写着"4 秒以上必须内部切 2-4 个小镜头"—— 不是模型不听话，是**这步没跑**。
	finish := func(plan *project.Plan) (*project.Plan, error) {
		enforceStyleLock(plan, sourceText)
		normalizePlan(plan, opt)
		if n, note := RepairPlan(ctx, client, plan, opt); n > 0 && opt.OnNote != nil {
			opt.OnNote("%s", note)
		}
		return plan, nil
	}

	if opt.MaxShots > batchThresholdShots || len([]rune(sourceText)) > batchThresholdRunes {
		plan, err := generateBatched(ctx, client, sourceText, opt, contentDriven)
		if err != nil {
			return nil, err
		}
		return finish(plan)
	}
	plan, err := generateSingle(ctx, client, sourceText, opt, contentDriven, secondsGiven)
	if err != nil {
		return nil, err
	}
	return finish(plan)
}

// normalizePlan 直出结果进契约之前的**最后一手归一化**（两条产出路径共用，只此一处）。
//
// 目前只做一件事：把"说话人是本方案角色、但不在本镜画面里"的台词标成画外音
// （`os=true`）—— 真跑实测 40 镜里有 5 镜是这种情况，全部报 fail，而**语义上本来就该是
// 画外音**（人在画外喊的那句）。判据与边界见 project.MarkOffscreenSpeakers 的说明：
// 只改这一种，画外音标签与"名字写错了"都不动。
func normalizePlan(plan *project.Plan, opt Options) {
	if plan == nil {
		return
	}
	if n := project.MarkOffscreenSpeakers(plan); n > 0 && opt.OnNote != nil {
		opt.OnNote("把 %d 条「说话人不在本镜画面里」的台词标成画外音（os=true）—— 语义上本来就是画外音，"+
			"标上之后契约不再因此 fail", n)
	}
	// 画外演出的**资产引用**由归一化补全（2026-09-28 实测：一次真跑里 35 镜的 fail 就是
	// 「#11 vo_kind=inner 但 chibi_ids 为空」——模型标了"这是内心镜"，却忘了写演出素材 id，
	// 契约于是 fail，用户看到的是"又说不能渲"）。口径是"字段标了不算，素材必须真在"，
	// 所以这里按该镜出场角色**确定性地**补出 Q版 / 系统素材 id：素材本来就会按角色自动生成
	// （见资产阶段的 Q 版生成），补 id 只是把双方对上号，不改任何创作内容。
	// 分镜里的器物引用**对号**（2026-09-28 实测：14 处 prop_ids 与卡片的 id 拼法不一致，
	// 锚定落空 = 那几镜的刀又变成模型自己编的）。id 拼法是机械约定，平台有名字与英文串，
	// 对号这件事它是唯一能确定做对的角色；对不上就丢引用，宁可不锚也不锚错。
	// **自愈三件**（器物对号 / 补台词标签 / 补画外素材）走同一个入口，
	// 与 project.LoadPlan、cmd/preflight 共用一份实现 —— 三个消费方必须看到同一份方案。
	if hr := project.HealPlan(plan); opt.OnNote != nil {
		if hr.EntityRefs > 0 {
			// 日志是给用户看的：只说"结果 + 不这么做会怎样"，实现细节（谁写名字、谁按 id 取图）
			// 写在 entity_refs.go 的注释里。
			opt.OnNote("✔ 已把 %d 处分镜里的角色/场景对上方案里的卡（否则这些镜头拿不到参考图，会变成纯文生）",
				hr.EntityRefs)
		}
		// 同名两卡：②与③会话各起过一个 id（实测 `char_shade` / `char_shoulder_projection`
		// 都叫「肩头虚影」）—— 并卡必须说出来，否则用户只会看到"怎么渲染两个"。
		if hr.CharDupes > 0 {
			opt.OnNote("✔ 把 %d 张同名重复的角色卡并成了一张（同一个角色有两套 id 会出两张定妆照、拿两张参考图）",
				hr.CharDupes)
		}
		// 字面重合兜底是**猜**（名字与卡片名不完全一致）：必须说出来。
		// 2026-09-29 真机现场：分镜写「归墟环断口维护舱」，场景卡是「归墟环维护舱大厅」——
		// 猜对了这一镜才有环境参考图，猜错了就要用户改分镜里的场景名。
		if hr.EntityRefsLoose > 0 {
			opt.OnNote("⚠ %d 处名字与卡片不完全一致，是按最接近的那张对上的 —— 对错了就把分镜里的名字改成卡片名",
				hr.EntityRefsLoose)
		}
		if hr.EntityRefsUnknown > 0 {
			opt.OnNote("⚠ 有 %d 处引用找不到对应的卡（这些镜头拿不到参考图）—— 群演/临时场景可以不管；要锚定就补进卡片表",
				hr.EntityRefsUnknown)
		}
		if hr.PropRefs > 0 {
			opt.OnNote("把 %d 处分镜里的器物引用**对上了卡片**（模型写的 id 与卡片 id 拼法不一致，"+
				"对不上号的话那几镜的器物又会被重新发明）", hr.PropRefs)
		}
		if hr.Dialogue > 0 {
			opt.OnNote("给 %d 镜补上了缺失的台词标签（计划里有台词、正文里却没有 <d>）——"+
				"不补的话成片会把那几句说成听不懂的话", hr.Dialogue)
		}
		if hr.Voiceover > 0 {
			opt.OnNote("补全 %d 镜的画外演出素材引用（内心镜→该角色的 Q 版、系统镜→机器人＋面板）—— "+
				"模型漏写 id 会让契约 fail，但素材本身是按角色自动生成的，补上即对上号", hr.Voiceover)
		}
		if hr.MusicNA > 0 {
			// 平台政策：每镜配乐一律 N/A（DEV-05）。实测写进提示词后仍有 19 镜带着配乐 ——
			// 所以这一步是**确定性改写**，不是"指望模型自觉"。如实播报改了几处。
			opt.OnNote("把 %d 镜的配乐段改写成 N/A（平台政策：片段内不带配乐，整片只铺一条连续 BGM）—— "+
				"逐镜带曲子会让成片在每个接缝处换调", hr.MusicNA)
		}
	}
}

// extractGlobalStyle 从 sourceText 提取「全局风格:」行的值(截到换行)。
func extractGlobalStyle(sourceText string) string {
	for _, line := range strings.Split(sourceText, "\n") {
		if i := strings.Index(line, "全局风格:"); i >= 0 {
			v := strings.TrimSpace(line[i+len("全局风格:"):])
			if v != "" {
				return v
			}
		}
	}
	return ""
}

// enforceStyleLock 代码级覆写:LLM 若无视 STYLE LOCK 指令自行发明风格,
// 在此处强制回写为全局风格(跨集一致的最终保障)。
func enforceStyleLock(plan *project.Plan, sourceText string) {
	locked := extractGlobalStyle(sourceText)
	if locked == "" || plan == nil {
		return
	}
	if plan.Style != locked {
		plan.Style = locked
	}
}

// styleHint 组装附加要求(单轮/分批共用)。
func styleHint(opt Options) string {
	hint := ""
	if strings.TrimSpace(opt.ExtraHint) != "" {
		hint = "- User style note: " + strings.TrimSpace(opt.ExtraHint)
	}
	// Q 版形象开启时,分镜中角色内心活动可用 Q 版形象插入画面(chibi cameo)
	if opt.Chibi {
		hint += "\n- Each main character has a chibi (Q-version) avatar generated alongside its portrait. " +
			"When a shot depicts a character's INNER monologue / mental reaction, you may describe a chibi cameo " +
			"of that character popping up in frame (cute miniature version, expressive) to visualize the inner activity."
	}
	return hint
}

// contentDrivenPromptFmt 内容驱动模板(MaxShots=0)。与 UserPromptFmt 的关键
// 区别是不写死镜头数:只给总时长,或连时长也交给内容决定,镜头数交由
// sysShotRules 的「镜头数由内容决定、不设上限」
// 按原文节拍自行判定。
var contentDrivenPromptFmt = promptText("contentDrivenPromptFmt__SOURCE_TEXT_CHINESE")

// buildSingleUserPrompt 组装单批直出的用户提示词。contentDriven 时不预设镜头
// 数;secondsGiven 决定要不要给时长约束(用户没给时长则连时长也交给内容决定)。
//
// opt.SettingBrief 非空时**前置**到 user 段最前面（设定集是硬锚：数字锚/外形锚/
// 道具的功能边界都在里面，"前置"是为了让它压过正文里的自由描述）。
func buildSingleUserPrompt(sourceText string, opt Options, contentDriven, secondsGiven bool) string {
	hint := styleHint(opt)
	sourceText = withSettingBrief(sourceText, opt.SettingBrief)
	if !contentDriven {
		return fmt.Sprintf(UserPromptFmt, sourceText, opt.TotalSeconds, opt.MaxShots, hint)
	}
	target := "- Target: short video. Decide BOTH the shot count and the total duration YOURSELF from the source text's natural story beats."
	if secondsGiven {
		target = fmt.Sprintf("- Target: short video, total duration about %d seconds. Decide the shot count YOURSELF from the source text's natural story beats.", opt.TotalSeconds)
	}
	return fmt.Sprintf(contentDrivenPromptFmt, sourceText, target, hint)
}

// withSettingBrief 把设定集摘要拼在正文**之前**（空摘要原样返回，行为与从前一致）。
func withSettingBrief(sourceText, brief string) string {
	b := strings.TrimSpace(brief)
	if b == "" {
		return sourceText
	}
	return b + "\n\n【本集原文】\n" + sourceText
}

func generateSingle(ctx context.Context, client *llm.Client, sourceText string, opt Options, contentDriven, secondsGiven bool) (*project.Plan, error) {
	user := buildSingleUserPrompt(sourceText, opt, contentDriven, secondsGiven)
	plan, err := chatPlan(ctx, client, SystemPrompt, user)
	if err != nil {
		return nil, err
	}
	if err := validateAndNote(plan, opt, contentDriven); err != nil {
		return nil, err
	}
	return plan, nil
}

// validateAndNote 收口"截断必须说出来"这一条(两条生成路径共用)。
//
// 为什么要收口:镜头数上限截断会**丢掉方案结尾**(用户反复强调“不跳段、不省略”),
// 而旧实现是静默截断 —— 用户只看到成片短了,不知道原因,也没人告诉他上限在哪调。
// 收成一个函数,是为了让"新增生成路径"不可能再漏掉这句提示。
func validateAndNote(plan *project.Plan, opt Options, contentDriven bool) error {
	limit := shotLimitOf(opt, contentDriven)
	before := len(plan.Shots)
	if err := validate(plan, limit); err != nil {
		return err
	}
	if dropped := before - len(plan.Shots); dropped > 0 && opt.OnNote != nil {
		opt.OnNote("分镜被镜头数上限收窄:LLM 给出 %d 镜,按上限 %d 镜截断(丢了 %d 镜,方案结尾会短)。"+
			"上限在「设置 → 拆分」的「镜头数安全上限」里调;填 0 = 按内容自决(内置安全网 %d 镜)",
			before, limit, dropped, project.MaxShotsCeiling)
	}
	return nil
}

// shotLimitOf 本次生成的镜头数上限:内容驱动时是安全网(设置页 split.shot_limit,
// 未配置则 project.MaxShotsCeiling),指定镜头数时就是该值。
//
// 为什么上限要可配(审计 P1-1):这个开关此前只在设置页存在、没有任何读取点,
// 而"内容驱动"是默认模式 —— 也就是说绝大多数用户跑的分镜,上限永远是代码里那个
// 120,与界面上写的数字无关。现在它真的生效,并且**仍然**受 MaxShotsCeiling 保护:
// 设置里填一个更大的值不该让安全网失效(那正是安全网存在的意义)。
func shotLimitOf(opt Options, contentDriven bool) int {
	if contentDriven {
		return clampShotLimit(opt.ShotLimit)
	}
	return opt.MaxShots
}

// clampShotLimit 归一化来自设置的镜头上限:>0 且不超过 MaxShotsCeiling 才采用。
func clampShotLimit(n int) int {
	if n <= 0 {
		return project.MaxShotsCeiling
	}
	if n > project.MaxShotsCeiling {
		return project.MaxShotsCeiling
	}
	return n
}

// chatPlan 单次方案请求 + 一次 JSON 修复重试。
func chatPlan(ctx context.Context, client *llm.Client, sys, user string) (*project.Plan, error) {
	raw, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: user},
	}, true)
	if err != nil {
		return nil, err
	}
	plan, err := parsePlan(raw)
	if err != nil {
		// 二次修复:把原始输出连同错误交回 LLM 修正一次
		fix, ferr := client.Chat(ctx, []llm.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: user},
			{Role: "assistant", Content: raw},
			{Role: "user", Content: "Your previous output is invalid JSON or violates the schema. Error: " +
				err.Error() + "\nReturn the corrected STRICT JSON only."},
		}, true)
		if ferr != nil {
			return nil, fmt.Errorf("方案解析失败: %w", err)
		}
		plan, err = parsePlan(fix)
		if err != nil {
			return nil, fmt.Errorf("方案二次解析仍失败: %w", err)
		}
	}
	return plan, nil
}

// parsePlan 从 LLM 输出提取并解析 JSON(容错:剥围栏/前后杂文/截断补全/拆一层包装)。
// 错误信息带原文摘录:方案解析失败时,日志里能直接看出是"截断"还是"格式跑偏"。
func parsePlan(raw string) (*project.Plan, error) {
	obj, ok := extractJSONObject(raw)
	if !ok {
		if fixed, closed := closeTruncatedJSON(obj); closed {
			obj, ok = fixed, true
		}
	}
	if !ok || obj == "" {
		path := dumpRawForDiagnosis(raw, "plan")
		return nil, fmt.Errorf("输出中找不到可解析的 JSON 对象(原文 %d 字)%s；原文已存到 %s",
			len([]rune(raw)), "；首尾摘录:"+snippet(raw, 120), firstNonEmpty(path, "(落盘失败)"))
	}
	s := repairJSONStrings(obj)
	decode := func(x string) (*project.Plan, error) {
		var plan project.Plan
		if err := json.Unmarshal([]byte(x), &plan); err != nil {
			return nil, err
		}
		// 【2026-09-29 真机实测修】这里原来只认 title/shots —— 那是**单轮直出**那份方案的形状。
		// 而「五步专职会话」里 ②人物道具 与 ④场景 的产出**天然没有 title 也没有 shots**
		// （schema 就是 `{"characters":[…],"props":[…]}` / `{"scenes":[…]}`），于是它们
		// 被判成"非法 JSON"→ 回喂重试 → 还是那个形状 → **整段产出被丢掉**。
		// 真机现场（第 01 集那一跑）：
		//
		//	⚠ ④场景提取 会话失败：…原文摘录:{"scenes":[{"id":"scene_maintenance_bay",…
		//	⚠ ②人物道具提取 会话失败：…原文摘录:{"characters":[{"id":"char_qiqi","name":"祁七",…
		//	⚠ 源文本的【人物外形锚表】列了 6 个角色，方案里只出了 0 个 —— 已按锚表原文补进…
		//
		// 后果是**用户直接看到的**：场景卡 0 张（渲染没有环境参考图）、6 个角色全部来自锚表的
		// 兜底卡（英文提示词只有"a Chinese period-film character, aged X to Y"）⇒ 六张定妆照
		// 长得几乎一样，机器人被画成普通古装人。
		//
		// 判据改成"**整份对象一个字段都没有**才算空"：会话形状（只有 characters / 只有 scenes）
		// 合法通过；真正的空 JSON `{}` 仍然报错。少了 shots 由契约门 G002 兜（那是它的活）。
		if strings.TrimSpace(plan.Title) == "" && len(plan.Shots) == 0 &&
			len(plan.Characters) == 0 && len(plan.Scenes) == 0 && len(plan.Props) == 0 {
			return nil, fmt.Errorf("JSON 合法但整份方案为空（title / shots / characters / scenes / props 一个都没有）")
		}
		return &plan, nil
	}
	plan, firstErr := decode(s)
	if firstErr == nil {
		return plan, nil
	}
	for _, inner := range allObjects(s) {
		if inner == s {
			continue
		}
		if p, err := decode(repairJSONStrings(inner)); err == nil {
			return p, nil
		}
	}
	return nil, fmt.Errorf("JSON 解析失败: %v;原文摘录:%s", firstErr, snippet(raw, 120))
}

// validate 做最小硬校验并规整(id 连续、引用存在、时长钳制)。
//
// shotLimit 是**本次生成的镜头数上限**:内容驱动时传 project.MaxShotsCeiling
// (安全网,不是预设值),指定镜头数时传该值。
// 旧实现在这里写死 `> 20 就截断` —— 与 params.go 的 MaxShotsCeiling=120 和
// 「0=内容驱动、该多少就多少」的定调直接冲突:实测一集按原文节拍规划出 32 镜,
// 分批也照 32 镜全跑完了,末了被这行**静默砍成 20 镜** —— 既白花 3 轮 LLM 调用,
// 又正好违反用户反复强调的「不跳段、不省略」。
func validate(p *project.Plan, shotLimit int) error {
	if p.Title == "" {
		return fmt.Errorf("缺少剧名")
	}
	if len(p.Shots) == 0 {
		return fmt.Errorf("分镜为空")
	}
	if shotLimit > 0 && len(p.Shots) > shotLimit {
		p.Shots = p.Shots[:shotLimit]
	}
	sceneIDs := map[string]bool{}
	for _, sc := range p.Scenes {
		if sc.ID == "" {
			sc.ID = "scene_" + strconv.Itoa(len(sceneIDs)+1)
		}
		sceneIDs[sc.ID] = true
	}
	charIDs := map[string]bool{}
	for _, c := range p.Characters {
		if c.ID == "" {
			c.ID = "char_" + c.Name
		}
		charIDs[c.ID] = true
	}
	var validShots []project.Shot
	for i := range p.Shots {
		sh := p.Shots[i]
		// 时长钳到**生成区间**(不是渲染窗口):下限是 GenMinShotSeconds(5),
		// 与云端 ≥5 秒对齐,避免计划与成片时长不一致(清单 §1.3)。
		// 4 秒镜从这里开始不再被产出 —— 那是本次整改的预期结果。
		if sh.Duration < GenMinShotSeconds {
			sh.Duration = GenMinShotSeconds
		}
		if sh.Duration > MaxShotSeconds {
			sh.Duration = MaxShotSeconds
		}
		if sh.Mode == "" {
			sh.Mode = "t2va"
		}
		if len(strings.TrimSpace(sh.H3Prompt)) < 400 {
			return fmt.Errorf("第 %d 镜 H3 提示词过短(<400 字符),疑似截断", sh.ID)
		}
		sh.ID = i + 1
		validShots = append(validShots, sh)
	}
	p.Shots = validShots
	return nil
}

// ShotSubtitle 生成的镜头字幕文本(用于 SRT)。
func ShotSubtitle(sh project.Shot) string {
	return strings.Join(ShotSubtitleParts(sh), "\n")
}

// ShotSubtitleParts 镜头字幕逐条拆分(用于 ASS 分时显示):
// 旁白一段、每段对白一段;单段超长按句读再切,保证单条两行内可读。
func ShotSubtitleParts(sh project.Shot) []string {
	var parts []string
	push := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if len([]rune(s)) > 42 {
			parts = append(parts, splitLong(s)...)
			return
		}
		parts = append(parts, s)
	}
	if strings.TrimSpace(sh.Narration) != "" {
		push(sh.Narration)
	}
	for _, d := range sh.Dialogue {
		if strings.TrimSpace(d.Text) == "" {
			continue
		}
		if strings.TrimSpace(d.Speaker) != "" && d.Speaker != "旁白" {
			push(d.Speaker + ":" + d.Text)
		} else {
			push(d.Text)
		}
	}
	return parts
}

// splitLong 超长段落按句读切到 ~40 字的条。
func splitLong(s string) []string {
	runes := []rune(s)
	var out []string
	var cur []rune
	for _, r := range runes {
		cur = append(cur, r)
		switch r {
		case '。', '!', '?', ';', '~', '…':
			if len(cur) >= 18 {
				out = append(out, string(cur))
				cur = nil
			}
		}
		if len(cur) >= 40 {
			out = append(out, string(cur))
			cur = nil
		}
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

// SanitizeForCache 清洗缓存键(保中文/字母/数字/下划线/连字符)。
var cacheCleanRe = regexp.MustCompile(`[^\p{L}\p{N}_-]+`)

// SanitizeForCache 清洗任意字符串为安全缓存名片段。
func SanitizeForCache(s string) string {
	return strings.Trim(cacheCleanRe.ReplaceAllString(s, "_"), "_")
}

// ---------- 分批生成:规划轮 + 分批出镜轮 ----------

// planSystemPrompt 规划轮:通读全文,只产出轻量头部(剧名/角色/场景/切分计划)。
var planSystemPrompt = sysHeader + promptText("planSystemPrompt__OUTPUT_BATCH_PLAN") + "\n\n" + sysCastRules + "\n\n" + sysFaceVocabRules + "\n\n" + sysPropRules + "\n\n" + sysHighlightRules + "" + sysSceneRules

// batchSystemPrompt 分镜轮:按已定头部与本段 brief,只产出指定区间的 shots。
// var 的理由同 SystemPrompt:内嵌的 sysShotRules 由常量渲染。
var batchSystemPrompt = sysHeader + promptText("batchSystemPrompt__OUTPUT_THIS_BATCH") + sysShotRules + "" + sysViewRules + "" + sysContinuityRules + "" + sysShotTableRules + "" + sysDynamicShotRules + "" + sysHighlightRules + "" + sysBlockingRules + "" + sysH3Format + "" + sysHard + "" + sysFaceVocabRules + "" + sysPropRules + "" + sysHighlightRules + promptText("batchSystemPrompt__OUTPUT_THIS_BATCH_p13")

// ShotPrompts 产出 **shots[]** 的两条直出路径（单轮整体直出 / 分批分镜），供双侧同源测试取用。
//
// 为什么按"产出什么"分组：视角词表与尾帧状态的纪律必须出现在**写 shots 的那两条**路径里，
// 而场景卡纪律必须出现在**写 scenes 的那两条**里（见 ScenePrompts）—— 分成两组，守卫才能
// 各盯各的；混成一个 map 时，测试只能笼统要求"每条提示词都有全部规则"，那不是事实
// （分批分镜轮根本不产出场景卡），也就守不住真正的缺口。
func ShotPrompts() map[string]string {
	return map[string]string{
		"SystemPrompt(单轮直出)":      SystemPrompt,
		"batchSystemPrompt(分批分镜)": batchSystemPrompt,
	}
}

// ScenePrompts 产出 **scenes[]** 的两条路径（单轮整体直出 / 规划轮），供双侧同源测试取用。
//
// 2026-09-27 这条守卫抓到的真实缺口：规划轮（`planSystemPrompt`）是**真正写 scenes[] 的地方**，
// 却只带了角色卡规则、漏了 `sysSceneRules` —— 场景卡的事件隔离与物件配额规则从没进过那条路径。
func ScenePrompts() map[string]string {
	return map[string]string{
		"SystemPrompt(单轮直出)":    SystemPrompt,
		"planSystemPrompt(规划轮)": planSystemPrompt,
	}
}

// buildPlanUserPrompt 组装规划轮的用户提示词。
// 单独成函数是为了让"线上跑的那段提示词"和诊断用的那段**完全同源** ——
// 复现失败时最怕提示词与线上不一致(改一处漏一处,复现出来的是另一个问题)。
func buildPlanUserPrompt(sourceText string, opt Options, contentDriven bool) string {
	hint := styleHint(opt)
	sourceText = withSettingBrief(sourceText, opt.SettingBrief)
	if contentDriven {
		// 内容驱动时不预设总镜头数:由规划轮自己决定拍多少镜才 cover 完整,
		// 只保留「每段 3-N 镜」这个分批粒度 —— 它是技术参数(保证单次输出不溢出)。
		return fmt.Sprintf(promptText("buildPlanUserPrompt__SOURCE_TEXT_CHINESE"), sourceText, batchMaxShots, hint)
	}
	return fmt.Sprintf(promptText("buildPlanUserPrompt__SOURCE_TEXT_CHINESE_p2"), sourceText, opt.MaxShots, opt.TotalSeconds, batchMaxShots, opt.MaxShots, hint)
}

// decodePlanHead 直接解析并校验(错误信息按**缺什么**分别说明)。
func decodePlanHead(s string) (planHead, error) {
	var head planHead
	if err := json.Unmarshal([]byte(s), &head); err != nil {
		return head, fmt.Errorf("JSON 解析失败: %v", err)
	}
	if strings.TrimSpace(head.Title) == "" {
		return head, fmt.Errorf("JSON 合法但缺少 title")
	}
	if len(head.Segments) == 0 {
		return head, fmt.Errorf("JSON 合法但缺少 segments")
	}
	return head, nil
}

// parsePlanHead 解析规划轮输出(标题/角色/场景/分段):
// 提取 → 直解 → 补全截断 → 拆一层包装,四步都失败才报错。
// 第二个返回值表示"这份结果是靠补全截断尾巴得到的"(方案可能不完整),
// 调用方据此决定要不要重问一轮。
// 规划轮是 48 集产线的咽喉,一句笼统的"解析失败"没法修,所以每步都留清楚原因。
func parsePlanHead(raw string) (head planHead, truncated bool, err error) {
	obj, ok := extractJSONObject(raw)
	if !ok {
		if fixed, closed := closeTruncatedJSON(obj); closed {
			obj, ok, truncated = fixed, true, true
		}
	}
	if !ok || obj == "" {
		return planHead{}, false, fmt.Errorf("未找到 JSON 对象(原文 %d 字)", len([]rune(raw)))
	}
	s := repairJSONStrings(obj)
	h, firstErr := decodePlanHead(s)
	if firstErr == nil {
		return h, truncated, nil
	}
	// 包了一层({"plan":{…}} / {"result":{…}} / {"data":{…}})是常见写法,拆一层再试
	for _, inner := range allObjects(s) {
		if inner == s {
			continue
		}
		if h2, err2 := decodePlanHead(repairJSONStrings(inner)); err2 == nil {
			return h2, truncated, nil
		}
	}
	return planHead{}, truncated, firstErr
}

// callPlanRound 规划轮:调用 + 一次修复重问。
//
// 为什么要专门做一层(2026-09-24 用户 48 集产线卡在第 1 集):
//   - 失败几乎都是**格式**问题(被 max_tokens 截断、或先写一段说明再给 JSON),
//     不是模型不会规划 —— 把原文回灌要求"只补发合法 JSON"通常一次就救回来;
//   - 旧实现只回一句"规划轮输出解析失败(缺少 title/segments)",finish_reason、
//     token 用量、原文摘录全丢了,现场无法复盘。这里把它们全部写进错误。
func callPlanRound(ctx context.Context, client *llm.Client, sys, user string, note func(string, ...any)) (planHead, error) {
	res, err := client.ChatEx(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: user},
	}, true)
	if err != nil {
		return planHead{}, fmt.Errorf("规划轮失败: %w", err)
	}
	head, truncated, perr := parsePlanHead(res.Content)
	if perr == nil && !truncated {
		return head, nil
	}
	// 走到这里两种情形:① 解析失败;② 解析成功但靠的是"补全截断的尾巴"
	// —— 后者那份方案大概率不完整(后面的段会落到占位 brief),
	// 所以先花一轮把它问全;真问不回来再用它兜底,不让产线停在第 1 集。
	var fallback *planHead
	if perr == nil {
		h := head
		fallback = &h
	}

	// ---- 修复/补全重问 ----
	prev := res.Content
	if len([]rune(prev)) > 40000 {
		prev = snippet(prev, 20000)
	}
	shrink := ""
	if truncated || res.Finish == "length" {
		shrink = "\nYour previous reply was CUT OFF by the output limit. This time make the plan SHORTER: fewer segments (each up to 5 shots) and terse briefs — still covering the whole story."
	}
	why := "output ended before the JSON object was complete"
	if perr != nil {
		why = perr.Error()
	}
	fixUser := fmt.Sprintf(promptText("callPlanRound__YOUR_PREVIOUS_REPLY_COULD"), why, shrink, prev)
	if fix, ferr := client.ChatEx(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: fixUser},
	}, true); ferr == nil {
		if h2, trunc2, e2 := parsePlanHead(fix.Content); e2 == nil && !trunc2 {
			return h2, nil
		} else if e2 == nil {
			perr, res = nil, fix
			fallback = &h2
		} else {
			perr, res = e2, fix
		}
	}
	if fallback != nil {
		if note != nil {
			note("⚠ 规划轮输出疑似被截断,已用补全后的方案继续(建议本集跑完后回看分镜;如明显缺段,重跑「剧本分镜」即可)")
		}
		return *fallback, nil
	}

	whyStr := perr.Error()
	if res.Finish == "length" {
		whyStr += ";输出被 max_tokens 截断"
	}
	return planHead{}, fmt.Errorf(
		"规划轮输出解析失败(%s;finish=%s,completion=%d tokens,原文 %d 字)\n原文摘录:%s",
		whyStr, res.Finish, res.Usage.CompletionTokens, len([]rune(res.Content)), snippet(res.Content, 240))
}

// generateBatched 分批路径:轮1 规划(轻量)→ 轮 2..k 逐批出镜(每批 ≤5 镜,
// 单轮生成量小,推理模型压回 1-3 分钟)。
func generateBatched(ctx context.Context, client *llm.Client, sourceText string, opt Options, contentDriven bool) (*project.Plan, error) {
	hint := styleHint(opt)

	// ---- 轮 1:规划 ----
	// 内容驱动时不预设总镜头数:由规划轮自己决定拍多少镜才 cover 完整,只保留
	// 「每段 3-N 镜」这个分批粒度 —— 它是技术参数(保证单次输出不溢出),不是数量上限。
	head, err := callPlanRound(ctx, client, planSystemPrompt, buildPlanUserPrompt(sourceText, opt, contentDriven), opt.OnNote)
	if err != nil {
		return nil, err
	}
	// 段规范化上限:内容驱动时不能用人为此限 —— 否则 LLM 决定拍 20 镜会被截到
	// 内部兜底值,「不设上限」就成了空话。这里取规划轮自己给出的最大镜号。
	segMax := opt.MaxShots
	if contentDriven {
		segMax = 0
		for _, s := range head.Segments {
			if s.To > segMax {
				segMax = s.To
			}
		}
		if segMax <= 0 {
			segMax = opt.MaxShots
		}
	}
	normalizeSegments(&head, segMax)

	total := len(head.Segments)
	plan := &project.Plan{
		Title: head.Title, Logline: head.Logline, Style: head.Style,
		Characters: head.Characters, Scenes: head.Scenes, Props: head.Props, CoreBeats: head.CoreBeats,
	}

	// ---- 轮 2..k:分批出镜（**并行**）----
	//
	// 【2026-09-28 用户问「拆完分镜脚本，脚本中分不同的镜头，这不需要花费那么的推理时间吧」】
	// 原实现逐批**串行**：36 镜 /每批 ≤5 镜 = 6 批，每批一次完整推理（一批要输出 5 镜的六段式
	// 提示词），于是这一段耗时 = 6 × 单批耗时。而各批之间**本来就没有依赖** —— 段落边界与
	// 各段梗概在规划轮就定好了；串行唯一的作用是把"上一批实际拍了什么"喂给下一批，
	// 而那点信息的上游就是**规划轮给的前一段 brief**。现在并发跑、按批号顺序合并。
	cards, _ := json.Marshal(map[string]any{"characters": head.Characters, "scenes": head.Scenes})
	type batchOut struct {
		shots []project.Shot
		err   error
	}
	outs := make([]batchOut, total)
	var wg sync.WaitGroup
	sem := make(chan struct{}, batchConcurrency)
	for bi, seg := range head.Segments {
		wg.Add(1)
		go func(bi int, seg segment) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if opt.OnBatch != nil { // 进度照旧实时报（并发下顺序会乱，但每条都带批号）
				opt.OnBatch(bi+1, total, seg.From, seg.To)
			}
			segSeconds := opt.TotalSeconds * (seg.To - seg.From + 1) / opt.MaxShots
			if contentDriven {
				// 内容驱动下 opt.TotalSeconds/MaxShots 只是内部兜底(12 镜×7 秒),
				// 拿它换算分段时长会把"32 镜的一集"每段写成 10 秒 —— 提示词自相矛盾。
				// 按本文件一贯的 7 秒/镜估,才和实际方案对得上。
				segSeconds = (seg.To - seg.From + 1) * 7
			}
			user := fmt.Sprintf(promptText("generateBatched__ESTABLISHED_PLAN_HEAD_STRICT"), cards, head.Style, hint,
				seg.From, seg.To, seg.To-seg.From+1, segSeconds, seg.Brief, prevSegBrief(head.Segments, bi))

			raw, err := client.Chat(ctx, []llm.Message{
				{Role: "system", Content: batchSystemPrompt},
				{Role: "user", Content: user},
			}, true)
			if err != nil {
				outs[bi] = batchOut{err: fmt.Errorf("分镜批次 %d/%d(镜 %d-%d)失败: %w", bi+1, total, seg.From, seg.To, err)}
				return
			}
			shots, err := parseShotsBatch(raw, seg.From, seg.To)
			errFirst := err // 留档：两次失败形态常常不同，只报后一个会把人带偏
			if err != nil {
				// 单批重试一次
				raw2, err2 := client.Chat(ctx, []llm.Message{
					{Role: "system", Content: batchSystemPrompt},
					{Role: "user", Content: user},
					{Role: "assistant", Content: raw},
					{Role: "user", Content: "Invalid JSON or shots outside range " +
						fmt.Sprintf("%d-%d", seg.From, seg.To) + ". Error: " + err.Error() + "\nReturn the corrected STRICT JSON only."},
				}, true)
				if err2 != nil {
					outs[bi] = batchOut{err: fmt.Errorf("分镜批次 %d/%d 解析失败: %w", bi+1, total, err)}
					return
				}
				shots, err = parseShotsBatch(raw2, seg.From, seg.To)
				if err != nil {
					// **两次错误都带上**（2026-09-28）：此前只报第二次的 —— 而两次的失败形态常常不同
					// （第一次可能是"镜号越界"，第二次是"JSON 配不平"），只看后一个会把人带偏。
					outs[bi] = batchOut{err: fmt.Errorf("分镜批次 %d/%d 二次解析仍失败（首次:%v；重试后:%w）", bi+1, total, errFirst, err)}
					return
				}
			}
			// 抢救（salvage）可能只救回一部分：**缺号必须播报**，不能静默少镜。
			// 缺口同时会撞上契约的"镜头号不连续"门（warn），两条一起保证"少镜看得见"。
			if opt.OnNote != nil {
				got := map[int]bool{}
				for _, sh := range shots {
					got[sh.ID] = true
				}
				var missing []string
				for id := seg.From; id <= seg.To; id++ {
					if !got[id] {
						missing = append(missing, fmt.Sprintf("#%d", id))
					}
				}
				if len(missing) > 0 {
					opt.OnNote("⚠ 第 %d/%d 批只解析出 %d/%d 镜，缺 %s —— 这些镜号没拿到内容"+
						"（原文已落盘可查），请重跑这一批或让修复轮补",
						bi+1, total, len(shots), seg.To-seg.From+1, strings.Join(missing, "、"))
				}
			}
			outs[bi] = batchOut{shots: shots}
		}(bi, seg)
	}
	wg.Wait()
	// 按**批号顺序**合并：镜号顺序不受并发影响（每一批只产出自己那一段的镜号）。
	for bi := range outs {
		if outs[bi].err != nil {
			return nil, outs[bi].err
		}
		plan.Shots = append(plan.Shots, outs[bi].shots...)
	}
	if err := validateAndNote(plan, opt, contentDriven); err != nil {
		return nil, err
	}
	return plan, nil
}

// prevSegBrief 上一段的梗概（并行出镜时的衔接上下文）。
//
// 串行版本喂给模型的是"上一批**实际产出**的镜头摘要"；并行时拿不到它 —— 改用规划轮给的
// 前一段 brief。那是同一份信息的上游（段落梗概本来就规定了这一段该拍什么），而且**更稳**：
// 串行时上一批少出几镜，下一批拿到的摘要也跟着变。
func prevSegBrief(segs []segment, bi int) string {
	if bi <= 0 || bi-1 >= len(segs) {
		return ""
	}
	p := segs[bi-1]
	b := strings.TrimSpace(p.Brief)
	if b == "" {
		return fmt.Sprintf("上一段（镜 %d-%d）已拍完。", p.From, p.To)
	}
	return fmt.Sprintf("上一段（镜 %d-%d）梗概：%s", p.From, p.To, b)
}

// normalizeSegments 规范切分:钳制批大小/镜号连续覆盖 1..max。
func normalizeSegments(h *planHead, max int) {
	var out []segment
	want := 1
	for _, s := range h.Segments {
		from, to := s.From, s.To
		if from < want {
			from = want
		}
		if to > max {
			to = max
		}
		if to-from+1 > batchMaxShots {
			to = from + batchMaxShots - 1
		}
		if from > to || from > max {
			continue
		}
		if from != want {
			// 镜号有洞:把洞并进本段
			from = want
			if to-from+1 > batchMaxShots+2 {
				to = from + batchMaxShots + 1
			}
		}
		out = append(out, segment{From: from, To: to, Brief: s.Brief})
		want = to + 1
		if want > max {
			break
		}
	}
	// 补尾洞
	for want <= max {
		to := want + batchMaxShots - 1
		if to > max {
			to = max
		}
		out = append(out, segment{From: want, To: to, Brief: "承接前文推进剧情至收尾(规划轮未覆盖,请依全片风格补全)"})
		want = to + 1
	}
	h.Segments = out
}

// parseShotsBatch 解析一批 shots 并校验区间(同样支持截断补全与拆一层包装)。
func parseShotsBatch(raw string, from, to int) ([]project.Shot, error) {
	obj, ok := extractJSONObject(raw)
	if !ok {
		if fixed, closed := closeTruncatedJSON(obj); closed {
			obj, ok = fixed, true
		}
	}
	if !ok || obj == "" {
		// ① **逐镜抢救**：整段配不平，但单个镜头对象往往是好的（缺口交给修复轮去补，
		//    它会如实播报补了什么 —— 所以"救回来的"不会静默变成少镜）。
		if saved := salvageShotObjects(raw); len(saved) > 0 {
			return saved, nil
		}
		// ② 抢救不动：把**原文落盘**并给出路径。只给首尾摘录没法查 —— 问题在中间某处
		//    （2026-09-28 用户 88 集卡在第 1 集，报的就是这种无从下手的错）。
		path := dumpRawForDiagnosis(raw, "shots-batch")
		return nil, fmt.Errorf("输出里找不到可解析的 JSON 对象(原文 %d 字、逐镜抢救也未成功)%s；原文已存到 %s — 把这个文件给开发者即可定位",
			len([]rune(raw)), "；首尾摘录:"+snippet(raw, 120), firstNonEmpty(path, "(落盘失败)"))
	}
	s := repairJSONStrings(obj)
	decode := func(x string) ([]project.Shot, error) {
		var out batchShotOut
		if err := json.Unmarshal([]byte(x), &out); err != nil {
			return nil, err
		}
		if len(out.Shots) == 0 {
			return nil, fmt.Errorf("shots 为空")
		}
		return out.Shots, nil
	}
	parsed, firstErr := decode(s)
	if firstErr != nil {
		for _, inner := range allObjects(s) {
			if inner == s {
				continue
			}
			if sh, err := decode(repairJSONStrings(inner)); err == nil {
				parsed, firstErr = sh, nil
				break
			}
		}
	}
	if firstErr != nil {
		return nil, fmt.Errorf("JSON 解析失败: %v;原文摘录:%s", firstErr, snippet(raw, 120))
	}
	out := batchShotOut{Shots: parsed}
	if len(out.Shots) == 0 {
		return nil, fmt.Errorf("shots 为空")
	}
	var valid []project.Shot
	for _, sh := range out.Shots {
		if sh.ID >= from && sh.ID <= to && strings.TrimSpace(sh.H3Prompt) != "" {
			valid = append(valid, sh)
		}
	}
	if len(valid) == 0 {
		return nil, fmt.Errorf("批内无有效镜头(镜号需在 %d-%d 且含 h3_prompt)", from, to)
	}
	return valid, nil
}

// previousShotsSummary 前批衔接摘要(每镜一行)。
func previousShotsSummary(shots []project.Shot) string {
	if len(shots) == 0 {
		return "Previous shots summary: (this is the first batch)"
	}
	var b strings.Builder
	b.WriteString("Previous shots summary (for continuity, do NOT repeat these events):\n")
	for _, s := range shots {
		line := fmt.Sprintf("- Shot %d [%s] %s", s.ID, s.ShotSize, s.Action)
		if len(s.Dialogue) > 0 {
			line += fmt.Sprintf(" | last line: %s:%s", s.Dialogue[len(s.Dialogue)-1].Speaker, s.Dialogue[len(s.Dialogue)-1].Text)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// trimJSONBlock 已由 jsonx.go 的 extractJSONObject/closeTruncatedJSON 取代
// (旧实现是"首个 { 到最后一个 }",模型在 JSON 前写一句带花括号的说明、或输出被
//  截断时会截出非法片段,且无法区分这两种情形)。保留此别名只为兼容历史调用点。

// sysHighlightRules 抓重点纪律（来源：用户《一、网文小说转剧本提示词指令》，
// 2026-09-28 用户：「分镜脚本 LLM 解析是否按这个来啊，抓重点啊，咋那么纯啊」）。
//
// 【为什么单列一段】这份文件是用户定的**改编方法论**，平台此前只在钩子类型/片尾钩上偶然对上，
// 最要命的四条（3 秒爆点、爽点密度、砍冗余、放大反差）一个字都没进提示词 ——
// 于是解析出来的分镜"忠实但平"：该炸的地方不炸，该砍的支线照单全收。
//
// 覆盖关系（用户文件 → 平台的机器门）：
//
//	· 开篇 3 秒无铺垫无旁白  → contract「#1 必须是强钩，且不许有旁白/画外音」
//	· 每 80 字小爽点 / 200 字大爽点 → contract「爽点密度」（按台词字数累计，超阈报警）
//	· 段段留钩 / 片尾终极钩  → contract「连续三镜钩子密度」「收尾钩」
//	· 砍冗余铺垫支线冗长对话 → 本段规则 + 分镜预算（信息密度优先，宁短勿平）
//	· 放大情绪反差 / 反转密集 → sysDynamicShotRules 与钩子密度规则
//	· 合规零风险 → sysHard 既有合规段
var sysHighlightRules = promptText("sysHighlightRules__GRAB_THE_POINT")

// firstNonEmpty 取第一个非空字符串（错误信息里给"落盘路径或兜底说明"用）。
func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
