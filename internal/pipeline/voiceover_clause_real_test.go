package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 真机夹具（与 project 包同一份，**逐字拷自盘上** `plan/plan.json`）：
// 镜 5 的台词是小灯说的，而小灯**不在本镜出场名单**里 ⇒ 平台必须补上"画内的人保持闭嘴"这条强制约束。
//
// 【为什么这是承重点】用户报的「旁白说话角色未对齐（另一人说话，其它角色动嘴）」：
// 那条约束此前因为模型把**每一句**都标成 `os=true` 而被判据跳过（见 project.OffScreenSpeakers
// 的注释），于是画内谁开口全凭模型自己安排 —— 补上之后平台才真正约束住了它。
func TestVoiceoverClauseOnRealPlan(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "project", "testdata", "plan_real_voiceover.json"))
	if err != nil {
		t.Fatalf("读不到真机夹具：%v", err)
	}
	var p project.Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("真机夹具不是合法方案：%v", err)
	}
	project.HealPlan(&p) // 读取边界那一步：按出场名单把误标的 os 改回来
	if len(p.Shots) < 6 {
		t.Fatalf("夹具少了镜 5/6：%d 镜", len(p.Shots))
	}

	// ① 镜 5（说话人不在本镜）⇒ 必须补画外音强制子句
	sh5 := p.Shots[4]
	if !EnsureVoiceoverClause(&p, &sh5) {
		t.Fatal("镜 5 的说话人不在本镜，平台必须补" +
			"\"画内的人保持闭嘴\"子句 —— 不补就是让画面里的人替他把嘴动了（用户报的那一条）")
	}
	if !strings.Contains(sh5.H3Prompt, project.VoiceoverClauseMarker) {
		t.Errorf("补完之后正文里没有那条强制串 %q", project.VoiceoverClauseMarker)
	}
	if !strings.Contains(sh5.H3Prompt, "OFF-SCREEN SPEAKER") ||
		!strings.Contains(sh5.H3Prompt, "nobody on screen mouths") {
		t.Errorf("子句必须明说：说话人不在画面里、画内的人不许对口型。实际尾部：%q",
			sh5.H3Prompt[maxInt(0, len(sh5.H3Prompt)-300):])
	}
	// ② 幂等：同一镜再调一次不该重复追加
	if EnsureVoiceoverClause(&p, &sh5) {
		t.Error("同一镜第二次调用不该再加一遍（幂等）")
	}
	// ③ 镜 1（说话人**就在**画面里）⇒ 不许加闭嘴句 ——
	//    加了就是把"该说话的人"也一起禁掉，声音与口型照样对不上。
	sh1 := p.Shots[0]
	if EnsureVoiceoverClause(&p, &sh1) {
		t.Error("镜 1 是姜晚舟在画面里亲口说，不许加\"画内的人保持闭嘴\"")
	}
}
