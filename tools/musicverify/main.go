package main

// Command musicverify 手动验证配乐链路：真跑一次生成 → 入资产库 → 按同需求（换个标点写法）查回来。
//
// 为什么留着：模型或 ComfyUI 升级后，"还能不能出曲子、出完能不能入库复用"这两件事值得一条命令验完
// （自动化测试里那条 TestMusicGenerationLive 是同一件事的测试版，但它输出到临时目录、跑完就删）。
// 它不是产线代码 —— 产线那一版在 pipeline.generateMusic。
import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/YiIimini/ManJuX/internal/config"
	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/library"
)

func main() {
	cfg := config.Default().Comfy
	eng := comfyengine.New(func() config.ComfySettings { return cfg })
	outDir := filepath.Join("data", "assets_library", "music_tmp")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		panic(err)
	}
	brief := "低沉竹笛与弦乐，压抑，慢速，碑下独白段落"
	tags := brief + ", instrumental only, no vocals, background score"
	path, err := eng.GenerateMusic(context.Background(), engine.MusicJob{
		Tags: tags, Seconds: 60, BPM: 66,
		OutputDir: outDir, OutputName: "bgm_verify",
	})
	if err != nil {
		fmt.Println("生成失败:", err)
		os.Exit(1)
	}
	st, _ := os.Stat(path)
	fmt.Printf("生成: %s（%.2f MB）\n", path, float64(st.Size())/1024/1024)

	lib, err := library.New(filepath.Join("data", "assets_library"))
	if err != nil {
		fmt.Println("开库失败:", err)
		os.Exit(1)
	}
	name := library.MusicEntryName(brief)
	e, err := lib.Put("music", name, brief, tags, path, "verify-project", "ace-step")
	if err != nil {
		fmt.Println("入库失败:", err)
		os.Exit(1)
	}
	fmt.Printf("入库: 条目「%s」主文件 %s\n", e.Name, filepath.Base(e.Image))
	// 立刻按同一需求（换一种标点写法）查回来 —— 这就是用户要的"复用"
	if e2, p2, ok := lib.Match("music", library.MusicEntryName("低沉竹笛与弦乐, 压抑 ,慢速,碑下独白段落")); ok {
		fmt.Printf("复用命中: 「%s」%s（来源 %s）\n", e2.Name, filepath.Base(p2), e2.Source)
	} else {
		fmt.Println("复用未命中 ✗（归一或匹配有问题）")
	}
	os.RemoveAll(outDir) // 临时落点清掉，库里那份才是资产
}
