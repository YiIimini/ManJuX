package pipeline

import "testing"

// 转场时长三级取值的穷举测试(审计 P1-1)。
//
// 守的是"设置页那个数字到底算不算数":合成阶段原先写死 0.5,用户在设置里把它改成
// 1.0 之后成片一模一样 —— 而且不会有任何提示。取值顺序刻意做成"项目参数 > 设置页 >
// 兜底",这样"我想让这一集慢一点"和"我这台机器都喜欢慢一点"都能表达。
func TestResolveFadeSec(t *testing.T) {
	cases := []struct {
		name            string
		project, global float64
		want            float64
	}{
		{"项目指定就用项目", 1.2, 0.5, 1.2},
		{"项目没指定用设置页", 0, 1.0, 1.0},
		{"两边都没给用兜底 0.5", 0, 0, 0.5},
		{"项目显式 0.1(合法最小值)不被当成未设置", 0.1, 1.0, 0.1},
		{"负值视为未设置", -1, 0.8, 0.8},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveFadeSec(c.project, c.global); got != c.want {
				t.Fatalf("resolveFadeSec(%.2f, %.2f) = %.2f,期望 %.2f", c.project, c.global, got, c.want)
			}
		})
	}
}
