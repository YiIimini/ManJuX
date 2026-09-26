package pipeline

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/YiIimini/ManJuX/internal/project"
)

// voice_bind.go 把**声线描述绑到说话的那一句上**(官方全参考/视频提示词格式要求的位置)。
//
// 【为什么必须绑】(2026-09-26 真机实测,用户报「配音也垃圾啊」)
// 现场提示词是这样两句:
//
//	【声线(逐字照抄,不得改写)】本镜每句台词的 [voice description] 必须与下面这一条逐字一致:
//	- 沈砚: a teenage boy's voice, light and quick
//	...
//	[Shot 1] 一个壮汉抬脚朝他肩膀踩下来。 Off-screen narration: the narrator says in an
//	off-screen voiceover: <d>[Chinese]还装死？</d>
//
// 两个问题:
//  1. **声线没落在说话点上**。官方格式是把音色写在**这句话的引述里**
//     (`<Subject 1> (S1) says in a <voice description>: <d>[Language]…</d>`),
//     而我们把它放在提示词顶部的一段中文说明里、正文那句只有一个裸的
//     "says in an off-screen voiceover:" —— 模型读到的音色信息是**缺席**的,于是每次自己编,
//     一集里旁白能有好几种嗓子。
//  2. 那段中文说明本身还带着字面占位符 `[voice description]`;更糟的是它下面那些
//     `- 沈砚: a teenage boy's voice, light and quick` 这种**短英文行**,真机里被模型
//     当成"要念/要打字幕的一行"—— 成片字幕里出现了 "a every' s voice light and qught"。
//
// 修法与既有的"谁装配谁负责"一致:**在渲染装配的最后一刻,把音色写进每一句引述里**,
// 并把音色清单改成不会被误当台词的形式(官方句式的 casting 段 + 全局文字政策兜底)。

// voiceBindKey 幂等判据(改文案必须保留这个串)。
const voiceBindKey = "VOICE CASTING (authoritative"

// narratorVoiceDefault 旁白的默认音色。
//
// 为什么由平台给:小说技能端的角色卡**只有角色的 voice,没有旁白的** —— 真机 23 个镜头的
// 旁白句一个音色都没有,全靠模型现编(用户听到的"配音垃圾")。旁白是全片最稳的那条声线,
// 必须有一个**跨镜、跨集一致**的默认值;技能端一旦自己给了旁白音色,这里就按它的走。
const narratorVoiceDefault = "a warm, low-pitched adult male narrator voice, calm and cinematic, " +
	"steady pacing with clear Mandarin broadcast delivery"

// reNarratorSays 旁白引述(官方要求音色写在冒号前)。
var reNarratorSays = regexp.MustCompile(`(?i)(the narrator says[^:<\n]{0,40}?voiceover)(\s*:)`)

// reSaysWithPlaceholder 引述里还是字面占位符 `[voice description]` 的句子。
var reSaysWithPlaceholder = regexp.MustCompile(`(?i)says in a\s*\[voice description\]\s*:`)

// EnsureVoiceInline 把声线绑进正文引述,并附一段官方句式的 casting 清单。幂等。
//
// plan 为 nil 或没有角色时,仍会处理旁白(旁白不依赖角色卡)。
func EnsureVoiceInline(prompt string, plan *project.Plan, chars []project.Character) string {
	if strings.Contains(prompt, voiceBindKey) {
		return prompt
	}
	out := prompt

	// ① 旁白:音色写进引述("...voiceover in <voice>:")
	voice := narratorVoiceDefault
	narrated := reNarratorSays.MatchString(out)
	if narrated {
		out = reNarratorSays.ReplaceAllString(out, "${1} in "+voice+"${2}")
	}

	// ② 角色:把字面占位符换成本镜该说话人的音色(按同一行里出现的名字判定,判不出就不动 ——
	//    宁可留着占位符让模型自由发挥,也不要塞错人的音色)。
	named := 0
	for i := range chars {
		c := chars[i]
		v := strings.TrimSpace(c.Voice)
		if v == "" {
			continue
		}
		named++
		out = replaceInLines(out, c.Name, func(line string) string {
			if !reSaysWithPlaceholder.MatchString(line) {
				return line
			}
			return reSaysWithPlaceholder.ReplaceAllString(line, "says in "+v+":")
		})
	}
	// 占位符没被任何角色认领(例如行里用的是 <Subject 1> 而不是名字):整句替换掉,
	// 免得字面 `[voice description]` 留在提示词里 —— 模型会把它当内容。
	out = reSaysWithPlaceholder.ReplaceAllString(out, "says in a steady, clearly articulated voice:")

	// ③ casting 清单:官方句式(名字 + 音色),并明确"这是制作说明,不是台词"。
	var sb strings.Builder
	sb.WriteString("\n\n" + voiceBindKey + " - use exactly these timbres, and keep them identical across " +
		"every shot and episode; this section is a production note, never speak it, never caption it):\n")
	if narrated {
		sb.WriteString("- the narrator speaks with " + voice + "\n")
	}
	for i := range chars {
		c := chars[i]
		if strings.TrimSpace(c.Voice) == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s speaks with %s\n", c.Name, strings.TrimSpace(c.Voice)))
	}
	if !narrated && named == 0 {
		return out // 既没有旁白也没有已知音色的角色:不留空段
	}
	return strings.TrimRight(out, "\n") + "\n" + sb.String()
}

// replaceInLines 只对"提到 name 的行"做改写,其余行原样。逐行处理是为了不跨句误改。
func replaceInLines(s, name string, fn func(string) string) string {
	if !strings.Contains(s, name) {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if strings.Contains(ln, name) {
			lines[i] = fn(ln)
		}
	}
	return strings.Join(lines, "\n")
}
