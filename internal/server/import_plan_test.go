package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// import_plan_test.go 双侧共组的入口契约:技能端产出的 plan 从这一条路进平台。
//
// 这一层测的是**分工的接缝**,不是业务逻辑:
//  1. 坏包必须被拒(悬空引用这类"技能端写自己的、平台端认不得"的东西);
//  2. 好包必须被后处理到**与平台自产包完全等价**(台词时长预算 + 中文台词标记),
//     否则"导进来的"和"自己生成的"在渲染眼里是两种东西,双边分工就名存实亡;
//  3. 技能端直接写盘的项目,必须能靠 rescan 被平台看见(不必重启应用)。

func mkPlan() map[string]any {
	return map[string]any{
		"title": "测试集", "logline": "一句话", "style": "PHOTOREAL 3D WORLD",
		"characters": []map[string]any{{
			"id": "char_a", "name": "阿甲", "kind": "human",
			"description": "二十岁男子", "image_prompt": "a young man", "voice": "young male",
		}},
		"scenes": []map[string]any{{
			"id": "scene_room", "name": "破庙", "description": "潮湿的破庙",
			"image_prompt": "a ruined temple",
		}},
		"shots": []map[string]any{{
			"id": 1, "scene": "scene_room", "characters": []string{"char_a"},
			"shot_size": "中景", "camera": "slow push-in", "action": "他站起来",
			"dialogue":  []map[string]any{{"speaker": "阿甲", "text": "我准了。"}},
			"narration": "", "duration": 5, "mode": "ref2va",
			// 故意不带 [Chinese] 标记:入口必须补上(与 script 阶段同一处理)
			"h3_prompt": "subject_definitions:\n<d>我准了。</d>",
		}},
	}
}

func newProjectForTest(t *testing.T, s *Server, name string) string {
	t.Helper()
	code, out := doReq(t, s, "POST", "/api/projects", map[string]any{"name": name})
	if code != 200 {
		t.Fatalf("建项目失败: %d %v", code, out)
	}
	id, _ := out["id"].(string)
	if id == "" {
		if p, ok := out["project"].(map[string]any); ok {
			id, _ = p["id"].(string)
		}
	}
	if id == "" {
		t.Fatalf("建项目响应里没有 id: %v", out)
	}
	return id
}

// TestImportPlanRejectsBadContract 坏包必须 400,且**不许落盘**。
//
// 为什么"不许落盘"要单独断言:落盘了的话,用户点"开始渲染"照样能跑 ——
// 校验就成了只挡一时、挡不住一路的装饰。
func TestImportPlanRejectsBadContract(t *testing.T) {
	s := newTestServer(t)
	id := newProjectForTest(t, s, "导入契约坏包")
	bad := mkPlan()
	bad["shots"].([]map[string]any)[0]["scene"] = "scene_nope" // 悬空场景引用
	code, out := doReq(t, s, "POST", "/api/projects/"+id+"/import-plan",
		map[string]any{"plan": bad})
	if code != 400 {
		t.Fatalf("悬空场景引用必须 400,实际 %d(%v)", code, out)
	}
	fails, _ := out["fails"].([]any)
	if len(fails) == 0 {
		t.Fatalf("响应必须列出具体哪一条 fail,实际 %v", out)
	}
	if joined := strings.Join(anyStrings(fails), ";"); !strings.Contains(joined, "不存在的场景") {
		t.Fatalf("fail 文案要点名悬空引用,实际 %s", joined)
	}
	if _, err := s.store.LoadPlan(id); err == nil {
		t.Fatal("校验失败却把方案落盘了 —— 校验必须挡在存盘之前")
	}
}

// TestImportPlanPostProcessesLikeScriptStage 好包导入后必须与自产包等价。
func TestImportPlanPostProcessesLikeScriptStage(t *testing.T) {
	s := newTestServer(t)
	id := newProjectForTest(t, s, "导入契约好包")
	code, out := doReq(t, s, "POST", "/api/projects/"+id+"/import-plan",
		map[string]any{"plan": mkPlan()})
	if code != 200 {
		t.Fatalf("合规方案应当 200,实际 %d(%v)", code, out)
	}
	if got, _ := out["shots"].(float64); got != 1 {
		t.Fatalf("响应 shots 应为 1,实际 %v", out["shots"])
	}
	plan, err := s.store.LoadPlan(id)
	if err != nil {
		t.Fatalf("方案未落盘: %v", err)
	}
	// ① 中文台词标记兜底(与 stages_script 同一个 EnsureChineseDialogue)
	if !strings.Contains(plan.Shots[0].H3Prompt, "<d>[Chinese]") {
		t.Fatalf("入口没补 [Chinese] 标记,成片会说听不懂的话: %q", plan.Shots[0].H3Prompt)
	}
	if !strings.Contains(plan.Shots[0].H3Prompt, "MANDARIN ONLY") {
		t.Fatal("入口没追加普通话强制句")
	}
	// ② 台词时长预算("我准了。"只要 4 秒,5 秒镜头不该被改)
	if det, _ := out["duration_details"].([]any); len(det) != 0 {
		t.Fatalf("只装得下的台词不该被改时长,实际调整 %v(%v)", det, out)
	}
	// ③ 契约信息如实回显
	if c, _ := out["contract"].(string); !strings.Contains(c, "manjux.plan") {
		t.Fatalf("响应必须回显契约版本,实际 %v", out["contract"])
	}
}

// TestImportPlanFitsDialogueDuration 台词装不下时入口按平台规则抬时长,抬不动要如实报。
func TestImportPlanFitsDialogueDuration(t *testing.T) {
	s := newTestServer(t)
	id := newProjectForTest(t, s, "导入契约台词")
	p := mkPlan()
	sh := p["shots"].([]map[string]any)[0]
	sh["duration"] = 4
	sh["dialogue"] = []map[string]any{{"speaker": "阿甲", "text": strings.Repeat("字", 30)}}
	code, out := doReq(t, s, "POST", "/api/projects/"+id+"/import-plan",
		map[string]any{"plan": p})
	if code != 200 {
		t.Fatalf("应当 200(抬时长是平台的正常动作),实际 %d(%v)", code, out)
	}
	// 时长调整明细要如实回显(改了哪一镜、从几秒到几秒)—— 只回一个数字等于让用户猜
	det, _ := out["duration_details"].([]any)
	if len(det) != 1 {
		t.Fatalf("30 字台词装不进 4 秒,应调整 1 镜并给出明细,实际 %v(%v)",
			out["duration_details"], out)
	}
	if s0, _ := det[0].(string); !strings.Contains(s0, "7") {
		t.Fatalf("调整明细应写明时长变化(4→7 秒),实际 %q", s0)
	}
	plan, _ := s.store.LoadPlan(id)
	// 30 字 ÷ 4.5 + 0.3 = 6.97 → 7 秒
	if plan.Shots[0].Duration != 7 {
		t.Fatalf("时长应被抬到 7 秒,实际 %d", plan.Shots[0].Duration)
	}

	// 抬不动的:60 字需约 13.6 秒 > 10 秒上限 → 如实报 overflow,不许静默
	id2 := newProjectForTest(t, s, "导入契约超长台词")
	p2 := mkPlan()
	sh2 := p2["shots"].([]map[string]any)[0]
	sh2["duration"] = 10
	sh2["dialogue"] = []map[string]any{{"speaker": "阿甲", "text": strings.Repeat("字", 60)}}
	code, out2 := doReq(t, s, "POST", "/api/projects/"+id2+"/import-plan",
		map[string]any{"plan": p2})
	if code != 200 {
		t.Fatalf("超长台词不拒收(拒了没法拆镜),实际 %d(%v)", code, out2)
	}
	overflow, _ := out2["dialogue_overflow"].([]any)
	if len(overflow) == 0 {
		t.Fatalf("抬到上限仍装不下时必须如实报 overflow,实际 %v", out2)
	}
}

// TestRescanPicksUpSkillProducedProject 技能端直接写盘 → 平台不重启也能看见。
//
// 这是双边分工的最后一环:技能端把成套项目包写进 projects/<书名>/<集名>/,
// 若平台只在启动时扫盘,用户就得重启应用才看得见刚产出的集。
func TestRescanPicksUpSkillProducedProject(t *testing.T) {
	s := newTestServer(t)
	const series, ep = "桥接测试书", "桥接测试书·第01集·开头"
	dir := filepath.Join(s.store.Root(), series, ep)
	for _, sub := range []string{"plan", "assets/characters", "assets/scenes", "clips", "output"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	meta := map[string]any{"id": ep, "name": ep, "config": map[string]any{}}
	raw, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, "project.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	planRaw, _ := json.Marshal(mkPlan())
	if err := os.WriteFile(filepath.Join(dir, "plan", "plan.json"), planRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := doReq(t, s, "POST", "/api/projects/rescan", nil)
	if code != 200 {
		t.Fatalf("rescan 应 200,实际 %d(%v)", code, out)
	}
	added, _ := out["added"].([]any)
	found := false
	for _, a := range added {
		if s, _ := a.(string); s == ep {
			found = true
		}
	}
	if !found {
		t.Fatalf("外部写盘的项目没被发现,added=%v", added)
	}
	// 看不见 = 没装载;再查一次项目详情确认它真的进了索引
	if code, _ := doReq(t, s, "GET", "/api/projects/"+ep, nil); code != 200 {
		t.Fatalf("重扫后项目详情仍取不到(%d)—— 只加进 added 列表没进索引", code)
	}
}

// TestImportPackagePullsSkillPackage 平台"主动拉"技能端产出的包(整系列一次导入)。
//
// 与 rescan 的分工:rescan 是技能端**已经写进** data/projects 时刷索引;
// 本端点是包还在别处时由平台自己拷进来 —— 技能端不必知道平台的数据目录布局。
// 支持两类入参:单集包,或系列根(子目录里多个集包)。
func TestImportPackagePullsSkillPackage(t *testing.T) {
	s := newTestServer(t)
	src := t.TempDir()
	const book = "桥接整本书"
	// 造两个集包(模拟技能端 build --all 的产物目录)
	for i, ep := range []string{"第01集", "第02集"} {
		dir := filepath.Join(src, book+"·"+ep+"·第0"+strconv.Itoa(i+1)+"章")
		if err := os.MkdirAll(filepath.Join(dir, "plan"), 0o755); err != nil {
			t.Fatal(err)
		}
		plan := mkPlan()
		plan["shots"].([]map[string]any)[0]["id"] = 1
		rawPlan, _ := json.Marshal(plan)
		if err := os.WriteFile(filepath.Join(dir, "plan", "plan.json"), rawPlan, 0o644); err != nil {
			t.Fatal(err)
		}
		man := map[string]any{"contract": "manjux.plan", "version": project.PlanContractVersion,
			"book": book, "episode": ep, "episode_title": "第0" + strconv.Itoa(i+1) + "章"}
		rawMan, _ := json.Marshal(man)
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), rawMan, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("【本集原文】…"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out := doReq(t, s, "POST", "/api/projects/import-package", map[string]any{"path": src})
	if code != 200 {
		t.Fatalf("整系列导入应 200,实际 %d(%v)", code, out)
	}
	if got, _ := out["imported"].(float64); got != 2 {
		t.Fatalf("应导入 2 集,实际 %v(%v)", out["imported"], out)
	}
	// 项目必须真的进索引(能查到详情)
	if code, _ := doReq(t, s, "GET", "/api/projects/"+book+"·第01集·第01章", nil); code != 200 {
		t.Fatalf("导入后项目详情取不到(%d)—— 拷了盘却没注册", code)
	}
	// 后处理必须生效(与自产包等价):[Chinese] 标记补齐
	plan, err := s.store.LoadPlan(book + "·第01集·第01章")
	if err != nil {
		t.Fatalf("导入的方案读不回:%v", err)
	}
	if !strings.Contains(plan.Shots[0].H3Prompt, "<d>[Chinese]") {
		t.Fatalf("导进来的方案没走后处理(缺 [Chinese]):%q", plan.Shots[0].H3Prompt)
	}
}

// TestImportPackageRejectsBadVersion 契约版本不符必须拒收(两侧要一起改,不许凑合)。
func TestImportPackageRejectsBadVersion(t *testing.T) {
	s := newTestServer(t)
	src := t.TempDir()
	dir := filepath.Join(src, "某书·第01集·开头")
	if err := os.MkdirAll(filepath.Join(dir, "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	rawPlan, _ := json.Marshal(mkPlan())
	_ = os.WriteFile(filepath.Join(dir, "plan", "plan.json"), rawPlan, 0o644)
	rawMan, _ := json.Marshal(map[string]any{"contract": "manjux.plan", "version": 99,
		"book": "某书", "episode": "第01集"})
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), rawMan, 0o644)

	code, out := doReq(t, s, "POST", "/api/projects/import-package", map[string]any{"path": src})
	if code != 400 {
		t.Fatalf("版本不符应 400,实际 %d(%v)", code, out)
	}
	// 半导入比不导入更难查:一个包坏 → 全都不许落盘
	if code, _ := doReq(t, s, "GET", "/api/projects/某书·第01集·开头", nil); code == 200 {
		t.Fatal("校验失败却把项目落盘了")
	}
}

// TestImportPackageAllOrNothing 一个坏包 → 整批不导入(不许留下半导入状态)。
func TestImportPackageAllOrNothing(t *testing.T) {
	s := newTestServer(t)
	src := t.TempDir()
	good := filepath.Join(src, "书A·第01集·开头")
	bad := filepath.Join(src, "书A·第02集·坏包")
	for _, d := range []string{good, bad} {
		if err := os.MkdirAll(filepath.Join(d, "plan"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rawPlan, _ := json.Marshal(mkPlan())
	_ = os.WriteFile(filepath.Join(good, "plan", "plan.json"), rawPlan, 0o644)
	_ = os.WriteFile(filepath.Join(good, "manifest.json"), []byte(
		`{"contract":"manjux.plan","version":1,"book":"书A","episode":"第01集","episode_title":"开头"}`), 0o644)
	// 坏包:悬空场景引用
	badPlan := mkPlan()
	badPlan["shots"].([]map[string]any)[0]["scene"] = "scene_nope"
	rawBad, _ := json.Marshal(badPlan)
	_ = os.WriteFile(filepath.Join(bad, "plan", "plan.json"), rawBad, 0o644)
	_ = os.WriteFile(filepath.Join(bad, "manifest.json"), []byte(
		`{"contract":"manjux.plan","version":1,"book":"书A","episode":"第02集","episode_title":"坏包"}`), 0o644)

	code, out := doReq(t, s, "POST", "/api/projects/import-package", map[string]any{"path": src})
	if code != 400 {
		t.Fatalf("含坏包应整批 400,实际 %d(%v)", code, out)
	}
	if code, _ := doReq(t, s, "GET", "/api/projects/书A·第01集·开头", nil); code == 200 {
		t.Fatal("好包被连带导入 = 半导入状态(坏包拒了、好包进了,用户以为整本书都成了)")
	}
}

func anyStrings(in []any) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// listIDs 取 GET /api/projects 的项目 id 列表(该路由返回裸数组,doReq 解不出来)。
func listIDs(t *testing.T, s *Server) []string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/projects", nil)
	req.Host = "127.0.0.1:8790"
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("列项目失败 %d", w.Code)
	}
	var items []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("列表不是合法 JSON 数组:%v", err)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

// TestListProjectsPicksUpExternalProject GUI 只调列表,没有"重扫"这个动作。
//
// 双侧共组的最后一环:技能端 emit 完,项目已经在 projects/ 里了;界面若只在启动时
// 扫一次盘,用户必须**关掉应用再打开**才看得见 —— 那这半条链路等于白做。
// 所以列表读路径自己重扫:看到列表的那一刻就是最新的。
func TestListProjectsPicksUpExternalProject(t *testing.T) {
	s := newTestServer(t)
	const series, ep = "外部落盘书", "外部落盘书·第01集·开头"
	dir := filepath.Join(s.store.Root(), series, ep)
	if err := os.MkdirAll(filepath.Join(dir, "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"id": ep, "name": ep, "config": map[string]any{}})
	if err := os.WriteFile(filepath.Join(dir, "project.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	planRaw, _ := json.Marshal(mkPlan())
	if err := os.WriteFile(filepath.Join(dir, "plan", "plan.json"), planRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	// **故意不调 rescan** —— 只拉一次列表
	ids := listIDs(t, s)
	found := false
	for _, id := range ids {
		if id == ep {
			found = true
		}
	}
	if !found {
		t.Fatalf("外部落盘的项目没进列表(界面看不到它,用户只能重启应用):%v", ids)
	}
	// 列表是幂等的:连拉两次不许出现重复项(重扫只新增,不重复装载)
	if again := listIDs(t, s); len(again) != len(ids) {
		t.Fatalf("连拉两次列表数量变了:%d → %d", len(ids), len(again))
	}
}

// mkPackage 造一个合法集包(含 manifest/source/plan),返回目录名。
func mkPackage(t *testing.T, root, dir, book, ep string) string {
	t.Helper()
	dir = filepath.Join(root, dir)
	if err := os.MkdirAll(filepath.Join(dir, "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	rawPlan, _ := json.Marshal(mkPlan())
	if err := os.WriteFile(filepath.Join(dir, "plan", "plan.json"), rawPlan, 0o644); err != nil {
		t.Fatal(err)
	}
	rawMan, _ := json.Marshal(map[string]any{"contract": "manjux.plan", "version": project.PlanContractVersion,
		"book": book, "episode": ep})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), rawMan, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestImportPackageSeriesDeclaredButIncomplete 系列声明了 N 集却只找到 M 个 → 拒收。
//
// 真实事故形态:技能端产出 40 集,用户拷到一半(或拷到别的盘时中断),目录里只有 20 个包。
// 若照单全收,他会以为整本书都进去了,渲染到第 21 集才发现缺 —— 这跟"半导入状态"同一类坑,
// 只是发现得更晚。所以按 series.json 对账:少一个都不导。
func TestImportPackageSeriesDeclaredButIncomplete(t *testing.T) {
	s := newTestServer(t)
	src := t.TempDir()
	mkPackage(t, src, "书B·第01集·开头", "书B", "第01集")
	mkPackage(t, src, "书B·第02集·承接", "书B", "第02集")
	// 声明 3 集,盘上只有 2 个包
	rawSer, _ := json.Marshal(map[string]any{
		"contract": "manjux.plan", "version": project.PlanContractVersion, "book": "书B",
		"episodes": []map[string]any{
			{"episode": "第01集", "package": "书B·第01集·开头"},
			{"episode": "第02集", "package": "书B·第02集·承接"},
			{"episode": "第03集", "package": "书B·第03集·结尾"},
		}})
	if err := os.WriteFile(filepath.Join(src, "series.json"), rawSer, 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := doReq(t, s, "POST", "/api/projects/import-package", map[string]any{"path": src})
	if code != 400 {
		t.Fatalf("系列不齐应 400,实际 %d(%v)", code, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "第03集") {
		t.Fatalf("拒收文案要点名缺哪一集(并给出「移开 series.json」的办法),实际 %q", msg)
	}
	if code, _ := doReq(t, s, "GET", "/api/projects/书B·第01集·开头", nil); code == 200 {
		t.Fatal("系列不齐却把找到的那两集导进去了 —— 用户会以为整本书都成了")
	}
}

// TestImportPackageSeriesComplete 声明齐了就正常入库(门不许乱拦)。
func TestImportPackageSeriesComplete(t *testing.T) {
	s := newTestServer(t)
	src := t.TempDir()
	mkPackage(t, src, "书C·第01集·开头", "书C", "第01集")
	mkPackage(t, src, "书C·第02集·承接", "书C", "第02集")
	rawSer, _ := json.Marshal(map[string]any{
		"contract": "manjux.plan", "version": project.PlanContractVersion, "book": "书C",
		"episodes": []map[string]any{
			{"episode": "第01集", "package": "书C·第01集·开头"},
			{"episode": "第02集", "package": "书C·第02集·承接"},
		}})
	if err := os.WriteFile(filepath.Join(src, "series.json"), rawSer, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := doReq(t, s, "POST", "/api/projects/import-package", map[string]any{"path": src})
	if code != 200 {
		t.Fatalf("系列齐了应 200,实际 %d(%v)", code, out)
	}
	if n, _ := out["imported"].(float64); n != 2 {
		t.Fatalf("应导入 2 集,实际 %v", out["imported"])
	}

	// series.json 版本不符同样拒收(两侧必须同版本)
	src2 := t.TempDir()
	mkPackage(t, src2, "书D·第01集·开头", "书D", "第01集")
	rawBad, _ := json.Marshal(map[string]any{"contract": "manjux.plan", "version": 99, "book": "书D"})
	if err := os.WriteFile(filepath.Join(src2, "series.json"), rawBad, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := doReq(t, s, "POST", "/api/projects/import-package",
		map[string]any{"path": src2}); code != 400 {
		t.Fatalf("series.json 版本不符应 400,实际 %d(%v)", code, out)
	}

	// 没有 series.json = 逐包导入,不受影响(不许因为"缺文件"就拦)
	src3 := t.TempDir()
	mkPackage(t, src3, "书E·第01集·开头", "书E", "第01集")
	if code, out := doReq(t, s, "POST", "/api/projects/import-package",
		map[string]any{"path": src3}); code != 200 {
		t.Fatalf("无 series.json 的目录应能逐包导入,实际 %d(%v)", code, out)
	}
}

var _ = project.PlanContractVersion // 断言契约版本可被 server 侧读到(编译期即可)
