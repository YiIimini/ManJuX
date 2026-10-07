<script setup>
import { G, api, refreshProject } from '../store'
import { computed, reactive, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { GRADE_OPTIONS, gradeOf, seamResetOf, seamResetSummary, cloudSeedWarning, deliverEdgeOf, deliverSummary, subtitleMarginOf } from '../renderHints'

const visible = computed({
  get: () => !!G.paramsDrawer,
  set: (v) => { if (!v) G.paramsDrawer = null },
})
const stageId = computed(() => G.paramsDrawer)

const pj = computed(() => G.project)

const form = reactive({ script: {}, assets: {}, render: {}, qc: {}, assemble: {} })

watch(stageId, () => {
  const p = pj.value?.project?.params
  if (!p) return
  const copy = JSON.parse(JSON.stringify(p))
  // 逐层兜底:老项目快照里可能没有某个阶段(或某个新字段)。整体覆盖会让
  // 模板里的 v-model="form.qc.enabled" 这类表达式打到 undefined 上直接抛错,
  // 抽屉一开就弹「界面错误」。
  for (const k of Object.keys(form)) {
    form[k] = Object.assign({}, form[k] || {}, copy[k] || {})
  }
  // 多视角:缺省 = **关**（2026-09-27 起三视图细节板接手角度参考；转台图那条要跑一次 H3 转身视频，
  // 是资产阶段最贵的一步）。参考图去冗余:缺省 = 关(与后端 ViewsOn()/CompactRefsOn() 同一语义)
  //
  // ⚠️ 【2026-09-29 数据完整性审计 #3】`views` 原来写的是 `!== false`（**缺键 → true**），
  // 与后端 `ViewsOn() = Views != nil && *Views`（缺键 = 关）**正好反着** —— 而同文件
  // 上面那句注释、以及下面 `compact_refs` 的写法都是"缺省=关"。
  //
  // 后果不是"显示错了"那么轻：用户打开「资产定妆 ⚙」抽屉**哪怕只看一眼**再点保存，
  // 整份快照回写就把 `views: true` 落了盘 —— 他什么都没改，却把 2026-09-27 特意默认关掉的、
  // 资产阶段最贵的那一步（每个角色一次 H3 转身视频，数分钟/人）打开了。
  form.assets.views = form.assets.views === true
  form.assets.compact_refs = form.assets.compact_refs === true // 默认关(只喂转台图会让锁脸变弱)
  // 多视角生成方式:缺省 = auto(与后端 ViewsSourceOf() 的「认不出就当 auto」同一语义)。
  // 必须在这里兜底成字符串:老项目快照里没有这个键,留 undefined 会让下拉显示空白,
  // 用户以为"没选",保存后仍是 auto 但界面看起来像坏了。
  form.assets.views_source = ['edit', 'h3'].includes(form.assets.views_source) ? form.assets.views_source : 'auto'
  // 角色动漫化 / 音频净化:同样「未表态即开」——老项目参数快照里没有这两个键,
  // 若不在这里兜底成 true,抽屉一开就会把用户亲手关掉(false)的开关也显示成开。
  form.script.stylized_faces = form.script.stylized_faces !== false
  form.assemble.denoise = form.assemble.denoise !== false
  // 配乐两个字段（2026-09-28）：老项目快照里没有这两个键 ⇒ 兜底成空串。
  // 不兜底的话 v-model 会绑到 undefined 上，输入框里会显示成一串看不懂的东西。
  form.assemble.bgm = form.assemble.bgm || ''
  form.assemble.bgm_description = form.assemble.bgm_description || ''
  // 画面文字检测(清单 §3.1a):同样是「未表态即开」——后端 QCParams.TextDetectOn() 把缺键
  // 读成开,这里必须同步兜底成 true,否则存量项目打开抽屉会看到"关着",而实际在跑(或反之:
  // 看着是关、一保存就真的把它关掉了)。
  form.qc.enable_text_detect = form.qc.enable_text_detect !== false
  // 语音核对(2026-10-07,用户「配音存在乱配音」):同一套「未表态即开」语义(后端
  // QCParams.ASROn() 把缺键读成开)。不兜底的话,存量项目一开抽屉就显示成"关着"而实际在跑,
  // 甚至"看着是关、一保存就真的把它关掉了" —— 关掉它等于把这条链上唯一能看见
  // "音轨里念的是不是台词"的判据扔掉。
  form.qc.enable_asr = form.qc.enable_asr !== false
  // 转写模型档:老项目快照里没有这个键 ⇒ 兜底成空串(后端 ASRModelOr() 空串 = medium),
  // 不兜底的话下拉会显示空白,而保存是整份快照回写。
  form.qc.asr_model = form.qc.asr_model || ''
  // 定妆照比例自检(清单 §5.1):同一套「未表态即开」语义(后端 QCParams.ProportionCheckOn()
  // 把缺键读成开)。不在这里兜底,存量项目一开抽屉就会显示成"关着",而实际在跑;
  // 更糟的是"看着是关、一保存就真的把它关掉了"。它跑在资产阶段(每张新出定妆照一次视觉调用)。
  form.qc.enable_proportion_check = form.qc.enable_proportion_check !== false
  // 两阶段渲染条件缓存(render.cond_cache):同一套「未表态即开」语义，而它此前**漏了兜底**
  // （2026-09-29 数据完整性审计 #6）。后端 `CondCacheOn()` 把缺键读成**开**，
  // 而这里不兜底就显示成 undefined → 开关看着是"关"、实际在跑 ——
  // 正是上面那两条注释里写的同一个坑（"看着是关、其实在跑"）。它决定是否复用条件缓存 .pt，
  // 影响的是渲染耗时与磁盘占用。
  form.render.cond_cache = form.render.cond_cache !== false
  // 电影调色档(assemble.grade)与接缝重置间隔(render.seam_reset_every):
  // 这两个键是后加的,存量项目快照里没有 —— 不在这里兜底,下拉会显示空白、
  // 数字框会显示成空,而保存是**整份快照回写**,于是空值被写进项目参数,
  // 界面上看着像坏了(后端还得再兜一次)。归一化规则见 renderHints.js。
  form.assemble.grade = gradeOf(form)
  form.render.seam_reset_every = seamResetOf(form)
  // 交付分辨率与字幕下边距:同样是后加的键。这里必须归一化成数字 ——
  // 空值会被整份快照回写进项目参数,界面上看着像坏了(后端还得再兜一次)。
  form.assemble.deliver_width = deliverEdgeOf(form, 'deliver_width')
  form.assemble.deliver_height = deliverEdgeOf(form, 'deliver_height')
  form.assemble.subtitle_margin_v = subtitleMarginOf(form)
}, { immediate: true })

// 调色档说明:跟着当前选项走(选了才知道这一档到底做什么)
const gradeDesc = computed(() =>
  GRADE_OPTIONS.find(g => g.key === form.assemble.grade)?.desc || '')

// 接缝重置间隔的人话说明
const seamSummary = computed(() => seamResetSummary(form.render.seam_reset_every))

// 交付分辨率的人话说明(留 0 = 按画幅自动)
const deliverNote = computed(() => deliverSummary(form.assemble.deliver_width, form.assemble.deliver_height))

// 云端 + 固定 seed = 不可复现(乙9):条件与文案见 renderHints.cloudSeedWarning
const cloudSeedNote = computed(() => cloudSeedWarning(form.render.engine, form.render.seed))

const titles = {
  script: '剧本分镜 · 调优',
  assets: '资产定妆 · 调优',
  render: '渲染出片 · 调优',
  qc: '质检 · 调优',
  assemble: '合成成片 · 调优',
}

// 渲染风格预设(与后端 pipeline.StylePresets 对齐;可多选组合)
const STYLE_PRESETS = [
  { key: 'guofeng_cinematic', name: '国风电影级', sw: ['#2f5d5a', '#c9b382'] },
  { key: 'urban_xianxia', name: '都市仙侠霓虹', sw: ['#f97a1f', '#53d8ff'] },
  { key: 'guoman_2d',     name: '国漫二维动画', sw: ['#ff6b6b', '#ffd166'] },
  { key: 'live_action',   name: '真人实拍质感', sw: ['#93a0bd', '#edf1fa'] },
  { key: 'pixar_3d',      name: '3D 皮克斯',   sw: ['#3ddc84', '#53d8ff'] },
  { key: 'ink_wash',      name: '水墨国风',    sw: ['#c9d4e8', '#5a6580'] },
  { key: 'cyberpunk',     name: '赛博朋克',    sw: ['#b28bff', '#ff2e88'] },
  { key: 'ghibli',        name: '吉卜力手绘',  sw: ['#8affc1', '#ffd166'] },
  { key: 'hongkong',      name: '港片胶片',    sw: ['#ffb454', '#12b886'] },
  { key: 'film_noir',     name: '黑色电影',    sw: ['#f2f4f8', '#33333c'] },
  { key: 'claymation',    name: '黏土定格',    sw: ['#ff9ecf', '#ffd166'] },
]

// 显示用清单:以后端 /api/meta 为准 —— 后端新增风格会自动出现在这里(用默认色),
// 后端删掉的风格也随之消失,不再需要两侧手工同步。颜色属纯展示信息,仍由前端决定。
// 后端未就绪(metaStyles 为空)时回退内置清单,保证抽屉始终可用。
const styleList = computed(() => {
  const meta = G.metaStyles || []
  if (!meta.length) return STYLE_PRESETS
  const byKey = new Map(STYLE_PRESETS.map(s => [s.key, s]))
  return meta.map(m => {
    const local = byKey.get(m.key)
    return { key: m.key, name: m.name || local?.name || m.key, sw: local?.sw || ['#8a93a8', '#c9d4e8'] }
  })
})

const hasStyle = (k) => (form.script.styles || []).includes(k)
function toggleStyle(k) {
  if (!Array.isArray(form.script.styles)) form.script.styles = []
  const i = form.script.styles.indexOf(k)
  if (i >= 0) form.script.styles.splice(i, 1)
  else form.script.styles.push(k)
}

async function save() {
  try {
    const saved = await api.putParams(pj.value.project.id, form)
    if (pj.value.project) pj.value.project.params = saved
    ElMessage.success('阶段参数已保存(仅本项目生效)')
    G.paramsDrawer = null
    refreshProject()
  } catch (e) { ElMessage.error(e.message) }
}
</script>

<template>
  <el-drawer v-model="visible" :title="titles[stageId] || '参数调优'" size="400px" append-to-body>
    <div v-if="stageId === 'script'" class="flex flex-col gap-4">
      <el-form label-position="top">
        <el-form-item label="目标镜头数(0=按内容自动,最多 120)">
          <el-input-number v-model="form.script.max_shots" :min="0" :max="120" style="width:100%" />
        </el-form-item>
        <el-form-item label="目标总时长(秒,0=按镜头数自动)">
          <el-input-number v-model="form.script.total_seconds" :min="0" :max="300" :step="6" style="width:100%" />
        </el-form-item>
      </el-form>
      <div>
        <div class="text-xs mb-2">
          <b>渲染视频风格</b>
          <span style="color:var(--mx-text3)">(可多选组合,决定全片视觉基调;已选 {{ (form.script.styles || []).length }} 项)</span>
        </div>
        <div class="style-chip-grid">
          <button v-for="st in styleList" :key="st.key" class="style-chip" :class="{ on: hasStyle(st.key) }" @click="toggleStyle(st.key)">
            <span class="sw"><i :style="{ background: st.sw[0] }"></i><i :style="{ background: st.sw[1] }"></i></span>
            {{ st.name }}
          </button>
        </div>
      </div>
      <el-form label-position="top">
        <el-form-item label="风格附加要求(自由文本,与上方预设叠加)">
          <el-input v-model="form.script.style_hint" type="textarea" :rows="3"
            placeholder="例如:偏冷色调、雨夜氛围、手持镜头感…" />
        </el-form-item>
        <el-form-item label="角色动漫化(非真人脸 + 哑光画风,避免影射真实人物/侵权)">
          <el-switch v-model="form.script.stylized_faces" />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            **未指定风格时的兜底画风**:方案里没有风格设定时,按动画化渲染(手绘线稿 + 赛璐璐平涂 + 哑光肤色),
            压掉"油光/汗湿反光"的油腻感。**风格由上游(小说技能端)在方案里指定,指定后本开关不参与画风** ——
            平台只负责照它渲染并保证全片一致。要拍真人剧就在方案里写真人/写实风格。
          </div>
        </el-form-item>
        <el-form-item label="Q 版形象(定妆时同步生成角色 Q 版;内心活动镜头的表达素材)">
          <el-switch v-model="form.script.chibi" />
        </el-form-item>
      </el-form>
    </div>

    <div v-else-if="stageId === 'assets'" class="flex flex-col gap-4">
      <el-form label-position="top">
        <el-form-item label="参考图来源">
          <el-select v-model="form.assets.source">
            <el-option value="krea2" label="本地模型自动生成(推荐,需本地 ComfyUI)" />
            <el-option value="upload" label="手动上传(锁脸最稳)" />
            <el-option value="skip" label="跳过(全部镜头纯文生 t2va)" />
          </el-select>
        </el-form-item>
        <el-form-item label="角色多视角转台图(默认关:角度参考已由三视图板接手;开了才生成)">
          <el-switch v-model="form.assets.views" />
        </el-form-item>
        <el-form-item label="多视角生成方式">
          <el-select v-model="form.assets.views_source">
            <el-option value="auto" label="自动(编辑模型就绪就用图像编辑,否则 H3 转身;推荐)" />
            <el-option value="edit" label="图像编辑(Qwen-Image-Edit-2511:只转角度,不改画风)" />
            <el-option value="h3" label="H3 转身视频(旧路径:会重新设计角色,身份校验多半拒绝)" />
          </el-select>
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            图像编辑拿定妆照本体改一个角度,身份/配色/画风按像素保住,每张几秒;它才是"转台图与定妆照是同一人"的那条路。
            权重没下齐时自动退回 H3 转身视频(每个角色数分钟,且可能被身份校验拒绝)。
          </div>
        </el-form-item>
        <el-form-item label="参考图去冗余(只喂转台图省一张;注意:拼合图缩放后脸部细节变弱,锁脸会变差,默认关)">
          <el-switch v-model="form.assets.compact_refs" />
        </el-form-item>
      </el-form>
      <el-alert type="info" :closable="false"
        title="本地模型自动生成:角色半身（按项目画幅，出厂 768×1344）/ 场景按项目画幅 / 道具 1:1 白底单件 / 人形角色另出三视图细节板 16:9·2K;已有资产自动复用,可上传替换。用的是设置里配置的图像模型(默认 Qwen-Image-2.1)。" />
      <el-alert type="warning" :closable="false"
        title="多视角转台图**默认关**(2026-09-27)：角度参考已由三视图细节板接手(正/侧/背三个全身 + 6 个头部细节，16:9·2K)。打开它会给每个角色多跑一次 H3 转身视频(每个数分钟)或逐张编辑。" />
    </div>

    <div v-else-if="stageId === 'render'" class="flex flex-col gap-4">
      <el-form label-position="top">
        <el-form-item label="渲染引擎">
          <el-select v-model="form.render.engine">
            <el-option value="auto" label="自动(本地优先,不可用切云端)" />
            <el-option value="comfy" label="本地 ComfyUI(H3-Base 768p)" />
            <el-option value="api" label="MiniMax 云端 API(按量计费)" />
          </el-select>
        </el-form-item>
        <el-form-item label="分辨率(32 整除;短边 ≤768)">
          <div class="flex gap-2 w-full">
            <el-input-number v-model="form.render.width" :step="32" :min="32" :max="1344" style="flex:1" />
            <el-input-number v-model="form.render.height" :step="32" :min="32" :max="1344" style="flex:1" />
          </div>
          <div class="flex gap-2 mt-2 w-full">
            <el-button size="small" @click="form.render.width = 768; form.render.height = 1344">9:16 竖屏</el-button>
            <el-button size="small" @click="form.render.width = 1344; form.render.height = 768">16:9 横屏</el-button>
            <el-button size="small" @click="form.render.width = 768; form.render.height = 768">1:1 方形</el-button>
          </div>
        </el-form-item>
        <el-form-item label="采样步数(turbo 4-8 / 基线 20)">
          <el-input-number v-model="form.render.steps" :min="1" :max="30" style="width:100%" />
        </el-form-item>
        <el-form-item label="Seed(0=每镜随机;固定值=全片画风一致)">
          <el-input-number v-model="form.render.seed" :min="0" :max="2147483647" style="width:100%" />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            本地 ComfyUI 下同一提示词 + 同一 seed 能复现同一镜;云端引擎不吃这个参数(见下)。
          </div>
        </el-form-item>
        <el-alert v-if="cloudSeedNote" type="warning" :closable="false" :title="cloudSeedNote" />
        <el-form-item label="加速档">
          <el-select v-model="form.render.accel">
            <el-option value="" label="Turbo 均衡（默认，官方 4 步蒸馏）" />
            <el-option value="quality" label="质量优先（Acc-8Step，成片推荐）" />
            <el-option value="refine" label="精修（不挂蒸馏 LoRA＋完整步数；慢 2–3×，画质基线最高）" />
            <el-option value="express" label="极速预览/抽卡（TaoMate 3 步，约 2×；参考图会压过提示词，不要用于交付）" />
          </el-select>
        </el-form-item>
        <el-form-item label="两阶段渲染(条件缓存,换 seed 不重编码)">
          <el-switch v-model="form.render.cond_cache" />
        </el-form-item>
        <el-form-item label="镜间连续接缝(MotionContext 22帧;仅本地引擎;同场景镜头更连贯)">
          <el-switch v-model="form.render.seam" />
        </el-form-item>
        <el-form-item label="接缝重置间隔(0/1=每场重置;>1=每 N 镜重置)">
          <el-input-number v-model="form.render.seam_reset_every" :min="0" :max="60" style="width:100%" />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            换场 / 换人会自动断开接缝(钉进下一镜的那 22 帧本来就是上一镜的内容,接上去画面会串);
            这里再给一条兜底:接缝是逐链累积误差的,链越长漂移越明显,填 N>1 就每 N 镜强制重置一次。
          </div>
          <div class="text-xs mt-1" style="color:var(--mx-amber-hi)">{{ seamSummary }}</div>
        </el-form-item>
      </el-form>
    </div>

    <div v-else-if="stageId === 'qc'" class="flex flex-col gap-4">
      <el-form label-position="top">
        <el-form-item label="启用质检">
          <el-switch v-model="form.qc.enabled" />
        </el-form-item>
        <el-form-item label="VLM 智能审片(抽帧判分+重渲建议,需视觉模型)">
          <el-switch v-model="form.qc.enable_vlm" />
        </el-form-item>
        <el-form-item label="画面文字检测(抽帧查画面里有没有被烧进去的字;命中即告警)">
          <el-switch v-model="form.qc.enable_text_detect" />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            默认开。在镜头原始产物上做(合成前),查有没有被模型画成字形的字幕/招牌/UI;命中逐镜告警,
            整集命中率超 10% 时升级为显式告警。每镜一次视觉调用(与 VLM 审片共用设置页的视觉模型),
            未配置视觉模型时如实标注「未配置视觉模型」,不当作通过。关掉它不影响其它质检项。
          </div>
        </el-form-item>
        <el-form-item label="语音核对(ASR:转写每镜音轨,与剧本台词逐句对账)">
          <el-switch v-model="form.qc.enable_asr" />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            默认开。渲染完成后用本机 ASR(faster-whisper,D:\Ai\Tools\ASR)把每镜音轨转写一遍,再与剧本台词
            逐句对账 —— 这是这条链上唯一能看见「念的是不是那句台词」的判据(字幕是平台后期烧的,跟音轨是
            两回事)。当场能抓三类问题:同一句念了多遍、凭空多出剧本里没有的话、整句漏念。只在**镜级原始产物**
            上做(合成前),成本约每镜 10~15 秒 CPU(不与 ComfyUI 抢显存)。命中只逐镜告警、不拦批次、不自动重渲;
            本机没装 ASR 时如实播报"跳过"并在报告里标注「未验」,不当作通过。关掉它不留任何痕迹。
          </div>
        </el-form-item>
        <el-form-item label="ASR 转写模型档">
          <el-select v-model="form.qc.asr_model" style="width:220px">
            <el-option label="medium(默认,推荐)" value="medium" />
            <el-option label="small(更快更糙)" value="small" />
            <el-option label="base(最快,只做粗筛)" value="base" />
            <el-option label="large-v3(最准更慢)" value="large-v3" />
          </el-select>
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            只影响转写的准确度与耗时(ASR 固定走 CPU,与渲染显存无关)。留空 = medium。
          </div>
        </el-form-item>
        <el-form-item label="定妆照比例自检(出图后核对三庭/眼距/头身比,明显偏离即告警)">
          <el-switch v-model="form.qc.enable_proportion_check" />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            默认开。在「资产定妆」阶段,每张新生成的定妆照做一次视觉核对(与 VLM 审片共用设置页的视觉模型),
            口径取脚本里技能侧给的比例锚点(眼线在头部中线 / 眼距＝一个眼宽 / 鼻长约脸长 ⅓ / 下巴宽约颧骨 ⅔ /
            身高七个半头…)—— 平台只负责测与报,不另定一套比例标准。明显偏离时点名是哪一项、期望多少、量到多少,
            并建议重出该卡(只告警,不拦产线、不自动重渲);半身幅里看不见的项如实标注"不可见"。
            未配置视觉模型时事件流里如实写「未做比例自检:无可用能力」,不假装通过。关掉它不留任何痕迹。
          </div>
        </el-form-item>
        <el-form-item label="暗像素失败阈值(0-1)">
          <el-slider v-model="form.qc.dark_ratio_fail" :min="0.3" :max="0.95" :step="0.05" show-input />
        </el-form-item>
        <el-form-item label="暗像素警告阈值">
          <el-slider v-model="form.qc.dark_ratio_warn" :min="0.2" :max="0.9" :step="0.05" show-input />
        </el-form-item>
        <el-form-item label="单镜时长区间(秒)">
          <div class="flex gap-2 w-full">
            <el-input-number v-model="form.qc.min_duration" :min="1" :max="10" :step="0.5" style="flex:1" />
            <el-input-number v-model="form.qc.max_duration" :min="5" :max="60" :step="1" style="flex:1" />
          </div>
        </el-form-item>
      </el-form>
    </div>

    <div v-else-if="stageId === 'assemble'" class="flex flex-col gap-4">
      <el-form label-position="top">
        <!-- 配乐（2026-09-28）：**优先级必须写在界面上** —— 否则用户填了需求，不知道库里已有同需求
             会直接复用、也不知道填了文件就压过一切（后端的解析顺序是：文件 ＞ 库内同需求 ＞ 生成）。 -->
        <el-form-item label="配乐需求（自然语言：风格/情绪/乐器/节奏）">
          <el-input
            v-model="form.assemble.bgm_description"
            type="textarea"
            :rows="2"
            placeholder="例：低沉竹笛与弦乐，压抑，慢速，碑下独白段落"
          />
          <div class="text-xs opacity-60 mt-1">
            留空 = 不铺配乐。填了就先去资产库找同需求的现成配乐；没有就自动生成一段纯器乐并入库，
            下次同样的需求（换个标点写法也算）直接复用。
          </div>
        </el-form-item>
        <el-form-item label="配乐文件（可选；填了就压过上面那条需求）">
          <el-input v-model="form.assemble.bgm" placeholder="绝对路径，或相对本项目目录的路径，如 bgm.mp3" />
          <div class="text-xs opacity-60 mt-1">
            手上有曲子时填这里。平台负责把它铺成一条连续的 BGM，并在人声下自动闪避。
          </div>
        </el-form-item>
        <el-form-item label="转场">
          <el-select v-model="form.assemble.transition">
            <el-option value="cut" label="硬切(最稳)" />
            <el-option value="fade" label="闪黑叠化(默认 0.5s,可调)" />
            <el-option value="dissolve" label="交叉叠化(默认 0.5s,可调)" />
          </el-select>
        </el-form-item>
        <el-form-item label="转场时长(秒;0 = 用设置页的「转场时长」)">
          <el-input-number v-model="form.assemble.fade_sec" :min="0" :max="3" :step="0.1" :precision="1" style="width:100%" />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            两镜画面重叠的时长,成片总长按此扣除 (n-1)×时长。留 0 表示跟随设置页的统一值。
          </div>
        </el-form-item>
        <el-form-item label="烧录字幕(台词金色/旁白白色)">
          <el-switch v-model="form.assemble.burn_subtitles" />
        </el-form-item>
        <el-form-item label="目标响度(LUFS)">
          <el-slider v-model="form.assemble.loudness" :min="-30" :max="-6" :step="1" show-input />
        </el-form-item>
        <el-form-item label="峰值上限(loudnorm 的真峰值 TP;0.92 ≈ -0.7 dBTP)">
          <el-slider v-model="form.assemble.peak" :min="0.5" :max="1" :step="0.01" show-input />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            越小越安全(不易在手机扬声器上削顶),越大越响。默认 0.92。
          </div>
        </el-form-item>
        <el-form-item label="音频净化(高通去隆隆声 + 频谱降噪去底噪/嘶声)">
          <el-switch v-model="form.assemble.denoise" />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            默认开。成片响度按两遍法归一(先测量再线性增益),不会逐帧抽气;镜间音频切点会自动落在静音上,不再切掉台词尾巴。
          </div>
        </el-form-item>
        <el-form-item label="电影调色档(终混调色:冷暖分离 + 颗粒 + 暗角)">
          <el-select v-model="form.assemble.grade">
            <el-option v-for="g in GRADE_OPTIONS" :key="g.key" :value="g.key" :label="g.name" />
          </el-select>
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            {{ gradeDesc }}
          </div>
        </el-form-item>
        <el-form-item label="字幕字号(屏高 %)">
          <el-slider v-model="form.assemble.subtitle_size" :min="3" :max="9" :step="1" show-input />
        </el-form-item>
        <el-form-item label="字幕下边距(屏高 %;避开平台底部 UI 安全区)">
          <el-slider v-model="form.assemble.subtitle_margin_v" :min="5" :max="30" :step="1" show-input />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            默认 15% —— 抖音/红果在画面底部盖着 UI(进度条、账号条),创作者侧口径是字幕要避开底部约 15% 的区域。
            按**交付高度**算:1080×1920 交付时 15% = 288px。
          </div>
        </el-form-item>
        <el-form-item label="交付分辨率(成片最终画幅;留 0 = 按画幅自动)">
          <div class="flex gap-2 items-center w-full">
            <el-input-number v-model="form.assemble.deliver_width" :min="0" :max="8192" :step="2" style="flex:1" />
            <span style="color:var(--mx-text3)">×</span>
            <el-input-number v-model="form.assemble.deliver_height" :min="0" :max="8192" :step="2" style="flex:1" />
          </div>
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            {{ deliverNote }}
          </div>
        </el-form-item>
        <el-form-item label="云端 2K 定稿(合成后自动 regeneration 升 2K,消耗云额度)">
          <el-switch v-model="form.assemble.upgrade_2k" />
        </el-form-item>
      </el-form>
    </div>

    <template #footer>
      <el-button @click="G.paramsDrawer = null">取消</el-button>
      <el-button type="primary" @click="save">保存(项目级)</el-button>
    </template>
  </el-drawer>
</template>
