package pipeline

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/YiIimini/ManJuX/internal/project"
)

// refmanifest_closing_test.go 收尾条款的闸门（技能侧《平台侧整改清单》§1.1 的判据）。
//
// 事故形态：装配顺序是 `ShotRenderPrompt`（内部链最后一步是画面文字规则）→
// `EnsureRefManifest`（stages_render.go:245/:261），也就是**权威清单是最后一块**；
// 而它每条清单行都写中文名（`<Picture 1> = 翻篇`），自己还带一段中文对照。
// 官方 ON-SCREEN TEXT 规则的判据是「没被英文引号括起来的文字，就是模型画成字形的来源」
// —— 于是技能侧刚把 `<d>` 外的汉字清到 0（实测 7083 → 0），平台在最后一步又追加一段
// 中文名，后面再无收尾，前面整段的清洗被最后一段抵消。
//
// 所以这里钉三件事：① 最终提示词尾部窗口内不许有无防护汉字；② `<d>` 内台词不受影响；
// ③ 收尾条款确实排在 manifest 之后、且是整条提示词的最后一段。

// cjkNamesFor 提示词里允许出现的"受保护汉字"：主体名（角色 + 场景 + 道具名）。
func cjkNamesFor(plan *project.Plan) []string {
	names := make([]string, 0, len(plan.Characters)+len(plan.Scenes)+len(plan.Props))
	for _, c := range plan.Characters {
		names = append(names, c.Name)
	}
	for _, s := range plan.Scenes {
		names = append(names, s.Name)
	}
	for _, pr := range plan.Props {
		names = append(names, pr.Name)
	}
	return names
}

// TestRefManifestClosingLeavesNoUnprotectedCJKInTail ① 有 manifest 的镜头，最终提示词
// 尾部 300 字符内不得出现无防护汉字串（受保护 = `<d>…</d>` 内、或主体名）。
func TestRefManifestClosingLeavesNoUnprotectedCJKInTail(t *testing.T) {
	for _, compact := range []bool{false, true} {
		dir, shot, assets := refmanAssets(t)
		_, roles, missing, _ := RefPlan(dir, shot, assets, compact)
		if len(missing) != 0 {
			t.Fatalf("不该报缺失，实际 %v", missing)
		}
		proj := &project.Project{}
		proj.Config.Params = project.DefaultStageParams()
		plan := &project.Plan{
			Style: "ANIMATED-FEATURE CINEMATIC LOOK, SEMI-STYLISED FACES",
			Characters: []project.Character{{
				ID: "char_q", Name: "顾青禾", Kind: "human",
				Description: "十七岁的女孩，右眉尾有一道浅疤", Voice: "quiet young female voice",
			}},
			Scenes: []project.Scene{{
				ID: "tent", Name: "旧城派送站棚", Description: "雨中的旧城派送站棚，棚布漏雨",
			}},
		}
		shot.Characters = []string{"char_q"}
		shot.Scene = "tent"
		shot.H3Prompt = "detailed_description:\n[Shot 6] she steps out of the tent: <d>[Chinese]我先走了。</d>"

		prompt := EnsureRefManifest(ShotRenderPrompt(proj, plan, &shot), shot, roles)
		names := cjkNamesFor(plan)

		// 反向控制：这份提示词里**本来就有**无防护汉字（清单的中文对照段），
		// 否则下面的判据等于什么都没测。
		if runs := project.UnprotectedCJKRuns(prompt, names); len(runs) == 0 {
			t.Fatalf("用例无效：拼出来的提示词里没有任何无防护汉字，判据形同不存在")
		}
		if runs := project.TailUnprotectedCJKRuns(prompt, refManifestTailWindow, names); len(runs) != 0 {
			t.Fatalf("最终提示词尾部 %d 字符内出现了无防护汉字串 %q —— 收尾条款没盖住 manifest 里的中文"+
				"（compact=%v）：\n%s", refManifestTailWindow, runs, compact, tailOf(prompt, refManifestTailWindow))
		}
	}
}

// TestRefManifestClosingKeepsDialogueIntact ② `<d>` 内的中文台词不受影响：它既不被
// 判成无防护汉字，也要**逐字留着**（H3 拿它出原生中文配音，删掉等于没配音）。
func TestRefManifestClosingKeepsDialogueIntact(t *testing.T) {
	dir, shot, assets := refmanAssets(t)
	_, roles, _, _ := RefPlan(dir, shot, assets, false)
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{
		Style: "ANIMATED-FEATURE CINEMATIC LOOK",
		Characters: []project.Character{{ID: "char_q", Name: "顾青禾", Kind: "human",
			Description: "十七岁的女孩", Voice: "quiet young female voice"}},
		Scenes: []project.Scene{{ID: "tent", Name: "旧城派送站棚", Description: "雨中的旧棚"}},
	}
	shot.Characters = []string{"char_q"}
	shot.Scene = "tent"
	shot.H3Prompt = "detailed_description:\n[Shot 6] she steps out: <d>[Chinese]我先走了。</d>"

	prompt := EnsureRefManifest(ShotRenderPrompt(proj, plan, &shot), shot, roles)
	if !strings.Contains(prompt, "<d>[Chinese]我先走了。</d>") {
		t.Fatal("收尾条款不得吃掉台词 —— <d>[Chinese]…</d> 是官方指定的「说」通道，还要供原生中文配音")
	}
	// 台词落在尾部窗口里也算受保护（判据必须先遮罩再取窗口，否则标签被切在边界上会漏保护）
	tail := strings.Repeat("x", refManifestTailWindow-40) + "<d>[Chinese]我先走了。</d>"
	if runs := project.TailUnprotectedCJKRuns(tail, refManifestTailWindow, cjkNamesFor(plan)); len(runs) != 0 {
		t.Fatalf("尾部窗口内的 <d> 台词不该报，实际 %v", runs)
	}
}

// TestRefManifestClosingIsTheLastParagraph ③ 收尾条款必须排在 manifest 之后，且是整条
// 提示词的**最后一段**：只把它塞进 manifest 中间，等于让后面的中文名继续当字形来源。
func TestRefManifestClosingIsTheLastParagraph(t *testing.T) {
	dir, shot, assets := refmanAssets(t)
	_, roles, _, _ := RefPlan(dir, shot, assets, false)
	m := RefManifest(shot, roles)

	iMan := strings.Index(m, refManifestKey)
	iClose := strings.Index(m, refManifestClosingKey)
	if iMan < 0 || iClose < 0 {
		t.Fatalf("清单或收尾条款不见了（判据串被改过？）：\n%s", m)
	}
	if iClose < iMan {
		t.Fatalf("收尾条款排在 manifest 之前（%d < %d）—— 它必须在清单之后才能盖住清单里的中文名",
			iClose, iMan)
	}
	if !strings.HasSuffix(strings.TrimRight(m, "\n"), refManifestClosing) {
		t.Fatalf("收尾条款不是清单的最后一段：\n%s", m)
	}
	if n := strings.Count(m, refManifestClosingKey); n != 1 {
		t.Fatalf("收尾条款必须恰好一条（幂等由 refManifestKey 统一守），实际 %d 条", n)
	}
	// 幂等：清单已带过时，EnsureRefManifest 不得再叠一条
	if n := strings.Count(EnsureRefManifest(m, shot, roles), refManifestClosingKey); n != 1 {
		t.Fatalf("收尾条款不幂等，实际 %d 条", n)
	}
	// 无参考图（t2va）时提示词原样放行，不该凭空多出收尾段
	if plain := EnsureRefManifest("subject_definitions: none.", shot, nil); plain != "subject_definitions: none." {
		t.Fatal("无参考图时不得改动提示词")
	}
}

// TestRefManifestClosingOutweighsTailWindow 收尾条款的长度是**判据的一部分**：
// manifest 自己的中文对照段紧挨在它前面，收尾若短于尾部窗口，那段中文仍会落在
// 300 字符窗口内 —— 判据会永远红。所以这里把"足够长"钉成闸门（改短即挂）。
func TestRefManifestClosingOutweighsTailWindow(t *testing.T) {
	if n := utf8.RuneCountInString(refManifestClosing); n < refManifestTailWindow {
		t.Fatalf("收尾条款只有 %d 字符，不足以把 manifest 的中文挤出尾部 %d 字符窗口 —— "+
			"判据会失效：尾部仍会出现无防护汉字串", n, refManifestTailWindow)
	}
	// 判据串必须在（改文案必须保留这个串），且必须是给模型的英文说明
	if !strings.Contains(refManifestClosing, refManifestClosingKey) {
		t.Fatal("收尾条款丢了判据串")
	}
	for _, want := range []string{
		"production notes, not visible text", // 名字/编号是制作用注，不是画面文字
		"never draw, caption or subtitle",    // 明令不许画成字、字幕、标题
	} {
		if !strings.Contains(refManifestClosing, want) {
			t.Errorf("收尾条款缺少 %q：\n%s", want, refManifestClosing)
		}
	}
}

// tailOf 取末尾 window 个字符，失败信息里贴那一段（否则"哪几个字"要人去数字符）。
func tailOf(s string, window int) string {
	r := []rune(s)
	if len(r) > window {
		r = r[len(r)-window:]
	}
	return string(r)
}
