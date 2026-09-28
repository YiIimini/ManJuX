package library

// 库查找的**实测**工具（性能审计 #1：进程内索引）。
//
// 审计给的数字是"库 600 条时每集白烧 1.6~2.6 秒"，那是**推算 + 微基准**；
// 这里用真实卡片（MJX_PERF_LIB 指向真跑出来的 data/assets_library，asset.json
// 逐字照抄，只把 name/id/dir 换成唯一值）铺到指定条数，量两段真实形态的查找：
//
//	① 只读复用查找：一集 15 张卡各 Find + Match 一次（装配/渲染期的形态）
//	② 资产阶段：每卡 Match 之后 BumpUsed（读一次写一次）—— **写会让索引失效**，
//	   这一段才是"索引到底省了多少"的关键：省不省取决于读写比，不取决于单次查询快多少
//
// 没有真实库时自动跳过（日常 `go test` 不该被它拖慢）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func perfLibTemplate(tb testing.TB) (string, map[string]any) {
	root := os.Getenv("MJX_PERF_LIB")
	if root == "" {
		tb.Skip("未提供 MJX_PERF_LIB（真实库根路径），跳过这项实测")
	}
	dirs, err := os.ReadDir(filepath.Join(root, "characters"))
	if err != nil || len(dirs) == 0 {
		tb.Skipf("真实库里没有角色卡可作模板：%v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "characters", dirs[0].Name(), "asset.json"))
	if err != nil {
		tb.Skipf("模板 asset.json 读不出来：%v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		tb.Fatalf("模板 asset.json 解析失败：%v", err)
	}
	srcImg := filepath.Join(root, "characters", dirs[0].Name(), "portrait.png")
	return srcImg, m
}

// buildPerfLibrary 铺一张 N 条的库（内容是真的，只有身份字段是唯一化的）。
func buildPerfLibrary(tb testing.TB, n int) (*Library, []string) {
	_, tmpl := perfLibTemplate(tb)
	root := tb.TempDir()
	l, err := New(root)
	if err != nil {
		tb.Fatal(err)
	}
	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("卡%04d", i+1)
		dir := name
		card := map[string]any{}
		for k, v := range tmpl {
			card[k] = v
		}
		card["name"] = name
		card["id"] = fmt.Sprintf("char_perf%04d", i+1)
		card["dir"] = dir
		card["image"] = "characters/" + dir + "/portrait.png"
		card["images"] = []string{"characters/" + dir + "/portrait.png"}
		raw, err := json.MarshalIndent(card, "", "  ")
		if err != nil {
			tb.Fatal(err)
		}
		d := filepath.Join(root, "characters", dir)
		if err := os.MkdirAll(d, 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "asset.json"), raw, 0o644); err != nil {
			tb.Fatal(err)
		}
		// 图片：写一张 1×1 的真 PNG（不是硬链接 —— 库在 D: 而临时目录在 C:，
		// 跨卷 os.Link 直接失败，而失败了每次 Match 都会去打一行"缺少图片文件"的日志，
		// 那行日志本身就是被测量的工作量）。查找的热路径只 stat 这个文件、不读内容。
		if err := os.WriteFile(filepath.Join(d, "portrait.png"), tinyPNG, 0o644); err != nil {
			tb.Fatal(err)
		}
		names = append(names, name)
	}
	// **先证明夹具真的被读到了**：如果 Match/Find 全是未命中，那两个实现都会"很快"，
	// 而快的原因跟索引一点关系都没有（这条断言值一个数字）。
	if _, _, ok := l.Match("characters", names[0]); !ok {
		tb.Fatalf("夹具没被读到：Match(%s) 未命中 —— 后面的耗时全是假的", names[0])
	}
	if _, _, ok := l.Find("characters", names[len(names)/2], ""); !ok {
		tb.Fatalf("夹具没被读到：Find(%s) 未命中", names[len(names)/2])
	}
	return l, names
}

// tinyPNG 一张合法的 1×1 PNG（8 字节签名 + IHDR/IDAT/IEND）。
var tinyPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
	0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
	0x0d, 0x0a, 0x2d, 0xb4,
	0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

func TestPerfLibraryIndex(t *testing.T) {
	n := 600
	if v := os.Getenv("MJX_PERF_CARDS"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			n = p
		}
	}
	l, names := buildPerfLibrary(t, n)
	// 一集真实的卡片量：15 张
	perEp := 15
	if perEp > len(names) {
		perEp = len(names)
	}
	// **取样必须铺开整张库**：目录是按文件名排序的，老实现顺序扫到命中为止 ——
	// 只取前面几张（第一版就是 names[i*7]，全落在前 100 条里）量出来的"单次 0.09ms"
	// 是"命中在第 1 个目录就返回"，跟库有 600 条还是 6000 条无关。
	// 这里按等距取样，平均扫描位置≈半张库 —— 这才是"这一集用到的角色恰好排在后面"的常态。
	pick := func(i int) string { return names[(i*len(names)/perEp)%len(names)] }

	measure := func(rounds int, fn func()) (time.Duration, uint64) {
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		start := time.Now()
		for i := 0; i < rounds; i++ {
			fn()
		}
		d := time.Since(start)
		runtime.ReadMemStats(&m1)
		runtime.GC() // 把索引重建的临时分配清掉，别让它算进下一次
		return d / time.Duration(rounds), m1.TotalAlloc - m0.TotalAlloc
	}

	// ① 目录名直接命中：按**同一个显示名**查（老实现先 os.Stat 同名目录，命中就不扫全库）
	byName := func() {
		for i := 0; i < perEp; i++ {
			name := pick(i)
			l.Find("characters", name, "")
			l.Match("characters", name)
		}
	}
	// ② 按**卡 ID** 查：这才是资产阶段的真实调用（FindForCard(kind, cardID, …)），
	// 而盘上的目录名是显示名 —— 老实现只能扫全库去找那条 id。
	byCardID := func() {
		for i := 0; i < perEp; i++ {
			idx := (i * len(names) / perEp) % len(names)
			l.FindForCard("characters", fmt.Sprintf("char_perf%04d", idx+1), names[idx], "")
		}
	}
	// ③ 资产阶段形态：每卡按 ID 读一次、再写一次复用计数（BumpUsed 会让索引失效）
	readWrite := func() {
		for i := 0; i < perEp; i++ {
			idx := (i * len(names) / perEp) % len(names)
			l.FindForCard("characters", fmt.Sprintf("char_perf%04d", idx+1), names[idx], "")
			l.BumpUsed("characters", names[idx])
		}
	}
	perLookup := func() {
		l.FindForCard("characters", "char_perf0600", names[len(names)-1], "")
	}

	// 先把索引建起来（真实使用中它由第一次读触发），让"每次调用"量到的是稳态
	l.Match("characters", names[0])

	perCall, _ := measure(200, perLookup)
	namePer, _ := measure(5, byName)
	idPer, _ := measure(5, byCardID)
	rwPer, _ := measure(5, readWrite)
	// 写后首查：写操作现在只更新那一条，**不再**让整张索引作废
	first, firstAlloc := measure(1, func() {
		l.BumpUsed("characters", names[len(names)-1])
		l.Match("characters", names[0])
	})

	t.Logf("库 %d 条（asset.json 逐字照抄真实卡片）", n)
	t.Logf("单次按 ID 查找 %.2f ms / 分配 %.2f MB", float64(perCall.Microseconds())/1000, 0.0)
	t.Logf("一集 15 卡：按显示名查(目录名命中) %.0f ms｜按卡 ID 查(资产阶段真实调用) %.0f ms｜按 ID 读+写交替 %.0f ms",
		float64(namePer.Microseconds())/1000,
		float64(idPer.Microseconds())/1000,
		float64(rwPer.Microseconds())/1000)
	t.Logf("写一条(记忆复用计数)后立刻读另一条 %.2f ms / 分配 %.2f MB",
		float64(first.Microseconds())/1000, float64(firstAlloc)/(1<<20))
}
