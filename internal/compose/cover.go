package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/YiIimini/ManJuX/internal/ff"
)

// cover.go **集封面**（每集一张）——选帧版，不烧显卡。
//
// ## 口径（2026-09-30 用户拍板：「集的封面图不需要技能侧吧」+「做啊，继续完善」）
//
//   - **剧封面**是技能侧既有的 hero 工艺（一部一张、五要素构图、顶部留剧名位），平台不动它；
//   - **集封面**归平台：**从成片里挑一帧**再压集名文字。理由三条 ——
//     ① 零额外生成、和剧里天然一致（就是同一帧），88 集批量也不会出现"88 张不一样的脸"；
//     ② 文字由 ffmpeg `drawtext` 压上去，**不让图像模型画字**（官方 ON-SCREEN TEXT 口径下
//     模型画的字必然是伪汉字；技能侧那份封面工艺卡也正是"上三分之一留白给书名"）；
//     ③ 挑不出抓人的一帧（全暗调/全背影）时才有必要回到生成路线，那是另一条，不混进来。
//
// ## 产物
//
//	out/cover.jpg                  ← 默认选用第 1 张候选（用户可在工作台换）
//	out/.cover-candidates/cover_1.jpg … cover_3.jpg
//
// 候选取全集时长的 15% / 50% / 82% 三个点（开场钩子、中段冲突、结尾钩子），
// 每个点抓一帧 → 缩放到交付尺寸 → 压集名。

// CoverCandidateRatios 候选取帧的位置（占全集时长的比例）。
var CoverCandidateRatios = []float64{0.15, 0.50, 0.82}

// coverTitleMaxRunes 集名画上去的字数上限（太长会压满画面）。
const coverTitleMaxRunes = 16

// CoverTitle 由集名/集号拼出封面标题（超长截断；返回的文本会原样画到封面上）。
func CoverTitle(episodeLabel string) string {
	s := strings.TrimSpace(episodeLabel)
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > coverTitleMaxRunes {
		r = append(r[:coverTitleMaxRunes-1], '…')
	}
	return string(r)
}

// coverFilter 拼出"抓帧 → 缩到交付尺寸 → 压标题"的滤镜串。
//
// 标题走 **textfile**（不是 text=）：中文直接写进滤镜串要处理冒号/逗号/引号转义，
// 一旦漏一个字符整个 ffmpeg 调用就废；写进一个 UTF-8 文件最稳。
func coverFilter(w, h int, titleFile string) string {
	return fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,"+
			"drawtext=fontfile='%s':textfile='%s':fontcolor=white:fontsize=%d:"+
			"borderw=6:bordercolor=black@0.85:x=(w-text_w)/2:y=h*0.08",
		w, h, w, h, coverFontFile(), escapeFilterPath(titleFile), h/14)
}

// coverFontFile 中文字体（Windows 自带；缺失时 drawtext 会失败，调用方只报 warn）。
func coverFontFile() string {
	for _, p := range []string{
		`C:\Windows\Fonts\msyh.ttc`, // 微软雅黑
		`C:\Windows\Fonts\msyhbd.ttc`,
		`C:\Windows\Fonts\simhei.ttf`, // 黑体
	} {
		if _, err := os.Stat(p); err == nil {
			return escapeFilterPath(p)
		}
	}
	return "sans" // 交给 fontconfig 兜底
}

// escapeFilterPath ffmpeg 滤镜里写 Windows 路径要把 `:` 转义（`C\:/...`）并把 `\` 换成 `/`。
func escapeFilterPath(p string) string {
	return strings.ReplaceAll(strings.ReplaceAll(p, `\`, `/`), ":", `\:`)
}

// safeCoverName 集名里可能有 `/`、`:` 这类不能进文件名的字符。
var unsafeCoverName = regexp.MustCompile(`[\\/:*?"<>|\s]+`)

// EpisodeCover 出**集封面**：按比例从成片抓三帧候选并压上集名，默认选用第 1 张。
//
// 返回默认封面路径（失败返回空串与错误；调用方只报一条 warn，不阻断合成）。
func EpisodeCover(ctx context.Context, f *ff.FF, filmPath, outDir, episodeLabel string, w, h int, titleSizeHint int) (string, error) {
	if f == nil || strings.TrimSpace(filmPath) == "" {
		return "", fmt.Errorf("没有成片可用来做封面")
	}
	if _, err := os.Stat(filmPath); err != nil {
		return "", fmt.Errorf("成片不在：%w", err)
	}
	info, err := f.Probe(filmPath)
	if err != nil || info == nil || info.Duration <= 0 {
		return "", fmt.Errorf("读不出成片时长：%v", err)
	}
	dur := info.Duration
	w, h = pickCoverSize(w, h, info.Width, info.Height)
	title := CoverTitle(episodeLabel)
	candDir := filepath.Join(outDir, ".cover-candidates")
	if err := os.MkdirAll(candDir, 0o755); err != nil {
		return "", err
	}
	// 标题写文件（UTF-8）：中文走滤镜串转义太脆。
	titleFile := filepath.Join(candDir, "title.txt")
	if err := os.WriteFile(titleFile, []byte(title), 0o644); err != nil {
		return "", err
	}
	vf := coverFilter(w, h, titleFile)
	var first string
	for i, ratio := range CoverCandidateRatios {
		at := dur * ratio
		out := filepath.Join(candDir, fmt.Sprintf("cover_%d.jpg", i+1))
		if _, err := f.Run(ctx, "-y", "-hide_banner", "-loglevel", "error",
			"-ss", fmt.Sprintf("%.2f", at), "-i", filmPath,
			"-frames:v", "1", "-vf", vf, "-q:v", "3", out); err != nil {
			continue // 单张候选失败不影响其它候选
		}
		if first == "" {
			first = out
		}
	}
	if first == "" {
		return "", fmt.Errorf("三张候选一张都没抓出来")
	}
	defaultCover := filepath.Join(outDir, "cover.jpg")
	if err := copyFile(first, defaultCover); err != nil {
		return "", err
	}
	return defaultCover, nil
}

// pickCoverSize 封面尺寸：给了就用给的（交付尺寸），没给就照成片自己的画幅。
func pickCoverSize(w, h, filmW, filmH int) (int, int) {
	if w > 0 && h > 0 {
		return w, h
	}
	if filmW > 0 && filmH > 0 {
		return filmW, filmH
	}
	return 1080, 1920
}

// copyFile 小文件复制（封面只有几百 KB）。
func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
