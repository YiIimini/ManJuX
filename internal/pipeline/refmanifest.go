package pipeline

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// refmanifest.go 渲染期**参考图权威清单**。
//
// 事故(2026-09-25,用户报「角色渲染漂移,看看镜头6主角形象」):
// 第01集镜头6 的参考图实际是 3 张(顾青禾定妆照 + 顾青禾拼合转身图 + 棚内场景),
// 日志可证:「参考图 3 张:角色 2 + 其中拼合转身图 1 + 场景 1」。但 plan 里的
// subject_definitions 写的是「Two pictures are attached in total, one character front
// sheet and the scene」,并把 **<Picture 2> 说成是场景图** —— 于是:
//
//	① 顾青禾自己的拼合转身图被模型当作"场景",retention_analysis 里那一行
//	    `<Picture 2>: weak_reference - scene layout and mood referenced, camera free`
//	    正好落在**她自己的脸上**,模型据此获得"这张脸可以自由重画"的许可;
//	② 真正的场景图(第 3 张)成了无标签的野图;
//	③ 定妆照作为"唯一身份权威"的地位被稀释 —— 成品脸比定妆照更窄更长、眉毛更重、
//	    更年长,而文案里写了、定妆照里没画的细节(右眉尾浅疤、皮筋外的黄铜"禾"扣)
//	    反而被画了出来:模型转去照着**文字**重画,而不是照着那张脸复制。
//
// 根因不是模型不听话,是**提示词在描述它看不见的东西**:张数取决于该角色有没有
// 拼合转身图,而这一步只有渲染器知道(见 RefPlan),LLM 只能猜 —— 猜错就把编号
// 整体错位。refs.go 早已写明「少一张图会让后续 <Picture> 编号整体前移……」,
// 但只在缺图时告警,多一张(多视角开启后每镜必然多)反而没人管。
//
// 修法与既有几处"同一职责收口"一致:**谁装配参考图,谁写编号**。渲染期由
// RefManifest 生成权威清单附在提示词尾部,并明确声明它覆盖上文任何张数/编号的
// 说法;LLM 侧同时被告知不要断言总张数(director 的 sysH3Format)。
// director 的 sysH3Format 与 AssembleShotRefs 的调用点必须成对使用
// EnsureRefManifest(有闸门盯着:TestRefPlanAndManifestArePairedAtEveryCallSite)。

// refManifestKey 幂等判据(改文案时必须保留这个串)。
const refManifestKey = "PICTURE MANIFEST (authoritative"

// RefManifest 按实际装配的参考图写出权威清单。roles 为空(无参考图的 t2va)返回空串,
// 调用方原样放行。
func RefManifest(shot project.Shot, roles []RefRole) string {
	if len(roles) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(refManifestKey + " - written by the renderer from the pictures that are ACTUALLY " +
		"attached; it OVERRIDES any picture count or picture number stated anywhere above, because the " +
		"text above was written before the pictures were attached)\n")
	sb.WriteString(fmt.Sprintf("Exactly %d picture(s) are attached, in this order:\n", len(roles)))
	for _, r := range roles {
		switch r.Kind {
		case "character_front":
			sb.WriteString(fmt.Sprintf(
				"- <Picture %d> = %s, the character's FRONT SHEET. This is the identity authority for "+
					"%s: face shape, chin, eyes, brows, nose, skin tone, hair and every mark are copied "+
					"from THIS picture.\n", r.Picture, r.Name, r.Name))
		case "character_angles":
			sb.WriteString(fmt.Sprintf(
				"- <Picture %d> = %s AGAIN — the SAME character's multi-view strip (several angles inside "+
					"ONE picture). It is NOT a scene, NOT a second character and NOT a new subject: it only "+
					"supplies the other viewing angles. Its cells are small, so NEVER take the face from it — "+
					"<Picture %d> wins on every detail.\n", r.Picture, r.Name, r.Picture-1))
		case "character_front_in_strip":
			sb.WriteString(fmt.Sprintf(
				"- <Picture %d> = %s, one multi-view strip whose LEFTMOST cell is that character's front "+
					"sheet. The leftmost cell is the identity authority (face, hair, skin tone, costume and "+
					"marks); the remaining cells only give the other angles. It is NOT a scene and NOT several "+
					"characters.\n", r.Picture, r.Name))
		case "scene":
			sb.WriteString(fmt.Sprintf(
				"- <Picture %d> = %s (the SCENE reference). This is the scene picture and it is the LAST "+
					"picture: background layout, props and lighting come from it, the camera stays free.\n",
				r.Picture, r.Name))
		}
	}
	// 结束语按**本镜实际有没有拼合图**定制:没有拼合图时还提它,只会让模型去找一张不存在的图。
	hasStrip := false
	for _, r := range roles {
		if r.Kind == "character_angles" || r.Kind == "character_front_in_strip" {
			hasStrip = true
			break
		}
	}
	sb.WriteString("Use these numbers exactly as written. Never renumber them, never merge two pictures into " +
		"one subject, and never read a picture as something this manifest does not call it. If a sentence " +
		"above claims a different total")
	if hasStrip {
		sb.WriteString(", or calls a multi-view strip a second character or a scene")
	} else {
		sb.WriteString(", or treats the scene picture as a character")
	}
	sb.WriteString(" — that sentence is WRONG and this manifest is right.\n")
	sb.WriteString("(中文对照:下面是**渲染器按实际附上的图片**写出的编号清单,它**覆盖**上文任何关于张数/" +
		"编号的说法。")
	if hasStrip {
		sb.WriteString("多视角拼合图是**同一个角色**的另一组角度,不是场景、不是第二个角色,也不得当作" +
			"“取脸来源”(脸一律以定妆照为准)。")
	} else {
		sb.WriteString("本镜只附了定妆照与场景图:脸一律以定妆照为准,场景图只供背景与光线,不含角色。")
	}
	sb.WriteString(")\n")
	if shot.ID > 0 {
		sb.WriteString(fmt.Sprintf("(shot %d)\n", shot.ID))
	}
	return sb.String()
}

// EnsureRefManifest 把权威清单接到提示词尾部,并**把 subject_definitions 里的每个角色
// 绑到它实际那张图上**(幂等:已带过就不再叠)。
//
// 【为什么要绑】(2026-09-26 真机实测:换风格后成片男主的脸仍与定妆照对不上)
// MiniMax 官方的**全参考模式**格式(ref-en.txt §2.1/§2.2)写得非常明确:
//
//	<Subject 1> is the young woman **in <Picture 1>**, with long dark hair, …
//	"If an image is used only to define a character, scene, costume, or style,
//	 do not create a standalone picture entry. Instead, cite the image source
//	 INSIDE the corresponding <Subject N> definition."
//
// 而我们的 subject_definitions 只写 `<Subject 1> is 沈砚（human）— 十八九岁…以附图为准`,
// **没有把任何一张图绑到这个 subject 上**("附图"是泛指)。模型于是按**文字**重画:
// 同一个沈砚在不同镜里成了不同的人(戴抹额的公子 / 十五六岁的少年 / 长刘海青年),
// 而定妆照只用上了它的"风格" —— 这正是用户反复报的"角色漂移"。
// 修复不是再喊一遍"照参考图",而是补上官方那句 `in <Picture K>`。
//
// 位置与画面文字规则、音频标记一致放在尾部:官方要求的六段结构(subject_definitions →
// summary → retention_analysis → detailed_description → overall_soundscape →
// non_diegetic_music)保持不变,清单作为一段结构性声明并列其后。
func EnsureRefManifest(prompt string, shot project.Shot, roles []RefRole) string {
	if len(roles) == 0 {
		return prompt
	}
	if strings.Contains(prompt, refManifestKey) {
		return prompt
	}
	bound := bindSubjectsToPictures(prompt, roles)
	return strings.TrimRight(bound, "\n") + "\n\n" + RefManifest(shot, roles)
}

// reSubjectLine 匹配 subject_definitions 里的一行,捕获编号与名字。
// 例:`<Subject 1> is 沈砚（human）— 十八九岁…` → 1 / `沈砚`
var reSubjectLine = regexp.MustCompile(`(?m)^<Subject (\d+)>\s+is\s+([^,，.。(\n（]+)`)

// bindSubjectsToPictures 给 subject_definitions 里每个角色行补上它**实际那张图**的编号:
//
//	`<Subject 1> is 沈砚（human）— …`      →  `<Subject 1> is 沈砚 in <Picture 1>（human）— …`
//
// 编号一律从 roles(渲染器按实际装配顺序给出)里取,**不猜不造**;名字对不上的行原样保留
// (对不上说明它讲的不是这些参考图里的对象,硬塞编号比不绑更坏)。已经有 "in <Picture"
// 的行不动(幂等)。
func bindSubjectsToPictures(prompt string, roles []RefRole) string {
	// 名字 → 该对象的"身份权威"图片编号(拼合图不给编号:官方明确它只提供角度,
	// 而让它当身份源正是 2026-09-25 镜头6 漂移的根因)。
	auth := map[string]int{}
	for _, r := range roles {
		switch r.Kind {
		case "character_front", "character_front_in_strip", "scene":
			if _, ok := auth[r.Name]; !ok {
				auth[r.Name] = r.Picture
			}
		}
	}
	if len(auth) == 0 {
		return prompt
	}
	return reSubjectLine.ReplaceAllStringFunc(prompt, func(line string) string {
		if strings.Contains(line, "in <Picture") {
			return line // 已绑过
		}
		m := reSubjectLine.FindStringSubmatch(line)
		if m == nil {
			return line
		}
		num, name := m[1], strings.TrimSpace(m[2])
		pic, ok := auth[name]
		if !ok {
			return line
		}
		// 在名字之后插入官方句式,名称原样保留(模型要在下文按名字继续指代)。
		return "<Subject " + num + "> is " + name + " in <Picture " + strconv.Itoa(pic) + ">" +
			strings.TrimPrefix(line, m[0])
	})
}
