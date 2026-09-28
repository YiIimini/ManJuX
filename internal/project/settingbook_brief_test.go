package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 设定集必须能**渲染成可注入提示词的一段**，而且**接到会话上**（用户 2026-09-28 架构）。
//
// 背景：读取器 `LoadSettingBook` 早就有了（实测一部戏读出 14 角色 / 13 道具 / 67 数字锚 /
// 12K 风格卡），但**没有任何会话消费它** —— 于是"现在是哪一年"集集不同、
// 角色的年龄与疤痕每集漂、道具的功能边界被忽略，而锚表里明明逐项写着。
func TestSettingBriefRendersForSessions(t *testing.T) {
	dir := t.TempDir()
	// 照真实形态写：技能侧的四份文件（前缀匹配，名字带画风后缀）
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 照真实形态写：道具表与数字锚表在技能侧是 **Markdown 表格**（`| 项 | 值 |`），
	// 外形锚表是 `### 名字（定位：…）` + `- 键：值`。夹具写错格式 = 门给假绿灯。
	write("人物外形锚表.md", "# 人物外形锚表\n\n### 祁七（定位：主角，机器人／机甲形态）\n- 年龄：19\n- 身形：瘦长脸、凤眼\n- 记号：左眉一道旧疤\n")
	write("重点道具表.md", "# 重点道具表\n\n### 1 旧柴刀\n\n| 项 | 内容 |\n|---|---|\n| 名称 | 旧柴刀 |\n| 功能作用 | 劈柴与防身，砍不动硬木 |\n| 独有特征 | 刀背一道豁口 |\n")
	write("数字锚表.md", "# 数字锚表\n\n| 锚名 | 定值 | 备注 |\n|---|---|---|\n| 现在是哪一年 | 灰纪元第八万年 | 不是第七万年 |\n| 城距 | 三百里 | 易错：不是三百公里 |\n")
	write("视觉风格卡-3D写实动漫脸.md", "# 视觉风格卡\n3D 写实动漫脸，哑光电影质感。\n")

	b, err := LoadSettingBook(dir)
	if err != nil || b == nil {
		t.Fatalf("设定集读不出来：%v", err)
	}
	if !b.HasContent() {
		t.Fatal("HasContent 该为真")
	}
	got := b.SettingBrief(true)
	for _, want := range []string{"祁七", "灰纪元第八万年", "旧柴刀", "三百里", "哑光"} {
		if !strings.Contains(got, want) {
			t.Errorf("设定集摘要该含「%s」，实际：\n%s", want, got)
		}
	}
	// **不许编 id**：锚表只有名字，编出来的 id 会指向不存在的卡
	if strings.Contains(got, "char_") || strings.Contains(got, "prop_") {
		t.Errorf("锚表里没有卡片 id，不该凭空造一个：\n%s", got)
	}
	// 无设定集时返回空串（调用方照旧走"只喂正文"的老路）
	var empty *SettingBook
	if empty.SettingBrief(true) != "" {
		t.Error("nil 设定集该返回空串")
	}
	if b2, _ := LoadSettingBook(filepath.Join(dir, "不存在的目录")); b2 != nil {
		t.Error("目录不存在时该返回 nil（纪律：不是错误）")
	}
}
