package server

import (
	"testing"

	"github.com/YiIimini/ManJuX/internal/library"
)

// 资产库列表接口必须**覆盖全部分类**（不只是道具那一个）。
//
// 来历（2026-09-28）：给库新增 music 分类时，后端 List() 是按 library.Kinds 遍历的、
// 接口照常返回 music 分组 —— 但**前端**写死了三个分类，于是用户根本看不到自己的配乐。
// 同类缺口当天发现两次（另一次是配乐参数界面上填不了）。
//
// 这条守卫钉住"接口侧永远齐"；前端那一侧改成**按接口返回的键生成标签页**（结构上不再需要同步），
// 两边合起来才是完整解法：一边保证数据齐，一边保证界面不写死清单。
func TestLibraryListCoversAllKinds(t *testing.T) {
	s := newTestServer(t)
	code, out := doReq(t, s, "GET", "/api/library", nil)
	if code != 200 {
		t.Fatalf("列表应 200,实际 %d", code)
	}
	for _, kind := range library.Kinds {
		v, ok := out[kind]
		if !ok {
			t.Errorf("列表缺少分类 %q（library.Kinds 里有，接口没给 ⇒ 前端那类会整个消失）", kind)
			continue
		}
		if _, isArr := v.([]any); !isArr {
			t.Errorf("分类 %q 必须是数组（前端按数组渲染），实际 %T", kind, v)
		}
	}
}

// TestLibraryKindLabelsExist 每个分类都要有中文名（缺了界面会显示成键名，用户看不懂）。
func TestLibraryKindLabelsExist(t *testing.T) {
	for _, kind := range library.Kinds {
		if label := library.KindLabel(kind); label == "" || label == "资产" {
			t.Errorf("分类 %q 没有中文名（KindLabel 返回 %q）", kind, label)
		}
	}
}
