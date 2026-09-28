package compose

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// bgm_test.go 单条 BGM 铺底 + 人声闪避（官方 qc-checklist STEP 8）。
//
// 官方原文：「Then generate one continuous BGM track that matches the story mood and embed it into
// the assembled video.」「Duck BGM under dialogue…」「Do not generate BGM per shot.」
// 而平台此前是**逐镜各带配乐**（30+ 片段拼起来每刀都换调）。这一步把配乐收到终混，只此一条。
func TestMixBGMBedRealFFmpeg(t *testing.T) {
	f, err := ff.New("", "")
	if err != nil {
		t.Skipf("本机没有 ffmpeg，跳过:%v", err)
	}
	dir := t.TempDir()
	voice := filepath.Join(dir, "voice.wav")
	bgm := filepath.Join(dir, "bgm.wav")
	out := filepath.Join(dir, "mixed.wav")
	mk := func(args ...string) {
		cmd := exec.Command("ffmpeg", append([]string{"-v", "error", "-y"}, args...)...)
		if err := cmd.Run(); err != nil {
			t.Skipf("造音频失败(跳过):%v", err)
		}
	}
	// 母轨：0-2 秒有声（模拟台词），2-6 秒静音
	mk("-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-af", "apad=whole_dur=6", voice)
	// BGM：6 秒连续音
	mk("-f", "lavfi", "-i", "sine=frequency=220:duration=6", bgm)

	got, note := mixBGMBed(context.Background(), f, voice, bgm, 6, out)
	if got != out {
		t.Fatalf("应返回混好的母轨，实际 %q（note=%s）", got, note)
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		t.Fatalf("混音产物缺失:%v", err)
	}
	// 时长必须仍是 6 秒（BGM 裁到片长，不能把成片拉长）；混音产物必须仍有音轨
	info, err := f.Probe(out)
	if err != nil {
		t.Fatalf("探测失败:%v", err)
	}
	if info.Duration < 5.5 || info.Duration > 6.6 {
		t.Errorf("配乐不该改变成片时长，期望≈6s，实际 %.2fs", info.Duration)
	}
	// 读不到 BGM 时：回退原母轨 + 一句实话，不因此失败
	got2, note2 := mixBGMBed(context.Background(), f, voice, filepath.Join(dir, "nope.wav"), 6, out+".2")
	if got2 != voice || note2 == "" {
		t.Errorf("BGM 缺失时应回退原母轨并播报，实际 (%q, %q)", got2, note2)
	}
	// 没设 BGM：完全不动
	if got3, note3 := mixBGMBed(context.Background(), f, voice, "", 6, out+".3"); got3 != voice || note3 != "" {
		t.Errorf("没设 BGM 时应原样返回，实际 (%q, %q)", got3, note3)
	}
}

// TestBGMNoteIsHonest 没铺 BGM 时必须如实说（而不是让人以为有配乐）。
func TestBGMNoteIsHonest(t *testing.T) {
	if BGMNote == "" {
		t.Fatal("没铺 BGM 时必须有一句实话")
	}
	for _, want := range []string{"未铺 BGM", "官方", "一条连续 BGM"} {
		if !containsStr(BGMNote, want) {
			t.Errorf("这句播报要说清 %q，实际：%s", want, BGMNote)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
