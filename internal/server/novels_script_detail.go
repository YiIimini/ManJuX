package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	bs := s.findBookScript(book)
	if bs == nil {
		writeJSON(w, 200, map[string]any{"found": false,
			"hint": "这本书目录里没有技能端脚本包(需要 manifest.json + plan/plan.json)"})
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
	var order []string // 角色/场景的首次出现顺序(池子顺序是书里的设定顺序,别按字典排乱)
	var scOrder []string

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
	}
	out := map[string]any{
		"found": true, "root": bs.Root, "kind": bs.Kind, "book": bs.Book, "version": bs.Version,
		"episodes": eps, "characters": orderChars(chars, order), "scenes": orderScenes(scenes, scOrder),
	}
	// 按需取某一集的镜头明细
	if want := strings.TrimSpace(r.URL.Query().Get("episode")); want != "" {
		target := ""
		for _, p := range pkgs {
			if p == want || filepath.Base(p) == want {
				target = p
				break
			}
		}
		if target == "" {
			writeErr(w, 404, fmt.Errorf("这本书里没有这一集:%s", want))
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
