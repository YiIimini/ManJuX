// Package project 定义 ManJuX 的项目域模型与磁盘目录规范。
//
// 单个项目目录结构(data/projects/<name>/):
//
//	project.json        项目元数据与配置
//	source.txt          输入源文本
//	plan/plan.json      LLM 直出的分镜方案(角色/场景/镜头+完整 H3 提示词)
//	assets/characters/  角色定妆照 + asset_map.json
//	assets/scenes/      场景图
//	assets/props/       道具图
//	clips/NNN.mp4       每镜渲染产物(自带 H3 原生音轨)+ manifest.json
//	output/final.mp4    合成成片
//	logs/run.log        管线运行日志
package project

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/fsutil"
)

// ShotStatus 镜头渲染状态。
type ShotStatus string

// 实际会被写入的状态只有 Rendering / Done / Failed 三种,按「用不到的枚举
// 不保留」清理掉了此前的 pending / stale 两个空定义:
//   - 没有 pending 阶段:镜头条目在首次渲染前根本不存在;
//   - 没有 stale 阶段:输入指纹变化是靠 manifest 的 InputFP 比对直接判定重渲,
//     不需要单独的状态标记(见 pipeline.renderFingerprint)。
const (
	ShotRendering ShotStatus = "rendering"
	ShotDone      ShotStatus = "done"
	ShotFailed    ShotStatus = "failed"
)

// Line 一句台词。
type Line struct {
	Speaker string `json:"speaker"` // 角色名,或画外音标签(旁白/系统/画外音/内心)
	Text    string `json:"text"`
	// OS 画外音(v2,2026-09-26):这句话**不是画面里的人张嘴说的**。
	//
	// 内心独白、系统提示音、无人称旁白都属于这一类 —— 画面里的角色嘴唇必须闭合。
	// 为什么必须进契约而不是靠提示词措辞:技能端产出的 889 条台词里有 543 条是旁白,
	// 而 v1 把它们一律写成 <d>[Chinese]…</d> 塞在镜头描述里,模型只看得见"这里有句话",
	// 看不见"谁不许开口",于是找画面里的人张嘴 —— 用户报的「系统旁白,主角动嘴」。
	// 有了这一位,平台就能:①入口拦住"画外音标签却没标 os"的包;②后处理补上
	// "画面里的人嘴唇完全闭合"子句;③字幕按画外音排版。
	OS bool `json:"os,omitempty"`
}

// Shot 单个分镜。
type Shot struct {
	ID         int      `json:"id"`
	Scene      string   `json:"scene"`      // 场景 id
	Characters []string `json:"characters"` // 出场角色 id
	// Positions 本镜每个出场角色在**画面里**的位置（键=角色 id，值=如「画面左三分之一、前景、面朝右」）。
	//
	// 来源：官方 `shot-table-spec.md` STEP 5 要求每行的 Reference Anchors 含四个子字段，
	// 其中 `Character Positions (camera view)`（左/中/右·上/中/下·前/中/后景＋朝向＋初始姿态）
	// 与 `Exited Character Status` 平台此前**根本没有字段** —— 位置只写在散文里，
	// 于是"人物凭空出现/凭空消失"没有任何判据可查。
	Positions map[string]string `json:"positions,omitempty"`
	// Offscreen 上一镜在场、本镜不在场的角色的**离屏状态**（键=角色 id，值=离屏位置与原因）。
	//
	// 官方纪律：已离屏角色至少保留 1 镜，连续 2 镜明确不在场后才移除 ——
	// 这条纪律让"人不见了"变成显式信息，而不是靠观众自己脑补。
	Offscreen map[string]string `json:"offscreen,omitempty"`
	ShotSize  string            `json:"shot_size"` // 景别:远景/全景/中景/近景/特写
	Camera    string            `json:"camera"`    // 运镜描述
	Action    string            `json:"action"`    // 画面动作概述(中文)
	Dialogue  []Line            `json:"dialogue"`  // 台词(逐字保留进 H3 提示词)
	Narration string            `json:"narration"` // 旁白(画外音)
	Duration  int               `json:"duration"`  // 秒(4-10)
	Mode      string            `json:"mode"`      // ref2va(有参考图) / t2va(纯文生)
	H3Prompt  string            `json:"h3_prompt"` // 完整六段式/三段式提示词(LLM 直出)

	// View 本镜的**主视角/机位角度**(英文,逐字来自官方 9 视角词表,见 view_vocab.go)。
	//
	// 为什么与 Camera 分开(2026-09-27 技能侧整改单 §1.1):Camera 是**运镜**(推/拉/摇),
	// View 是**机位**(往哪看、从多高看)。原先只有 Camera,于是镜头语言退化成
	// "景别推两个运镜值"—— 最长 21 镜同景别、零机位角度,画面像用同一台机器拍完整集。
	// 空值合法(存量包):契约按 warn 提示,不拒收。
	View string `json:"view,omitempty"`

	// ViewNote 机位视角**留空的原因**（技能侧一直写这个字段，平台此前没建模 ⇒ 被静默丢掉）。
	//
	// 【2026-09-29 用户：「前后镜漂移，压不住场」】官方那 9 条是大机位表（鸟瞰/平视深远/
	// 纵深压缩/条件性仰视…），"平视中近景""俯拍特写"在里面没有对应项 —— 硬填就是写错词
	// （比留空更糟，见 plan_checks 的裁定 A）。所以留空合法，但**要写清为什么留空**：
	// 实测那一集 11 镜里 8 镜既没 view 也没说明，成片的表现就是"机位随机跳、越轴"。
	ViewNote string `json:"view_note,omitempty"`

	// AngleZH 机位角度的**中文短标签**（平视/仰拍/俯拍/鸟瞰，技能侧《平台侧生成规范》§6.2）。
	//
	// 与 View 的关系：View 是官方 9 条**英文**大视角（要逐字进正文，机检判它在不在正文里）；
	// AngleZH 是技能侧明细里那四个中文词（"从哪看、看多高"的粗档）。两者都要读进来 ——
	// 契约门按 AngleZH 的表判"角度词写没写对"，这正是 §8 自检第 6 条。
	// 空值合法（平台直出的方案不写这个字段，存量包也可能没有）。
	AngleZH string `json:"angle_zh,omitempty"`

	// Cuts 本片段内部的**小镜头**（用户 2026-09-28 的「片段 → 小镜头」两层结构）。
	//
	// 用户原话：「一个分镜里可以包含多个小分镜，小分镜里包含运镜」。
	//
	// 平台此前是**单层**：小镜头只作为文本写在 `h3_prompt` 的 `[Shot N] At MM:SS.mmm` 里
	// （官方的多镜形式），于是"这一片段到底切了几刀、每刀多长、每刀什么运镜"**没有结构化数据**——
	// 契约门只能拿正则去正文里捞，捞到的也只是字符串：改不了、查不动、界面上也摆不出来。
	//
	// 现在两层：
	//
	//	片段（Shot）—— 一次生成，4–10 秒，是**渲染与资产的单位**（参考图、种子、指纹都挂在它上面）；
	//	小镜头（Cut）—— 片段内部的切点，1.5–4 秒（用户《【额外】仿真人10S分镜头指令》），
	//	                是**镜头语言的单位**。
	//
	// 与 `h3_prompt` 的关系：**同一份数据的两种表示**。解析时从正文读回来
	// （`ParseCutsFromPrompt`），装配时按 `Cuts` 渲染回正文 —— 权威在**结构字段**，
	// 正文是它的投影（与 plan 的其它部分同一套口径）。
	Cuts []Cut `json:"cuts,omitempty"`

	// EndState 本镜**尾帧状态**:这一镜最后一帧的画面上有什么(人物位置/姿态/表情/
	// 视线方向/环境状态),供下一镜接着写。
	//
	// 为什么要有(整改单 §1.2):平台只有 seam 开关(把上一镜尾帧喂给下一镜),
	// 却没有任何"尾帧长什么样"的写法约束 —— 于是"接上一镜"这件事全靠模型自己猜。
	// **禁写听觉/氛围/情绪词**(寂静/压抑…)——下游画不出来,只会稀释视觉信息,
	// 判据见 view_vocab.go 的 EndStateBannedIn。跨场景切换处不要求动作连贯
	// (改用渐隐渐显 + 转场字幕),所以空值对"每段场景的首镜"是允许的。
	EndState string `json:"end_state,omitempty"`

	// HookType 本镜的**钩子类型**（受控词表，见 shot_table.go）。	//
	// 来自官方「标准镜头信息表」六列之一（`skills/3d-animation-short-generator` STEP 5），
	// 用途是**每集钩子分布自检**（STEP 5.5 检查 1）：每镜一个，每连续三镜至少一个
	// reveal/reversal/callback，开篇与收尾必须是强钩子。没有它，"节奏"就只能靠感觉说，
	// 有它之后"这一集是不是平"是可机检的。空值合法（存量包）：契约按 warn 提示。
	HookType string `json:"hook_type,omitempty"`

	// Unbound / UnboundReason 本镜的画面里有人，但**刻意不绑卡、也不给脸**。
	//
	// 为什么要有这一位（2026-09-27 技能侧回复 §6/§9）：`特写/近景却没有出场角色` 是一条门
	// （脸上特写没参考图 = 每次换一张脸），但有一类镜是**故意**这样的 —— 群演、一次性角色、
	// 只给背影/局部/人群的人。把"刻意不露脸"与"忘了绑卡"分开，门才不会永远挂着假告警。
	//
	// 取值（技能侧的 `unbound_reason`）：`no_card`（画面里的人不在本集角色表）/
	// `faceless_by_design`（有意不给脸）。**旧写法**是他们早先在提示词里写的
	// `kept off-face and unbound`（prose），平台仍认它作兜底（见 PlanUnboundFaceMarker）。
	Unbound       bool   `json:"unbound,omitempty"`
	UnboundReason string `json:"unbound_reason,omitempty"`

	// PropIDs 本镜用到的**道具**（技能侧 09-27 S8 按道具名分词匹配出的引用）。
	//
	// 为什么要有（2026-09-27）：平台侧刚把"道具图"这条路补完（此前道具只被读进来、从没出过图），
	// 而"哪一集需要哪件道具"此前无据可依 —— 只能把全书的道具塞进每一集。有了这一位，
	// 资产范围（asset_scope.go 的 wantsProp）就能按引用收窄，每集只带真用得着的。
	// 空值合法（没有这个字段的明细/手工方案）：那时退回"全带上"。
	PropIDs []string `json:"prop_ids,omitempty"`

	// VoKind 本镜的**画外演出类型**（技能侧 2026-09-27 新口径，用户定调）：
	//
	//	"inner"  正角内心活动 —— 由该角色的 **Q 版形象**在画面里演出这段心思，**本人闭嘴、不演这段**；
	//	"system" 系统 —— 科幻悬浮**全息面板**（道具）＋ **Q 版小机器人**（人物）同框，人闭嘴、不念系统台词；
	//	空        普通镜。
	//
	// 它**不是展示字段**：参考图装配按它决定带哪几张（见 pipeline/refs.go），
	// 所以契约门会检查它的取值、它的悬挂引用与"演出句有没有逐字写进正文"。
	VoKind string `json:"vo_kind,omitempty"`
	// ChibiIDs inner 镜里演出的 Q 版卡 id（技能侧约定 `chibi_<原角色id>`）。
	ChibiIDs []string `json:"chibi_ids,omitempty"`
	// SystemAssets system 镜的两件资产：{"bot":"chibi_sys_01","panel":"prop_holo_01"}。
	SystemAssets map[string]string `json:"system_assets,omitempty"`

	// ShotSizeNote 景别被调整过的说明（技能侧留下的人话，如"建立镜头：由 特写 提为中景"）。
	// 平台**不消费**它（判据只看 ShotSize 本身），收下来只为两件事：不静默丢字段、
	// 页签能把它显示给人看（"这个景别为什么是中景而不是特写"）。
	ShotSizeNote string `json:"shot_size_note,omitempty"`

	// KeyframePrompt 本镜**首帧 T2I 静帧**提示词(技能侧 manju_plan.py 的
	// build_keyframe_prompt 产出)。
	//
	// 为什么平台要收下来(2026-09-26):① 之前 Plan 里没有这个字段,技能侧写进 plan.json 的
	// 首帧提示词被平台**静默丢弃** —— 导入后平台再重写一次 plan.json,它就永久没了
	// (与 props 同一类缺陷);② 它是图生图模型的输入,而图生图模型画中文几乎必然成乱码
	// 字形(实测该字段汉字 1362 → 0 是技能侧 v2.3 的清洗成果),所以契约里对它单独设门:
	// 出现任何汉字都 warn(见 plan_contract.go)—— 这是"这一镜的第一帧就带字"的可判形态。
	KeyframePrompt string `json:"keyframe_prompt,omitempty"`
}

// Character 角色卡。
//
// Kind 是**实体类型**,决定走哪一套渲染方法(定妆照取景/多视角方式/镜头身份锁都不同):
// human 人 / object 物 / spirit_pet 灵宠 / beast 妖兽 / incorporeal 无实体 / chibi Q版。
// 为什么它必须是数据而不是"渲染时猜":一只机关铜雀与一个少年,在提示词层需要的是
// 完全不同的取景与身份约束 —— 用同一套"半身正面、照抄脸"去渲染铜雀,模型只会画出
// 一个人(2026-09-24 用户实证)。**空值合法**:存量方案没有这个字段,由代码按名称与
// 描述推断(见 pipeline.ResolveEntityKind),UI 里可人工纠正。
type Character struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind,omitempty"` // human / object / spirit_pet / beast / incorporeal / chibi
	Description string `json:"description"`    // 外貌/服装/气质(供一致性)
	ImagePrompt string `json:"image_prompt"`   // 定妆照文生图提示词(英文)
	// BoardPrompt 三视图板串（技能侧卡片的 `board_prompt`，见《平台侧生成规范》§3.4/§3.5）。
	//
	// 为什么平台要读它：板子的 15 格内容（3 全身 ＋ 6 头角度 ＋ 6 局部细节）是技能侧定死的口径，
	// 而平台自己那份列表与它并不一致（头角度与局部细节两项对不上）。以卡片为准、平台那份只作**兜底**，
	// 才不会有"两套板子口径"。装配时会走与定妆串同一套清洗（剥风格句、掐掉要求画标签的那一行）。
	BoardPrompt string `json:"board_prompt,omitempty"`
	Voice       string `json:"voice"` // 声线描述(英文,进 H3 提示词)

	// Age 角色的**年龄 / 年龄段**（结构字段）。
	//
	// 【2026-09-28 审计 #21】此前年龄只活在 `Description` 的自由文本里，而它是**用户点名过
	// 的问题**：「中年人渲染出来跟个小年轻一样」—— 模型的默认审美就是年轻化，
	// 不在提示词里把年龄写成**硬锚**，它每次都往年轻里画。
	//
	// 写成字段之后有两件事才能做：
	//   · 装配侧把它变成定妆照/板的**年龄锚**（见 pipeline 的 anchorAgePhrase）；
	//   · 契约门拿它与**设定集的外形锚表**对账（`- 年龄：45` 与卡片说的是不是同一个人）。
	Age string `json:"age,omitempty"`
}

// Scene 场景卡。
type Scene struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ImagePrompt string `json:"image_prompt"`

	// Landmarks 本场景的**连续性地标**:同场景跨镜必须保持屏幕位置的固定物
	// (门框/中岛/系船桩/树…)。跨镜回到同一地点时,观众靠这些不动的东西认出"还是这儿";
	// 地标在镜头之间换位置,空间就散了。
	Landmarks string `json:"landmarks,omitempty"`

	// LightState 本场景的**光态**:日/夜 + 主光方向(如 "黄昏,主光从西侧门洞斜射")。
	// H3 每镜独立渲染,同一地点相邻两镜一个中午一个黄昏就是硬伤;写成字段后
	// 分镜与提示词都照着它写,不靠模型从上下文猜。
	LightState string `json:"light_state,omitempty"`
}

// Prop 道具卡。
//
// 技能侧原来只给 {id, name, en}(英文外观短语),口径是"道具只走文字描述、不给参考图";
// 用户 2026-09-26 定调改成**提示词也由技能侧直出**,所以这里预留 description /
// image_prompt 两个字段 —— 技能侧还没吐出来的存量包照旧能读(en 是老的 look 短语)。
//
// ⚠ 之前 Plan 里**根本没有 props 字段**:技能侧写进 plan.json 的道具被平台静默丢弃,
// 导入后若平台再重写这份 plan.json,道具信息就永久没了。字段收下来才算真读到了。
type Prop struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	EN          string `json:"en,omitempty"`           // 英文外观短语(技能侧早期的 look)
	Description string `json:"description,omitempty"`  // 中文外观/材质/配色描述
	ImagePrompt string `json:"image_prompt,omitempty"` // 道具文生图提示词(英文)
}

// Plan LLM 直出的完整分镜方案。
type Plan struct {
	Title   string `json:"title"`
	Logline string `json:"logline"`
	// CoreBeats 本集的**核心提炼**：核心冲突 + 2-3 个大爽点（逆袭/打脸/揭秘/升阶），各标类型。
	//
	// 【为什么要有这个字段】用户 2026-09-28：「分镜脚本按照爆款爽剧的核心要点来，LLM 需要抓住文字
	// 内容中的重点总结输出核心分镜脚本，而不是一味的照搬原文内容」。平台此前给 LLM 的指令恰恰相反
	// （"必须沿原文节拍逐段改编、不得跳段"），改编于是退化成抄写。
	// 提炼结果**落成字段**才能机检：有提炼，才谈得上"取舍"；没有提炼，分镜就只能是原文的段落表。
	CoreBeats  []string    `json:"core_beats,omitempty"`
	Style      string      `json:"style"` // 视觉风格(英文,注入每镜提示词)
	Characters []Character `json:"characters"`
	Scenes     []Scene     `json:"scenes"`
	Props      []Prop      `json:"props,omitempty"`
	Shots      []Shot      `json:"shots"`

	// StrippedDirectives 本次读取时从 h3_prompt 里剔掉的"生成器指令行"条数（见 h3_clean.go）。
	// **不进 JSON**：它是读取期的诊断量，不是方案的一部分（写回磁盘会把上游文件"改脏"）。
	StrippedDirectives int
	// StrippedPlaceholders 读取时被清掉的**占位台词标签**句数（`<d>…</d>` 里没有真话的那些）。
	// 单独计数是为了如实播报：用户听到的"怪配音"就是从这些标签来的，清了要说一声。
	StrippedPlaceholders int `json:"-"`
	// VoiceRebound 本次装配时被**按权威声线重绑**的台词句数（见 pipeline.BindLineVoices）。
	//
	// 【为什么要有这个数】2026-10-07 真机实测：角色卡上写着 voice，提示词里也写着「逐字照抄，
	// 不得改写」，而 H3 是把那句引述当**音频条件**用的（"rewording it changes the timbre"，
	// 见 internal/director/prompts/sysH3Format__H3_PROMPT_FORMAT_CRITICAL.md）——
	// 模型照样每镜改写一遍（真机 13 镜 11 句，卡片原文一次都没出现）⇒ 音色逐镜漂移。
	// 平台现在在装配点**确定性重绑**（不让模型自觉），重绑了几处要如实说一声。
	// 与 StrippedPlaceholders 同样**不进 JSON**：它是装配期的诊断量，不是方案的一部分。
	VoiceRebound int `json:"-"`
	// VoiceUnresolved 本次装配时**认不出说话人/权威声线**、因而保持原样的台词句数。
	//
	// 认不出的情形只有两种：<d> 里的台词与方案里的 dialogue 一个字都对不上（模型改了词），
	// 或者说话人名两边写法对不上。这两种都**不猜**（宁可留着让模型自由发挥，也不要塞错人的音色），
	// 但要如实播报 —— 静默保留 = 用户以为"声线都绑好了"。
	VoiceUnresolved int `json:"-"`
	// SpeechPlacementSkipped 本次装配时**没有加逐刀人声清单**的镜头数。
	//
	// 【为什么会有"不加"这种结果】那条清单是在向模型许愿「这一刀要念这几句，其余一刀不许有人声」——
	// 数错了比不加更糟：2026-10-08 真机实测，清单把条款正文里当例子引用的 `<d>…</d>` 也算成台词，
	// 写成「这一刀 4 行，每行念一遍」而真实只有 1 行 ⇒ 模型把空出来的格子填满（镜 1 重复念、
	// 镜 5 凭空多一句「或是血」）。所以装配时会先与 `PlanSpokenTexts` 对账，对不上就**不加**这条，
	// 并在这里计数、由渲染收尾处如实播报（不加 = 少一条约束，用户有权知道）。
	SpeechPlacementSkipped int `json:"-"`

	// VoiceClashStripped 本次装配时**清掉的"性别/年龄描述残留"句数**（见 pipeline.replaceVoiceClause）。
	//
	// 【真机代价（2026-10-08）】镜 4 实际提交的引述是
	// `Off-screen left, young woman's light, eager, 实习生 (S2) says in Bright high young male voice …`
	// —— 模型自己写的 **"young woman's"** 与**卡上的男声**同时留在同一句里（重绑只换掉了后半截），
	// 用户报的「女的配男音」正是这一类。清掉要说一声：这是平台替模型改的措辞。
	VoiceClashStripped int `json:"-"`
	// BlackFlashShots / BlackFlashSec 本次渲染**剪掉黑闪**的镜数与秒数。
	//
	// 【为什么记账】引擎偶发吐几十毫秒全黑（真机那一集：镜 1/8 片头 0.208s、镜 6 第 4.46s、
	// 镜 13 第 2.42s）—— 观众看到"画面无故一黑"，质检的最暗采样帧压在上面时还会整镜判"疑似黑屏"。
	// 平台把它当**故障**剪掉（不剪剧情黑：长黑段一律不碰），剪了多少必须说出来。
	// 与其他装配期诊断量一样**不进 JSON**：它是这次的账，不是方案的一部分。
	BlackFlashShots int     `json:"-"`
	BlackFlashSec   float64 `json:"-"`
	// FilledBodyShots 本次读取/存盘时**平台按方案自己的材料拼了最小正文**的镜号（见 empty_body.go）。
	//
	// 【为什么会有这种结果（2026-10-08 用户第二次报「还是报一样的错误」）】真机镜 12 的材料齐全
	// （场次/人物/走位/景别/机位/台词/时长），只有 `action` 与 `h3_prompt` 是空的；模型在自纠一轮里
	// 补不出来，而渲染入口的致命门「h3_prompt 为空」**不让启动** ⇒ 48 集的批量产线停在第 1 集。
	// 材料是已知量 ⇒ 平台按它拼一版最小合规正文（不是新编内容），拼过哪几镜必须如实播报。
	// 与其他装配期诊断量一样**不进 JSON**：它是这次的账，不是方案的一部分。
	FilledBodyShots []int `json:"-"`
}

// Asset 参考图资产。
type Asset struct {
	ID          string `json:"id"`   // 形如 char:linfan / scene:yuelai_inn
	Kind        string `json:"kind"` // characters / scenes / props
	Name        string `json:"name"`
	File        string `json:"file"` // 相对项目目录路径
	ImagePrompt string `json:"image_prompt"`
	Source      string `json:"source"`      // upload / comfy / llm-prompt
	Fingerprint string `json:"fingerprint"` // 内容指纹(变更检测)
}

// ClipManifest 镜头产物清单(断点续跑与时效管理)。
type ClipManifest struct {
	ShotID  int        `json:"shot_id"`
	Status  ShotStatus `json:"status"`
	Engine  string     `json:"engine"`   // comfy / api
	File    string     `json:"file"`     // 相对项目目录
	InputFP string     `json:"input_fp"` // 渲染输入指纹(提示词+资产+参数)
	// Length 本镜产物**实际渲出的帧数**(= duration × 24)。
	//
	// 为什么不并进 InputFP(2026-09-24 审计 P0-8):指纹串是老项目断点续跑的判据,
	// 往里加字段会让**存量项目全部重渲**。而"改了时长却没重渲"是真实故障:用户把
	// 4 秒改成 6 秒(为了装下被截断的台词),指纹不变 → 走跳过分支 → 旧片原样留着,
	// 界面还报成功。所以单独一个字段,跳过条件追加 `m.Length == shot.Duration*24`;
	// 老 manifest 里没有这个键 → Length=0 → 与任何非零时长都不等 → 正确地重渲一次
	// (改过时长的镜头本来就该重渲),此后就稳定了。
	Length int `json:"length,omitempty"`
	// Model 本镜产物的**渲染模型身份**(底模 + 加速 LoRA / 云端模型名)。
	//
	// 为什么要单独记(2026-09-25 用户问「Minimax-h3_Singularity 用起来没有?」时发现的缺口):
	// 渲染指纹里**不含模型名** —— 换底模(如 2026-09-24 换到 Singularity)之后,
	// 已渲镜头不会被判失效,于是同一集里会混着"旧模型的老镜头"和"新模型的新镜头",
	// 画风/质感不一致而没人发现。与 P0-8 同一类缺陷,处理方式也一致:
	// **不往指纹里加字段**(那会让存量项目全部重渲),而是单独记一列并纳入跳过判据;
	// 老清单没有这个键(Model=="")时不推翻指纹结论,只提示用户"这些镜头是换模型之前渲的"。
	Model string `json:"model,omitempty"`
	// SeamFrom 接缝链的**上游依赖**:渲这一镜时,上一镜的 InputFP 是什么。
	//
	// 为什么要单独记(审计:接缝链的上游依赖不在下游镜头的判据里):镜间连续是跨镜
	// 硬依赖 —— 上一镜渲完会把它的 AV latent 写进固定槽 clip_N,下一镜开头 22 帧钉的
	// 就是那份 latent。用户只重渲镜 N(改提示词/时长/档位)之后,镜 N+1 的指纹没变,
	// 于是被判「产物有效,跳过」,它钉的仍是**旧**镜 N 的尾部:接缝处画面跳变、色调
	// 突变,而成片照常合成、阶段照常报成功。
	//
	// 处理方式与 Length/Model 一致:**不往指纹里加字段**(那会让存量项目全部重渲),
	// 而是单独记一列。老清单没有这个键(空串)时不推翻指纹结论,只对"接缝确实生效且
	// 上游指纹变了"的新清单判失效。
	SeamFrom string `json:"seam_from,omitempty"`
	// AssetFP 渲这一镜时**所用资产**的指纹（角色定妆照/场景板/道具卡/系统卡片的 id+kind+fingerprint 集合哈希）。
	//
	// 为什么要单独记（2026-09-28 全面审计，官方 qc-checklist 重生成纪律）：官方要求"用户重做产物后
	// 下游必须全用最新版本，且**不得在最终拼接里静默混用新旧资产**"。manifest 里已记 Length/Model/SeamFrom
	// 三列各治一类静默故障，却没有一列说明"这镜用的是哪批资产" —— 于是"换了定妆照只重渲前 5 镜、
	// 成片里同一个人前后两张脸"这种事故合成时无人过问。与 Model 同样处理：单独一列，空值不推翻结论。
	AssetFP   string    `json:"asset_fp,omitempty"`
	TaskID    string    `json:"task_id"` // 云端任务 id(如有)
	Error     string    `json:"error"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProjectConfig 项目级配置(创建时从全局默认拷贝,之后独立调优)。
type ProjectConfig struct {
	Params StageParams `json:"params"`
}

// Project 项目元数据。
type Project struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Config    ProjectConfig `json:"config"`

	// Chapters 本集覆盖的章号(平台在拆集时按章切片写入的真源)。
	// 为什么落盘:覆盖核对与"自动补缺"此前依赖 LLM 写在 chapters 字段里的字符串或
	// 【本集原文】是否附着成功 —— 两者都不可靠,曾据此误判缺章、删掉正确分集
	// (2026-09-12 事故:64 集被删成 63 集)。章号由平台自己记,判定才有依据。
	Chapters []int `json:"chapters,omitempty"`

	// 运行时状态(不落盘字段由 Store 维护)
	Running bool   `json:"running"`
	Phase   string `json:"phase"` // script/assets/render/qc/assemble/idle
}

// Store 项目仓库(线程安全的内存索引 + 磁盘读写)。
type Store struct {
	mu       sync.Mutex
	root     string // data/projects
	projects map[string]*Project

	// 资产表的**按项目写锁**（2026-09-29 运行时审计 P1-3）：`UpdateAssets` 用它把
	// "重读 → 改 → 写回"变成原子操作，避免并发写方互相覆盖（见 UpdateAssets 注释）。
	// 不同项目各一把，互不阻塞；map 本身由 assetLocksMu 保护。
	assetLocksMu sync.Mutex
	assetLocks   map[string]*sync.Mutex
}

// NewStore 创建项目仓库并扫描磁盘恢复项目列表。
// NewStore 打开项目仓库:先做旧版平铺分集的一次性迁移,再扫描目录装载项目。
// 目录规范(两级):projects/普通项目/ 与 projects/书名/分集/;
// 以目录内是否存在 project.json 判定项目,书名目录无 meta 不会被误判。
func NewStore(root string) (*Store, error) {
	s := &Store{root: root, projects: map[string]*Project{}}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	s.migrateSeriesDirs()
	s.reportSplitSeriesDirs()

	loadDir := func(dir string) { // 目录下所有含 project.json 的子目录装为项目
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := &Project{ID: e.Name(), Name: e.Name()}
			if err := s.loadMeta(p); err == nil {
				s.projects[p.ID] = p
			}
		}
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(full, "project.json")); err == nil {
			p := &Project{ID: e.Name(), Name: e.Name()}
			if err := s.loadMeta(p); err == nil {
				s.projects[p.ID] = p
			}
			continue
		}
		loadDir(full) // 书名目录:装载其下分集
	}
	return s, nil
}

// Root 返回项目根目录。
func (s *Store) Root() string { return s.root }

// Rescan 重扫项目根目录,把**外部新建**的项目装进内存索引,返回新增的 id 列表与总数。
//
// 为什么必须有这一个(2026-09-26 · 双侧共组):
// 技能端(ManJuNovel)现在直接产出项目包并写进 `projects/<书名>/<集名>/` —— 契约见
// `docs/渲染脚本契约.md`。而 Store 只在**进程启动时**扫一次盘,不重扫的后果是
// "技能端出好脚本了,平台端得重启才看得见" —— 链路断在这一步,双边分工就白做。
//
// 语义边界:只**新增**与**刷新元信息**,不删任何东西(盘上目录被外部删掉时,列表里
// 那条留到下次启动;这样比"运行中悄悄消失一个项目"更可预期)。已装载的项目保留
// 内存里的运行态(参数调优不会被盘上旧值顶掉)。
func (s *Store) Rescan() (added []string, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	added = []string{}
	loadIn := func(dir string) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() || s.projects[e.Name()] != nil {
				continue
			}
			p := &Project{ID: e.Name(), Name: e.Name()}
			if err := s.loadMeta(p); err == nil {
				s.projects[p.ID] = p
				added = append(added, p.ID)
			}
		}
	}
	entries, _ := os.ReadDir(s.root)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(s.root, e.Name())
		if _, err := os.Stat(filepath.Join(full, "project.json")); err == nil {
			if s.projects[e.Name()] == nil {
				p := &Project{ID: e.Name(), Name: e.Name()}
				if err := s.loadMeta(p); err == nil {
					s.projects[p.ID] = p
					added = append(added, p.ID)
				}
			}
			continue
		}
		loadIn(full) // 书名目录:装载其下分集
	}
	return added, len(s.projects)
}

// Dir 返回项目目录。
// SeriesBookName 从系列分集 id（「书名·第01集·标题」）里取**书名**；不是分集时返回空串。
//
// 用途（2026-09-28）：把设定集接进各会话 —— 设定集按用户约定放在
// `data/小说/<书名>/设定集/`，而项目侧只有 id，得先把书名取出来。
func SeriesBookName(id string) string {
	if m := seriesRe.FindStringSubmatch(strings.TrimSpace(id)); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// seriesRe 「书名·第N集·标题」系列分集命名。
var seriesRe = regexp.MustCompile(`^(.+)·第\d+集`)

// Dir 项目磁盘目录:系列分集归入「书名」子目录管理
// (projects/书名/书名·第01集·标题),普通项目仍在根下。
func (s *Store) Dir(id string) string {
	if m := seriesRe.FindStringSubmatch(id); m != nil {
		return filepath.Join(s.root, m[1], id)
	}
	return filepath.Join(s.root, id)
}

func (s *Store) metaPath(id string) string {
	return filepath.Join(s.Dir(id), "project.json")
}

// loadMeta 读项目元数据。
//
// ## 两处自愈（2026-09-29 数据完整性审计 #1）
//
// **① `id` 与目录名不一致时以目录名为准。**
// 盘上的目录名才是"这个项目住在哪"，而 `project.json` 里的 `id` 是**可能过期的副本**：
// 技能端产出的包写的是 `id = 标题`（≠ 目录名），导入时靠 `handlers_project.go` 补写一次；
// 那次补写如果没成功（非原子写、被打断），盘上就留下"目录叫 A、文件说自己是 B"的项目 ——
// 平台以 `id` 为键登记它，于是 `Dir(id)` 指向**不存在的路径**：项目点得开，
// 方案/资产/产物**全读不到**，而界面上看不出任何异常。
//
// 自愈是安全的方向：目录名是平台自己用的地址，文件里的 id 只是个字段。
// （反过来"按文件里的 id 去改名目录"才是危险的 —— 那会在两处不一致时挑一个可能错的名字。）
//
// **② `json` 坏掉时要留下痕迹。**
// 调用方（`NewStore` 的两处 `loadDir`）只看 `err == nil`，失败就**静默跳过**这个项目 ——
// 用户看到的是"盘上明明有目录，列表里没有它"，一行日志都没有。这里补一条播报，
// 让"项目消失了"至少能在运行日志里查到原因（对比：`library.go` 对损坏的 `asset.json`
// 早就是"专门记 log 并写明理由"的做法）。
func (s *Store) loadMeta(p *Project) error {
	dirID := p.ID // 调用方用目录名填进来的
	raw, err := os.ReadFile(s.metaPath(p.ID))
	if err != nil {
		return err
	}
	if err := jsonUnmarshal(string(raw), p); err != nil {
		return fmt.Errorf("项目元数据损坏（%s/project.json）：%w —— 该目录在列表里会被跳过", dirID, err)
	}
	if p.ID != dirID {
		log.Printf("[store] 项目「%s」的 project.json 里写着 id=%q（与目录名不一致）—— 以目录名为准自愈",
			dirID, p.ID)
		p.ID = dirID
	}
	if strings.TrimSpace(p.Name) == "" {
		p.Name = dirID
	}
	p.ensureDefaultParams()
	return nil
}

// reportSplitSeriesDirs 检测「同一本书裂成多个书名目录」并**如实播报**（2026-09-29 数据完整性审计 #2）。
//
// ## 为什么会裂
//
// 书名目录曾经历过两次命名规范化（全角逗号换成间隔号 `·`；2026-09-28 又放行全角标点），
// 于是同一本书可能留下两个目录。实盘证据：`data/projects/` 下同时存在
// `补碑人改命，守钟女逆时`（17:04）与 `补碑人改命·守钟女逆时`（15:24）。
//
// 裂开的后果：覆盖核对 / 自动补缺 / 按名复用**只看到一半** —— 缺章误报、
// "重拆不复用旧集"、封面只刷新一半。
//
// ## 为什么**只报不合**
//
// 合并要改目录名、要把两边的集并到一起，而"两边的集同名"时**必然要覆盖一个** ——
// 那是不可逆的数据搬迁，不该由平台替用户决定（本仓纪律：涉及用户数据形态的变更要留痕、
// 要用户拍板；而"改个目录名"看起来无害，实际会连带 `Dir(id)` 的解析与已渲产物的路径）。
//
// 所以这里只做**检测与播报**：把"疑似同一本书的多份目录"报出来，
// 用户想合并时自己在资源管理器里并（本仓对用户数据的处置一贯如此）。
//
// 判据：把书名里的标点归一化（全角逗号/顿号/空格/间隔号统一成 `·`）后相同，即视为同一本。
// 只在**两份及以上的目录里都没有 project.json**（即都是书名目录、不是单集项目）时才报 ——
// 单集项目名恰好长得像不算。
func (s *Store) reportSplitSeriesDirs() {
	for _, msg := range s.splitSeriesDirReports() {
		log.Printf("[store] %s", msg)
	}
}

// splitSeriesDirReports 返回"疑似同一本书多份目录"的播报文本（空 = 没问题）。
// 拆出来是为了可测：日志没法断言，返回值可以。
func (s *Store) splitSeriesDirReports() []string {
	ents, err := os.ReadDir(s.root)
	if err != nil {
		return nil
	}
	byBook := map[string][]string{} // 归一化书名 → 目录名列表
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// 有 project.json 的是单集项目，不是书名目录
		if _, err := os.Stat(filepath.Join(s.root, name, "project.json")); err == nil {
			continue
		}
		key := normalizeBookPunct(name)
		if key == "" {
			continue
		}
		byBook[key] = append(byBook[key], name)
	}
	var out []string
	for key, dirs := range byBook {
		if len(dirs) < 2 {
			continue
		}
		sort.Strings(dirs)
		out = append(out, fmt.Sprintf("⚠ 同一本书「%s」在 projects/ 下有 %d 个书名目录：%s —— "+
			"拆成多份会让覆盖核对/自动补缺/按名复用只看到一半（缺章误报、重拆不复用旧集）。"+
			"平台**不自动合并**（合并要在同名集之间取舍，那是不可逆的数据搬迁）："+
			"请在资源管理器里把两份并成一份（建议保留带间隔号「·」的那个名字）",
			key, len(dirs), strings.Join(dirs, " / ")))
	}
	sort.Strings(out)
	return out
}

// normalizeBookPunct 把书名里的标点归一化（用于"这是不是同一本书"的比较）。
//
// 支持"特殊字符 保留"的用法：把全角逗号、顿号、空格、中点统一成 `·`，
// 其余原样。归一化后为空（纯标点目录名）时返回空串，调用方跳过。
func normalizeBookPunct(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case '，', '、', ' ', '　', '·', '・', ',', '.':
			sb.WriteRune('·')
		default:
			sb.WriteRune(r)
		}
	}
	// 去掉连续的 `·`（"a，，b" → "a·b"）
	out := sb.String()
	for strings.Contains(out, "··") {
		out = strings.ReplaceAll(out, "··", "·")
	}
	out = strings.Trim(out, "·")
	return out
}

// Create 新建项目:name 仅允许中文/字母/数字/下划线/连字符。
// migrateSeriesDirs 一次性迁移:旧版平铺在 projects/ 根的分集目录
// 移入「书名」子目录(目标已存在则跳过,可安全重试)。
func (s *Store) migrateSeriesDirs() {
	ents, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		m := seriesRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		src := filepath.Join(s.root, e.Name())
		dst := filepath.Join(s.root, m[1], e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		if err := os.Rename(src, dst); err == nil {
			log.Printf("[store] 迁移分集目录:%s → %s/", e.Name(), m[1])
		}
	}
}

func (s *Store) Create(name string, cfg ProjectConfig) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("项目名不能为空")
	}
	if !validName(name) {
		return nil, fmt.Errorf("项目名仅允许中文、字母、数字、下划线、连字符")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := name
	if _, exists := s.projects[id]; exists {
		return nil, fmt.Errorf("项目 %s 已存在", name)
	}
	now := time.Now()
	p := &Project{ID: id, Name: name, CreatedAt: now, UpdatedAt: now, Config: cfg}
	for _, sub := range []string{"plan", "assets/characters", "assets/scenes", "assets/props", "clips", "output", "logs"} {
		if err := os.MkdirAll(filepath.Join(s.Dir(id), sub), 0o755); err != nil {
			return nil, err
		}
	}
	if err := s.saveMetaLocked(p); err != nil {
		return nil, err
	}
	s.projects[id] = p
	return p, nil
}

// List 返回按更新时间倒序的项目列表。
func (s *Store) List() []*Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Project, 0, len(s.projects))
	for _, p := range s.projects {
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// Get 查询项目。
func (s *Store) Get(id string) (*Project, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return nil, false
	}
	cp := *p
	return &cp, true
}

// ensureDefaultParams 读盘兜底:参数的空白补平台默认。
//
// 【为什么必须有这一处】2026-09-26 实测事故:技能端导入的 `project.json` 里
// `config.params` 是空的 `{}`(技能端只写 id/name/config,不写平台的分阶段参数)。
// 后果是一条链:
//
//	assets.source 为空 → 资产阶段**连引擎都不去要**(comfyGen 保持 nil)
//	→ 资产 0/3 却报 ✔ 就绪 → 镜头降级 t2va 纯文生 → 到渲染阶段才因"无可用引擎"失败,
//	批量产线第 1 集就断。
//
// 契约承诺"好包导入后与平台自产包**等价**",而等价的前提是**参数有默认值** ——
// 所以兜底放在**读盘**这一处(不是导入那一处):已经导进来的项目、以及任何来源的
// 老项目,都在同一处被补齐,不必逐个入口去补。
//
// 判据取两个关键字段(Assets.Source 与 Script.Styles 同时为空 = 从没配过),
// 而不是"整个结构等于零值" —— 后者要逐字段比较,参数表每加一个字段就得改一次。
func (p *Project) ensureDefaultParams() {
	if p == nil {
		return
	}
	if strings.TrimSpace(p.Config.Params.Assets.Source) != "" || len(p.Config.Params.Script.Styles) > 0 {
		return
	}
	p.Config.Params = DefaultStageParams()
}

// SaveMeta 更新项目元数据。
func (s *Store) SaveMeta(p *Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.projects[p.ID]
	if !ok {
		return fmt.Errorf("项目不存在: %s", p.ID)
	}
	p.UpdatedAt = time.Now()
	if err := s.saveMetaLocked(p); err != nil {
		return err
	}
	*cur = *p
	return nil
}

func (s *Store) saveMetaLocked(p *Project) error {
	raw, _ := jsonMarshal(p)
	return atomicWrite(s.metaPath(p.ID), raw)
}

// Delete 删除项目(整目录)。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[id]; !ok {
		return fmt.Errorf("项目不存在: %s", id)
	}
	if err := os.RemoveAll(s.Dir(id)); err != nil {
		return err
	}
	delete(s.projects, id)
	return nil
}

// ---------- 方案 / 资产 / 清单读写 ----------

// PlanPath 方案文件路径。
func (s *Store) PlanPath(id string) string {
	return filepath.Join(s.Dir(id), "plan", "plan.json")
}

// SourcePath 源文本路径。
func (s *Store) SourcePath(id string) string {
	return filepath.Join(s.Dir(id), "source.txt")
}

// SaveSource 保存源文本。
func (s *Store) SaveSource(id, text string) error {
	return atomicWrite(s.SourcePath(id), []byte(text))
}

// LoadSource 读取源文本。
func (s *Store) LoadSource(id string) (string, error) {
	raw, err := os.ReadFile(s.SourcePath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(raw), nil
}

// SavePlan 保存分镜方案。
func (s *Store) SavePlan(id string, plan *Plan) error {
	raw, _ := jsonMarshal(plan)
	return atomicWrite(s.PlanPath(id), raw)
}

// LoadPlan 读取分镜方案。
//
// 顺带做一次 mode 空值推断(见 shot_mode.go):契约把"空值"的推断交给平台,而所有
// 消费者(渲染阶段、资产范围、2K 升级)都从这里拿 plan —— 在读取边界推断一次,
// 四处同时正确。不写回磁盘,只影响本次读取的语义。
//
// 2026-09-27 晚再加一次**指令行剔除**(见 h3_clean.go):技能侧重出的包里,全书 2462 镜的
// `h3_prompt` 都混着"写给生成器的话"(REFERENCE ORDER / PICTURE MANIFEST …),而平台是把
// `shot.H3Prompt` 原样拼给模型的。同样放在这个边界上:一处清理,渲染/2K/质检四条消费路径
// 同时干净;剔除条数记在 `StrippedDirectives` 上,由协调器播报一次(不静默改数据)。
func (s *Store) LoadPlan(id string) (*Plan, error) {
	raw, err := os.ReadFile(s.PlanPath(id))
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := jsonUnmarshal(string(raw), &p); err != nil {
		return nil, err
	}
	p.NormalizeShotModes()
	for i := range p.Shots {
		cleaned, n := StripH3WriterDirectives(p.Shots[i].H3Prompt)
		if n > 0 {
			p.Shots[i].H3Prompt = cleaned
			p.StrippedDirectives += n
		}
		// 占位台词标签（`<d>[Chinese]…</d>`）：给模型一个"这里要说点什么"的空标签，
		// 它就会自己编一句或发出怪声 —— 实测那一集 36/74 个 <d> 都是这种（用户报"重复配音"）。
		// 与上一条同源：都在**唯一读取边界**收口，四条消费路径一起干净（见 h3_clean.go）。
		cleaned2, n2 := StripPlaceholderDialogue(p.Shots[i].H3Prompt)
		if n2 > 0 {
			p.Shots[i].H3Prompt = cleaned2
			p.StrippedPlaceholders += n2
		}
	}
	// **自愈三件**放在唯一读取边界（2026-09-28）：读出来的方案就是渲染器将要用的那一份 ——
	// 器物引用对号、缺失的台词标签补全、画外演出素材引用补齐。三条都幂等，且都已证明
	// 「不改创作内容、只把已知量写到位」。放这里而不是只放在 director 里，是因为预检/人审/渲染
	// 三个消费方都必须看到**同一份**方案，否则预检会报渲染器早已修好的假警（实测踩过）。
	HealPlan(&p)
	// 方案标题兜底（2026-09-29 用户报「作品库里的每一集名称没有正确展示」）：
	// 五步专职会话那条路（`split.five_sessions`）**没人负责写 title**，于是
	// `plan.title` 是空串 —— 而作品库卡片、工作台标题、阶段抽屉都拿它当标题显示，
	// 用户看到的是《》。"这一集叫什么"平台**本来就知道**：项目 id 就是
	// `书·第NN集·章名`。取其中的集标签补齐，幂等（只补空值，不覆盖模型/技能侧写的标题）。
	if strings.TrimSpace(p.Title) == "" {
		p.Title = EpisodeLabel(id)
	}
	return &p, nil
}

// EpisodeLabel 从项目 id 里取**集标签**：`书·第01集·章名` → `第01集·章名`；
// 不是分集项目（没有 `·第N集`）时原样返回 id。
//
// 为什么要它：分集项目的 id 里同时装着书名与集名，而作品库是按书分组的 ——
// 组头已经写了书名，卡片里再显示整串 `书·第NN集·章名` 只会被截断成
// `名册之外，八万零一号·第01…`（用户报的"名称没有正确展示"）。
func EpisodeLabel(id string) string {
	id = strings.TrimSpace(id)
	if i := strings.Index(id, "·第"); i >= 0 {
		if label := strings.TrimSpace(id[i+len("·"):]); label != "" {
			return label
		}
	}
	return id
}

// ManifestPath 镜头清单路径。
func (s *Store) ManifestPath(id string) string {
	return filepath.Join(s.Dir(id), "clips", "manifest.json")
}

// LoadManifest 读取镜头清单(不存在返回空表)。
func (s *Store) LoadManifest(id string) (map[string]ClipManifest, error) {
	raw, err := os.ReadFile(s.ManifestPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]ClipManifest{}, nil
		}
		return nil, err
	}
	var m map[string]ClipManifest
	if err := jsonUnmarshal(string(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// SaveManifest 保存镜头清单。
func (s *Store) SaveManifest(id string, m map[string]ClipManifest) error {
	raw, _ := jsonMarshal(m)
	return atomicWrite(s.ManifestPath(id), raw)
}

// AssetsPath 资产映射路径。
func (s *Store) AssetsPath(id string) string {
	return filepath.Join(s.Dir(id), "assets", "asset_map.json")
}

// LoadAssets 读取资产映射(不存在返回空表)。
func (s *Store) LoadAssets(id string) (map[string]Asset, error) {
	raw, err := os.ReadFile(s.AssetsPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Asset{}, nil
		}
		return nil, err
	}
	var m map[string]Asset
	if err := jsonUnmarshal(string(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// SaveAssets 保存资产映射。
//
// ⚠️ 这是一次**整体覆盖写**。当心"读一次 → 改 → 写回"的调用方（2026-09-29 运行时审计 P1-3）：
// 资产表是**被允许并发写**的 —— 管线跑资产阶段（窗口几十分钟）的同时，用户可以点
// 「重新生成」出图、截帧定妆、上传资产、2K 定稿，每个都是一次"读快照 → 干几分钟活 → 写回"。
// 于是资产阶段收尾时会把它开场时那份**旧快照**整体写回，用户刚重生成的那张脸在盘上有、
// 在表里被回滚成旧文件（渲染取参考图走 `assets[...]`，于是"刚重生成完，渲出来还是旧脸"），
// 而两条链各自都报了成功。
//
// 修法不是在这里加锁就完（那样只能防"同时写"），而是**让写入变成按 key 合并**：
// `UpdateAssets` 在同一把项目锁内"重读 → 改 → 写回"，所有写方都走它。
// 直接调本函数的调用方要么是一次性全量重建（资产阶段就是这类，它本来就该覆盖），
// 要么应当改走 `UpdateAssets`。
func (s *Store) SaveAssets(id string, m map[string]Asset) error {
	raw, _ := jsonMarshal(m)
	return atomicWrite(s.AssetsPath(id), raw)
}

// UpdateAssets 原子地改资产表：**同一把项目锁内**重读 → 交给 mutate 改 → 写回。
//
// 所有"只改自己那几个 key"的写方都该走这里（重生成 / 截帧 / 上传 / 2K 定稿），
// 这样并发写不会互相覆盖：后进来的会看到前一个已经落盘的结果。
// mutate 返回错误则不写盘（原表保持不动）。
//
// 锁是**按项目**的（不同项目互不阻塞），存在 Store 上的一个 map 里。
func (s *Store) UpdateAssets(id string, mutate func(map[string]Asset) error) error {
	mu := s.assetsLock(id)
	mu.Lock()
	defer mu.Unlock()
	cur, err := s.LoadAssets(id)
	if err != nil {
		return err
	}
	if cur == nil {
		cur = map[string]Asset{}
	}
	if err := mutate(cur); err != nil {
		return err
	}
	return s.SaveAssets(id, cur)
}

// assetsLock 取（必要时建）某个项目的资产写锁。
func (s *Store) assetsLock(id string) *sync.Mutex {
	s.assetLocksMu.Lock()
	defer s.assetLocksMu.Unlock()
	if s.assetLocks == nil {
		s.assetLocks = map[string]*sync.Mutex{}
	}
	mu, ok := s.assetLocks[id]
	if !ok {
		mu = &sync.Mutex{}
		s.assetLocks[id] = mu
	}
	return mu
}

// ---------- 工具 ----------

func validName(name string) bool {
	for _, r := range name {
		if !IsAllowedNameRune(r) {
			return false
		}
	}
	return true
}

// IsAllowedNameRune 项目名/集名里允许的字符（**唯一出处**：validName 与调用方共用它，
// 免得"校验一套字表、清洗另一套字表"，一处松一处紧）。
func IsAllowedNameRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r >= 0x4e00 && r <= 0x9fff: // CJK 统一表意
		return true
	case r == '_', r == '-', r == '·': // 间隔号·(剧集命名「主名·第N集·标题」用)
		return true
	case IsFullWidthPunct(r): // 全角标点（技能侧 2026-09-28 规定只准全角）
		return true
	}
	return false
}

// isFullWidthPunct 技能侧 2026-09-28 规定的**允许全角标点集**。
//
// 【为什么平台名里要放行全角标点】技能侧的通知（`docs/技能侧通知-章名书名符号规定-2026-09-28.md`）
// 把章名/书名的符号统一为「**只准全角标点，禁一切 ASCII 标点与空格**」，理由是文件系统、
// ASS 覆写块与导出分隔符三处会被半角打穿。而**平台此前只允许 中文/字母/数字/_/-/·** ——
// 于是书名《补碑人改命，守钟女逆时》在盘上被静默改成 `补碑人改命·守钟女逆时`，
// 章名「她替他挡账（二）」会被改成「她替他挡账·二」：**平台在替技能侧改字，而且不吭声**。
//
// 技能侧同时明确要求"不要静默替换"（章名就是集名，改字会让成片与集号记录对不上）。
// 既然他们已保证章名/书名里**不会出现** ASCII 标点与空格，平台就该**照着放行全角**，
// 而不是继续把合规字符改坏。放行只会让更多名字合法（旧目录名不受影响）。
func IsFullWidthPunct(r rune) bool {
	switch r {
	case '，', '。', '！', '？', '；', '、', '：', '·', '—', '…',
		'《', '》', '〈', '〉', '「', '」', '『', '』', '【', '】', '（', '）', '〔', '〕':
		return true
	}
	return false
}

// ValidProjectName 项目名是否合法（规则与 Create 校验的**同一份**实现）。
//
// 为什么导出：导入侧要在"建项目"之前自检 —— 用户点一次「脚本创作」是**整批 40 集**，
// 一个标题里带了全角逗号就会让整批导入失败（2026-09-27 实测：
// `求你们骂我·第02集·骂人一处，现钱五文` → "项目名仅允许中文、字母、数字、下划线、连字符"）。
// 所以调用方先清洗、再自检、真不合规就退用安全名，绝不把整批拖下水。
func ValidProjectName(name string) bool { return validName(name) }

// FingerprintFile 计算文件内容指纹(md5 前 16 位),用于资产变更检测。
// FingerprintFile 文件内容指纹（md5 前 16 位十六进制）。
//
// ## 为什么要记忆化（2026-09-29 性能审计 #3）
//
// 调用点里有一处是**逐镜循环**（`stages_render.go` 拼参考图清单），而一张参考图
// 1~8MB：每镜 3 张（定妆照 + 三视图板 + 场景）⇒ 每镜 5~25 ms 的纯重复劳动 + 每镜
// 5~25 MB 的瞬时分配（实测：1MB 1.13 ms / 5MB 6.2 ms / 8MB 10.0 ms）。
// 而**同一张图在一集里会被反复指纹**（每个用到该角色的镜头都要算一次），内容根本没变。
//
// 键取 `(路径, 大小, 修改时间)`：这三样任一变了就重算 —— 覆盖"文件被换掉"的所有现实形态
// （重生成、上传、截帧、2K 定稿都会改 mtime），而 mtime 的精度足够（写盘时间不会相同）。
//
// 缓存的是**指纹字符串**（16 字符）而不是文件内容，所以内存占用可以忽略；
// 仍然给了条数上限，超了整表清空（简单、无锁竞争陷阱；指纹是纯函数，清空只是下次重算）。
var (
	fpMu    sync.Mutex
	fpCache = map[string]string{}
)

// fpCacheMax 缓存条数上限：一集几十镜×几张 = 几百条，4096 够几部剧的量。
const fpCacheMax = 4096

func FingerprintFile(path string) string {
	st, statErr := os.Stat(path)
	if statErr != nil {
		return ""
	}
	key := fmt.Sprintf("%s\x1f%d\x1f%d", path, st.Size(), st.ModTime().UnixNano())
	fpMu.Lock()
	if v, ok := fpCache[key]; ok {
		fpMu.Unlock()
		return v
	}
	fpMu.Unlock()

	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := md5.Sum(raw)
	fp := hex.EncodeToString(sum[:])[:16]

	fpMu.Lock()
	if len(fpCache) >= fpCacheMax {
		fpCache = map[string]string{} // 整表清空：指纹是纯函数，重算即可
	}
	fpCache[key] = fp
	fpMu.Unlock()
	return fp
}

// FingerprintText 计算文本指纹。
func FingerprintText(parts ...string) string {
	h := md5.Sum([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(h[:])[:16]
}

// atomicWrite 原子写。实现收敛到 fsutil,包内 5 个调用点保持原样。
// 跨项目资产库(library)也需要同一条保证,故做成公共工具而非各包各写一份。
func atomicWrite(path string, data []byte) error {
	return fsutil.WriteFileAtomic(path, data)
}

// PlaceholderMarker 失败镜在清单里的**标记词**（官方 fallback 阶梯第 4 级的原话：
// "Skip the shot and add a `placeholder: missing clip` note for downstream review"）。
//
// 放在 project 包是因为它有两个消费方：渲染阶段写入、合成阶段据此判定"这一段是占位、可以进成片
// 但要大声播报"。定义在 pipeline 里会让 project 包的读者看不到这个词的来处。
const PlaceholderMarker = "placeholder: missing clip"
