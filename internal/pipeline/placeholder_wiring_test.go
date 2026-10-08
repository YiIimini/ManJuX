package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/project"
)

// placeholder_wiring_test.go 失败镜占位片段的**接线**（2026-10-06 审计）。
//
// 已有的两道门只测了零件：`writePlaceholderClip` 写得出黑场（placeholder_clip_test.go）、
// `isPlaceholderClip` 认得出标记（placeholder_assemble_test.go）。**接起来之后**坏掉了这件事
// 没有任何门看着 —— 实测的后果是：
//
//	渲染阶段（stages_render.go）写占位、日志播报"占位保住了时长与顺序"，
//	但清单的 `File` 列写的是**空串**，而合成阶段第一句判据就是 `m.File == ""`
//	⇒ 一镜失败整片**永远合不出来**（反复提示"镜头 N 尚未渲染完成"，而那一镜本来
//	  就渲不出来、重渲又失败，还会把上一次的产物路径一起清空）。占位代码等于白写。
//
// 判据两条，都写死在这里（不靠"我改过"）：
//  ① 阶梯三次都失败的镜头：清单里必须有一个**指向真实文件**的产物路径，
//     且它必须能过合成阶段那道判据（assembleClipReject 是同一份生产代码）。
//  ② 缩短重出的那一次（官方阶梯第二级），提示词必须是**四步装配过的** ——
//     从裸正文另拼一份 = 参考图照旧提交而权威清单没了，模型按文字重画角色
//     （2026-09-25 镜头 6 角色漂移的根因）。

// failLadderFixture 一镜、时长 10 秒、真 ffmpeg（写占位要用它）的夹具。
//
// 时长取 10 秒（> planShortenSeconds=6）才走得到"缩短重出"那一级；夹具里那道
// `[Shot 1]` 是**照真实方案形态**写的（单小镜头 + 六段式正文），不是"两三句意思一下"。
func failLadderFixture(t *testing.T) (*Coordinator, *fakeEngine, *project.Project) {
	t.Helper()
	c, fe, proj := renderFixture(t, 1)
	realFF, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机没有 ffmpeg（写占位片段要用它）:%v", err)
	}
	c.ff = realFF
	plan, err := c.store.LoadPlan(proj.ID)
	if err != nil {
		t.Fatalf("读方案失败:%v", err)
	}
	plan.Shots[0].Duration = 10
	if err := c.store.SavePlan(proj.ID, plan); err != nil {
		t.Fatalf("存方案失败:%v", err)
	}
	return c, fe, proj
}

// ① 三次都失败 → 清单里必须有指向真实文件的占位产物，且能过合成判据。
func TestFailedShotPlaceholderIsRecordedForAssembly(t *testing.T) {
	c, fe, proj := failLadderFixture(t)
	// 三次都**超时**失败：超时才会走满阶梯（第 1 级换种子重试 → 第 2 级缩短 → 第 3 级放弃）
	fe.withPlan(
		fakeResult{err: engine.ErrRenderTimeout},
		fakeResult{err: engine.ErrRenderTimeout},
		fakeResult{err: engine.ErrRenderTimeout},
	)
	// 单镜全失败时 runRender 会返回错误（语义见函数尾），这里要的是**清单记账**，故不看 err
	_ = c.runRender(context.Background(), proj, nil, "", 0)

	if n := fe.callCount(); n != 3 {
		t.Fatalf("阶梯该试到第三次（换种子 → 缩短 → 放弃），实际调用 %d 次", n)
	}
	m, err := c.store.LoadManifest(proj.ID)
	if err != nil {
		t.Fatalf("读清单失败:%v", err)
	}
	item, ok := m["1"]
	if !ok {
		t.Fatal("清单里没有镜 1 的条目")
	}
	if item.Status != project.ShotFailed {
		t.Fatalf("三次都失败的镜该标 failed，实际 %q", item.Status)
	}
	if !strings.Contains(item.Error, project.PlaceholderMarker) {
		t.Fatalf("该镜的说明里必须有官方标记 %q（下游复核靠它认占位），实际:%q", project.PlaceholderMarker, item.Error)
	}
	// **本条门要挡的正是这一句**：File 为空 ⇒ 合成阶段 `m.File == ""` 直接拒收整片
	if item.File == "" {
		t.Fatal("失败镜的占位片段没记进清单的 File 列 —— 合成阶段会在 `m.File == \"\"` 处短路，" +
			"用户永远拿不到成片（这正是占位代码存在的意义）")
	}
	if !strings.HasSuffix(item.File, "_placeholder.mp4") {
		t.Errorf("占位片段该用独立文件名（不得顶掉真产物 `<outName>.mp4`），实际:%s", item.File)
	}
	full := filepath.Join(c.store.Dir(proj.ID), filepath.FromSlash(item.File))
	st, err := os.Stat(full)
	if err != nil {
		t.Fatalf("清单指向的占位文件在盘上不存在(%s):%v —— 那合成时会报「产物缺失」", full, err)
	}
	if st.Size() == 0 {
		t.Errorf("占位文件是空的:%s", full)
	}
	// **用生产判据复核**：合成阶段那道闸就是它（不是测试里另写一份镜像逻辑）
	if err := assembleClipReject(item, true); err != nil {
		t.Errorf("这一镜的占位片段还是进不了合成:%v", err)
	}
}

// ② 缩短重出的那一次，发出去的提示词必须是装配过的（转场字幕那层在）。
func TestShortenedRetrySendsAssembledPrompt(t *testing.T) {
	c, fe, proj := failLadderFixture(t)
	fe.withPlan(
		fakeResult{err: engine.ErrRenderTimeout},
		fakeResult{err: engine.ErrRenderTimeout},
		fakeResult{err: engine.ErrRenderTimeout},
	)
	_ = c.runRender(context.Background(), proj, nil, "", 0)

	first := fe.callOf(1)
	third := fe.callOf(3)
	// 首镜按装配点补"从黑渐显"的转场条款（EnsureTransitionCaption：首镜也算切场）
	if !HasTransitionCaption(first.Prompt) {
		t.Fatalf("正常渲染那一次就没带上装配层 —— 夹具或装配链坏了：%q", first.Prompt)
	}
	if first.Length != 10*24 {
		t.Errorf("第一次该按计划 10 秒(240 帧)出，实际 %d 帧", first.Length)
	}
	// **缩短重出的那一次**：正文是从裸 shot.H3Prompt 另拼过的，装配层必须跟着重做一遍
	if third.Length != planShortenSeconds*24 {
		t.Fatalf("第三次该缩短到 %d 秒(%d 帧)，实际 %d 帧", planShortenSeconds, planShortenSeconds*24, third.Length)
	}
	var want string
	plan, err := c.store.LoadPlan(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	shortShot := plan.Shots[0]
	shortShot.Duration = planShortenSeconds
	want = assembleShotPrompt(proj, plan, &shortShot, nil, nil)
	if third.Prompt != want {
		t.Errorf("缩短重出那一次发的不是**装配过的**提示词（参考图权威清单/道具锚定/转场字幕会整层丢失）。\n实得:%q\n应为:%q",
			third.Prompt, want)
	}
	if !strings.Contains(third.Prompt, "[Shot 1]") {
		t.Errorf("缩短后的正文里该保留小镜头标题:%q", third.Prompt)
	}
}

// ③ 反向面：占位标记在、路径却是空的（老清单/漏记账）必须**拒收** ——
// 收进去没有任何文件可指向，问题只会在更远的地方炸（成片缺一段）。
func TestAssembleRejectsPlaceholderWithoutPath(t *testing.T) {
	marked := project.ClipManifest{Status: project.ShotFailed, File: "",
		Error: project.PlaceholderMarker + " —— 官方阶梯已试到第二级，仍失败：超时"}
	if err := assembleClipReject(marked, true); err == nil {
		t.Error("占位标记在、路径为空时必须拒收（否则成片会缺一段而无人过问）")
	}
	// 反向面：没渲过的镜（状态为空）即使有标记也不算产物
	untouched := project.ClipManifest{Status: "", File: "clips/a.mp4", Error: project.PlaceholderMarker}
	if err := assembleClipReject(untouched, true); err == nil {
		t.Error("没渲过的镜（状态为空）不该被标记蒙混过关")
	}
	// 正常完成：放行
	done := project.ClipManifest{Status: project.ShotDone, File: "clips/shot_001.mp4"}
	if err := assembleClipReject(done, true); err != nil {
		t.Errorf("正常完成的镜该放行，实际:%v", err)
	}
	// 清单里根本没有这一镜：拒收（"还没做"不是"带瑕疵出片"）
	if err := assembleClipReject(project.ClipManifest{}, false); err == nil {
		t.Error("清单里没有这一镜时必须拒收")
	}
}

// ④ 【2026-10-08 深度排查修的 P0】引擎**返回成功但没落盘**时，这一镜也必须写占位、也记进 File 列。
//
// 与 ① 是同一件事的另一半：① 覆盖的是"三次都失败"，而这一条覆盖的是引擎侧能返回 nil error
// 却什么都没写的五种情形（云端 task 状态竞态、本地 ffmpeg 半途失败、产物路径被引擎改写、
// 下载截断、以及 fake 引擎的 emptyOut）。原来这条分支把 `File` 传了空串就 continue
// ⇒ 合成阶段第一句 `m.File == ""` 直接拒收整片（用户看到的是"镜头 1 尚未渲染完成"、
// 重渲又总是同一情况，**整集永远拿不到成片**）—— 只给一条路补占位等于没补。
func TestNoArtifactShotAlsoGetsPlaceholder(t *testing.T) {
	c, fe, proj := failLadderFixture(t)
	// 每次都"成功但什么都不写"
	fe.withPlan(fakeResult{emptyOut: true}, fakeResult{emptyOut: true}, fakeResult{emptyOut: true})
	_ = c.runRender(context.Background(), proj, nil, "", 0)

	if n := fe.callCount(); n != 1 {
		t.Fatalf("\"成功但没产物\"不是可重试的错误，不该走降级阶梯，实际调用 %d 次", n)
	}
	m, err := c.store.LoadManifest(proj.ID)
	if err != nil {
		t.Fatalf("读清单失败:%v", err)
	}
	item, ok := m["1"]
	if !ok {
		t.Fatal("清单里没有镜 1 的条目")
	}
	if item.Status != project.ShotFailed {
		t.Fatalf("没有产物的镜必须标 failed（不许当成功），实际 %q", item.Status)
	}
	if item.File == "" {
		t.Fatal("这条路径的占位片段没记进清单的 File 列 —— 合成阶段会在 `m.File == \"\"` 处短路，" +
			"整集永远拿不到成片（与 2026-10-06 修过的那条路是同一个后果）")
	}
	if !strings.HasSuffix(item.File, "_placeholder.mp4") {
		t.Errorf("占位片段该用独立文件名，实际:%s", item.File)
	}
	if !strings.Contains(item.Error, project.PlaceholderMarker) {
		t.Errorf("说明里必须有官方标记 %q（下游复核靠它认占位），实际:%q", project.PlaceholderMarker, item.Error)
	}
	full := filepath.Join(c.store.Dir(proj.ID), filepath.FromSlash(item.File))
	st, err := os.Stat(full)
	if err != nil {
		t.Fatalf("清单指向的占位文件在盘上不存在(%s):%v", full, err)
	}
	if st.Size() == 0 {
		t.Errorf("占位文件是空的:%s", full)
	}
	// **用生产判据复核**：合成阶段那道闸就是它。
	if err := assembleClipReject(item, true); err != nil {
		t.Errorf("这一镜的占位片段还是进不了合成:%v", err)
	}
}

// ⑤ 【2026-10-08 深度排查】没有 ffmpeg 时，质检与合成必须**如实降级**，不许 panic。
//
// main.go 在 ffmpeg 不可用时只打一条日志、把 Coordinator.ff 留成 nil 继续跑（设计如此：
// 平台别的功能不依赖它）。同包别处都判了空（黑闪切除、音频净化、基频核对），只有质检/合成
// 这两条路没有 —— 用户没装 ffmpeg 或没填路径时，点「质检」得到的是**进程 panic**，
// 而不是"去设置页填 ffmpeg 路径"。这两条是同一件事的两个入口，必须一起判。
func TestQCAndAssembleRequireFFmpeg(t *testing.T) {
	c, _, proj := renderFixture(t, 1)
	c.ff = nil
	if err := c.runQC(context.Background(), proj); err == nil {
		t.Error("没有 ffmpeg 时质检必须返回错误（如实说\"缺 ffmpeg\"），而不是 panic 或被当成通过")
	} else if !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("错误里要指名缺的是 ffmpeg（用户据此去设置页填路径）：%v", err)
	}
	if err := c.runAssemble(context.Background(), proj); err == nil {
		t.Error("没有 ffmpeg 时合成必须返回错误（如实说\"缺 ffmpeg\"）")
	} else if !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("错误里要指名缺的是 ffmpeg：%v", err)
	}
}
