// cover_text_qc.go 封面成图的**文字判定**：书名以外还有没有别的文字/字形。
//
// 【2026-10-10 用户「封面文字内容版本需要优化」→ 口径「封面上只许有书名一处文字」】
//
// 为什么光改提示词不够：真机那张封面（`data/小说/离婚后，千亿前夫跪求复合/封面/封面.png`）
// 用的词里明明写着 `no other text`，出图仍在书名下方多画了一行乱码伪字 —— 平台自己的 VLM 原话：
//
//  1. 有文字。在下三分之一、居中；主标题约占宽85%、高20%，**下方另有一行极小字**。
//  2. 主标题：《离婚后，千亿前夫跪求复合》；**下方小字：乱码/糊成一团**。
//  7. **不能**：主标题正确清晰，但底部乱码小字必须删掉或重做后才可用。
//
// 根因：出图侧 cfg=1.0，**否定词不生效**（`pipeline/artstyle.go` 顶上那条平台口径），
// 而提到的名词会被当成"要画的东西"。所以提示词那侧改成**正向的唯一一处文字**口径
// （`coverTextClause`，全平台唯一出处）。但平台的规矩是"能确定性做到的不要让模型自觉" ——
// 于是这里补上**出图之后看图判一次**：
//
//   - 自动出封面（`seedCoverFromTitle`：视频制作里"书没有封面时先出一张"，**没人盯着**）
//     ⇒ 判出书名之外还有字形、或书名没认全 ⇒ **换种子重出一轮**（有界），仍不合格**如实播报**；
//   - 出候选（弹窗/菜单一键，**有人挑**）⇒ 每张候选都把判定带出来，挑之前就知道哪张干净。
//
// **不许把"没判成"说成"干净"**：没配视觉模型/调用失败时 judged=false + err 如实带出去。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/llm"
)

const coverTextQCSys = `你在做短剧封面的**文字审校**。只看这一张图，回答严格 JSON（不要 markdown 包裹、不要解释）：
{"texts":["图上出现的每一处文字，按位置逐条写；认不出是什么字就写 无法辨认"],
 "title_readable":true/false,
 "other_than_title":true/false,
 "note":"一句话结论（中文，40 字内）"}

判据：
1. 书名应当是「《%s》」。图上把它**认全**了才算 title_readable=true（字形残缺、错字、漏字、糊成一团都算 false）。
2. 图上**除了书名那一处**还有任何文字、字形、乱码伪字、水印、署名、边框字符 ⇒ other_than_title=true。
3. 图上只有书名一处文字 ⇒ other_than_title=false（书名号与标点不算"别的文字"）。
4. 只报你**真看见**的：没看见文字就如实说没有，不要脑补、不要客套。`

// coverTextVerdict 一次文字判定的结论（**必须能播报**：用户要能看出这张封面到底验没验过）。
type coverTextVerdict struct {
	Judged  bool     // 视觉模型真的判过了吗（false = 没配/调用失败 ⇒ **不许当成"干净"**）
	Stray   bool     // 书名之外还有别的文字/字形
	TitleOK bool     // 书名那一处认得全
	Texts   []string // 图上读到的每一处文字（模型原话）
	Note    string   // 一句话结论（模型原话）
	Err     string   // 没判成的原因（播报用）
}

// Bad 这张封面该不该重出：判过了、而且（书名之外还有字形 **或** 书名没认全）⇒ 不合格。
// 书名认不全同样不能用 —— 封面就是靠标题认书（真机那张 VLM 给的结论正是「不能」）。
func (v coverTextVerdict) Bad() bool {
	return v.Judged && (v.Stray || !v.TitleOK)
}

// Text 人读的一句话（日志/接口字段都用它，保证同一个口径）。
func (v coverTextVerdict) Text() string {
	if !v.Judged {
		return "没判成（" + v.Err + "）"
	}
	if !v.Bad() {
		return "只有书名一处文字"
	}
	why := make([]string, 0, 2)
	if v.Stray {
		s := "书名之外还有别的字形"
		if len(v.Texts) > 0 {
			s += "：" + strings.Join(v.Texts, " / ")
		}
		why = append(why, s)
	}
	if !v.TitleOK {
		why = append(why, "书名没认全")
	}
	if v.Note != "" {
		why = append(why, v.Note)
	}
	return strings.Join(why, "；")
}

// judgeCoverText 让**平台自己的视觉模型**看这张封面：除了书名还有没有别的文字。
//
// 失败一律如实带 err（判不了就是判不了，绝不静默返回"干净"）：
// 视觉模型没配 / 读不出图 / 调用失败 / 返回解析不了 —— 四种都记 Err。
func (s *Server) judgeCoverText(ctx context.Context, imgPath, bookName string) coverTextVerdict {
	g := s.cfg.Get()
	if !llm.New(g.VLM.BaseURL, g.VLM.APIKey, g.VLM.Model, 180).Configured() {
		return coverTextVerdict{Err: "未配置视觉模型（设置里填 VLM 地址/密钥/模型）"}
	}
	if strings.TrimSpace(imgPath) == "" {
		return coverTextVerdict{Err: "没有可判的图"}
	}
	dataURL, err := s.imageDataURL(ctx, imgPath)
	if err != nil {
		return coverTextVerdict{Err: "读图失败：" + err.Error()}
	}
	return s.judgeCoverTextData(ctx, dataURL, bookName)
}

// judgeCoverTextData 判定本体（**只吃 data URL**）：门直接喂它 ——
// 否则这条判据要依赖本机 ffmpeg/网络（`imageDataURL` 经 ffmpeg 压图），
// 在没有那些工具的机器上就成了"看着有门、其实什么都没拦"。
func (s *Server) judgeCoverTextData(ctx context.Context, dataURL, bookName string) coverTextVerdict {
	var v coverTextVerdict
	g := s.cfg.Get()
	cli := llm.New(g.VLM.BaseURL, g.VLM.APIKey, g.VLM.Model, 180)
	if !cli.Configured() {
		v.Err = "未配置视觉模型（设置里填 VLM 地址/密钥/模型）"
		return v
	}
	if strings.TrimSpace(dataURL) == "" {
		v.Err = "没有可判的图"
		return v
	}
	out, err := cli.ChatVision(ctx, fmt.Sprintf(coverTextQCSys, strings.TrimSpace(bookName)),
		[]llm.VisionContent{{Role: "user", Text: "这是这本小说的封面成图，请按要求判文字。", Images: []string{dataURL}}}, true)
	if err != nil {
		v.Err = "视觉模型调用失败：" + err.Error()
		return v
	}
	var card struct {
		Texts          []string `json:"texts"`
		TitleReadable  bool     `json:"title_readable"`
		OtherThanTitle bool     `json:"other_than_title"`
		Note           string   `json:"note"`
	}
	if err := json.Unmarshal([]byte(trimJSON(out)), &card); err != nil {
		v.Err = "视觉模型返回解析失败"
		return v
	}
	v.Judged = true
	v.Stray = card.OtherThanTitle
	v.TitleOK = card.TitleReadable
	v.Note = strings.TrimSpace(card.Note)
	for _, t := range card.Texts {
		if t = strings.TrimSpace(t); t != "" {
			v.Texts = append(v.Texts, t)
		}
	}
	return v
}

// coverTextRerollDecision 「判出不合格之后该不该再出一轮」——**纯函数**（便于穷举，也因为
// 出图那一段没有引擎跑不了：测试直接喂这张判定表就能验策略）。
//
// try 是 0 起数的当前轮次，tries 是总轮数（1 + 允许重出的次数）。
// 返回值在 msg=="" 时表示"通过，不用说话"。
func coverTextRerollDecision(v coverTextVerdict, try, tries int) (again bool, msg string) {
	if !v.Judged {
		// 没判成 ⇒ 不重出（重出靠的还是同一套判据，没判就无从判断），但**必须说清没验过**。
		return false, "封面文字没判成（" + v.Err + "）：这张按原样落盘，但**别当成验过**"
	}
	if !v.Bad() {
		return false, ""
	}
	if try+1 < tries {
		return true, fmt.Sprintf("封面文字不合格（%s）—— 换种子重出第 %d 轮", v.Text(), try+2)
	}
	return false, fmt.Sprintf("封面文字仍不合格（%s）—— 已重出共 %d 轮，这张仍落盘（如实播报，不假装干净）",
		v.Text(), tries)
}

// coverTextTries 自动出封面时一共出几轮：**1 次正常 + 1 次重出**（有界：一张多花约一分钟 GPU，
// 不能为了"干净"无限重出）。
const coverTextTries = 2

// coverTextSummary 一批候选的文字判定汇总（播报用）——"N 张里 M 张不合格 / K 张没判成"。
func coverTextSummary(vs []coverTextVerdict) string {
	bad, unjudged := 0, 0
	for _, v := range vs {
		switch {
		case !v.Judged:
			unjudged++
		case v.Bad():
			bad++
		}
	}
	if bad == 0 && unjudged == 0 {
		return "全部只有书名一处文字"
	}
	parts := make([]string, 0, 2)
	if bad > 0 {
		parts = append(parts, strconv.Itoa(bad)+" 张书名之外还有字形")
	}
	if unjudged > 0 {
		parts = append(parts, strconv.Itoa(unjudged)+" 张没判成")
	}
	return strings.Join(parts, "、")
}
