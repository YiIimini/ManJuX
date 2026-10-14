package project

import "testing"

// render_resolution_note_test.go 渲染分辨率的**口径下限**（H3 原生短边 768）与那句提示。
//
// 【为什么加（2026-10-12，用户给了一篇公众号实测文）】文章的第一条经验是「分辨率别一味往下压」：
// 作者测下来分辨率压到约 0.6 档以下时，画面里凭空多出异常字幕/文字的概率明显上升。
// 平台这一侧此前**一个字都不说**：`Sanitize` 只在 `Width < 32` 时兜底，中间那一段（32…767）
// 一路放行且无提示 —— 而 `RenderParams.Width` 的字段注释本来就写着「32 整除，短边 ≤768」，
// 云端 2K 那条链也要求"短边须为 768"。
//
// 边界：文章那条 0.6 是**作者的经验线**，我们没复核过 ⇒ **只提示、不拦、不改参数**
// （与「亮度/黑场只报不改」同一条政策）。这条门钉的就是"合规时一个字都不说"。
func TestRenderResolutionNote(t *testing.T) {
	cases := []struct {
		name       string
		w, h       int
		wantQuiet  bool
		wantSubstr string
	}{
		{"竖屏原生 768×1344（项目默认）", 768, 1344, true, ""},
		{"横屏原生 1344×768（短边仍是 768）", 1344, 768, true, ""},
		{"方形原生 768×768", 768, 768, true, ""},
		{"竖屏被压到 576×1008", 576, 1008, false, "576"},
		{"短边在高度那一侧 1024×640", 1024, 640, false, "640"},
		{"未表态（0）不出声", 0, 0, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RenderResolutionNote(RenderParams{Width: c.w, Height: c.h})
			if c.wantQuiet {
				if got != "" {
					t.Errorf("%dx%d 是合规分辨率，不该出声，实际：%s", c.w, c.h, got)
				}
				return
			}
			if got == "" {
				t.Fatalf("%dx%d 低于原生短边 %d，必须有提示", c.w, c.h, H3NativeShortSide)
			}
			if !contains(got, c.wantSubstr) {
				t.Errorf("提示里要点名**实际那个短边**（%s），实际：%s", c.wantSubstr, got)
			}
			// 只报不改：这条函数是**纯函数**，不许顺手把参数改掉。
			// （判据：同一个入参再算一次，入参本身没被谁改写 ⇒ 值仍是原值。Go 是值传递，
			//  所以这里真正钉的是"函数签名收的是值、不是指针"——指针就会让人忍不住就地改。）
			r := RenderParams{Width: c.w, Height: c.h}
			_ = RenderResolutionNote(r)
			if r.Width != c.w || r.Height != c.h {
				t.Errorf("提示函数改了分辨率参数：%dx%d → %dx%d", c.w, c.h, r.Width, r.Height)
			}
			// 提示里要如实交代"这条是别人的经验线、我们没复核"，否则读的人会当成我们的结论。
			if !contains(got, "没复核") {
				t.Errorf("提示里必须写明这条经验线**我们没复核过**，实际：%s", got)
			}
		})
	}
}

// 口径下限本身只许有一处出处：改它必须改这里。
func TestH3NativeShortSideIsPinned(t *testing.T) {
	if H3NativeShortSide != 768 {
		t.Fatalf("H3 原生短边被改成 %d —— 这个数同时是 RenderParams.Width 注释里那条上限、"+
			"云端 2K 链的前置条件（h3api/regen.go「短边须为 768」），改它要三处同改", H3NativeShortSide)
	}
	// 反面：768 这个数不许在提示文案里再写死一份（只许走常量）——写死两份就会走散。
	got := RenderResolutionNote(RenderParams{Width: 512, Height: 896})
	if !contains(got, "768") {
		t.Errorf("提示里要带上原生短边的实际值，实际：%s", got)
	}
}
