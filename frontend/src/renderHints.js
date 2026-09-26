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
// 默认档 film:用户抽帧反馈的"3D 截图感"里有相当一部分是**没有调子**造成的。
export const GRADE_OPTIONS = [
  { key: 'off',   name: '关闭(原片直出)', desc: '不做任何调色,终混只做字幕与响度归一。想自己后期调色的项目选它。' },
  { key: 'soft',  name: '轻调(soft)',      desc: '轻微对比曲线 + 很淡的暗角,不动色相;适合已经调好的素材,只补一点"镜头感"。' },
  { key: 'film',  name: '电影(film,默认)', desc: '冷暖分离(暗部偏青、亮部偏暖)+ 对比曲线 + 肤色归正 + 胶片颗粒 + 暗角。' },
  { key: 'heavy', name: '浓重(heavy)',     desc: '在 film 的基础上加强冷暖分离、颗粒与暗角 —— 观感更"电影",也更吃画面细节。' },
]

// GRADE_KEYS 合法档位(供归一化与闸门使用)。
export const GRADE_KEYS = GRADE_OPTIONS.map(g => g.key)

// DEFAULT_GRADE 默认调色档(与后端默认一致)。
export const DEFAULT_GRADE = 'film'

// gradeOf 归一化调色档:认不出的值(缺键 / 旧参数快照 / 手改 JSON)一律回默认档。
//
// 为什么不能"原样传":项目参数抽屉保存时是**整份快照回写**,留一个空字符串在下拉里
// 既显示成空白,又会在保存时把空值写进项目参数 —— 界面看起来像坏了,后端还得再兜一次。
export function gradeOf(params) {
  const v = String(params?.assemble?.grade || '').trim().toLowerCase()
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
