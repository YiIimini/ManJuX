package server

import (
	"strings"
	"testing"
)

// 导入 = 开跑,两条入口都不许漏(2026-09-26 用户:「脚本创作 导入后就完事了?」)。
//
// 真实事故形态:用户点的是**「项目包导入」**(不是我新加的脚本创作),日志里
// 只有 📥 40 行 + 🔄 重扫一行,没有任何 ▶ —— 因为"自动开跑"当时只长在脚本创作那条入口上。
// 所以这条用例**两个入口都钉**:谁的入口少了一次开跑请求,谁就变红。

// recordAutoStart 把产线启动换成记录器(真产线是进程级副作用,见 Server.startProdFn)。
func recordAutoStart(s *Server) *[]struct {
	Prefix string
	Eps    []string
} {
	got := &[]struct {
		Prefix string
		Eps    []string
	}{}
	s.startProdFn = func(prefix string, eps []string) bool {
		*got = append(*got, struct {
			Prefix string
			Eps    []string
		}{prefix, eps})
		return true
	}
	return got
}

// TestProjectPackageImportStartsProduction 入口一:POST /api/projects/import-package。
func TestProjectPackageImportStartsProduction(t *testing.T) {
	s := newTestServer(t)
	got := recordAutoStart(s)

	src := t.TempDir()
	mkPackage(t, src, "书A·第01集·开头", "书A", "第01集")
	mkPackage(t, src, "书A·第02集·承接", "书A", "第02集")

	if code, out := doReq(t, s, "POST", "/api/projects/import-package", map[string]any{"path": src}); code != 200 {
		t.Fatalf("导入应 200,实际 %d(%v)", code, out)
	}
	if len(*got) != 1 {
		t.Fatalf("导入成功后必须请求开跑 1 次(用户点导入的意图是出片),实际 %d 次:%+v", len(*got), *got)
	}
	if p := (*got)[0].Prefix; p != "书A" {
		t.Fatalf("开跑的书名应是《书A》(从项目 id 的「·第」前截出),实际 %q", p)
	}
	// 【2026-09-28 用户口径（第二版）】集数照常全部入队、一路顺下去（不等人点），
	// 但**解析是即时的**：轮到第 N 集才做第 N 集的 LLM 解析（ensureEpisodeBrief），
	// 解析完紧接着跑这一集的渲染管线 —— 防"一上来全解析"的点在**这里**，不在入队范围。
	if eps := (*got)[0].Eps; len(eps) != 2 {
		t.Fatalf("导入应把各集都交给产线(逐集流水线,不等人点),实际 %d 集:%v", len(eps), eps)
	}
	if !strings.Contains(strings.Join((*got)[0].Eps, " "), "第01集") {
		t.Fatalf("队列里应含第 1 集:%v", (*got)[0].Eps)
	}
	// 交给产线的集名必须是**真的能对上项目**的 id —— 否则产线会空转(启动横幅说 N 集,
	// 逐集处理时一个都找不到)。
	if eps := s.episodesOf("书A"); len(eps) != 2 {
		t.Fatalf("按前缀回溯应能找到 2 集,实际 %v", eps)
	}
}

// TestScriptEntryImportStartsProduction 入口二:POST /api/novels/produce-script(小说库「脚本创作」)。
func TestScriptEntryImportStartsProduction(t *testing.T) {
	s := newTestServer(t)
	got := recordAutoStart(s)

	book := mkBookWithScript(t, s, "有脚本", "脚本", 2)
	if code, out := doReq(t, s, "POST", "/api/novels/produce-script", map[string]any{"path": book}); code != 200 {
		t.Fatalf("脚本创作应 200,实际 %d(%v)", code, out)
	}
	if len(*got) != 1 {
		t.Fatalf("脚本创作导入成功后必须请求开跑 1 次,实际 %d 次:%+v", len(*got), *got)
	}
	p := (*got)[0].Prefix
	if p == "" {
		t.Fatal("开跑的书名为空 —— 产线起来了也找不到集")
	}
	if eps := s.episodesOf(p); len(eps) == 0 {
		t.Fatalf("开跑书名 %q 回溯不到任何集(产线会空转),项目:%v", p, listIDs(t, s))
	}
}

// TestImportFailureNeverStartsProduction 导入被拒收时**不许**开跑 —— 否则产线起来
// 却没有项目可跑,界面显示"产线运行中"而实际什么都没发生(比不动更坏)。
func TestImportFailureNeverStartsProduction(t *testing.T) {
	s := newTestServer(t)
	got := recordAutoStart(s)

	src := t.TempDir() // 空目录:没有包
	if code, _ := doReq(t, s, "POST", "/api/projects/import-package", map[string]any{"path": src}); code == 200 {
		t.Fatal("空目录不该被当成导入成功")
	}
	if len(*got) != 0 {
		t.Fatalf("导入失败却请求了开跑:%+v", *got)
	}
}
