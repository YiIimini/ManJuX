package comfyengine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// H3 插件节点类名"只有一张表"闸门(审计 P1-2)。
//
// 起因:preflight.go 里有一张节点常量表,而**图构造用的是字面量** —— 那张表在
// "体检"这一侧被引用,在图构造那一侧是死表。升级/改名 H3 插件时的症状很难查:
// 体检说"节点在"(它查的是常量),图里提交的是另一个名字 → ComfyUI 报未知节点,
// 用户看到的是原始报文,而代码两边看起来都对。
//
// 判据:非测试的 comfyengine 源码里,除常量定义处(preflight.go)之外不得再出现
// H3 插件节点的字面量类名。新加节点先在这里定义常量,图与体检都用它。
var pluginNodeLiterals = []string{
	"MiniMaxH3ReferenceToVideo",
	"MiniMaxH3ImageToVideo",
	"MiniMaxH3SigmaShift",
	"EmptyMiniMaxH3LatentAV",
	"MiniMaxH3CondSave",
	"MiniMaxH3CondLoad",
	"MiniMaxH3MotionContext",
	"MiniMaxH3MotionContextLoadLatent",
	"MiniMaxH3MotionContextSaveLatent",
	"MiniMaxH3MotionContextTrim",
}

func TestPluginNodeNamesComeFromOneTable(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	var offenders []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "preflight.go" {
			continue // 常量定义处允许出现字面量;测试里可以写期望值
		}
		raw, rerr := os.ReadFile(f)
		if rerr != nil {
			t.Fatal(rerr)
		}
		scanned++
		for i, ln := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(ln)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, lit := range pluginNodeLiterals {
				if strings.Contains(ln, `"`+lit+`"`) {
					offenders = append(offenders, f+":"+strconv.Itoa(i+1)+"  字面量 "+lit)
				}
			}
		}
	}
	if scanned == 0 {
		t.Fatal("没扫到任何源码文件 —— 闸门失效")
	}
	if len(offenders) > 0 {
		t.Errorf("图构造与体检必须共用同一批节点常量(preflight.go),不要在别处写字面量:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
