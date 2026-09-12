package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestProjectFreshestDetectsNewArtifacts 产线等待策略的活性信号。
//
// 回归 2026-09-12(用户定调「管线生产需要合理化」):批量产线原用固定 2.5 小时上限
// 判断"这一集是不是跑完了",超时就当完成开下一集 —— 于是上一集还在渲染、下一集
// 已经开跑,多集并发抢一张显卡,定妆照生成被排队超时杀掉。改为看产物进展后,
// "有没有新产物"这个信号必须准确:新落地要能识别,日志改动不算产物。
func TestProjectFreshestDetectsNewArtifacts(t *testing.T) {
	dir := t.TempDir()
	// 初始:一个产物 + 一个日志目录
	clip := filepath.Join(dir, "clips", "shot_001.mp4")
	if err := os.MkdirAll(filepath.Dir(clip), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clip, []byte("mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(logs, "run.log")
	if err := os.WriteFile(logFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := projectFreshest(dir)
	if base.IsZero() {
		t.Fatal("应能取到产物时间")
	}
	// ① 只有日志在动 → 不算产物进展(否则卡死的集永远"看起来在跑")
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(logFile, later, later)
	if got := projectFreshest(dir); got.After(base) {
		t.Errorf("日志目录不算产物:got %v > base %v", got, base)
	}
	// ② 新镜头落盘 → 活性信号必须刷新
	newer := time.Now().Add(4 * time.Second)
	clip2 := filepath.Join(dir, "clips", "shot_002.mp4")
	if err := os.WriteFile(clip2, []byte("mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(clip2, newer, newer)
	if got := projectFreshest(dir); !got.Equal(newer.Truncate(time.Second)) && got.Before(newer) {
		t.Errorf("新产物应刷新活性信号:got %v want >= %v", got, newer)
	}
	// ③ 空目录/不存在目录不得 panic
	if projectFreshest(filepath.Join(dir, "不存在")).IsZero() != true {
		t.Error("不存在目录应返回零值时间")
	}
}
