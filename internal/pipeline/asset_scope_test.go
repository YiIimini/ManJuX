package pipeline

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/project"
)

// 资产补齐范围的穷举测试(审计 P0-9)。
//
// 守的是"只改一镜 seed,却顺手生成全片资产":单镜重渲的成本应当只与那一镜有关,
// 而不是与整部剧的角色数×数分钟(H3 多视角)挂钩。
func TestNeedForShotsScopesToRenderedShots(t *testing.T) {
	plan := []project.Shot{
		{ID: 1, Mode: "ref2va", Characters: []string{"c1"}, Scene: "s1"},
		{ID: 2, Mode: "ref2va", Characters: []string{"c2", "c3"}, Scene: "s2"},
		{ID: 3, Mode: "t2va", Characters: []string{"c4"}, Scene: "s3"}, // 纯文生:不吃参考图
	}

	// 全片渲染(不传 only)= 不做范围限制
	if n := needForShots(nil); n != nil {
		t.Fatal("没有镜头集时应为 nil(不做范围限制),而不是空集合")
	}
	all := needForShots(plan)
	if !all.wantsChar("c1") || !all.wantsChar("c3") {
		t.Error("全片渲染时所有 ref2va 镜头的角色都算需要")
	}
	if all.wantsChar("c4") {
		t.Error("只出现在纯文生镜头里的角色不算需要:那张定妆照没有任何镜头会用到")
	}

	// 单镜重渲:只算这一镜
	one := needForShots(plan[1:2])
	if !one.wantsChar("c2") || !one.wantsChar("c3") {
		t.Error("第 2 镜的两个角色都应算需要")
	}
	if one.wantsChar("c1") || one.wantsChar("c4") {
		t.Error("别的镜头的角色不算需要 —— 这正是旧实现白烧 GPU 的地方")
	}
	if !one.wantsScene("s2") || one.wantsScene("s1") {
		t.Error("场景范围应与角色同步收敛")
	}

	// 单镜重渲一个纯文生镜头:范围内没有任何资产需求
	two := needForShots(plan[2:3])
	if !two.isEmpty() {
		t.Error("t2va 镜头不吃参考图,不应触发任何资产生成")
	}

	// 范围判据的 nil 语义 = 全都要
	var nilNeed *assetNeed
	if !nilNeed.wantsChar("anything") || !nilNeed.wantsScene("anything") {
		t.Error("nil 范围应表示'全都要'(资产阶段与整片渲染走这条路)")
	}
	if nilNeed.isEmpty() {
		t.Error("nil 范围不是'空需求'")
	}
}
