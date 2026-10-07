package server

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// —— 剧本的**爆款纪律**判据（来源：用户《一、网文小说转剧本提示词指令》）——
//
// 【为什么单开这一道门】（2026-10-07，用户：「总感觉剧本提取文章内容不是按照爆款短剧要求来的」）
//
// 那份文件把改编纪律写死了四条：开篇 3 秒爆点（**无铺垫、无旁白起手**）、爽点密度、
// 段段留钩、砍冗余。平台此前只把它抄进提示词（novels_jobs.go 的 sys「一、爆款开篇」），
// **没有任何机器判据** —— 而本仓的结论早就写死了：散文规则管不住模型（AGENTS.md：
// 「一条规则 → 一条测试」，以及「生成 → 机器判定 → 把违规喂回去重写」）。
//
// 真机取证（第01集 source.txt，2082 字，**结构门全过**：4 场每场六节、道具清单齐全）：
//
//	· 【开场爆点】写成了旁白：
//	    「“重新检测到生命反应。”——灰纪元第八万年，一个不该存在的编号，在死寂的维护舱里睁开了眼。」
//	  而引号里那句台词在**第 1 场正文里一次都没出现**。正文前几行是环境与外形描写：
//	    「镜面地板，冷白灯只亮右边一排。」「祁七单膝撑地，右手攥着枚发暗的铭牌。」
//	    「头顶黄铜环转，六个发光点，亮四个。」
//	  冲突台词到第 6 行才起（「异动来源，硬件钢印U-80001。」）⇒ 分镜第一镜拍的是环境、
//	  前几镜全在交代外形 ⇒ 违反「无铺垫、无旁白起手，直接切入高潮名场面」；
//	· 全集 4 场只有 **1** 个大爽点（作者口径 2-3 个）⇒ 违反「爽点密度」。
//
// 判据只用**确定性**信息（引号里的台词、行序、节标记），不猜语义：
// 「放大反差」「砍冗余的取舍」这种要靠人看的**不进这道门** —— 别把门做成摆设（会天天误报，
// 然后被忽略，最后等于没有）。
//
// 与 `missingBriefSections` 的分工：那一门管「这一节**有没有**写」；这一门管
// 「写了、但**没按爆款纪律写**」。两者合成 `briefContractIssues`，供三处共用：
// ① 「已经是像样的剧本」的跳过判据（briefIsUsable）② 生成后要不要喂回去补写一轮
// ③ 补写结果要不要采纳（adoptRepairedBrief）。同一个判据只能有一份，否则必然漂移
// （本仓栽过：自愈逻辑只挂在一个入口上，另外两个入口从不跑）。

const (
	// briefBoomWindowLines 开场爆点必须落在第 1 场正文的**前几行**内。
	// 剧本正文一行 ≈ 一镜（3-8 秒）⇒ 3 行 ≈ 首 10-20 秒；仍排不进去，说明开场在铺垫。
	briefBoomWindowLines = 3

	// briefMinBigPayoffs 全集至少几个「大爽点」。作者口径是「2-3 个大爽点」
	// （分镜提示与 project.CoreBeats 用的是同一个数）⇒ 取 2 为硬线。
	//
	// 「每 80 字小爽点 / 200 字大爽点」那条**不按字数硬算**：分镜侧同样只按 200 字卡
	// 强节拍（见 project/plan_checks.go 的说明）—— 80 字那条是引导语，硬算会把
	// 一份 2000 字的剧本判成"该有 25 个小爽点"，那不是门，是噪音。
	briefMinBigPayoffs = 2

	briefBigPayoffWord = "大爽点"
)

// briefSceneFields 每一场必须写全的节（与 briefSectionMarkers 里那五个同名）。
var briefSceneFields = []string{"目的", "冲突", "转折", "爽点", "钩子"}

var (
	// 场次头：`【场 3|地点|时间|内外】`（全角竖线与空格都认）。
	briefSceneHeadRE = regexp.MustCompile(`【场\s*(\d+)\s*[|｜]`)
	// 一场戏里的节标记（用于切「正文:」开始、遇到下一个节就结束）。
	briefFieldRE = regexp.MustCompile(`^(目的|冲突|转折|爽点|钩子|正文)\s*[:：]`)
	// 爆点里的台词（引号内）——「3 秒要演的那一句」就是它。
	briefQuoteRE = regexp.MustCompile(`[“"「『]([^”"」』]{2,})[”"」』]`)
	// 归一化：判「这句话有没有出现」时，标点与空白不该造成区别
	//（真机里同一句台词在爆点栏与正文里的引号、逗号写法并不一致）。
	briefNoiseRE = regexp.MustCompile(`[^\p{Han}\p{Latin}\p{Nd}]+`)
)

// briefNormalize 只留汉字/字母/数字。
func briefNormalize(s string) string { return briefNoiseRE.ReplaceAllString(s, "") }

// briefScene 一场戏：场号 + 从场次头到下一个场次头之间的原文。
type briefScene struct {
	num  int
	text string
}

// briefScenes 把剧本按场次头切成一场一块。场次头之前的内容（【开场爆点】等）不属任何一场。
func briefScenes(brief string) []briefScene {
	var out []briefScene
	var cur []string
	num := 0
	for _, ln := range strings.Split(brief, "\n") {
		if m := briefSceneHeadRE.FindStringSubmatch(ln); m != nil {
			if num > 0 {
				out = append(out, briefScene{num: num, text: strings.Join(cur, "\n")})
			}
			num, _ = strconv.Atoi(m[1])
			cur = []string{ln}
			continue
		}
		if num > 0 {
			cur = append(cur, ln)
		}
	}
	if num > 0 {
		out = append(out, briefScene{num: num, text: strings.Join(cur, "\n")})
	}
	return out
}

// briefSceneBody 取一场戏里「正文:」下面的前 k 行
// （正文可以写在「正文:」同行，也可以另起行；遇到下一个节或下一个【节名】就停）。
func briefSceneBody(scene string, k int) []string {
	var body []string
	inBody := false
	for _, ln := range strings.Split(scene, "\n") {
		t := strings.TrimSpace(ln)
		if !inBody {
			if m := briefFieldRE.FindStringSubmatch(t); m != nil && m[1] == "正文" {
				inBody = true
				if rest := strings.TrimSpace(t[len(m[0]):]); rest != "" {
					body = append(body, rest)
				}
			}
			continue
		}
		// 正文结束：下一个节标记，或下一个【…】节名（真机里是【关键道具与信息】）。
		if briefFieldRE.MatchString(t) || strings.HasPrefix(t, "【") {
			break
		}
		if t == "" {
			continue
		}
		body = append(body, t)
		if len(body) >= k {
			break
		}
	}
	return body
}

// briefBoomText 取【开场爆点】那一节的内容（到行尾；真机里它与【本集剧情】同行）。
func briefBoomText(brief string) (string, bool) {
	const mark = "【开场爆点】"
	i := strings.Index(brief, mark)
	if i < 0 {
		return "", false
	}
	rest := brief[i+len(mark):]
	if j := strings.Index(rest, "\n"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest), true
}

// briefBoomCore 爆点里**必须被演出来的那一句**：优先引号里的台词；没有引号时取第一个
// 句末标点之前（「——」后面的解释性旁白不算爆点本身）。判不了就返回空串（宁可放过，不误报）。
func briefBoomCore(boom string) string {
	if m := briefQuoteRE.FindStringSubmatch(boom); m != nil {
		return strings.TrimSpace(m[1])
	}
	seg := boom
	if i := strings.IndexAny(seg, "。！？"); i >= 0 {
		seg = seg[:i]
	}
	if i := strings.Index(seg, "——"); i > 0 {
		seg = seg[:i]
	}
	seg = strings.TrimSpace(seg)
	if len([]rune(seg)) < 4 {
		return ""
	}
	return seg
}

// briefViralIssues 剧本违反**爆款纪律**的地方（一条一句人话，可直接喂回模型重写）。
func briefViralIssues(brief string) []string {
	var issues []string
	scenes := briefScenes(brief)

	// ① 开场 3 秒爆点：栏里那一句必须**真的演在第 1 场正文的最前面**。
	if boom, ok := briefBoomText(brief); ok {
		if core := briefBoomCore(boom); core != "" && len(scenes) > 0 {
			head := strings.Join(briefSceneBody(scenes[0].text, briefBoomWindowLines), "")
			if strings.TrimSpace(head) != "" && !strings.Contains(briefNormalize(head), briefNormalize(core)) {
				issues = append(issues, fmt.Sprintf(
					"开场爆点没被演出来：【开场爆点】里的「%s」不在第 1 场正文前 %d 行内（现在开头是「%s」）"+
						"—— 3 秒爆点要**无铺垫、无旁白起手**：把它的台词或动作写成第 1 场正文的最前面几行，"+
						"写在爆点栏里却让正文从环境/外形描写起手，分镜第一镜拍的就是铺垫",
					clampRunes(core, 24), briefBoomWindowLines, clampRunes(head, 40)))
			}
		}
	}

	// ② 大爽点数量（爽点密度里唯一能确定性判的那一条）。
	if n := strings.Count(brief, briefBigPayoffWord); n < briefMinBigPayoffs {
		issues = append(issues, fmt.Sprintf(
			"全集只有 %d 个大爽点（口径：至少 %d 个）—— 每场的「爽点:」节要写明「小爽点——…」还是「大爽点——…」，"+
				"大爽点写成能拍的动作或台词（逆袭/打脸/揭秘/升阶）", n, briefMinBigPayoffs))
	}

	// ③ 逐场逐节（段段留钩）：**每一场**都要自己写全那五节 + 正文。
	//    只在「这一节整篇都写了」时才逐场查 —— 整篇都没有是 missingBriefSections 的活，
	//    别为同一件事报两遍。
	for _, sc := range scenes {
		for _, f := range briefSceneFields {
			if briefHasMarker(brief, f) && !briefHasMarker(sc.text, f) {
				issues = append(issues, fmt.Sprintf(
					"第 %d 场缺「%s」节 —— 段段留钩：每一场都要逐节写全（%s + 正文）",
					sc.num, f, strings.Join(briefSceneFields, "/")))
			}
		}
		if len(briefSceneBody(sc.text, 1)) == 0 {
			issues = append(issues, fmt.Sprintf(
				"第 %d 场没有正文（只剩分节标注）—— 分镜拿不到这场要拍的动作与台词", sc.num))
		}
	}
	return issues
}

// briefContractIssues 剧本与契约的**全部**差距：结构（缺节）+ 爆款纪律（briefViralIssues）。
//
// 三个消费方必须看同一份判据（跳过判据 / 生成后的补写一轮 / 补写的采纳判据）——
// 各写一份必然漂移。
func briefContractIssues(brief string) []string {
	return append(missingBriefSections(brief), briefViralIssues(brief)...)
}

// briefIssueSummary 把问题清单压成一行人话（日志用，超长就截断并注明总数）。
func briefIssueSummary(issues []string, max int) string {
	if max > 0 && len(issues) > max {
		return strings.Join(issues[:max], "；") + fmt.Sprintf("；…（共 %d 条）", len(issues))
	}
	return strings.Join(issues, "；")
}

// briefIssueBullets 把问题清单排成给模型看的逐条清单（补写指令用）。
func briefIssueBullets(issues []string) string {
	var sb strings.Builder
	for i, it := range issues {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("· ")
		sb.WriteString(it)
	}
	return sb.String()
}
