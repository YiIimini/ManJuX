package server

import (
	"os"
	"strings"
	"testing"
)

// deep_fixes_wiring_test.go 2026-10-08 深度排查里几条"失败路径必须如实说"的接线闸门。
//
// 为什么用源码断言而不是行为夹具：这几条都要求**制造一个删除失败/写盘失败**（目录被占用、
// 磁盘满），在本机测试里要么依赖平台（Windows 独占句柄）要么只能注入故障 —— 而它们要防的
// 是"代码被改回静默"这一类回归（真正的行为验证在 cleanup_skipped_test.go 里做了一条）。
func TestFailurePathsAreReportedNotSwallowed(t *testing.T) {
	cases := []struct {
		file string
		want []struct{ needle, why string }
	}{
		{"novels_detail_import.go", []struct{ needle, why string }{
			{"rollback :=", "回滚要收成一个入口（否则每处各写一遍、漏一处就又静默了）"},
			{`stubborn = append(stubborn, fmt.Sprintf("%s(%v)", id, err))`, "删不掉的项目要**逐条点名**，不许再说\"不留半批\""},
			{"回滚时**有 %d 个项目删不掉**", "报警文必须如实（假报\"不留半批\"比不报更糟）"},
			{`rollback("写入方案失败：" + err.Error())`, "SavePlan 失败也要回滚（否则作品库里多出一批没有方案的空集）"},
		}},
		{"novels_split.go", []struct{ needle, why string }{
			{`stubborn = append(stubborn, fmt.Sprintf("%s(%v)", c.id, err))`, "补缺删集失败要逐条点名（原来静默返回 false、流程照报\"拆集完成\"）"},
			{"本次自动补缺没做成", "一个都没删掉时必须说清\"补缺没做成\"（缺章仍在）"},
		}},
		{"cleanup.go", []struct{ needle, why string }{
			{"if busy {", "⑦ 转身视频中间目录也要有 busy 判据（资产阶段正在抽帧时不能删）"},
			{"转身视频中间目录（*_views）：**本机还有管线在跑**", "跳过时要写进 Skipped 说明原因"},
			{`系统临时文件 %s：删不掉(%v)`, "删不掉要如实说，不许把字节数算进释放量"},
			{"引擎日志 logs/comfyui.log 已归档", "引擎日志要**先归档再清**（原地截断会把渲染排障的第一现场毁掉）"},
			{"引擎日志 logs/comfyui.log 归档失败", "归档失败也要如实说（并说明没动它）"},
			{"运行日志未清理:本次会话没拿到日志写入器", "日志写入器没注入时要如实说这一项没做"},
		}},
	}
	for _, c := range cases {
		src := serverSrc(t, c.file)
		for _, w := range c.want {
			if !strings.Contains(src, w.needle) {
				t.Errorf("%s 缺 %q —— %s", c.file, w.needle, w.why)
			}
		}
	}
}

// TestCleanupNoLongerSwallowsRemoveErrors 清理路径里不许再有"删了不管结果、紧接着记账"的写法。
//
// 判据是**行为契约**：`_ = os.RemoveAll(...)` 后紧跟 add(...)/计数自增 = 虚报释放量。
func TestCleanupNoLongerSwallowsRemoveErrors(t *testing.T) {
	src := serverSrc(t, "cleanup.go")
	lines := strings.Split(src, "\n")
	for i, ln := range lines {
		if !strings.Contains(ln, "_ = os.RemoveAll(") {
			continue
		}
		for j := i + 1; j < len(lines) && j <= i+3; j++ {
			next := strings.TrimSpace(lines[j])
			if strings.HasPrefix(next, "add(") || strings.HasSuffix(next, "++") {
				t.Errorf("第 %d 行删了不管结果、紧接着就记账：\n\t%s\n\t%s", i+1, strings.TrimSpace(ln), next)
			}
		}
	}
}

// serverSrc 读 internal/server 下的源文件（本测试文件所在目录）。
func serverSrc(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读不到 %s：%v", name, err)
	}
	return string(raw)
}
