package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
)

// anchor_translate.go 专职的「**人物定妆提示词翻译**」会话（用户架构里的第②步）。
//
// ## 为什么必须有它（2026-09-28 用户报「角色三视图渲染异常」）
//
// 锚表补齐出来的角色卡只有**中文** `description`，而出图装配只吃**英文**提示词。
// 上一轮我只补了"年龄 + 头身比"这两条能确定性翻译的字段，其余（脸型/眉眼/发型/服装/记号）
// 全留在中文里 —— 结果模型只知道"这个人 30–36 岁、8 头身"，剩下的靠想象：
// 实测 **尉迟彻**（设定：深灰值勤装甲、方中带圆脸、剑眉、细长单眼皮、肩背厚实、
// 左手小指缺一节、腕上编号环）被画成了一个**穿现代浅灰衬衫、挂工牌的年轻职员**。
//
// 这些字段是**自由文本**，没法靠词表确定性翻译（"深灰值勤装甲（肩甲有划痕）＋高领内衬"、
// "走路时右肩低半寸"），所以只能交给模型 —— 但必须是**专职会话**：
// 先告诉它这是什么活、按什么口径干，再给它材料（用户定的做法：每一步新建会话并先给到 LLM）。
//
// ## 口径来源（不是我自己编的）
//
// 用户在 `D:\Ai\Datas\漫剧制作\二、人物道具提取指令.txt` 里写死了这件事的规矩：
// 五官必须**具体到脸型/眼型/眉形/鼻型/唇形**、不许笼统；不许尺寸词（large/big eyes）、
// 不许抽象词（绝美/出尘）；服装要写材质与剪裁；**所有提示词必须中文**——但那一条是给
// "对话式产出清单"用的，平台这里是**进图像模型的正向提示词**，按平台既定口径必须全英文
// （技能侧《平台侧生成规范》§2：正向提示词全英文零汉字，中文只进字幕轨）。
// 这一处差异在 docs/口径对账-平台vs用户参考库.md 里已登记为**有意反着做**。

// NeedsPromptTranslation 这张卡是否需要专职会话补英文定妆提示词。
//
// 【2026-09-29 真机实测】判据不能只认空串：锚表补出来的卡带的是
// `[ANCHOR-STUB] a Chinese period-film character, aged 18 to 20` 这种**只有年龄**的兜底串，
// 而它一填进去，"ImagePrompt 非空"这条判据就让翻译永远不会发生 —— 图像模型拿到的是
// "一个古装人、18–20 岁"，于是**六张定妆照长得几乎一样**，白瓷装甲的机器人被画成普通古装人。
func NeedsPromptTranslation(ch project.Character) bool {
	if strings.TrimSpace(ch.Description) == "" {
		return false // 没描述可翻
	}
	p := strings.TrimSpace(ch.ImagePrompt)
	return p == "" || strings.Contains(p, AnchorStubTag)
}

// TranslateCardPrompts 给"没有英文画图提示词"的角色卡生成英文定妆提示词（一次调用）。
//
// 返回改了几张卡。client 为 nil 或没有待补的卡时直接返回（不打断流水线）。
func TranslateCardPrompts(ctx context.Context, client *llm.Client, plan *project.Plan) (int, error) {
	if plan == nil || client == nil {
		return 0, nil
	}
	var todo []project.Character
	for _, ch := range plan.Characters {
		if NeedsPromptTranslation(ch) {
			todo = append(todo, ch)
		}
	}
	if len(todo) == 0 {
		return 0, nil
	}

	sys := "你是 3D/半写实角色概念设计师，专精为 AI 绘画平台写电影级、高质感的**角色定妆提示词**。\n" +
		"你会收到若干角色的**中文设定**（来自作者的《人物外形锚表》，那是外形唯一权威）。\n" +
		"任务：为每个角色写一段 **English** 的定妆照画图提示词。\n" +
		"\n" +
		"硬要求：\n" +
		"1. **逐项照抄**设定里的：脸型 / 眉眼（眉形＋眼型＋眼神）/ 鼻唇 / 肤色肤质 / 独特记号 /\n" +
		"   发型三要素（束法＋长度＋额前状态）/ 首套服装（颜色、材质、版型、配饰）—— 一个都不许漏。\n" +
		"   不许美化、不许加形容词、**不许把两个人的特征合并**。\n" +
		"2. **只写英文，一个汉字都不许有**（图像模型会把汉字画成乱码糊在脸上）。\n" +
		"3. 五官只写**形状与神态**：脸型、眼型、眉形要写出具体名称（鹅蛋脸/丹凤眼/剑眉…的英文）。\n" +
		"   禁止尺寸词（large eyes / big eyes / big nose）、禁止抽象词（beautiful / stunning / perfect）。\n" +
		"4. 用词要能画：写「它长什么样」，不写比喻。\n" +
		"5. 结尾固定补一句：plain white seamless background, a cinematic photoreal 3D render, full-body standing view.\n" +
		"\n" +
		"输出：严格的 JSON 对象，键是角色名、值是那段英文提示词。不要任何解释、不要 markdown 围栏。"

	var sb strings.Builder
	for _, ch := range todo {
		sb.WriteString("### ")
		sb.WriteString(ch.Name)
		sb.WriteString("\n")
		sb.WriteString(strings.TrimSpace(ch.Description))
		sb.WriteString("\n\n")
	}
	out, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: sb.String()},
	}, false)
	if err != nil {
		return 0, err
	}
	m := parsePromptMap(out)
	if len(m) == 0 {
		return 0, fmt.Errorf("翻译结果里没有可用的 JSON")
	}
	n, missing := applyTranslatedPrompts(plan, m)
	if len(missing) > 0 {
		// 部分失败也要说出来：这些卡出图时只有"年龄＋头身比"，画出来就是通用脸
		// （2026-09-29 真机：钟离与石头两张卡都是兜底串，出图后用户问「咋那么相似」）。
		return n, fmt.Errorf("%d 张卡的翻译结果缺失：%s", len(missing), strings.Join(missing, "、"))
	}
	return n, nil
}

// applyTranslatedPrompts 把 {名: 英文提示词} 写回卡里，返回（写回几张, 没拿到结果的卡名）。
//
// **判据必须与收集待翻译卡时同一套**（`NeedsPromptTranslation`：空串**或**兜底串）。
// 2026-09-29 真机事故：收集那一侧已经改成"兜底串也算待翻译"，而这里还写着
// `if ImagePrompt != "" { continue }` —— 于是模型翻了、结果也回来了，**却因为字段非空被跳过**：
// 锚表补出来的卡（钟离、石头…）永远停在 `[ANCHOR-STUB] a Chinese period-film character, aged X`
// ⇒ 出图只有年龄差异 ⇒ 用户问「石头 和 钟离 咋那么相似？」。
func applyTranslatedPrompts(plan *project.Plan, m map[string]string) (n int, missing []string) {
	if plan == nil {
		return 0, nil
	}
	for i := range plan.Characters {
		ch := &plan.Characters[i]
		if !NeedsPromptTranslation(*ch) {
			continue // 已有**真**提示词的卡不许被覆盖（那是作者口径）
		}
		en, ok := m[strings.TrimSpace(ch.Name)]
		if !ok {
			missing = append(missing, ch.Name)
			continue
		}
		en = strings.TrimSpace(en)
		if en == "" || project.CountCJK(en) > 0 {
			missing = append(missing, ch.Name)
			continue // 带汉字的不要（会被画成乱码），宁缺勿滥
		}
		ch.ImagePrompt = en
		n++
	}
	return n, missing
}

// parsePromptMap 从模型输出里取 {名: 提示词}，容忍 markdown 围栏与前后废话。
func parsePromptMap(raw string) map[string]string {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "{"); i >= 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "}"); j >= 0 {
		s = s[:j+1]
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}
