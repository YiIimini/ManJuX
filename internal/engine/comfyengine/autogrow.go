package comfyengine

import "fmt"

// autogrowKey 生成 ComfyUI v3 autogrow(动态槽位)输入在 API prompt 里的提交键。
//
// 依据(ComfyUI 0.37.4 源码 comfy_api/latest/_io.py):
//   - _expand_schema_for_dynamic 里 expected_id = finalize_prefix(["ref_images"], "ref_image_0")
//     == "ref_images.ref_image_0",**只有这个名字**出现在节点输入里,该槽位才会被登记为真实输入;
//   - build_nested_inputs 再按 "." 把它还原成 {ref_images: {ref_image_0: tensor}} 交给节点。
//
// 所以「聚合键.模板名」是唯一被识别的形态。写成聚合数组 ref_images=[link,…] 时,
// _expand_schema_for_dynamic 找不到任何 expanded 槽位,会走「所有 optional 都没值」那条分支
// (dynamic_paths_default_value = empty_dict),build_nested_inputs 于是把 ref_images 填成 {},
// 节点拿到空 dict —— 参考图一张都不进条件,而且**不报错**(静默失效)。
//
// 2026-09-27 实测(pruned int8 底模 + ComfyUI 0.37.4,两张截然不同的参考图):
//   - 聚合数组写法:ref_items=0 / ref_blocks=0,conditioning 里没有 minimax_refs,
//     22 帧对照的帧指纹完全相同(逐帧一致);
//   - 点号键写法:ref_items=1 / ref_blocks=1,conditioning 里出现 minimax_refs,
//     cross_attn 随参考图改变,帧指纹完全不同,画面里的人与服装确实跟着参考图变。
//
// 新增 autogrow 输入(如 ref_videos / ref_video_audios)时一律走这里,不要手拼键名。
func autogrowKey(aggregate, template string, i int) string {
	return fmt.Sprintf("%s.%s%d", aggregate, template, i)
}
