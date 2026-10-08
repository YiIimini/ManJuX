package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/llm"
	"github.com/YiIimini/ManJuX/internal/project"
)

// zz_probe_staging_live_test.go 临时探针：拿**真机那份方案** + 真 LLM 跑一次
// `TranslateShotStaging`，看"空正文那几镜的中文走位/动作"是不是真能翻成英文。
//
// 为什么必须有它：判据只能验纯函数（请求拼得对不对、结果怎么解析/过滤），
// **真实模型回什么**只有打一次才知道（"翻不出来/给回汉字"这一类只有真机看得见）。
// 没有 settings/项目/凭据时自动跳过；**只读，绝不写盘**（方案一个字都不改回磁盘）。
func TestZZProbeStagingTranslationOnRealPlan(t *testing.T) {
	settingsPath := filepath.Join("..", "..", "data", "settings.json")
	if os.Getenv("MANJUX_LIVE_PROBE") == "" {
		t.Skip("真机 LLM 探针：设 MANJUX_LIVE_PROBE=1 才跑")
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Skip("没有 settings.json，跳过探针")
	}
	var s config.Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Skip(err)
	}
	if strings.TrimSpace(s.LLM.APIKey) == "" {
		t.Skip("LLM 凭据为空，跳过探针")
	}
	client := llm.New(s.LLM.BaseURL, s.LLM.APIKey, s.LLM.Model, 300)
	if !client.Configured() {
		t.Skip("LLM 未配置齐（BaseURL/APIKey/Model），跳过探针")
	}
	root := filepath.Join("..", "..", "data", "projects")
	projID := "我捡的团子，暖了三千人的船·第01集·霜里刨出个活物"
	st, err := project.NewStore(root)
	if err != nil {
		t.Skip(err)
	}
	if _, err := os.Stat(st.PlanPath(projID)); err != nil {
		t.Skip("没有这份真机方案，跳过探针")
	}
	plan, err := st.LoadPlan(projID)
	if err != nil {
		t.Fatalf("读方案失败：%v", err)
	}
	todo := stagingTodo(plan)
	t.Logf("脚本阶段那套判据待翻的镜：%v（读边界已先拼好正文，所以这里通常为空）", shotIDsText(idsOf(todo)))
	// 真正要验的是**渲染阶段那条路**：读边界拼出来的正文缺中文走位 ⇒ 补翻 + 重拼。
	targets := refillTargets(plan)
	t.Logf("渲染阶段要补翻+重拼的镜：%v（FilledBodyCJK=%d）", shotIDsText(targets), plan.FilledBodyCJK)
	if len(targets) == 0 {
		t.Skip("这份方案没有「平台自己拼的正文且中文材料没能进正文」的镜，跳过")
	}
	before := shotBodyOf(plan, targets[0])
	n, err := RefillPlatformBodiesWithStaging(t.Context(), client, plan)
	if err != nil {
		t.Fatalf("真机补翻+重拼失败：%v", err)
	}
	t.Logf("重拼 %d 镜；重拼后 FilledBodyCJK=%d（0 = 中文材料不再少了）", n, plan.FilledBodyCJK)
	for _, id := range targets {
		sh := shotPtrByID(plan, id)
		if sh == nil {
			continue
		}
		t.Logf("镜 %d action_en=%q", id, sh.ActionEN)
		for k, v := range sh.PositionsEN {
			t.Logf("   走位 %s：%s", k, v)
		}
		// 与卡片翻译同一条纪律：写回来的必须**一个汉字都没有**。
		if project.CountCJK(sh.ActionEN) > 0 {
			t.Errorf("action_en 里混进了汉字：%q", sh.ActionEN)
		}
		for k, v := range sh.PositionsEN {
			if project.CountCJK(v) > 0 {
				t.Errorf("positions_en[%s] 里混进了汉字：%q", k, v)
			}
		}
		if strings.TrimSpace(sh.H3Prompt) == "" {
			t.Errorf("镜 %d 重拼之后正文是空的（不许把方案弄坏）", id)
		}
		if !strings.Contains(sh.H3Prompt, "In frame:") {
			t.Errorf("镜 %d 重拼后的正文里没有走位（英文翻出来了却没用上）", id)
		}
	}
	if plan.FilledBodyCJK != 0 {
		t.Errorf("补翻之后不该再报\"少了材料\"，实得 %d", plan.FilledBodyCJK)
	}
	if before == shotBodyOf(plan, targets[0]) {
		t.Error("正文没有被重拼（内容与原来逐字相同 ⇒ 英文走位没进去）")
	}
}

func idsOf(shots []project.Shot) []int {
	out := make([]int, 0, len(shots))
	for _, sh := range shots {
		out = append(out, sh.ID)
	}
	return out
}

// shotBodyOf 取某一镜的正文（探针里用来比"重拼前后是否真的变了"）。
func shotBodyOf(plan *project.Plan, id int) string {
	if sh := shotPtrByID(plan, id); sh != nil {
		return sh.H3Prompt
	}
	return ""
}
