package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 主题覆盖闸门 —— "换了主题,这块 UI 还是灰的/还是旧色" 这类回归,靠人记不住。
//
// 起因:2026-09-24 用户连着两次报障(先"数据列表卡片没体现主题配色",再
// "表单元素也要同步突出主题配色"),每次都是我漏一处、他提一次。**能被提醒两次
// 就是流程问题**,所以把"UI 颜色必须可追溯到主题"变成闸门:
//
// 规则:frontend/src 下(排除纯色板数据 themes.js 与 wailsjs 生成码)出现的每个
// 颜色字面量,必须属于以下之一,否则测试失败:
//
//	① 主题变量兜底   —— `var(--mx-x, #hex)` 里的 #hex(首屏用,由
//	                    TestRootMirrorsDefaultTheme 保证与默认主题同值)
//	② style.css 的 :root 调色板定义(同上,有专门测试守着)
//	③ 注释里提到的历史色值(说明性文字,不参与渲染)
//	④ 白名单条目     —— 必须写明理由,只允许三类见下
//
// 白名单只承认三类,且写了理由才算数:
//
//	[光]   纯白/纯黑的高光与投影:inset 高光、drop shadow、顶部反光、暗角、点阵纹理。
//	       它们是"光"不是"色",换成主题色反而会把材质弄脏。
//	[平台] 平台约定:Windows 关闭键红 #e81123、视频信箱纯黑 #000、应用图标 SVG 配色。
//	[插图] 非 UI 色:画风预设色板(它是"内容"的一部分,不该跟着皮肤变)。
//
// 交互态(hover/active/focus/选中)一律 --mx-hover*;语义态(成功/警告/失败)
// 一律 color-mix(var(--mx-green|yellow|red) …);面(面板/卡片/轨道/底盘/斑马纹)
// 一律 --mx-panel-bg / --mx-hover-soft / --mx-line / --mx-input-bg。这四句是
// 判定标准,改代码时按它走就不会碰壁。

type colorAllow struct {
	literal string
	reason  string
}

const (
	reasonLight    = "[光] 高光/投影/反光/暗角/纹理:属于「光」不属于「色」,跟随主题上色会弄脏材质"
	reasonPlatform = "[平台] 平台约定色(Windows 关闭键红 / 视频信箱纯黑 / 应用图标)"
	reasonArt      = "[插图] 画风预设色板:是「内容」不是 UI 皮肤,不该跟着主题变"
)

// colorAllowlist 是**已经确认无害**的裸色字面量。新增一处就必须在这里写一行理由 ——
// 写不出理由,基本就说明那处该改成 var(--mx-*)。
var colorAllowlist = map[string][]colorAllow{
	"style.css": {
		{"rgba(255, 255, 255, .075)", reasonLight}, // body::after 点阵
		{"rgba(255, 255, 255, .035)", reasonLight}, // body::after 斜纹
		{"rgba(255, 255, 255, .022)", reasonLight}, // .panel 顶部反光
		{"rgba(255, 255, 255, .025)", reasonLight}, // .topbar2 顶部反光
		{"rgba(255, 255, 255, .02)", reasonLight},  // .log-console / .sidebar 顶部反光
		{"rgba(255, 255, 255, .03)", reasonLight},  // 弹窗/主题卡顶部反光
		{"rgba(255, 255, 255, .04)", reasonLight},  // .fa-btn 顶部反光
		{"rgba(255, 255, 255, .07)", reasonLight},  // 玻璃态侧栏/顶栏 inset 高光
		{"rgba(255, 255, 255, .08)", reasonLight},  // 玻璃态顶栏 inset 高光
		{"rgba(255, 255, 255, .10)", reasonLight},  // 玻璃态侧栏 inset 高光
		{"rgba(255, 255, 255, .13)", reasonLight},  // 玻璃态卡片 inset 高光
		{"rgba(255, 255, 255, .18)", reasonLight},  // 主题色卡色块的描边高光
		{"rgba(255,255,255,.2)", reasonLight},      // 画风 chip 色块描边高光
		{"rgba(0, 0, 0, .1)", reasonLight},         // 面板暗角
		{"rgba(0, 0, 0, .12)", reasonLight},        // 卡片底部暗角
		{"rgba(0, 0, 0, .14)", reasonLight},        // 顶栏/浮动按钮暗角
		{"rgba(0, 0, 0, .15)", reasonLight},        // 弹窗暗角
		{"rgba(0, 0, 0, .16)", reasonLight},        // 状态栏暗角
		{"rgba(0, 0, 0, .18)", reasonLight},        // 侧栏暗角
		{"rgba(0, 0, 0, .2)", reasonLight},         // 日志台暗角
		{"rgba(0, 0, 0, .28)", reasonLight},        // 状态栏暗角(下缘)
		{"rgba(0, 0, 0, .35)", reasonLight},        // 悬浮按钮投影
		{"rgba(0, 0, 0, .5)", reasonLight},         // 浮层投影
		{"#fff", reasonPlatform},                   // 关闭键红底上的白图标
		{"#e81123", reasonPlatform},                // Windows 关闭键红
		{"#000", reasonPlatform},                   // 视频信箱
	},
	"App.vue": {
		{"#10141F", reasonPlatform}, // 应用图标底
		{"#FFD08A", reasonPlatform}, // 图标 M
		{"#53D8FF", reasonPlatform}, // 图标 X
		{"#F97A1F", reasonPlatform}, // 图标描边渐变
	},
	"components/ParamsDrawer.vue": {
		{"#f97a1f", reasonArt}, {"#53d8ff", reasonArt},
		{"#ff6b6b", reasonArt}, {"#ffd166", reasonArt},
		{"#93a0bd", reasonArt}, {"#edf1fa", reasonArt},
		{"#3ddc84", reasonArt}, {"#c9d4e8", reasonArt},
		{"#5a6580", reasonArt}, {"#b28bff", reasonArt},
		{"#ff2e88", reasonArt}, {"#8affc1", reasonArt},
		{"#ffb454", reasonArt}, {"#12b886", reasonArt},
		{"#f2f4f8", reasonArt}, {"#33333c", reasonArt},
		{"#ff9ecf", reasonArt}, {"#8a93a8", reasonArt},
	},
	"components/ShotCard.vue":       {{"#000", reasonPlatform}},
	"components/ShotTuneDialog.vue": {{"#000", reasonPlatform}},
}

var (
	reHexColor  = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	reFuncColor = regexp.MustCompile(`\b(?:rgba?|hsla?)\([^)]*\)`)
	rePaletteLn = regexp.MustCompile(`^\s*--mx-[a-z0-9-]+\s*:`)
	reCommentLn = regexp.MustCompile(`^\s*(\*|/\*|//|<!--)`)
)

// TestThemeCoverage 扫描前端源码,拦截"没跟上主题"的裸色。
func TestThemeCoverage(t *testing.T) {
	root := filepath.Join("..", "..", "frontend", "src")
	used := map[string]map[string]bool{} // 记录白名单条目是否真的被用到
	for f := range colorAllowlist {
		used[f] = map[string]bool{}
	}

	type hit struct {
		file, line string
		no         int
		text       string
	}
	var offenders []hit

	err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if info.Name() == "wailsjs" { // Wails 生成的绑定,不归我们管
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".css", ".vue", ".js":
		default:
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if rel == "themes.js" { // 色板真源本身:它就是主题的定义处
			return nil
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		// 注释要连块一起跳过:CSS 注释的续行既不starts with * 也不 starts with /*,
		// 而注释里常提到历史色值(如"原先写死一套蓝灰 #46536f")—— 那是说明文字,不参与渲染。
		inBlockComment := false
		for i, line := range strings.Split(string(b), "\n") {
			if inBlockComment {
				if strings.Contains(line, "*/") {
					inBlockComment = false
				}
				continue
			}
			if reCommentLn.MatchString(line) {
				if strings.Contains(line, "/*") && !strings.Contains(line, "*/") {
					inBlockComment = true
				}
				continue
			}
			if rePaletteLn.MatchString(line) {
				continue // :root 调色板定义(另有测试守着)
			}
			for _, m := range append(reHexColor.FindAllString(line, -1), reFuncColor.FindAllString(line, -1)...) {
				idx := strings.Index(line, m)
				if idx > 0 && strings.Contains(line[:idx], "var(--mx-") {
					continue // 主题变量兜底值
				}
				key := rel
				if strings.HasPrefix(rel, "components/") || strings.HasPrefix(rel, "views/") {
					key = rel // 保持完整相对路径,便于定位
				}
				if entryUsed(key, m, used) {
					continue
				}
				offenders = append(offenders, hit{rel, strings.TrimSpace(line), i + 1, m})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 frontend/src 失败: %v", err)
	}

	for _, o := range offenders {
		t.Errorf("%s:%d 出现未登记的颜色 %q\n    %s\n    → 改成主题派生(--mx-hover* / color-mix(var(--mx-*)…)),"+
			"确实该保留裸色就往 internal/server/theme_coverage_test.go 的 colorAllowlist 加一行并写明理由",
			o.file, o.no, o.text, o.line)
	}

	// 白名单里失效的条目要清掉,否则它会越攒越大、最后没人看
	for f, entries := range colorAllowlist {
		for _, e := range entries {
			if !used[f][e.literal] {
				t.Errorf("白名单条目已失效,请删除:%s 的 %q(%s)", f, e.literal, e.reason)
			}
		}
	}
	if len(offenders) == 0 {
		t.Logf("前端 %d 个文件的颜色全部可追溯到主题变量;白名单豁免 %d 条(全部带理由)",
			countScanned(t, root), countAllowed())
	}
}

func entryUsed(file, literal string, used map[string]map[string]bool) bool {
	if used[file] == nil {
		used[file] = map[string]bool{}
	}
	for _, e := range colorAllowlist[file] {
		if e.literal == literal {
			used[file][literal] = true
			return true
		}
	}
	return false
}

func countAllowed() int {
	n := 0
	for _, es := range colorAllowlist {
		n += len(es)
	}
	return n
}

func countScanned(t *testing.T, root string) int {
	t.Helper()
	n := 0
	_ = filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".css", ".vue", ".js":
			n++
		}
		return nil
	})
	return n
}
