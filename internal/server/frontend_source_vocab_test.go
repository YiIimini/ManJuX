package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 前端"来源兜底表"与后端词汇表一致性闸门(2026-09-25 换图像模型时暴露)。
//
// store.js 里的 FALLBACK_SOURCE_NAMES 是 project/source_label.go 的**副本**(meta 未就绪
// 时用)。图像模型从 Krea-2 换成 Qwen-Image-2.1 后,后端多出了 qwen-image-2.1 等 key,
// 前端副本若不跟着补,新资产的标签就会退化成显示原始 key。
//
// 判据:前端表里的每个 key 都必须出现在后端词汇表里(反向不要求 —— 后端多几个 key
// 是正常的,meta 会下发)。这样"换了模型忘了改前端"当场变红,而不会静默降级。
var (
	reFallbackKeys    = regexp.MustCompile(`(?s)FALLBACK_SOURCE_NAMES\s*=\s*\{(.*?)\}`)
	reFallbackKeyName = regexp.MustCompile(`['"]?([A-Za-z0-9._-]+)['"]?\s*:`)
)

func TestFrontendSourceFallbackMatchesBackend(t *testing.T) {
	path := filepath.Join("..", "..", "frontend", "src", "store.js")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到 %s: %v", path, err)
	}
	m := reFallbackKeys.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("store.js 里找不到 FALLBACK_SOURCE_NAMES —— 闸门失效(改名了就要同步这里)")
	}
	keys := map[string]bool{}
	// 一行里可能写多个键值对('a': 'x', 'b': 'y'),所以按键名正则逐个提取,
	// 而不是按行取第一个冒号(那样会漏掉同一行后面的键 —— 本闸门第一版就漏了)。
	for _, km := range reFallbackKeyName.FindAllStringSubmatch(m[1], -1) {
		keys[km[1]] = true
	}
	if len(keys) == 0 {
		t.Fatal("没解析出任何来源 key —— 解析失效比漏 key 更糟")
	}
	backend := map[string]bool{}
	for _, s := range project.AssetSources() {
		backend[s.Key] = true
	}
	var stale []string
	for k := range keys {
		if !backend[k] {
			stale = append(stale, k)
		}
	}
	if len(stale) > 0 {
		t.Errorf("前端兜底表里有后端词汇表没有的来源 key:%v\n  两处必须同步(frontend/src/store.js ↔ internal/project/source_label.go)",
			strings.Join(stale, ", "))
	}
	// 反向提醒但不判失败:图像家族的 key 必须都在前端能显示出来
	for _, fam := range []string{"qwen-image-2.1", "qwen-image-2.1-chibi", "z-image", "krea2"} {
		if !keys[fam] {
			t.Errorf("前端兜底表缺少图像家族来源 %q —— 新资产的标签会退化成原始 key", fam)
		}
	}
}
