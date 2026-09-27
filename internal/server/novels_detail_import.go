package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// novels_detail_import.go 「脚本创作」的**新导入路**：吃技能侧的结构化明细
// （`分镜/分镜明细.json`），而不是旧的渲染包目录。
//
// ## 为什么要有这条路（2026-09-27 用户拍板走 C 方案）
//
// 技能侧把交付面换成了 markdown + 结构化明细之后，盘上唯一"机器可读且可渲染"的东西
// 曾经只剩 `漫剧脚本/**/plan.json` —— 而那批包是技能侧**自己标为历史**的
// （`_已废弃-说明.md`），于是小说的「脚本创作」入口上永远挂着「历史包」标签。
// 技能侧 09-27 回复里把三样接线补齐（R1 每镜 `h3_prompt`、R2 `scene_id`/`character_ids`、
// R3 三类卡进 JSON），这条路才成立：**从明细直接建集项目 → 同一条校验 → 自动开产线**。
//
// ## 与旧包那条路的关系
//
// 完全并列的两条入口，共用后半段（契约校验 → 后处理 → 封面 → 自动开线）：
//   - 明细在 → **走这条**（新路）；
//   - 明细不在 → 退回 `importPackageRoot`（旧包，仍可用，入口上会标「历史包」）。
//
// ## 两处"必须有"的处理（都在本文件里，别散到别处）
//
//  1. **风格句**：技能侧的 `h3_prompt` 自带一段 `GUOFENG …` 风格句（官方格式要求它写在
//     `[Shot 1]` 之前），而平台装配时会**再插一段平台侧的风格/介质块** —— 一段提示词两套风格，
//     模型只会取中值（与 `applyCardPrompts` 剥角色卡风格句是同一条机理）。
//     所以这里把技能侧那句**提出来当 `plan.Style`**（单一真源、仍由平台在正常位置注入），
//     并从每镜提示词里**剔掉**它，避免重复。
//  2. **集标题**：明细的 `episodes[]` 目前只有 `{no, shots, seconds}`（没有标题），
//     而平台的项目名是 `<书名>·第NN集·<标题>` —— 标题从 `分镜/第NN集.md` 的一级标题取
//     （`# 第01集 · 求你们骂我 — 分镜（内核）`），取不到退「未命名」。
//     已请技能侧在 `episodes[]` 补 `title`，补了就以它为准（见 episodeTitles）。

// detailEpKeys 明细里出现过的集号（升序）。
func detailEpKeys(shots []scriptDetailShot) []int {
	seen := map[int]bool{}
	var out []int
	for _, s := range shots {
		k := epKeyOf(s.Episode)
		n, err := strconv.Atoi(k)
		if err != nil {
			continue // 非数字集号（理论上有）：这条路按数字集组织，跳过并保持安静
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// episodeTitles 集标题：优先取明细头部 `episodes[].title`（技能侧补了就生效），
// 否则从 `分镜/第NN集.md` 的一级标题解析（`# 第01集 · 求你们骂我 — 分镜（内核）`）。
//
// 为什么值得为标题写两套：平台的项目名就是 `<书名>·第NN集·<标题>`，退化成「未命名」
// 会让 40 个集项目在界面上长得一模一样（用户按标题找集）。
func episodeTitles(book string, d *novelDetailFile) map[int]string {
	out := map[int]string{}
	for _, ep := range d.Episodes {
		if t := strings.TrimSpace(ep.Title); t != "" {
			out[ep.No] = t
		}
	}
	for _, n := range detailEpKeys(d.Shots) {
		if _, ok := out[n]; ok {
			continue
		}
		if t := titleFromEpisodeMarkdown(book, n); t != "" {
			out[n] = t
		}
	}
	return out
}

// titleFromEpisodeMarkdown 从 `分镜/第NN集.md` 的一级标题里取集标题。
func titleFromEpisodeMarkdown(book string, no int) string {
	for _, name := range []string{fmt.Sprintf("第%02d集.md", no), fmt.Sprintf("第%d集.md", no)} {
		raw, err := os.ReadFile(filepath.Join(book, "分镜", name))
		if err != nil {
			continue
		}
		line := strings.SplitN(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n", 2)[0]
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		// 形态：`第01集 · 求你们骂我 — 分镜（内核）` → 取 `·` 与 `—` 之间
		if i := strings.Index(line, "·"); i >= 0 {
			line = line[i+len("·"):]
		}
		for _, cut := range []string{"—", "-", "－"} {
			if j := strings.Index(line, cut); j >= 0 {
				line = line[:j]
			}
		}
		return strings.TrimSpace(line)
	}
	return ""
}

// buildEpisodePlan 把明细里某一集的镜头组装成平台的 plan。
//
// 返回 (plan, 用到的角色/场景/道具卡条数, 被剔掉风格句的镜数)。
func buildEpisodePlan(d *novelDetailFile, no int, title string, epShots []scriptDetailShot) (*project.Plan, int, int) {
	plan := &project.Plan{Title: title}

	// ① 卡：**只带这一集实际用到的**（旧包就是这样：每集只带自己那几个角色/场景）。
	// 全带会把 30 个角色 × 40 集的参考图全生成一遍，纯浪费 GPU。
	needChar, needScene := map[string]bool{}, map[string]bool{}
	for _, s := range epShots {
		for _, id := range s.CharacterIDs {
			needChar[id] = true
		}
		if sid := strings.TrimSpace(s.SceneID); sid != "" {
			needScene[sid] = true
		}
	}
	for _, c := range d.Characters {
		if needChar[c.ID] {
			plan.Characters = append(plan.Characters, project.Character{
				ID: c.ID, Name: c.Name, Kind: c.Kind, Description: c.Description,
				ImagePrompt: c.ImagePrompt, Voice: c.Voice,
			})
		}
	}
	for _, sc := range d.Scenes {
		if needScene[sc.ID] {
			plan.Scenes = append(plan.Scenes, project.Scene{
				ID: sc.ID, Name: sc.Name, Description: sc.Description,
				ImagePrompt: sc.ImagePrompt, Landmarks: sc.Landmarks, LightState: sc.LightState,
			})
		}
	}
	// 道具：明细的镜头里**没有**道具引用（技能侧的字段还没加），所以只能按名字在正文里
	// 做**尽力匹配** —— 宁可少带一个道具图，也不要把 15 件道具塞进每一集（那是 15×40 张图）。
	// 已请技能侧给 `prop_ids`；字段到位后这一段直接换成 ID 匹配。
	if len(d.Props) > 0 {
		text := episodeText(epShots)
		for _, pr := range d.Props {
			if strings.TrimSpace(pr.Name) == "" || !strings.Contains(text, pr.Name) {
				continue
			}
			plan.Props = append(plan.Props, project.Prop{
				ID: pr.ID, Name: pr.Name, EN: pr.EN, Description: pr.Description, ImagePrompt: pr.ImagePrompt,
			})
		}
	}

	// ② 镜头 + 风格句提取
	styled := 0 // 提成 plan.Style 的镜数（0 表示这些镜本来就没写风格句）
	for _, s := range epShots {
		sh := project.Shot{
			ID: s.ID, ShotSize: s.ShotSize, Camera: s.Camera, Action: s.Action,
			Narration: s.Narration, Duration: s.Duration, H3Prompt: s.H3Prompt,
			View: s.View, HookType: s.HookType, EndState: s.EndState,
			Unbound: s.Unbound, UnboundReason: s.UnboundReason,
		}
		sh.Scene = strings.TrimSpace(s.SceneID)
		if sh.Scene == "" {
			sh.Scene = strings.TrimSpace(s.Scene) // 兜底：用场景名当 id（契约只要求与 scenes[] 对得上）
		}
		sh.Characters = append(sh.Characters, s.CharacterIDs...)
		for _, ln := range s.Dialogue {
			if strings.TrimSpace(ln.Text) == "" {
				continue
			}
			sh.Dialogue = append(sh.Dialogue, project.Line{Speaker: ln.Speaker, Text: ln.Text, OS: ln.OS})
		}
		// 风格句：**只提取、不删除**。技能侧那段风格句留在提示词里（官方格式就要求它写在
		// `[Shot 1]` 之前），同时提成 `plan.Style` —— 这样参考图（定妆照/场景板）也服从同一套
		// 风格，而且装配时平台会**让位**（见 pipeline/styleAndMediumBlock 与 project.HasStyleSentence），
		// 不会再插一段平台风格块。早先的做法是"剔掉镜头里的那句"，结果契约门开始报
		// "detailed_description 里没有风格句" —— 删上游文本会连带破坏它自己的格式判据。
		if style, ok := project.StyleSentenceOf(sh.H3Prompt); ok {
			styled++
			if strings.TrimSpace(plan.Style) == "" {
				plan.Style = style // 单一真源：同一部剧里这段是一样的，取第一次出现的即可
			}
		}
		plan.Shots = append(plan.Shots, sh)
	}
	return plan, len(plan.Characters) + len(plan.Scenes) + len(plan.Props), styled
}

// episodeText 一集里所有可搜文本（道具名字匹配用）。
func episodeText(shots []scriptDetailShot) string {
	var sb strings.Builder
	for _, s := range shots {
		sb.WriteString(s.Action)
		sb.WriteString("\n")
		sb.WriteString(s.ActionEN)
		sb.WriteString("\n")
	}
	return sb.String()
}

// importNovelDetail 从结构化明细建集项目。**返回 false 表示这条路不适用**（调用方退回旧包）。
//
// 与旧包那条路同一条政策：**任何一个集不过契约校验就全部不导**（半导入状态更难查）。
func (s *Server) importNovelDetail(w http.ResponseWriter, book string, d *novelDetailFile) bool {
	eps := detailEpKeys(d.Shots)
	if len(eps) == 0 {
		return false
	}
	titles := episodeTitles(book, d)
	bookName := strings.TrimSpace(d.Book)
	if bookName == "" {
		bookName = filepath.Base(book)
	}

	type result struct {
		Package string   `json:"package"`
		ID      string   `json:"id,omitempty"`
		Shots   int      `json:"shots"`
		Warns   []string `json:"warns,omitempty"`
		Fails   []string `json:"fails,omitempty"`
	}
	type staged struct {
		no    int
		name  string
		plan  *project.Plan
		issue []project.Issue
	}
	var stage []staged
	var results []result
	fails := 0
	for _, no := range eps {
		var epShots []scriptDetailShot
		for _, sh := range d.Shots {
			if k, err := strconv.Atoi(epKeyOf(sh.Episode)); err == nil && k == no {
				epShots = append(epShots, sh)
			}
		}
		title := titles[no]
		name := fmt.Sprintf("%s·第%02d集·%s", bookName, no, orDefault(title, "未命名"))
		plan, _, _ := buildEpisodePlan(d, no, title, epShots)
		issues := project.ValidatePlan(plan)
		res := result{Package: fmt.Sprintf("%s·第%02d集", bookName, no), ID: name, Shots: len(plan.Shots)}
		if f := project.PlanFails(issues); len(f) > 0 {
			res.Fails = project.PlanIssuesText(f)
			res.Warns = warnsText(issues)
			fails++
			results = append(results, res)
			continue
		}
		stage = append(stage, staged{no: no, name: name, plan: plan, issue: issues})
	}
	if fails > 0 {
		s.opLog("⛔ 导入脚本明细:有 %d 集未过契约校验,本次**全部不导入**", fails)
		writeJSON(w, 400, map[string]any{
			"error":   fmt.Sprintf("%d/%d 集未过契约校验,已全部拒绝导入", fails, len(eps)),
			"results": results,
			"source":  novelShotDetailJSON,
			"hint":    "技能侧可先跑 scripts/qa_check.py --check-shot-table 看明细",
		})
		return true
	}

	// 落盘：与拆集/旧包导入同一条约定（`<projects>/<书名>/<书名·第NN集·标题>/`，见 Store.Dir）
	created := 0
	for _, st := range stage {
		if _, ok := s.store.Get(st.name); !ok {
			if _, err := s.store.Create(st.name, s.projectConfigDefault()); err != nil {
				writeErr(w, 500, fmt.Errorf("建项目 %s 失败:%w", st.name, err))
				return true
			}
			created++
		}
	}
	s.store.Rescan()
	for i := range stage {
		st := &stage[i]
		details, overflow := s.planPostProcess(st.name, st.plan)
		nMode := st.plan.NormalizeShotModes()
		if err := s.store.SavePlan(st.name, st.plan); err != nil {
			writeErr(w, 500, err)
			return true
		}
		results = append(results, result{
			Package: fmt.Sprintf("%s·第%02d集", bookName, st.no), ID: st.name, Shots: len(st.plan.Shots),
			Warns: append(warnsText(st.issue), append(details, overflow...)...),
		})
		s.opLog("📥 已从脚本明细导入第%02d集(项目「%s」):%d 镜 / %d 角色 / %d 场景 / %d 道具(新建 %d)",
			st.no, st.name, len(st.plan.Shots), len(st.plan.Characters), len(st.plan.Scenes), len(st.plan.Props), created)
		_ = nMode
	}
	// 顺序与旧包那条路一致：先写盘 → 再重扫 → 再封面 → 最后开产线
	added, total := s.store.Rescan()
	ids := make([]string, 0, len(results))
	for _, r := range results {
		if r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "imported": len(results), "results": results,
		"source": novelShotDetailJSON, "rescan_added": added, "projects_total": total,
		"contract": fmt.Sprintf("manjux.plan v%d", project.PlanContractVersion),
	})
	s.applyNovelCover(bookName, ids, book)
	s.autoStartBatch(bookName)
	return true
}

// detailCardViews 用明细内嵌的卡拼页签要的角色/场景/道具视图（P2：不再依赖旧渲染包）。
func detailCardViews(d *novelDetailFile) (chars []*scriptCharView, scenes []*scriptSceneView, props []*scriptPropView) {
	// 各卡出现在几集里
	epOf := func(shots []scriptDetailShot, hit func(scriptDetailShot) bool) map[string]bool {
		out := map[string]bool{}
		for _, s := range shots {
			if hit(s) {
				out[epKeyOf(s.Episode)] = true
			}
		}
		return out
	}
	for _, c := range d.Characters {
		in := len(epOf(d.Shots, func(s scriptDetailShot) bool {
			for _, id := range s.CharacterIDs {
				if id == c.ID {
					return true
				}
			}
			return false
		}))
		chars = append(chars, &scriptCharView{
			ID: c.ID, Name: c.Name, Kind: c.Kind, Desc: c.Description, Voice: c.Voice,
			Prompt: c.ImagePrompt, In: in,
		})
	}
	for _, sc := range d.Scenes {
		in := len(epOf(d.Shots, func(s scriptDetailShot) bool { return s.SceneID == sc.ID }))
		scenes = append(scenes, &scriptSceneView{ID: sc.ID, Name: sc.Name, Desc: sc.Description, In: in})
	}
	for _, pr := range d.Props {
		in := 0
		if len(d.Shots) > 0 {
			// 道具没有镜头引用（技能侧字段待补），只能按名字在正文里数 —— 与导入时的同一口径
			for _, k := range detailEpKeys(d.Shots) {
				var epShots []scriptDetailShot
				for _, sh := range d.Shots {
					if n, err := strconv.Atoi(epKeyOf(sh.Episode)); err == nil && n == k {
						epShots = append(epShots, sh)
					}
				}
				if strings.Contains(episodeText(epShots), pr.Name) {
					in++
				}
			}
		}
		props = append(props, &scriptPropView{ID: pr.ID, Name: pr.Name, EN: pr.EN, Desc: pr.Description, Prompt: pr.ImagePrompt, In: in})
	}
	return chars, scenes, props
}
