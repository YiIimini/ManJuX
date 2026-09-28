package project

import (
	"fmt"
	"strings"
)

// asset_gate.go 场景卡与道具卡的**补充产出侧判据**。
//
// ## 由来（2026-09-28 用户问「道具和场景的呢」）
//
// 已有的门（见 `docs/门目录.md`）覆盖了：场景 id / 命名 4 字 / 板子贫瘠 /
// landmarks＋light_state；道具完整性 / 悬空引用 / 一件都没提取。
// 这里补的是**用户参考库里明写、而平台一条判据都没有**的那些：
//
//	场景（用户《四、场景提示词指令》）：
//	  · 无人纯场景（严禁人影）—— 此前**只有装配点兜底**，产出侧无门；
//	  · 事件隔离（禁血渍/尸体/打斗痕迹/遗落物）—— 同上；
//	  · 四要素里的「具体时间」与「内/外景」—— 卡上没有字段，只能从提示词文本判。
//	道具（用户《二、人物道具提取指令》八项）：
//	  · 「功能作用」与「独有特征」两项**没有任何落点**（代码只查了六项）。
//
// 判据一律**从卡文本判**（场景卡没有"时间/内外景"字段，道具卡没有"功能"字段）——
// 不为了好判去加字段：字段多了要维护、要迁移，而这里要的只是"有没有写"。

// scenePeopleWords 场景板**不该出现**的人物词。
//
// 只收**具体的人物名词**，**不收** `unpeopled` / `deserted` / `empty` —— 那些是平台自己
// 追加的"空场"正向串（见 entity_prompts.go 的 SheetFraming），收了就会把合规板判成违规。
var scenePeopleWords = []string{
	"crowd", "crowds", "passersby", "passers-by", "villagers", "townsfolk",
	"a few people", "people walking", "figures in the distance", "silhouette of a person",
	"人群", "行人", "路人", "人影", "围观",
}

// sceneEventTraceWords 场景板**不该出现**的事件痕迹词（用户《四》§4.3 事件隔离）。
//
// 口径原话："只画事件发生前的地点固有状态" —— 板上带着血渍/尸体/刚破碎物/遗落道具，
// 那一集每一镜都像"事情已经发生过"（与"板上画了人 → 每镜都出现同一个陌生人"同一条机理）。
// 允许的是**时间的痕迹**（风化/旧锈/苔藓/旧裂纹/褪色/积尘），那些不在本表里。
var sceneEventTraceWords = []string{
	"bloodstain", "blood stain", "pool of blood", "corpse", "dead body", "carcass",
	"broken glass", "shattered glass", "burnt embers", "smouldering", "smoldering",
	"fight damage", "claw marks", "footprints", "tire tracks", "dropped weapon",
	"血迹", "血渍", "尸体", "打斗痕迹", "脚印", "车辙", "余烬", "遗落",
}

// SceneIssueKind 场景板问题的种类。
type SceneIssueKind string

const (
	SceneHasPeople SceneIssueKind = "people" // 板上有具体的人物名词
	SceneHasEvent  SceneIssueKind = "event"  // 板上有事件痕迹名词
)

// SceneIssue 一条场景板问题（**只给数据**，文案在接线处 —— 理由同 camera_gate.go）。
type SceneIssue struct {
	Kind  SceneIssueKind
	Name  string
	Words []string
}

// SceneIssues 场景板的两条判据（空 = 没问题）。
//
// **必须走否定语境判断**：平台自己的固定串里就写着 `no blood, no corpses, …`
// （`PlanScenePreEventClause`）—— 不排除否定语境的话，**合规的场景板会被自家禁词判成违规**
// （第一版就是这么栽的，`TestValidatePlanCleanSample` 当场咬住）。
func SceneIssues(scenes []Scene) []SceneIssue {
	var out []SceneIssue
	for _, sc := range scenes {
		low := strings.ToLower(sc.ImagePrompt + " " + sc.Description)
		if hits := matchWords(low, scenePeopleWords); len(hits) > 0 {
			out = append(out, SceneIssue{Kind: SceneHasPeople, Name: sc.Name, Words: hits})
		}
		if hits := matchWordsOutsideNegation(low, sceneEventTraceWords); len(hits) > 0 {
			out = append(out, SceneIssue{Kind: SceneHasEvent, Name: sc.Name, Words: hits})
		}
	}
	return out
}

// matchWordsOutsideNegation 与 matchWords 相同，但**跳过否定语境里的命中**。
//
// 复用 `negatedBefore`（景别/文字那几条门用来判"没写"的同一套）：`no corpses`、
// `without footprints` 这类是**在禁止**这件事，不是在描述它。
func matchWordsOutsideNegation(low string, words []string) []string {
	var hits []string
	seen := map[string]bool{}
	for _, w := range words {
		lw := strings.ToLower(w)
		idx := 0
		for {
			i := strings.Index(low[idx:], lw)
			if i < 0 {
				break
			}
			at := idx + i
			if !negatedBefore(low, at) {
				if !seen[w] {
					seen[w] = true
					hits = append(hits, w)
				}
				break
			}
			idx = at + len(lw)
		}
	}
	return hits
}

// propFeatureWords 道具卡里**该写出来**的两项（用户《二》八项里的第 7、8 项）。
//
// 判据是"有没有这类词",不是"写得对不对" —— 与既有那条"道具卡完整性"门同一个粒度。
var (
	propFunctionWords = []string{"function", "used for", "serves as", "作用", "用途", "能做什么", "功能"}
	propSignatureWord = []string{"distinct", "signature", "unique", "notable", "特征", "独有", "记号", "标志"}
)

// PropIssue 一条道具卡问题。
type PropIssue struct {
	Name string
	Miss string // 缺哪一项
}

// PropFeatureIssues 道具卡缺「功能作用 / 独有特征」的（空 = 没问题）。
//
// 【为什么单列】既有那条"道具卡完整性"门只查 6 项（id/中文名/英文外观/中文描述/出图提示词/1:1 白底），
// 而用户《二》明写八项 —— 「功能作用」与「独有特征」**从来没有落点**。
// 这两项不是装饰：「功能边界」常常就是后面情节的钥匙（用户《重点道具表》里写着
// "托不起人这种限制往往就是情节的钥匙"），而"独有特征"是它在画面里被认出来的唯一依据。
func PropFeatureIssues(props []Prop) []PropIssue {
	var out []PropIssue
	for _, p := range props {
		text := strings.ToLower(p.Description + " " + p.ImagePrompt + " " + p.EN)
		if len(matchWords(text, propFunctionWords)) == 0 {
			out = append(out, PropIssue{Name: p.Name, Miss: "功能作用"})
		}
		if len(matchWords(text, propSignatureWord)) == 0 {
			out = append(out, PropIssue{Name: p.Name, Miss: "独有特征"})
		}
	}
	return out
}

// PropIssueText 把一条道具缺失写成给用户看的一句话。
func PropIssueText(iss PropIssue) string {
	return fmt.Sprintf("道具「%s」缺「%s」", iss.Name, iss.Miss)
}

// matchWords 在已小写的文本里找命中的词（返回命中的那几个，去重）。
func matchWords(low string, words []string) []string {
	var hits []string
	seen := map[string]bool{}
	for _, w := range words {
		if strings.Contains(low, strings.ToLower(w)) && !seen[w] {
			seen[w] = true
			hits = append(hits, w)
		}
	}
	return hits
}
