package project

import (
	"strings"
	"testing"
)

// G057「镜的内部切点超出片段时长」的**反例**（2026-09-29 审计 P0-2）。
//
// ## 为什么单写这一条
//
// 审计逐条对了 17 道 `fail` 级门的触发分支覆盖率：**16 道被反例覆盖，只有这一道没有**
// （`add("fail", ...)` 那一行从未执行过，跨包口径也一样）。而它的判据是
//
//	tp.ms >= sh.Duration*1000
//
// —— **秒/毫秒换算 + 边界方向**，恰恰是最容易写错又最不该错的地方，而且它是 `fail` 级
// = **直接拦住渲染**。判据写反的两种后果都很难看：
//
//	· 偏严 → 合规方案全被拒收，整个平台渲不出片；
//	· 偏松 → 该拦的放过去，成片时间轴与时长对不上（节奏乱）。
//
// 一条判据如果连"它到底会不会响"都没被验证过，那它就不是保险，是装饰。
func TestG057CutBeyondDuration(t *testing.T) {
	// 造一镜：片长 6 秒，正文里写切点。`shotTimestamps` 认的是「[Shot N] At MM:SS.mmm, …」
	// 这种官方多镜写法（夹具照**真实形态**写，不写"意思一下"的假正文）。
	shotWith := func(at string) *Plan {
		p := samplePlan()
		p.Shots = p.Shots[:1]
		p.Shots[0].ID = 1
		p.Shots[0].Duration = 6
		p.Shots[0].H3Prompt = "detailed_description:\nGUOFENG. [Shot 1] At 00:00.000, he turns.\n" +
			"[Shot 2] At " + at + ", he raises the sparrow orb.\n" +
			"overall_soundscape:\nwind."
		return p
	}
	hasG057 := func(p *Plan) bool {
		for _, is := range ValidatePlan(p) {
			if is.Level == "fail" && strings.Contains(is.Msg, "内部切点超出了片段时长") {
				return true
			}
		}
		return false
	}

	// ① **反例（该响）**：6 秒片段里写着 At 00:09.000 —— 切点比片长还晚，自相矛盾
	if !hasG057(shotWith("00:09.000")) {
		t.Error("6 秒的片段里出现 00:09.000 的切点必须报 fail —— 时间轴与片长对不上，" +
			"模型拿到的节奏是错的（这条是 17 道 fail 级门里唯一从没被反例跑过的）")
	}
	// ② **边界正好相等也要响**：`>=` 不是 `>` —— 切点等于片长意味着"最后一刀在片尾之后"
	if !hasG057(shotWith("00:06.000")) {
		t.Error("切点正好等于片长（00:06.000 / 6 秒）也必须报 —— 判据是 >= 不是 >")
	}
	// ③ **反方向（不该响）**：5.999 秒仍在片内，**不能拦**
	if hasG057(shotWith("00:05.999")) {
		t.Error("00:05.999 还在 6 秒之内，报 fail 会把合规方案拒收（这就是判据方向写反的后果）")
	}
	// ④ 正常切点当然不能响
	if hasG057(shotWith("00:03.000")) {
		t.Error("00:03.000 是正常切点，不该报")
	}
	// ⑤ 时长的边界：片长 0 的镜头跳过判据（没法判断"超出"），不该因此误报
	zero := samplePlan()
	zero.Shots[0].Duration = 0
	zero.Shots[0].H3Prompt = "detailed_description:\n[Shot 1] At 00:09.000, x.\n[Shot 2] At 00:20.000, y."
	if hasG057(zero) {
		t.Error("片长未定（0）的镜头不该按「切点超出」报 fail —— 没有参照物就不判")
	}
}
