package pipeline

import (
	"fmt"
	"strings"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 参数按能力过滤(审计 P1-2 的"引擎能力"收口)。
//
// 行为不变的部分:能做的照做。变的是**做不到的要说出来** —— 旧实现是
// `Job.Seam: rp.Seam && engName == "comfy"`,云端跑的时候用户设了"镜间连续"
// 却不生效,而日志里一个字都没有。这类"设了没反应"正是用户反复报障的那一类。
//
// 告警粒度是**每次渲染一条**(不是每镜一条),不会把日志淹掉。

// unsupportedParamNote 返回"本次引擎做不到、但项目参数里开着"的说明(空 = 无)。
func unsupportedParamNote(caps engine.Caps, rp project.RenderParams) string {
	var miss []string
	if rp.Seam && !caps.Seam {
		miss = append(miss, "镜间连续接缝")
	}
	// 这里只认**显式勾选**:CondCacheOn() 把 nil(缺键/未表态)当开,那是"生效性"口径;
	// 而这条提示是给"用户自己在项目里开了、引擎却给不了"用的 —— 拿生效性口径来刷,
	// 会让每个没碰过该开关的项目在云端引擎下都收到一条无意义的告警(闸门 TestUnsupportedParamNote 钉住)。
	if rp.CondCache != nil && *rp.CondCache && !caps.Local {
		miss = append(miss, "两阶段条件缓存")
	}
	// 云端引擎**不支持 seed**:H3 的 /v2/video_generation 请求体只有
	// model/content/duration/resolution/ratio(官方 API reference;社区整理的
	// awesome-minimax-h3 亦写明 "There is no seed parameter. Generations are not
	// reproducible")。项目里设了固定 seed(render.seed != 0)时,引擎会在提交每一镜时
	// 逐镜告警(见 h3api.Render),但"这次渲染整体做不到某个参数"属于**能力清单**的职责 ——
	// 用户需要在一个地方看清"哪些参数本次根本不生效",而不是从几十条逐镜日志里拼出来。
	//
	// 判据用 !caps.Local(与条件缓存同一条):云端引擎没有"本地"这一概念,
	// 而 seed 是本地采样器的入参。将来若有支持 seed 的云端引擎,应在 engine.Caps 里
	// 补一个显式能力位,而不是在这里加引擎名判断(见 caps.go 的说明)。
	if rp.Seed != 0 && !caps.Local {
		miss = append(miss, "固定种子 seed(云端请求体没有该字段,产物**不可复现**)")
	}
	if len(miss) == 0 {
		return ""
	}
	return fmt.Sprintf("本次引擎不支持这些项目参数,已自动跳过:%s;需要它们请在项目参数里切到本地 ComfyUI 引擎",
		strings.Join(miss, "、"))
}
