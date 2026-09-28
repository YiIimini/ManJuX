package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
)

// rescanProjects 重扫项目目录(POST /api/projects/rescan)。
//
// 技能端(ManJuNovel)把成套项目包直接写进 projects/<书名>/<集名>/,而 Store 只在
// 启动时扫一次盘 —— 这个端点让"技能端出脚本 → 平台端点一下就能看见",不必重启应用。
// 只新增、不删除(语义边界见 project.Store.Rescan 注释)。
func (s *Server) rescanProjects(w http.ResponseWriter, r *http.Request) {
	added, total := s.store.Rescan()
	if len(added) > 0 {
		s.opLog("🔄 重扫项目目录:新增 %d 个(技能端产出或手工放入)—— %s",
			len(added), strings.Join(added, "、"))
		for _, id := range added {
			if plan, err := s.store.LoadPlan(id); err == nil {
				s.opLog("    %s:%d 镜 / %d 角色(%d 张造型卡) / %d 场景(方案已就位,可跳过 script 直接渲染)",
					id, len(plan.Shots), project.CharFamilyCount(plan.Characters), len(plan.Characters), len(plan.Scenes))
			} else {
				s.opLog("    %s:尚无 plan.json(需先导入方案或跑分镜阶段)", id)
			}
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "added": added, "total": total})
}

// importPackages 从**技能端产出的项目包目录**导入(POST /api/projects/import-package {path})。
//
// 与 rescan 的分工:
//
//	· rescan  —— 技能端已经把包写进 data/projects/ 时,刷一下索引就能看见(轻,原地);
//	· 本端点 —— 包还在任意位置(技能端的输出目录、别的盘、U 盘)时,**由平台主动拉进来**:
//	  自己拷进 data/projects/<书名>/,自己注册。这样技能端不必知道平台的数据目录布局,
//	  用户也不必先手动搬文件。
//
// 支持两种入参:
//
//	· 单集包(有 plan/plan.json)     → 导入一集;
//	· 系列根(有 series.json + 子目录)→ **整本书一次导入**(逐集校验,逐集汇报)。
//
// 系列根会**按 series.json 对账**:技能端在 series.json 里声明了这本书有几集、
// 每集是哪个目录。声明了 N 集却只找到 M 个包(M<N)= 包没拷全/拷到一半,一律拒收 ——
// "半套系列静默入库"和"半导入状态"是同一类坑:用户以为整本书都进去了,渲染到第 20 集才发现缺。
// 想只导一部分,把 series.json 移开(那时按子目录逐包导入)。
//
// 契约守门与 importPlan 完全一致(同一张规则表 + 同一套后处理):坏包拒收并说明在哪一条;
// 好包导入后与平台自产包等价。**全部先校验再落盘** —— 半导入状态比不导入更难查。
func (s *Server) importPackages(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if err := readBody(r, &in); err != nil || strings.TrimSpace(in.Path) == "" {
		writeErr(w, 400, fmt.Errorf("需要 path(项目包目录或系列根目录)"))
		return
	}
	s.importPackageRoot(w, strings.TrimSpace(in.Path))
}

// importPackageRoot 导入逻辑本体(与 HTTP 入口解耦)。
//
// 为什么拆出来:小说库新增的「脚本创作」入口要**直接复用同一条路** —— 校验、系列对账、
// 后处理(台词时长预算 / <d>[Chinese] 标记兜底)全在这里。另写一份"只导一个目录"的实现,
// 两边必然分叉,而分叉的后果是"从这个入口进来的包少了一道校验"这种最难查的形态。
func (s *Server) importPackageRoot(w http.ResponseWriter, root string) {
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		writeErr(w, 400, fmt.Errorf("目录不存在:%s", root))
		return
	}
	pkgs, err := collectPackages(root)
	if err != nil || len(pkgs) == 0 {
		writeErr(w, 400, fmt.Errorf("该目录下没有技能端项目包(需要 plan/plan.json):%s", root))
		return
	}
	// 系列根:按 series.json 对账(声明几集就该有几个包)。见函数头注释。
	if !hasPlan(root) {
		if ser, serr := readSeries(root); serr != nil {
			writeErr(w, 400, serr)
			return
		} else if ser != nil {
			if missing := ser.missing(pkgs); len(missing) > 0 {
				msg := fmt.Sprintf("系列「%s」声明 %d 集,只找到 %d 个包;缺:%s"+
					"(包没拷全?只想导一部分就把 series.json 移开)",
					orDefault(ser.Book, filepath.Base(root)), len(ser.Episodes), len(pkgs),
					strings.Join(briefList(missing, 5), "、"))
				s.opLog("⛔ %s", msg)
				writeErr(w, 400, fmt.Errorf("%s", msg))
				return
			}
		}
	}

	type result struct {
		Package string   `json:"package"`
		ID      string   `json:"id,omitempty"`
		Shots   int      `json:"shots"`
		Warns   []string `json:"warns,omitempty"`
		Fails   []string `json:"fails,omitempty"`
	}
	results, fails := []result{}, 0

	// ── 第一遍:全部校验(任何一个 fail 都不落盘) ──
	type staged struct {
		pkg     string
		man     pkgManifest
		plan    *project.Plan
		issues  []project.Issue
		refused bool
	}
	var stage []staged
	for _, pkg := range pkgs {
		res := result{Package: filepath.Base(pkg)}
		man, plan, issues, err := readPackage(pkg)
		if err != nil {
			res.Fails = []string{err.Error()}
			fails++
			results = append(results, res)
			continue
		}
		if f := project.PlanFails(issues); len(f) > 0 {
			res.Fails = project.PlanIssuesText(f)
			res.Warns = warnsText(issues)
			res.Shots = len(plan.Shots)
			fails++
			results = append(results, res)
			stage = append(stage, staged{pkg: pkg, man: man, plan: plan, issues: issues, refused: true})
			continue
		}
		stage = append(stage, staged{pkg: pkg, man: man, plan: plan, issues: issues})
	}
	if fails > 0 {
		s.opLog("⛔ 导入项目包:有 %d 个包未过契约校验,本次**全部不导入**(半导入状态更难查)", fails)
		for _, r0 := range results {
			for _, f := range r0.Fails {
				s.opLog("    %s:%s", r0.Package, f)
			}
		}
		writeJSON(w, 400, map[string]any{
			"error":   fmt.Sprintf("%d/%d 个包未过契约校验,已全部拒绝导入", fails, len(pkgs)),
			"results": results,
			"hint":    "先跑技能端 manju_plan.py check --package <包> 看明细",
		})
		return
	}

	// ── 第二遍:落盘 + 注册 + 后处理 ──
	for _, st := range stage {
		dest := filepath.Join(s.store.Root(), st.man.Book,
			fmt.Sprintf("%s·%s·%s", st.man.Book, st.man.Episode, orDefault(st.man.EpisodeTitle, "未命名")))
		if err := copyPackage(st.pkg, dest); err != nil {
			writeErr(w, 500, fmt.Errorf("拷贝 %s 失败:%w", filepath.Base(st.pkg), err))
			return
		}
		// project.json 的 id 必须与目录名一致(Store 以目录名为 id)
		m := map[string]any{}
		if raw, err := os.ReadFile(filepath.Join(dest, "project.json")); err == nil {
			_ = json.Unmarshal(raw, &m)
		}
		m["id"] = filepath.Base(dest)
		m["name"] = filepath.Base(dest)
		if raw, err := json.MarshalIndent(m, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(dest, "project.json"), raw, 0o644)
		}
		// 后处理:与分镜阶段同等(台词时长预算 + 中文台词标记),再存盘
		id := filepath.Base(dest)
		details, overflow := s.planPostProcess(id, st.plan)
		// 契约把 mode 空值交给平台推断(见 project/shot_mode.go),而技能端产出的方案
		// 根本不写这个字段 —— 于是**导入时就推断并落盘**,盘上是具体值:
		// 用户/技能端都能看到平台实际按什么模式跑,而不是读取时才算。
		nMode := st.plan.NormalizeShotModes()
		if err := s.store.SavePlan(id, st.plan); err != nil {
			writeErr(w, 500, err)
			return
		}
		results = append(results, result{
			Package: filepath.Base(st.pkg), ID: id, Shots: len(st.plan.Shots),
			Warns: append(warnsText(st.issues), append(details, overflow...)...),
		})
		tail := ""
		if nMode > 0 {
			tail = fmt.Sprintf("(其中 %d 镜 mode 为空,已按 ref2va 推断)", nMode)
		}
		s.opLog("📥 已导入项目包(项目「%s」):%d 镜 / %d 角色(%d 张造型卡) / %d 场景%s",
			id, len(st.plan.Shots), project.CharFamilyCount(st.plan.Characters), len(st.plan.Characters), len(st.plan.Scenes), tail)
	}
	added, total := s.store.Rescan() // 让新目录进索引(与 rescan 端点同一实现)
	if len(added) > 0 {
		s.opLog("🔄 重扫:新增 %d 个项目(技能端导入)", len(added))
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "imported": len(results), "results": results,
		"rescan_added": added, "projects_total": total,
		"contract": fmt.Sprintf("manjux.plan v%d", project.PlanContractVersion),
	})
	// 导入完就**自动开跑**。放在这里(导入本体)而不是各入口各写一遍:
	// 导入入口不止一个(小说库「脚本创作」、项目包导入),谁漏一处,用户看到的就是
	// "导入了但什么都不跑" —— 2026-09-26 实测:用户点的是「项目包导入」,日志里
	// 只有 📥 40 行 + 🔄 重扫,一行 ▶ 都没有,而自动开跑只长在我那条入口上。
	// 用户点"导入"的意图是出片,不是把文件抄进 data/projects。
	bookPrefix := ""
	for _, r := range results {
		if r.ID != "" {
			if i := strings.Index(r.ID, "·第"); i > 0 {
				bookPrefix = r.ID[:i]
				break
			}
		}
	}
	// 封面:先带小说自己的封面,再开跑(用户 2026-09-26:导入出来的项目封面成了"生成的角色")
	ids := make([]string, 0, len(results))
	for _, r := range results {
		if r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
	s.applyNovelCover(bookPrefix, ids, root)
	s.autoStartBatch(bookPrefix)
}

// autoStartBatch 导入成功后启动产线：**逐集流水线**。
//
// 【2026-09-28 用户口径（第二版，纠正我上一版的理解）】原话：
// 「不是等我点开始，是批量跑一集进行这一集的 LLM 解析后进行渲染管线顺序，到下一集就是
//
//	下一集的 LLM 解析后进行渲染管线顺序，后面依次顺序，流水线一样到了那一步就做那一步」。
//
// 所以**不是**"跑完第 1 集就停 ✗"（我上一版理解错了），而是：
//
//	· 整本集数照常排队、一路顺下去（不等人点）；
//	· 但**解析是即时的**：轮到第 N 集才做第 N 集的 LLM 解析（见 ensureEpisodeBrief），
//	  解析完紧接着跑这一集的渲染管线，然后第 N+1 集 —— 而不是导入时把整本一次性解析掉。
func (s *Server) autoStartBatch(prefix string) {
	if strings.TrimSpace(prefix) == "" {
		return
	}
	eps := s.episodesOf(prefix)
	if len(eps) == 0 {
		return
	}
	s.reapStaleBatch() // 先请走死批次,否则单例被占着永远起不来
	if s.startProd(prefix, eps) {
		s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
			Message: fmt.Sprintf("📜 已导入并启动产线:《%s》共 %d 集 —— **逐集流水线**:到第 N 集才做第 N 集的 LLM 解析,"+
				"解析完接着跑这一集的渲染管线,再进第 N+1 集(不再一上来就把整本解析掉)", prefix, len(eps))})
		return
	}
	batch.mu.Lock()
	holder, held, running := batch.prefix, len(batch.projects), batch.running
	batch.mu.Unlock()
	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "warn",
		Message: fmt.Sprintf("⚠ 《%s》已导入 %d 集,但批量产线被《%s》(%d 集, running=%v)占着 —— "+
			"先在首页点那张卡的「停止」再点「开始」", prefix, len(eps), holder, held, running)})
}

// pkgManifest 技能端项目包的 manifest(契约声明 + 归属信息)。
type pkgManifest struct {
	Contract     string `json:"contract"`
	Version      int    `json:"version"`
	Book         string `json:"book"`
	Episode      string `json:"episode"`
	EpisodeTitle string `json:"episode_title"`
	ProducedBy   string `json:"produced_by"`
}

// collectPackages 找出目录下的项目包:自身是一个包,或(系列根)其一级子目录是包。
func collectPackages(root string) ([]string, error) {
	if hasPlan(root) {
		return []string{root}, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && hasPlan(filepath.Join(root, e.Name())) {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out, nil
}

func hasPlan(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "plan", "plan.json"))
	return err == nil
}

// seriesManifest 是技能端写在系列根的 series.json(声明这本书有哪些集)。
//
// 平台**不拿它当数据源**(数据源永远是各集自己的 plan.json),只用它**对账**:
// 声明了却找不到的包 = 拷贝不全,必须在入库前拦住。
type seriesManifest struct {
	Contract string `json:"contract"`
	Version  int    `json:"version"`
	Book     string `json:"book"`
	Episodes []struct {
		Episode string `json:"episode"`
		Package string `json:"package"`
	} `json:"episodes"`
}

// readSeries 读系列根的对账文件;没有该文件返回 (nil, nil) —— 那是"逐包导入"的正常用法。
func readSeries(root string) (*seriesManifest, error) {
	raw, err := os.ReadFile(filepath.Join(root, "series.json"))
	if err != nil {
		return nil, nil //nolint:nilerr // 缺失不是错误:没有 series.json 就按子目录逐包导入
	}
	var ser seriesManifest
	if err := json.Unmarshal(raw, &ser); err != nil {
		return nil, fmt.Errorf("series.json 不是合法 JSON:%w", err)
	}
	if ser.Version != 0 && ser.Version != project.PlanContractVersion {
		return nil, fmt.Errorf("series.json 契约版本不符:%d(本侧支持 v%d)—— 两侧必须同版本",
			ser.Version, project.PlanContractVersion)
	}
	return &ser, nil
}

// missing 返回**声明了却没找到**的集目录名(空的 = 齐了)。
func (s *seriesManifest) missing(pkgs []string) []string {
	have := map[string]bool{}
	for _, p := range pkgs {
		have[filepath.Base(p)] = true
	}
	var out []string
	for _, e := range s.Episodes {
		name := e.Package
		if name == "" {
			continue
		}
		if !have[name] {
			out = append(out, name)
		}
	}
	return out
}

// readPackage 读一个包并跑契约校验(含 manifest 的契约名/版本对账)。
func readPackage(pkg string) (pkgManifest, *project.Plan, []project.Issue, error) {
	var man pkgManifest
	rawMan, err := os.ReadFile(filepath.Join(pkg, "manifest.json"))
	if err != nil {
		return man, nil, nil, fmt.Errorf("缺 manifest.json(平台据此识别技能端产出)")
	}
	if err := json.Unmarshal(rawMan, &man); err != nil {
		return man, nil, nil, fmt.Errorf("manifest.json 不是合法 JSON:%w", err)
	}
	if man.Contract != "" && man.Contract != "manjux.plan" {
		return man, nil, nil, fmt.Errorf("契约名不符:%q(本平台支持 manjux.plan)", man.Contract)
	}
	if man.Version != 0 && man.Version != project.PlanContractVersion {
		return man, nil, nil, fmt.Errorf("契约版本不符:包是 v%d、本平台是 v%d —— 两侧要一起改",
			man.Version, project.PlanContractVersion)
	}
	if strings.TrimSpace(man.Book) == "" {
		return man, nil, nil, fmt.Errorf("manifest 缺 book 字段(不知道该放进哪本书的目录)")
	}
	rawPlan, err := os.ReadFile(filepath.Join(pkg, "plan", "plan.json"))
	if err != nil {
		return man, nil, nil, fmt.Errorf("读不到 plan/plan.json:%w", err)
	}
	var plan project.Plan
	if err := json.Unmarshal(rawPlan, &plan); err != nil {
		return man, nil, nil, fmt.Errorf("plan.json 不是合法 JSON:%w", err)
	}
	return man, &plan, project.ValidatePlan(&plan), nil
}

// copyPackage 把包拷进项目目录(不含 assets/clips/output —— 那些是平台自己的产物)。
func copyPackage(src, dest string) error {
	for _, sub := range []string{"plan", "assets/characters", "assets/scenes", "assets/props", "clips", "output", "logs"} {
		if err := os.MkdirAll(filepath.Join(dest, sub), 0o755); err != nil {
			return err
		}
	}
	for _, name := range []string{"manifest.json", "project.json", "source.txt"} {
		if raw, err := os.ReadFile(filepath.Join(src, name)); err == nil {
			if err := os.WriteFile(filepath.Join(dest, name), raw, 0o644); err != nil {
				return err
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join(src, "plan", "plan.json"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, "plan", "plan.json"), raw, 0o644)
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// briefList 只列前 n 项 + 省略号:日志里一条项目名就够定位,几十行会把操作日志刷没。
func briefList(ids []string, n int) []string {
	if len(ids) <= n {
		return ids
	}
	return append(append([]string{}, ids[:n]...), fmt.Sprintf("等 %d 个", len(ids)))
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	// 拉列表时顺手重扫一次盘:**「列表」就是盘上的真相**。
	//
	// 为什么放在这里而不是等用户去点/去调 rescan(2026-09-26 · 双侧共组收尾):
	// 技能端 emit 完就把包写进了 projects/,但界面从来没有"重扫"这个动作,
	// 于是链路最后一环仍是「关掉应用再打开」。放在读路径上,用户看到列表的那一刻
	// 就是最新的 —— 成本只是几次 readdir(已装载的项目直接跳过,不重读元信息),
	// 语义仍是只新增不删除(见 project.Store.Rescan 的边界说明)。
	if added, _ := s.store.Rescan(); len(added) > 0 {
		s.opLog("📥 发现 %d 个外部新建的项目(技能端直接落盘):%s",
			len(added), strings.Join(briefList(added, 3), "、"))
	}
	type item struct {
		*project.Project
		HasPlan bool   `json:"has_plan"`
		Done    int    `json:"shots_done"`
		Total   int    `json:"shots_total"`
		Cover   string `json:"cover"`      // 项目封面(角色定妆照/场景图)
		Chars   int    `json:"characters"` // 方案内**角色人数**(造型档算同一个人;首页统计用)
		Scenes  int    `json:"scenes"`     // 方案内场景数
	}
	out := []item{}
	for _, p := range s.store.List() {
		it := item{Project: p, Cover: s.projectCover(p.ID)}
		if plan, err := s.store.LoadPlan(p.ID); err == nil {
			it.HasPlan = true
			it.Total = len(plan.Shots)
			// 按**人**算(§4.2):技能侧把每个造型档做成独立卡,按卡计数会把
			// 《翻篇》的 12 个角色报成 21 个,首页统计就成了"多出来一堆新角色"。
			it.Chars = project.CharFamilyCount(plan.Characters)
			it.Scenes = len(plan.Scenes)
			if m, err := s.store.LoadManifest(p.ID); err == nil {
				for _, c := range m {
					if c.Status == project.ShotDone {
						it.Done++
					}
				}
			}
		}
		if run := s.pipe.Running(p.ID); run != nil {
			it.Running = true
			it.Phase = run.Phase
		}
		out = append(out, it)
	}
	writeJSON(w, 200, out)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	cfg := project.ProjectConfig{Params: project.DefaultStageParams()}
	// 全局设置的默认引擎优先(auto=本地优先自动降级;api=纯云端;comfy=纯本地)
	if e := s.cfg.Get().Render.DefaultEngine; e == "auto" || e == "comfy" || e == "api" {
		cfg.Params.Render.Engine = e
	}
	p, err := s.store.Create(in.Name, cfg)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.opLog("＋ 已创建项目「%s」", in.Name)
	writeJSON(w, 200, p)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := s.store.Get(id)
	if !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	if run := s.pipe.Running(id); run != nil {
		p.Running = true
		p.Phase = run.Phase
	}
	out := map[string]any{"project": p}
	if plan, err := s.store.LoadPlan(id); err == nil {
		out["plan"] = plan
	}
	// 每个角色的**生效实体类型**由后端下发(审计 P1-2):前端原先各自保留一份
	// 关键词推断表,而那份表已经和后端漂移过 —— 界面显示"器物"、渲染按"人"走,
	// 或者相反,用户看到的就是"改了类型没生效"。类型决定整套渲染方法
	// (定妆照取景/多视角方式/镜头身份锁),不允许有两个真源。
	out["char_kinds"] = planCharKinds(id, s)
	// 角色卡的**人的归属**同样由后端下发(§4.2):技能侧把造型档做成独立卡
	// (`char_01v1` / `char_01v2` + 名字 `翻篇·收容所` / `翻篇·进城`),前端要按
	// "一个人 + 他的 N 个造型档"分组显示,而不是 N 个新角色。判据只许有一份实现。
	out["char_families"] = planCharFamilies(id, s)
	if assets, err := s.store.LoadAssets(id); err == nil {
		out["assets"] = assets
	}
	if m, err := s.store.LoadManifest(id); err == nil {
		out["manifest"] = m
	}
	if source, err := s.store.LoadSource(id); err == nil {
		out["source"] = source
	}
	// qc 报告
	qcRaw, err := os.ReadFile(filepath.Join(s.store.Dir(id), "output", "qc_report.json"))
	if err == nil {
		var qcAny any
		if json.Unmarshal(qcRaw, &qcAny) == nil {
			out["qc"] = qcAny
		}
	}
	// 成片状态(含 2K 定稿)
	outReady := map[string]any{"ready": false, "final": "output/final.mp4"}
	if st, err := os.Stat(filepath.Join(s.store.Dir(id), "output", "final.mp4")); err == nil && st.Size() > 0 {
		outReady["ready"] = true
		outReady["size"] = st.Size()
	}
	outReady["final_2k"] = "output/final_2k.mp4"
	if st, err := os.Stat(filepath.Join(s.store.Dir(id), "output", "final_2k.mp4")); err == nil && st.Size() > 0 {
		outReady["ready_2k"] = true
	}
	out["output"] = outReady
	writeJSON(w, 200, out)
}

// planCharKinds 角色的**生效实体类型**映射(id → kind),供前端显示与选择器使用。
//
// 为什么不把推断结果写回方案:方案里的 `kind` 是用户显式指定的东西,
// "未指定则推断"是渲染时的兜底语义。把兜底结果持久化,等于把一次关键词命中
// 固化成了用户的决定 —— 之后改描述也不会改变类型,反而更难理解。
func planCharKinds(projectID string, s *Server) map[string]string {
	plan, err := s.store.LoadPlan(projectID)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(plan.Characters))
	for _, ch := range plan.Characters {
		out[ch.ID] = string(pipeline.ResolveEntityKind(ch))
	}
	return out
}

// charFamilyInfo 一张角色卡的「人的归属」(技能侧清单 §4.2),随项目下发供前端分组。
type charFamilyInfo struct {
	Family  string `json:"family"`  // 家族键(前端分组用;同一个人多张卡共用一个键)
	Base    string `json:"base"`    // 人名的基名(「翻篇·归零」→「翻篇」)
	Variant string `json:"variant"` // 造型档名(「归零」;空 = 这张卡没写档名)
	Master  bool   `json:"master"`  // 是不是这一族的**主卡**(变体卡的派生起点)
}

// planCharFamilies 角色 id → 人的归属。前端据此把同一角色的多张造型档显示成
// 「一个人 + 他的 N 个造型档」,而不是 N 个新角色;不这样显示,用户看到《翻篇》
// 21 张卡就会以为多出来 9 个新角色。
//
// 判据只在 project.CharFamily* 一处实现(id 带 vK / 名字带「·档名」),不在这里另写一份:
// 前端再抄一份必然与后端漂移(与上面 char_kinds 的同一条理由)。
func planCharFamilies(projectID string, s *Server) map[string]charFamilyInfo {
	plan, err := s.store.LoadPlan(projectID)
	if err != nil {
		return nil
	}
	// 归族走**共用实现**（2026-09-28）：单卡口径（CharFamilyKey，id 优先）在
	// "主卡 id 规范、档卡 id 不规范"时会分成两族，而 SameCharacter 那把尺子已经修正 ——
	// 两处各写一遍必然走散（界面显示"两个人"，撞脸自检却知道他们是一个人）。
	index := project.CharFamilyIndex(plan.Characters)
	// 主卡 = 同族里**档位号最小**的那张(0 = 名字不带档名的家族本名,最优先);
	// 档位相同(理论上不会)时取计划里先出现的那张,保证结果稳定。
	bestRank, masterID := map[string]int{}, map[string]string{}
	for _, ch := range plan.Characters {
		k := index[ch.ID]
		r := project.CharVariantNumberOf(ch.ID)
		if cur, ok := bestRank[k]; !ok || r < cur {
			bestRank[k], masterID[k] = r, ch.ID
		}
	}
	out := make(map[string]charFamilyInfo, len(plan.Characters))
	for _, ch := range plan.Characters {
		k := index[ch.ID]
		out[ch.ID] = charFamilyInfo{
			Family:  k,
			Base:    project.CharFamilyBaseName(ch.Name),
			Variant: project.CharVariantLabel(ch.Name),
			Master:  masterID[k] == ch.ID,
		}
	}
	return out
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,请先停止"))
		return
	}
	if err := s.store.Delete(id); err != nil {
		writeErr(w, 400, err)
		return
	}
	// 同步清理条件缓存(cond_cache/{projectID}/ 目录整删,2026-09-11)
	if eng, err := s.reg.Get("comfy"); err == nil {
		if ce, ok := eng.(*comfyengine.Engine); ok {
			ce.CleanupProjectCache(id)
		}
	}
	s.opLog("🗑 已删除项目「%s」(方案/资产/镜头/成片/条件缓存一并移除)", id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) getSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	text, err := s.store.LoadSource(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]string{"text": text})
}

func (s *Server) putSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.store.Get(id); !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	if strings.ContainsRune(in.Text, '\uFFFD') {
		writeErr(w, 400, fmt.Errorf("文本含乱码字符(U+FFFD):请通过工作台界面提交,确保 UTF-8 编码"))
		return
	}
	if err := s.store.SaveSource(id, in.Text); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	plan, err := s.store.LoadPlan(id)
	if err != nil {
		writeErr(w, 404, fmt.Errorf("尚未生成分镜方案"))
		return
	}
	writeJSON(w, 200, plan)
}

// putParams 阶段参数调优(整体替换,服务端 Sanitize 钳制)。
func (s *Server) putParams(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,不能调参"))
		return
	}
	p, ok := s.store.Get(id)
	if !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	var in project.StageParams
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	// 拒绝含 U+FFFD 的输入(GBK 直传等编码事故),防脏数据入库
	if strings.ContainsRune(fmt.Sprint(in), '\uFFFD') {
		writeErr(w, 400, fmt.Errorf("参数含乱码字符(U+FFFD):请通过工作台界面提交,确保 UTF-8 编码"))
		return
	}
	in.Sanitize()
	p.Config.Params = in
	if err := s.store.SaveMeta(p); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, p.Config.Params)
}

func (s *Server) runProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Phase  string `json:"phase"`
		Only   []int  `json:"only"`
		Engine string `json:"engine"` // 单次引擎覆盖(auto/comfy/api,空=项目配置)
		Seed   int64  `json:"seed"`   // 单镜微调种子(>0 生效:seed+N*101)
	}
	_ = readBody(r, &in)
	if in.Phase == "" {
		in.Phase = pipeline.PhaseAll
	}
	if err := s.pipe.Start(id, in.Phase, in.Only, in.Engine, in.Seed); err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "phase": in.Phase})
}

// upgrade2K 云端 2K 定稿:body {shot: N}(0=成片)。
func (s *Server) upgrade2K(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pipe.Running(id) != nil {
		writeErr(w, 409, fmt.Errorf("项目运行中,请先停止"))
		return
	}
	var in struct {
		Shot int `json:"shot"`
	}
	_ = readBody(r, &in)
	if err := s.pipe.StartUpgrade2K(id, in.Shot); err != nil {
		writeErr(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) stopProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.pipe.Stop(id) {
		// 后端已结束而前端状态未同步(常见于刚跑完就点停止):不报错,
		// 返回 stopped=false 让前端立即刷新状态即可。
		writeJSON(w, 200, map[string]any{"ok": true, "stopped": false})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "stopped": true})
}

// ---------- 资产上传 ----------
