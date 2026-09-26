package pipeline

import (
	"github.com/YiIimini/ManJuX/internal/engine/comfyengine"
	"github.com/YiIimini/ManJuX/internal/project"
)

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

// imageSourceLabel 当前图像模型家族的用户可见名,给**事件/日志**用。
//
// 起因(2026-09-26 真机实测):资产阶段的事件还写着「Krea-2 生成:角色「沈砚」…」,
// 而实际跑的已经是 Qwen-Image-2.1 —— 用户 2026-09-24 报过的同一句话(「Krea-2 字样
// 写死了,应该显示正确的内容」)在提示词与资产来源上收了口,却漏在**运行日志**上。
// 日志是用户判断"它到底在用什么出图"的唯一现场,这里写错等于现场造假。
func (c *Coordinator) imageSourceLabel() string {
	return project.AssetSourceLabel(c.imageSourceKey())
}

// ImageSourceLabel 对外暴露(供 server 侧的错误文案复用同一次解析)。
func (c *Coordinator) ImageSourceLabel() string { return c.imageSourceLabel() }
