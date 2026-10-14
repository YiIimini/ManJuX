package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// single_speaker_test.go 「一次只一张嘴」：同镜多说话人的硬条款必须**真的进提交版提示词**。
//
// 【为什么（2026-10-12，用户给了一篇公众号实测文）】文章给的第一条是**硬规矩**：
// 「别让好几个人同时处在正在说话的状态里」—— 模型分不清谁在出声的时候，它会自己挑一个，
// 挑错了就是观众看到的"抢词"（文章原话的现场：「本该男二说的话，被女主照着念了一遍」）。
// 做法是"一个画面里只留一张嘴"。
//
// 平台这一侧此前是**半边**的：画外音那半边有确定性约束（EnsureVoiceoverClause ⇒
// 说话人不在画面里时"画内的人一律闭嘴"）；**画面里两个人在说**这半边一条约束都没有。
// 这条门钉住补上的那半边，判据挂**装配结果**（不是"函数被调用过"）。
//
// 真机取证（夹具 plan_real_voiceover.json，11 镜）：**5 镜**画内说话人 ≥2
// —— 也就是说真实一集里将近一半的镜头此前一句"谁在什么时候开口"都没有。
func TestSingleSpeakerClauseOnRealPlan(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "project", "testdata", "plan_real_voiceover.json"))
	if err != nil {
		t.Fatalf("读不到真机夹具：%v", err)
	}
	var p project.Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("真机夹具不是合法方案：%v", err)
	}
	project.HealPlan(&p)
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()

	multi, quiet := 0, 0
	for i := range p.Shots {
		sh := &p.Shots[i]
		on := project.OnScreenSpeakers(&p, *sh)
		got := ShotRenderPrompt(proj, &p, sh)
		has := strings.Contains(got, project.SingleSpeakerClauseMarker)
		if len(on) >= 2 {
			multi++
			if !has {
				t.Errorf("镜 %d 有 %d 位画面内说话人 %v，却没有「一次只一张嘴」的条款 —— "+
					"模型只能自己排谁开口，排错就是抢词", sh.ID, len(on), on)
			}
			// 条款必须**点名**到这几位（不点名等于只说了一句套话）。
			for _, n := range on {
				if !strings.Contains(got, n) {
					t.Errorf("镜 %d 的条款没点名「%s」（画内说话人是 %v）", sh.ID, n, on)
				}
			}
			// 条款必须把"另一张嘴闭上"写清楚，而不只是"别同时说"。
			if !strings.Contains(got, "lips move") || !strings.Contains(got, "mouth stays completely closed") {
				t.Errorf("镜 %d 的条款没写清"+"'只许当前这位动嘴、其他人嘴闭着'：\n%s", sh.ID, tailOf(got, 400))
			}
		} else {
			quiet++
			if has {
				t.Errorf("镜 %d 只有 %d 位画面内说话人 %v，不该补「一次只一张嘴」——"+
					"多补只会白占模型的注意力", sh.ID, len(on), on)
			}
		}
	}
	// 门槛必须有意义：夹具里两种形态都得有，否则这条门是空的（夹具必须照真实形态）。
	if multi < 2 {
		t.Fatalf("真机夹具里只有 %d 镜是"+"'多画内说话人'，这条门失去意义（真实一集是 5/11）", multi)
	}
	if quiet < 1 {
		t.Fatalf("真机夹具里一镜"+"'单/无画内说话人'都没有（%d），"+"'不该多补'那一半断言是空的", quiet)
	}
}

// 条款必须**真的在最外层提交串里**：`assembleShotPrompt` 才是渲染主循环交出去的那一份。
func TestSingleSpeakerClauseReachesAssembledPrompt(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "project", "testdata", "plan_real_voiceover.json"))
	if err != nil {
		t.Fatalf("读不到真机夹具：%v", err)
	}
	var p project.Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("真机夹具不是合法方案：%v", err)
	}
	project.HealPlan(&p)
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()

	hits := 0
	for i := range p.Shots {
		sh := &p.Shots[i]
		if len(project.OnScreenSpeakers(&p, *sh)) < 2 {
			continue
		}
		got := assembleShotPrompt(proj, &p, sh, nil, nil)
		if !strings.Contains(got, project.SingleSpeakerClauseMarker) {
			t.Errorf("镜 %d 的提交串（装配收口那一步）里没有「一次只一张嘴」", sh.ID)
			continue
		}
		hits++
	}
	if hits == 0 {
		t.Fatal("夹具里没有一镜是多画内说话人 —— 这条门没验到任何东西")
	}
}

// 幂等：装配点会被调用两次（渲染 + 2K/重绘/音频驱动四路共用），条款只许有一份。
// 判据是 marker 的**个数**（不是"在不在"）—— 重复叠加时"在不在"照样绿。
func TestSingleSpeakerClauseIsIdempotent(t *testing.T) {
	proj, plan, shot := specFixture()
	plan.Characters = []project.Character{
		{ID: "char_shen", Name: "沈砚"},
		{ID: "char_xie", Name: "谢无咎"},
	}
	shot.Characters = []string{"char_shen", "char_xie"}
	shot.Dialogue = []project.Line{
		{Speaker: "沈砚", Text: "读我的钢印。"},
		{Speaker: "谢无咎", Text: "我先看账。"},
	}
	shot.H3Prompt += " 沈砚 (S1) says: <d>[Chinese]读我的钢印。</d> " +
		"谢无咎 (S2) says: <d>[Chinese]我先看账。</d>"

	once := ShotRenderPrompt(proj, plan, &shot)
	if n := strings.Count(once, project.SingleSpeakerClauseMarker); n != 1 {
		t.Fatalf("条款应恰好 1 份，实际 %d 份", n)
	}
	twice := ShotRenderPrompt(proj, plan, &shot)
	if n := strings.Count(twice, project.SingleSpeakerClauseMarker); n != 1 {
		t.Fatalf("装配点被调用两次后条款变成了 %d 份（幂等判据是 marker 在不在）", n)
	}
	// 幂等判据串必须与 project 侧登记的那一个逐字相同（两边各写一份就会走散）。
	if singleSpeakerMarker != project.SingleSpeakerClauseMarker {
		t.Errorf("pipeline 的 marker（%q）与 project 登记的那一个（%q）不一致",
			singleSpeakerMarker, project.SingleSpeakerClauseMarker)
	}
}

// 反面：说话人**全在画面外**时不许补这条 —— 那是 EnsureVoiceoverClause 的活，
// 两条一起补等于一条说"画内闭嘴"、另一条说"什么时候动嘴"。
func TestSingleSpeakerClauseIgnoresOffScreenOnly(t *testing.T) {
	plan := &project.Plan{Characters: []project.Character{
		{ID: "char_a", Name: "姜晚舟"},
		{ID: "char_b", Name: "小灯"},
	}}
	sh := &project.Shot{
		ID:         9,
		Characters: []string{"char_a"},
		Dialogue: []project.Line{
			{Speaker: "姜晚舟", Text: "把灯递给我。"}, // 画内
			{Speaker: "小灯", Text: "我够不着。"},   // 画外（不在本镜名单）
		},
		H3Prompt: "Cinematic, 3D CG. [Shot 1] a lamp is handed over.",
	}
	if got := ShotRenderPrompt(&project.Project{}, plan, sh); strings.Contains(got, project.SingleSpeakerClauseMarker) {
		t.Errorf("画内说话人只有 1 位，不该补「一次只一张嘴」：\n%s", tailOf(got, 400))
	}
	// 但画外那一半的约束**必须在**（这才是这对函数的分工）。
	if !strings.Contains(sh.H3Prompt, project.VoiceoverClauseMarker) {
		t.Error("画外说话人那一镜没有补「画内的人保持闭嘴」—— 两条判据的分工被打乱了")
	}
}
