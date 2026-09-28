package gatecat

import (
	"os"
	"strings"
	"testing"
)

// 门目录的两个约定：
//  1. 它必须与源码**一致** —— 手写清单一定会过期（本周就栽过"文档说有、代码里没有"）；
//  2. 它必须真的覆盖住了门 —— 数量太少说明扫描失效（那时测试会静默通过，比红更糟）。
func TestCatalogueMatchesPlanContract(t *testing.T) {
	const src = "../project/plan_contract.go"
	const doc = "../../docs/门目录.md"

	want, err := Render(src)
	if err != nil {
		t.Fatalf("生成目录失败:%v", err)
	}
	got, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("读不到门目录(应先跑 `go run ./tools/gatecat`):%v", err)
	}
	if string(got) != want {
		t.Errorf("门目录已过期 —— 源码里的门改了，但 docs/门目录.md 没重新生成。\n"+
			"跑一次：go run ./tools/gatecat\n（目录 %d 字节，期望 %d 字节）", len(got), len(want))
	}
}

func TestCatalogueCoversRealGates(t *testing.T) {
	gates, err := Collect("../project/plan_contract.go")
	if err != nil {
		t.Fatalf("扫描失败:%v", err)
	}
	// 数量下限：门少了说明 AST 扫描没扫到（静默失效比红更危险）
	if len(gates) < 50 {
		t.Fatalf("只扫到 %d 道门，明显偏少 —— 扫描逻辑可能失效", len(gates))
	}
	for i, g := range gates {
		wantID := "G" + pad(i+1)
		if g.ID != wantID {
			t.Errorf("编号必须按源码顺序连续：第 %d 道应是 %s，实际 %s", i+1, wantID, g.ID)
		}
		// 说明必须有（可以是"见源码"这种诚实兜底），但不许空
		if strings.TrimSpace(g.Where) == "" || strings.TrimSpace(g.What) == "" {
			t.Errorf("%s 的门缺位置或说明（where=%q what=%q）", g.ID, g.Where, g.What)
		}
		if g.Tier != TierOf(g.Level) {
			t.Errorf("%s 的档位与级别不一致：level=%s tier=%s", g.ID, g.Level, g.Tier)
		}
	}
	// 硬门必须真有（全是 warn 说明 fail 分支被改没了）
	fails := 0
	for _, g := range gates {
		if g.Level == "fail" {
			fails++
		}
	}
	if fails < 5 {
		t.Errorf("structural 门只有 %d 道，太少 —— fail 分支可能被改坏", fails)
	}
}

// TestCatalogueExplainsTiers 目录必须自己解释档位口径（免得读者猜）。
func TestCatalogueExplainsTiers(t *testing.T) {
	md, err := Render("../project/plan_contract.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"structural", "craft", "不许进入渲染", "不阻断"} {
		if !strings.Contains(md, want) {
			t.Errorf("目录里缺少档位说明 %q", want)
		}
	}
}

func pad(i int) string {
	s := "000" + itoa(i)
	return s[len(s)-3:]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
