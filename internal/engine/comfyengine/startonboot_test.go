package comfyengine

import (
	"context"
	"testing"

	"github.com/YiIimini/ManJuX/internal/config"
)

// TestStartOnBootRespectsAutoStart 开机预热必须**听设置的**:
// AutoStart 关 → 什么都不做(不代理启动,免得用户在别处管着自己的 ComfyUI);
// AutoStart 开但根目录不对 → 返回错误由调用方记日志(启动预热失败不该阻塞窗口)。
func TestStartOnBootRespectsAutoStart(t *testing.T) {
	off := config.ComfySettings{AutoStart: false, BaseURL: "http://127.0.0.1:9", RootDir: t.TempDir()}
	e := New(func() config.ComfySettings { return off })
	if err := e.StartOnBoot(context.Background()); err != nil {
		t.Errorf("AutoStart 关时不该有任何动作,实际 %v", err)
	}

	on := config.ComfySettings{AutoStart: true, BaseURL: "http://127.0.0.1:9", RootDir: t.TempDir()}
	e2 := New(func() config.ComfySettings { return on })
	err := e2.StartOnBoot(context.Background())
	if err == nil {
		t.Error("根目录里没有 standalone-env/python.exe 时应报错(供调用方记日志)")
	}
}
