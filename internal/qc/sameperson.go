package qc

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/llm"
)

// sameperson.go 撞脸判定：两张角色定妆照**是否会被认成同一个人**。
//
// ## 为什么要它（2026-09-27 用户报「周廷和主角脸相似，这不合理吧」）
//
// 查证结果很清楚：**两边的外形锁分得很开**（沈砚 19 岁瘦长脸/凤眼/浅麦色
// vs 周廷 31 岁方脸/高颧骨/深眼窝/蜡黄），可出图却是两个瘦长脸的年轻人、连发髻都差不多。
// 也就是说：**提示词写对了，渲染器没照做**。这类失败任何"读提示词"的检查都看不见
// —— 只能**看图**。所以这一条把两张已生成的定妆照交给视觉模型，问一个只有看图才能回答的问题。
//
// ## 判定口径
//
// 只比**脸**：脸型与骨相、眼型、眉形、年龄感、肤色、发型（含额前状态）。
// 衣服/配饰/光线不算 —— 那些本来就不同，拿它们当差异等于永远判定"不同"。
// 判定 SAME 的含义是"换身衣服就会被观众当成同一个人"（含"像同一个演员/像兄弟"）。
type SamePersonVerdict struct {
	Same bool   // true = 判定撞脸
	Note string // 一句话说明（SAME 时说明像在哪；给人看）
	Raw  string // 模型原文（排查用）
}

const samePersonSystem = "你是短剧的选角与角色一致性审片员。你的唯一任务：判断两张角色定妆照" +
	"会不会被观众认成**同一个人**。只比脸 —— 脸型与骨相、眼型、眉形、年龄感、肤色、发型(含额前状态)；" +
	"服装、配饰、光线、背景、构图、画风都不算差异（那些本来就不同）。" +
	"如果换身衣服就会被当成同一个人，或像同一个演员演的两个角色、像亲兄弟级别，就判 SAME。" +
	"严格按要求输出，不要解释过程。"

// DetectSamePerson 读两张图并判定是否撞脸。
func DetectSamePerson(ctx context.Context, client VisionClient, pathA, pathB string) (*SamePersonVerdict, error) {
	if client == nil {
		return nil, fmt.Errorf("未配置视觉模型")
	}
	a, err := imageDataURL(pathA)
	if err != nil {
		return nil, err
	}
	b, err := imageDataURL(pathB)
	if err != nil {
		return nil, err
	}
	return judgeSamePerson(ctx, client, a, b)
}

// judgeSamePerson 把已编码的图交给视觉模型（便于单测用假客户端穷举解析分支）。
func judgeSamePerson(ctx context.Context, client VisionClient, dataURLA, dataURLB string) (*SamePersonVerdict, error) {
	if client == nil {
		return nil, fmt.Errorf("未配置视觉模型")
	}
	out, err := client.ChatVision(ctx, samePersonSystem, []llm.VisionContent{{
		Role:   "user",
		Text:   samePersonPrompt(),
		Images: []string{dataURLA, dataURLB},
	}}, false)
	if err != nil {
		return nil, err
	}
	return parseSamePerson(out)
}

func samePersonPrompt() string {
	return "Picture 1 与 Picture 2 是**同一部剧里两个不同角色**的定妆照（各自单独生成，互不参考）。\n" +
		"只看脸判断：这两张会不会被观众认成同一个人？\n" +
		"第一行只回一个词：SAME 或 DIFFERENT。\n" +
		"第二行用不超过 30 个中文字说清理由（SAME 时说明像在哪：脸型/眼型/年龄感/发型；" +
		"DIFFERENT 时说明最明显的两处不同）。"
}

// imageDataURL 读图并编码成 data URL（与原图同字节，不缩放：判定的是细粒度五官）。
func imageDataURL(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("缺少图片路径")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取图片失败:%w", err)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("图片为空:%s", path)
	}
	mime := "image/png"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".webp":
		mime = "image/webp"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

// parseSamePerson 解析判定结果。
//
// 容忍度按**实测口径**给：模型可能输出 `SAME`、`SAME.`、`**SAME**`、`同一个人`、
// `判定：SAME`，也可能先写一句解释再给结论。所以：
//   - 先找第一行里的关键词；找不到就全文找（模型偶尔把结论写在第二行）；
//   - 中文词同样认（"同一个人/相同/撞脸" vs "不同/两个人/不像"）；
//   - **认不出就报错**（不默认成"不同"）—— 判定失败必须与"判定为不同"分开，
//     否则一次调用失败会被当成"通过"（与比例自检同一条口径）。
func parseSamePerson(out string) (*SamePersonVerdict, error) {
	raw := strings.TrimSpace(out)
	if raw == "" {
		return nil, fmt.Errorf("视觉模型返回空")
	}
	verdict := func(s string) (bool, bool) {
		u := strings.ToUpper(s)
		if strings.Contains(u, "DIFFERENT") || strings.Contains(s, "不同") || strings.Contains(s, "不像") ||
			strings.Contains(s, "两个人") || strings.Contains(s, "非同一") {
			return false, true
		}
		if strings.Contains(u, "SAME") || strings.Contains(s, "同一人") || strings.Contains(s, "同一个人") ||
			strings.Contains(s, "相同") || strings.Contains(s, "撞脸") || strings.Contains(s, "像同一个人") {
			return true, true
		}
		return false, false
	}
	lines := strings.Split(raw, "\n")
	first := ""
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			first = ln
			break
		}
	}
	same, ok := verdict(first)
	if !ok {
		same, ok = verdict(raw)
	}
	if !ok {
		return nil, fmt.Errorf("认不出判定结果(既没有 SAME 也没有 DIFFERENT):%.120s", raw)
	}
	// note = 除结论那一行以外的第一段非空内容（模型通常第二行给理由）
	note := ""
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if i == 0 {
			continue
		}
		note = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(ln), "-—：: "))
		break
	}
	if note == "" {
		note = strings.TrimSpace(strings.Replace(raw, first, "", 1))
	}
	if r := []rune(note); len(r) > 80 {
		note = string(r[:80]) + "…"
	}
	return &SamePersonVerdict{Same: same, Note: note, Raw: raw}, nil
}

// SamePersonWarnText 撞脸时给人看的一句话（空 = 不撞）。
func SamePersonWarnText(self, other string, v *SamePersonVerdict) string {
	if v == nil || !v.Same {
		return ""
	}
	txt := fmt.Sprintf("定妆照「%s」与「%s」**长得像同一个人**", self, other)
	if v.Note != "" {
		txt += "（" + v.Note + "）"
	}
	return txt + " —— 同一部剧里这样会被观众认成一个人；请重出其中一张（脸型/眼型/年龄感要能一眼分开）"
}
