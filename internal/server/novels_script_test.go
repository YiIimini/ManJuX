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
