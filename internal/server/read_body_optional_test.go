package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 请求体读不出来 ⇒ **不许按零值走**（2026-09-30 深度审计）。
//
// 【现场】三处 `_ = readBody(r, &in)`（runProject / upgrade2K / 成片删除）。body 坏了
// （网络截断、非 JSON）时所有字段停在零值，而零值在这几个端点上**是有含义的**：
//
//	runProject  phase 空 ⇒ 兜底 PhaseAll ⇒ 用户点的"只跑渲染"变成从剧本到成片跑全流程；
//	upgrade2K   shot 0  ⇒ 整片 2K ⇒ 一次截断就按**整部片子**去花云额度，用户看不出是哪步变味的。
//
// 判据分两半：**不带 body 合法**（按默认值，外部/脚本这么调是正常用法），
// **有 body 但读不出来必须报错**。少了后一半就是"悄悄换目标"，少了一半就是"把正常调用挡掉"。
func TestReadBodyOptionalSemantics(t *testing.T) {
	type payload struct {
		Phase string `json:"phase"`
		Shot  int    `json:"shot"`
	}

	// ① 不带 body：合法，按默认值（不报错、字段保持零值）
	req := httptest.NewRequest("POST", "/api/projects/x/run", nil)
	var p1 payload
	if err := readBodyOptional(req, &p1); err != nil {
		t.Fatalf("不带 body 是合法用法（按默认值），不该报错：%v", err)
	}
	if p1.Phase != "" || p1.Shot != 0 {
		t.Errorf("不带 body 时字段应保持零值，实得 %+v", p1)
	}

	// ② body 坏了：必须报错 —— 否则 phase 空会兜底成"全流程"、shot 0 会变成"整片 2K"
	broken := httptest.NewRequest("POST", "/api/projects/x/run", bytes.NewReader([]byte(`{"phase":"rend`)))
	var p2 payload
	if err := readBodyOptional(broken, &p2); err == nil {
		t.Error("截断的 body 必须报错：按零值走会把 phase 兜底成「全流程」、把 shot 兜底成「整片 2K」")
	}

	// ③ 正常 body：照旧解析
	ok := httptest.NewRequest("POST", "/api/projects/x/run", bytes.NewReader([]byte(`{"phase":"render","shot":7}`)))
	var p3 payload
	if err := readBodyOptional(ok, &p3); err != nil {
		t.Fatalf("正常 body 不该报错：%v", err)
	}
	if p3.Phase != "render" || p3.Shot != 7 {
		t.Errorf("解析结果不对：%+v", p3)
	}
}

// 坏 body 打在这两个端点上必须 **400**，而不是"按零值开跑"。
//
// 接线判据（单元判据对了但没接上等于没修）：端点必须真的用 readBodyOptional，
// 而不是继续吞掉 readBody 的返回值。
func TestEndpointsRejectBrokenBody(t *testing.T) {
	s := newTestServer(t)
	proj, err := s.store.Create("坏请求体测试", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	// 请求方法要**照真实路由**发：删成片那条是 DELETE，POST 会被 SPA 的兜底路由
	// 接走并返回 200 + index.html（第一版就是这么假绿的）。
	send := func(method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
		req.Host = "127.0.0.1:8790"
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.mux.ServeHTTP(w, req)
		reason := ""
		if w.Code != 200 {
			var out map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &out)
			reason, _ = out["error"].(string)
		}
		return w.Code, reason
	}
	for _, tc := range []struct{ name, method, path, body string }{
		{"跑项目", "POST", "/api/projects/" + proj.ID + "/run", `{"phase":"rend`},
		{"2K 定稿", "POST", "/api/projects/" + proj.ID + "/upgrade2k", `{"shot":3`},
		{"删成片", "DELETE", "/api/projects/" + proj.ID + "/output", `{"what":"fin`},
	} {
		code, reason := send(tc.method, tc.path, tc.body)
		if code != 400 {
			t.Errorf("%s：坏 body 应 400 拒绝，实际 %d（%s）—— "+
				"零值在这个端点上有含义（phase 空=全流程 / shot 0=整片 2K / what 空=报错理由对不上）",
				tc.name, code, reason)
		}
	}
}

// 删成片：**报出的数必须与盘上的事实一致**（2026-09-30 深度审计）。
// 【现场】原来 `if stat 成功 { _ = os.Remove(p); removed++ }` —— Remove 的返回值被丢掉，
// 于是 Windows 上 final.mp4 被播放器/剪辑软件占着（共享冲突删不掉）时，日志与事件流照样报
// 「🗑 已删除成片产物 N 个文件」，作品库那张卡也还在：用户看到"删了"与"还在"同时成立。
//
// 判据写成"报告与事实一致"（跨平台成立）：删掉的文件必须计入 removed，
// **没删掉的必须在 failed 里点名、且 ok=false**。锁住文件在 Windows 上会真的删不掉；
// 别的平台上如果删成功了，那"报告=事实"依然成立（这条判据不依赖平台差异）。
func TestDeleteOutputReportsOnlyRealRemovals(t *testing.T) {
	s := newTestServer(t)
	proj, err := s.store.Create("成片删除计数测试", project.ProjectConfig{Params: project.DefaultStageParams()})
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(s.store.Dir(proj.ID), "output")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(outDir, "final.mp4")
	free := filepath.Join(outDir, "final.srt")
	for _, p := range []string{locked, free} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 独占打开（不给 FILE_SHARE_DELETE 的机会）：Windows 上随后 os.Remove 必然失败
	f, err := os.Open(locked)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	code, out := doReq(t, s, "DELETE", "/api/projects/"+proj.ID+"/output", map[string]any{"what": "all"})
	if code != 200 {
		t.Fatalf("删除应 200（删不掉的用 ok/failed 表达，不是 HTTP 错误），实际 %d", code)
	}

	// 盘上的事实
	lockedStillThere := true
	if _, err := os.Stat(locked); os.IsNotExist(err) {
		lockedStillThere = false
	}
	freeStillThere := false
	if _, err := os.Stat(free); err == nil {
		freeStillThere = true
	}

	if freeStillThere {
		t.Errorf("没被占用的 final.srt 应该被删掉")
	}
	failedAny, _ := out["failed"].([]any)
	if lockedStillThere {
		if okv, _ := out["ok"].(bool); okv {
			t.Error("有文件没删掉时 ok 不能为真 —— 界面会当成删成功，而作品库里那张卡还在")
		}
		named := false
		for _, f := range failedAny {
			if s, _ := f.(string); s == "final.mp4" {
				named = true
			}
		}
		if !named {
			t.Errorf("删不掉的文件必须在 failed 里点名（实得 %v）—— 不点名用户只知道「有东西没删掉」，不知道是哪个、为什么", out["failed"])
		}
	} else {
		// 文件真的删掉了：这时报告必须是"全删干净"
		if okv, _ := out["ok"].(bool); !okv {
			t.Errorf("文件确实删掉了却报 ok=false（%v）", out)
		}
	}
	if removedAny, ok := out["removed"].(float64); ok {
		if int(removedAny) != 1 {
			t.Errorf("removed 应等于真正删掉的个数 1，实得 %v", removedAny)
		}
	}
}
