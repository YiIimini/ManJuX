package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 小说详情弹窗的两个新要求(2026-09-26 用户):
// 「小说详情弹窗需要可鼠标拖动大小」「剧本脚本、角色、场景相关内容也需要可查看详情内容」。
//
// 钉的是**结构与接线**,不是观感:拖柄必须真的挂在弹窗上、尺寸真的绑到 el-dialog;
// 四个分页真的存在;剧本分页真的接的是 /api/novels/script(而不是又去跑 LLM)。
func TestNovelDetailDialogResizableAndTabs(t *testing.T) {
	vue := readFrontend(t, "src/views/NovelsView.vue")
	store := readFrontend(t, "src/store.js")

	// ① 尺寸绑定:弹窗宽度不再是字面量,而是 ref
	if !strings.Contains(vue, `:width="dlgW + 'px'"`) {
		t.Error("详情弹窗宽度必须绑到 dlgW(否则拖不动)")
	}
	if strings.Contains(vue, `width="860px"`) {
		t.Error("详情弹窗仍是固定宽度 860px —— 拖动改大小等于没做")
	}
	// ② 拖柄 + 拖动逻辑
	for _, want := range []string{"novel-dlg-grip", "@mousedown=\"startResize\"", "function startResize", "addEventListener('mousemove'"} {
		if !strings.Contains(vue, want) {
			t.Errorf("拖动改大小缺少「%s」", want)
		}
	}
	// ③ 四个分页
	for _, want := range []string{`<el-tab-pane label="正文"`, `<el-tab-pane label="剧本脚本"`, `name="chars"`, `name="scenes"`} {
		if !strings.Contains(vue, want) {
			t.Errorf("详情弹窗缺少分页「%s」(剧本/角色/场景要看详情)", want)
		}
	}
	// ④ 剧本数据来自只读接口(不跑 LLM),并且镜头明细按集点播
	if !strings.Contains(store, "novelScript:") {
		t.Error("store 缺少 novelScript —— 剧本/角色/场景没有数据源")
	}
	if !strings.Contains(vue, "api.novelScript(dlg.value.novel.path, dir)") {
		t.Error("镜头明细必须按集点播(40 集 × 60 镜一次全拉是几百 KB 的白等)")
	}
	// ⑤ 风格:拖柄与条目样式要在 style.css 里,且用主题变量(裸色会被 theme 闸门拦)
	css := readFrontend(t, "src/style.css")
	if !strings.Contains(css, ".novel-dlg-grip") {
		t.Error("style.css 缺少 .novel-dlg-grip(拖柄没有观感就等于没有)")
	}
	if !strings.Contains(css, ".novel-dlg.novel-dlg {") {
		t.Error("novel-dlg 的 position:relative 必须用双类名立住 —— 单写一遍会被 EP 的同权重规则顶掉(封面菜单钮栽过)")
	}
}

// TestNovelScriptEndpointReadsPackages 接口读的是**技能端已产出的脚本包**(不跑 LLM),
// 并且分两次取:汇总(集/角色/场景)+ 单集镜头明细。
func TestNovelScriptEndpointReadsPackages(t *testing.T) {
	s := newTestServer(t)
	book := mkBookWithScript(t, s, "书H", "脚本", 2)

	// 第一次:不带 episode → 集清单 + 角色/场景汇总
	code, out := doReq(t, s, "GET", "/api/novels/script?path="+urlEnc(book), nil)
	if code != 200 {
		t.Fatalf("应 200,实际 %d(%v)", code, out)
	}
	if found, _ := out["found"].(bool); !found {
		t.Fatalf("这本书有脚本包,found 应为 true(%v)", out)
	}
	eps, _ := out["episodes"].([]any)
	if len(eps) != 2 {
		t.Errorf("应列出 2 集,实际 %v", out["episodes"])
	}
	if _, hasShots := out["shots"]; hasShots {
		t.Error("不带 episode 时不该返回镜头明细(响应会白白几百 KB)")
	}
	if _, ok := out["characters"]; !ok {
		t.Error("应返回角色汇总")
	}
	if _, ok := out["scenes"]; !ok {
		t.Error("应返回场景汇总")
	}

	// 第二次:带 episode → 这一集的镜头明细
	first := ""
	if arr, ok := eps[0].(map[string]any); ok {
		first, _ = arr["dir"].(string)
	}
	if first == "" {
		t.Fatalf("集清单里没有 dir(无法点播):%v", out["episodes"])
	}
	code, out2 := doReq(t, s, "GET", "/api/novels/script?path="+urlEnc(book)+"&episode="+urlEnc(first), nil)
	if code != 200 {
		t.Fatalf("取单集明细应 200,实际 %d(%v)", code, out2)
	}
	shots, _ := out2["shots"].([]any)
	if len(shots) == 0 {
		t.Fatalf("单集明细应带镜头列表(%v)", out2)
	}
	if _, hasShots := out2["shots"]; !hasShots {
		t.Error("带 episode 时必须返回 shots")
	}

	// ③ 没有脚本包的书:明确说"没有",而不是 500
	plain := mkBookWithScript(t, s, "书I", "脚本", 0)
	code, out3 := doReq(t, s, "GET", "/api/novels/script?path="+urlEnc(plain), nil)
	if code != 200 {
		t.Fatalf("没有脚本包应 200 + found:false,实际 %d", code)
	}
	if found, _ := out3["found"].(bool); found {
		t.Error("没有脚本包时 found 不该是 true")
	}

	// ④ 越界
	code, _ = doReq(t, s, "GET", "/api/novels/script?path="+urlEnc(t.TempDir()), nil)
	if code != 403 {
		t.Errorf("小说库之外的路径应 403,实际 %d", code)
	}
}

// TestReadPackageIdentityToleratesNumericVersion 技能端 manifest 的 `version` 是**数字 2**,
// 而平台一度把该字段声明成 string —— `json.Unmarshal` 整条失败,**书名也跟着掉进目录名兜底**
// (小说库脚本卡于是显示「求你们骂我·第01集·求你们骂我」,版本永远空)。
// 一个字段类型写错、另一个字段跟着说谎,这条把它钉住。
func TestReadPackageIdentityToleratesNumericVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.json"), []byte(`{"contract":"manjux.plan","version":2,"book":"某书","episode":"第01集"}`))
	book, version := readPackageIdentity(dir)
	if book != "某书" {
		t.Errorf("version 是数字时书名仍须读出,实际 %q(掉进目录名兜底了)", book)
	}
	if version != "2" {
		t.Errorf("数字版本应转成 \"2\",实际 %q", version)
	}
	// 字符串版本照旧
	dir2 := t.TempDir()
	writeFile(t, filepath.Join(dir2, "manifest.json"), []byte(`{"book":"书二","version":"2.1"}`))
	if b, v := readPackageIdentity(dir2); b != "书二" || v != "2.1" {
		t.Errorf("字符串版本应原样读出,实际 %q/%q", b, v)
	}
}

// TestEpNoOf 集号提取:目录名是 `<书名>·第01集·<集标题>` 这类形态。
func TestEpNoOf(t *testing.T) {
	cases := map[string]string{
		"求你们骂我·第01集·求你们骂我": "第01集",
		"书_第12集_某标题":       "第12集",
		"第003集":            "第003集",
		"某书·第5话·开场":        "第5话",
		"没有集号的目录":          "没有集号的目录",
	}
	for in, want := range cases {
		if got := epNoOf(in); got != want {
			t.Errorf("epNoOf(%q) = %q,期望 %q", in, got, want)
		}
	}
}

func readFrontend(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile("../../frontend/" + rel)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", rel, err)
	}
	return string(b)
}

// urlEnc 只处理本用例会出现的字符(中文路径 + 反斜杠 + 冒号),避免为一个参数引入 net/url 的编码面。
func urlEnc(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if r < 128 && (r == '/' || r == ':' || r == '-' || r == '_' || r == '.' || r == '~' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			sb.WriteRune(r)
			continue
		}
		for _, b := range []byte(string(r)) {
			sb.WriteString("%")
			const hex = "0123456789ABCDEF"
			sb.WriteByte(hex[b>>4])
			sb.WriteByte(hex[b&0xF])
		}
	}
	return sb.String()
}
