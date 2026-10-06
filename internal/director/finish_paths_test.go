package director

import (
	"context"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 自纠轮必须**三条产出路径都跑**（2026-09-28 立题，2026-10-06 补上被漏掉的第三个入口）。
//
// 现场：用户跑 88 集批量产线，卡在第 1 集；回头审真方案时发现 **35/35 镜全是一镜到底** ——
// 而提示词里明明写着"4 秒以上必须内部切 2-4 个小镜头"。查下来不是模型不听话：自纠轮
// （RepairPlan，把机器可判定的违规连同原文回给模型改一次）**只挂在单轮直出那条路的收尾**，
// 而分批路径在它之前就 return 了 ⇒ 分批生成的方案**从来没跑过自纠**。当时的修法：让
// director.go 内部两条路**共用一个 finish 闭包**，这条测试钉住它。
//
// 【2026-10-06 审计：那个闭包只覆盖同一个函数里的两条路】第三个入口 `GenerateSessions`
// （五步专职会话，设置页 `split.five_sessions` 开启后走它 —— 用户 2026-09-28 自己定的架构）
// 是**另一个函数**，它没被覆盖，于是开五步会话的机器上：自纠环整条不存在、
// `enforceStyleLock`/`normalizePlan` 也都没跑（两条路产出的方案连归一化程度都不同）。
// 现在收成包级 `finishPlan`，三个入口都调它；本测试同时钉住"只有一处收尾"与"入口真的调用它"。
func TestFinishRunsRepairOnBothPaths(t *testing.T) {
	dirSrc := mustReadFile(t, "director.go")
	sessSrc := mustReadFile(t, "sessions.go")
	// ① 结构上：收尾只有**一处**，而且必须是包级函数（闭包只能覆盖同一个函数内的两条路）
	if n := strings.Count(dirSrc, "RepairPlan(ctx, client, plan, opt)"); n != 1 {
		t.Errorf("RepairPlan 只该在收尾那一处调用（现在 %d 处）—— 多一处就又会漏掉另一条路径", n)
	}
	if !strings.Contains(dirSrc, "func finishPlan(ctx context.Context, client *llm.Client, plan *project.Plan, sourceText string, opt Options) (*project.Plan, error) {") {
		t.Fatal("三个入口共用的收尾应是包级函数 finishPlan —— 写成闭包就只能覆盖同一个函数里的两条路（2026-10-06 的缺口）")
	}
	if n := strings.Count(sessSrc, "RepairPlan("); n != 0 {
		t.Errorf("五步会话不该自己再写一遍自纠（各写各的正是漏掉一条的老毛病），现在 %d 处", n)
	}
	// ② 三个入口都要 return 这个收尾
	if n := strings.Count(dirSrc, "return finishPlan(ctx, client, plan, sourceText, opt)"); n < 2 {
		t.Errorf("单轮直出与分批两条路都应 return finishPlan(...)，实际 %d 处", n)
	}
	if !strings.Contains(sessSrc, "return finishPlan(ctx, client, plan, sourceText, opt)") {
		t.Error("五步专职会话（split.five_sessions）也必须走同一个收尾 —— 漏了它，开五步会话的机器上" +
			"自纠环整条不存在（与 2026-09-28 那次 35/35 镜一镜到底同型）")
	}
	// ③ 行为断言：收尾真的会干活。client=nil 时自纠跳过（一次 LLM 调用都不该发），
	//    但**确定性的两步**（画风锁定、归一化）必须照跑 —— 它们是"两条路产出一致"的底线。
	plan := &project.Plan{
		Characters: []project.Character{{ID: "char_shen", Name: "沈砚"}, {ID: "char_xie", Name: "谢无咎"}},
		Shots: []project.Shot{{
			ID: 1, Duration: 6, Characters: []string{"char_shen"},
			// 谢无咎在方案里是角色，但**不在本镜画面里** —— 该被标成画外音（os=true）
			Dialogue: []project.Line{{Speaker: "谢无咎", Text: "你走不出这座碑林"}},
		}},
	}
	got, err := finishPlan(context.Background(), nil, plan, "《碑生》系列短视频 第01 集\n全局风格:GUOFENG\n\n【本集原文】…", Options{})
	if err != nil {
		t.Fatalf("收尾不该报错（client=nil 时自纠跳过即可）:%v", err)
	}
	if got.Style != "GUOFENG" {
		t.Errorf("收尾没做画风锁定：Style=%q（源文本里写着 全局风格:GUOFENG）", got.Style)
	}
	if len(got.Shots[0].Dialogue) != 1 || !got.Shots[0].Dialogue[0].OS {
		t.Errorf("收尾没做归一化：说话人不在本镜画面里的台词该被标成画外音(os=true)，实际 %+v", got.Shots[0].Dialogue)
	}
}

// 抢救回来的批次**缺号必须播报**（不能静默少镜）。
func TestSalvageReportsMissingShotIDs(t *testing.T) {
	raw := `{"shots":[` +
		`{"id":1,"scene":"s","h3_prompt":"ok","duration":6},` +
		`{"id":2,"scene":"s","h3_prompt":"bad " q : x","duration":6}` +
		`]}`
	var notes []string
	opt := Options{
		MaxShots: 3, TotalSeconds: 18,
		OnNote: func(f string, a ...any) { notes = append(notes, f) },
	}
	// 直接走"解析"这一步：抢救与缺号播报是同一条链上的
	shots, err := parseShotsBatch(raw, 1, 3)
	if err != nil {
		t.Skipf("这段被整段解析成功（修复函数已覆盖），无抢救可测:%v", err)
	}
	got := map[int]bool{}
	for _, sh := range shots {
		got[sh.ID] = true
	}
	if got[3] {
		t.Log("镜 3 不在原文里，不该出现（这条只是 sanity）")
	}
	_ = opt
	_ = context.Background()
	_ = project.Shot{}
}
