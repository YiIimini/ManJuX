package pipeline

import (
	"strings"
	"testing"
)

// cut_audit_wiring_test.go 「小分镜兑现」体检的**接线**门（2026-10-14）。
//
// 【为什么单独一道（这一轮自己栽过）】判据本体（filmaudit/cuts.go）写完之后，真机跑出来
// 只报了一句「没量到」——**最该报的那一镜（计划 2 刀 / 画面 0 刀）被吞了**，根因是量尺把
// "量到 0 刀"写成了 nil（与"没量到"同形）。单测当时全绿，因为它喂的是手写切片、没走真入口。
// 教训与本包记过多次的一样：**判据对了不等于接对了**，接线这一层必须有人看着。
// 这里咬三件事：① app 内体检真的量了（量尺来自本机 ffmpeg）；② 三态都要有出口；
// ③ 命令行与 app 共用同一套、且都点名"哪几镜没量到"；④ `Audit` 真的把这个判据接进去了。
func TestCutDeliveryAuditIsWiredAndThreeState(t *testing.T) {
	app := readSrc(t, "film_audit.go")
	const call = "filmaudit.MeasureCuts(cutFn, clips)"
	at := strings.Index(app, call)
	if at < 0 {
		t.Fatal("合片体检没有量逐镜的内容切点 —— 「小分镜到底有没有做好」只能等用户自己盯着看")
	}
	if line := lineOf(app, at); strings.HasPrefix(strings.TrimSpace(line), "//") {
		t.Fatalf("那句调用只出现在注释里：%q", line)
	}
	if !strings.Contains(app, "filmaudit.CutsFromFFmpeg(") {
		t.Error("量尺不是用 filmaudit.CutsFromFFmpeg 造的 —— 内容切点量不出来")
	}
	// 三态必须都有出口：量尺没有 / 一镜都没量到 / 有几镜没量到。
	for _, want := range []string{"cutFn == nil", "len(in.ShotCuts) == 0", "len(cutMissing) > 0"} {
		if !strings.Contains(app, want) {
			t.Errorf("少了三态里的这一支：%s —— 没量到会被当成「合格」", want)
		}
	}
	// 量不到的镜必须**点名**，不然用户不知道该重出哪一镜。
	if !strings.Contains(app, "shotIDsText(cutMissing)") {
		t.Error("没量到的镜没有点名（shotIDsText(cutMissing)）")
	}
	// 结论要**真的追加进体检结论表**（不然只有那几句 info 播报，`film_audit.md` 里翻不到；
	// 而且 `_ = cutDeliveryFindings(in)` 这种"算完就扔"的写法也必须被咬住 —— 那是真缺陷）。
	if !strings.Contains(readSrc(t, "../filmaudit/audit.go"), "out = append(out, cutDeliveryFindings(in)...)") {
		t.Error("逐镜判据没有**追加**进体检结论表（应当是 out = append(out, cutDeliveryFindings(in)...)）")
	}
}

// 命令行与 app 必须共用同一套判据 —— 包括逐镜的内容切点这一项（不然两处结论会分叉）。
//
// 【判据要咬**接线处**，不是咬"文件里出现过这个函数名"（2026-10-14 反向验证 P4 第一次没变红）】
// 量尺构造函数 `cutsFn` 里当然写着 `filmaudit.CutsFromFFmpeg(`，可只要主流程不调 `cutsFn`，
// 命令行那边照样永远量不到 —— 只查函数名会放它过去（摆设守卫）。
func TestCLIAndAppShareTheCutDeliveryJudgement(t *testing.T) {
	cli := readSrc(t, "../../cmd/auditfilm/main.go")
	if !strings.Contains(cli, "cuts := cutsFn(") {
		t.Error("cmd/auditfilm 的量尺没被主流程调起来（要有 `cuts := cutsFn(...)`）—— 命令行与 app 的结论会分叉")
	}
	if !strings.Contains(cli, "filmaudit.CutsFromFFmpeg(") {
		t.Error("量尺不是用 filmaudit.CutsFromFFmpeg 造的 —— 内容切点量不出来")
	}
	if !strings.Contains(cli, "filmaudit.LoadEpisode(dir, probe, tracks, luma, cuts)") {
		t.Error("cmd/auditfilm 没把切点量尺交给 LoadEpisode —— 命令行那边永远量不到")
	}
	// 命令行也要**如实播报没量到**（不许静默跳过、更不许当成合格）：
	// ① 量尺没有 ⇒ 当场说一句；② 有几镜没量到 ⇒ 点名（`MissingCutShots`）。
	if !strings.Contains(cli, "cuts == nil") {
		t.Error("cmd/auditfilm 在没量到切点时一句话都不说 —— 用户会以为「这集的分镜都兑现了」")
	}
	if !strings.Contains(cli, "filmaudit.MissingCutShots(in)") {
		t.Error("cmd/auditfilm 没点名『哪几镜的内容切点没量到』 —— 用户不知道该重出哪一镜")
	}
}

// 判据本体必须真的被 `Audit` **追加进结论**（纯函数写好了但没人问 = 没有；
// 算完就扔掉（`_ = cutDeliveryFindings(in)`）同样等于没有 —— 反向验证 P2 咬出来的）。
func TestAuditCallsCutDeliveryFindings(t *testing.T) {
	src := readSrc(t, "../filmaudit/audit.go")
	const call = "out = append(out, cutDeliveryFindings(in)...)"
	at := strings.Index(src, call)
	if at < 0 {
		t.Fatal("`Audit` 没有把逐镜判据追加进结论（应为 " + call + "）—— 写好了但没人问，用户看不到结论")
	}
	if line := lineOf(src, at); strings.HasPrefix(strings.TrimSpace(line), "//") {
		t.Fatalf("那句调用只出现在注释里：%q", line)
	}
}
