package pipeline

import (
	"os"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 定妆照比例自检的**开关与降级**策略(技能侧清单 §5.1)。
//
// 与画面文字检测(planTextDetect)同一套要求,理由也相同 —— 这三种处置一旦写错不会报错:
//   - 关掉它必须彻底静默(连事件都不发),否则"关掉"看起来就像"每张卡都量过且合格";
//   - 类型不适用(器物没有人体比例 / Q 版故意的二到三头身)是"没这项",不是"没验过";
//   - 开着却没有视觉模型必须**如实降级**(事件流里写明"未做"),不假装通过。
func TestPlanProportionCheck(t *testing.T) {
	cases := []struct {
		name        string
		enabled     bool
		haveClient  bool
		applicable  bool
		wantRun     bool
		wantSkipped string // "" = 连事件都不发
	}{
		{name: "开关关闭 → 不跑,且不留痕", enabled: false, haveClient: true, applicable: true, wantRun: false, wantSkipped: ""},
		{name: "开关关闭时也不因缺模型留痕", enabled: false, haveClient: false, applicable: true, wantRun: false, wantSkipped: ""},
		{name: "类型不适用(器物/妖兽/Q版) → 不跑,也不是「没验过」", enabled: true, haveClient: true, applicable: false, wantRun: false, wantSkipped: ""},
		{name: "类型不适用 + 缺模型 → 仍然不留痕(没这项,不必提醒)", enabled: true, haveClient: false, applicable: false, wantRun: false, wantSkipped: ""},
		{name: "开了但无可用能力 → 如实降级", enabled: true, haveClient: false, applicable: true, wantRun: false, wantSkipped: "未做比例自检:无可用能力(未配置视觉模型;在设置页填好 VLM 后会自动开检)"},
		{name: "开了、有能力、类型适用 → 跑", enabled: true, haveClient: true, applicable: true, wantRun: true, wantSkipped: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planProportionCheck(tc.enabled, tc.haveClient, tc.applicable)
			if got.Run != tc.wantRun {
				t.Errorf("Run = %v,期望 %v", got.Run, tc.wantRun)
			}
			if got.Skipped != tc.wantSkipped {
				t.Errorf("Skipped = %q,期望 %q", got.Skipped, tc.wantSkipped)
			}
			if got.Run && got.Skipped != "" {
				t.Errorf("Run=true 却带了跳过原因 %q", got.Skipped)
			}
			// 不变量:开着、有能力、类型也适用,却不跑 = 静默跳过(必须有原因)
			if tc.enabled && tc.haveClient && tc.applicable && !got.Run {
				t.Error("条件齐备却没跑,却不写原因 —— 事件流里看不出到底量没量")
			}
		})
	}
}

// 接线闸门:资产阶段必须真的把这条链接上,而不是只留一个能配的开关。
//
// 与 TestQCTextDetectIsWired 同一手法(源码级钉住):runAssets 需要真 ComfyUI + 真项目
// 才能跑起来,而这里要钉的是**接线**本身 —— 开关被读到了、自检在定妆照出图后被调用、
// 告警与降级文案都发出去了。任何一处被删掉,这条就红。
func TestProportionCheckIsWired(t *testing.T) {
	src := assetsStageSource(t)
	for _, want := range []string{
		"proj.Config.Params.QC.Enabled && proj.Config.Params.QC.ProportionCheckOn()", // 质检总开关 + 本条三态开关(缺键 = 开)
		"planProportionCheck(enabled, client != nil, applicable)",                    // 开关/能力/类型共同决定处置
		"PortraitProportionCheckable(ek, imagePrompt)",                               // 只对人做(器物没有人体比例)
		"TurnaroundVerifier(c.cfg.Get().VLM)",                                        // 复用设置页那台视觉模型
		"qc.DetectPortraitProportion(",                                               // 真的读图 + 调视觉模型
		"qc.ProportionWarnText(",                                                     // 偏离 → 点名哪一项的告警
		"checkProportion(ek, imagePrompt, name, path)",                               // 定妆照**出图后**立刻自检
		"HumanProportionClausePresent(imagePrompt)",                                  // 锚点是否在场只作留痕
		"proportionNoCapability",                                                     // 没能力要能被调用方认出来(如实降级)
	} {
		if !strings.Contains(src, want) {
			t.Errorf("资产阶段缺少接线 %q —— 开关/告警会变成「接了但没生效」", want)
		}
	}
	// 降级文案必须用 qc 侧那一份(单一来源,不许各写一份)
	if !strings.Contains(src, "qc.ProportionSkipNoCapability") {
		t.Error("如实降级文案必须取自 qc 侧(单一来源)")
	}
	// 「单资产重新生成」那条出图路径也必须量一遍 —— 偏离告警里那句"建议重出该卡"
	// 只有配上"重出后再量一遍"才闭环,否则用户按建议重出了却没有任何反馈。
	coord := readSource(t, "coordinator.go")
	for _, want := range []string{
		"c.checkPortraitProportion(ctx, proj, ek, prompt, name, path)",
		"qc.ProportionSkipNoCapability",
	} {
		if !strings.Contains(coord, want) {
			t.Errorf("单资产重新生成路径缺少接线 %q —— 重出后不再量,建议就成了死路", want)
		}
	}
}

// assetsStageSource 读资产阶段源码(源码级闸门用)。
func assetsStageSource(t *testing.T) string {
	t.Helper()
	return readSource(t, "stages_assets.go")
}

// readSource 读本包某个源文件(源码级闸门用)。
func readSource(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// 参数三态:缺键(存量项目)= 开,显式关才是关;出厂默认必须是开。
func TestProportionCheckParamIsThreeState(t *testing.T) {
	var zero project.QCParams
	if !zero.ProportionCheckOn() {
		t.Error("缺键(存量项目 json 里没有这个键)必须读成开 —— 否则这条监控在最需要它的老项目上一次都不跑")
	}
	off := false
	if (project.QCParams{EnableProportionCheck: &off}).ProportionCheckOn() {
		t.Error("显式关掉必须是关")
	}
	on := true
	if !(project.QCParams{EnableProportionCheck: &on}).ProportionCheckOn() {
		t.Error("显式打开必须是开")
	}
	if !project.DefaultStageParams().QC.ProportionCheckOn() {
		t.Error("出厂默认必须是开(不默认开就又要靠肉眼盯「丑不丑」)")
	}
}

// 朝向的接线闸门:定妆照出成四分之三侧脸 → **换种子重出**(2026-09-27 真机实测的处置)。
//
// 为什么必须源码级钉住:这条链要真 ComfyUI + 真视觉模型才能跑,而它防的是一个**静默**
// 的坑 —— 定妆照的种子按资产名锚定("同名必同图"),所以一次采样偏成侧脸,那个角色
// 就**一直是侧脸**,重跑资产阶段也不会自愈;而框住朝向的提示词压不住模型(前置强调
// 做过 A/B,结果完全一样)。任何一处被删掉,这条就红。
func TestFacingRegenIsWired(t *testing.T) {
	src := assetsStageSource(t)
	for _, want := range []string{
		"qc.FacingNeedsRegen(",                                // 判据只有一处实现(在 qc 侧)
		"c.regenerateOnBadFacing(",                            // 出图后按朝向下处置
		"rep := checkProportion(ek, imagePrompt, name, path)", // 自检结果必须被接住(丢掉就等于没测)
		"maxFacingRegen",                                      // 次数有上限(不然就是无限烧 GPU)
		// 重出必须**换种子**:同种子重出 = 同一张图。
		// 锚定键 2026-09-28 从显示名改成 `cardSeedKey(id, name)`（有 id 用 id）：名字一漂
		// 种子就变、"同名必同图"静默失效，见 B6。这里跟着改成新表达式。
		"nameSeed(kind, cardSeedKey(id, name)) + int64(attempt)*101",
		"a.Fingerprint = project.FingerprintFile(next)", // 重出写同一路径 → 指纹必须刷新
	} {
		if !strings.Contains(src, want) {
			t.Errorf("资产阶段缺少朝向重出接线 %q —— 侧脸定妆照会一直存在且没人知道", want)
		}
	}
	// 「重新生成」那条路**不自动重出**(那是用户手动换一张,连抽两三次会把一次点击
	// 变成不知在等什么),但必须如实告警
	coord := readSource(t, "coordinator.go")
	if !strings.Contains(coord, "qc.FacingNeedsRegen(rep)") {
		t.Error("单资产重新生成后必须如实告知「这张不是正面」——否则用户按了重新生成,却不知道有没有好")
	}
	if strings.Contains(coord, "regenerateOnBadFacing") {
		t.Error("单资产重新生成不该自动重出:它本来就是用户手动换一张(随机种子),再自动连抽会让一次点击变成静默等待")
	}
}
