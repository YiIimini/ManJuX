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

	"github.com/YiIimini/ManJuX/internal/project"
)

type scriptShotView struct {
	ID        int      `json:"id"`
	Size      string   `json:"size"`
	Duration  int      `json:"duration"`
	Mode      string   `json:"mode"`
	Action    string   `json:"action"`
	Dialogue  []string `json:"dialogue,omitempty"` // "说话人:台词"
	Narration string   `json:"narration,omitempty"`
	Chars     []string `json:"characters,omitempty"`
	Prompt    string   `json:"prompt,omitempty"` // h3_prompt(按需返回)

	// 下面这几个来自**结构化明细**（`分镜/分镜明细.json`，技能侧 2026-09-27 新交）：
	// 渲染包里没有它们（plan.json 是停用前的旧形态）。只有明细这条路会填。
	//
	// 口径（技能侧 09-27 订正版 + 平台裁定 A）：`view` 是**官方 9 条之一、可以留空**
	// （小景别在官方表里没有对应项，硬填错词比留空更糟）；中文机位角度另走 `angle_zh`，
	// **不是契约字段**、不回写 plan；留空的原因在 `view_note` 里。
	View      string `json:"view,omitempty"`       // 官方主视角（可能是空字符串）
	ViewNote  string `json:"view_note,omitempty"`  // 留空原因
	AngleZH   string `json:"angle_zh,omitempty"`   // 中文机位角度（平视/仰拍/俯拍/鸟瞰）
	HookType  string `json:"hook_type,omitempty"`  // 官方受控 10 个之一
	EndState  string `json:"end_state,omitempty"`  // 尾帧状态
	Landmarks string `json:"landmarks,omitempty"`  // 场景地标（连续性锚点）
	Light     string `json:"light,omitempty"`      // 光态
	SceneName string `json:"scene_name,omitempty"` // 场景名（明细里是中文名）
}

type scriptEpView struct {
	Episode string `json:"episode"`
	Title   string `json:"title"`
	Dir     string `json:"dir"`
	Shots   int    `json:"shots"`
}

type scriptCharView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Desc   string `json:"desc"`
	Voice  string `json:"voice,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	In     int    `json:"in"` // 出现在几集里
}

type scriptSceneView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
	In   int    `json:"in"`
}

// scriptPropView 道具卡(用户 2026-09-26:「在小说库的弹窗里新增 道具 TAG」)。
//
// 技能侧的字段是逐步加的:老包只有 en(英文外观短语),新包会有 description 与
// image_prompt —— 这里**都收、都原样带出去**,缺哪个就不显示哪个,不让前端为了
// 一个还没吐出来的字段去猜。
type scriptPropView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	EN     string `json:"en,omitempty"`
	Desc   string `json:"desc,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	In     int    `json:"in"` // 出现在几集里
}

// novelDetailChar / Scene / Prop 技能侧**卡**（`素材/卡片.json`，也内嵌在明细里）。
// 字段名是平台契约名（见 project.Character/Scene/Prop），只多了 `board_prompt`（三视图板提示词）。
type novelDetailChar struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	ImagePrompt string `json:"image_prompt"`
	BoardPrompt string `json:"board_prompt"`
	Voice       string `json:"voice"`
}

type novelDetailScene struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ImagePrompt string `json:"image_prompt"`
	Landmarks   string `json:"landmarks"`
	LightState  string `json:"light_state"`
}

type novelDetailProp struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	EN          string `json:"en"`
	Description string `json:"description"`
	ImagePrompt string `json:"image_prompt"`
}

// novelDetailFile 技能侧结构化明细全文（`分镜/分镜明细.json`，2026-09-27 起 = 可渲染的 plan）。
//
// 头部 `contract/version/book/episodes/episode_seconds` 让平台能像读包一样判契约版本；
// 内嵌三类卡之后，平台**不必再解析 markdown**（技能侧回复 §3 的原话）。
type novelDetailFile struct {
	Contract string `json:"contract"`
	Version  int    `json:"version"`
	Book     string `json:"book"`
	Episodes []struct {
		No      int    `json:"no"`
		Title   string `json:"title"`
		Shots   int    `json:"shots"`
		Seconds int    `json:"seconds"`
	} `json:"episodes"`
	Characters []novelDetailChar  `json:"characters"`
	Scenes     []novelDetailScene `json:"scenes"`
	Props      []novelDetailProp  `json:"props"`
	Shots      []scriptDetailShot `json:"shots"`
}

// loadNovelDetail 读整份结构化明细（没有/读不动返回 nil —— 页签与导入各自有兜底）。
func loadNovelDetail(book string) *novelDetailFile {
	raw, err := os.ReadFile(filepath.Join(book, "分镜", novelShotDetailJSON))
	if err != nil {
		return nil
	}
	var f novelDetailFile
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	if len(f.Shots) == 0 {
		return nil
	}
	return &f
}

// loadNovelShotDetail 只要镜头（页签那条路用）。
func loadNovelShotDetail(book string) []scriptDetailShot {
	if d := loadNovelDetail(book); d != nil {
		return d.Shots
	}
	return nil
}

// scriptDeliverableView 技能侧**新交付物**的一条（`素材/*.md`、`分镜/*.md`）。
//
// 为什么要它（2026-09-27 用户：「技能侧的脚本重新输出了，小说库里的剧本创作功能回归」）：
// 技能侧 v2.14 把交付面改成了 markdown —— `素材/{角色卡,场景卡,道具卡}.md` 与
// `分镜/{第NN集.md,H3-提示词.md,分镜脚本.md,分镜-通用.md}`（六列内核 ＋ H3 六段式），
// 而本页签**只认渲染脚本包**（`manifest.json + plan/plan.json`）。于是盘上明明有新产物，
// 界面却只说"没有技能端脚本包" —— 功能看起来"没了"。这里把新产物也列出来（只列与读，
// 不解析成数据：它们是给人读的，渲染输入仍走 plan.json / 平台 LLM 直出）。
type scriptDeliverableView struct {
	Name  string `json:"name"`
	Rel   string `json:"rel"`  // 相对书目录（界面显示）
	Path  string `json:"path"` // 绝对路径（交给 GET /api/novels/read 读）
	Size  int64  `json:"size"`
	MTime string `json:"mtime"`
}

// scriptPackageView 渲染脚本包的存在性（含"技能侧是否已把它标为废弃"）。
type scriptPackageView struct {
	Present    bool   `json:"present"`
	Count      int    `json:"count,omitempty"`
	Deprecated bool   `json:"deprecated,omitempty"`
	Hint       string `json:"hint,omitempty"`
}

// collectNovelDeliverables 列出一本书目录里技能侧的**新交付物**与渲染包状态。
//
// 只认技能侧明确定义的两个目录（`素材/`、`分镜/`），**不递归**：这两个目录的产物是
// 技能侧脚本写的（`asset_cards.py` / `storyboard_export.py`），深度固定；扫太深会把
// `素材/_llm/*.json` 缓存、备份目录里的旧文件当成交付物列出来。
func collectNovelDeliverables(book string) (assets, storyboard []scriptDeliverableView, pkg scriptPackageView) {
	pick := func(sub string, sortByEpisode bool) []scriptDeliverableView {
		dir := filepath.Join(book, sub)
		ents, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		out := make([]scriptDeliverableView, 0, len(ents))
		for _, e := range ents {
			if e.IsDir() {
				continue // `_llm/` 之类是缓存目录，不是交付物
			}
			// 只收 markdown，外加**结构化明细**那一个 JSON（技能侧 2026-09-27 新交的
			// `分镜明细.json`：按 N3 的请求让它在这里可见、可读；其它 json 不收 —— 免得
			// 把缓存、中间产物当交付物列出来）。
			name := e.Name()
			lower := strings.ToLower(name)
			if !strings.HasSuffix(lower, ".md") && name != novelShotDetailJSON {
				continue
			}
			full := filepath.Join(dir, e.Name())
			fi, err := os.Stat(full)
			if err != nil {
				continue
			}
			out = append(out, scriptDeliverableView{
				Name: e.Name(), Rel: filepath.ToSlash(filepath.Join(sub, e.Name())), Path: full,
				Size: fi.Size(), MTime: fi.ModTime().Format("2006-01-02 15:04"),
			})
		}
		// 分镜目录按集号排（`第02集.md` 不能排到 `第10集.md` 后面），其余按名字
		sort.Slice(out, func(i, j int) bool {
			if sortByEpisode {
				ni, nj := epNumOf(out[i].Name), epNumOf(out[j].Name)
				if ni != nj {
					return ni < nj
				}
			}
			return out[i].Name < out[j].Name
		})
		return out
	}
	assets = pick("素材", false)
	storyboard = pick("分镜", true)

	// 渲染包是否已被技能侧标为"废弃/别当渲染输入"。
	// 与 findBookScript 用**同一个判据函数**（两处各写一份，将来技能侧改标记文件名时必漏一处）。
	if dep, hint := scriptPackagesDeprecated(filepath.Join(book, "漫剧脚本"), filepath.Join(book, "脚本")); dep {
		pkg.Deprecated = true
		pkg.Hint = hint
	}
	return assets, storyboard, pkg
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// epNumOf 从 `第NN集.md` 里取集号（取不到给一个大数，排到最后）。
func epNumOf(name string) int {
	s := epNoOf(name)
	s = strings.TrimPrefix(s, "第")
	s = strings.TrimSuffix(s, "集")
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 1 << 20
	}
	return n
}

// novelShotDetailJSON 技能侧新交的**结构化分镜明细**文件名（`分镜/分镜明细.json`）。
//
// 为什么要读它（2026-09-27 技能侧追补单 N3）：渲染包里的 `plan.json` 是停用前的旧形态
// （没有 `view` / `hook_type` / `end_state`，也没有 `scene_landmarks` / `light`），而这份 JSON
// 的**字段名直接用平台契约的字段名**、引擎无关（2462 镜 / 40 集），页签要展示"新规则下的分镜"
// 就得读它。**只读展示，不当作渲染输入** —— 渲染仍走 plan.json 或平台 LLM 直出。
//
// ⚠ 口径提醒（已回报技能侧）：`view` 的值是**中文短标签**（平视/仰拍/俯拍/鸟瞰），不是契约要求的
// 官方 9 条英文串（`ultra-wide eye-level deep perspective` …）。展示无妨，但**不能**把它当契约字段
// 喂回 plan —— 那样每一镜都会撞"不在官方词表"。
const novelShotDetailJSON = "分镜明细.json"

// scriptDetailShot 结构化明细里的一条镜头（字段名对齐平台契约）。
type scriptDetailShot struct {
	// Episode 收成 any：真文件里它是**数字**（`"episode": 1`），而人是会写成字符串的 ——
	// 声明成 string 会让 json.Unmarshal 整条失败、整份明细读不出来（这类"一个字段类型写错、
	// 连带整份数据消失"的坑本项目记过一次：novels_script.go 里 manifest.version 就是）。
	Episode    any      `json:"episode"`
	ID         int      `json:"id"`
	Duration   int      `json:"duration"`
	ShotSize   string   `json:"shot_size"`
	Scene      string   `json:"scene"`
	Characters []string `json:"characters"`
	Mode       string   `json:"mode"`
	// SceneID / CharacterIDs 是技能侧 2026-09-27 补齐的**机器用引用**（给人读的是
	// `scene`(中文名) 与 `characters`(显示名)）。平台按 ID 绑参考图 —— 只认名字会遇到
	// 同名与造型档（`沈砚` vs `沈砚·一品`）歧义。老明细没有这两个字段时留空，
	// 导入时退回按名字匹配（见 buildEpisodePlan）。
	SceneID      string   `json:"scene_id"`
	CharacterIDs []string `json:"character_ids"`
	View         string   `json:"view"`      // 官方 9 条之一，**可空**（裁定 A：小景别留空是对的）
	ViewNote     string   `json:"view_note"` // view 为什么留空（技能侧写的说明）
	AngleZH      string   `json:"angle_zh"`  // 机位角度中文短标签（平视/仰拍/俯拍/鸟瞰）——不是契约字段
	Camera       string   `json:"camera"`
	HookType     string   `json:"hook_type"`
	EndState     string   `json:"end_state"`
	Action       string   `json:"action"`
	ActionEN     string   `json:"action_en"`
	Emotion      string   `json:"emotion"`
	Sound        string   `json:"sound"`
	Dialogue     []struct {
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
		OS      bool   `json:"os"`
	} `json:"dialogue"`
	Narration string `json:"narration"`
	// H3Prompt 每镜的**六段式/三段式全文**（技能侧 2026-09-27 补齐，对应他们的 R1）。
	// **这是渲染真正吃的东西** —— 明细能不能当 plan 用，就看有没有这个字段。
	H3Prompt string `json:"h3_prompt"`
	// SceneLandmarks 真文件里是**字符串数组**（`["City gate tower","Stone arch bridge"]`）。
	// 收成 any 是吃过一次亏的：先前声明成 string → 整份 2.3MB JSON 的 Unmarshal 直接失败，
	// 页签看起来"什么都没读到"，而**夹具测试却是绿的**（夹具里我自己写成了字符串）。
	// 教训：夹具必须照**真实形态**写，否则测的是我脑子里那个文件，不是盘上那个。
	SceneLandmarks any    `json:"scene_landmarks"`
	Light          string `json:"light"`
}

// landmarksText 地标字段 → 一行文字（容忍数组/单字符串/空）。
func landmarksText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case []string:
		return strings.Join(t, " · ")
	case []any:
		parts := make([]string, 0, len(t))
		for _, it := range t {
			if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
		return strings.Join(parts, " · ")
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

// detailEpisodes 明细里的集清单（按集号排序；`dir` 用**原始集号字符串**，供前端当键用）。
func detailEpisodes(shots []scriptDetailShot) []scriptEpView {
	type agg struct {
		ep  string
		cnt int
	}
	order := []string{}
	byKey := map[string]*agg{}
	for _, s := range shots {
		k := epKeyOf(s.Episode)
		if k == "" {
			k = "1"
		}
		if _, ok := byKey[k]; !ok {
			byKey[k] = &agg{ep: k}
			order = append(order, k)
		}
		byKey[k].cnt++
	}
	sort.Slice(order, func(i, j int) bool {
		ni, ei := strconv.Atoi(order[i])
		nj, ej := strconv.Atoi(order[j])
		if ei == nil && ej == nil && ni != nj {
			return ni < nj
		}
		return order[i] < order[j]
	})
	out := make([]scriptEpView, 0, len(order))
	for _, k := range order {
		a := byKey[k]
		out = append(out, scriptEpView{Episode: detailEpLabel(k), Dir: k, Shots: a.cnt})
	}
	return out
}

// detailEpLabel 集号 → 「第NN集」（明细里是数字字符串）。
func detailEpLabel(k string) string {
	if n, err := strconv.Atoi(k); err == nil {
		return fmt.Sprintf("第%02d集", n)
	}
	return k
}

// detailShotViews 把明细里的某一集转成页签用的镜头视图（带上新字段）。
func detailShotViews(shots []scriptDetailShot, want string) []scriptShotView {
	out := []scriptShotView{}
	for _, s := range shots {
		k := epKeyOf(s.Episode)
		if k == "" {
			k = "1"
		}
		if want != k && want != detailEpLabel(k) {
			continue
		}
		v := scriptShotView{
			ID: s.ID, Size: s.ShotSize, Duration: s.Duration, Mode: s.Mode,
			Action: s.Action, Narration: s.Narration, Chars: s.Characters,
			View: s.View, ViewNote: s.ViewNote, AngleZH: s.AngleZH,
			HookType: s.HookType, EndState: s.EndState,
			Landmarks: landmarksText(s.SceneLandmarks), Light: s.Light, SceneName: s.Scene,
		}
		if v.Mode == "" {
			v.Mode = "ref2va"
		}
		for _, d := range s.Dialogue {
			if strings.TrimSpace(d.Text) == "" {
				continue
			}
			who := strings.TrimSpace(d.Speaker)
			if who == "" {
				who = "?"
			}
			if d.OS {
				who += "(画外)"
			}
			v.Dialogue = append(v.Dialogue, who+":"+d.Text)
		}
		out = append(out, v)
	}
	return out
}

// handleNovelScript GET /api/novels/script?path=<书目录>[&episode=<包目录>]
//
// 小说库详情弹窗的「剧本脚本 / 角色 / 场景」数据源(2026-09-26 用户要求:
// 「剧本脚本、角色、场景相关内容也需要可查看详情内容」)。
//
// 数据来自**技能端产出的脚本包**(`manifest.json` + `plan/plan.json`),不重新跑 LLM:
// 那是这本书已经定稿的东西,弹窗只是把它**读出来给人看**。分两次取:
//   - 不带 episode:返回集清单 + 角色/场景汇总(跨集去重、统计各出现在几集);
//   - 带 episode:额外返回**这一集的镜头明细**(动作/台词/旁白/景别/时长/h3_prompt)。
//
// 为什么要分层:一集 60+ 镜、40 集就是 2500+ 条,连提示词一起塞进一个响应是几百 KB 的白等;
// 用户点哪一集就只取哪一集。
func (s *Server) handleNovelScript(w http.ResponseWriter, r *http.Request) {
	book := strings.TrimSpace(r.URL.Query().Get("path"))
	if !s.inNovelRoot(book) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
		return
	}
	// 技能侧的**新交付物**（素材三卡 / 分镜 markdown）先收集：它们与渲染包是两条线，
	// 一本书可能只有其中之一（技能侧 v2.14 起就常常只有新产物）。
	assets, storyboard, pkgView := collectNovelDeliverables(book)
	hasDeliverables := len(assets) > 0 || len(storyboard) > 0
	// 结构化明细（技能侧新交的 `分镜/分镜明细.json`）：有它就用它出集清单与镜头明细 ——
	// 它的字段与平台契约同名（view/hook_type/end_state/landmarks/light），比旧 plan.json 全。
	detail := loadNovelDetail(book)
	var detailShots []scriptDetailShot
	if detail != nil {
		detailShots = detail.Shots
	}
	wantEp := strings.TrimSpace(r.URL.Query().Get("episode"))

	bs := s.findBookScript(book)
	if bs == nil {
		// ① 没有渲染包，但有**结构化明细** → 直接用它服务（这正是不再"功能看起来没了"的那条路）
		if len(detailShots) > 0 {
			chars, scenes, props := detailCardViews(detail)
			out := map[string]any{
				"found": true, "kind": "detail-json", "source": novelShotDetailJSON,
				"episodes":   detailEpisodes(detailShots),
				"characters": chars, "scenes": scenes, "props": props, "deliverables": map[string]any{"assets": assets, "storyboard": storyboard},
				"package": pkgView,
				"hint": "这本书没有渲染脚本包，镜头明细来自技能侧的结构化明细 " + novelShotDetailJSON +
					"（字段与平台契约同名）；渲染请走「视频制作」（平台 LLM 直出）",
			}
			if wantEp != "" {
				out["shots"] = detailShotViews(detailShots, wantEp)
			}
			writeJSON(w, 200, out)
			return
		}
		// ② 没有渲染包，但盘上有技能侧新产物 → **仍然算"有东西可看"**。
		// 这是本页签 2026-09-27 之前的一个真缺口:技能侧把交付面换成 markdown 之后,
		// 界面只会说"没有技能端脚本包",用户以为功能没了。
		if hasDeliverables {
			writeJSON(w, 200, map[string]any{
				"found": true, "kind": "deliverables", "episodes": []any{},
				"deliverables": map[string]any{"assets": assets, "storyboard": storyboard},
				"package":      pkgView,
				"hint": "这本书没有**渲染脚本包**（manifest.json + plan/plan.json），" +
					"但技能侧已经产出了下面的交付物；渲染请走「视频制作」（平台 LLM 直出）",
			})
			return
		}
		writeJSON(w, 200, map[string]any{"found": false,
			"hint": "这本书目录里既没有渲染脚本包(manifest.json + plan/plan.json)，也没有技能侧交付物(素材/*.md、分镜/*.md)"})
		return
	}
	pkgs, err := collectPackages(bs.Root)
	if err != nil || len(pkgs) == 0 {
		writeJSON(w, 200, map[string]any{"found": false, "hint": "脚本目录里没有可读的集包"})
		return
	}
	pkgs = dedupStrings(pkgs)
	sort.Strings(pkgs)

	eps := make([]scriptEpView, 0, len(pkgs))
	chars := map[string]*scriptCharView{}
	scenes := map[string]*scriptSceneView{}
	props := map[string]*scriptPropView{}
	var order []string // 角色/场景的首次出现顺序(池子顺序是书里的设定顺序,别按字典排乱)
	var scOrder []string
	var prOrder []string

	for _, pkg := range pkgs {
		plan := loadPlanLenient(pkg)
		if plan == nil {
			continue
		}
		ep := filepath.Base(pkg)
		title := strings.TrimSpace(plan.Title)
		eps = append(eps, scriptEpView{Episode: epNoOf(ep), Title: title, Dir: pkg, Shots: len(plan.Shots)})
		for _, c := range plan.Characters {
			key := orDefault(c.ID, c.Name)
			if v, ok := chars[key]; ok {
				v.In++
				continue
			}
			chars[key] = &scriptCharView{ID: c.ID, Name: c.Name, Kind: c.Kind, Desc: c.Description,
				Voice: c.Voice, Prompt: c.ImagePrompt, In: 1}
			order = append(order, key)
		}
		for _, sc := range plan.Scenes {
			key := orDefault(sc.ID, sc.Name)
			if v, ok := scenes[key]; ok {
				v.In++
				continue
			}
			scenes[key] = &scriptSceneView{ID: sc.ID, Name: sc.Name, Desc: sc.Description, In: 1}
			scOrder = append(scOrder, key)
		}
		// 道具:按 ID 去重(与角色/场景同一套口径 —— 技能侧的池子是**全书共享**的
		// _pool.json,id 跨集稳定,不会把两集的不同道具并成一条)
		for _, p := range plan.Props {
			key := orDefault(p.ID, p.Name)
			if v, ok := props[key]; ok {
				v.In++
				continue
			}
			props[key] = &scriptPropView{ID: p.ID, Name: p.Name, EN: p.EN,
				Desc: p.Description, Prompt: p.ImagePrompt, In: 1}
			prOrder = append(prOrder, key)
		}
	}
	out := map[string]any{
		"found": true, "root": bs.Root, "kind": bs.Kind, "book": bs.Book, "version": bs.Version,
		"episodes": eps, "characters": orderChars(chars, order), "scenes": orderScenes(scenes, scOrder),
		"props": orderProps(props, prOrder),
	}
	// 技能侧交付物 + 渲染包状态：与上面的集清单并列返回（新产物那半与渲染包无关，
	// 只影响"界面能不能看见/读它们"）。
	pkgView.Present = true
	pkgView.Count = len(eps)
	out["deliverables"] = map[string]any{"assets": assets, "storyboard": storyboard}
	out["package"] = pkgView
	if bs.Deprecated {
		out["deprecated"] = true
	}
	// 结构化明细优先（技能侧 2026-09-27 新交，字段与契约同名、比旧 plan.json 全）：
	// 有它就覆盖集清单与镜头明细，包那半只留角色/场景/道具卡与"包是否废弃"的标记。
	if len(detailShots) > 0 {
		out["source"] = novelShotDetailJSON
		out["episodes"] = detailEpisodes(detailShots)
		// 三类卡也以明细内嵌的那份为准（旧包的卡没有 scene 的地标/光态，道具也只有 en）
		if chars, scenes, props := detailCardViews(detail); len(chars)+len(scenes)+len(props) > 0 {
			out["characters"] = chars
			out["scenes"] = scenes
			out["props"] = props
		}
		if wantEp != "" {
			out["shots"] = detailShotViews(detailShots, wantEp)
			out["h3_prompt_of"] = detailEpLabel(wantEp)
		}
		writeJSON(w, 200, out)
		return
	}
	// 按需取某一集的镜头明细
	if wantEp != "" {
		target := ""
		for _, p := range pkgs {
			if p == wantEp || filepath.Base(p) == wantEp {
				target = p
				break
			}
		}
		if target == "" {
			writeErr(w, 404, fmt.Errorf("这本书里没有这一集:%s", wantEp))
			return
		}
		plan := loadPlanLenient(target)
		if plan == nil {
			writeErr(w, 500, fmt.Errorf("这一集的 plan.json 读不出来:%s", target))
			return
		}
		out["shots"] = shotViews(plan)
		out["h3_prompt_of"] = orDefault(plan.Title, filepath.Base(target))
	}
	writeJSON(w, 200, out)
}

// epNoOf 从包目录名里取「第NN集」。
//
// 目录名的形态是 `<书名>·第01集·<集标题>`(也可能用 `_` 或直接以「第」开头),
// 所以判据是**从「第」取到「集」为止**,而不是"到下一个分隔符为止" ——
// 后者在 `<书>·第01集·x` 上会先撞上紧跟在「第」前面的那个分隔符,
// 结果返回一整串带前导点的名字(真机首测就是这个:'·第01集·求你们骂我')。
func epNoOf(dir string) string {
	n := filepath.Base(dir)
	i := strings.Index(n, "第")
	if i < 0 {
		return n
	}
	rest := n[i:]
	if j := strings.Index(rest, "集"); j > 0 {
		return rest[:j+len("集")]
	}
	// 没有「集」字(如「第3话」):取到下一个分隔符
	for _, sep := range []string{"·", "_", "-"} {
		if j := strings.Index(rest, sep); j > 0 {
			return rest[:j]
		}
	}
	return rest
}

func orderChars(m map[string]*scriptCharView, order []string) []*scriptCharView {
	out := make([]*scriptCharView, 0, len(m))
	for _, k := range order {
		if v, ok := m[k]; ok {
			out = append(out, v)
		}
	}
	return out
}

func orderScenes(m map[string]*scriptSceneView, order []string) []*scriptSceneView {
	out := make([]*scriptSceneView, 0, len(m))
	for _, k := range order {
		if v, ok := m[k]; ok {
			out = append(out, v)
		}
	}
	return out
}

func orderProps(m map[string]*scriptPropView, order []string) []*scriptPropView {
	out := make([]*scriptPropView, 0, len(m))
	for _, k := range order {
		if v, ok := m[k]; ok {
			out = append(out, v)
		}
	}
	return out
}

// shotViews 一集的分镜明细(动作/台词/旁白/景别/时长/mode + 原始 h3_prompt)。
func shotViews(plan *project.Plan) []scriptShotView {
	out := make([]scriptShotView, 0, len(plan.Shots))
	for _, sh := range plan.Shots {
		v := scriptShotView{ID: sh.ID, Size: sh.ShotSize, Duration: sh.Duration,
			Mode: project.EffectiveShotMode(sh), Action: sh.Action, Narration: sh.Narration,
			Chars: sh.Characters, Prompt: sh.H3Prompt}
		for _, d := range sh.Dialogue {
			if strings.TrimSpace(d.Text) == "" {
				continue
			}
			who := strings.TrimSpace(d.Speaker)
			if who == "" {
				who = "?"
			}
			v.Dialogue = append(v.Dialogue, who+":"+d.Text)
		}
		out = append(out, v)
	}
	return out
}

// loadPlanLenient 宽松地读一个包的 plan.json。
//
// 与导入路径上的 readPackage **故意不同**:导入要过契约校验(坏包拒收),而这里是**给人看**
// —— 契约不合的包恰恰更需要被看见(用户要判断"技能端到底写了什么"),所以解析失败才放弃,
// 字段缺漏照原样显示。
func loadPlanLenient(pkgDir string) *project.Plan {
	raw, err := os.ReadFile(filepath.Join(pkgDir, "plan", "plan.json"))
	if err != nil {
		return nil
	}
	var p project.Plan
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	return &p
}

// epKeyOf 集号 → 排序/匹配用的字符串键（数字与字符串两种写法都认）。
func epKeyOf(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case float64:
		if t == float64(int(t)) {
			return strconv.Itoa(int(t))
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case json.Number:
		return t.String()
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}
