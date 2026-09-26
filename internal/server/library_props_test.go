package server

import (
	"strings"
	"testing"
)

// 道具创建接口 POST /api/library/{kind}。
//
// 只钉**参数校验**这几条(不碰网络):成功路径要真调视觉模型补描述,那属于真机验证的活,
// 不该让单元测试去连 LLM。这里覆盖的恰恰是"少一条就静默放行"的分支:
//   - 未知分类必须 400(kind 来自 URL,放行等于让请求随便建目录);
//   - 非角色分类没有"提示词代生成"这条路,只给提示词必须 400 并说清原因
//     (代生成用的是角色定妆照取景,画道具会跑偏);
//   - 名字必填,且文案要按分类说话("道具名必填",不是"角色名必填")。
func TestLibraryCreateAssetKindGuards(t *testing.T) {
	s := newTestServer(t)

	if code, out := doReq(t, s, "POST", "/api/library/stuffs", map[string]any{"name": "x"}); code != 400 {
		t.Errorf("未知分类应 400,实际 %d(%v)", code, out)
	}
	code, out := doReq(t, s, "POST", "/api/library/props", map[string]any{"name": "无图道具", "image_prompt": "a bronze mirror"})
	if code != 400 {
		t.Errorf("道具只有提示词、没有图,应 400(本分类不支持代生成),实际 %d(%v)", code, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "上传") {
		t.Errorf("拒绝理由要指向「传一张图」,实际 %q", msg)
	}
	code, out = doReq(t, s, "POST", "/api/library/props", map[string]any{"description": "无名字"})
	if code != 400 {
		t.Errorf("道具名必填,应 400,实际 %d", code)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "道具名") {
		t.Errorf("文案要按分类说话(道具名必填),实际 %q", msg)
	}
	// 角色那条路没被改坏:同样缺名时文案仍是"角色名必填"
	code, out = doReq(t, s, "POST", "/api/library/characters", map[string]any{"description": "无名字"})
	if code != 400 {
		t.Errorf("角色名必填,应 400,实际 %d", code)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "角色名") {
		t.Errorf("角色文案不该被泛化掉,实际 %q", msg)
	}
}

// TestLibraryListHasPropsGroup 列表接口必须带 props 分组。
// 缺分组的症状是前端 lib.props 为 undefined —— 页面一打开就在渲染里崩,而接口本身 200。
func TestLibraryListHasPropsGroup(t *testing.T) {
	s := newTestServer(t)
	if code, out := doReq(t, s, "GET", "/api/library", nil); code != 200 {
		t.Fatalf("列表应 200,实际 %d", code)
	} else if _, ok := out["props"]; !ok {
		t.Errorf("列表应含 props 分组,实际键: %v", out)
	}
}
