package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 配乐进资产库（2026-09-28 用户要求：生成的背景音乐也进资产库管理，后续遇到相同的需求直接复用）。
//
// 配乐没有天然的"名字"，决定能不能复用的是**这段需求**本身 ⇒ 条目名 = 归一后的需求描述，
// 归一必须稳定（同一句需求在不同集/不同项目里要算出同一个名字，否则复用永远不命中）。
func TestMusicEntryNameIsStable(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"低沉竹笛与弦乐，压抑，慢速", "低沉竹笛与弦乐·压抑·慢速"},
		{"低沉竹笛与弦乐, 压抑,  慢速", "低沉竹笛与弦乐·压抑·慢速"}, // 半角逗号/多余空格
		{"低沉竹笛与弦乐\n压抑\t慢速", "低沉竹笛与弦乐·压抑·慢速"},  // 换行与制表符
		{"  两端空白要剪掉  ", "两端空白要剪掉"},
	}
	for _, c := range cases {
		if got := MusicEntryName(c.in); got != c.want {
			t.Errorf("MusicEntryName(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
	// 同一句需求的几种写法必须归一到同一个名字（否则复用不命中）
	a := MusicEntryName("低沉竹笛与弦乐，压抑，慢速")
	b := MusicEntryName("低沉竹笛与弦乐, 压抑 ,慢速")
	if a != b {
		t.Errorf("同一需求的两种写法应归一成同一名字：%q vs %q", a, b)
	}
	// 超长截断，且不留下悬空分隔符
	long := strings.Repeat("很长的配乐需求描述", 20)
	if got := MusicEntryName(long); len([]rune(got)) > 60 || strings.HasSuffix(got, "·") {
		t.Errorf("超长应截断到 60 字且不带悬空分隔符，实际 %q(%d 字)", got, len([]rune(got)))
	}
}

// TestMusicRoundTrip 配乐入库后能被**同需求**再查出来（复用链的核心）。
func TestMusicRoundTrip(t *testing.T) {
	root := t.TempDir()
	lib, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	brief := "低沉竹笛与弦乐，压抑，慢速，碑下独白段落"
	name := MusicEntryName(brief)
	// 造一个真的小音频文件（Put 只校验"文件非空"，不解码 —— 这正是音乐能进同一个库的原因）
	audio := filepath.Join(t.TempDir(), "track.mp3")
	if err := os.WriteFile(audio, []byte("ID3\x03\x00\x00\x00\x00\x00\x00FAKE-AUDIO-BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := lib.Put("music", name, brief, "", audio, "ep01", "ace-step")
	if err != nil {
		t.Fatalf("配乐入库失败:%v", err)
	}
	// 主文件必须叫 track.*（翻库时一眼认出这是曲子，不是图）
	if !strings.Contains(filepath.ToSlash(e.Image), "track") {
		t.Errorf("配乐主文件名应含 track，实际 %q", e.Image)
	}
	// 同需求（换个写法）要能查出来
	again := MusicEntryName("低沉竹笛与弦乐, 压抑 ,慢速,碑下独白段落")
	if again != name {
		t.Fatalf("同一需求的两种写法归一不同：%q vs %q", again, name)
	}
	e2, path, ok := lib.Match("music", again)
	if !ok {
		t.Fatal("同需求的配乐应能从库里命中（否则复用链断了）")
	}
	if e2.Source != "ace-step" {
		t.Errorf("来源应记下来（用于播报「来自谁」），实际 %q", e2.Source)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("命中的文件应真实存在:%v", err)
	}
	// 不同需求不该命中
	if _, _, ok := lib.Match("music", MusicEntryName("激昂战鼓，快速")); ok {
		t.Error("不同需求不该命中同一条目")
	}
}

// TestMusicKindRegistered 分类必须注册（未注册的 kind 会被 entryDir 拒绝）。
func TestMusicKindRegistered(t *testing.T) {
	if !ValidKind("music") {
		t.Fatal("music 必须是合法分类（否则配乐根本存不进库）")
	}
	if KindLabel("music") != "配乐" {
		t.Errorf("music 的中文名应是「配乐」，实际 %q", KindLabel("music"))
	}
}
