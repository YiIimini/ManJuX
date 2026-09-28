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
	const src = "internal/project/plan_contract.go"
	const out = "docs/门目录.md"
	if err := gatecat.Write(src, out); err != nil {
		fmt.Fprintln(os.Stderr, "生成失败:", err)
		os.Exit(1)
	}
	gates, _ := gatecat.Collect(src)
	fails := 0
	for _, g := range gates {
		if g.Level == "fail" {
			fails++
		}
	}
	fmt.Printf("门目录已生成:%s（共 %d 道：structural %d / craft %d）\n", out, len(gates), fails, len(gates)-fails)
}
