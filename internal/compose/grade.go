package compose

import "strings"

// 调色档(甲1)。终混此前只有字幕与响度,零调色 —— 成片观感是"H3 直出"的平淡数码味。
// 四档都必须在界面上可选(assemble.grade),所以取值用稳定的英文 key,中文只在播报里出现。
const (
	// GradeOff 不调色:终混视频零滤镜(此时才允许 -c:v copy,见 finalVideoFilters)。
	GradeOff = "off"
	// GradeSoft 轻调:只抬一点对比与饱和,不压暗角、不加颗粒。适合本来就干净的素材。
	GradeSoft = "soft"
	// GradeFilm 电影感(默认档):压饱和 + 冷阴影/暖高光 + 轻锐化 + 暗角 + 胶片颗粒。
	GradeFilm = "film"
	// GradeHeavy 重调:film 之上加倍颗粒并加 2px 色差(色边),夜戏/情绪戏用。
	GradeHeavy = "heavy"
)

// 各档的滤镜串。
//
// 为什么 film/heavy 的参数值一个都不许动:这一串是拿真实素材逐项试出来的
// (对比 1.12 / 饱和 0.94 / gamma 0.96、colorbalance 的阴影靠蓝高光靠红、
// vignette=PI/4.5 的暗角强度、颗粒 6),重调一遍等于重新定风格。
//
// **unsharp 的写法必须是 `unsharp=5:5:0.8:5:5:0.0`**(参数序:luma_msize_x:luma_msize_y:
// luma_amount:chroma_msize_x:chroma_msize_y:chroma_amount)。曾经写成
// `unsharp=5:5=0.0:5:5:0.8` —— ffmpeg 把 `5=0.0` 当成"名为 5=0.0 的选项"直接拒绝整条链:
//
//	[AVFilterGraph] No option name near '5:5:0.8'
//	Error parsing filterchain '...': Invalid argument
//
// (2026-09-26 本机 ffmpeg 9.0.1 实测;参数名与顺序见 ffmpeg-filters 文档的 unsharp 一节。
// 这条链每一档都已用 `ffmpeg -f lavfi -i testsrc -vf <链> -f null -` 跑通,不是照抄。)
const (
	gradeSoft = "eq=contrast=1.06:saturation=0.97:gamma=0.98,unsharp=5:5:0.5"

	gradeFilm = "eq=contrast=1.12:saturation=0.94:gamma=0.96," +
		"colorbalance=rs=-0.04:gs=-0.01:bs=0.06:rm=0.02:bm=-0.02:rh=0.04:gh=0.01:bh=-0.05," +
		"unsharp=5:5:0.8:5:5:0.0,vignette=PI/4.5,noise=alls=6:allf=t+u"

	// heavy = film 同一串,颗粒推到 10 并在暗角之后加 2px 色差。
	// 用"改写 film 里的 noise"而不是再叠一个 noise 滤镜:叠两个只会把颗粒噪上加噪,
	// 且成本翻倍,观感反而不如一次性加重。
	gradeHeavy = "eq=contrast=1.12:saturation=0.94:gamma=0.96," +
		"colorbalance=rs=-0.04:gs=-0.01:bs=0.06:rm=0.02:bm=-0.02:rh=0.04:gh=0.01:bh=-0.05," +
		"unsharp=5:5:0.8:5:5:0.0,vignette=PI/4.5," +
		"chromashift=cbh=2:crh=-2,noise=alls=10:allf=t+u"
)

// NormalizeGrade 归一化调色档:空值/未知值一律按 film(默认开)。
//
// 与 project.StageParams.Sanitize 的"非法值按 film"同一口径 —— 那边管保存参数时,
// 这边管装载之后的最后一道(手改 JSON / 老项目缺键都从这里过)。两处不能互相调用
// (project 不能反向依赖 compose),所以是刻意保留的同口径双写,改一处必须改另一处。
func NormalizeGrade(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case GradeOff, "none", "0", "false":
		return GradeOff
	case GradeSoft, "light":
		return GradeSoft
	case GradeHeavy, "strong":
		return GradeHeavy
	case GradeFilm:
		return GradeFilm
	default:
		// 空串(老项目 json 里没有这个键)与任何不认识的值:默认开电影调色。
		// 为什么未知值也是 film 而不是 off:关掉调色是"显式表态",写错一个词就静默
		// 关掉整片调色,比默认开着更难发现(用户看到的是"这版怎么没调色")。
		return GradeFilm
	}
}

// GradeFilter 返回该档的 ffmpeg 滤镜串;off 返回空串(= 不加滤镜)。
func GradeFilter(name string) string {
	switch NormalizeGrade(name) {
	case GradeOff:
		return ""
	case GradeSoft:
		return gradeSoft
	case GradeHeavy:
		return gradeHeavy
	default:
		return gradeFilm
	}
}

// finalVideoFilters 终混那一步的视频滤镜串。
//
// 顺序是硬约束:**调色在前、字幕在后**。颗粒(noise)、暗角(vignette)与锐化
// (unsharp)如果落在字幕之后,会把字幕字面上叠一层噪点、再在字上压出暗角,
// 描边跟着糊掉(调色是给画面的,不该作用在图形层)。
//
// 返回 needEncode=false 表示这一路一个视频滤镜都没有 —— 只有这时才允许 -c:v copy
// (审计 M35:不为"什么都没做"再压一代 x264)。burnSubs 为真时调用方必须先写好
// final.ass(滤镜串里的相对文件名是给 libass 用的)。
func finalVideoFilters(grade string, burnSubs bool) (filters string, needEncode bool) {
	var parts []string
	if g := GradeFilter(grade); g != "" {
		parts = append(parts, g)
	}
	if burnSubs {
		parts = append(parts, "subtitles=filename=final.ass")
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, ","), true
}
