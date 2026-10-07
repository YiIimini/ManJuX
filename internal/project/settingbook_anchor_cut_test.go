package project

import (
	"strings"
	"testing"
)

// 夹具 = 真机 source.txt 的尾部**原文照抄**（《名册之外，八万零一号》第01集）。
//
// 锚表最后一节「断桅」只写到 `- 身形：瘦，背驼，走路` 就直接接上了平台自己的
// 【本集剧情】…，随后是整集剧本 —— 不设正文边界时，解析出来的 Raw 一路吞到文末
// （实测 4846 字，里面大半是剧本正文），而这一段会被当成「这个人的外形描述」用。
const realAnchorHead = `霜见（定位：第二主角／通信手。拾荒船团的读数员，全书唯一的「只信数字的人」）
- 性别：女

- 年龄：少年，17–19 岁
- 脸型：瘦长脸，下颌线清晰，轮廓分明不生硬、无充气感
- 眉眼：灰蓝色瞳孔；柳叶眼（介于杏眼与丹凤之间，古典内敛），双眼皮浅，眼尾略垂；眼神克制
- 独特记号：颈侧一道义体接缝纹（黑色机械与皮肤的交界线，正面可见）
- 发型三要素：无束／中长过耳至下颌／额前蓬松微卷刘海盖过眉毛、碎发遮眼
- 比例锚_机器：7 头身
`

const realAnchorTail = `断桅（定位：工具人 · 拾荒船团的老拾荒人。把她从回收舱里捡回来的那个）
- 性别：女

- 年龄：老年，58–66 岁
- 身形：瘦，背驼，走路

【本集剧情】【开场爆点】扬声器冷声宣告「异动来源，硬件钢印 U-80001」——下一句却是「该编号不存在，予以静默」：主角刚睁眼，就被告知自己**不存在**。

【场 1|维护舱中央|灰纪元第八万年 第三千四百六十二标准日 后半夜|内】
  目的:三秒立住绝境——她活着,却被系统判为「不存在」。
  冲突:祁七 vs 舱载系统。争的是「我算不算数」。
  正文:
  扬声器:“重新检测到生命反应。”
  半秒。“异动来源,硬件钢印 U-80001。”
`

// TestParseCharAnchorStopsAtScriptBody 锚表小节必须在**剧本正文的起点标记**处收尾。
//
// 【为什么这是一道真门】被吞进去的正文有三千多字：它会进 `description`（"逐项照抄"的权威文本）、
// 进专职翻译会话的输入、进卡片详情的展示 —— 而"人物外形"这一段从此就废了。
// 与 internal/server 的 briefEndMarkers 同一口径：只有平台自己的节标记能可靠划边界
// （作者在每一项之间就爱空一行，空行/缩进都判不得）。
func TestParseCharAnchorStopsAtScriptBody(t *testing.T) {
	chars := ParseCharAnchor(realAnchorHead + "\n" + realAnchorTail)
	if len(chars) != 2 {
		t.Fatalf("应解析出 2 个角色，实际 %d", len(chars))
	}
	byName := map[string]SettingChar{}
	for _, c := range chars {
		byName[c.Name] = c
	}
	// ① 正常结束的那一节：没有被正文截断，原文照旧。
	shuang := byName["霜见"]
	if shuang.Cut {
		t.Error("霜见那一节后面接的是下一个角色标题，不该被判成被正文截断")
	}
	if !strings.Contains(shuang.Raw, "瘦长脸") || !strings.Contains(shuang.Raw, "义体接缝纹") {
		t.Errorf("正常节的 Raw 应逐字保留外形项，实际 %q", shuang.Raw)
	}
	// ② 被正文截断的那一节：正文一个字都不许进来。
	duan := byName["断桅"]
	if !duan.Cut {
		t.Error("断桅那一节是被【本集剧情】截断的 —— 必须标出来（平台要如实点名告诉用户这一节不全）")
	}
	for _, leak := range []string{"【本集剧情】", "【场 1|", "扬声器", "开场爆点", "目的:"} {
		if strings.Contains(duan.Raw, leak) {
			t.Errorf("剧本正文泄漏进了外形描述（含 %q）：%q", leak, duan.Raw)
		}
	}
	if !strings.Contains(duan.Raw, "身形：瘦，背驼，走路") {
		t.Errorf("断点之前的原文必须保留，实际 %q", duan.Raw)
	}
	if len([]rune(duan.Raw)) > 200 {
		t.Errorf("Raw 又被正文撑大了（%d 字）—— 正文边界失效", len([]rune(duan.Raw)))
	}
	// ③ 字段也只从断点前的正文里取（正文里的 `目的:` 之类不许变成外形字段）。
	if got := duan.Field("年龄"); got != "老年，58–66 岁" {
		t.Errorf("年龄字段应取到「老年，58–66 岁」，实际 %q", got)
	}
	for _, f := range duan.Fields {
		if strings.Contains(f.Value, "祁七") || strings.Contains(f.Key, "目的") {
			t.Errorf("正文被当成了字段：%q = %q", f.Key, f.Value)
		}
	}
}

// TestParseCharAnchorCutMarkers 每个正文标记都要能划边界（少一个就等于漏一类源文本写法）。
func TestParseCharAnchorCutMarkers(t *testing.T) {
	for _, marker := range scriptBodyMarkers {
		src := "甲（定位：路人）\n- 性别：男\n- 年龄：三十岁\n\n" + marker + "这里有正文。\n"
		chars := ParseCharAnchor(src)
		if len(chars) != 1 {
			t.Fatalf("标记 %q：应解析出 1 个角色，实际 %d", marker, len(chars))
		}
		if !chars[0].Cut || strings.Contains(chars[0].Raw, "这里有正文") {
			t.Errorf("标记 %q 没能划出边界：Cut=%v Raw=%q", marker, chars[0].Cut, chars[0].Raw)
		}
	}
	// 没有标记时**不许**乱截（设定集里的锚表文件就没有这些标记）。
	chars := ParseCharAnchor("甲（定位：路人）\n- 性别：男\n- 年龄：三十岁\n- 气质：冷\n")
	if len(chars) != 1 || chars[0].Cut || !strings.Contains(chars[0].Raw, "气质") {
		t.Errorf("没有正文标记时不该截断：%+v", chars)
	}
}
