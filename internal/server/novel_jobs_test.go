package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestNovelJobsPersistAndReload 回归:拆集任务状态必须落盘,且重启后「运行中」
// 的任务要降级为「被中断」—— 否则进程重启后首页占位卡会重新显示「AI 拆集中」,
// 用户既看不到进度也不知道发生了什么(只能等 20 分钟孤儿清理兜底)。
func TestNovelJobsPersistAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "novel_jobs.json")

	SetJobsPath(path)
	jobSet("书甲", "running", 3, "拆解进行中")
	jobSet("书乙", "done", 12, "拆集完成:12 集")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("状态未落盘: %v", err)
	}
	var saved map[string]*novelJob
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("落盘内容不是合法 JSON: %v", err)
	}
	if len(saved) != 2 {
		t.Fatalf("应落盘 2 条,实际 %d", len(saved))
	}

	// 模拟进程重启:清空内存后按同一路径重新载入
	novelJobs.Lock()
	novelJobs.m = map[string]*novelJob{}
	novelJobs.Unlock()
	SetJobsPath(path)

	novelJobs.Lock()
	defer novelJobs.Unlock()
	run := novelJobs.m["书甲"]
	if run == nil {
		t.Fatal("重启后应载入「书甲」")
	}
	if run.State != "failed" {
		t.Errorf("重启后 running 应降级为 failed(前端已支持该状态展示),实际 %q", run.State)
	}
	if run.Message == "" || run.Message == "拆解进行中" {
		t.Errorf("中断消息应被改写为可理解的原因,实际 %q", run.Message)
	}
	if done := novelJobs.m["书乙"]; done == nil || done.State != "done" {
		t.Error("已完成的任务状态不应被改写")
	}
}

// TestJobSetEmptyStatePersistsRemoval 终态清理必须同时落盘,
// 否则重启后已清理的条目会"复活"。
func TestJobSetEmptyStatePersistsRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "novel_jobs.json")
	SetJobsPath(path)
	jobSet("书丙", "failed", 0, "拆集失败")
	jobSet("书丙", "", 0, "") // 终态清理

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]*novelJob
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("落盘内容不是合法 JSON: %v", err)
	}
	if len(saved) != 0 {
		t.Errorf("清理后落盘应为空,实际 %d 条", len(saved))
	}
}

// TestLoadNovelJobsWithoutFile 首次运行(文件不存在)不应报错或 panic。
func TestLoadNovelJobsWithoutFile(t *testing.T) {
	SetJobsPath(filepath.Join(t.TempDir(), "never-exists.json"))
	// 只要求不 panic;内存表保持为空
	novelJobs.Lock()
	defer novelJobs.Unlock()
	if len(novelJobs.m) != 0 {
		t.Errorf("无落盘文件时内存表应为空,实际 %d 条", len(novelJobs.m))
	}
}
