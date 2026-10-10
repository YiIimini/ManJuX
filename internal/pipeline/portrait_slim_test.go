package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 定妆照提示词瘦身（2026-10-10 用户点名）。
//
// ① 取景口径**不许被稀释**。真机盘上那批卡（探针 `TestZZProbeSheetSlim` 实测）里，
// "胸像 / 头占 1/4 / 裁到胸口"这件事出现在**平台自己的两句**里 —— 开头声明
// （`BUST PORTRAIT (…) SCALE (mandatory): … The picture is a CHEST-UP portrait: …`，一句话里写三遍）
// 与收尾纪律（`FRAMING (mandatory): … ONE QUARTER … CHEST-UP portrait …`）：`chest-up portrait` ×3、
// `ONE QUARTER` ×2、裁切口径 ×2。这两句是 2026-09-27 实测出来的"开头声明 + 末尾收纪律"
// （只写末尾压不住卡里大段的全身描述），由 `portrait_framing_lead_test.go` 守着，**本轮不动它们**。
// 平台自己的 VLM 读那张图的原话是「头连发髻约占画面高度 1/3；**裁到腰胯**，底边露双拳」。
//
// 本轮处理的是**卡自己带来的**取景句（新写的卡 / 旧措辞的存量句）：
// 装配点一律剥掉，取景只剩平台那一处收口。
//
// staleFramingCue 旧措辞的取景声明（逐字取自盘上卡的抬头，2026-10-08 之前平台自己写的）。
// 它的特征串 `head-to-body ratio` **不在** `sheetFramingWords` 里 ⇒ 这条门最可靠的判据就是它
// （见下面那条断言上的说明：只看 `chest-up portrait` 计数会被短语剥离掩护成摆设）。
const staleFramingCue = "Keep the head-to-body ratio stated in the brief (a seven-and-a-half or eight-heads-tall " +
	"adult reads as tall and long-limbed, with a proportionate head)."

func TestPortraitPromptStatesFramingExactlyOnce(t *testing.T) {
	// 卡 = 平台自己的取景抬头（真机原文，就是 portraitLeadBust 这个常量，它自带 portraitCropCue）
	//     + 一段正常外貌正文 + 中段再写一遍裁切口径 + **一句旧措辞的取景**（真机那张就是这样）。
	card := portraitLeadBust +
		"A lean young man of twenty, long narrow face with a clear jawline and modest cheekbones, " +
		"deep-black phoenix-style eyes, wild straight brows with sparse tails. " +
		portraitCropCue + " " +
		// 旧措辞那句（2026-10-08 之前平台自己写的，真机卡里就是它）——它的特征串 `head-to-body ratio`
		// **不在** `sheetFramingWords` 里（不像 "portrait" 会被短语剥离顺手摘掉），所以它是这条门
		// **唯一**可靠的判据：反向验证当场咬出过"只看 chest-up portrait 计数"是摆设
		// （短语剥离会把 `CHEST-UP portrait` 摘成 `CHEST-UP :`，计数照样相等 ⇒ 门不会红）。
		staleFramingCue + " " +
		"wearing a grey hemp short work jacket with two patched squares on the front."

	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{Style: "国风电影级"}
	ch := project.Character{ID: "char_shenyan", Name: "沈砚", ImagePrompt: card}
	got := PortraitPromptForProject(proj, plan, ch)

	// 期望值 = **平台自己的那两句**里的取景声明条数（开头一句 + 收尾一句各含一次裁切口径）。
	lead := PortraitLeadFraming(KindHuman, proj.Config.Params.Render.Width, proj.Config.Params.Render.Height)
	clause := PortraitFramingClause(KindHuman, proj.Config.Params.Render.Width, proj.Config.Params.Render.Height)
	if lead == "" || clause == "" {
		t.Fatalf("夹具画幅下平台该有自己的取景两句（lead=%q clause=%q）", lead, clause)
	}
	const cue = "chest-up portrait"
	want := strings.Count(strings.ToLower(lead+clause), cue)
	gotN := strings.Count(strings.ToLower(got), cue)
	if gotN != want {
		t.Errorf("取景裁切口径（%q）在装配后的提示词里出现 %d 次，平台自己那两句只有 %d 次 —— "+
			"多出来的就是**卡里带来的旧取景**没剥净（三套口径互相稀释，模型只会按其中一套执行，"+
			"真机就是按\"裁到腰胯\"画的）：\n%s", cue, gotN, want, got)
	}
	// **这条才是硬判据**：卡里那句**旧措辞**取景必须整句没了。它的特征串 `head-to-body ratio`
	// 不在 `sheetFramingWords` 里 ⇒ 短语剥离摘不掉它，只有整句剥离能。
	//
	// 【为什么不能只看上面的 `cue` 计数（反向验证当场咬出的摆设）】关掉整句剥离后门**照样绿**：
	// `stripSheetFramingWords` 会把卡里那句的 `portrait` 单词摘掉（`CHEST-UP portrait` → `CHEST-UP :`），
	// 于是 `cue` 计数又相等了 —— 判据挂在"会被下游改写的字面量"上，等于没判。
	if strings.Contains(got, "head-to-body ratio") {
		t.Errorf("卡里带来的旧措辞取景句没剥净（`head-to-body ratio` 还在）：\n%s", got)
	}
	// 平台自己的两句都必须**恰好一次**（幂等：重复装配不许叠加）。
	if n := strings.Count(got, lead); n != 1 {
		t.Errorf("开头那句取景声明应恰好一次，实际 %d 次", n)
	}
	if n := strings.Count(got, clause); n != 1 {
		t.Errorf("收尾那句构图纪律应恰好一次，实际 %d 次", n)
	}
	// 反向针：外貌正文**一个字都不能少**（取景是替换取景句，不是替换卡里的外貌描述）。
	for _, keep := range []string{"grey hemp short work jacket", "long narrow face", "phoenix-style eyes"} {
		if !strings.Contains(got, keep) {
			t.Errorf("剥取景句时误伤了外貌正文（%q 不见了）：\n%s", keep, got)
		}
	}
	// 幂等：把装配结果再喂一遍装配（"重新生成那张定妆照"走的就是这条路），取景不许翻倍。
	again := PortraitPromptForProject(proj, plan, project.Character{ID: ch.ID, Name: ch.Name, ImagePrompt: got})
	if n := strings.Count(strings.ToLower(again), cue); n > want {
		t.Errorf("重复装配把取景叠成了 %d 次（幂等破了）：\n%s", n, again)
	}
}

// 自称（selfName）拿不到时不许留半截句 —— 真机那张卡的结尾就是 `… in the frame — .`
// （一个破折号接句号），半截话比不写更容易被模型当成指令。
func TestCastContrastBlockNoDanglingDashWhenSelfUnknown(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{
		{ID: "a", Name: "沈砚", ImagePrompt: "A lean young man of twenty with a long narrow face and black hair tied in a hank."},
		{ID: "b", Name: "卫承渊", ImagePrompt: "A heavyset man of fifty-one with a square-rounded face and heavy hooded eyelids."},
	}}
	blk := castContrastBlock(plan, "", "", KindHuman) // 自称未知（真机那张就是）
	if blk == "" {
		t.Fatal("同剧还有其他角色时该拼 CAST DISTINCTNESS（这是防撞脸的那一块，不许整块删）")
	}
	if strings.Contains(blk, "— .") || strings.Contains(blk, "「」") {
		t.Errorf("自称未知时不许留半截句：%q", blk)
	}
	if !strings.Contains(blk, "Do NOT depict any of these other characters:") {
		t.Errorf("防撞脸的那句结论必须还在：%q", blk)
	}
	if !strings.Contains(blk, "CAST DISTINCTNESS") {
		t.Errorf("防撞脸块本身不许整块删：%q", blk)
	}
}

// 别人的外形摘要必须是**完整从句**（2026-10-10 用户「把截断的六段摘要换成结论式一句」）。
//
// 真机形态（逐字取自盘上 `asset_map.json` 的 `char:char_*` 卡）：旧实现按 160 字符硬切 + 补省略号，
// 六条**全部切在词中间** —— `…clear jawline, deep-blac…`、`…feathered and diss…`、
// `…heavy hooded eyelids over eyes …`、`…thick ey…`。半截词对模型不是信息、是噪声。
// 但 CAST DISTINCTNESS 是防撞脸的那一块（2026-09-11 实测靠它把"胖→瘦、疤→无疤"掰回来），
// 所以判据是"**收成完整从句**"，不是"整块删"。
//
// 夹具就用**盘上那几张卡的真实外形段**（逐字，只有换行被拼平）——长到必然触发收句。
func TestCastDistinctnessEntriesAreWholeClauses(t *testing.T) {
	real := []struct{ name, body string }{
		{"卫承渊", "A heavyset man of fifty-one, broad-shouldered and thick-backed, square-rounded face with flat cheekbones and a broad square jaw, heavy hooded eyelids over eyes set a little deep, a level lip line, deep nasolabial folds, sallow yellow complexion with two age patches on the cheekbones, thick calluses and coarse knuckles on the backs of the hands"},
		{"江白屿", "A tall slim young man of twenty-one, angular melon-seed face with a narrow fleshless chin and high cheekbones, the mid-face a little hollow, phoenix eyes with a faint upward tilt and upward-slanted brows, thin shaped lips with a pronounced cupid's bow and mouth corners habitually pressed down, cold pale skin with no colour in the face"},
		{"裴九", "A slim young man of twenty-six with a very straight back, long narrow face with a flat mid-face and a narrow jaw, narrow long single-lidded eyes with grey pupils, thinly shaved shadow on the chin, slender fingers with reading calluses, an ink stain on the inner side of the right index finger"},
		{"锁匠", "A stocky man of about fifty, thick upper body with a short neck and a slightly stooped back, square-rounded face with heavy cheeks and a flat mid-face, thick eyebrows with grey in them, roughened sallow skin, big short fingers with thick calluses on the pads and an old burn scar on the back of the left hand"},
		{"沈砚·Q版", "A palm-sized miniature of a lean young man at two and a half heads tall, big head on a small short-legged body, long narrow face with a clear jawline, deep-black phoenix-style eyes with a slight upward tilt at the outer corner, a thin old scar at the outer end of the left brow"},
		{"吞吞", "A palm-sized flat matte-black shadow creature lying low on the ground, no eyes, no mouth, no nose, no edges a shadow should have, its outline feathered and dissolving like ink blotted into paper so that the fading rim shows nothing inside, the whole surface a fine short-furred black mist that reflects no light"},
	}
	plan := &project.Plan{}
	for i, r := range real {
		plan.Characters = append(plan.Characters, project.Character{
			ID: "char_" + string(rune('a'+i)), Name: r.name,
			// 真机形态：description 是中文、英文外形写在 image_prompt 里，且卡**以取景抬头开头**
			// （装配点与摘要都先剥掉那句再取外形正文 —— 这里就走同一条路）。
			ImagePrompt: portraitLeadBust + r.body,
		})
	}
	blk := castContrastBlock(plan, "", "", KindHuman)
	if blk == "" {
		t.Fatal("六张真机形态的卡该拼出对比块（它是防撞脸的那一块，不许整块删）")
	}
	if strings.Contains(blk, "…") {
		t.Errorf("条目里不许再出现省略号（半截词对模型是噪声，用户点名要求换成结论式）：%q", blk)
	}
	// 每一条摘要都必须是**完整从句**：在源卡正文里找得到，且紧接着的字符不是字母/数字（没切断单词）。
	src := map[string]string{} // 夹具卡的**英文外形正文**（摘要就是从它里面取的）
	for _, r := range real {
		src[r.name] = r.body
	}
	// 名单段的边界：`…of this series: <条目>; <条目>… . They are listed ONLY so that …`
	const listHead = "other cast members of this series: "
	const listTail = ". They are listed ONLY so that "
	i := strings.Index(blk, listHead)
	j := strings.LastIndex(blk, listTail)
	if i < 0 || j < i {
		t.Fatalf("对比块的名单段找不到（夹具形态变了？）：%q", blk)
	}
	entries := strings.Split(blk[i+len(listHead):j], "); ")
	if len(entries) < 4 {
		t.Fatalf("真机形态下该列出多条别人外形（实际 %d 条）：%q", len(entries), blk)
	}
	whole := 0
	for _, e := range entries {
		open := strings.Index(e, "(")
		if open < 0 {
			t.Errorf("条目缺 `名字(摘要)` 形态（旧实现里那句 `( …)` 残句就是它）：%q", e)
			continue
		}
		note := strings.TrimSpace(strings.TrimSuffix(e[open+1:], ")"))
		if note == "" {
			t.Errorf("空条目（旧实现里那句 `( …)` 残句）：%q", blk)
			continue
		}
		found := false
		for _, body := range src {
			k := strings.Index(body, note)
			if k < 0 {
				continue
			}
			found = true
			next := body[k+len(note):]
			if next != "" && isWordRune(rune(next[0])) {
				t.Errorf("条目切在词中间（`%s` 之后紧接着 %q）：这是旧实现的 160 字符硬切形态", note, next[:min(8, len(next))])
			}
			break
		}
		if !found {
			t.Errorf("条目 %q 在源卡正文里找不到 —— 摘要被改写/拼接过了（对比块要的是**别人的原话**）", note)
			continue
		}
		whole++
	}
	if whole == 0 {
		t.Error("一条完整从句都没验到 —— 这条门形同虚设")
	}
	// 结尾那句防撞脸纪律照旧（与条目瘦身无关，不许被顺手删）。
	if !strings.Contains(blk, "never reuse one face template") ||
		!strings.Contains(blk, "Do NOT depict any of these other characters: exactly ONE person appears in the frame") {
		t.Errorf("防撞脸的结论句必须保留：%q", blk)
	}
}

// 【判据复用】`isWordRune` 定义在 viewedit_test.go（与 compactIdentity 的整词判据同源）——
// 同一件事不写第二份（本仓老毛病就是"同一口径两处实现，改一处忘一处"）。

// 存量成品（库里已入库的定妆照提示词）也要**同一件收口**。
//
// 【为什么单列一条（2026-10-10）】新建卡的路径只在卡**还没有平台标记**时跑；真机盘上那批
// 提示词是"卡 + 装配块"的**成品**（带 IDENTITY FIDELITY 标记）⇒ 走幂等短路 ⇒ 卡自己带来的
// 取景句（真机那张有两份：抬头 + 中段）与旧措辞的收尾纪律会**原样保留**。
// 用户点一次「重新生成」，三套口径照旧稀释 —— 这条门钉的就是那条短路也要收口。
func TestExistingAssembledSheetGetsSlimmedToo(t *testing.T) {
	card := "A lean young man of twenty, long narrow face with a clear jawline and modest cheekbones, " +
		"deep-black phoenix-style eyes with a slight upward tilt at the outer corner, a thin old scar " +
		"at the outer end of the left brow, wearing a grey hemp short work jacket with two patched squares."
	// 真机成品的形态：卡的取景抬头 + 外貌正文 + 卡中段又写一遍裁切 + 平台装配块 + 收尾纪律 + FACE ID。
	stored := portraitLeadBust + card + " " + portraitCropCue + " " +
		"IDENTITY FIDELITY (mandatory): copy the face in the reference picture exactly. " +
		PortraitFramingClause(KindHuman, 768, 1344) + " " +
		"FACE ID (final check): the face is the subject of this sheet — do not drift from the identity lock."
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{Style: "国风电影级"}
	ch := project.Character{ID: "char_shenyan", Name: "沈砚", ImagePrompt: stored}
	got := PortraitPromptForProject(proj, plan, ch)
	lead := PortraitLeadFraming(KindHuman, 768, 1344)
	clause := PortraitFramingClause(KindHuman, 768, 1344)
	if n := strings.Count(got, lead); n != 1 {
		t.Errorf("开头取景声明该恰好一次，实际 %d 次：\n%s", n, got)
	}
	if n := strings.Count(got, clause); n != 1 {
		t.Errorf("收尾取景纪律该恰好一次，实际 %d 次：\n%s", n, got)
	}
	const cue = "chest-up portrait"
	want := strings.Count(strings.ToLower(lead+clause), cue)
	if n := strings.Count(strings.ToLower(got), cue); n != want {
		t.Errorf("存量成品装配后取景口径出现 %d 次（平台自己那两句是 %d 次）—— 卡自己带来的那份没收掉：\n%s", n, want, got)
	}
	// 收尾纪律要插在 FACE ID **之前**（"这张脸是谁"必须是最后一句）。
	if i, j := strings.LastIndex(got, clause), strings.LastIndex(got, "FACE ID"); i < 0 || j < 0 || i > j {
		t.Errorf("收尾取景纪律必须排在 FACE ID 之前（i=%d j=%d）：\n%s", i, j, got)
	}
	// 卡的外貌正文一个字不能少。
	for _, keep := range []string{"grey hemp short work jacket", "long narrow face", "IDENTITY FIDELITY"} {
		if !strings.Contains(got, keep) {
			t.Errorf("收口时误伤了正文/装配块（%q 不见了）：\n%s", keep, got)
		}
	}
	// 卡中段那份裁切口径必须被删（留下的两份：开头声明自带一份、收尾纪律自带一份）。
	if n, w := strings.Count(got, portraitCropCue), strings.Count(lead+clause, portraitCropCue); n != w {
		t.Errorf("卡中段那份裁切口径没删净（cropCue 出现 %d 次，平台自己那两句是 %d 次）：\n%s", n, w, got)
	}
	// 幂等：收过一次再收不变（装配点会被调用两次）。
	if again := PortraitPromptForProject(proj, plan, project.Character{ID: ch.ID, Name: ch.Name, ImagePrompt: got}); again != got {
		t.Errorf("存量成品的收口不是幂等的：\n第一次=%s\n第二次=%s", got, again)
	}
	// 非人类型不套人像取景纪律 ⇒ 原样返回（器物/场景各有自己的取景，见 EntityProfile）。
	obj := "A single small brass key centred on a plain pure white background, tied onto a red cord."
	if s := TightenSheetFraming(obj, KindObject, 768, 1344); s != obj {
		t.Errorf("器物不该被套人像取景纪律：%q", s)
	}
}

// 存量 **Q 版**成品也要剥掉卡里那份**人像**取景纪律（真机探针咬出来的重复）。
//
// 【形态（盘上 `char:char_shenyan_q` 那张 6664 字的卡就是）】Q 版卡是被人像那套装配出来的
// （标记 = IDENTITY FIDELITY、收尾纪律 = 人像胸像那套），而重新生成时 kind 判成 chibi
// （CHIBI FIDELITY 那一档）⇒ 卡里那份人像收尾纪律与新的 Q 版取景纪律**并存**：
// 探针实测 6664 字 → 9554 字、`FRAMING (mandatory)` ×2（改完后 8347 字、×1）。
func TestChibiSheetDropsHumanFramingClause(t *testing.T) {
	humanClause := PortraitFramingClause(KindHuman, 768, 1344)
	stored := portraitLeadBust +
		"A palm-sized miniature of a lean young man at two and a half heads tall, big head on a " +
		"small short-legged body, long narrow face with a clear jawline. " +
		humanClause + " FACE ID (final check, mandatory): the chibi keeps the identity markers of the character."
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{Style: "国风电影级"}
	ch := project.Character{ID: "char_shenyan_q", Name: "灵影小自己", Kind: "chibi",
		Description: "沈砚本人的Q版影子形象", ImagePrompt: stored}
	got := PortraitPromptForProject(proj, plan, ch)
	if n := strings.Count(got, "FRAMING (mandatory)"); n != 1 {
		t.Errorf("Q 版成品里取景纪律该只剩一处（卡里那份人像的必须剥掉），实际 %d 处：\n%s", n, got)
	}
	// 卡里那份收尾纪律必须**整份**不再出现第二次：Q 版与人的收尾纪律**是同一段文字**
	// （`PortraitFramingClause` 把 KindHuman / KindAndroid / KindChibi 归在一档），所以判据只能是
	// "整个 clause 只出现一次" —— 用 `Contains` 判等于什么都没判（末尾那份新纪律自己就命中了）。
	if n := strings.Count(got, humanClause); n != 1 {
		t.Errorf("人像收尾纪律在 Q 版成品里出现 %d 次（卡里那份没剥掉）：\n%s", n, got)
	}
	if !strings.Contains(got, "CHIBI FIDELITY") {
		t.Errorf("Q 版自己的忠实度块丢了：\n%s", got)
	}
	// 幂等：再装配一次不许继续变（真机探针盯的就是这一条）。
	if again := PortraitPromptForProject(proj, plan, project.Character{ID: ch.ID, Name: ch.Name, Kind: ch.Kind,
		Description: ch.Description, ImagePrompt: got}); again != got {
		t.Errorf("Q 版成品的收口不是幂等的：第一次 %d 字、第二次 %d 字", len(got), len(again))
	}
}
