package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// zz_probe_scene_luma_live_test.go 临时探针：用平台自己的 imageMeanLuma 量真机场景图亮度，
// 好把 scene_exposure_test.go 的夹具钉在**实测值**上（口径必须与门一致）。
// 没有这份真机项目时自动跳过（CI/别的机器上不报 fail）。
func TestZZProbeSceneLuma(t *testing.T) {
	dir := filepath.Join("..", "..", "data", "projects", "顺其自然（真实事迹短片）",
		"顺其自然（真实事迹短片）·第01集·顺其自然", "assets", "scenes")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("没有真机项目，跳过探针")
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Skip(err)
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		got, ok := imageMeanLuma(filepath.Join(dir, e.Name()))
		if !ok {
			t.Logf("%-46s 读不出", e.Name())
			continue
		}
		t.Logf("%-46s luma=%.1f", e.Name(), got)
	}
}
