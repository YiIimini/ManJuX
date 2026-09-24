package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/sourceaudit"
)

// 设置字段"必须有读取点"闸门(审计 P1-1)。
//
// 起因:四个设置项(render.fade_sec / split.shot_limit / assemble.peak / auto_open_ui)
// 都有界面、都能保存,而 Go 侧**一个读取点都没有** —— 用户调了它,系统毫无反应,
// 而且不报错、不留日志。这类缺陷靠人回看代码查不出来(字段名到处都是),只能靠扫描。
//
// 判据:每个设置字段必须在非测试 Go 源码里以 `.<字段名>` 出现过。用反射取字段名,
// 不做文本解析 —— 反射的清单不会漂。
//
// **已知边界**(别把它当成万能的):扫描只能证明"有人读过",证明不了"读它的地方
// 就是对的地方"。`assemble.peak` 只在校验里被读、却从不影响 loudnorm 的 TP,
// 这一条扫描抓不到 —— 它的兜底是 compose 侧那个针对性测试(见 compose_test.go)。
var settingsReadAllowlist = map[string]string{
	// 键 = "结构体.字段",值 = 为什么它没有读取点还能留
}

func TestSettingsFieldsAreRead(t *testing.T) {
	root := filepath.Join("..", "..")
	files := sourceaudit.Sources(root)
	if len(files) == 0 {
		t.Fatal("没扫到 Go 源码 —— 路径失效,闸门形同不存在")
	}

	types := []reflect.Type{
		reflect.TypeOf(Settings{}),
		reflect.TypeOf(LLMSettings{}),
		reflect.TypeOf(ComfySettings{}),
		reflect.TypeOf(MiniMaxSettings{}),
		reflect.TypeOf(FFSettings{}),
		reflect.TypeOf(VLMSettings{}),
		reflect.TypeOf(RenderSettings{}),
		reflect.TypeOf(SplitSettings{}),
	}

	var missing []string
	for _, tp := range types {
		for i := 0; i < tp.NumField(); i++ {
			f := tp.Field(i)
			if !f.IsExported() {
				continue
			}
			key := tp.Name() + "." + f.Name
			if _, ok := settingsReadAllowlist[key]; ok {
				continue
			}
			if !sourceaudit.FieldIsRead(files, f.Name) {
				missing = append(missing, key)
			}
		}
	}
	if len(missing) > 0 {
		t.Errorf("这些设置字段没有任何读取点(用户能改、但改了没用):\n  %s\n"+
			"处置:接上读取点,或者删掉它(留一个假开关比删掉更糟)。"+
			"确属展示/透传用途时,在 settingsReadAllowlist 里写一行理由。",
			strings.Join(missing, "\n  "))
	}
}
