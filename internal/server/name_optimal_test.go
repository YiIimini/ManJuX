package server

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 名称问题的**平台侧最优解**（2026-09-28 用户定调："名称问题平台侧自行最优解决，
// 技能侧只是输出内容，平台侧抓住重点就行，不必两端严丝合缝"）。
//
// 落地口径：**目录名（ID）走安全名，显示名（Name）保留原始集名** ——
//
//	· 技能侧输出什么名字平台都接得住：不拒收、不要求对方改名
//	· 信息一点不丢：用户在界面上看到的仍是原始集名
//	· 磁盘路径必须安全：这是平台自己的实现细节，不该让技能侧迁就
func TestNameHandlingIsPlatformSideOptimal(t *testing.T) {
	cases := []struct{ raw, wantSafeContains string }{
		{"骂人一处, 现钱五文", "骂人一处"},   // 半角逗号 + 空格
		{"她替他挡账（二）", "她替他挡账（二）"}, // 全角括号：平台**不再改字**
		{"碑上写我死了", "碑上写我死了"},     // 正常名
		{"a:b*c?", "a"}, // 文件系统非法字符一串
	}
	for _, c := range cases {
		got := safeSeries(c.raw)
		if got == "" {
			t.Errorf("safeSeries(%q) 不该为空", c.raw)
		}
		if !project.ValidProjectName(got) {
			t.Errorf("safeSeries(%q) = %q 必须是合法项目名（否则建目录会失败）", c.raw, got)
		}
		if !strings.Contains(got, c.wantSafeContains) {
			t.Errorf("safeSeries(%q) = %q，应保留可读部分 %q", c.raw, got, c.wantSafeContains)
		}
	}
	// 全角标点**必须原样保留**（技能侧规定只准全角，平台改它就是替对方改字）
	for _, raw := range []string{"她替他挡账（二）", "骂人一处，现钱五文", "《书名》·第1集"} {
		if got := safeSeries(raw); got != raw {
			t.Errorf("全角标点不该被改动：%q → %q", raw, got)
		}
	}
}
