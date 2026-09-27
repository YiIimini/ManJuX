package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// novels_script.go 小说库的**第二个人口**:技能端已经出了脚本,平台就直接导包,不再跑 LLM 拆集。
//
// 背景(用户 2026-09-26):"目前是技能侧直出脚本,平台侧在小说目录里解析到脚本后就不用走
// LLM 解析太浪费时间"。契约见 docs/渲染脚本契约.md:技能端出 `manifest.json` + `plan/plan.json`
// (系列根另有 `series.json`),平台端原来的「LLM 直出分镜」整段省掉 —— 单集 2-6 分钟深度推理
// 加 token,全部省掉。
//
// 这一层只做两件事:**在书目录里把包找出来**、**把它交给同一条导入路**
// (importPackageRoot,与 /api/projects/import-package 完全同一套校验与后处理)。
// 不在小说库这一侧另写解析 —— 契约文件只有一份读者。

// bookScript 一本书目录里发现的技能端脚本包。
type bookScript struct {
	Root     string `json:"root"`     // 交给导入器的目录(单集包本身,或含若干集包的系列目录)
	Kind     string `json:"kind"`     // episode=这一本就是单集包 / series=目录下有多集包
	Episodes int    `json:"episodes"` // 找到几个包(界面用它显示"已有脚本 N 集")
	Book     string `json:"book,omitempty"`
	Version  string `json:"version,omitempty"` // 契约版本(与平台 PlanContractVersion 对照)

	// Deprecated 技能侧是否已把这个渲染包目录标为废弃(2026-09-27 起技能侧不再产出渲染包,
	// 只在 `漫剧脚本/` 留一份 `_已废弃-说明.md`)。界面据此把"导入历史包"这件事说清楚 ——
	// 判据用技能侧自己放的说明文件，平台不猜内容。
	Deprecated     bool   `json:"deprecated,omitempty"`
	DeprecatedHint string `json:"deprecated_hint,omitempty"`
}

// scriptPackagesDeprecated 这些目录里有没有技能侧的"已废弃"说明文件。
//
// 判据是**文件的存在性**（`_已废弃*`），不是内容匹配：技能侧换措辞不该让平台这边失效，
// 而不放这个文件 = 没标废弃（老包在停用前就是这个状态，照旧可导）。
func scriptPackagesDeprecated(dirs ...string) (bool, string) {
	for _, d := range dirs {
		if strings.TrimSpace(d) == "" || !dirExists(d) {
			continue
		}
		if m, _ := filepath.Glob(filepath.Join(d, "_已废弃*")); len(m) > 0 {
			return true, "技能侧已把该目录标为废弃（只留查看，不再更新）：渲染输入请走「视频制作」（平台 LLM 直出）"
		}
	}
	return false, ""
}

// findBookScript 在一本书的目录里找技能端脚本包。
//
// 扫描深度**只到书目录下两层**:技能端的 --out 通常是 `<书>/脚本/<集包>` 或直接
// `<书>/<集包>`,再深的目录属于用户自己的组织方式,平台不去猜(unzip 出来的一棵树、
// 备份目录里躺着旧包 —— 扫太深会把"上次的包"当成"这次的")。
//
// 路径必须落在小说库内:书目录来自扫描结果(已经过 inNovelRoot),这里再挡一次 ——
// 这条路上的参数会一路走到写盘,任何一处缺口都可能被构造成写到库外。
func (s *Server) findBookScript(bookPath string) *bookScript {
	if strings.TrimSpace(bookPath) == "" || !s.inNovelRoot(bookPath) {
		return nil
	}
	fi, err := os.Stat(bookPath)
	if err != nil || !fi.IsDir() {
		return nil
	}
	// ① 这一本自己就是个单集包(最直接:build 的 out 就指向书目录)
	if hasPlan(bookPath) {
		bs := &bookScript{Root: bookPath, Kind: "episode", Episodes: 1}
		bs.Book, bs.Version = readPackageIdentity(bookPath)
		return bs
	}
	// ② 书目录(或它的某个一级子目录)下有若干集包 → 系列。
	//
	// **Root 必须是"包的直接父目录"**:导入器(collectPackages)只认"自己就是包"或
	// "一级子目录是包",不像这里能多看一层。检测到 `书/脚本/第01集` 却把 `书` 交给导入器,
	// 就会"检测说有、导入说没有"(2026-09-26 实测踩到)。所以下面按候选目录逐一试,
	// 取包最多的那个当 Root —— 检测与导入从此看同一层。
	candidates := []string{bookPath}
	if ents, err := os.ReadDir(bookPath); err == nil {
		for _, e := range ents {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			candidates = append(candidates, filepath.Join(bookPath, e.Name()))
		}
	}
	bestRoot, bestPkgs := "", []string(nil)
	for _, r := range candidates {
		got, err := collectPackages(r)
		if err != nil || len(got) == 0 {
			continue
		}
		if len(got) > len(bestPkgs) {
			bestRoot, bestPkgs = r, got
		}
	}
	if len(bestPkgs) == 0 {
		return nil
	}
	bestPkgs = dedupStrings(bestPkgs)
	sort.Strings(bestPkgs)
	kind := "series"
	if bestRoot == bookPath && len(bestPkgs) == 1 {
		kind = "episode"
	}
	bs := &bookScript{Root: bestRoot, Kind: kind, Episodes: len(bestPkgs)}
	bs.Book, bs.Version = readPackageIdentity(bestPkgs[0])
	bs.Deprecated, bs.DeprecatedHint = scriptPackagesDeprecated(bestRoot, filepath.Dir(bestRoot))
	return bs
}

// readPackageIdentity 读一个包的身份(书名 / 契约版本)。
//
// manifest.json 读不到时**用目录名当书名兜底**:少一行字而已,不该因此让整本书的脚本检测失败。
//
// 【踩过的坑 · 2026-09-26 真机】原先 `Version` 声明成 `string`,而技能端 manifest 里的
// `version` 是**数字 2** —— `json.Unmarshal` 直接整条失败(类型不符),于是**书名也一起**
// 掉进目录名兜底:小说库的脚本卡上显示的是「求你们骂我·第01集·求你们骂我」,版本永远是空。
// 一个字段类型写错,连带另一个字段说谎 —— 所以这里按 `any` 收,数字/字符串都认。
func readPackageIdentity(pkgDir string) (book, version string) {
	book = filepath.Base(pkgDir)
	raw, err := os.ReadFile(filepath.Join(pkgDir, "manifest.json"))
	if err != nil {
		return book, ""
	}
	var man struct {
		Book    string `json:"book"`
		Version any    `json:"version"`
	}
	if json.Unmarshal(raw, &man) != nil {
		return book, ""
	}
	if strings.TrimSpace(man.Book) != "" {
		book = strings.TrimSpace(man.Book)
	}
	switch v := man.Version.(type) {
	case string:
		version = strings.TrimSpace(v)
	case float64:
		version = strconv.Itoa(int(v))
	}
	return book, version
}

func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// handleNovelProduceScript POST /api/novels/produce-script {path}
//
// path = **书目录**(小说库里那张卡的 path)。找到了包就交给同一条导入路;找不到就明确
// 说清"没找到什么、需要什么文件",而不是回落到 LLM 拆集 —— 用户点的是"脚本创作",
// 悄悄换一条路(几分钟 LLM 推理)是最坏的结果。
func (s *Server) handleNovelProduceScript(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if err := readBody(r, &in); err != nil || strings.TrimSpace(in.Path) == "" {
		writeErr(w, 400, fmt.Errorf("需要 path(小说库里的书目录)"))
		return
	}
	book := strings.TrimSpace(in.Path)
	// ① **新路优先**：技能侧的结构化明细（`分镜/分镜明细.json`）现在就是"可渲染的 plan"
	// （每镜带 `h3_prompt`、引用是 ID、三类卡内嵌）。走它，旧渲染包才真正退成历史。
	if d := loadNovelDetail(book); d != nil {
		eps := detailEpKeys(d.Shots)
		s.opLog("📜 脚本创作:《%s》 → 从 %s 导入(%d 集 / %d 镜)",
			filepath.Base(book), novelShotDetailJSON, len(eps), len(d.Shots))
		if s.importNovelDetail(w, book, d) {
			return
		}
	}
	// ② 老路：旧的渲染脚本包（没明细时才走；入口上会标「历史包」）
	bs := s.findBookScript(book)
	if bs == nil {
		writeErr(w, 400, fmt.Errorf(
			"《%s》目录里没有技能端渲染脚本包:需要 manifest.json + plan/plan.json(系列根另有 series.json)。"+
				"注意技能侧自 2026-09-27 起**默认不再产出该包**(manju_plan.py build 会拒绝执行,要考古加 --legacy-force)；"+
				"正常渲染请走「视频制作」——平台读小说正文 LLM 直出分镜与每镜 H3 提示词",
			filepath.Base(book)))
		return
	}
	if bs.Deprecated {
		s.opLog("⚠ 《%s》的渲染包已被技能侧标为废弃:%s —— 仍按你的点击导入(%s, %d 集)",
			orDefault(bs.Book, filepath.Base(book)), bs.DeprecatedHint, bs.Root, bs.Episodes)
	}
	prefix := orDefault(bs.Book, filepath.Base(book))
	s.opLog("📜 脚本创作:《%s》 → 导入 %s(%d 集,契约 %s)",
		prefix, bs.Root, bs.Episodes, orDefault(bs.Version, "?"))
	s.importPackageRoot(w, bs.Root)
	// 导入只是"把脚本变成项目"。用户点这个入口的意图是**开始生产**,而自动开跑长在
	// importPackageRoot(导入本体)里 —— 所以两个入口(项目包导入 / 脚本创作)行为完全一致:
	// 导完就起产线。这里**不再自己起一次**,避免同一本书被启动两遍。
}
