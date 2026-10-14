package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// h3_prompt_mirror_test.go **中英对照块不许进提交版提示词**（2026-10-12）。
//
// 【为什么（用户给了一篇公众号实测文，要求按它排查"画面里凭空多出字幕"）】
// 拿真机夹具走**真实装配**逐镜量（`<d>` 外、主体名已扣除的可避免汉字）：
//
//	plan_real_voiceover.json       夹具原文 0  → 装配后每镜 1502
//	skill-produced-plan-ep01.json  夹具原文 6  → 装配后每镜 395
//
// 夹具**自己是干净的** —— 那些汉字全是平台在装配时追加的对照块
// （`(中文对照:……)`，见 project.StripCNMirror）。而本包的注释里写得很清楚：
// 中文半**是写给维护者看的**，图片模型会把它当成"要画的字"整段照抄到画面上（真机出过这一版）。
// 视频侧此前一直没清 —— 这条门钉住"从今往后提交版里一个对照块都没有"。
//
// 判据挂**装配结果**而不是"某个函数有没有被调用"：有函数没接上 = 没写。
func TestAssembledShotPromptHasNoCNMirror(t *testing.T) {
	for _, fx := range []string{"plan_real_voiceover.json", "skill-produced-plan-ep01.json"} {
		raw, err := os.ReadFile(filepath.Join("..", "project", "testdata", fx))
		if err != nil {
			t.Fatalf("读不到真机夹具 %s：%v", fx, err)
		}
		var p project.Plan
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("%s 不是合法方案：%v", fx, err)
		}
		project.HealPlan(&p)
		proj := &project.Project{}
		proj.Config.Params = project.DefaultStageParams()
		names := mirrorFixtureNames(&p)

		assembled, spoken := 0, 0
		for i := range p.Shots {
			sh := &p.Shots[i]
			got := assembleShotPrompt(proj, &p, sh, nil, nil)
			assembled++

			if k := strings.Index(got, "中文对照"); k >= 0 {
				t.Errorf("[%s] 镜 %d 的提交版里还有中英对照块（第一次出现在第 %d 字）：\n%s",
					fx, sh.ID, k, window(got, k))
			}
			// 英文规则必须还在（只摘中文半，不许连英文一起摘掉）。
			for _, marker := range []string{onScreenTextMarker, spokenLinesMarker} {
				if !strings.Contains(got, marker) {
					t.Errorf("[%s] 镜 %d 缺英文条款 %q —— 摘对照块时把英文也摘掉了", fx, sh.ID, marker)
				}
			}
			// 台词一个字都不许动（汉字合法的两处位置之一）。
			// 判据取**每一段 `<d>…</d>` 的内容**是否都还在 —— 数标签个数不行：
			// 语音纪律条款自己也会提到 `<d>`，装配本来就会让标签个数变多。
			before := dialogueBodies(sh.H3Prompt)
			after := dialogueBodies(got)
			spoken += len(before)
			for _, body := range before {
				if !containsStr(after, body) {
					t.Errorf("[%s] 镜 %d 的台词「%s」装配之后不见了（台词通道被动过）", fx, sh.ID, body)
				}
			}
			// 主体名一个字都不许动（汉字合法的另一处位置：官方要求"用文字点名主体"）。
			if len(names) > 0 && !hasAnyName(got, names) {
				t.Errorf("[%s] 镜 %d 的提交版里一个主体名都没有了 —— 编号清单靠它对号", fx, sh.ID)
			}
		}
		if assembled == 0 {
			t.Fatalf("%s 一镜都没装配出来，这条门是空的", fx)
		}
		// 夹具形态反证：真机夹具自己必须带 `<d>` 台词，否则"台词没被动过"是空断言。
		if spoken == 0 {
			t.Fatalf("%s 的夹具里一个 `<d>` 都没有 —— 这形态不该出现在真机产物上", fx)
		}
	}
}

// TestCNMirrorStripIsWiredIntoBothFunnels 两个装配入口都接了这一步。
//
// 为什么是**两个**：`ShotRenderPrompt` 是四条出像素路径（主渲染 / 单镜 2K / 局部重绘 /
// 音频驱动）共用的那一段；`assembleShotPrompt` 在主渲染那条路上又追加了参考图清单 / 道具锚定 /
// 转场字幕 / 镜头表字段 —— 每一条都可能再带一份对照块，所以收口那一步必须再清一次。
func TestCNMirrorStripIsWiredIntoBothFunnels(t *testing.T) {
	src := string(mustRead(t, "prompts.go"))
	if n := strings.Count(src, "promptOut = project.StripCNMirror(promptOut)"); n != 1 {
		t.Errorf("ShotRenderPrompt 的收口必须恰好调用一次 StripCNMirror，实际 %d 次", n)
	}
	stage := string(mustRead(t, "stages_render.go"))
	if n := strings.Count(stage, "return project.StripCNMirror(EnsureShotSpecClause("); n != 1 {
		t.Errorf("assembleShotPrompt 的收口必须恰好包一层 StripCNMirror，实际 %d 次", n)
	}
	// 反证：清这一步不许出现在**别的**地方（散落的清理点 = 总有路径漏掉）。
	if n := strings.Count(stage, "project.StripCNMirror("); n != 1 {
		t.Errorf("stages_render.go 里 StripCNMirror 应只出现 1 次（装配收口），实际 %d 次", n)
	}
}

// reDialogueBody 抓 `<d>…</d>` 的**内容**（含语言标记，逐字）。
var reDialogueBody = regexp.MustCompile(`(?s)<d>(.*?)</d>`)

func dialogueBodies(s string) []string {
	var out []string
	for _, m := range reDialogueBody.FindAllStringSubmatch(s, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func mirrorFixtureNames(p *project.Plan) []string {
	var out []string
	for _, c := range p.Characters {
		if n := strings.TrimSpace(c.Name); n != "" {
			out = append(out, n)
		}
	}
	for _, s := range p.Scenes {
		if n := strings.TrimSpace(s.Name); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func hasAnyName(s string, names []string) bool {
	for _, n := range names {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// window 取下标 k 附近的一段，给人看懂是哪一处出了问题。
func window(s string, k int) string {
	lo := k - 120
	if lo < 0 {
		lo = 0
	}
	hi := k + 160
	if hi > len(s) {
		hi = len(s)
	}
	return s[lo:hi]
}
