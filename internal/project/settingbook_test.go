package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 设定集解析的契约门。夹具**照真实文件的形态写**（取自 data/小说 下实际产出的设定集），
// 因为这里最容易犯的错就是"按自己想象的结构写解析器"。
//
// 真实形态（2026-09-28 读盘）：
//   人物外形锚表：`### 祁七（定位：主角。机器人／机甲形态……）` + 若干 `- 字段：值`
//   重点道具表：  `### 1 记名链` + `| 项 | 内容 |` 表格（含 `锚定章_机器` 那一行）
//   数字锚表：    `## 一、锚清单` + `| 锚名 | 定值 | 依据 | 已出现过此数的章 | 备注 |`

const realCharAnchor = `# 人物外形锚表 ·《名册之外，八万零一号》

> **口径**：本表是**外形唯一权威**。平台直出人物卡时**逐项照抄**，不许自行改写、不许加形容词。

---

### 祁七（定位：主角。机器人／机甲形态——旧舰队义体维护单元，机型钢印 U-80001）

- 年龄：外观 18–20 岁（非人类机体，实际运行年限不详）
- 身形：纤细，站姿笔直，肩线平；八头身（人偶比例），四肢偏长
- 脸型：鹅蛋脸，下巴微尖不过分；轮廓分明不生硬、无充气感
- 独特记号：头顶金色黄铜机械光环状发饰（最强记忆点）
- 比例锚_机器：8 头身

### 霜见（定位：第二主角／通信手。拾荒船团的读数员）

- 年龄：少年，17–19 岁
- 身形：清瘦，肩背不宽；七点五头身
- 眉眼：灰蓝色瞳孔；柳叶眼（介于杏眼与丹凤之间），双眼皮浅，眼尾略垂；眼神克制
- 比例锚_机器：7.5 头身
`

const realPropTable = `# 重点道具表 ·《名册之外，八万零一号》

### 1 记名链

| 项 | 内容 |
|---|---|
| 名称 | 记名链 |
| 外形 | 一串三十毫米宽、两毫米厚的拉丝钛银金属铭牌 |
| 功能 | **能做什么**：熔接口借它记录「已熔入过谁」 |
| 标志性细节 | 走路会响；链子末端永远空着扣环 |
| 锚定章 | 第 1 章／第 4 章／第 8 章 |
| 锚定章_机器 | 1,4,8,16,32,48,56,64,72,88 |

### 2 熔接口

| 项 | 内容 |
|---|---|
| 名称 | 熔接口 |
| 外形 | 背脊中段外壳缺失一整块 |
| 锚定章_机器 | 1,4,12,24,44,68,88 |
`

const realNumberAnchor = `# 数字锚表 ·《名册之外，八万零一号》

## 一、锚清单

| 锚名 | 定值（唯一） | 依据 | 已出现过此数的章 | 备注（易错点） |
|---|---|---|---|---|
| 现在是哪一年 | **灰纪元第八万年** | 设定 §三 | 全书 | 不许写成「八万零一年」 |
| 归墟环建成 | **灰纪元六万一千二百年** | 设定 §三 | 20／57 | 不许写「六万一千年」 |
`

// 锚表：名字、括号里的定位说明、字段顺序与取值都要对。
func TestParseCharAnchorReadsTheRealFormat(t *testing.T) {
	chars := ParseCharAnchor(realCharAnchor)
	if len(chars) != 2 {
		t.Fatalf("应解析出 2 个角色（祁七/霜见），实际 %d 个：%+v", len(chars), chars)
	}
	if chars[0].Name != "祁七" || chars[1].Name != "霜见" {
		t.Fatalf("角色名解析错：%q / %q", chars[0].Name, chars[1].Name)
	}
	// 括号里的定位说明要单独拆出来（选角色 kind 时要用）
	if !strings.Contains(chars[0].Note, "定位：主角") {
		t.Errorf("定位说明没拆出来：%q", chars[0].Note)
	}
	// 字段必须**保序**（拼提示词时要照作者排的顺序写）
	if len(chars[0].Fields) < 4 {
		t.Fatalf("祁七该有 4+ 个字段，实际 %v", chars[0].Fields)
	}
	if chars[0].Fields[0].Key != "年龄" {
		t.Errorf("字段顺序应保持原文，首个应为「年龄」，实际 %q", chars[0].Fields[0].Key)
	}
	if got := chars[0].Field("比例锚_机器"); got != "8 头身" {
		t.Errorf("比例锚应为「8 头身」，实际 %q", got)
	}
	if got := chars[1].Field("比例锚_机器"); got != "7.5 头身" {
		t.Errorf("霜见的比例锚应为「7.5 头身」，实际 %q", got)
	}
	if got := chars[1].Field("不存在的字段"); got != "" {
		t.Errorf("不存在的字段应返回空串，实际 %q", got)
	}
	// Raw 要能"逐项照抄"
	if !strings.Contains(chars[0].Raw, "黄铜机械光环状发饰") {
		t.Errorf("Raw 应保留原文（逐项照抄用），实际 %q", chars[0].Raw)
	}
	// 只认 `###` 小节：文件开头的 `#` 一级标题不该被当成角色
	for _, c := range chars {
		if strings.Contains(c.Name, "人物外形锚表") {
			t.Errorf("一级标题被误判成角色：%q", c.Name)
		}
	}
}

// 道具表：表格要解析成字段，`锚定章_机器` 要能直接算成章号列表。
func TestParsePropTableAndMachineAnchors(t *testing.T) {
	props := ParsePropTable(realPropTable)
	if len(props) != 2 {
		t.Fatalf("应解析出 2 件道具，实际 %d：%+v", len(props), props)
	}
	if props[0].Name != "记名链" || props[0].Index != 1 {
		t.Fatalf("道具名/序号解析错：%q #%d", props[0].Name, props[0].Index)
	}
	if got := props[0].Field("外形"); !strings.Contains(got, "拉丝钛银") {
		t.Errorf("外形字段没读到：%q", got)
	}
	// 表头 `| 项 | 内容 |` 与分隔行不许混进字段
	for _, f := range props[0].Fields {
		if f.Key == "项" || strings.HasPrefix(f.Key, "---") {
			t.Errorf("表头/分隔行混进了字段：%+v", f)
		}
	}
	want := []int{1, 4, 8, 16, 32, 48, 56, 64, 72, 88}
	got := props[0].AnchorChapters()
	if len(got) != len(want) {
		t.Fatalf("锚定章_机器 应解析出 %d 个章号，实际 %v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("锚定章解析错：want %v got %v", want, got)
		}
	}
	// 没有这一行的道具返回 nil（不是 panic、也不是空切片里的 0）
	if a := props[1].AnchorChapters(); len(a) != 7 {
		t.Errorf("熔接口应有 7 个锚定章，实际 %v", a)
	}
}

// 数字锚表：锚名/定值/备注三列要分开（备注是"易错点"，运行时要用）。
func TestParseNumberAnchorReadsTheRealFormat(t *testing.T) {
	anchors := ParseNumberAnchor(realNumberAnchor)
	if len(anchors) != 2 {
		t.Fatalf("应有 2 条锚，实际 %d：%+v", len(anchors), anchors)
	}
	if anchors[0].Name != "现在是哪一年" {
		t.Errorf("锚名解析错：%q", anchors[0].Name)
	}
	if !strings.Contains(anchors[0].Value, "灰纪元第八万年") {
		t.Errorf("定值应保留原文（含加粗标记也能接受，但要有数字本身）：%q", anchors[0].Value)
	}
	if !strings.Contains(anchors[0].Note, "八万零一年") {
		t.Errorf("备注（易错点）应并进来：%q", anchors[0].Note)
	}
}

// 整本读取：目录不存在 / 文件缺失都**不是错误**（老包、纯原创、技能侧还没产出的中间态）。
func TestLoadSettingBookToleratesMissing(t *testing.T) {
	if b, err := LoadSettingBook(filepath.Join(t.TempDir(), "不存在")); err != nil || b != nil {
		t.Errorf("目录不存在应返回 (nil, nil)，实际 (%v, %v)", b, err)
	}
	empty := t.TempDir()
	if b, err := LoadSettingBook(empty); err != nil || b != nil {
		t.Errorf("空目录应返回 (nil, nil)，实际 (%v, %v)", b, err)
	}

	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("人物外形锚表.md", realCharAnchor)
	write("重点道具表.md", realPropTable)
	write("数字锚表.md", realNumberAnchor)
	// 风格卡的文件名带画风，解析要按**前缀**认（换个画风就换名字）
	write("视觉风格卡-3D写实动漫脸.md", "# 视觉风格卡\n\n三维渲染、国产动画电影质感。\n")

	b, err := LoadSettingBook(dir)
	if err != nil || b == nil {
		t.Fatalf("应读到设定集，实际 (%v, %v)", b, err)
	}
	if len(b.Characters) != 2 || len(b.Props) != 2 || len(b.Numbers) != 2 {
		t.Errorf("四类数据的条数不对：角色 %d / 道具 %d / 数字 %d",
			len(b.Characters), len(b.Props), len(b.Numbers))
	}
	if !strings.Contains(b.StyleCard, "三维渲染") || !strings.Contains(b.StyleName, "视觉风格卡") {
		t.Errorf("风格卡要按前缀读到并记下文件名：%q / %q", b.StyleName, b.StyleCard)
	}
}
