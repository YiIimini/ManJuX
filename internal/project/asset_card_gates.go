package project

import (
	"fmt"
	"strings"
)

// asset_card_gates.go 角色卡 / 道具卡 / 三视图板的**补充判据**（2026-09-28 欠账小项）。
//
// 本批四条，出处逐条写在函数上：
//
//	#21 角色年龄 —— 用户报过「中年人渲染出来跟个小年轻一样」；年龄要在卡片里成字段、
//	    并在提示词里成硬锚（模型的默认审美年轻化）。
//	#23 道具材质对撞 —— 《二、人物道具提取指令》对**服装**要求"材质对撞至少 4 种高级材质"，
//	    而**道具完全没有**这条；道具是单独出图的，材质单薄直接表现为"一块塑料"。
//	#24 三视图板纯白底 —— 用户《三》「白底」；板子带场景会把"规格书"变成"定妆照"。
//	#11 场景配套物件 2–4 组 —— 用户《四》§4.6「每个场景总计控制在 2-4 组，前景 1-2 组、
//	    中景 1-2 组」。`PlanSceneFixedPropMin/Max` 长期是**死常量**（全仓只有定义处）。

// PlanAgeAnchorWords 提示词里能表达年龄的锚点词（中文卡与英文提示词都认）。
//
// 判据只看"有没有把年龄写成锚"，不管写得对不对 —— 与"比例锚"那条门同一个粒度。
var PlanAgeAnchorWords = []string{
	"岁", "年龄", "中年", "老年", "少年", "青年", "孩童",
	"year-old", "years old", "aged", "middle-aged", "elderly", "teen", "young man", "old man", "old woman",
}

// PlanMaterialWords 材质词表（《二》要求"材质对撞至少 4 种**高级材质**"）。
//
// 只收**具体的材质名**，不收"质感/光泽"这类形容 —— 判据要判的是"有没有真的换了材料"。
var PlanMaterialWords = []string{
	"silk", "brocade", "linen", "cotton", "wool", "leather", "fur", "velvet", "lace",
	"bronze", "brass", "copper", "iron", "steel", "gold", "silver", "jade", "porcelain",
	"wood", "bamboo", "lacquer", "glass", "ceramic", "stone", "bone", "horn", "rattan",
	"丝绸", "锦缎", "麻布", "棉", "毛呢", "皮革", "皮毛", "天鹅绒", "蕾丝",
	"青铜", "黄铜", "紫铜", "铁", "精钢", "金", "银", "玉", "瓷", "木", "竹", "漆", "琉璃", "骨", "角", "藤",
}

// humanoidKind 这张卡是不是人形（**人 / Q 版**）—— 与 pipeline 的 `HumanoidBoardKind` 同一口径。
//
// 这里用字符串判而不是 import pipeline：`project` 是最底层的数据包，
// 反向依赖 `pipeline` 会成环（本仓的模块纪律：依赖单向无环）。
// 值必须与 `pipeline.KindHuman = "human"` 对齐 —— 有测试钉这一条。
func humanoidKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "human", "chibi":
		return true
	}
	return false
}

// AgeIssues 角色卡缺年龄锚的（返回角色名）。
//
// 【#21】用户报过「中年人渲染出来跟个小年轻一样」。`Character.Age` 是结构字段，
// 但如果提示词里没有对应的**英文锚**，渲染照样按默认审美往年轻画 ——
// 字段只解决"能对账"，锚才解决"画得对"。
func AgeIssues(chars []Character) []string {
	var out []string
	for _, c := range chars {
		if !humanoidKind(c.Kind) {
			continue // 器物/妖兽/无形体没有年龄这回事
		}
		// **结构字段本身就是锚**：`Age` 写了就不必再猜。
		if strings.TrimSpace(c.Age) != "" {
			continue
		}
		text := strings.ToLower(c.Description + " " + c.ImagePrompt + " " + c.BoardPrompt)
		if len(matchWords(text, PlanAgeAnchorWords)) == 0 {
			out = append(out, c.Name)
		}
	}
	return out
}

// MaterialIssues 道具卡材质过于单一的（返回道具名 + 命中了几种）。
//
// 【#23】《二》对服装写着"材质对撞至少 4 种高级材质"，而**道具完全没有这条**。
// 道具图是拿卡当提示词单独出的，材质单薄就是"一块塑料"的来源。
// 阈值比服装低（3 种）：道具通常只由一两种材料构成，强求 4 种会逼出假材质。
func MaterialIssues(props []Prop) []string {
	const need = 3
	var out []string
	for _, p := range props {
		text := strings.ToLower(p.Description + " " + p.ImagePrompt + " " + p.EN)
		n := len(matchWords(text, PlanMaterialWords))
		if n < need {
			out = append(out, fmt.Sprintf("「%s」(%d 种)", p.Name, n))
		}
	}
	return out
}

// BoardWhiteBackdropIssues 三视图板缺"纯白无缝底"的（返回角色名）。
//
// 【#24】用户《三》写明"白底"。板子是**规格书** —— 带上场景（石板地、木地板、背景墙）
// 之后模型会把精力用在画环境上，15 格的身份信息被摊薄，而且底不一致就没法当角度参考用。
func BoardWhiteBackdropIssues(chars []Character) []string {
	var out []string
	for _, c := range chars {
		if !humanoidKind(c.Kind) {
			continue
		}
		low := strings.ToLower(c.BoardPrompt + " " + c.ImagePrompt)
		if !strings.Contains(low, "white") && !strings.Contains(low, "白底") && !strings.Contains(low, "白色背景") {
			out = append(out, c.Name)
		}
	}
	return out
}

// PlanScenePropGroupMin / PlanScenePropGroupMax 场景卡"环境配套物件"的组数区间。
//
// 【#11】用户《四》§4.6：「每个场景总计控制在 **2-4 组**，前景 1-2 组、中景 1-2 组」。
// 平台侧原有的常量不在这里 —— 见下方 ScenePropGroupIssues 的说明。
const (
	PlanScenePropGroupMin = 2
	PlanScenePropGroupMax = 4
)

// scenePropGroupWords 能表达"一组配套物件"的词。
//
// 为什么用词表而不是数数：场景卡是散文（"前中景合计 2-4 组"是**写作要求**，
// 不是结构化字段）。用"有没有成组的物件描写"做判据，是这件事在文本层面**能做到的最好**；
// 要真数得准，得先让场景卡有 `fixed_props []string` 字段 —— 那是技能侧改口径的事，
// 平台不单方面编一个（见下方注释）。
var scenePropGroupWords = []string{
	"group of", "groups of", "set of", "sets of", "furnishings", "props", "objects",
	"foreground", "midground", "mid-ground", "middle ground",
	"一组", "两组", "几组", "陈设", "摆设", "器物", "前景", "中景",
}

// ScenePropGroupIssues 场景卡没有"环境配套物件"描写的（返回场景名）。
//
// ⚠️ **这条判据的边界要说清楚**：用户《四》要的是"**2-4 组**"，
// 而平台**数不出"组"** —— `Scene` 没有配套物件字段，散文里的"一组"也没有可靠的计数方式
// （"一组石灯与两个石墩"到底算一组还是三件？）。所以这条门只判**下限方向**：
// **一处都没提**就是错的（板子会空得像一张纯风景照），提了则不猜它够不够。
//
// **真正的修法是加字段**（`Scene.FixedProps []string`），那是技能侧产物格式的改动，
// 由技能侧先吐出来、平台再读 —— 平台不单方面编一个字段名，否则两边口径立刻分叉。
// 这条写在这里，免得下次有人看到"2-4"就以为门写漏了。
func ScenePropGroupIssues(scenes []Scene) []string {
	var out []string
	for _, sc := range scenes {
		text := strings.ToLower(sc.Description + " " + sc.ImagePrompt)
		if len(matchWords(text, scenePropGroupWords)) == 0 {
			out = append(out, sc.Name)
		}
	}
	return out
}
