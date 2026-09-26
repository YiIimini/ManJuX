package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/project"
)

// 资产来源标签的写入端闸门(2026-09-25 换图像模型时暴露)。
//
// 起因:asset.Source 此前在四处写死 "krea2" / "krea2-chibi"。图像模型换成
// Qwen-Image-2.1 后,这几个字面量立刻变成假话 —— 文件是 Qwen 出的,资产库显示
// 「Krea-2」。用户 2026-09-24 就报过同一类问题,当时收了词汇表(project/source_label.go)
// 却没把**写入端**一起收口,所以这次两头都钉住。

// TestFamilySourcesHaveLabels 每个图像家族的来源 key 都必须在词汇表里有中文名 ——
// 否则资产库会把 key 原样显示出来(qwen-image-2.1-chibi 这种)。
func TestFamilySourcesHaveLabels(t *testing.T) {
	fams := comfyengine.KnownImageFamilies()
	// 另加"旧 Qwen-Image 权重"这条兜底家族(由键名推断产生,不在 Known 列表里)
	fams = append(fams, comfyengine.ResolveImageFamily("", "qwen_image_2512_bf16.safetensors", "qwen_2.5_vl_7b.safetensors"))
	for _, fam := range fams {
		if fam.Source == "" {
			t.Errorf("家族 %s 没有 Source key —— 资产来源会写空", fam.Label)
			continue
		}
		for _, key := range []string{fam.Source, fam.Source + "-chibi"} {
			if got := project.AssetSourceLabel(key); got == key {
				t.Errorf("来源 key %q(家族 %s)未登记中文名,资产库会原样显示 key;请加进 project/source_label.go",
					key, fam.Label)
			}
		}
	}
}

// TestImageSourceKeyFollowsConfig 来源 key 必须随配置里的家族走,而不是写死。
func TestImageSourceKeyFollowsConfig(t *testing.T) {
	c := &Coordinator{}
	if got := c.imageSourceKey(); got == "" {
		t.Fatal("配置不可用时也应给出默认家族的来源 key,而不是空串")
	} else if got != comfyengine.ResolveImageFamily("", "", "").Source {
		t.Errorf("无配置时应回落到默认家族来源,实际 %q", got)
	}
	if c.chibiSourceKey() != c.imageSourceKey()+"-chibi" {
		t.Error("Q 版来源 key 应是主来源加 -chibi 后缀")
	}
}

// TestNoHardcodedImageSource 结构性闸门:图像来源的写入点不得再出现写死的 "krea2"。
//
// 为什么扫源码而不是只测行为:漏掉的形态是"某一条路径又写了字面量",行为测试只覆盖
// 它自己走的那条路径。与 refmanifest_test.go 里那条成对闸门同一思路。
func TestNoHardcodedImageSource(t *testing.T) {
	for _, f := range []string{"stages_assets.go", "coordinator.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读不到 %s: %v", f, err)
		}
		src := string(b)
		for _, bad := range []string{`Source: "krea2"`, `Source: "krea2-chibi"`, `, "krea2")`} {
			if strings.Contains(src, bad) {
				t.Errorf("%s 里仍有写死的图像来源 %s —— 必须走 c.imageSourceKey() / c.chibiSourceKey()", f, bad)
			}
		}
		if !strings.Contains(src, "imageSourceKey()") {
			t.Errorf("%s 看不到 imageSourceKey() 调用 —— 若它确实会写资产来源,就是漏了", f)
		}
	}
}

// TestNoHardcodedModelNameInUserFacingStrings 结构性闸门:**用户可见的字符串**里
// 不许再写死模型名。
//
// 起因(2026-09-26 真机实测):换到 Qwen-Image-2.1 之后,资产阶段的事件仍然播报
// 「Krea-2 生成:角色「沈砚」(832×1248)…」。用户 2026-09-24 报过同一句话
// (「资产库里的 Krea-2 字样写死了」),提示词与资产来源都收了口,**运行日志没收** ——
// 而日志正是用户判断"它到底在用什么出图"的唯一现场,写错等于现场造假。
//
// 判据取"行里的**代码部分**含 Krea-2":先把 `//` 之后的注释切掉,所以
// 「Krea-2 是蒸馏模型(CFG 1.0)…」这类历史注释不受影响;而
// `emit(..., "Krea-2 生成:…")` / `fmt.Errorf("…(Krea-2)")` 一律现行。
// 家族名字典本身(project/source_label.go 的 {"krea2","Krea-2"}、imagefamily.go 的
// Label 字段)是**合法命名**,不在扫描范围。
func TestNoHardcodedModelNameInUserFacingStrings(t *testing.T) {
	const bad = "Krea-2"
	dirs := []string{".", "../server"}
	scanned := 0
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			t.Fatalf("读目录 %s 失败: %v", d, err)
		}
		for _, e := range ents {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			// 词典/家族表:这些文件的职责就是给模型起名字,允许出现。
			if name == "imagefamily.go" || name == "source_label.go" {
				continue
			}
			b, err := os.ReadFile(filepath.Join(d, name))
			if err != nil {
				t.Fatalf("读 %s 失败: %v", name, err)
			}
			scanned++
			for i, line := range strings.Split(string(b), "\n") {
				if idx := strings.Index(line, "//"); idx >= 0 {
					line = line[:idx] // 注释不算用户可见内容
				}
				if strings.Contains(line, bad) {
					t.Errorf("%s:%d 用户可见字符串里写死了模型名 %q:%s\n"+
						"  模型名必须从当前家族取(项目侧 c.imageSourceLabel() / 服务侧 s.pipe.ImageSourceLabel()),否则换模型后日志说的是假话",
						filepath.Join(d, name), i+1, bad, strings.TrimSpace(line))
				}
			}
		}
	}
	if scanned < 20 {
		t.Fatalf("只扫到 %d 个文件 —— 闸门本身可能扫错了目录", scanned)
	}
}
