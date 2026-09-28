package project

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// 文档里的关键数值必须与**代码常量同源**（2026-09-29 数据完整性审计 #7/#8/#9）。
//
// ## 为什么值得一道门
//
// `docs/渲染脚本契约.md` 是**双侧共同规则**（文档自己写着"同一张表，两侧各跑一遍"）：
// 技能端按它判"台词装得下"、平台按同一批常量判。两边数字不同就会算出**不同结论** ——
// 实测过的那组是台词语速：文档写 4.5 字/秒、代码 2026-09-28 已按 H3 官方契约校正成 4.1，
// 于是 39 字台词技能端算 9 秒、平台算 10 秒（平台自动抬时长并报"建议拆镜"，技能端认为合规）。
//
// 同类还有两处：契约文档的版本号停在 v1 而代码 `PlanContractVersion = 2`
// （照文档示例产出的包会在导入时被 400 拒收）；产物文件名文档写 `NNN.mp4`
// 而实际是 `shot_NNN.mp4`（外部按契约去找会读成"缺失"）。
//
// ## 门怎么判
//
// 只查**已经漂过一次的那几个字面量**，不做通用解析 —— 通用解析要么太脆（改个措辞就红）、
// 要么太松（漏掉真漂移）。这里列的是"写错就会让两边结论分叉"的那些。
func TestDocsMatchCodeConstants(t *testing.T) {
	root := repoRoot(t)
	// **只查"平台自己声明口径"的两份**：
	//
	//	· `渲染脚本契约.md` —— 双侧共同规则，数字必须与常量同源；
	//	· `使用手册.md` —— 教用户怎么算，写错了用户按错的算。
	//
	// **刻意不查** `口径对账` 与 `漫剧制作口径总表`：那两份里的「3.5~4.5 字/秒」是
	// **引用用户参考库的原话**（`口径对账` 那一列的表头就是"用户参考库口径"），
	// 属于"来源引用"而不是"平台常量" —— 把它们改掉反而是篡改出处。
	// （这条是门抓出来的：它第一次跑就报了那两处，逐条读原文才分清楚。）
	docs := []string{
		"docs/渲染脚本契约.md",
		"docs/使用手册.md",
	}

	// 每条：文档里不该出现的旧值 → 说明它现在该是什么
	type stale struct {
		pattern *regexp.Regexp
		why     string
	}
	stales := []stale{
		{regexp.MustCompile(`4\.5\s*字/秒`),
			"台词语速已按 H3 官方契约校正为 " + strconv.FormatFloat(PlanCharsPerSecond, 'g', -1, 64) + " 字/秒（PlanCharsPerSecond）"},
		{regexp.MustCompile(`PlanCharsPerSecond\s*=\s*4\.5`),
			"同上：常量真源是 " + strconv.FormatFloat(PlanCharsPerSecond, 'g', -1, 64)},
		{regexp.MustCompile(`manjux\.plan v1`),
			"契约版本真源是 PlanContractVersion = " + strconv.Itoa(PlanContractVersion)},
		{regexp.MustCompile("`plan\\.json` schema v1"),
			"同上"},
		{regexp.MustCompile(`产物文件名 NNN\.mp4`),
			"实际产物名是 shot_NNN.mp4（见 stages_render 的 outName）"},
		{regexp.MustCompile(`平台产出（NNN\.mp4`),
			"同上"},
	}

	for _, d := range docs {
		raw, err := os.ReadFile(filepath.Join(root, d))
		if err != nil {
			t.Fatalf("读不到 %s（路径变了要同步本测试）: %v", d, err)
		}
		text := string(raw)
		for _, s := range stales {
			if loc := s.pattern.FindStringIndex(text); loc != nil {
				// 报出行号，方便直接去改
				line := strings.Count(text[:loc[0]], "\n") + 1
				t.Errorf("%s:%d 还写着过期值「%s」—— %s。\n"+
					"这份契约是**双侧各跑一遍**的：两边数字不同会让技能端与平台对同一份方案\n"+
					"算出不同结论（实测过：39 字台词一边算 9 秒、一边算 10 秒）。",
					d, line, text[loc[0]:loc[1]], s.why)
			}
		}
	}

	// 反向确认：改完之后文档里**应当**出现的新值确实写着（防止"只删旧的没写新的"）
	contract, err := os.ReadFile(filepath.Join(root, "docs/渲染脚本契约.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contract), "v"+strconv.Itoa(PlanContractVersion)) {
		t.Errorf("契约文档里应当出现当前版本号 v%d", PlanContractVersion)
	}
}

// repoRoot 向上找有 go.mod 的那层。
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("找不到仓库根")
	return ""
}
