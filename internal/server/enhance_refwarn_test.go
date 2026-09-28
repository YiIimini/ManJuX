package server

import (
	"strings"
	"testing"
)

// 增强入口的"参考图变少"告警(甲5,2026-09-26 渲染管线审查)。
//
// 回归背景:pipeline.RefPlan 在角色没有定妆照 / 场景没有场景图时不会报错,只会**少给**
// 几张参考图,而两个入口此前把它的 missing 直接丢进 `_` —— 静默降级。少一张图就足以让
// 后续 <Picture> 编号整体前移(LLM 的 subject_definitions 按 shot.Characters 顺序写),
// 于是画面内容与提示词对不上:用户看到的是"这角色不像",而日志里一个字都没有。

// TestMissingRefWarning 纯函数面:缺失非空 → 点名并说清后果;为空 → 完全安静。
func TestMissingRefWarning(t *testing.T) {
	if got := missingRefWarning(nil); got != "" {
		t.Errorf("没有缺失时必须保持安静(否则每次增强都刷一行没用的告警),实际 %q", got)
	}
	if got := missingRefWarning([]string{}); got != "" {
		t.Errorf("空切片同样要保持安静,实际 %q", got)
	}

	cases := []struct {
		name    string
		missing []string
	}{
		{name: "单角色缺定妆照", missing: []string{"char_lin"}},
		{name: "角色与场景都缺", missing: []string{"char_lin", "char_yu", "雨夜老街"}},
	}
	for _, c := range cases {
		got := missingRefWarning(c.missing)
		if got == "" {
			t.Fatalf("%s:必须告警(参考图变少会让身份漂移,而产物本身看不出异常)", c.name)
		}
		// 逐项点名:只说"缺了点东西"用户不知道该补哪张
		for _, item := range c.missing {
			if !strings.Contains(got, item) {
				t.Errorf("%s:告警应点名缺失项 %q,实际:%s", c.name, item, got)
			}
		}
		// 后果与建议都要有:用户得知道为什么要在意、下一步做什么
		for _, want := range []string{"参考图", "身份漂移", "补齐"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s:告警应提到 %q,实际:%s", c.name, want, got)
			}
		}
	}
}

// TestEnhanceEntrypointsWarnOnMissingRefs 两个增强入口都必须消费 RefPlan 的 missing。
//
// 这是一条**类级**回归屏障:同一个"丢弃 missing"的写法在这两个入口各写过一次,
// 只要有人再写成 `_, _ :=`/`roles, _ :=` 丢回来,这条断言立刻红。
func TestEnhanceEntrypointsWarnOnMissingRefs(t *testing.T) {
	src := readServerSource(t, "handlers_enhance.go")

	if n := strings.Count(src, "pipeline.RefPlan("); n != 2 {
		t.Fatalf("增强入口应有两处 RefPlan 调用(局部重绘 / 音频驱动),实际 %d 处 —— "+
			"函数形状变了,这条断言需要跟着更新", n)
	}
	// 两处都必须把 missing 接出来（2026-09-29 起签名多了一个 `overCap`：
	// "参考图太多被裁"与"缺图"方向相反，必须分开报 —— 见审计 P0-3）
	if n := strings.Count(src, "missing, overCap := pipeline.RefPlan("); n != 2 {
		t.Errorf("两处 RefPlan 都必须接住 missing 与 overCap,实际只有 %d 处", n)
	}
	// 两处都必须告警
	if n := strings.Count(src, "missingRefWarning(missing)"); n != 2 {
		t.Errorf("两处入口都要把 missing 交给 missingRefWarning,实际 %d 处", n)
	}
	// **"太多被裁"必须单独报**：混进 missing 会让提示变成"补齐参考图"，
	// 而用户照做只会更超上限（这正是审计 P0-3 那个 bug）。
	if n := strings.Count(src, "for _, w := range overCap {"); n != 2 {
		t.Errorf("两处入口都要单独报 overCap（不能混进 missing）,实际 %d 处", n)
	}
	// 告警只能走这一处实现(避免两处口径漂移)
	if n := strings.Count(src, "func missingRefWarning("); n != 1 {
		t.Errorf("missingRefWarning 只应有一处实现,实际 %d 处", n)
	}

	// 反向断言:丢弃 missing 的旧写法不许再出现
	for _, bad := range []string{
		"roles, _ := pipeline.RefPlan(",
		"refs, roles, _ := pipeline.RefPlan(",
		", _, _ = pipeline.RefPlan(",
	} {
		if strings.Contains(src, bad) {
			t.Errorf("入口里又出现了丢弃 missing 的写法 %q:参考图会静默变少", bad)
		}
	}

	// 告警必须排在 EnsureRefManifest 之前 —— 它要在这一镜**开始之前**说出来,
	// 而不是等编号写进提示词之后(那时已经晚了:编号已经按少的张数编好了)
	for _, sig := range []string{
		"func (s *Server) handleShotMaskFix(",
		"func (s *Server) handleShotAudioDrive(",
	} {
		start := strings.Index(src, sig)
		if start < 0 {
			t.Fatalf("找不到 %s:函数形状变了,这条断言需要跟着更新", sig)
		}
		body := src[start:]
		// 音频驱动是文件里的最后一个 handler;局部重绘截到下一个 handler 注释之前
		if i := strings.Index(body, "// handleShotAudioDrive"); i > 0 {
			body = body[:i]
		}
		warn := strings.Index(body, "missingRefWarning(")
		manifest := strings.Index(body, "EnsureRefManifest(")
		if warn < 0 {
			t.Errorf("%s:必须消费 RefPlan 的 missing", sig)
			continue
		}
		if manifest >= 0 && warn > manifest {
			t.Errorf("%s:告警要排在 EnsureRefManifest 之前(编号写进提示词就晚了)", sig)
		}
	}
}
