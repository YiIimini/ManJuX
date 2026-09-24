package engine

import "testing"

// 引擎能力表(审计 P1-2)。
//
// 收口的意义在于"新增引擎时不会漏":旧实现靠 6 处 `engName == "comfy"` 字符串比较,
// 新引擎要么被当成 ComfyUI 去做它做不到的事,要么被静默跳过。
func TestCapsOf(t *testing.T) {
	local := CapsOf("comfy")
	if !local.Local || !local.Seam || !local.VRAM || !local.Queue || !local.ImageGen || !local.MaskFix {
		t.Errorf("本地 ComfyUI 应具备全部本地能力,实际 %+v", local)
	}
	cloud := CapsOf("api")
	if cloud.Local || cloud.Seam || cloud.VRAM || cloud.Queue || cloud.ImageGen || cloud.MaskFix {
		t.Errorf("云端引擎不该声称任何本地能力(会导致它硬做本地动作),实际 %+v", cloud)
	}
	// 未知引擎 = 零值 = 什么本地能力都没有(安全默认:跳过并提示,而不是硬做)
	if got := CapsOf("未来某个引擎"); got != (Caps{}) {
		t.Errorf("未知引擎应返回零值能力表,实际 %+v", got)
	}
}
