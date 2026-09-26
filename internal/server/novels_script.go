package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	return bs
}

// readPackageIdentity 读一个包的身份(书名 / 契约版本)。
//
// manifest.json 读不到时**用目录名当书名兜底**:书目录名就是书名(扫描器也是这么取名的),
// 少一行字而已,不该因此让整本书的脚本检测失败。契约版本读不到就留空。
func readPackageIdentity(pkgDir string) (book, version string) {
	book = filepath.Base(pkgDir)
	raw, err := os.ReadFile(filepath.Join(pkgDir, "manifest.json"))
	if err != nil {
		return book, ""
	}
	var man struct {
		Book    string `json:"book"`
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &man) != nil {
		return book, ""
	}
	if strings.TrimSpace(man.Book) != "" {
		book = strings.TrimSpace(man.Book)
	}
	return book, strings.TrimSpace(man.Version)
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
	bs := s.findBookScript(book)
	if bs == nil {
		writeErr(w, 400, fmt.Errorf(
			"《%s》目录里没有技能端脚本包:需要 manifest.json + plan/plan.json(系列根另有 series.json)。"+
				"先在技能端跑 manju_plan.py build --out <书目录>/脚本,再回来点这里",
			filepath.Base(book)))
		return
	}
	s.opLog("📜 脚本创作:%s → 导入 %s(%d 集,契约 %s)",
		filepath.Base(book), bs.Root, bs.Episodes, orDefault(bs.Version, "?"))
	s.importPackageRoot(w, bs.Root)
}
