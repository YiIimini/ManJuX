// 渲染相关参数的界面口径(**单一真源**)。
//
// 为什么单独一个模块:同一条提示要在两个及以上的地方出现(项目参数抽屉、单镜微调弹窗),
// 两处各写一份文案必然漂移 —— 而"这个引擎做不到这件事"的说法一旦不一致,用户会以为
// 遇到的是两个不同的问题。
//
// 文案口径与后端对齐:`internal/pipeline/caps_params.go` 的 unsupportedParamNote
// 说的是「本次引擎不支持这些项目参数,已自动跳过:…;需要它们请在项目参数里切到
// 本地 ComfyUI 引擎」。前端这里说的是同一件事的**事前**版本(提交前就能看见),
// 因此两句都必须落在"云端不支持 / 不可复现 / 切本地 ComfyUI"这三个点上
// —— 有闸门钉住(TestFrontendCloudSeedNote)。

// CLOUD_ENGINES 云端引擎的注册名。
//
// "api" 是 engine.Registry 里的名字(h3api.Engine.Name());"h3api" 是同一引擎在
// 历史配置/参数文件里出现过的写法,一起认下来 —— 认漏一个,提示就不会弹。
export const CLOUD_ENGINES = ['api', 'h3api']

// GRADE_OPTIONS 合成阶段的电影调色档。
//
// key 是**后端词汇表**(管线按它选滤镜链),前端不得自行增删;显示名与说明属展示信息,
// 放在这里是为了让"下拉里到底有哪几档"只有一份答案(项目参数抽屉直接消费它)。
// 默认档 auto(按风格自动):调色默认开着(用户抽帧反馈的"3D 截图感"里相当一部分是
// **没有调子**造成的),但档位跟着项目风格串走 —— 动画电影类风格(film 的压饱和 + 暗角
// + 颗粒叠在动画赛璐璐上偏重)默认降一档用 soft,其余用 film;口径见后端
// project.EffectiveGrade。选了具体档位就照选的,不再自动。
export const GRADE_OPTIONS = [
  { key: 'auto',  name: '自动(按风格)', desc: '按项目风格串自动选档:动画电影类风格(ANIMATED-FEATURE / donghua / anime)用 soft,其余用 film。合成时会在日志里说明用了哪一档,随时可改成具体档位。' },
  { key: 'off',   name: '关闭(原片直出)', desc: '不做任何调色,终混只做字幕与响度归一。想自己后期调色的项目选它。' },
  { key: 'soft',  name: '轻调(soft)',      desc: '轻微对比曲线 + 很淡的锐化,不动色相、无暗角、无颗粒;适合已经调好的素材,只补一点"镜头感"。' },
  { key: 'film',  name: '电影(film)',     desc: '冷暖分离(暗部偏青、亮部偏暖)+ 对比曲线 + 轻锐化 + 暗角。**不含颗粒**:实测颗粒把码率推到 2.5 倍,而平台二次编码会先把它抹掉 —— 要胶片颗粒请选"浓重"。' },
  { key: 'heavy', name: '浓重(heavy)',     desc: 'film 之上加胶片颗粒与 2px 色差(色边),夜戏/情绪戏用。观感更"电影",也更吃画面细节与码率。' },
]

// GRADE_KEYS 合法档位(供归一化与闸门使用)。
export const GRADE_KEYS = GRADE_OPTIONS.map(g => g.key)

// AUTO_GRADE 未表态档(与后端 project.GradeAuto 同一语义):缺键/空串 = 按风格自动。
//
// 为什么"未表态"必须能表达出来:参数抽屉保存时是**整份快照回写**,而缺键若兜成显式
// film,那么开一次抽屉(哪怕只是改字幕字号)再保存,项目就再也不是"未表态" ——
// 档位被悄悄钉死成 film,按风格自动降档(动画电影 → soft)静默失效,而用户什么都没改。
export const AUTO_GRADE = 'auto'

// DEFAULT_GRADE 认不出的脏值的兜底档(与后端 Sanitize 的"认不出落回 film"一致)。
export const DEFAULT_GRADE = 'film'

// gradeOf 归一化调色档:缺键/空串 = auto(未表态);认不出的值(手改 JSON)回默认档。
//
// 为什么不能"原样传":项目参数抽屉保存时是**整份快照回写**,留一个空字符串在下拉里
// 既显示成空白,又会在保存时把空值写进项目参数 —— 界面看起来像坏了,后端还得再兜一次。
export function gradeOf(params) {
  const v = String(params?.assemble?.grade || '').trim().toLowerCase()
  if (v === '') return AUTO_GRADE
  return GRADE_KEYS.includes(v) ? v : DEFAULT_GRADE
}

// seamResetOf 归一化"接缝重置间隔":0/1 = 每场重置;>1 = 每 N 镜再强制重置一次。
//
// 负数/非数字都是脏数据,按 0 处理(0 = 只在换场/换人时断开,最保守的渲染选择)。
export function seamResetOf(params) {
  const n = Number(params?.render?.seam_reset_every)
  if (!Number.isFinite(n) || n < 0) return 0
  return Math.floor(n)
}

// seamResetSummary 把重置间隔说成人话(控件下方的那行说明)。
export function seamResetSummary(v) {
  const n = Number(v)
  if (!Number.isFinite(n) || n <= 1) return '每场重置:换场 / 换人时自动断开接缝。'
  return `每场重置,并每 ${Math.floor(n)} 镜再强制重置一次(接缝是逐链累积误差的,链越长漂移越明显)。`
}

// ---------- 交付分辨率(assemble.deliver_width/height) ----------
//
// 存在的理由:渲染尺寸受 H3 的短边 768 上限约束,而交付口径是 1080×1920 ——
// 两者在 2026-09-27 之后解耦(后端 compose/deliver.go)。这里只负责**界面的归一化**:
// 参数抽屉保存时是整份快照回写,留一个空/NaN 下去就会把坏值写进项目参数。

// DELIVER_EDGE 交付边长的界面上下限(与后端 project.DeliverMinEdge/MaxEdge 同口径)。
export const DELIVER_MIN_EDGE = 64
export const DELIVER_MAX_EDGE = 8192

// deliverEdgeOf 归一化单个交付边长:0 = 未表态(按画幅自动);非数字/负数一律当 0。
//
// 为什么不在这里替用户填 1080:交付档是"按画幅自动"的(竖屏 1080×1920 / 横屏
// 1920×1080),这个判断只有一个实现点(后端 compose.deliverSize)。前端替它算一遍,
// 就等于把"按画幅算"这条规则写了两份 —— 项目吃过同口径双写的亏。
export function deliverEdgeOf(params, key) {
  const v = Number(params?.assemble?.[key])
  if (!Number.isFinite(v) || v <= 0) return 0
  return Math.min(DELIVER_MAX_EDGE, Math.max(DELIVER_MIN_EDGE, Math.floor(v)))
}

// deliverSummary 把那对数字说成人话(控件下方那行说明)。
export function deliverSummary(w, h) {
  const auto = !w || !h
  if (auto) {
    return '留 0 = 按画幅自动:竖屏交付 1080×1920、横屏交付 1920×1080(H3 只能渲 768 短边,'
      + '放大在终混时由本机完成,并作用在调色与字幕之前)。想按渲染尺寸原样交付,就把渲染的宽高填进来。'
  }
  return `成片最终画幅 ${w}×${h}:终混时用 lanczos 放大/缩小到该尺寸,调色与字幕都按它算。`
    + '画幅与渲染比例不一致时会等比缩放并补黑边(不变形,但成片会带边)。'
}

// ---------- 字幕下边距(assemble.subtitle_margin_v) ----------

// SUBTITLE_MARGIN_OPTIONS 字幕下边距的取值口径(与后端 project.Sanitize 的 15/30 同口径)。
export const SUBTITLE_MARGIN_DEFAULT = 15
export const SUBTITLE_MARGIN_MAX = 30

// subtitleMarginOf 归一化字幕下边距(屏高百分比):未表态 = 默认 15%。
//
// 为什么默认 15%:平台在画面底部盖着 UI(进度条/账号条),创作者侧口径是字幕要避开
// 底部约 15%。旧实现写死 8%,恰好压在危险区里(手机上会被遮住下半截)。
export function subtitleMarginOf(params) {
  const v = Number(params?.assemble?.subtitle_margin_v)
  if (!Number.isFinite(v) || v <= 0) return SUBTITLE_MARGIN_DEFAULT
  return Math.min(SUBTITLE_MARGIN_MAX, Math.round(v))
}

// cloudSeedWarning 云端引擎 + 固定 seed 的不可复现提示(乙9)。
//
// 依据:云端请求体里没有 seed 字段(h3api 组装请求时只发 model/content/duration/
// resolution/ratio —— 见 internal/engine/h3api/h3api.go)。也就是说这个组合下
// "全项目固定种子"和单镜 seed 都**不产生任何效果**,而产物仍然照常出来:
// 用户会以为同一 seed 能复现,排查时往错方向找(使用手册承诺的"同 seed 大概率复现"
// 只对本地成立)。
//
// 触发条件刻意收窄成"引擎明确是云端 **且** seed 非 0":seed=0 本来就是每镜随机,
// 谈不上"不可复现";引擎是 auto 时按项目配置走,不该在这里替它下结论。
export function cloudSeedWarning(engine, seed) {
  const eng = String(engine || '').trim().toLowerCase()
  if (!CLOUD_ENGINES.includes(eng)) return ''
  const s = Number(seed) || 0
  if (s === 0) return ''
  return '云端引擎不支持 seed:同一提示词、同一 seed 也会出不同样本,产物**不可复现**。'
    + '要复现请把引擎切到本地 ComfyUI(H3-Base);切不了就把 seed 改成 0(每镜随机),别指望它复现。'
}
