package project

import "strings"

// 资产来源(source)的**唯一词汇表**。
//
// 起因(2026-09-24 用户:「资产库里的 Krea-2 字样不该写死,应该显示正确的内容」):
// `source` 是后端写入的自由字符串,而前端在**七处**各自用三元表达式猜它的中文名,
// 且只认三种值(krea2 / library / 其余当"上传")。真实来源有六种 ——
// Q 版定妆(krea2-chibi)、视频截帧替换(video-frame)、抽卡(gacha)全都被显示成"上传",
// 也就是**显示的内容与事实不符**,而且以后后端新增一种来源,前端一律继续显示"上传"。
//
// 收口方式与阶段清单/实体类型一致:词汇表在后端,前端只渲染 ——
// 后端通过 /api/meta 下发 asset_sources,前端一个 sourceLabel() 查表。
// 闸门(见 source_label_test.go)会扫后端所有 source 字面量,漏登记就变红。

// assetSourceNames 来源 key → 用户可见名称。
//
// 名称刻意保留"Krea-2"这类实现名:用户就是在设置页按这些名字配模型的,
// 换个抽象说法反而对不上。要改的是"哪些来源被认出来",不是把这些名字抹掉。
var assetSourceNames = []struct {
	Key  string
	Name string
}{
	// 图像模型家族(写入端见 pipeline.imagesource,取值见 comfyengine/imagefamily.go)。
	// 名称刻意保留实现名:用户就是在设置页按这些名字配模型的,换个抽象说法反而对不上。
	{"qwen-image-2.1", "Qwen-Image-2.1"}, // 2026-09-25 起的新默认
	{"qwen-image-2.1-chibi", "Qwen-Image-2.1 · Q版"},
	{"z-image", "Z-Image Turbo"}, // 本地兜底家族
	{"z-image-chibi", "Z-Image Turbo · Q版"},
	{"qwen-image", "Qwen-Image"},
	{"qwen-image-chibi", "Qwen-Image · Q版"},
	// 存量资产:2026-09-25 之前由 Krea-2 生成的文件仍然是 Krea-2 出的,标签保留。
	{"krea2", "Krea-2"},
	{"krea2-chibi", "Krea-2 · Q版"},
	{"library", "库复用"},
	{"upload", "上传"},
	{"upload-whitebg", "上传 · 白底化"},
	{"video-frame", "视频截帧"},
	{"gacha", "抽卡"},
}

// AssetSources 全部来源(按展示顺序),供 /api/meta 下发。
func AssetSources() []struct {
	Key  string
	Name string
} {
	out := make([]struct {
		Key  string
		Name string
	}, len(assetSourceNames))
	copy(out, assetSourceNames)
	return out
}

// AssetSourceLabel 来源的中文名。**认不出时原样返回 key** ——
// 这比统一兜底成"上传"诚实:用户至少能看出"这是另一种来源",而不是被误导成
// "我明明没上传过,怎么说是上传"。
func AssetSourceLabel(src string) string {
	s := strings.TrimSpace(src)
	if s == "" {
		return "未知"
	}
	for _, e := range assetSourceNames {
		if e.Key == s {
			return e.Name
		}
	}
	return s
}
