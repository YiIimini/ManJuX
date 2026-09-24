package project

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/sourceaudit"
)

// 阶段参数字段"必须有读取点"闸门(审计 P1-1,与 config 侧同一判据)。
//
// 起因:`assemble.peak` 有界面、能保存、Sanitize 也在校验,而真正决定成片峰值的
// loudnorm TP 写死 -1.5 —— 用户调它没有任何效果。设置页那几个也一样
// (render.fade_sec / split.shot_limit / auto_open_ui)。
//
// 边界与 config 侧相同:扫描只证明"有人读过"。见 internal/sourceaudit 的说明。
var paramsReadAllowlist = map[string]string{
	// 键 = "结构体.字段",值 = 为什么它没有读取点还能留
}

func TestStageParamsFieldsAreRead(t *testing.T) {
	root := filepath.Join("..", "..")
	files := sourceaudit.Sources(root)
	if len(files) == 0 {
		t.Fatal("没扫到 Go 源码 —— 路径失效,闸门形同不存在")
	}

	types := []reflect.Type{
		reflect.TypeOf(ScriptParams{}),
		reflect.TypeOf(AssetsParams{}),
		reflect.TypeOf(RenderParams{}),
		reflect.TypeOf(QCParams{}),
		reflect.TypeOf(AssembleParams{}),
	}

	var missing []string
	for _, tp := range types {
		for i := 0; i < tp.NumField(); i++ {
			f := tp.Field(i)
			if !f.IsExported() {
				continue
			}
			key := tp.Name() + "." + f.Name
			if _, ok := paramsReadAllowlist[key]; ok {
				continue
			}
			if !sourceaudit.FieldIsRead(files, f.Name) {
				missing = append(missing, key)
			}
		}
	}
	if len(missing) > 0 {
		t.Errorf("这些阶段参数没有任何读取点(界面上能调、实际不生效):\n  %s\n"+
			"处置:接上读取点,或者删掉它。确属展示/透传用途时在 paramsReadAllowlist 里写理由。",
			strings.Join(missing, "\n  "))
	}
}
