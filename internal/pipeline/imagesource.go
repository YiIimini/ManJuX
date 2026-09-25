package pipeline

import "github.com/YiIimini/ManJuX/internal/engine/comfyengine"

// imagesource.go 资产**来源标签**的唯一来源。
//
// 起因:asset.Source 此前在四处写死成 "krea2" / "krea2-chibi"。2026-09-25 图像模型
// 换成 Qwen-Image-2.1 之后,那几个字面量就成了假话 —— 文件明明是 Qwen-Image-2.1 出的,
// 资产库和前端却显示成「Krea-2」。用户 2026-09-24 报过同一类问题(「资产库里的 Krea-2
// 字样不该写死,应显示正确的内容」),当时收了词汇表,却没把**写入端**一起收口。
//
// 现在写入端只有这一个函数:来源 = 当前图像模型家族的 Source(见 comfyengine/imagefamily.go),
// 于是"换模型"和"标签正确"是同一件事,不会再分叉。

// imageSourceKey 当前配置下图像生成资产的来源 key。
//
// 配置取不到时退回 Krea-2 的 key 不合逻辑,所以直接返回家族解析器的结果 ——
// 它认不出文件名时也会给出当前默认家族,不会返回空串。
func (c *Coordinator) imageSourceKey() string {
	if c == nil || c.cfg == nil {
		return comfyengine.ResolveImageFamily("", "", "").Source
	}
	cs := c.cfg.Get().Comfy
	return comfyengine.ResolveImageFamily(cs.ImageFamily, cs.ImageUNet, cs.ImageCLIP).Source
}

// chibiSourceKey Q 版定妆的来源 key(与主定妆同族,后缀 -chibi 便于区分与筛选)。
func (c *Coordinator) chibiSourceKey() string {
	return c.imageSourceKey() + "-chibi"
}
