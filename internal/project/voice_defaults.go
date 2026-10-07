package project

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// voice_defaults.go 角色卡**缺失声线**时的确定性兜底（跨镜、跨集稳定，同一份方案内不撞声）。
//
// 【2026-10-07 真机复核查出的撞声通路】`voice` 只有 ②人物道具会话的 schema 在要，
// 而**走锚表补出来的角色卡从来不写它** —— `pipeline.EnsureAnchorCast` 只写
// `Description` + `ImagePrompt`（见 cast_anchor.go 的 append）；渲染侧
// `pipeline.defaultVoiceFor` 对"认不出的说话人"一律返回同一个常数 `genericVoiceDefault`
// ⇒ 苔生、拾叁、回波、关棠、剥线刀… 这些卡在成片里是**同一个嗓子**，无卡说话人
// （家丁甲/家丁乙）也一样。观众听到的是"好几个人一个声音"，与用户报的"乱配音"同源。
//
// 判据是确定性的（不许让模型自觉，见 AGENTS 的"门与装配的分工"）：
//
//	· 同一个名字 → 永远同一条声线（跨镜、跨集：按基名散列，不随机、不用时间戳）；
//	· **同一份方案内每个角色一条不同的声线**（散列到池子里的位置被占就顺延 ——
//	  顺序取计划里卡片本身的顺序，所以同一套卡片每次跑的结果完全一样）；
//	· 已有的 voice 一个字都不覆盖（作者/技能侧给了就用它的，幂等），并且**被占的声线
//	  不会再被派生出来**（否则补出来的卡会和作者指定的人撞声）；
//	· Q 版卡与本体**同一个嗓子**（"Q 版＝同一个人的卡通化"，见三卡规范）。
//
// 只在**唯一读取边界**（HealPlan）调用，所以预检/渲染/人审看到的是同一份声线。
//
// 边界（照实说）：每池 12 条 ⇒ 同性别角色超过 12 人时必然复用（真机那本书 10 女 / 3 男 / 1 未定，
// 不会用尽）。要更多音色就扩池子，不要改成"按顺序轮着发" —— 那会让同一个角色换一集就换嗓子。
var (
	// female 池（12 条）：清亮少女 / 温和中音 / 细弱含气 / 低哑 / 高亮快嘴 / 沉稳有力 /
	// 怯懦细小 / 中年沙哑 / 冷静平直 / 轻软绵慢 / 尖利急促 / 低厚温润。
	platformVoicePaletteFemale = []string{
		"a young, light female voice, quick and clear, slightly cold",
		"a warm mid-range female voice, soft-spoken but steady",
		"a thin, breathy female voice, hesitant and quiet",
		"a low, husky female voice, slow and deliberate",
		"a bright, high female voice, chirpy and fast",
		"a mature, full-bodied female voice, calm and commanding",
		"a small, thin female voice, timid and uneven",
		"a raspy, middle-aged female voice, clipped and dry",
		"a cool, even female voice, precise and unhurried",
		"a soft, lilting female voice, gentle and slow",
		"a sharp, mid-range female voice, brusque and impatient",
		"a resonant, low female voice, warm and steady",
	}
	// male 池（12 条）：低沉 / 干硬公务嗓 / 青年快语 / 沙哑老嗓 / 轻声犹豫 / 洪亮逼人 /
	// 细鼻音急 / 温厚稳当 / 冷硬公文嗓 / 油滑低哑 / 圆滑劝诱 / 干瘦气喘。
	platformVoicePaletteMale = []string{
		"a deep, low male voice, slow and unhurried",
		"a dry mid-range male voice, clipped and businesslike",
		"a young male voice, bright and fast-talking",
		"a gravelly old male voice, rough and tired",
		"a soft, quiet male voice, breathy and hesitant",
		"a loud, booming male voice, forceful and impatient",
		"a thin, nasal male voice, nervous and quick",
		"a warm, steady male voice, friendly and measured",
		"a cold, flat male voice, bureaucratic and precise",
		"a raspy, mid-pitched male voice, sardonic and slow",
		"a smooth, low male voice, persuasive and unhurried",
		"a reedy, middle-aged male voice, wheezy and quick",
	}
	// neutral 池（12 条）：性别不明（锚表写「未定」）、非人形角色（机器人/无实体/器物类"角色"）。
	platformVoicePaletteNeutral = []string{
		"a neutral, androgynous voice, even-paced and calm",
		"a soft, breathy androgynous voice, quiet and thoughtful",
		"a crisp, youthful androgynous voice, energetic",
		"a rough, mid-pitched androgynous voice, fast and impatient",
		"a flat, mechanical voice with perfectly even pacing",
		"a hollow, echoing androgynous voice, slow and distant",
		"a tense, high-strung androgynous voice, staccato",
		"a low, monotone androgynous voice, subdued and heavy",
		"a humming, filtered voice, level and unhurried",
		"a dry, clicking androgynous voice, staccato and cold",
		"a deep, resonant androgynous voice, broad and slow",
		"a thin, whispered androgynous voice, airy and quiet",
	}
)

// DerivedVoice 由（基名 + 性别提示）确定性派生一条默认声线（不关心"池子里这条被谁占了"）。
//
// genderHint 取 `genderOfDescription` 的结果（""=不明 ⇒ 中性池）。空名字返回空串
// （调用方据此跳过，不许给"无名"编一条）。
func DerivedVoice(name, genderHint string) string {
	pool := voicePoolFor(genderHint)
	if len(pool) == 0 {
		return ""
	}
	base := voiceLookupKey(name)
	if base == "" {
		return ""
	}
	return pool[fnvIndex(base, len(pool))]
}

// PickVoice 在池子里挑一条**还没被占用**的声线：先试散列位置，被占就顺着池子往后找。
//
// used 传的是"这份方案里已经用掉的声线"（作者指定的 + 先补的）。池子用尽时返回散列位置那条
// （复用），调用方不必特殊处理 —— 但**不许**因为池子用尽就不补（没有声线比撞声更糟）。
func PickVoice(name, genderHint string, used map[string]bool) string {
	pool := voicePoolFor(genderHint)
	if len(pool) == 0 {
		return ""
	}
	base := voiceLookupKey(name)
	if base == "" {
		return ""
	}
	start := fnvIndex(base, len(pool))
	for i := 0; i < len(pool); i++ {
		if v := pool[(start+i)%len(pool)]; used == nil || !used[v] {
			return v
		}
	}
	return pool[start]
}

// voicePoolFor 按性别提示选池子（""/未定 ⇒ 中性池）。
func voicePoolFor(genderHint string) []string {
	switch strings.ToLower(strings.TrimSpace(genderHint)) {
	case "female", "女":
		return platformVoicePaletteFemale
	case "male", "男":
		return platformVoicePaletteMale
	}
	return platformVoicePaletteNeutral
}

// fnvIndex 基名 → 池内下标（确定性：同名永远同一个下标）。
func fnvIndex(base string, n int) int {
	if n <= 0 {
		return 0
	}
	h := fnv.New64a()
	if _, err := h.Write([]byte(base)); err != nil {
		return 0
	}
	return int(h.Sum64() % uint64(n))
}

// genderOfDescription 从角色卡的描述里读性别。
//
// 锚表原文那一套是结构化的（`- 性别：女`），②会话的 description 也照它的写法；
// 读不到就是不明 —— **不许按名字猜性别**（"霜见""墨洄"这种名字猜不对，猜错比不猜更糟：
// 一个女角色被配成低沉男声比"中性"难听得多）。锚表里的「未定（正文刻意不给代词）」也算不明。
func genderOfDescription(desc string) string {
	d := strings.TrimSpace(desc)
	if d == "" {
		return ""
	}
	for _, kv := range []string{"性别：女", "性别:女", "性别 女", "性别＝女", "性别=女"} {
		if strings.Contains(d, kv) {
			return "female"
		}
	}
	for _, kv := range []string{"性别：男", "性别:男", "性别 男", "性别＝男", "性别=男"} {
		if strings.Contains(d, kv) {
			return "male"
		}
	}
	return ""
}

// voiceLookupKey 声线的**归一基名**：Q 版前缀丢掉、括注丢掉、造型档后缀丢掉。
//
// Q 版卡（`Q版祁七`）必须与本体（`祁七`）落在同一条声线上 —— 内心戏里说话的是本人，
// 嗓子换了观众就听不出是同一个人的心里话（与"Q 版＝同一个人的卡通化"同一条口径）。
func voiceLookupKey(name string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		return ""
	}
	for _, pre := range []string{"Q版", "Q 版", "q版", "q 版", "Q-版", "SD版"} {
		n = strings.TrimPrefix(n, pre)
	}
	n = strings.TrimSpace(n)
	if i := strings.IndexAny(n, "（("); i > 0 {
		n = strings.TrimSpace(n[:i])
	}
	if b := CharFamilyBaseName(n); b != "" {
		n = b
	}
	return strings.ToLower(strings.TrimSpace(n))
}

// FillMissingVoices 给**没有声线**的角色卡补一条确定性默认声线（幂等）。
//
// 只填空的：作者/技能侧写了 voice 的卡一个字都不动。返回补上的张数与名字
// （调用方如实播报 —— "自愈改了什么"必须看得见）。
//
// 顺序与占用：
//
//	· 先扫一遍"已被用掉的声线"（作者指定的也算），保证补出来的卡不与任何已有的人撞声；
//	· 性别提示的**兄弟兜底**：Q 版卡自己的 description 常常没有「性别：」那一行，
//	  那就借用同名本体的 description —— 否则 Q 版会掉进中性池，与本体变成两把嗓子。
func FillMissingVoices(plan *Plan) (int, []string) {
	if plan == nil || len(plan.Characters) == 0 {
		return 0, nil
	}
	used := map[string]bool{}
	byKey := map[string]string{} // 基名 → 已知性别提示（取第一个非空的）
	for i := range plan.Characters {
		c := plan.Characters[i]
		if v := strings.TrimSpace(c.Voice); v != "" {
			used[v] = true
		}
		k := voiceLookupKey(c.Name)
		if k == "" || byKey[k] != "" {
			continue
		}
		if g := genderOfDescription(c.Description); g != "" {
			byKey[k] = g
		}
	}
	n := 0
	var names []string
	for i := range plan.Characters {
		c := &plan.Characters[i]
		if strings.TrimSpace(c.Voice) != "" {
			continue // 已有的一个字都不覆盖
		}
		k := voiceLookupKey(c.Name)
		if k == "" {
			continue
		}
		g := genderOfDescription(c.Description)
		if g == "" {
			g = byKey[k]
		}
		v := PickVoice(c.Name, g, used)
		if v == "" {
			continue
		}
		c.Voice = v
		used[v] = true
		n++
		names = append(names, c.Name)
	}
	return n, names
}

// VoiceGaps 还没有声线的角色卡名字（渲染前的"点名"，与 IdentityPromptGaps 同一口径）。
//
// 正常情况下 HealPlan 已经补过，这里为空；非空说明这份方案**绕过了读取边界**
// （例如有人直接改了 plan.json 又让某个只管读文件的入口用了它）。
func VoiceGaps(plan *Plan) []string {
	if plan == nil {
		return nil
	}
	var out []string
	for _, c := range plan.Characters {
		if strings.TrimSpace(c.Voice) == "" && voiceLookupKey(c.Name) != "" {
			out = append(out, c.Name)
		}
	}
	return out
}

// VoiceSummary 给日志用的一句话（谁被补了声线）。
func VoiceSummary(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("%d 张角色卡缺声线，已按「基名+性别」确定性补上（同名同声、同一份方案内不撞声）：%s",
		len(names), strings.Join(names, "、"))
}
