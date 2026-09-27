package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// novels_detail_import_test.go 「脚本创作」新导入路（吃技能侧结构化明细）的契约。
//
// 依据：技能侧 2026-09-27 回复里补齐的三样接线（R1 每镜 h3_prompt、R2 scene_id/character_ids、
// R3 三类卡进 JSON），以及平台侧那两个必须先定的设计点（集标题、双风格句）。
//
// 夹具照**真实形态**写：字段名逐字取自盘上的 `分镜/分镜明细.json`；
// 换行/类型这类"小差别"正是这类 bug 的全部内容（本项目已经因为 LF 夹具 + CRLF 真文件吃过一次亏）。

// mkBookDetail 造一本书 + 一份结构化明细（2 集，第 2 集只有 1 镜）。
func mkBookDetail(t *testing.T, s *Server, name string) string {
	t.Helper()
	book := filepath.Join(s.novelRoot(), name)
	dir := filepath.Join(book, "分镜")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 集标题从 markdown 一级标题取（明细头部没给 title —— 正是设计点 1 的兜底路）
	if err := os.WriteFile(filepath.Join(dir, "第01集.md"), []byte("# 第01集 · 求你们骂我 — 分镜（内核）\r\n\r\n正文\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	style := "GUOFENG ANIMATED-FEATURE ART DIRECTION (period Chinese): a photoreal 3D surface with IDEALISED faces."
	h3 := func(action string) string {
		return "subject_definitions:\n<Subject 1> is 沈砚 — the young man.\n\n" +
			"summary:\n[reference generation] A 5-second shot.\n\n" +
			"retention_analysis:\n<Subject 1> (appears in [Shot 1]): fully_preserved - face held.\n\n" +
			"detailed_description:\n" + style + "\n" +
			"[Shot 1] " + action + " <d>[Chinese]我准了。</d>\n\n" +
			"overall_soundscape:\nroom tone\n\nnon_diegetic_music:\nN/A"
	}
	detail := fmt.Sprintf(`{
  "contract":"manjux.plan","version":2,"book":%q,
  "episodes":[{"no":1,"shots":2,"seconds":10},{"no":2,"shots":1,"seconds":5}],
  "episode_seconds":{"1":10,"2":5},
  "characters":[
    {"id":"char_01","name":"沈砚","kind":"human","description":"瘦长脸","image_prompt":"Shen Yan front sheet","board_prompt":"board","voice":"teen boy"},
    {"id":"char_09","name":"路人甲","kind":"human","description":"路人","image_prompt":"extra","board_prompt":"board","voice":"man"}
  ],
  "scenes":[{"id":"scene_01","name":"临水城","description":"城门","image_prompt":"gate plate","landmarks":"city gate (left)","light_state":"warm 3000K"}],
  "props":[{"id":"prop_01","name":"沈砚的伞","en":"an oiled paper umbrella","description":"旧伞","image_prompt":"umbrella"},
           {"id":"prop_99","name":"没被提到的令牌","en":"an unmentioned tablet","description":"正文里没人提它","image_prompt":"tablet"}],
  "shots":[
    {"episode":1,"id":1,"duration":5,"shot_size":"中景","scene":"临水城","scene_id":"scene_01",
     "characters":["沈砚"],"character_ids":["char_01"],"mode":"ref2va","view":"telephoto compression",
     "is_establishing":true,"view_note":"","angle_zh":"平视","camera":"static","hook_type":"reveal",
     "end_state":"he stands","action":"沈砚撑开沈砚的伞","action_en":"he opens the umbrella",
     "emotion":"calm","sound":"rain","dialogue":[{"speaker":"沈砚","text":"我准了","os":false}],
     "narration":"","scene_landmarks":["city gate"],"light":"warm","h3_prompt":%q},
    {"episode":1,"id":2,"duration":5,"shot_size":"近景","scene":"临水城","scene_id":"scene_01",
     "characters":["沈砚"],"character_ids":["char_01"],"mode":"ref2va","view":"","view_note":"小景别留空",
     "angle_zh":"俯拍","camera":"static","hook_type":"tender","end_state":"he looks up",
     "action":"他抬头","action_en":"he looks up","emotion":"","sound":"","dialogue":[],"narration":"",
     "scene_landmarks":["city gate"],"light":"warm","h3_prompt":%q},
    {"episode":2,"id":1,"duration":5,"shot_size":"远景","scene":"临水城","scene_id":"scene_01",
     "characters":[],"character_ids":[],"mode":"t2va","view":"compressed distant depth","view_note":"",
     "is_establishing":true,"angle_zh":"鸟瞰","camera":"static","hook_type":"setup","end_state":"empty",
     "action":"空镜","action_en":"empty","emotion":"","sound":"","dialogue":[],"narration":"三年了。",
     "scene_landmarks":["city gate"],"light":"warm","h3_prompt":%q}
  ]}`, name, h3("he opens the umbrella"), h3("he looks up"), h3("empty street"))
	if err := os.WriteFile(filepath.Join(dir, novelShotDetailJSON), []byte(detail), 0o644); err != nil {
		t.Fatal(err)
	}
	return book
}

// TestImportFromShotDetail 明细 → 集项目：两集都建出来、字段映射对、**风格句被提到 plan.Style 并从每镜剔掉**。
func TestImportFromShotDetail(t *testing.T) {
	s, _ := newHiddenTestServer(t)
	book := mkBookDetail(t, s, "明细书")
	started := recordAutoStart(s)

	code, out := doReq(t, s, "POST", "/api/novels/produce-script", map[string]any{"path": book})
	if code != 200 {
		t.Fatalf("导入应 200，实际 %d：%v", code, out)
	}
	if src, _ := out["source"].(string); src != novelShotDetailJSON {
		t.Errorf("应标明来源是结构化明细，实际 %v", out["source"])
	}
	if imp, _ := out["imported"].(float64); int(imp) != 2 {
		t.Fatalf("应导入 2 集，实际 %v", out["imported"])
	}
	// 项目名：<书名>·第01集·<标题>（标题来自 markdown 一级标题）
	eps, _ := doReq(t, s, "GET", "/api/novels?path="+urlEnc(book), nil)
	_ = eps
	ep1 := "明细书·第01集·求你们骂我"
	plan, err := s.store.LoadPlan(ep1)
	if err != nil {
		t.Fatalf("第01集应有 plan（%s）：%v", ep1, err)
	}
	if len(plan.Shots) != 2 {
		t.Fatalf("第01集应有 2 镜，实际 %d", len(plan.Shots))
	}
	// ① 风格句：**提成 plan.Style**（让参考图也服从同一套风格），但**不删**提示词里那句
	// —— 官方格式就要求它写在 `[Shot 1]` 之前；装配时平台会让位（见 pipeline 的用例），
	// 早先"剔掉镜头里那句"的做法会让契约门报"detailed_description 里没有风格句"。
	if !strings.Contains(plan.Style, "GUOFENG") {
		t.Errorf("风格句应被提成 plan.Style，实际 %q", plan.Style)
	}
	for _, sh := range plan.Shots {
		if !strings.Contains(sh.H3Prompt, "GUOFENG") {
			t.Errorf("镜 %d 的提示词应保留技能端自己的风格句（删了它 = 破坏上游格式）：%.80s", sh.ID, sh.H3Prompt)
		}
		if !strings.Contains(sh.H3Prompt, "[Shot 1]") {
			t.Errorf("镜 %d 的正文应完好：%.120s", sh.ID, sh.H3Prompt)
		}
	}
	// ② 引用与字段按契约映射
	sh := plan.Shots[0]
	if sh.Scene != "scene_01" || len(sh.Characters) != 1 || sh.Characters[0] != "char_01" {
		t.Errorf("引用应映射成 ID：scene=%q characters=%v", sh.Scene, sh.Characters)
	}
	if sh.View != "telephoto compression" || sh.HookType != "reveal" || sh.EndState == "" {
		t.Errorf("view/hook_type/end_state 应带过来：%+v", sh)
	}
	if len(sh.Dialogue) != 1 || sh.Dialogue[0].Speaker != "沈砚" || sh.Dialogue[0].Text != "我准了" {
		t.Errorf("台词应映射成 Line：%+v", sh.Dialogue)
	}
	// ③ 卡：只带这一集用到的角色/场景；**道具全带**（道具图是书级资产、靠库跨集复用，
	//    按名字猜会漏掉"正文没点名它"的那一件）
	if len(plan.Characters) != 1 || plan.Characters[0].ID != "char_01" {
		t.Errorf("只应带第 1 集用到的角色，实际 %+v", plan.Characters)
	}
	if len(plan.Scenes) != 1 || plan.Scenes[0].Landmarks == "" || plan.Scenes[0].LightState == "" {
		t.Errorf("场景卡应带地标与光态：%+v", plan.Scenes)
	}
	if len(plan.Props) != 2 {
		t.Errorf("道具应**全带上**（含正文里没提到的那件），实际 %+v", plan.Props)
	}
	// ④ 自动开线（用户点导入的意图是出片）
	if len(*started) != 1 || (*started)[0].Prefix != "明细书" {
		t.Errorf("导入后应为这一本启动产线，实际 %v", *started)
	}
	// ⑤ 第 2 集（只有 1 镜、无台词的远景）也建出来了
	if _, err := s.store.LoadPlan("明细书·第02集·未命名"); err != nil {
		t.Errorf("第 2 集也该建出来（明细头部没给 title → 退「未命名」）：%v", err)
	}
}

// TestImportFromShotDetailRefusesAllOnFail 一个集不过契约 → **全部不导**（与旧包同一条政策）。
func TestImportFromShotDetailRefusesAllOnFail(t *testing.T) {
	s, _ := newHiddenTestServer(t)
	book := mkBookDetail(t, s, "坏明细书")
	// 把第 1 集的时长改成 12 秒（超上限 → fail）
	raw, err := os.ReadFile(filepath.Join(book, "分镜", novelShotDetailJSON))
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(raw), `"duration":5,"shot_size":"中景"`, `"duration":12,"shot_size":"中景"`, 1)
	if err := os.WriteFile(filepath.Join(book, "分镜", novelShotDetailJSON), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := doReq(t, s, "POST", "/api/novels/produce-script", map[string]any{"path": book})
	if code != 400 {
		t.Fatalf("有 fail 时应整批拒收（400），实际 %d：%v", code, out)
	}
	if _, err := s.store.LoadPlan("坏明细书·第01集·求你们骂我"); err == nil {
		t.Error("拒收时不该留下半成品项目")
	}
}

// TestNovelScriptCardsFromDetail 页签的三类卡改读明细内嵌卡（P2：包退休后 tab 不能空）。
func TestNovelScriptCardsFromDetail(t *testing.T) {
	s, _ := newHiddenTestServer(t)
	book := mkBookDetail(t, s, "卡片书")
	_, out := doReq(t, s, "GET", "/api/novels/script?path="+urlEnc(book), nil)
	if src, _ := out["source"].(string); src != novelShotDetailJSON {
		t.Errorf("有明细时来源应是明细，实际 %v", out["source"])
	}
	chars, _ := out["characters"].([]any)
	if len(chars) != 2 {
		t.Fatalf("角色卡应来自明细内嵌卡（2 个），实际 %d", len(chars))
	}
	c0, _ := chars[0].(map[string]any)
	if c0["id"] != "char_01" || c0["prompt"] == "" || c0["voice"] == "" {
		t.Errorf("角色卡字段应齐全：%v", c0)
	}
	scenes, _ := out["scenes"].([]any)
	if len(scenes) != 1 {
		t.Fatalf("场景卡应 1 个，实际 %d", len(scenes))
	}
	props, _ := out["props"].([]any)
	if len(props) != 2 {
		t.Fatalf("道具卡应来自明细内嵌卡（2 个），实际 %d", len(props))
	}
	// 「出现在几集」：char_01 只在第 1 集 → in=1
	if in, _ := c0["in"].(float64); int(in) != 1 {
		t.Errorf("char_01 出现在 1 集里，实际 %v", c0["in"])
	}
}

// TestNovelsListExposesDetail 书卡要暴露"有没有结构化明细"（否则前端入口永远禁用）。
func TestNovelsListExposesDetail(t *testing.T) {
	s, _ := newHiddenTestServer(t)
	mkBookDetail(t, s, "列表书")
	_, out := doReq(t, s, "GET", "/api/novels", nil)
	items, _ := out["novels"].([]any)
	if len(items) == 0 {
		t.Fatalf("列表应有这本书：%v", out)
	}
	found := false
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["name"] != "列表书" {
			continue
		}
		found = true
		d, _ := m["detail"].(map[string]any)
		if d == nil {
			t.Fatalf("书卡应带 detail 概况：%v", m)
		}
		if eps, _ := d["episodes"].(float64); int(eps) != 2 {
			t.Errorf("detail.episodes 应为 2，实际 %v", d["episodes"])
		}
		if sh, _ := d["shots"].(float64); int(sh) != 3 {
			t.Errorf("detail.shots 应为 3，实际 %v", d["shots"])
		}
	}
	if !found {
		t.Fatal("没找到「列表书」")
	}
}
