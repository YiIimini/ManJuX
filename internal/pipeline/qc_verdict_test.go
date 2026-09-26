package pipeline

import (
	"os"
	"strings"
	"testing"
)

// 质检结论的穷举测试(审计:镜头缺失时仍报「全部合格」)。
//
// 旧实现的判据只有 checked==0 与 failed>0 —— **缺镜既不影响结论也不进文案**,
// 于是一集 20 镜里挂了 5 镜也能得到「✔ 质检通过:15 镜全部合格」和一份干净的
// qc_report.json;用户据此以为这一集没问题,只有全流程跑到合成阶段才会炸。
func TestQCVerdictNeverSaysAllPassWhenShotsAreMissing(t *testing.T) {
	cases := []struct {
		name       string
		total      int
		checked    int
		failed     int
		notChecked []int
		vlmFlagged int
		wantLevel  string
		wantErr    bool
		wantInMsg  []string
	}{
		{
			name: "全部检查过且合格 → success", total: 5, checked: 5,
			wantLevel: "success", wantInMsg: []string{"5/5"},
		},
		{
			name: "部分镜缺产物 → 不许成功,且说清缺了几镜", total: 20, checked: 15,
			notChecked: []int{4, 9, 11, 17, 20},
			wantLevel:  "error", wantErr: true,
			wantInMsg: []string{"15/20", "5 镜", "4、9、11"},
		},
		{
			name: "有不合格 + 有缺镜 → 两个数都要报", total: 10, checked: 8, failed: 2,
			notChecked: []int{3, 6},
			wantLevel:  "error", wantErr: true,
			wantInMsg: []string{"2 个不合格", "2 个镜头尚无产物"},
		},
		{
			name: "只有不合格 → 照旧报错误码与重渲指引", total: 4, checked: 4, failed: 1,
			wantLevel: "error", wantErr: true, wantInMsg: []string{"1 个不合格", "单镜重渲"},
		},
		{
			name: "一镜都没得质检 → 指向渲染阶段", total: 4, checked: 0,
			notChecked: []int{1, 2, 3, 4},
			wantLevel:  "error", wantErr: true, wantInMsg: []string{"渲染出片"},
		},
		{
			name: "全合格但 VLM 有标记 → warn 且不算失败", total: 3, checked: 3, vlmFlagged: 2,
			wantLevel: "warn", wantInMsg: []string{"VLM", "未自动重渲"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := qcVerdictOf(tc.total, tc.checked, tc.failed, tc.notChecked, tc.vlmFlagged)
			if v.Level != tc.wantLevel {
				t.Errorf("结论级别 = %s,期望 %s(文案:%s)", v.Level, tc.wantLevel, v.Msg)
			}
			if (v.Err != nil) != tc.wantErr {
				t.Errorf("Err = %v,期望有无错误 = %v", v.Err, tc.wantErr)
			}
			// 核心不变量:只要有镜没被质检,就绝不允许给出"全部合格"的结论
			if len(tc.notChecked) > 0 {
				if v.Level == "success" {
					t.Fatal("有镜头没被质检却报成功 —— 这正是「报成功其实没做」")
				}
				if !strings.Contains(v.Msg, "未质检") && v.Err == nil {
					t.Errorf("缺镜结论必须写明「未质检」,实际 %q", v.Msg)
				}
			}
			for _, want := range tc.wantInMsg {
				hay := v.Msg
				if v.Err != nil {
					hay += v.Err.Error()
				}
				if !strings.Contains(hay, want) {
					t.Errorf("结论里应包含 %q,实际 文案=%q 错误=%v", want, v.Msg, v.Err)
				}
			}
		})
	}
}

// 缺镜镜号列的排版(过长要折叠,不能把日志刷爆)。
func TestShotIDList(t *testing.T) {
	if got := shotIDList([]int{3, 7, 12}); got != "3、7、12" {
		t.Errorf("普通情况应顿号相连,实际 %q", got)
	}
	long := make([]int, 0, 20)
	for i := 1; i <= 20; i++ {
		long = append(long, i)
	}
	got := shotIDList(long)
	if !strings.Contains(got, "…等 20 个") {
		t.Errorf("超过 12 个应折叠并报总数,实际 %q", got)
	}
	if strings.Count(got, "、") > 12 {
		t.Errorf("折叠后仍在堆镜号:%q", got)
	}
	if shotIDList(nil) != "" {
		t.Errorf("空列表应为空串,实际 %q", shotIDList(nil))
	}
}

// 全流程模式下质检不通过的留痕:不许清成 nil 之后又打「✔ 全流程完成」。
//
// 这里做源码级钉住(execute 需要真实阶段才能跑起来):只要有人把 qcFailed 的留痕
// 逻辑删掉,这条就红。
func TestAllModeQCFailureIsRemembered(t *testing.T) {
	src := coordinatorSource(t)
	for _, want := range []string{
		"qcFailed = err",       // 质检错误被留住
		"if qcFailed != nil {", // 收尾时按它判断
		"ended = ResultFailed", // 批产线据此停住(不自动跳集)
	} {
		if !strings.Contains(src, want) {
			t.Errorf("全流程收尾缺少 %q —— 坏镜拼进成片却仍报「全流程完成」", want)
		}
	}
	if !strings.Contains(src, "if qcFailed != nil {") || !strings.Contains(src, "全流程跑完但**质检未通过**") {
		t.Error("带瑕疵收尾必须如实说明「质检未通过」,不能只留一个被清成 nil 的 error")
	}
}

// coordinatorSource 读 coordinator.go 源码(源码级闸门用)。
func coordinatorSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("coordinator.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
