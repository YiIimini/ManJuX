package pipeline

import (
	"strings"
	"testing"
)

// 多视角生成方式的**分流规则**测试。
//
// 为什么值得单独测:分流判错的两种后果都很难在事后发现 ——
//  ① 该走编辑却走了视频:白白多等几分钟,而且视频模型会把角色重画成另一个人,
//     逐帧身份校验正确地拒绝 → 多视角静默拿不到(用户报的正是这个);
//  ② 该走视频却走了编辑:权重没下的人每次都以"模型未配置"失败,连旧的补救都没有。
// 所以规则本身要能被穷举验证,而不是只在真实流水线里偶发暴露。

func TestViewSourceDecision(t *testing.T) {
	cases := []struct {
		name        string
		requested   string
		capable     bool
		missing     []string
		wantSource  string
		wantReason  bool // 是否必须给出"为什么没走编辑"的说明
		reasonMatch string
	}{
		{name: "auto+就绪→编辑", requested: "", capable: true, missing: nil, wantSource: "edit"},
		{name: "auto+引擎不支持→视频", requested: "", capable: false, wantSource: "h3",
			wantReason: true, reasonMatch: "不支持图像编辑"},
		{name: "auto+缺权重→视频", requested: "", capable: true,
			missing: []string{"多视角编辑底模「x」不在 ComfyUI 的模型目录里"}, wantSource: "h3",
			wantReason: true, reasonMatch: "不在 ComfyUI 的模型目录里"},
		{name: "auto+ComfyUI未就绪→视频", requested: "", capable: true,
			missing: []string{"本地 ComfyUI 未就绪"}, wantSource: "h3",
			wantReason: true, reasonMatch: "本地 ComfyUI 未就绪"},
		// 显式指定一律照做:用户要 A/B 或明确不要某条路时不做"自作聪明"的回退。
		{name: "显式edit+缺权重仍走edit", requested: "edit", capable: true,
			missing: []string{"缺件"}, wantSource: "edit"},
		{name: "显式edit+引擎不支持仍走edit", requested: "edit", capable: false, wantSource: "edit"},
		{name: "显式h3", requested: "h3", capable: true, wantSource: "h3"},
		{name: "显式video别名", requested: "video", capable: true, wantSource: "h3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source, reason := viewSourceDecision(tc.requested, tc.capable, tc.missing)
			if source != tc.wantSource {
				t.Fatalf("走哪条路应为 %q,实际 %q(reason=%q)", tc.wantSource, source, reason)
			}
			if tc.wantReason && strings.TrimSpace(reason) == "" {
				t.Fatal("退回视频路径时必须说明原因,不能静默降级")
			}
			if !tc.wantReason && reason != "" {
				t.Errorf("不该有原因说明,实际 %q", reason)
			}
			if tc.reasonMatch != "" && !strings.Contains(reason, tc.reasonMatch) {
				t.Errorf("原因说明应包含 %q,实际 %q", tc.reasonMatch, reason)
			}
		})
	}
}

// TestViewSourceReasonTellsUserHowToUpgrade 退回视频路径的说明必须**可操作**:
// 只说"降级了"等于没说;用户要能从中知道"缺的是哪一项、补上会自动变好"。
func TestViewSourceReasonTellsUserHowToUpgrade(t *testing.T) {
	_, reason := viewSourceDecision("", true, []string{"多视角编辑 VAE:未指定文件名"})
	for _, want := range []string{"未指定文件名", "自动改用编辑路径", "models"} {
		if !strings.Contains(reason, want) {
			t.Errorf("说明里应包含 %q,实际 %q", want, reason)
		}
	}
}
