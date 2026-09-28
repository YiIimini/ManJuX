package pipeline

import (
	"fmt"
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

// boardImagePath 由角色定妆照的相对路径推导**三视图细节板**；文件不存在返回空串。
//
// 板子从 2026-09-27 起顶替转台图当**角度参考**（见 RefPlan 里的说明），
// 所以这条推导与 turnaroundPath 同一形态：同目录、主图主名 + `_board`。
func boardImagePath(dir, rel string) string {
	p := BoardOutPath(filepath.Join(dir, rel))
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
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
	// 画外演出（技能侧 2026-09-27 新口径，用户定调）：**内心活动由 Q 版形象演出、系统由
	// 机器人＋全息面板演出**。这不是"顺手多带一张图"，而是这两类镜**必须带对图** ——
	// 带错的表现是"该出 Q 版却出了本人"或"系统镜没有面板、模型自己编一块屏"。
	voKind := strings.TrimSpace(shot.VoKind)
	chibiOf := map[string]string{} // 原角色 id → Q 版资产 id（技能侧约定 chibi_<原角色id>）
	for _, id := range shot.ChibiIDs {
		if base := strings.TrimPrefix(strings.TrimSpace(id), "chibi_"); base != "" && base != id {
			chibiOf[base] = id
		}
	}
	for _, cid := range shot.Characters {
		a, ok := assets["char:"+cid]
		if !ok || a.File == "" {
			missing = append(missing, cid)
			continue
		}
		// 内心镜：**本人不演这段**（口径原话）—— 有 Q 版卡就用 Q 版顶替本镜的身份参考，
		// 不再附本人那张正面照（两张一起附会请模型把两个人**都**画进画面）。
		// Q 版卡还没出图时不顶替（宁可按老样子给本人照，也不让这一镜没有身份参考）。
		if voKind == "inner" {
			if chibiID, has := chibiOf[cid]; has {
				if ca, ok2 := assets["char:"+chibiID]; ok2 && ca.File != "" {
					add("character", "character_chibi", chibiID, ca.Name, filepath.Join(dir, ca.File))
					continue
				}
			}
		}
		master := filepath.Join(dir, a.File)
		turn := turnaroundPath(dir, a.File)
		// 角度参考有**两个来源**（2026-09-27 用户定调：「有了三视图，就不需要转台图、
		// 背面、侧面、四分之三了」）：
		//   ① 显式开了多视角的项目 → 转台图（老路，留着）；
		//   ② 否则 → **三视图细节板**（它现在顶替转台图当角度参考）。
		// 顶替的只是"那张图"，角色名（character_angles）与参考图顺序**都没变**，
		// 所以 RefManifest 与 director 的 REFERENCE ORDER 契约不用动。
		// 板子比转台图更该当角度参考：同一张 2K 板上有正/侧/背三个全身 + 6 个头部细节，
		// 而转台图是 4 格横向拼图（每格只剩 1/4 像素，脸被摊薄 —— 见下面 compact 的教训）。
		if turn == "" && withAngles {
			turn = boardImagePath(dir, a.File)
		}
		if turn != "" && !withAngles {
			turn = "" // 近景/特写:角度参考只用得上 1/4 像素的脸,不附
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
	// 系统镜：机器人（Q 版人物）＋ 全息面板（道具）同框。
	//
	// 这是**唯一**把道具图塞进参考图集合的地方，与官方那句 "Props never get a picture; describe props
	// in text only" 是一次**明确例外**：面板不是"布景里的一件东西"，而是这一镜的**演出者**（系统
	// 的门面），不给她一张图，模型每镜都会自己编一块形状与发光方式都不同的屏。口径来自用户
	// 2026-09-27 的定调，记在这里免得下次有人按"官方规则"把它删掉。
	if voKind == "system" {
		bot := strings.TrimSpace(shot.SystemAssets["bot"])
		if bot != "" {
			if a, ok := assets["char:"+bot]; ok && a.File != "" {
				add("character", "system_bot", bot, a.Name, filepath.Join(dir, a.File))
			} else {
				missing = append(missing, bot)
			}
		}
		panel := strings.TrimSpace(shot.SystemAssets["panel"])
		if panel != "" {
			if a, ok := assets["prop:"+panel]; ok && a.File != "" {
				add("prop", "system_panel", panel, a.Name, filepath.Join(dir, a.File))
			} else {
				missing = append(missing, panel)
			}
		}
	}
	// H3 公开契约的参考图上限（1 首帧 + 1 尾帧 + **9 参考图**）：超了要在这里裁掉并如实说 ——
	// 不同后端对超额的处理不一样（静默丢弃 ⇒ 编号整体前移、模型对错号；或直接报错），
	// 把风险留给后端等于把"人物乱配"交给运气。
	if keptRefs, keptRoles, note := capRefsToLimit(refs, roles); note != "" {
		refs, roles = keptRefs, keptRoles
		missing = append(missing, note)
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

// MaxH3RefImages H3 公开输入契约的**参考图上限**（1 首帧 + 1 尾帧 + **9 参考图** + 3 视频 + 3 音频）。
//
// 出处：`drama-skills`（MIT）的 `skills/short-drama-video-prompts/references/minimax-h3.md` ——
// 公开文档里唯一把 H3 输入契约写死的一份；平台只走参考图（不用首尾帧），所以能吃满 9 张。
//
// 为什么要设上限：一镜里角色多（每人定妆照 + 拼合转身图）+ 场景板 + 道具备注时，参考图会**超**。
// 超了以后不同后端的行为不一样：有的静默丢掉后面的图（**编号整体前移 ⇒ 模型对错号**，
// 正是 refs.go 注释里那句"少一张图会让后续 <Picture> 编号整体前移"的同族事故），有的直接报错。
// 所以在**装配处**就裁掉超额部分并**如实播报**，而不是把风险留给后端。
const MaxH3RefImages = 9

// capRefsToLimit 把参考图裁到 H3 上限，返回(裁后的图, 裁后的角色, 被裁掉的描述)。
// 截断从**尾部**来：尾部是场景板与拼合图，角色定妆照在最前面（身份优先）。
func capRefsToLimit(refs []engine.RefImage, roles []RefRole) ([]engine.RefImage, []RefRole, string) {
	if len(refs) <= MaxH3RefImages {
		return refs, roles, ""
	}
	dropped := make([]string, 0, len(refs)-MaxH3RefImages)
	for _, r := range roles[MaxH3RefImages:] {
		if strings.TrimSpace(r.Name) != "" {
			dropped = append(dropped, r.Name)
		} else {
			dropped = append(dropped, r.Kind)
		}
	}
	kept := refs[:MaxH3RefImages]
	keptRoles := roles[:MaxH3RefImages]
	note := fmt.Sprintf("⚠ 这一镜的参考图有 %d 张，超过 H3 公开契约的上限 %d 张 —— 已按**身份优先**裁掉尾部 %d 张（%s）。"+
		"裁掉的是场景板/拼合图这类非身份参考；若被裁的是角色定妆照，请拆镜或减少同框角色",
		len(refs), MaxH3RefImages, len(dropped), strings.Join(dropped, "、"))
	return kept, keptRoles, note
}
