package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCharacterSheetRealTableSurvivesWhole 真机锚表**一条不落**进每一集的输入。
//
// 【2026-10-07 真机实测】夹具是《名册之外，八万零一号》的真实
// `设定集/人物外形锚表.md`(15468 B / 6202 字 / **14 条角色**)。
// 注入上限原来是 2500 字,后果是:砍点落在「断桅」条目中间
// (`- 身形：瘦，背驼，走路` 之后直接接【本集剧情】),其后的
// 苔生/拾叁/回波/关棠/剥线刀/毕申/白手套/议长 8 条**从未进过任何一集的输入** ——
// 这些人的定妆照只能靠模型现编脸,与用户问的「非主角随机脸」同源。
//
// 判据是**结构性的**:14 条角色的 `- Q版形象：` 字段一条不缺(半个人的描述比没描述更坏:
// 模型会照半条现编另外半张脸),且文末附录还在(没有整块被吃掉)。
func TestCharacterSheetRealTableSurvivesWhole(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "char_anchor_real.md"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sd := filepath.Join(dir, "设定集")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "人物外形锚表.md"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	body, note := readCharacterSheet(dir)
	if note != "" {
		t.Errorf("真机锚表(%d 字)不该被限长,却报了:%s", len([]rune(body)), note)
	}
	names := []string{"祁七", "霜见", "墨洄", "邵舷", "尉迟彻", "断桅",
		"苔生", "拾叁", "回波", "关棠", "剥线刀", "毕申", "白手套", "议长"}
	for _, n := range names {
		if !strings.Contains(body, n+"（定位：") && !strings.Contains(body, n+"（定位:") {
			t.Errorf("锚表里的角色「%s」没进输入 —— 这个人的外形对模型等于不存在", n)
		}
	}
	if n := strings.Count(body, "- Q版形象："); n != len(names) {
		t.Errorf("%d 条角色的字段应一条不缺,实得 %d 个「- Q版形象：」—— 有条目被腰斩", len(names), n)
	}
	if !strings.Contains(body, "口径见 references/资产提示词-三卡规范.md") {
		t.Error("锚表文末(三卡规范口径)被截掉了:整块内容没进输入")
	}
}

// TestCharacterSheetTrimKeepsWholeEntries 超上限时**整条丢人**,不腰斩某个人;并如实点名。
//
// 夹具 = 真机锚表 ×3(18606 字 > 12000 上限)。判据:留下的小节数与「- Q版形象：」字段数**相等** ——
// 每个留下的角色都是完整的;说明里必须点名被丢掉的角色。
func TestCharacterSheetTrimKeepsWholeEntries(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "char_anchor_real.md"))
	if err != nil {
		t.Fatal(err)
	}
	one := strings.TrimSpace(string(NormalizeNovelText(raw)))
	big := one + "\n\n" + one + "\n\n" + one
	got, note := trimCharacterSheet(big)
	if n := len([]rune(got)); n > characterSheetMaxRunes {
		t.Errorf("限长后应 ≤ %d 字,实得 %d", characterSheetMaxRunes, n)
	}
	heads := len(sheetEntryHead.FindAllString(got, -1))
	if fields := strings.Count(got, "- Q版形象："); fields != heads {
		t.Errorf("留下的 %d 个小节里有 %d 个完整字段 —— 有角色被腰斩(半个人的描述会诱导模型现编另一半脸)",
			heads, fields)
	}
	if heads == 0 {
		t.Fatal("限长后不该一条都不剩")
	}
	for _, want := range []string{"超上限", "被整条略过的角色"} {
		if !strings.Contains(note, want) {
			t.Errorf("截断说明里缺「%s」,实得:%s", want, note)
		}
	}
	if !strings.Contains(note, "祁七") {
		t.Errorf("截断说明应点名被丢掉的角色,实得:%s", note)
	}
}

// TestCharacterSheetTrimFallsBackToParagraph 没有角色小节时退到最后一个空行,
// **不许从一行的中间切**(半行字段比没有更误导),说明里也不许编出"被略过的角色"。
func TestCharacterSheetTrimFallsBackToParagraph(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString(strings.Repeat("甲", 40))
		b.WriteString("完\n\n")
	}
	got, note := trimCharacterSheet(b.String())
	if n := len([]rune(got)); n > characterSheetMaxRunes {
		t.Errorf("限长后应 ≤ %d 字,实得 %d", characterSheetMaxRunes, n)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "完") {
		t.Error("砍在了一行中间:应该退到最后一个空行边界")
	}
	if !strings.Contains(note, "超上限") {
		t.Errorf("截断了就必须如实说明,实得 %q", note)
	}
	if strings.Contains(note, "被整条略过的角色") {
		t.Errorf("这张表里没有角色小节,不该编出角色名,实得 %q", note)
	}
}
