package pipeline

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/engine"
	"github.com/YiIimini/ManJuX/internal/project"
)

// turnaroundPath 由角色定妆照的相对路径推导同目录的多视角转台图;文件不存在则
// 返回空串 —— 这样没有转台图的存量项目参考图组成完全不变,渲染指纹也不会变。
func turnaroundPath(dir, rel string) string {
	if rel == "" {
		return ""
	}
	full := filepath.Join(dir, rel)
	ext := filepath.Ext(full)
	cand := strings.TrimSuffix(full, ext) + "_turnaround" + ext
	if _, err := os.Stat(cand); err == nil {
		return cand
	}
	return ""
}

// AssembleShotRefs 按 H3 参考图契约组装单镜的参考图。
//
// 顺序本身就是契约的一部分:先 shot.Characters 中每个已定妆的角色(严格按数组
// 顺序),最后追加场景图。director 的 sysH3Format 告诉 LLM 的就是这个顺序 ——
// 注意转台图让「下标 = 角色序号」不再成立:有转台图的角色会占 2 张图(定妆照 +
// 转台图),所以场景下标不再是 N+1,可能更靠后。因此 sysH3Format 已改为要求
// LLM「按画面内容而非下标对应 Subject」,不再声明「N 个角色 → 场景是 N+1」。
// render 阶段与 2K 重生成共用本函数,装配规则变更只需改这一处(旧实现有两份
// 逐字副本,改一处漏一处会让 2K 定稿与主渲染的参考图不一致)。
//
// 返回的 missing 是缺失定妆照的角色/场景 ID。少一张图会让后续 <Picture> 编号
// 整体前移,而 LLM 的 subject_definitions 是按 characters 顺序写的,画面内容
// 与描述仍会对不上(例如首角色缺图时,LLM 以为第一张是角色甲,实际却是角色乙)。
// 调用方必须告警,而不是静默降级。
func AssembleShotRefs(dir string, shot project.Shot, assets map[string]project.Asset) (refs []engine.RefImage, missing []string) {
	for _, cid := range shot.Characters {
		a, ok := assets["char:"+cid]
		if !ok || a.File == "" {
			missing = append(missing, cid)
			continue
		}
		refs = append(refs, engine.RefImage{Kind: "character", Path: filepath.Join(dir, a.File)})
		// 多视角转台图:存在则作为该角色的**第二张**参考 —— 定妆照说明"正面长什么样",
		// 转台图补足"从别的角度长什么样",显著降低转身/走位/多人同框时的身份漂移。
		// 拼成单张是为了只占 1 个参考槽(多图并列容易让模型混淆)。
		if turn := turnaroundPath(dir, a.File); turn != "" {
			refs = append(refs, engine.RefImage{Kind: "character", Path: turn})
		}
	}
	if shot.Scene != "" {
		if a, ok := assets["scene:"+shot.Scene]; ok && a.File != "" {
			refs = append(refs, engine.RefImage{Kind: "scene", Path: filepath.Join(dir, a.File)})
		} else {
			missing = append(missing, shot.Scene)
		}
	}
	return refs, missing
}
