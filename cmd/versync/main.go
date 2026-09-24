// Command versync 把三处版本文件统一到同一个版本号(唯一实现点见 internal/version)。
//
// 用法:
//
//	versync -root .            # 以 version.go 为真源同步三处(本来就一致则不写盘)
//	versync -root . -set v1.2.3 # 发布:显式指定版本(可带 -3-gabc 之类后缀,会被剥掉)
//	versync -root . -check      # 只校验是否同源,不一致则退出码 2
//
// 为什么不写在 PowerShell 里:脚本里的正则改写没法被单元测试覆盖,而
// "构建把版本号改回旧版"正是这个盲区里发生的(见 internal/version/sync.go 的说明)。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/YiIimini/ManJuX/internal/version"
)

func main() {
	root := flag.String("root", ".", "仓库根目录")
	set := flag.String("set", "", "要写入的版本(release 注入);留空 = 用 version.go 现值")
	check := flag.Bool("check", false, "只校验三处是否同源")
	flag.Parse()

	if *check {
		if problems := version.Check(*root); len(problems) > 0 {
			for _, p := range problems {
				fmt.Fprintln(os.Stderr, "✕ "+p)
			}
			os.Exit(2)
		}
		cur, _ := version.Current(*root)
		fmt.Println("✔ 三处版本同源:" + cur)
		return
	}

	changed, err := version.Sync(*root, *set)
	if err != nil {
		fmt.Fprintln(os.Stderr, "✕ "+err.Error())
		os.Exit(1)
	}
	cur, _ := version.Current(*root)
	if len(changed) == 0 {
		fmt.Println("版本已一致,无需改动:" + cur)
		return
	}
	for _, f := range changed {
		fmt.Println("-> 版本已同步:" + f)
	}
	fmt.Println("版本:" + cur)
}
