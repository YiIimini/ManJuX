// 身份一致性判定:把定妆照与若干帧一起送视觉大模型,逐帧回答"是不是同一个人"。
//
// 为什么需要它(2026-09-24 用户报「多视角/转台图和参考的定妆照不一致」):
// 多视角图是由 H3 **转身视频**抽帧来的,而 H3 是有创作自由的生成模型 —— 它可以把人
// 转着转着画成另一个人(实测过:换脸、换族裔、加腮红、变 Q 版)。旧实现对此毫无察觉:
// 转台图生产出来就直接当作该角色渲染时的**第二张参考图**喂回 H3,于是一张错的参考图
// 会主动把成片里的人脸往错的方向拉 —— 比"没有多视角"更糟。
//
// 所以这里补上闭环:生成之后先判"像不像本人",不像就不投入使用(见 pipeline/turnaround.go)。
// 判定用项目已配置的视觉模型(设置页 VLM);没配就跳过校验并如实告知用户。
package qc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/YiIimini/ManJuX/internal/llm"
)

// FrameScore 单帧的身份判定结果。
type FrameScore struct {
	Index      int    `json:"index"`       // 1 起(1 = 第一个候选帧)
	SamePerson bool   `json:"same_person"` // 是否与定妆照是同一个人
	Score      int    `json:"score"`       // 0-100 相似度
	Issue      string `json:"issue"`       // 不像的具体原因(中文,空=无问题)
}

// IdentityReport 一次多视角一致性判定。
type IdentityReport struct {
	Pass     bool         `json:"pass"`
	MinScore int          `json:"min_score"`
	Frames   []FrameScore `json:"frames"`
	// Reason 不通过时的摘要(中文),供日志直接展示。
	Reason string `json:"reason"`
}

const identitySystem = `You are a strict continuity checker for an animation production.
You are given ONE reference sheet (image 1) followed by several candidate frames (images 2..N) generated from that same
reference — for a person or creature it is a rotation of the same subject, for an artefact it is a turntable strip,
for a bodiless entity it is the same form seen from other angles.
For EVERY candidate frame, decide whether it depicts THE SAME SUBJECT as the reference sheet, judging the points listed
in the USER MESSAGE and NOTHING ELSE — do not invent extra requirements, and do not penalise a difference that the
subject's own kind makes legitimate (an artefact has no face; a bodiless entity may shift its silhouette a little;
a creature seen from behind hides its face).
Be strict: a re-designed subject, a different material or colour, a changed part count, a different species or a
restyled / de-aged / prettified / chibi-fied version, added marks or ornaments all count as NOT the same subject.
Additionally, flag as NOT the same subject if the subject has been turned into something it must never be:
an artefact drawn as a human or an animal, a creature turned into a person, or a bodiless entity hardened into a solid body.
Return STRICT JSON ONLY:
{"frames":[{"index":1,"same_person":true,"score":0-100,"issue":"中文,不像的具体原因;像则留空"}],"min_score":0-100,"pass":true/false,"reason":"中文一句话结论"}
"score" is 100 when the frame is indistinguishable in identity and near 0 when it is clearly a different subject.
"pass" must be true only when EVERY frame is the same subject as the reference.`

// IdentityJudge 判定多视角帧与定妆照是否同一主体。frames 为候选帧的**本地图片路径**。
//
// kindBrief 是该**实体类型**的判定要点(人看脸、器物看结构与材质、灵体看形体与透明度,
// 见 pipeline 的 entityProfiles.JudgePoints),kindLabel 是该类型的中文名(仅用于提示词)。
// 为什么必须按类型给判据:同一套"看脸像不像"的判据套在铜雀上,模型只能回答"没有脸,
// 无法判定",于是要么瞎给高分、要么把合格的角度图判成不合格。
//
// minScore 为通过阈值(每帧都必须达标);<=0 时取 70。任一张读不出来直接报错 ——
// 校验的意义就在于"必须都看过",漏看一张等于没校验。
func IdentityJudge(ctx context.Context, client *llm.Client, master string, frames []string, minScore int, kindBrief, kindLabel string) (*IdentityReport, error) {
	if client == nil {
		return nil, fmt.Errorf("未配置视觉模型")
	}
	if minScore <= 0 {
		minScore = 70
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("没有待判定的帧")
	}
	if strings.TrimSpace(kindBrief) == "" {
		kindBrief = "the same person: face shape, facial features, hairstyle, apparent age, ethnicity, skin tone and costume"
	}
	if strings.TrimSpace(kindLabel) == "" {
		kindLabel = "人"
	}
	var imgs []string
	for _, p := range append([]string{master}, frames...) {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("读取图片失败(%s): %w", p, err)
		}
		if len(raw) == 0 {
			return nil, fmt.Errorf("图片为空: %s", p)
		}
		imgs = append(imgs, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(raw))
	}
	out, err := client.ChatVision(ctx, identitySystem, []llm.VisionContent{
		{Role: "user", Text: fmt.Sprintf("Image 1 is the reference sheet. Images 2-%d are candidate angle frames. "+
			"The subject's entity type is: %s. Judge every candidate against image 1 on exactly these points: %s. "+
			"Return the JSON.", len(imgs), kindLabel, kindBrief), Images: imgs},
	}, true)
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(out)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	if a := strings.Index(s, "{"); a >= 0 {
		if b := strings.LastIndex(s, "}"); b > a {
			s = s[a : b+1]
		}
	}
	var rep IdentityReport
	if err := json.Unmarshal([]byte(s), &rep); err != nil {
		return nil, fmt.Errorf("身份判定输出解析失败: %v", err)
	}
	rep.MinScore = minScore
	// **以逐帧分数为准重算 pass**,不信模型自己的汇总字段:它常把"看着像"写成 pass,
	// 而下面的某个帧分数其实只有 30 分。判据落在一处,别让模型有机会和它自己矛盾。
	pass := len(rep.Frames) > 0
	min := 100
	var bad []string
	for _, fr := range rep.Frames {
		if fr.Score < min {
			min = fr.Score
		}
		if !fr.SamePerson || fr.Score < minScore {
			pass = false
			issue := strings.TrimSpace(fr.Issue)
			if issue == "" {
				issue = "与定妆照不像同一个" + kindLabel
			}
			bad = append(bad, fmt.Sprintf("第 %d 帧(%d 分):%s", fr.Index, fr.Score, issue))
		}
	}
	rep.Pass = pass
	if pass {
		rep.Reason = fmt.Sprintf("逐帧与定妆照一致(%s,最低 %d 分)", kindLabel, min)
	} else {
		rep.Reason = strings.Join(bad, ";")
	}
	return &rep, nil
}
