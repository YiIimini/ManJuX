package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// spoken_lines_test.go 「只说 <d> 里的台词、其余时间没有人声」这条纪律**真的进提示词**。
//
// 为什么值得单独钉（2026-10-07，用户「配音存在乱配音」）：
//
//	ASR 复验真机第01集 13 镜抓到 —— 镜 4「已读取。无对应档位。」念两遍、镜 6 两遍、
//	镜 7「自检窗口，七十二标准时。」三遍；镜 3 多出一整串「跟强我的对数还修戴眼影一下,也续点倾向」
//	而第一句「80000。」根本没念；镜 1 多出「没禁条」、镜 11 多出「图墙白头圣呢﹔裙尾便宜着人呢」。
//
// 机制是"提示词里没有一句话管其余几秒怎么办"：H3 一次要出整条音轨，台词只占其中 2~4 秒，
// 剩下的时间模型自己填。这条条款就是给那几秒立规矩 —— 它一旦没进提示词（或者进了两遍），
// 症状会在**成片音轨**上出现，而代码、日志、机械质检都不会报错。
func TestEnsureSpokenLinesClauseReachesTheRenderedPrompt(t *testing.T) {
	proj, plan, shot := specFixture()
	shot.Dialogue = []project.Line{{Speaker: "沈砚", Text: "读我的钢印。"}}
	got := ShotRenderPrompt(proj, plan, &shot)
	if !strings.Contains(got, spokenLinesMarker) {
		t.Fatal("渲染提示词里没有语音纪律条款 —— 模型会继续在空档里自己填话")
	}
	for _, want := range []string{
		"exactly once",   // 每句只念一遍（镜 4/6/7 的"念了两三遍"）
		"NO human voice", // 其余时间没有人声（镜 3/1/11 的"凭空多出话"）
		"<d>",            // 只认标签里的台词
		// 【2026-10-08 真机《顺其自然》】"每句只念一遍"管不住的是**假起手**：
		// 镜 5 把「他就拆开,一张都不超过一万」先念糊一遍、再念一遍；
		// 镜 1 把「十一月底」连着说了两遍。所以单独写死一条：念砸了接着往下念，不许回头重念。
		"No false starts and no re-reading",
		"不许把同一句重新起头",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("条款里缺少 %q —— 少了这一句就管不住对应的那类问题", want)
		}
	}
	// 幂等：装配点会被调用两次（渲染 + 2K/重绘/音频驱动四路共用），条款只许有一份。
	twice := EnsureSpokenLinesClause(got, &shot)
	if n := strings.Count(twice, spokenLinesMarker); n != 1 {
		t.Fatalf("条款出现了 %d 次 —— 装配点被调用两次就会重复叠加", n)
	}
	if twice != got {
		t.Error("已含条款的提示词应原样返回（幂等）")
	}
}

// 没有台词的镜头另给一句：不许造人声。
//
// 【为什么这条也算真缺陷】真机实测（见 ShotSpeakers 的文件头）：本集 54 个镜头里 17 个
// 既没有台词也没有旁白，却都背着一份"这些人要用这些嗓子说话"的音色授权清单 ——
// 模型只能自己造人声，用户听到的是满耳童声嘈杂。没有台词就是**不许有人声**。
func TestEnsureSpokenLinesClauseOnSilentShot(t *testing.T) {
	proj, plan, shot := specFixture()
	shot.Dialogue = nil
	shot.Narration = ""
	got := EnsureSpokenLinesClause("detailed_description:\nGUOFENG. [Shot 1] the corridor is empty.", &shot)
	if !strings.Contains(got, spokenLinesMarker) {
		t.Fatal("无台词镜也要有条款（那正是最容易被填成人声嘈杂的一类）")
	}
	if !strings.Contains(got, "NO spoken dialogue at all") {
		t.Error("无台词镜必须明说：本镜一句台词都没有")
	}
	if strings.Contains(got, "the ONLY human speech in this shot is") {
		t.Error("无台词镜不该收到「只念这些台词」那一版（它没有台词可念，等于给了授权）")
	}
	// 只有旁白（画外音）也算"有人声要说"，走的仍是有台词那一版。
	shot.Narration = "舱内只剩下嗡鸣。"
	if got := EnsureSpokenLinesClause("X", &shot); !strings.Contains(got, "the ONLY human speech in this shot is") {
		t.Error("有旁白的镜头属于「有人声要说」，应拿到「只念这些」那一版")
	}
	// nil 镜头：不 panic、不塞条款（没有镜头就没有可说的台词）。
	if got := EnsureSpokenLinesClause("X", nil); got != "X" {
		t.Errorf("nil 镜头应原样返回，实际 %q", got)
	}
	_ = proj
	_ = plan
}
