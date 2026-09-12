package pipeline

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/project"
)

// TestStartRefusesConcurrentPipeline 全局单管线铁律。
//
// 回归 2026-09-12(用户定调「管线生产需要合理化,别老是出现失败」):多集并发共用
// 一张显卡不是"更快"而是互相伤害 —— 出图任务排在长视频任务后面排队(旧实现排队
// 时间还计入超时预算 → 定妆照整批被判"渲染超时"),单镜从 3-5 分钟涨到 20+ 分钟。
// 实测三集并发时角色与场景定妆照接连失败、镜头缺参考图降级 t2va。
//
// 这里用注入 runs 的方式验证护栏(不真跑管线):有别的项目在跑时,新启动必须被
// 明确拒绝并说清原因;清掉之后必须放行(走到项目校验,而不是被护栏卡死)。
func TestStartRefusesConcurrentPipeline(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.NewManager(filepath.Join(dir, "settings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := project.NewStore(filepath.Join(dir, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	c := New(store, cfg, engine.NewRegistry(), new(ff.FF), NewBus(), nil)

	// ① 同一个项目重复启动:报"已在运行"
	c.mu.Lock()
	c.runs["甲集"] = &Run{Phase: PhaseAll, StartedAt: time.Now()}
	c.mu.Unlock()
	if err := c.Start("甲集", PhaseAll, nil, "", 0); err == nil || !strings.Contains(err.Error(), "正在运行") {
		t.Fatalf("同项目重复启动应被拒,实际 err=%v", err)
	}
	// ② 换一个项目启动:必须被全局护栏拒绝,且提示不能只说"失败",要说清代价
	err = c.Start("乙集", PhaseAll, nil, "", 0)
	if err == nil || !strings.Contains(err.Error(), "另一条管线正在运行") {
		t.Fatalf("有管线在跑时应拒绝新管线,实际 err=%v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "显卡") {
		t.Errorf("拒绝理由应说明「抢显卡」的代价,实际:%v", err)
	}
	// ③ 放开之后必须放行(此处"乙集"不存在,报的是项目不存在 = 已越过护栏)
	c.mu.Lock()
	delete(c.runs, "甲集")
	c.mu.Unlock()
	if err := c.Start("乙集", PhaseAll, nil, "", 0); err == nil || !strings.Contains(err.Error(), "项目不存在") {
		t.Fatalf("没有管线在跑时应放行到项目校验,实际 err=%v", err)
	}
}
