package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// **关键落盘不许吞错**（2026-09-28 审计 A2–A15 的共同病根）。
//
// 那 14 条里有 13 条是同一个形状：`_ = s.store.SaveXxx(...)` 或 `b, _ := os.ReadFile(...)`
// —— 丢掉错误之后，**流程照跑、日志照报成功**，而事实上什么也没写进去。
// 用户看到的症状永远出现在很远的地方：
//
//	· 源文本没落盘 → 该集"建好了但一跑就废"（A3）
//	· 资产表没落盘 → 续跑时又被当"缺图"补一遍，白烧 GPU（A4）
//	· 清单没落盘   → 重开应用，几十镜全判未渲（A12）
//	· 册子读不出   → 章的内容静默消失，仍报"64 册已读入"（A10）
//
// 这种形状靠人盯不住（写的时候每个都"看起来没问题"），所以用一道**源码级门**钉住：
// 这几类**持久化/读取**调用不许出现在被丢弃的返回值位置。
//
// 允许的例外写在 allowlist 里，每一条都要有理由；有新例外时**先想清楚**再往这里加。
func TestNoSwallowedPersistenceErrors(t *testing.T) {
	// 必须接住错误的调用（出现这些名字就要求返回值被使用）
	calls := []string{
		"SaveAssets(", "SaveManifest(", "SaveSource(", "SaveMeta(", "SavePlan(",
		"SaveProject(", "SaveClips(", "LoadAssets(", "LoadPlan(", "ReadFile(",
	}
	// 例外：**测试与工具**不在此门范围。下面这些是"探测式读取"，失败本身就构成完整语义
	// （不是"该做的事没做成"），加进白名单时都必须写明理由：
	//
	//   · sheetderive.go —— fileReadable 内部：读不出就是"不可用"，本身就是判据；
	//   · library.go —— 读图片文件算指纹/做复制：读不出即"这张图不可用"，调用方按不可用处理；
	//   · frame_asset.go / handlers_asset.go —— `if plan, _ := LoadPlan(id); plan != nil`
	//     是"有方案就取描述、没方案就走另一支"；**读不出与"没方案"确实会混同**，
	//     但那两处的另一支本身已如实报错，且这两个入口是用户显式上传资产的动作，
	//     真有问题时上传本身会失败并报出来。
	allow := []string{
		"sheetderive.go",
		"library.go",
		"frame_asset.go",
		"handlers_asset.go",
		"novels_cover_redo.go", // 读旧封面备份：备份不存在是正常路径（首次做封面）
	}

	root := ".." // internal/project → internal
	var bad []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		base := filepath.Base(path)
		for _, a := range allow {
			if base == a {
				return nil
			}
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for i, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			// 只查"赋值给下划线"的形状
			if !strings.HasPrefix(trimmed, "_ = ") && !strings.Contains(trimmed, ", _ := ") &&
				!strings.Contains(trimmed, ", _ = ") && !strings.HasPrefix(trimmed, "_ =") {
				continue
			}
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, c := range calls {
				if strings.Contains(line, c) {
					bad = append(bad, filepath.ToSlash(path)+":"+itoa(i+1)+"  "+trimmed)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Errorf("这些地方把持久化/读取的错误丢掉了（审计 A2–A15 的病根：流程照跑、日志照报成功）：\n  %s\n"+
			"修法：接住错误并**如实播报**（用户看到的症状永远出现在很远的地方）。"+
			"确有理由时请加进本测试的 allowlist 并写明原因。", strings.Join(bad, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
