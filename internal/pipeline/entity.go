package pipeline

import (
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// entity.go 角色卡的**实体类型**,以及每类各自的渲染方法。
//
// 为什么必须有这个文件(2026-09-24 用户报「阿凿是一只铜雀却渲染成人物」):
// 此前 pipeline 把所有 characters[] 条目都当"人"处理 —— 定妆照提示词尾巴统一追加
// `plain background, upper-body framing, front-facing`(半身正面肖像),身份约束统一写
// "脸型/五官/肤色/年龄感必须照抄参考图",多视角统一走"原地转身"。于是一只"巴掌大的
// 机关铜雀"被要求"半身、正面、照抄脸" —— 模型只能给它画一张人脸、一个半身像。
// 实测该项目的定妆照就是**一个女孩的半身肖像**(已核对原图)。
//
// 类型不同,**三处渲染方法都不同**,必须分别调用:
//  1. 定妆照(Krea-2):取景/构图 —— 人是半身正面;器物是整件居中三视图;灵宠/妖兽是
//     全身兽形;无实体是整团形体;Q 版是大头小身;
//  2. 多视角(H3 转身视频):人是原地转身;器物是**转台自转**;妖兽是绕行环视;无实体是
//     原地缓慢旋动、形体持续漂移;Q 版是 Q 版转身;
//  3. 镜头锚定与画风(H3 渲染):身份锁的落点不同 —— 人是"脸照抄",器物是"材质/结构/
//     部件照抄且**不得拟人化**",无实体是"不得实体化、不得落地成影"。
//
// 类型判定有三层,优先级从高到低:① 方案里 LLM 填的 kind 字段(分镜提示词已要求分类)
// ② 代码按名称+描述+提示词**推断**(存量项目没有这个字段,阿凿正是这一类)
// ③ 兜底 human。判定只影响走哪套渲染方法,不改写角色描述本身。
// 用户的最终纠错口子在 UI:资产定妆详情里可手动改类型(见 server 的 putCharacter)。

// EntityKind 实体类型。取值与 project.Character.Kind 一致(json 存储用这些英文键)。
type EntityKind string

const (
	KindHuman       EntityKind = "human"       // 人
	KindObject      EntityKind = "object"      // 物:器物/机关/傀儡/雕像/道具类"角色"
	KindSpiritPet   EntityKind = "spirit_pet"  // 灵宠
	KindBeast       EntityKind = "beast"       // 妖兽
	KindIncorporeal EntityKind = "incorporeal" // 无实体:鬼魂/灵体/意识/声音/投影
	KindChibi       EntityKind = "chibi"       // Q 版
)

// EntityKinds 全部类型(顺序即 UI 展示顺序)。
var EntityKinds = []EntityKind{KindHuman, KindObject, KindSpiritPet, KindBeast, KindIncorporeal, KindChibi}

// EntityKindLabel 中文标签(UI 与日志用)。
func EntityKindLabel(k EntityKind) string {
	switch k {
	case KindObject:
		return "物"
	case KindSpiritPet:
		return "灵宠"
	case KindBeast:
		return "妖兽"
	case KindIncorporeal:
		return "无实体"
	case KindChibi:
		return "Q版"
	default:
		return "人"
	}
}

// KindHint 给用户看的一句话说明(UI 提示用)。
func KindHint(k EntityKind) string {
	switch k {
	case KindObject:
		return "器物/机关/傀儡/雕像等非生命体(如一只铜雀):整件居中取景 + 转台自转,并禁止拟人化"
	case KindSpiritPet:
		return "小型灵物/宠物:全身兽形取景,保留自身解剖结构"
	case KindBeast:
		return "大型或凶猛的非人生物:全身取景,体型体量照原文,不得缩小成萌宠"
	case KindIncorporeal:
		return "鬼魂/灵体/意识/投影等无实体存在:半透明形体,禁止落地成影与实体化"
	case KindChibi:
		return "Q 版/大头身比例:按 Q 版取景与转身渲染"
	default:
		return "人物:半身正面定妆照 + 原地转身"
	}
}

// NormalizeEntityKind 容错解析:接受英文键与中文标签;不认识返回空串(交给推断)。
func NormalizeEntityKind(s string) EntityKind {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "human", "person", "人", "人物", "人类":
		return KindHuman
	case "object", "artefact", "artifact", "prop", "物", "器物", "物件", "道具", "机关", "傀儡", "木偶":
		return KindObject
	case "spirit_pet", "spiritpet", "pet", "familiar", "灵宠", "宠", "灵物":
		return KindSpiritPet
	case "beast", "monster", "creature", "妖兽", "兽", "魔兽", "妖怪":
		return KindBeast
	case "incorporeal", "spirit", "ghost", "phantom", "无实体", "灵体", "鬼魂", "魂体", "无形":
		return KindIncorporeal
	case "chibi", "q", "sd", "q版", "q 版", "呆萌":
		return KindChibi
	}
	return ""
}

// 推断用的关键词。**只用于存量项目兜底**(新方案由 LLM 填 kind),所以宁可保守:
// 命中强信号才偏离 human,避免把"肤色暗铜哑光 + 旧铜环"的真人误判成器物。
var (
	// 强"非人"信号:出现即判物件(即使描述里有人形/年龄词 —— 机关人、傀儡戏子都算物件)
	objectStrongWords = []string{
		"铜雀", "器物", "机关", "傀儡", "木偶", "偶人", "人偶", "提线", "纸人", "面塑",
		"雕像", "石像", "铜像", "泥塑", "灯笼", "走马灯", "伞妖", "机械", "铆钉", "齿轮",
		"木雕", "铜雕", "陶俑", "俑", "法器", "令牌", "符纸", "玉玺", "印章", "面具", "机枢",
		"puppet", "marionette", "doll", "statue", "figurine", "automaton", "android",
		"mechanical", "clockwork", "rivet", "gear", "artefact", "artifact", "golem",
		"bronze sparrow", "mechanical bird", "wooden man", "clockwork",
	}
	// 无实体信号
	incorporealWords = []string{
		"鬼魂", "幽灵", "幽魂", "魂魄", "灵体", "神魂", "元神", "虚影", "残影", "幻影",
		"无形", "无实体", "缥缈", "半透明", "投影", "全息", "一缕意识", "意识体", "声音本体",
		"ghost", "phantom", "wraith", "spectre", "specter", "incorporeal", "intangible",
		"semi-transparent", "translucent spirit", "hologram", "disembodied", "spirit form",
	}
	// 妖兽信号(体量大/凶猛/非宠物)
	beastWords = []string{
		"妖兽", "魔兽", "妖怪", "巨龙", "龙", "麒麟", "饕餮", "穷奇", "巨狼", "巨蟒", "巨兽",
		"猛兽", "凶兽", "恶兽", "虎妖", "蛇妖", "狐妖", "魔王", "demon", "monster", "beast",
		"dragon", "wyvern", "leviathan", "chimera", "dire wolf", "serpent",
	}
	// 灵宠信号(小型伴随生物)
	spiritPetWords = []string{
		"灵宠", "宠物", "小兽", "小灵", "灵狐", "灵鹤", "灵蝶", "灵雀", "幼兽", "一团灵",
		"spirit pet", "familiar", "companion creature", "little spirit", "spirit beast cub",
	}
	// Q 版信号
	chibiWords = []string{"q版", "q 版", "q-版", "呆萌", "大头身", "chibi", "super deformed", "sd figure"}
	// 明确的"人"信号(仅用于在**没有**强非人信号时确认 human,不参与否定)
)

// InferEntityKind 由角色卡文本推断实体类型(存量项目兜底 + kind 缺失时使用)。
//
// 判定顺序即优先级:无实体 > 物件 > 妖兽/灵宠 > Q 版 > 人。
// 为什么物件排最前:一只"机关铜雀"的描述里同时有"雀"(动物)、"铜"(材质)、"铆钉"(结构),
// 而它的本体是**做出来的东西**;把它判成动物或人都会画出完全错误的东西。
func InferEntityKind(name, description, imagePrompt string) EntityKind {
	hay := strings.ToLower(strings.Join([]string{name, description, imagePrompt}, "\n"))
	if hay == "" {
		return KindHuman
	}
	hit := func(list []string) string {
		for _, w := range list {
			if strings.Contains(hay, strings.ToLower(w)) {
				return w
			}
		}
		return ""
	}
	if hit(incorporealWords) != "" {
		return KindIncorporeal
	}
	if hit(objectStrongWords) != "" {
		return KindObject
	}
	if hit(beastWords) != "" {
		return KindBeast
	}
	if hit(spiritPetWords) != "" {
		return KindSpiritPet
	}
	if hit(chibiWords) != "" {
		return KindChibi
	}
	return KindHuman
}

// ResolveEntityKind 解析角色卡最终使用的类型:显式字段优先,缺失/非法则推断。
func ResolveEntityKind(ch project.Character) EntityKind {
	if k := NormalizeEntityKind(ch.Kind); k != "" {
		return k
	}
	return InferEntityKind(ch.Name, ch.Description, ch.ImagePrompt)
}

// EntityProfileOf 取该类型的渲染方法集合。
func EntityProfileOf(k EntityKind) entityProfile {
	if p, ok := entityProfiles[NormalizeEntityKind(string(k))]; ok {
		return p
	}
	return entityProfiles[KindHuman]
}

// EntityKindMarker 该类型定妆照提示词的标记串(装配时写入,回来可核对)。
//
// 用途:资产表里存着**当时装配出来的** image_prompt —— 如果它不含当前类型的标记,
// 说明这张定妆照是"按别的类型(或旧版本)渲染的",界面上要提示重出。这正是
// 「铜雀的人像定妆照静静躺在项目里、还被当第一参考喂回渲染」能发生的那个缺口。
func EntityKindMarker(k EntityKind) string {
	return EntityProfileOf(k).SheetMarker
}

// TurnVerb 该类型的"多视角"动作词(日志用:人转身 / 器物转台自转 / 灵体原地旋动)。
func (k EntityKind) TurnVerb() string {
	switch NormalizeEntityKind(string(k)) {
	case KindObject:
		return "转台自转"
	case KindIncorporeal:
		return "原地旋动"
	case KindSpiritPet, KindBeast:
		return "转身环视"
	default:
		return "原地转身"
	}
}

// ResolveShotEntities 解析本镜出场角色的类型(供锚定与画风块按类型分发)。
func ResolveShotEntities(plan *project.Plan, shot *project.Shot) map[string]EntityKind {
	out := make(map[string]EntityKind, len(shot.Characters))
	for _, cid := range shot.Characters {
		for _, ch := range plan.Characters {
			if ch.ID == cid {
				out[cid] = ResolveEntityKind(ch)
			}
		}
	}
	return out
}

// ShotEntityKinds 本镜实际出场角色的类型集合(去重,固定顺序)。
func ShotEntityKinds(plan *project.Plan, shot *project.Shot) []EntityKind {
	m := ResolveShotEntities(plan, shot)
	var out []EntityKind
	for _, k := range EntityKinds {
		for _, v := range m {
			if v == k {
				out = append(out, k)
				break
			}
		}
	}
	return out
}
