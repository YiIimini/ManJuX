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

// RefRole 描述一张参考图**在提示词里的身份**。Kind 取值:
//
//	character_front        角色的定妆照(正面表,身份的唯一权威)
//	character_angles       同一角色的多视角拼合图(**另附**,定妆照同时在场)
//	character_front_in_strip 去冗余模式:只喂拼合图,它的最左格就是定妆照
//	scene                  场景图(永远最后一张)
//
// 为什么要单独记:engine.RefImage.Kind 只有 character/scene 两种(引擎按它决定上传
// 与首帧语义),而**提示词**需要区分"这张脸是权威"还是"这张只是补角度" —— 两者混在
// 一起正是身份漂移的入口(见 refmanifest.go)。
type RefRole struct {
	Picture int    // 1-based <Picture N> 编号
	Kind    string // 见上
	Name    string // 角色名 / 场景名
	ID      string // char_x / scene_x
}

// wantsAnglesStrip 该景别要不要附**多视角拼合图**。
//
// 【为什么近景/特写不附(2026-09-25 实测)】
// 拼合图是 4 格横向拼图,模型按入口分辨率缩放后每格只剩约 1/4 像素(refs.go 顶部记过
// 同一条:只喂拼合图时"服装对、脸变了")。对**特写/近景**来说画面里几乎只有一张脸,
// 其它角度用不上,而这 4 个低分辨率的脸却在参与"取脸"——纯是身份信号的稀释源。
// 中景以上才真的需要其它角度(侧身、背面、器物转台),所以保留。
//
// 未知景别按**保留**处理:宁可多一张,不要把"其实需要角度"的镜头悄悄降级。
func wantsAnglesStrip(shotSize string) bool {
	s := strings.TrimSpace(shotSize)
	switch {
	case strings.Contains(s, "特写"):
		return false
	// 中近景夹在中景与近景之间,身体仍有一部分可见,其它角度还有用 —— 先按保留处理。
	case strings.Contains(s, "中近景"):
		return true
	case strings.Contains(s, "近景"):
		return false
	case strings.Contains(s, "中景"), strings.Contains(s, "全景"), strings.Contains(s, "远景"):
		return true
	}
	return true
}

// RefPlan 是 AssembleShotRefs 的完整形态:除参考图本身,还给出每张图的身份归属
// (roles),供 RefManifest 写出**渲染期权威清单**。
//
// 顺序即契约,与 AssembleShotRefs 完全一致:先 shot.Characters 每个已定妆的角色
// (严格按数组顺序),最后追加场景图。拼合图按景别决定要不要附(见 wantsAnglesStrip)。
func RefPlan(dir string, shot project.Shot, assets map[string]project.Asset, compact bool) (refs []engine.RefImage, roles []RefRole, missing []string) {
	add := func(engineKind, roleKind, id, name, path string) {
		refs = append(refs, engine.RefImage{Kind: engineKind, Path: path})
		roles = append(roles, RefRole{Picture: len(refs), Kind: roleKind, Name: name, ID: id})
	}
	withAngles := wantsAnglesStrip(shot.ShotSize)
	for _, cid := range shot.Characters {
		a, ok := assets["char:"+cid]
		if !ok || a.File == "" {
			missing = append(missing, cid)
			continue
		}
		master := filepath.Join(dir, a.File)
		turn := turnaroundPath(dir, a.File)
		if turn != "" && !withAngles {
			turn = "" // 近景/特写:拼合图只用得上 1/4 像素的脸,不附
		}
		switch {
		case turn != "" && compact:
			// 转台图含正面 → 只喂这一张(左起第一格即定妆照)
			add("character", "character_front_in_strip", cid, a.Name, turn)
		case turn != "":
			add("character", "character_front", cid, a.Name, master)
			add("character", "character_angles", cid, a.Name, turn)
		default:
			add("character", "character_front", cid, a.Name, master)
		}
	}
	if shot.Scene != "" {
		if a, ok := assets["scene:"+shot.Scene]; ok && a.File != "" {
			add("scene", "scene", shot.Scene, a.Name, filepath.Join(dir, a.File))
		} else {
			missing = append(missing, shot.Scene)
		}
	}
	return refs, roles, missing
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
//
// 调用方请改用 RefPlan:它同样给出这张表的身份归属,而**提示词必须带 RefManifest**
// —— 否则 LLM 猜错张数时,拼合图会被当成场景图(2026-09-25 实测事故,见 refmanifest.go)。
func AssembleShotRefs(dir string, shot project.Shot, assets map[string]project.Asset, compact bool) (refs []engine.RefImage, missing []string) {
	refs, _, missing = RefPlan(dir, shot, assets, compact)
	return refs, missing
}
