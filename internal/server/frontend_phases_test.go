package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/pipeline"
)

// 阶段清单单一真源闸门(审计 P1-2)。
//
// 起因:后端有 `pipeline.PhaseNames`,前端 store.js 另有一份硬编码 PHASES。
// 两份清单不会同时改:后端加一个阶段,前端看不到(用户以为没有);前端自己加一个,
// 后端不认识(点了没反应)。现在前端**以后端 /api/meta 为准**,本地那份只是
// "后端未就绪时的兜底 + 图标/描述"。
//
// 闸门做两件事:
//
//	① 兜底清单的阶段 id 必须与后端 PhaseNames **完全一致**(顺序也一致 ——
//	   流水线栏按它算进度位置,顺序变了会出现"进度往回跳");
//	② 前端必须真的去读 metaPhases(而不是把兜底当唯一来源)。
func TestFrontendPhaseListMatchesBackend(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "store.js"))
	if err != nil {
		t.Fatalf("读 store.js 失败:%v", err)
	}
	src := string(raw)

	// ② 必须消费后端清单
	if !strings.Contains(src, "metaPhases") {
		t.Fatal("store.js 没有消费 /api/meta 的阶段清单 —— 阶段真源又变回前端硬编码了")
	}

	// ① 兜底清单的 id 顺序与后端一致
	block := regexp.MustCompile(`(?s)export const PHASES = \[(.*?)\]`).FindStringSubmatch(src)
	if block == nil {
		t.Fatal("store.js 里找不到 PHASES 兜底清单(闸门解析失效?)")
	}
	var ids []string
	for _, m := range regexp.MustCompile(`id:\s*'([^']+)'`).FindAllStringSubmatch(block[1], -1) {
		ids = append(ids, m[1])
	}
	if len(ids) == 0 {
		t.Fatal("兜底清单里没解析出任何阶段 id")
	}
	var want []string
	for _, p := range pipeline.PhaseNames {
		want = append(want, p.ID)
	}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("前端兜底阶段清单与后端不一致:\n  前端:%v\n  后端:%v\n"+
			"顺序也要一致(流水线栏按它算位置)。", ids, want)
	}
}
