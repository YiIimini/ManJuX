package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 锚表原文 → 已存在卡的 description 的回填（2026-10-07 用户问：「人物生成的角色提示词去哪了？
// 墨洄、尉迟彻、断桅、邵舷、霜见等不是主角的角色都随机生成的吗？」）。
//
// 夹具 = **真机第01集的现场**照抄：
//   - 卡片：祁七/Q版（LLM 直出，描述与提示词都齐）、系统机器人（LLM 直出）；
//     霜见/墨洄/邵舷/尉迟彻/断桅 —— `description` 空 + `image_prompt` 是锚表兜底串（plan.json 原文）；
//   - 锚表：source.txt 里那五节的**原文**（逐项写全）。
//
// 这一组判据要钉住的是"断在哪一环"：只补兜底串（不补中文描述）⇒ 专职翻译会话永远不启动
// ⇒ 出图只拿到"一个古装人、某岁" ⇒ 撞脸（真机日志：墨洄与霜见长得像同一个人，重出一次仍像）。
const (
	anchorShuangjian = `霜见（定位：第二主角／通信手。拾荒船团的读数员，全书唯一的「只信数字的人」）
- 性别：女

- 年龄：少年，17–19 岁
- 脸型：瘦长脸，下颌线清晰，轮廓分明不生硬、无充气感
- 眉眼：灰蓝色瞳孔；柳叶眼（介于杏眼与丹凤之间，古典内敛），双眼皮浅，眼尾略垂；眼神克制
- 独特记号：颈侧一道义体接缝纹（黑色机械与皮肤的交界线，正面可见）＋ 双耳银质细长坠式耳环
- 发型三要素：无束／中长过耳至下颌／额前蓬松微卷刘海盖过眉毛、碎发遮眼
- 首套服装：黑色高领针织 ＋ 灰白围巾（毛领）＋ 黑色哑光机能外套（织带与贴布细节）
- 比例锚_机器：7 头身
`
	anchorMohui = `墨洄（定位：反派 · 静默局核验官。全书的核心伪善反派）
- 性别：女

- 年龄：青年，25–30 岁
- 脸型：长菱形脸（高冷疏离，颧骨微宽＋下颌收窄）
- 眉眼：冷调银蓝色眼影，眼线上扬；丹凤眼（内尖外翘，凌厉高傲）
- 发型三要素：无束／黑色长直及背／额前中分、发丝贴颊（湿发质感）
- 比例锚_机器：8 头身
`
	anchorShaoxian = `邵舷（定位：反派 · 归墟环名册官。伪善型，把注销配额换成自己的档位）
- 性别：男

- 年龄：中年，40–48 岁
- 脸型：方中带圆脸，颧骨高、面中塌、下颌方
- 独特记号：右手中指一枚磨秃的金属指套（点了太多年名册）
- 比例锚_机器：7.5 头身
`
	anchorYuchiche = `尉迟彻（定位：反派 · 静默局值星长。硬对手，不降智，按条例办事）
- 性别：男

- 年龄：青年，30–36 岁
- 脸型：方中带圆脸，下颌方，眉骨压眼
- 眉眼：剑眉（眉峰上扬、线条直）；细长单眼皮，眼神直、不躲
- 独特记号：左手小指缺一节（旧值勤留下的）＋ 腕上一圈编号金属环
- 首套服装：深灰值勤装甲（肩甲有划痕）＋ 高领内衬；装甲膝盖处磨得发亮
- 比例锚_机器：7.5 头身
`
	anchorDuanwei = `断桅（定位：工具人 · 拾荒船团的老拾荒人。把她从回收舱里捡回来的那个）
- 性别：女

- 年龄：老年，58–66 岁
- 身形：瘦，背驼，走路
`
)

func realPlanFixture() *project.Plan {
	return &project.Plan{
		Title: "名册之外，八万零一号",
		Characters: []project.Character{
			// LLM 直出的两张（真机 plan.json 原文，描述与英文提示词都齐）——它们**不许被覆盖**。
			{ID: "char_qiqi", Name: "祁七", Kind: "android",
				Description: "年龄：外观二十岁（非人类机体）。脸型：鹅蛋脸，下巴微尖不过分。",
				ImagePrompt: "Twenty-year-old non-human maintenance unit (android, not human), slender", Voice: "A cool, even voice."},
			{ID: "char_qiqi_q", Name: "Q版祁七（U-80001）", Kind: "android",
				Description: "祁七的 Q 版机械人偶本体，巴掌大。", ImagePrompt: "Palm-sized chibi android figure"},
			// 真机现场的五张：描述空、提示词是锚表兜底串。
			{ID: "char_shuangjian", Name: "霜见", ImagePrompt: AnchorStubTag + "a Chinese period-film character, aged 17 to 19, seven heads tall"},
			{ID: "char_mohui", Name: "墨洄", ImagePrompt: AnchorStubTag + "a Chinese period-film character, aged 25 to 30, eight heads tall"},
			{ID: "char_shaoxian", Name: "邵舷", ImagePrompt: AnchorStubTag + "a Chinese period-film character, aged 40 to 48, seven and a half heads tall"},
			{ID: "char_yuchiche", Name: "尉迟彻", ImagePrompt: AnchorStubTag + "a Chinese period-film character, aged 30 to 36, seven and a half heads tall"},
			{ID: "char_duanwei", Name: "断桅", ImagePrompt: AnchorStubTag + "a Chinese period-film character, aged 58 to 66"},
		},
	}
}

func realAnchorSrc5() string {
	return anchorShuangjian + "\n" + anchorMohui + "\n" + anchorShaoxian + "\n" +
		anchorYuchiche + "\n" + anchorDuanwei
}

// TestFillAnchorDescriptionsRepairsTheGap 五张空描述的卡要从锚表补回原文，且**已有的一个字不动**。
func TestFillAnchorDescriptionsRepairsTheGap(t *testing.T) {
	plan := realPlanFixture()
	before := plan.Characters[0] // 祁七
	filled := FillAnchorDescriptions(plan, realAnchorSrc5())
	if strings.Join(filled, "、") != "霜见、墨洄、邵舷、尉迟彻、断桅" {
		t.Fatalf("应补这五张（按方案顺序），实际 %v", filled)
	}
	want := map[string]string{
		"霜见":  "颈侧一道义体接缝纹",
		"墨洄":  "长菱形脸",
		"邵舷":  "右手中指一枚磨秃的金属指套",
		"尉迟彻": "左手小指缺一节",
		"断桅":  "身形：瘦，背驼，走路",
	}
	for i := range plan.Characters {
		ch := plan.Characters[i]
		key, ok := want[ch.Name]
		if !ok {
			continue
		}
		if !strings.Contains(ch.Description, key) {
			t.Errorf("「%s」的描述没从锚表补回（应含 %q），实际 %q", ch.Name, key, ch.Description)
		}
		// 兜底串仍在：它是"等专职会话翻译"的标记（AnchorStubTag），不许在这一步被抹掉。
		if !strings.Contains(ch.ImagePrompt, AnchorStubTag) {
			t.Errorf("「%s」的提示词应仍是兜底串（等翻译），实际 %q", ch.Name, ch.ImagePrompt)
		}
	}
	if plan.Characters[0].Description != before.Description || plan.Characters[0].ImagePrompt != before.ImagePrompt {
		t.Error("已有描述/提示词的卡（祁七）被覆盖了 —— 那是作者口径，一个字都不许动")
	}
	// **关键一环**：补完之后，专职翻译会话才会认这些卡（此前它因为"没描述可翻"整批跳过）。
	if n := NeedsTranslationCount(plan); n != 5 {
		t.Errorf("补完之后应有 5 张卡在等翻译，实际 %d —— 翻译不启动，出图仍只有年龄", n)
	}
	if NeedsPromptTranslation(plan.Characters[0]) {
		t.Error("已有真提示词的卡不该进翻译队列")
	}
	// 幂等：再调一次什么都不做。
	if again := FillAnchorDescriptions(plan, realAnchorSrc5()); len(again) != 0 {
		t.Errorf("幂等失败：第二次还补了 %v", again)
	}
}

// TestIdentityPromptGapsNamesTheCards 出图前点名"谁的提示词只有年龄"——这是撞脸门看到的那件事的原因。
func TestIdentityPromptGapsNamesTheCards(t *testing.T) {
	plan := realPlanFixture()
	if gaps := IdentityPromptGaps(plan); strings.Join(gaps, "、") != "霜见、墨洄、邵舷、尉迟彻、断桅" {
		t.Fatalf("译前应点名这五张，实际 %v", gaps)
	}
	FillAnchorDescriptions(plan, realAnchorSrc5())
	if gaps := IdentityPromptGaps(plan); len(gaps) != 5 {
		t.Fatalf("补了描述但还没翻译时仍要点名（出图照样现编脸），实际 %v", gaps)
	}
	// 翻译会话写回真正的英文提示词之后，缺口消失。
	for i := range plan.Characters {
		if NeedsPromptTranslation(plan.Characters[i]) {
			plan.Characters[i].ImagePrompt = "long-limbed young woman with a narrow face, clear jawline, " +
				"grey-blue pupils, willow-leaf eyes, a black mechanical seam down the side of her neck"
		}
	}
	if gaps := IdentityPromptGaps(plan); len(gaps) != 0 {
		t.Errorf("译完就不该再点名，实际 %v", gaps)
	}
	if n := NeedsTranslationCount(plan); n != 0 {
		t.Errorf("译完不该再有待翻译，实际 %d", n)
	}
}

// TestFillAnchorDescriptionsDoesNotInvent 锚表里没有这个人时**不许编**（空着 + 上面那条点名播报负责说清）。
func TestFillAnchorDescriptionsDoesNotInvent(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{{ID: "char_x", Name: "路人甲"}}}
	if got := FillAnchorDescriptions(plan, realAnchorSrc5()); len(got) != 0 {
		t.Fatalf("锚表里没有「路人甲」，不该补，实际 %v", got)
	}
	if plan.Characters[0].Description != "" || plan.Characters[0].ImagePrompt != "" {
		t.Error("不许凭空生成描述或提示词 —— 那正是「随机生成」的来源")
	}
	if gaps := IdentityPromptGaps(plan); len(gaps) != 1 {
		t.Errorf("这种卡必须被点名（出图会现编，用户该知道），实际 %v", gaps)
	}
}

// 原文首现段那条兜底：锚表里**没有**这个人时，用小说原文的首次出场描写当材料。
//
// 夹具同样照真机原文（《名册之外，八万零一号》第013章霜见出场那段，连同它前后的剧情与对话）——
// 每个元素对应原文里的一个自然段，照抄。
var novelShuangjianPassage = strings.Join([]string{
	"有人从坡上跳下来。落地的时候他单膝跪了一下，一只手撑在铁皮上。",
	"",
	"他站起来。少年，清瘦，肩背不宽，外套的体积大于身体，袖口磨出了毛边。他没有束发，中长过耳直到下颌，额前蓬松微卷的刘海盖过眉毛，几缕碎发遮着眼。冷白的脸上，下颌线清楚。颈侧有一道黑色机械与皮肤交界的接缝纹，正面看得见。双耳各挂着一条细长的银坠，左耳那只歪了，像被人拽过一次。",
	"",
	"他右手拇指和食指捏着一块很小的读数板，板上一行冷青色的数在跳。",
	"",
	"剥线刀说：“霜见。”",
	"",
	"剥线刀说：“你算你的数，别管这个。”",
	"",
	"霜见没看他。他先看甲板，再看那三只箱子，最后看剥线刀脚底下。",
}, "\n\n")

func TestFillCastDescriptionsFromSourceUsesTheNovel(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{
		// 锚表里没有、卡上也没有：应当从原文首现段拿到材料。
		{ID: "char_shuangjian", Name: "霜见"},
		// 已有描述（作者口径）：一个字都不许动。
		{ID: "char_qiqi", Name: "祁七", Description: "瓷白装甲的维护单元", ImagePrompt: "porcelain android"},
		// 原文里没有外形材料：宁可不填，留给点名播报。
		{ID: "char_nobody", Name: "查无此人"},
	}}
	filled := FillCastDescriptionsFromSource(plan, novelShuangjianPassage)
	if strings.Join(filled, "、") != "霜见" {
		t.Fatalf("只该补「霜见」，实际 %v", filled)
	}
	if !strings.Contains(plan.Characters[0].Description, "接缝纹") {
		t.Errorf("霜见应从原文首现段拿到外形材料，实际 %q", plan.Characters[0].Description)
	}
	for _, leak := range []string{"剥线刀说", "读数板", "数不对"} {
		if strings.Contains(plan.Characters[0].Description, leak) {
			t.Errorf("原文里的剧情/对话不该进材料（%q）：%q", leak, plan.Characters[0].Description)
		}
	}
	// 兜底串装上 ⇒ 专职翻译会话会认这张卡（与锚表那条同一套链条）。
	if !strings.Contains(plan.Characters[0].ImagePrompt, AnchorStubTag) || !NeedsPromptTranslation(plan.Characters[0]) {
		t.Errorf("补完材料要留下「待翻译」标记，实际 %q", plan.Characters[0].ImagePrompt)
	}
	if plan.Characters[1].Description != "瓷白装甲的维护单元" || plan.Characters[1].ImagePrompt != "porcelain android" {
		t.Error("已有描述的卡被覆盖了 —— 那是作者口径")
	}
	if plan.Characters[2].Description != "" {
		t.Errorf("原文里没有材料的人不该被编出描述，实际 %q", plan.Characters[2].Description)
	}
	// 注意：霜见**也在**缺口名单里 —— 材料有了，但英文定妆提示词要等专职翻译会话写回来
	//（`backfillCastIdentity` 里紧接着就跑），在那之前出图仍然只能现编。这是如实的态度，不是漏。
	// 注意：霜见**也在**缺口名单里 —— 材料有了，但英文定妆提示词要等专职翻译会话写回来
	//（`backfillCastIdentity` 里紧接着就跑），在那之前出图仍然只能现编。这是如实的态度，不是漏。
	if gaps := IdentityPromptGaps(plan); strings.Join(gaps, "、") != "霜见、查无此人" {
		t.Errorf("译前这两张都算缺口，实际 %v", gaps)
	}
	// 幂等。
	if again := FillCastDescriptionsFromSource(plan, novelShuangjianPassage); len(again) != 0 {
		t.Errorf("幂等失败：第二次还补了 %v", again)
	}
}
