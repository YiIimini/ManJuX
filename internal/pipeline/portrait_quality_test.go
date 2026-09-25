package pipeline

import (
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 定妆照"丑"的两个具体成因(2026-09-25 用户报「动漫化角色还很丑」后看真机定妆照定位)。
//
// 那张图上有两处一眼可辨的缺陷,都不是"画风"问题,而是**提示词把不可能画对的东西
// 塞进了半身幅**:
//  1. 胸口铜扣上刻着"拇指盖大、几乎磨没的『禾』字" → 832×1248 里没有足够像素,
//     模型不省略,而是糊成一片灰渍(同一张图上腕部、肩部也有同类污渍);
//  2. 提示词没写姿态,模型自作主张画了**双臂抱胸** → 半身幅里手只占很少像素,
//     画成了一只连指手套。
//
// 所以这两条都在装配点处理掉,而不是等出图后再挑毛病。
func TestStripMicroTextForImage(t *testing.T) {
	in := "a thumb-nail-sized brass clip clamped at the ponytail base engraved with a tiny worn '禾' character whose second stroke is almost rubbed away"
	got := StripMicroTextForImage(in)
	if strings.Contains(got, "禾") {
		t.Errorf("微雕汉字必须被替换掉(渲染出来只会是灰渍):%q", got)
	}
	if !strings.Contains(got, "engraved with a faint worn mark") {
		t.Errorf("应替换为「磨旧的痕迹」这类可画的描述:%q", got)
	}
	// 其它写法
	if s := StripMicroTextForImage(`a brass plate printed with "禾" character`); strings.Contains(s, "禾") {
		t.Errorf("引号+character 的写法也要处理:%q", s)
	}
	// 正常描述不得被改动
	plain := "a slim young man in a grey robe, short black hair, plain background"
	if StripMicroTextForImage(plain) != plain {
		t.Errorf("无关描述不该被改动:%q", StripMicroTextForImage(plain))
	}
}

// TestPortraitPoseClause 人像幅面必须写姿态纪律(否则模型自己安排一个画不好的动作)。
func TestPortraitPoseClause(t *testing.T) {
	if c := PortraitPoseClause(KindHuman); !strings.Contains(c, "hands out of frame") {
		t.Errorf("人像应带姿态纪律(手出画或简单):%q", c)
	}
	if c := PortraitPoseClause(KindChibi); c == "" {
		t.Error("Q 版同样是半身幅,也要姿态纪律")
	}
	if c := PortraitPoseClause(KindObject); c != "" {
		t.Errorf("器物没有人手问题,不该塞人像姿态条款:%q", c)
	}
	// 端到端:装配结果里两条都在
	proj := &project.Project{}
	proj.Config.Params = project.DefaultStageParams()
	plan := &project.Plan{Style: "2D DONGHUA", Characters: []project.Character{{ID: "c1", Name: "顾青禾"}}}
	ch := project.Character{
		ID: "c1", Name: "顾青禾",
		ImagePrompt: "a slim woman, a brass clip engraved with a tiny worn '禾' character, plain background, upper-body framing, front-facing",
	}
	got := PortraitPromptForProject(proj, plan, ch)
	if strings.Contains(got, "禾") {
		t.Errorf("定妆照提示词里不该再出现微雕汉字:\n%s", got)
	}
	if !strings.Contains(got, "hands out of frame") {
		t.Errorf("定妆照提示词应带姿态纪律:\n%s", got)
	}
}
