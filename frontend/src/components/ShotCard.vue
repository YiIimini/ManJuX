<script setup>
import { G, api, refreshProject } from '../store'
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { confirmGpuContention } from '../pipelineBusy'

const props = defineProps({ shot: Object, manifestItem: Object })

const pj = computed(() => G.project)
const running = computed(() => pj.value?.project?.running)
const idx = computed(() => (props.shot?.id ?? 1) - 1)

// ---------- 镜头增强:局部重绘 / 音频驱动 ----------
// 两者都产出**独立文件**,不覆盖原镜头产物 —— 比对满意后再决定是否采用。
const enhanceBusy = ref(false)

// 局部重绘:用 SAM3 跟踪目标,只重生成该区域,其余画面逐帧保留(省一个数量级成本)
//
// **必须问用户"要重绘什么"**(2026-09-24 审计):SAM3 是开放词表跟踪器,不告诉它找什么
// 节点就直接 raise ValueError —— 这张图从建立起两个必需输入一个都没传,于是"局部重绘"
// 一直是个有按钮、必然失败的死功能。现在目标由用户给(英文名词短语,SAM3 的词表是英文)。
async function maskFix() {
  const mf = props.manifestItem
  if (!mf || mf.status !== 'done') { ElMessage.info('镜头尚未渲染完成'); return }
  const guess = shotCharHint()
  let target
  try {
    const r = await ElMessageBox.prompt(
      `要重绘画面里的**哪一块**?填英文名词短语(如 the young man / the bronze sparrow / the hands / the lantern)。\n\n` +
      `SAM3 会跨帧跟踪这个目标,只重新生成它所在的区域,其余画面逐帧保留 —— ` +
      `适合"只有手部/道具/人脸崩坏"的情形。结果作为独立文件产出,原镜头不变。`,
      `局部重绘 · 镜头 ${props.shot.id}`,
      { confirmButtonText: '开始重绘', cancelButtonText: '取消', inputValue: guess, inputPlaceholder: 'the young man' })
    target = (r.value || '').trim()
  } catch { return }
  if (!target) { ElMessage.warning('未填写要重绘的目标'); return }
  // **可选：改成什么**。后端一直收 `prompt`（修正后的画面描述），而前端此前不传 ——
  // 于是局部重绘永远拿"当初画坏的那句话"重绘：能限定区域，却改不了内容，功能只有一半。
  // 留空＝沿用原提示词（与从前行为一致），想改内容时才填。
  let fixPrompt = ''
  try {
    const r2 = await ElMessageBox.prompt(
      `要让这一块**变成什么样**?填英文描述。\n\n留空＝沿用原提示词(只重绘这一块,不改内容)。`,
      '修正后的画面描述(可选)',
      { confirmButtonText: '开始重绘', cancelButtonText: '取消',
        inputPlaceholder: 'a bronze sparrow orb with a warm inner glow', inputValue: '' })
    fixPrompt = (r2.value || '').trim()
  } catch { return }
  // 管线闲不闲与"重绘什么"无关,所以排在问完目标之后、提交之前 ——
  // 用户已经想清楚要改哪儿了,这时告知代价才有意义(而不是先拦一道再问)。
  if (!(await confirmGpuContention(`局部重绘 · 镜头 ${props.shot.id}`))) return
  enhanceBusy.value = true
  try {
    const d = await api.maskFix(pj.value.project.id, props.shot.id, target, fixPrompt)
    ElMessage.success(`局部重绘完成(目标:${target}${fixPrompt ? '·已改用修正描述' : ''}):` + (d.file || ''))
    refreshProject()
  } catch (e) { ElMessage.error('局部重绘失败:' + e.message) }
  enhanceBusy.value = false
}

// shotCharHint 该镜出场角色的英文提示词里挑一个名词短语当默认值:
// 角色卡的 image_prompt 是英文,取开头那段身份描述的前几个词即可(SAM3 只认名词短语)。
// 取不到就给中性默认值,由用户改 —— 不猜具体目标,猜错会把重绘打到错误的区域。
function shotCharHint() {
  const chars = (pj.value.project?.plan?.characters) || []
  const ids = props.shot?.characters || []
  for (const id of ids) {
    const c = chars.find(x => x.id === id)
    if (!c) continue
    if (c.kind === 'object' || c.kind === 'spirit_pet' || c.kind === 'beast') return c.name || 'the object'
    const m = (c.image_prompt || '').match(/^(A|An|The)\s+[\w-]+(\s+[\w-]+){0,3}/i)
    if (m) return m[0].toLowerCase()
  }
  return 'the character'
}

// 音频驱动:给一个配音文件路径,重渲时人物口型与动作节奏会跟随该音频
// (H3 Ref2V 的原生音频条件,用于精确台词路线)
async function audioDrive() {
  const mf = props.manifestItem
  if (!mf || mf.status !== 'done') { ElMessage.info('镜头尚未渲染完成'); return }
  let input
  try {
    const r = await ElMessageBox.prompt(
      '填入配音文件的**完整路径**(如 C:\\配音\\shot3.wav)。\n\n重渲后人物口型与动作节奏会跟随这段音频。结果作为独立文件产出,原镜头不变。',
      `音频驱动 · 镜头 ${props.shot.id}`,
      { confirmButtonText: '开始重渲', cancelButtonText: '取消', inputPlaceholder: 'C:\\path\\to\\voice.wav' })
    input = (r.value || '').trim()
  } catch { return }
  if (!input) { ElMessage.warning('未填写配音路径'); return }
  if (!(await confirmGpuContention(`音频驱动 · 镜头 ${props.shot.id}`))) return
  enhanceBusy.value = true
  try {
    const d = await api.audioDrive(pj.value.project.id, props.shot.id, input)
    ElMessage.success('音频驱动完成:' + (d.file || ''))
    refreshProject()
  } catch (e) { ElMessage.error('音频驱动失败:' + e.message) }
  enhanceBusy.value = false
}

// 已渲染才能进全屏预览;未渲染/失败/渲染中不打开,绝不跳到其它镜头的片子
function openViewer() {
  const mf = props.manifestItem
  if (!mf || mf.status !== 'done' || !mf.file) {
    ElMessage.info(`镜头 ${props.shot.id}${mf?.status === 'rendering' ? ' 正在渲染中' : mf?.status === 'failed' ? ' 渲染失败' : ' 尚未渲染'},渲染完成即可预览`)
    return
  }
  const shots = pj.value?.plan?.shots || []
  const mfs = pj.value?.manifest || {}
  const items = shots
    .filter(s => mfs[String(s.id)]?.status === 'done' && mfs[String(s.id)]?.file)
    .map(s => {
      const m = mfs[String(s.id)]
      return {
        type: 'video', src: api.fileURL(pj.value.project.id, m.file),
        title: `镜头 ${s.id} · ${s.shot_size}`,
        meta: `${s.mode.toUpperCase()} · ${s.duration}s · ${m.engine === 'comfy' ? '本地' : '云端'}`,
        foot: [s.action, s.narration, ...(s.dialogue || []).map(d => `${d.speaker}:${d.text}`)].filter(Boolean).join('\n'),
        kindLabel: 'SHOT',
      }
    })
  if (!items.length) return
  G.viewer = { items, index: items.findIndex(x => x.title.startsWith(`镜头 ${props.shot.id} `)) }
}

// 删除单镜:该镜方案与已渲染产物一并移除,成片作废(需重新合成),其它镜头不受影响
async function removeShot() {
  if (running.value) return
  try {
    await ElMessageBox.confirm(
      `删除镜头 ${props.shot.id}?该镜的方案与已渲染产物将一并移除,成片需重新合成;其它镜头不受影响。`, '删除镜头',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  try {
    await api.deleteShot(pj.value.project.id, props.shot.id)
    ElMessage.success(`镜头 ${props.shot.id} 已删除`)
    refreshProject()
  } catch (e) { ElMessage.error(e.message) }
}

async function rerender(cmd) {
  if (cmd === 'api2k') {
    try { await api.upgrade2k(pj.value.project.id, props.shot.id); ElMessage.success(`镜头 ${props.shot.id} 已提交云端 2K 定稿`) }
    catch (e) { ElMessage.error(e.message) }
    return
  }
  try {
    await api.run(pj.value.project.id, 'render', [props.shot.id], cmd)
    ElMessage.success(`镜头 ${props.shot.id} 已提交重渲${cmd ? '(' + (cmd === 'api' ? '云端' : '本地') + ')' : ''}`)
    refreshProject()
  } catch (e) { ElMessage.error(e.message) }
}

// 2× 放大后处理:放大**已完成的画面**,不是重新生成 —— 音轨沿用源片、时长不变。
//
// 两条链路的前置条件不同,所以分别确认:帧级(esrgan)只要一个 upscale_models 权重;
// LTX 隐空间还依赖 LTX 的视频 VAE。权重文件名**不在前端写死** —— 它是引擎侧的事实,
// 由 /api/meta 下发(见 store 的 metaUpscale),这里只回显默认值并允许覆盖。
// 副作用是"填错也没关系":后端提交前会体检权重,缺哪个文件、该放哪个目录都会讲清楚。
async function upscale(cmd) {
  const mf = props.manifestItem
  if (!mf || mf.status !== 'done') { ElMessage.info('镜头尚未渲染完成'); return }
  let opts = { type: 'esrgan' }
  if (cmd === 'ltx') {
    const defVae = G.metaUpscale?.default_ltx_vae || ''
    let vae
    try {
      const r = await ElMessageBox.prompt(
        `对镜头 ${props.shot.id} 做 LTX 隐空间 2× 放大。\n\n` +
        `这条链路更快、实拍与动漫画面的纹理更干净(本机实测 768P 源片约 18 秒/48 帧),\n` +
        `代价是学习式视频 VAE 会抹软硬边细字 —— 片内有字幕/小字时请改走帧级。\n\n` +
        `它依赖 ComfyUI 已装好 LTX 的视频 VAE:下面填的是默认文件名,只有你装了别的 VAE\n` +
        `才需要改;留空也等于用默认值。提交前会先体检权重,缺文件会直接告诉你该放哪。\n\n` +
        `只提升分辨率:音轨沿用源片、时长不变。结果作为独立文件产出,原镜头不变。`,
        '2× 放大 · 隐空间',
        {
          confirmButtonText: '开始放大', cancelButtonText: '取消',
          inputValue: defVae, inputPlaceholder: '如 LTX2_video_vae_bf16.safetensors'
        })
      vae = (r.value || '').trim()
    } catch { return }
    // 与默认值相同(或留空)就**不透传**:让后端用它自己的默认值,
    // 避免把"没改这一栏"变成"显式要一个空文件名"(后端对空白是直接 400 的)。
    if (vae && vae !== defVae) opts = { type: 'ltx', ltx_vae: vae }
    else opts = { type: 'ltx' }
  } else {
    try {
      await ElMessageBox.confirm(
        `对镜头 ${props.shot.id} 做 2× 放大?\n\n` +
        `走帧级链路(4x-UltraSharp):逐帧真超分,画面最忠实于源片 —— 片内硬边细字(字幕、路牌小字)\n` +
        `也能保住笔画。代价是慢:本机实测 768P 源片约 3.8 秒/帧(48 帧 ≈ 3 分钟)。\n\n` +
        `只提升分辨率:音轨沿用源片、时长不变。结果作为独立文件产出,原镜头不变。`,
        '2× 放大 · 帧级', { type: 'info', confirmButtonText: '开始放大', cancelButtonText: '取消' })
    } catch { return }
  }
  enhanceBusy.value = true
  try {
    const d = await api.upscale(pj.value.project.id, props.shot.id, opts)
    ElMessage.success('2× 放大完成:' + (d.file || ''))
    refreshProject()
  } catch (e) { ElMessage.error('2× 放大失败:' + e.message) }
  enhanceBusy.value = false
}

const statusTag = computed(() => {
  const m = props.manifestItem
  if (!m) return { text: '待渲染', type: 'info' }
  switch (m.status) {
    case 'done': return { text: '✓ 完成', type: 'success' }
    case 'rendering': return { text: '渲染中', type: 'warning' }
    case 'failed': return { text: '✕ 失败', type: 'danger' }
    default: return { text: '待渲染', type: 'info' }
  }
})

// statusHint 失败镜的**把手**（鼠标停在状态标签上看得到）。
//
// 为什么必须说这一句（2026-09-28）：渲染失败之后，平台会按官方降级阶梯给这一镜写一个
// **黑场静音占位**并标记 `placeholder: missing clip` —— 也就是说**这段已经进了成片**。
// 而卡片上只显示"✕ 失败"时，看起来像"还没做完"，用户可能以为成片是完整的、直接交付；
// 或者反过来以为整片都没出、白重渲一遍。两件事差别很大，必须让人一眼能分清。
const statusHint = computed(() => {
  const m = props.manifestItem
  if (!m) return '尚未渲染'
  if (m.status !== 'failed') return ''
  const err = String(m.error || '')
  if (err.includes('placeholder: missing clip')) {
    return '这一镜已按官方降级阶梯写成**黑场占位**（保住时长与对位）—— 成片里这段是没有画面的，重渲后必须重合成。原因：' + err
  }
  return '渲染失败：' + (err || '未记录原因')
})
const dialogues = computed(() => (props.shot.dialogue || []).map(d => `${d.speaker}:${d.text}`).join('\n'))

// 出场角色/场景名(分镜卡标注:id → 名称)
const castNames = computed(() => {
  const plan = pj.value?.plan
  if (!plan?.characters) return []
  return (props.shot.characters || [])
    .map(cid => plan.characters.find(c => c.id === cid)?.name)
    .filter(Boolean)
})
const sceneName = computed(() => {
  const plan = pj.value?.plan
  return plan?.scenes?.find(s => s.id === props.shot.scene)?.name || ''
})
// 本镜用到的道具名（2026-09-27：技能侧的 `prop_ids` 到位后，镜头才知道自己要用哪件道具）。
// 标出来是为了让人一眼看到"这一镜有道具参与" —— 道具图只做人审、不进参考图，
// 所以这是**唯一的**镜级线索；没有 prop_ids 的老方案退化为不显示（不猜）。
const propNames = computed(() => {
  const plan = pj.value?.plan
  if (!plan?.props) return []
  return (props.shot.prop_ids || [])
    .map(pid => plan.props.find(p => p.id === pid)?.name)
    .filter(Boolean)
})
</script>

<template>
  <div class="shot-card">
    <div class="relative cursor-pointer" style="aspect-ratio:9/16;background:var(--mx-bg0)"
         @click="openViewer" :title="manifestItem?.status === 'done' ? '点击全屏预览(可左右切换已渲镜头)' : '尚未渲染,渲染完成后可预览'">
      <video v-if="manifestItem?.status === 'done' && manifestItem?.file"
             :src="api.fileURL(pj.project.id, manifestItem.file) + '?v=' + (manifestItem.updated_at || '')"
             preload="metadata" playsinline muted
             style="width:100%;height:100%;object-fit:contain;background:#000" />
      <div v-else class="absolute inset-0 flex flex-col items-center justify-center gap-2" style="color:var(--mx-text3)">
        <el-icon :size="30"><Picture /></el-icon>
        <span class="timecode">SHOT {{ String(shot.id).padStart(2, '0') }}</span>
        <span class="timecode" v-if="manifestItem?.status === 'rendering'" style="color:var(--mx-amber)">渲染中…</span>
      </div>
      <div v-if="manifestItem?.status === 'done'" class="absolute inset-0 flex items-center justify-center opacity-0 hover:opacity-100 transition-opacity"
           style="background:color-mix(in srgb, var(--mx-bg0) 35%, transparent)">
        <div class="w-12 h-12 rounded-full flex items-center justify-center" style="background:color-mix(in srgb, var(--mx-bg0) 60%, transparent);border:1px solid color-mix(in srgb, var(--mx-amber) 50%, transparent)">
          <el-icon :size="20" color="var(--mx-amber-hi)"><VideoPlay /></el-icon>
        </div>
      </div>
      <div class="absolute top-2 left-2 flex gap-1.5 items-center pointer-events-none">
        <span class="badge-mode" :class="shot.mode">{{ shot.mode?.toUpperCase() }}</span>
        <span class="badge-mode dur">{{ shot.duration }}s</span>
      </div>
      <!-- 右上:删除本镜(方案+产物),运行中禁用 -->
      <button class="cover-del" title="删除该镜头(方案与产物)" :disabled="running" @click.stop="removeShot">
        <el-icon :size="13"><Delete /></el-icon>
      </button>
    </div>
    <div class="p-3 flex flex-col gap-2">
      <div class="flex items-center gap-2 text-xs">
        <b>镜 {{ shot.id }}</b>
        <el-tooltip v-if="statusHint" :content="statusHint" placement="top" :show-after="200">
              <el-tag size="small" :type="statusTag.type" effect="dark" style="cursor:help">{{ statusTag.text }}</el-tag>
            </el-tooltip>
            <el-tag v-else size="small" :type="statusTag.type" effect="dark">{{ statusTag.text }}</el-tag>
        <span v-if="manifestItem?.engine" class="timecode">{{ manifestItem.engine === 'comfy' ? '本地' : '云端' }}</span>
      </div>
      <div class="text-xs leading-relaxed" style="color:var(--mx-text2)">
        <div class="flex items-center gap-1.5 flex-wrap mb-1.5">
          <el-tooltip v-for="n in castNames" :key="n" :content="`出场角色:${n}`" placement="top">
            <span class="cast-chip"><el-icon :size="10"><User /></el-icon>{{ n }}</span>
          </el-tooltip>
          <el-tooltip v-if="sceneName" :content="`场景:${sceneName}`" placement="top">
            <span class="cast-chip scene"><el-icon :size="10"><Location /></el-icon>{{ sceneName }}</span>
        <span v-for="pn in propNames" :key="pn" class="cast-chip" :title="'本镜用到的道具:' + pn"><el-icon :size="10"><Suitcase /></el-icon>{{ pn }}</span>
          </el-tooltip>
        </div>
        <div><b style="color:var(--mx-cyan)">{{ shot.shot_size }}</b> · {{ shot.camera }}</div>
        <div class="mt-1">{{ shot.action }}</div>
        <div v-if="dialogues" class="mt-1" style="color:var(--mx-amber-hi);white-space:pre-line">{{ dialogues }}</div>
        <div v-if="shot.narration" class="mt-1" style="font-style:italic">旁白:{{ shot.narration }}</div>
      </div>
      <div v-if="manifestItem?.status === 'failed' && manifestItem?.error"
           class="text-xs p-2 rounded-lg" style="background:var(--mx-red-soft);color:var(--mx-red)">
        {{ manifestItem.error }}
      </div>
      <!-- 底部操作条:等宽三钮(提示词/微调 + 主操作 渲染|重渲 下拉),运行中整体禁用 -->
      <div class="card-ops">
        <el-tooltip content="查看完整 H3 提示词" placement="top">
          <button class="card-act" @click="G.promptShot = shot"><el-icon :size="13"><Document /></el-icon><span>提示词</span></button>
        </el-tooltip>
        <el-tooltip content="画布微调(提示词/时长/seed/引擎)" placement="top">
          <button class="card-act" :disabled="running" @click="G.tuneShot = shot"><el-icon :size="13"><Brush /></el-icon><span>微调</span></button>
          <button class="card-act" :disabled="running || enhanceBusy || manifestItem?.status !== 'done'"
                  title="局部重绘:只重生成崩坏区域,其余画面逐帧保留(需本地 ComfyUI)"
                  @click.stop="maskFix"><el-icon :size="13"><MagicStick /></el-icon><span>局部重绘</span></button>
          <button class="card-act" :disabled="running || enhanceBusy || manifestItem?.status !== 'done'"
                  title="音频驱动:用配音驱动重渲,口型与节奏跟随音频"
                  @click.stop="audioDrive"><el-icon :size="13"><Microphone /></el-icon><span>音频驱动</span></button>
        </el-tooltip>
        <el-dropdown trigger="click" :disabled="running || enhanceBusy || manifestItem?.status !== 'done'" @command="upscale">
          <button class="card-act" :disabled="running || enhanceBusy || manifestItem?.status !== 'done'"
                  title="2× 放大:只提升分辨率,音轨沿用源片、时长不变(需本地 ComfyUI)"
                  @click.stop>
            <el-icon :size="13"><ZoomIn /></el-icon><span>2× 放大</span>
            <el-icon :size="10"><ArrowDown /></el-icon>
          </button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="esrgan">帧级 2×(最省依赖 · 硬边细字更保真)</el-dropdown-item>
              <el-dropdown-item command="ltx">隐空间 2×(更快 · 实拍质感更好)</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <el-dropdown trigger="click" :disabled="running" @command="rerender">
          <button class="card-act primary" :disabled="running" @click.stop>
            <el-icon :size="13"><RefreshRight /></el-icon>
            <span>{{ manifestItem?.status === 'done' ? '重渲' : '渲染' }}</span>
            <el-icon :size="10"><ArrowDown /></el-icon>
          </button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="">按项目配置</el-dropdown-item>
              <el-dropdown-item command="comfy">本地 ComfyUI</el-dropdown-item>
              <el-dropdown-item command="api">云端 MiniMax</el-dropdown-item>
              <el-dropdown-item divided command="api2k" :disabled="manifestItem?.status!=='done'">云端 2K 定稿(需先渲出)</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </div>
  </div>
</template>
