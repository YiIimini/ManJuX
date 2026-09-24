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
	if rp.CondCache && !caps.Local {
		miss = append(miss, "两阶段条件缓存")
	}
	if len(miss) == 0 {
		return ""
	}
	return fmt.Sprintf("本次引擎不支持这些项目参数,已自动跳过:%s;需要它们请在项目参数里切到本地 ComfyUI 引擎",
		strings.Join(miss, "、"))
}
