package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
	"github.com/YiIimini/ManJuX/internal/project"
	"github.com/YiIimini/ManJuX/internal/qc"
)

// zz_probe_voice_pitch_live_test.go 真机探针：逐镜量音色，打印"期望声线 × 实测基频 × 判词"。
//
// 只在那份真机项目在盘上时跑（没有就 skip，不假装验过）。
// 用途：① 出一轮"哪些镜的音色不对"的清单；② 反向验证时用它确认判据真的会咬（不是摆设）。
func TestZZProbeVoicePitchLive(t *testing.T) {
	root := filepath.Join("..", "..", "data", "projects")
	id := "顺其自然（真实事迹短片）·第01集·顺其自然"
	dir := filepath.Join(root, "顺其自然（真实事迹短片）", id)
	if v := strings.TrimSpace(os.Getenv("MANJUX_PROBE_PROJECT")); v != "" {
		id = v
		dir = ""
		if m, _ := filepath.Glob(filepath.Join(root, "*", id)); len(m) > 0 {
			dir = m[0]
		}
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skip("没有那份真机项目，跳过探针")
	}
	store, err := project.NewStore(root)
	if err != nil {
		t.Skip(err)
	}
	proj, ok := store.Get(id)
	if !ok {
		t.Skip("项目读不到")
	}
	plan, err := store.LoadPlan(id)
	if err != nil {
		t.Fatal(err)
	}
	c := &Coordinator{}
	if c.ff, err = ff.New("", ""); err != nil {
		t.Skipf("本机没有 ffmpeg：%v", err)
	}
	manifest, err := store.LoadManifest(id)
	if err != nil {
		t.Fatal(err)
	}
	dirAbs, _ := filepath.Abs(dir)
	bad := 0
	for i := range plan.Shots {
		sh := plan.Shots[i]
		lines := submittedVoiceLines(proj, plan, &sh)
		clip := clipPathOf(dirAbs, manifest[strconv.Itoa(sh.ID)])
		if clip == "" {
			t.Logf("镜 %-3d 没有可用产物，跳过", sh.ID)
			continue
		}
		rep := voicePitchForQC(context.Background(), c, proj, plan, &sh, clip)
		if rep == nil {
			continue
		}
		t.Logf("镜 %-3d 期望=%s 台词%d 句 | 中位F0=%.1fHz 可靠=%.2fs 男带%.0f%%/女带%.0f%% | %s",
			sh.ID, rep.Expected, len(lines), rep.MedianHz, rep.VoicedSec, rep.MalePct, rep.FemalePct,
			qc.VoicePitchSummary(rep))
		if rep.Bad() {
			bad++
			t.Logf("      ⚠ %s", rep.Findings[0].Detail)
		}
	}
	t.Logf("=== 判负 %d 镜", bad)
}

func itoaID(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	if n < 100 {
		return string(rune('0'+n/10)) + string(rune('0'+n%10))
	}
	return string(rune('0'+n/100)) + string(rune('0'+(n/10)%10)) + string(rune('0'+n%10))
}
