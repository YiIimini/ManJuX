package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 小说库「脚本创作」入口闸门(2026-09-26 用户要求:"完善小说库里的入口,新增一个入口")。
//
// 这个入口的意义是**省掉 LLM 拆集**:技能端已经出了脚本,平台解析到就直接导包
// (契约见 docs/渲染脚本契约.md)。所以要钉住三件事:
//  ① 书目录里真能认出包(单集包 / 系列包 / 没有包);
//  ② 认不出来时**明确报错,不回落到 LLM**(点了"脚本创作"却悄悄跑几分钟推理是最坏结果);
//  ③ 导入走的是**同一条路**(importPackageRoot),不另写一套校验。

// mkBookWithScript 造一本书,并按需在里面放 N 个技能端包。
func mkBookWithScript(t *testing.T, s *Server, name string, sub string, eps int) string {
	t.Helper()
	book := filepath.Join(s.novelRoot(), name)
	if err := os.MkdirAll(book, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= eps; i++ {
		ep := filepath.Join(sub, epName(i))
		mkPackage(t, book, ep, name, epName(i))
	}
	return book
}

func epName(i int) string {
	if i < 10 {
		return "第0" + string(rune('0'+i)) + "集"
	}
	return "第" + string(rune('0'+i/10)) + string(rune('0'+i%10)) + "集"
}

// TestNovelScriptListsSkillDeliverables 技能侧**新交付物**（素材三卡 / 分镜 markdown）必须可见。
//
// 2026-09-27 用户：「技能侧的脚本重新输出了，小说库里的剧本创作功能回归」。
// 技能侧 v2.14 把交付面换成 markdown（`素材/{角色卡,场景卡,道具卡}.md`、
// `分镜/{第NN集.md,H3-提示词.md,…}`），而本页签原先**只认渲染包** —— 盘上明明有新产物，
// 界面却只说"没有技能端脚本包"，功能看起来没了。这条钉住三种形态：
//
//	① 只有新交付物 → found=true + 列出文件（不再谎报"什么都没有"）；
//	② 交付物 + 渲染包 → 两者都返回，且包的"已废弃"标记如实带上；
//	③ 两者都没有 → found=false。
func TestNovelScriptListsSkillDeliverables(t *testing.T) {
	s, _ := newHiddenTestServer(t)

	write := func(book, rel, body string) {
		full := filepath.Join(book, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	get := func(book string) map[string]any {
		t.Helper()
		code, m := doReq(t, s, "GET", "/api/novels/script?path="+urlEnc(book), nil)
		if code != 200 {
			t.Fatalf("读脚本页签应 200，实际 %d：%v", code, m)
		}
		return m
	}
	namesOf := func(m map[string]any, group string) []string {
		dl, _ := m["deliverables"].(map[string]any)
		items, _ := dl[group].([]any)
		out := make([]string, 0, len(items))
		for _, it := range items {
			if mm, ok := it.(map[string]any); ok {
				out = append(out, mm["name"].(string))
			}
		}
		return out
	}

	// ① 只有交付物
	only := filepath.Join(s.novelRoot(), "只有交付物")
	write(only, "素材/角色卡.md", "# 角色卡\n")
	write(only, "分镜/第01集.md", "| 镜头编号 |\n")
	write(only, "分镜/第02集.md", "| 镜头编号 |\n")
	write(only, "分镜/H3-提示词.md", "…\n")
	m := get(only)
	if found, _ := m["found"].(bool); !found {
		t.Fatalf("有交付物时必须 found=true（否则界面谎报「什么都没有」）：%v", m)
	}
	if got := namesOf(m, "assets"); len(got) != 1 || got[0] != "角色卡.md" {
		t.Errorf("素材交付物应列出角色卡.md，实际 %v", got)
	}
	// 分镜按集号排（第02集不能排到第10集前面那种错法在这里就被挡住）
	if got := namesOf(m, "storyboard"); len(got) != 3 || got[0] != "第01集.md" || got[1] != "第02集.md" {
		t.Errorf("分镜交付物应按集号排序，实际 %v", got)
	}

	// ② 交付物 + 渲染包（且包被技能侧标成废弃）
	both := mkBookWithScript(t, s, "交付物加包", "漫剧脚本", 2)
	write(both, "素材/场景卡.md", "# 场景卡\n")
	write(both, "漫剧脚本/_已废弃-说明.md", "已废弃\n")
	m = get(both)
	if got := namesOf(m, "assets"); len(got) != 1 {
		t.Errorf("应同时列出素材交付物，实际 %v", got)
	}
	if eps, _ := m["episodes"].([]any); len(eps) != 2 {
		t.Errorf("应同时返回集清单（2 集），实际 %v", m["episodes"])
	}
	pkg, _ := m["package"].(map[string]any)
	if pkg == nil || pkg["present"] != true {
		t.Errorf("package.present 应为 true，实际 %v", m["package"])
	}
	if dep, _ := m["deprecated"].(bool); !dep {
		t.Errorf("技能侧放了 _已废弃 说明文件时，必须如实带上废弃标记：%v", m["package"])
	}
	if bs := s.findBookScript(both); bs == nil || !bs.Deprecated || bs.DeprecatedHint == "" {
		t.Errorf("书卡上的脚本信息也要带废弃标记与说明，实际 %+v", bs)
	}

	// ③ 什么都没有
	empty := filepath.Join(s.novelRoot(), "空书")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if found, _ := get(empty)["found"].(bool); found {
		t.Error("既没有包也没有交付物时必须 found=false")
	}
}

// TestNovelScriptPrefersStructuredDetail 技能侧的**结构化明细**优先于旧渲染包（追补单 N3）。
//
// 依据：技能侧 2026-09-27 新交 `分镜/分镜明细.json`（2462 镜 / 40 集），字段名直接用平台契约的
// `view` / `hook_type` / `end_state`，另有 `scene_landmarks` / `light`；而渲染包里的 plan.json 是
// 停用前的旧形态（没有这几个字段）。所以页签的镜头明细要读明细，**包只用来补角色/场景/道具卡**。
//
// 同时钉住两件容易漏的：① 明细文件本身要在"技能侧交付"里看得见（原先的 `.md` 过滤器把它挡掉了）；
// ② 只有明细、没有包的书也要能打开页签（found=true）。
func TestNovelScriptPrefersStructuredDetail(t *testing.T) {
	s, _ := newHiddenTestServer(t)
	writeFile := func(book, rel, body string) {
		full := filepath.Join(book, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	detail := `{"generated":"2026-09-27","engine":"h3","note":"机械派生","shots":[
	  {"episode":1,"id":1,"duration":5,"shot_size":"特写","scene":"临水城","characters":["Shen Yan"],
	   "mode":"ref2va","view":"telephoto compression","view_note":"","angle_zh":"平视","camera":"Static, no movement","hook_type":"reveal",
	   "end_state":"he stands at the threshold","action":"他站在门槛前","action_en":"he stands",
	   "emotion":"calm","sound":"wind","dialogue":[{"speaker":"沈砚","text":"我准了","os":false}],
	   "narration":"","scene_landmarks":["gate tower","stone arch bridge"],"light":"3000K warm side-backlight"},
	  {"episode":2,"id":1,"duration":6,"shot_size":"中景","scene":"城隍庙","characters":[],
	   "mode":"t2va","view":"","view_note":"景别 中景 无对应视角","angle_zh":"俯拍","camera":"static","hook_type":"setup",
	   "end_state":"empty hall","action":"空镜","action_en":"empty hall",
	   "emotion":"","sound":"","dialogue":[],"narration":"三年了。",
	   "scene_landmarks":["altar","incense burner"],"light":"7000K moon"}]}`

	// ① 有包 + 有明细：明细优先，包补卡
	both := mkBookWithScript(t, s, "两者都有", "漫剧脚本", 2)
	writeFile(both, "分镜/分镜明细.json", detail)
	_, m := doReq(t, s, "GET", "/api/novels/script?path="+urlEnc(both), nil)
	if m["source"] != "分镜明细.json" {
		t.Errorf("应标明数据来源是结构化明细，实际 %v", m["source"])
	}
	if eps, _ := m["episodes"].([]any); len(eps) != 2 {
		t.Errorf("明细里有 2 集，实际 %v", m["episodes"])
	}
	if pkg, _ := m["package"].(map[string]any); pkg == nil || pkg["present"] != true {
		t.Errorf("包仍在盘上时 package.present 应为 true（角色/场景卡还要用它），实际 %v", m["package"])
	}
	if chars, _ := m["characters"].([]any); len(chars) == 0 {
		t.Error("角色卡那半仍应由渲染包提供（明细里没有卡）")
	}
	// 交付物清单里要看得见这个 json（原先只收 .md，正是 N3 报的缺口）
	names := map[string]bool{}
	if dl, ok := m["deliverables"].(map[string]any); ok {
		for _, group := range []string{"assets", "storyboard"} {
			if items, ok := dl[group].([]any); ok {
				for _, it := range items {
					if mm, ok := it.(map[string]any); ok {
						names[mm["name"].(string)] = true
					}
				}
			}
		}
	}
	if !names["分镜明细.json"] {
		t.Errorf("技能侧交付里应列出 分镜明细.json，实际 %v", names)
	}

	// ② 某一集的镜头明细要走明细（新字段必须真的带到前端）
	_, m2 := doReq(t, s, "GET", "/api/novels/script?path="+urlEnc(both)+"&episode=1", nil)
	shots, _ := m2["shots"].([]any)
	if len(shots) != 1 {
		t.Fatalf("第 1 集应有 1 镜，实际 %v", m2["shots"])
	}
	s0, _ := shots[0].(map[string]any)
	for k, want := range map[string]string{
		"view": "telephoto compression", "angle_zh": "平视", "hook_type": "reveal", "end_state": "he stands at the threshold",
		"landmarks": "gate tower · stone arch bridge", "light": "3000K warm side-backlight",
	} {
		if got, _ := s0[k].(string); got != want {
			t.Errorf("镜头明细缺 %s：得到 %q，期望 %q", k, got, want)
		}
	}
	if d, _ := s0["dialogue"].([]any); len(d) != 1 || d[0] != "沈砚:我准了" {
		t.Errorf("台词应转成「说话人:台词」，实际 %v", s0["dialogue"])
	}
	// 集号也能用「第NN集」形态取（前端显示的是这个）
	_, m3 := doReq(t, s, "GET", "/api/novels/script?path="+urlEnc(both)+"&episode="+urlEnc("第01集"), nil)
	if shots3, _ := m3["shots"].([]any); len(shots3) != 1 {
		t.Errorf("用「第01集」也应取到第 1 集，实际 %v", m3["shots"])
	}

	// ③ 只有明细、没有包：页签照样要能打开
	only := filepath.Join(s.novelRoot(), "只有明细")
	writeFile(only, "分镜/分镜明细.json", detail)
	_, m4 := doReq(t, s, "GET", "/api/novels/script?path="+urlEnc(only), nil)
	if found, _ := m4["found"].(bool); !found {
		t.Fatalf("只有结构化明细时 found 应为 true，实际 %v", m4)
	}
	if eps, _ := m4["episodes"].([]any); len(eps) != 2 {
		t.Errorf("只有明细时也要给出集清单，实际 %v", m4["episodes"])
	}
}

// TestFindBookScriptDetects 三种形态:书目录本身就是单集包 / 子目录里有多集包 / 什么都没有。
func TestFindBookScriptDetects(t *testing.T) {
	s, _ := newHiddenTestServer(t)

	// ① 没有包
	plain := mkBookWithScript(t, s, "无脚本", "脚本", 0)
	if bs := s.findBookScript(plain); bs != nil {
		t.Errorf("没有包的书不该报出脚本:%+v", bs)
	}

	// ② 单集包就在书目录里(sub="." → 包直接落在书目录)
	one := mkBookWithScript(t, s, "单集书", ".", 1)
	bs := s.findBookScript(one)
	if bs == nil || bs.Kind != "episode" || bs.Episodes != 1 {
		t.Fatalf("书目录本身就是单集包时应识别为 episode/1,实际 %+v", bs)
	}
	if bs.Book == "" {
		t.Errorf("应读出包身份(书名;manifest 缺失时用目录名兜底),实际 %+v", bs)
	}

	// ③ 子目录里多集包:Root 必须是**包的父目录**(书/脚本),导入器才认
	three := mkBookWithScript(t, s, "连载书", "脚本", 3)
	bs = s.findBookScript(three)
	if bs == nil || bs.Kind != "series" || bs.Episodes != 3 {
		t.Fatalf("子目录里 3 个包应识别为 series/3,实际 %+v", bs)
	}
	if bs.Root != filepath.Join(three, "脚本") {
		t.Errorf("Root 应是包的父目录,实际 %q", bs.Root)
	}
	if got, _ := collectPackages(bs.Root); len(got) != bs.Episodes {
		t.Errorf("检测说 %d 个包,导入器只认 %d 个 —— 检测与导入必须看同一层", bs.Episodes, len(got))
	}

	// ④ 库外路径一律拒绝(这条路的参数会一路走到写盘)
	outside := t.TempDir()
	mkPackage(t, outside, "脚本", "外书", epName(1))
	if bs := s.findBookScript(filepath.Join(outside, "外书")); bs != nil {
		t.Error("小说库之外的路径不该被认成脚本包")
	}
	if bs := s.findBookScript(""); bs != nil {
		t.Error("空路径不该被认成脚本包")
	}
	if bs := s.findBookScript(filepath.Join(s.novelRoot(), "无脚本", "正文", "第001章_一.md")); bs != nil {
		t.Error("单文件路径不该被认成脚本包")
	}
}

// TestProduceScriptRoute 入口行为:没包 → 400 且说清要什么;有包 → 交给同一条导入路。
func TestProduceScriptRoute(t *testing.T) {
	s, _ := newHiddenTestServer(t)

	// 缺 path → 400
	rec := httptest.NewRecorder()
	s.handleNovelProduceScript(rec, httptest.NewRequest(http.MethodPost, "/api/novels/produce-script",
		strings.NewReader(`{}`)))
	if rec.Code != 400 {
		t.Errorf("缺 path 应 400,实际 %d", rec.Code)
	}

	// 目录里没有包 → 400,且消息里点名需要的文件(而不是 200 后什么都没发生)
	plain := mkBookWithScript(t, s, "无脚本", "脚本", 0)
	rec = httptest.NewRecorder()
	s.handleNovelProduceScript(rec, httptest.NewRequest(http.MethodPost, "/api/novels/produce-script",
		strings.NewReader(`{"path":`+jsonStr(plain)+`}`)))
	if rec.Code != 400 {
		t.Fatalf("没有脚本包应 400,实际 %d: %s", rec.Code, rec.Body.String())
	}
	msg := rec.Body.String()
	for _, want := range []string{"manifest.json", "plan/plan.json"} {
		if !strings.Contains(msg, want) {
			t.Errorf("报错应说清需要什么文件(%q),实际:%s", want, msg)
		}
	}

	// 有包 → 200 且真的导入了(项目列表里出现这一集)
	book := mkBookWithScript(t, s, "有脚本", "脚本", 1)
	rec = httptest.NewRecorder()
	s.handleNovelProduceScript(rec, httptest.NewRequest(http.MethodPost, "/api/novels/produce-script",
		strings.NewReader(`{"path":`+jsonStr(book)+`}`)))
	if rec.Code != 200 {
		t.Fatalf("有脚本包应 200,实际 %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不可解析: %v  raw=%s", err, rec.Body.String())
	}
	if len(out.Results) != 1 || out.Results[0].ID == "" {
		t.Errorf("应导入 1 个包并给出项目 id,实际 %+v", out)
	}
	if got := listIDs(t, s); len(got) != 1 {
		t.Errorf("导入后项目列表应有 1 项,实际 %v", got)
	}
}
