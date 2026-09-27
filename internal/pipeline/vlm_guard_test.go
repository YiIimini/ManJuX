package pipeline

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/project"
)

// vlm_guard_test.go 视觉模型的限流熔断与调用预算（2026-09-27 用户报障：
// 「⚠ 定妆照「周廷」比例自检失败…VLM HTTP 429…该模型当前访问量过大」＋「走剧本脚本为什么还超？」）。
//
// 用户看到的"还超"是**行为**问题不是文案问题：限流之后平台仍在逐张打接口、逐张刷告警。
// 这里钉三件事：① 认限流 → 进静默期；② 静默期内不再调用；③ 预算用尽后不再调用（但要说一次）。

func TestVLMGuardCooldownOnRateLimit(t *testing.T) {
	old := vlmCooldown
	vlmCooldown = 30 * time.Millisecond
	defer func() { vlmCooldown = old }()

	c := &Coordinator{}
	if !c.vlmAllowed() {
		t.Fatal("初始状态应允许调用")
	}
	// 普通错误**不进**静默期（那是本地问题，该逐条如实告警）
	c.vlmNote(nil, errors.New("认不出判定结果"))
	if !c.vlmAllowed() {
		t.Error("解析失败不该触发限流静默")
	}
	// 限流 → 进静默期
	c.vlmNote(nil, errors.New(`VLM HTTP 429: {"error":{"code":"1305","message":"该模型当前访问量过大"}}`))
	if c.vlmAllowed() {
		t.Error("限流后应进入静默期")
	}
	time.Sleep(40 * time.Millisecond)
	if !c.vlmAllowed() {
		t.Error("静默期结束后应自动恢复（到点自动恢复，不用重启）")
	}
}

func TestVLMGuardBudget(t *testing.T) {
	c := &Coordinator{}
	c.VLMResetBudget()
	for i := 0; i < maxVLMCallsPerRun; i++ {
		if !c.vlmBudgetTake() {
			t.Fatalf("第 %d 次预算就该够", i+1)
		}
	}
	if c.vlmBudgetTake() {
		t.Error("预算用尽后不该再取到")
	}
	c.VLMResetBudget()
	if !c.vlmBudgetTake() {
		t.Error("重置后应恢复满额")
	}
}

// TestVLMSkipReason 跳过的原因要能给人看，且**不能**在 proj=nil 时炸（调用点可能没有项目上下文）。
func TestVLMSkipReason(t *testing.T) {
	c := &Coordinator{}
	c.VLMResetBudget()
	if why := c.vlmSkipReason(nil); why != "" {
		t.Errorf("正常情况下不该跳过:%s", why)
	}
	for c.vlmBudgetTake() {
	}
	if why := c.vlmSkipReason(nil); why == "" {
		t.Error("预算用尽后要说得出原因")
	}
	c.VLMResetBudget()
	old := vlmCooldown
	vlmCooldown = 50 * time.Millisecond
	defer func() { vlmCooldown = old }()
	c.vlmNote(nil, errors.New("429 too many requests"))
	if why := c.vlmSkipReason(nil); why == "" {
		t.Error("静默期内要说得出原因")
	}
}

// TestCastCacheInvalidatesOnRepaint 撞脸判定按**图片指纹**记账：重画过就失效。
//
// 这条直接决定"反复重跑同一段资产阶段"会不会把接口打满（用户就是这样把免费档压到限流的）。
func TestCastCacheInvalidatesOnRepaint(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.png")
	b := filepath.Join(dir, "b.png")
	writeTestPNG(t, a, 64, 64)
	writeTestPNG(t, b, 64, 64)

	cache := loadCastCache(dir)
	if _, ok := cache.lookup(a, b); ok {
		t.Fatal("空缓存不该命中")
	}
	cache.put(a, b, true, "都是瘦长脸")
	if err := saveCastCache(dir, cache); err != nil {
		t.Fatal(err)
	}
	// 读回落盘的那份（跨运行复用）
	again := loadCastCache(dir)
	e, ok := again.lookup(a, b)
	if !ok || !e.Same || e.Note != "都是瘦长脸" {
		t.Fatalf("同一对图应命中缓存：%+v ok=%v", e, ok)
	}
	// **重画了其中一张** → 指纹变了 → 必须重新判定（否则改完脸还以为撞）
	writeTestPNG(t, b, 96, 96)
	if _, ok := again.lookup(a, b); ok {
		t.Error("图被重画过之后缓存必须失效")
	}
}

// TestAssetStageResetsVLMBudget 每轮资产阶段都要重置预算（否则第二轮一次都不验）。
func TestAssetStageResetsVLMBudget(t *testing.T) {
	raw, err := os.ReadFile("stages_assets.go")
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(string(raw), "c.VLMResetBudget()") {
		t.Error("资产阶段开头要 VLMResetBudget()（否则跨轮次累积，第二轮直接没预算）")
	}
	_ = project.Plan{}
}
