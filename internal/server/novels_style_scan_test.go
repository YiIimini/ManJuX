package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// novels_style_scan_test.go 「导入的小说有没有视频风格字段」的解析判据（2026-10-09）。
//
// 用户原话：「另外导入的小说也需要解析是否存在视频风格相关字段」。
//
// 夹具 = **真机那本书的原文件**（`testdata/novel_brief_real.json` = 逐字拷贝的《我捡的团子…》立项.json，
// `testdata/novel_stylecard_real.md` = 逐字拷贝的 设定集/视觉风格卡-鬼刀式东方星海.md）——
// 照 AGENTS.md「夹具必须照真实形态写」。
func TestScanNovelStyle(t *testing.T) {
	briefReal, err := os.ReadFile(filepath.Join("testdata", "novel_brief_real.json"))
	if err != nil {
		t.Fatal(err)
	}
	cardReal, err := os.ReadFile(filepath.Join("testdata", "novel_stylecard_real.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeNovel := func(t *testing.T, brief, card string, extraCards map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		if brief != "" {
			if err := os.WriteFile(filepath.Join(dir, "立项.json"), []byte(brief), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cards := map[string]string{}
		for k, v := range extraCards {
			cards[k] = v
		}
		if card != "" {
			cards["视觉风格卡-鬼刀式东方星海.md"] = card
		}
		for name, body := range cards {
			if err := os.MkdirAll(filepath.Join(dir, "设定集"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "设定集", name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	// ① 真机原文件：取 art_style（英文权威串），**同时**报出风格卡与中文画风名
	got := scanNovelStyle(writeNovel(t, string(briefReal), string(cardReal), nil))
	if !got.Found() || !strings.Contains(got.Style, "PAINTERLY CINEMATIC EASTERN COSMIC FANTASY") {
		t.Fatalf("真机那本书必须解析出风格串，实际：%q/%q", got.Style, got.Field)
	}
	if got.Field != "立项.json 的 art_style" {
		t.Fatalf("要如实说清取自哪个字段，实际：%q", got.Field)
	}
	if got.Card != "视觉风格卡-鬼刀式东方星海.md" || got.Chars < 1000 {
		t.Fatalf("风格卡必须被报出来（真机卡 4250 字），实际：%q/%d", got.Card, got.Chars)
	}
	if !strings.Contains(got.Hint, "guofeng-starsea-painterly") {
		t.Fatalf("中文「画风」名要带给界面看，实际：%q", got.Hint)
	}

	// ② art_style 被改名/缺失 ⇒ 用同义的「渲染风格串」兜底（真机里两者同值）
	noArt := strings.Replace(string(briefReal), `"art_style"`, `"art_style_renamed"`, 1)
	got = scanNovelStyle(writeNovel(t, noArt, "", nil))
	if got.Field != "立项.json 的 渲染风格串" || !strings.Contains(got.Style, "PAINTERLY") {
		t.Fatalf("art_style 缺失时应退到「渲染风格串」，实际：%q/%q", got.Style, got.Field)
	}

	// ③ 两个英文串都没有、只剩中文「画风」⇒ 兜底用它，并**说清是兜底**
	noEn := strings.Replace(noArt, `"渲染风格串"`, `"render_disabled"`, 1)
	got = scanNovelStyle(writeNovel(t, noEn, "", nil))
	if !got.Found() || !strings.Contains(got.Field, "画风") || !strings.Contains(got.Field, "兜底") {
		t.Fatalf("只剩中文画风名时要兜底且说清，实际：%q/%q", got.Style, got.Field)
	}

	// ④ 只有「风格」（**文学风格** dict：主/次/与上一本的差异）⇒ **不算**视频风格
	//    （把它当画风串会把"极致爽点"这类词塞进画面提示词）
	lit := `{"书名":"某书","风格":{"主":"极致爽点","次":"治愈温馨","与上一本的差异":"上一本冷峻"}}`
	if got := scanNovelStyle(writeNovel(t, lit, "", nil)); got.Found() {
		t.Fatalf("文学风格字段不许当画风串用，实际取到：%q（字段 %q）", got.Style, got.Field)
	}

	// ⑤ 立项.json 什么都没写、但有风格卡 ⇒ 用卡里**第一段代码块**（真机卡自己写着"这就是风格串"）
	got = scanNovelStyle(writeNovel(t, `{"书名":"某书"}`, string(cardReal), nil))
	if !got.Found() || !strings.Contains(got.Style, "PAINTERLY CINEMATIC EASTERN COSMIC FANTASY") ||
		!strings.Contains(got.Field, "风格串代码块") {
		t.Fatalf("有风格卡时应从卡的代码块取串并说明来源，实际：%q/%q", got.Style, got.Field)
	}

	// ⑥ 同目录多张风格卡 ⇒ 取**文件名排序最后**的一张（真机里 3D写实动漫脸 是技能自带快照，
	//    鬼刀式东方星海 才是本书专用；排序靠后的正是后者）
	got = scanNovelStyle(writeNovel(t, `{}`, "", map[string]string{
		"视觉风格卡-3D写实动漫脸.md": "# 旧卡\n\n```\nOLD SNAPSHOT STYLE\n```\n",
		"视觉风格卡-鬼刀式东方星海.md": "# 本书卡\n\n```\nGHOSTBLADE EASTERN COSMIC STYLE\n```\n",
	}))
	if !strings.Contains(got.Style, "GHOSTBLADE EASTERN COSMIC STYLE") || strings.Contains(got.Style, "OLD SNAPSHOT") {
		t.Fatalf("多张卡时要取本书专用那张（排序最后），实际：%q（卡 %q）", got.Style, got.Card)
	}

	// ⑦ 什么都没有 ⇒ Found()==false（界面据此提示"自己选一个"，不许假装有）
	if scanNovelStyle(writeNovel(t, "", "", nil)).Found() {
		t.Fatal("空目录不该报「有风格」")
	}
}
