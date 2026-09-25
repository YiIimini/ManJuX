package pipeline

import (
	"fmt"
	"strings"
)

// audio_ref.go 音频驱动(Audio Drive)提示词的**官方标记层**。
//
// 依据(MiniMax 官方全参考模式指南 §2.4 / §3 / §4.2 + ComfyUI 官方提示词指南的社区实测):
//
//	https://huggingface.co/MiniMaxAI/MiniMax-H3/blob/main/docs/VIDEO_PROMPT_WRITING_GUIDE_ref_en.md
//	https://docs.comfy.org/zh/tutorials/video/minimax/minimax-h3-prompt-guide
//
// 三件事必须写进提示词,否则 H3 只会把喂进去的音频当**音色参考**而不是"要同步的音轨":
//
//	① subject_definitions 里的 `<Audio N>` 定义(它是什么、服务哪个说话人);
//	② summary 的任务类型前缀(`audio reuse` = 复用音轨 / `audio reference` = 只参考音色);
//	③ retention_analysis 里的关系标记(`fully_copy` / `partially_copy` / `reference`)。
//
// ComfyUI 官方文档原文的社区实测:"标记不正确时,H3 只把音频当音色参考,**口型会与歌词
// 脱节**"。用户 2026-09-25 报的「人物配音错误异常」正落在这一条上 —— 我们的音频驱动
// 路径此前**一个标记都没写**(只把音频塞进 ref_audios 节点),H3 于是按"参考音色"处理,
// 画面口型与配音自然对不上。
//
// 标记语义(官方固定英文值,不可改写):
//
//	fully_copy      源音频 1:1 作为目标视频的完整最终音轨
//	partially_copy  只复制部分时间轴/部分音层,或复制后又增删替换了其它声音
//	reference       不复制信号,只参考音色/节奏/音乐风格/台词内容/声音质感
//	weak_reference  只保留类别或氛围上的大致相似
func AudioDriveMarkers(label string) string {
	if strings.TrimSpace(label) == "" {
		label = "<Audio 1>"
	}
	var sb strings.Builder
	sb.WriteString("【音频参考标记(官方全参考模式要求,不可省略)】\n")
	sb.WriteString("summary 任务类型前缀:[audio reuse]\n")
	sb.WriteString(fmt.Sprintf(
		"subject_definitions:%s is the finalized dubbing track of this shot and serves as the voice-timbre and "+
			"lip-sync reference for the speaking character.\n", label))
	sb.WriteString(fmt.Sprintf(
		"retention_analysis:%s: fully_copy - %s is reused 1:1 as the target video's complete final audio track; "+
			"the characters' mouth shapes must follow it exactly, syllable by syllable.\n", label, label))
	sb.WriteString("(中文对照:音频按 fully_copy 1:1 复用为最终音轨,口型必须逐音节跟随它;" +
		"若写成 reference,模型只会拿它当音色参考,口型会与配音脱节。)\n")
	return sb.String()
}

// EnsureAudioDriveMarkers 给音频驱动的提示词补上标记段(幂等:已带过就不再叠)。
//
// 位置:接在原提示词之后 —— 这些标记是**结构声明**(summary / subject_definitions /
// retention_analysis),与画面描述段并列,不参与画面语义。
func EnsureAudioDriveMarkers(prompt string) string {
	if strings.Contains(prompt, audioMarkerKey) {
		return prompt
	}
	return strings.TrimRight(prompt, "\n") + "\n\n" + AudioDriveMarkers("<Audio 1>")
}

// audioMarkerKey 幂等判据(改文案时必须保留这个串)。
const audioMarkerKey = "【音频参考标记(官方全参考模式要求,不可省略)】"
