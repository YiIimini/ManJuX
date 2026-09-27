package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadCharacterSheet 人物外形锚表:读到就带硬约束抬头注入,读不到就老实返回空串。
//
// 这张表是 2026-09-27 补的缺口:正文里的外貌描写实测为零(《求你们骂我》第 001 章对主角
// 只有"躺在泔水里"),而平台 LLM 写出的角色卡却有一整套外形 —— 那是它现编的,换一次直出
// 就可能换一张脸。锚表把外形变成**硬约束**。见 docs/技能侧调整-人物外形锚表.md。
func TestReadCharacterSheet(t *testing.T) {
	dir := t.TempDir()
	if got := readCharacterSheet(dir); got != "" {
		t.Errorf("没有锚表时应返回空串(走原来的路,不该让流程失败),实际 %q", got)
	}
	if got := readCharacterSheet(filepath.Join(dir, "不存在的目录")); got != "" {
		t.Errorf("路径不存在时应返回空串,实际 %q", got)
	}
	// 传进来是文件(单本小说)时同样安全返回
	f := filepath.Join(dir, "单本.txt")
	if err := os.WriteFile(f, []byte("正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readCharacterSheet(f); got != "" {
		t.Errorf("路径是文件时应返回空串,实际 %q", got)
	}

	sd := filepath.Join(dir, "设定集")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	sheet := filepath.Join(sd, "人物外形锚表.md")
	if err := os.WriteFile(sheet, []byte("# 人物外形\n\n沈砚:瘦长脸,肩背挺直,左眉尾一道短疤,素麻绳低马尾、及肩胛、额前全部向后梳净。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readCharacterSheet(dir)
	if !strings.HasPrefix(got, "【人物外形锚表 · 硬约束】") {
		t.Errorf("应带硬约束抬头(它是「逐项照抄」的依据,不是参考资料):\n%s", got)
	}
	for _, want := range []string{"瘦长脸", "左眉尾一道短疤", "额前全部向后梳净"} {
		if !strings.Contains(got, want) {
			t.Errorf("锚表内容应原样带进提示词,缺「%s」:\n%s", want, got)
		}
	}

	// 超长截断:按**字符**边界切,不许切坏 UTF-8(中文一个字 3 字节)
	long := strings.Repeat("甲", characterSheetMaxRunes+500)
	if err := os.WriteFile(sheet, []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}
	got2 := readCharacterSheet(dir)
	body := strings.TrimPrefix(got2, characterSheetHeader)
	if n := len([]rune(body)); n != characterSheetMaxRunes {
		t.Errorf("超长应截断到 %d 字,实得 %d", characterSheetMaxRunes, n)
	}
	if !strings.Contains(got2, "甲") {
		t.Error("截断后内容不该为空")
	}
}
