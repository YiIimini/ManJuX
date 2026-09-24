package engine

// Caps 引擎能力表。
//
// 为什么要有它(审计 P1-2):管线里靠 `engName == "comfy"` 这类**字符串判断**决定
// "要不要做本地专属动作"(镜间接缝、清队列、释放显存、本地预检、本地资产补齐)。
// 字符串比较的毛病不在写错,而在**新增引擎时没人会想起来改这 6 处** ——
// 结果要么新引擎被当成 ComfyUI 去做它做不到的事(报原始错误),
// 要么被静默跳过(用户设了参数却不生效、还没有提示)。
//
// 能力表的判据是"这台引擎实际能做什么",不是"它叫什么"。
type Caps struct {
	Local bool // 本地引擎:有 ComfyUI 进程 / 队列 / 显存可管
	Seam  bool // 镜间接缝(MotionContext 钉上一镜尾部)
	VRAM  bool // 可释放显存(/free)
	Queue bool // 有可清空的队列
	// ImageGen 能生成静态图(定妆照 / 多视角 / 抽卡)。云端引擎不提供。
	ImageGen bool
	// MaskFix 局部重绘、AudioDrive 音频驱动这类图增强(依赖本地节点链)。
	MaskFix bool
}

// CapsOf 按引擎名给出能力表。未知引擎返回**零值**:
// 零值 = "什么本地专属能力都没有",这是安全的默认(跳过而不是硬做),
// 调用方据此给出"该引擎不支持 X"的提示。
func CapsOf(name string) Caps {
	switch name {
	case "comfy":
		return Caps{Local: true, Seam: true, VRAM: true, Queue: true, ImageGen: true, MaskFix: true}
	case "api":
		return Caps{} // 云端:纯文生/图生视频,没有本地概念
	}
	return Caps{}
}
