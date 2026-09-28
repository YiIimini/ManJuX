// Command gatecat 生成《门目录》（docs/门目录.md）。用法：
//
//	go run ./tools/gatecat
package main

import (
	"fmt"
	"os"

	"github.com/YiIimini/ManJuX/internal/gatecat"
)

func main() {
	// srcs 扫哪些源文件里的门 —— **从 gatecat.DefaultSources 取**（唯一出处）。
	//
	// 【2026-09-29】这里原来是写死的单个 `plan_contract.go` —— 而 `ValidatePlan` 拆分之后
	// （代码健康审计 P2-11），判据搬到了 `plan_checks.go`，这个工具当场从"106 道门"变成
	// "3 道门"，而且**没有任何测试因此变红**（是 `TestCatalogueCoversRealGates` 兜住的
	// —— 那条门正是为这种情况写的）。现在清单只有一份，工具与测试共用。
	srcs := gatecat.DefaultSources
	const out = "docs/门目录.md"
	if err := gatecat.Write(out, srcs...); err != nil {
		fmt.Fprintln(os.Stderr, "生成失败:", err)
		os.Exit(1)
	}
	gates, _ := gatecat.Collect(srcs...)
	fails := 0
	for _, g := range gates {
		if g.Level == "fail" {
			fails++
		}
	}
	fmt.Printf("门目录已生成:%s（共 %d 道：structural %d / craft %d）\n", out, len(gates), fails, len(gates)-fails)
}
