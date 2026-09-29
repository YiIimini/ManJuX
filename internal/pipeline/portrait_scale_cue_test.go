package pipeline

import (
	"strings"
	"testing"
)

// 定妆照取景必须带**尺度线索**（2026-09-29 用户：「碑生（丙十七）、钟离、碑生·Q版、崔无咎
// 这几个定妆照的比例就不对」）。
//
// 实测：只写"裁到胸口"时，模型把**头放大到占满竖幅** —— 霍延（卡面写八头身）那张头占了
// 画面一半以上，碑生（七头半身）那张头约占 1/3，高瘦角色全画成"大头脑像"。
// 判据：人形/人形机械的定妆照取景句里必须有"头占画面高度约四分之一、肩宽于头"的线索，
// 并且**不许**把否定词写进去（Krea-2 会把名词当指令画出来，见 sheet_framing 的既有纪律）。
func TestPortraitFramingCarriesScaleCue(t *testing.T) {
	framed := PortraitPrompt(PortraitSubject{
		Kind: KindHuman, Name: "碑生", Description: "瘦高，七头半身", FacesOn: true,
		FrameW: 768, FrameH: 1344,
	}, nil)
	if !strings.Contains(framed, "ONE QUARTER") {
		t.Error("定妆照取景缺「头占画面高度约四分之一」这条尺度线索 —— 模型会把头放大成大头照")
	}
	if !strings.Contains(strings.ToUpper(framed), "WIDER THAN THE HEAD") {
		t.Error("定妆照取景缺「肩宽于头」这条线索 —— 高瘦角色会被画成窄肩大头")
	}
	if strings.Contains(framed, "big-headed child") {
		t.Error("尺度线索里带了否定词（Krea-2 会把名词当指令画出来）")
	}
}
