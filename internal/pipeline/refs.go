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
// 所以它只要求 LLM「按画面内容而非下标对应 Subject」,不声明「N 个角色 → 场景是
// N+1」:参考图可能是拼合图,下标与角色序号并不总是一一对应。
// render 阶段、2K 重生成、局部重绘、音频驱动共用本函数,装配规则变更只需改这一处
// (旧实现有两份逐字副本,改一处漏一处会让 2K 定稿与主渲染的参考图不一致)。
//
// **参考图组成(2026-09-12 实测定稿)**:默认喂**定妆照 + 转台图两张** ——
// 定妆照是锐利的"锁脸锚点",转台图补其它角度。曾试过只喂转台图(它左起第一格就是
// 定妆照、看似信息不重复),但那是错判:转台图是 4 格横向拼合图,模型按入口分辨率
// 缩放时每格只剩约 1/4 像素,脸的细节被摊薄 → 实测渲染出"服装对、脸变了"的另一个人,
// 用户当场报一致性丢失。所以 compact 只在显式开启时才生效,默认关。
//
// 返回的 missing 是缺失定妆照的角色/场景 ID。少一张图会让后续 <Picture> 编号
// 整体前移,而 LLM 的 subject_definitions 是按 characters 顺序写的,画面内容
// 与描述仍会对不上(例如首角色缺图时,LLM 以为第一张是角色甲,实际却是角色乙)。
// 调用方必须告警,而不是静默降级。
func AssembleShotRefs(dir string, shot project.Shot, assets map[string]project.Asset, compact bool) (refs []engine.RefImage, missing []string) {
	for _, cid := range shot.Characters {
		a, ok := assets["char:"+cid]
		if !ok || a.File == "" {
			missing = append(missing, cid)
			continue
		}
		master := filepath.Join(dir, a.File)
		turn := turnaroundPath(dir, a.File)
		switch {
		case turn != "" && compact:
			// 转台图含正面 → 只喂这一张(左起第一格即定妆照)
			refs = append(refs, engine.RefImage{Kind: "character", Path: turn})
		case turn != "":
			refs = append(refs, engine.RefImage{Kind: "character", Path: master})
			refs = append(refs, engine.RefImage{Kind: "character", Path: turn})
		default:
			refs = append(refs, engine.RefImage{Kind: "character", Path: master})
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
