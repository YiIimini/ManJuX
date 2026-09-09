<script setup>
import { G, api, refreshProjects, refreshStatus, phaseLabel, fileToB64 } from '../store'
import { ref, computed, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const creating = ref(false)

// ComfyUI 引擎启停(首页快捷控制)
const comfyBusy = ref(false)
const comfyLive = computed(() => !!G.status?.env?.comfy_live)
async function comfyToggle() {
  if (comfyLive.value) {
    try { await ElMessageBox.confirm('停止引擎?将结束全部 ComfyUI 进程,运行中的任务将中断。', '确认', { type: 'warning' }) }
    catch { return }
  }
  comfyBusy.value = true
  try {
    const act = comfyLive.value ? 'stop' : 'start'
    const r = await fetch(`/api/comfy/${act}`, { method: 'POST' })
    const d = await r.json()
    if (!r.ok) throw new Error(d.error)
    ElMessage.success(act === 'start' ? 'ComfyUI 启动指令已下发,引擎预热中…' : 'ComfyUI 已停止')
    setTimeout(refreshStatus, act === 'start' ? 12000 : 1500)
  } catch (e) { ElMessage.error(e.message) } finally { comfyBusy.value = false }
}

// 新建项目(带文件上传:txt/md 源文本,json 分镜方案)
const dlg = ref(false)
const newName = ref('')
const pickedFile = ref(null)   // {name, text, kind: 'text'|'plan', plan?, preview}
const fileInput = ref(null)
const busy = ref(false)
// AI 现场创作模式(曾漏定义三个 ref,致弹窗渲染崩溃/AI 开关失灵)
const aiMode = ref(false)
const aiPrompt = ref('')
const composing = ref(false)
// 整本小说批量模式
const novelMode = ref(false)
const novelText = ref('')
const splitting = ref(false)
const novelInput = ref(null)
const novelDirInput = ref(null)
const novelCover = ref(null)   // File:系列封面(目录「封面」文件夹自动识别 或 手动选择)
const novelCoverPath = ref('') // 桌面拖放(wails 原生)的本机封面路径
const novelCoverInput = ref(null)
const novelCoverURL = computed(() => novelCover.value ? URL.createObjectURL(novelCover.value) : (novelCoverPath.value ? '/api/thumb?path=' + encodeURIComponent(novelCoverPath.value) : ''))
function onCoverNativeDrop(ev) {
  const p = ev.detail?.[0]
  if (!p) return
  if (!/\.(png|jpe?g|webp)$/i.test(p)) { ElMessage.error('仅支持 png/jpg/webp'); return }
  novelCoverPath.value = p
  novelCover.value = null
}
function onPickNovel(ev) {
  const f = ev.target.files?.[0]
  ev.target.value = ''
  if (!f) return
  if (f.size > 20 << 20) { ElMessage.error('文件过大(>20MB)'); return }
  const rd = new FileReader()
  rd.onload = () => { novelText.value = String(rd.result || '') }
  rd.readAsText(f, 'utf-8')
}
// 目录导入:合并目录内 txt/md 章节(按路径排序);
// 路径含「封面」目录的图片自动识别为系列封面(选包含小说与封面文件夹的上层目录即可)。
function onPickNovelDir(ev) {
  const files = Array.from(ev.target.files || [])
  ev.target.value = ''
  if (!files.length) return
  const chapters = files.filter(f => /\.(txt|md)$/i.test(f.name) && !f.webkitRelativePath.includes('封面'))
  const cover = files.find(f => /\.(png|jpe?g|webp)$/i.test(f.name) && (f.webkitRelativePath || '').includes('封面'))
  if (!chapters.length) { ElMessage.warning('所选目录中没有 txt/md 章节文件'); return }
  chapters.sort((a, b) => (a.webkitRelativePath || a.name).localeCompare(b.webkitRelativePath || b.name, 'zh'))
  if (cover) novelCover.value = cover
  let done = 0
  let text = ''
  for (const ch of chapters) {
    const rd = new FileReader()
    rd.onload = () => {
      text += (text ? '\n\n' : '') + String(rd.result || '')
      if (++done === chapters.length) novelText.value = text
    }
    rd.readAsText(ch, 'utf-8')
  }
  ElMessage.info(`已读入 ${chapters.length} 个章节文件` + (cover ? ',并识别到封面' : ''))
}
function onPickNovelCover(ev) {
  const f = ev.target.files?.[0]
  ev.target.value = ''
  if (!f) return
  if (!/^image\/(png|jpe?g|webp)/.test(f.type)) { ElMessage.error('仅支持 png/jpg/webp'); return }
  novelCover.value = f
  novelCoverPath.value = ''
}
async function doNovelSplit() {
  if (novelText.value.trim().length < 200) { ElMessage.warning('小说正文太短(至少 200 字)'); return }
  splitting.value = true
  try {
    // 书名留空 → 后端用 LLM 从小说内容自动识别
    const r = await api.novelSplit(newName.value.trim() || '', novelText.value, 0)
    // 封面:上传到每集项目(系列卡取第一集封面,单集卡也各自显示)
    if (novelCover.value || novelCoverPath.value) {
      let okc = 0
      const payload = novelCoverPath.value
        ? { image_path: novelCoverPath.value }
        : await fileToB64(novelCover.value).then(b64 => ({ image_b64: b64, filename: novelCover.value.name })).catch(() => null)
      if (payload) {
        for (const e of r.episodes) {
          try {
            const cr = await fetch(`/api/projects/${encodeURIComponent(e.project)}/cover`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify(payload),
            })
            if (cr.ok) okc++
          } catch { /* 单集失败不阻塞 */ }
        }
      }
      if (okc) ElMessage.success(`封面已应用到 ${okc} 集`)
    }
    ElMessage.success(`拆集完成:《${r.book}》共 ${r.episodes.length} 集,点击「剧集合集」卡片管理并批量生产`)
    novelText.value = ''
    novelCover.value = null
    novelCoverPath.value = ''
    dlg.value = false
    await refreshProjects()
  } catch (e) { ElMessage.error(e.message) } finally { splitting.value = false }
}

const totalShots = computed(() => G.projects.reduce((a, p) => a + (p.shots_total || 0), 0))
const doneShots = computed(() => G.projects.reduce((a, p) => a + (p.shots_done || 0), 0))
// 项目总量按「管理单元」计:整部剧集系列算 1 部,独立项目算 1 个(不按集数膨胀)
const totalUnits = computed(() => seriesGroups.value.singles.length + Object.keys(seriesGroups.value.groups).length)
const totalChars = computed(() => G.projects.reduce((a, p) => a + (p.characters || 0), 0))
const totalScenes = computed(() => G.projects.reduce((a, p) => a + (p.scenes || 0), 0))

// 进行中拆集任务(首页占位卡:提交一键创作后书卡立即出现)
const novelJobs = ref([])
watch(() => G.logs.length, () => {
  const last = G.logs[G.logs.length - 1]
  if (last && last.phase === 'novel') refreshJobs() // 拆集事件即时刷新占位卡
})
async function refreshJobs() {
  try {
    const r = await fetch('/api/novel-jobs').then(x => x.json())
    novelJobs.value = r.jobs || []
  } catch { /* */ }
}
// 移除失败占位卡(仅清除提示;运行中的任务卡不可移除)
async function clearJob(j) {
  if (j.state !== 'failed') return
  try {
    const r = await fetch('/api/novel-jobs?series=' + encodeURIComponent(j.series), { method: 'DELETE' })
    const d = await r.json().catch(() => ({}))
    if (!r.ok) throw new Error(d.error || `HTTP ${r.status}`)
    ElMessage.success('已移除失败占位卡')
    refreshJobs()
  } catch (e) { ElMessage.error(e.message) }
}
onMounted(() => {
  refreshJobs()
  setInterval(refreshJobs, 8000)
})

// 剧集弹窗:点击分组卡打开,逐集管理(含单集独立删除)
const seriesDlg = ref(null) // {prefix, eps}
function openSeries(prefix, eps) { seriesDlg.value = { prefix, eps } }
async function delEpisode(e) {
  try {
    await ElMessageBox.confirm(`删除分集「${e.name}」?该集的方案/资产/镜头/成片将移除,其它集不受影响。`, '删除分集',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  try {
    await api.deleteProject(e.id)
    ElMessage.success(`已删除「${e.name}」`)
    // 刷新弹窗与首页
    await refreshProjects()
    const left = (G.projects || []).filter(p => p.name.startsWith(seriesDlg.value.prefix + '·第'))
    if (left.length) seriesDlg.value = { prefix: seriesDlg.value.prefix, eps: left }
    else seriesDlg.value = null
  } catch (err) { ElMessage.error(err.message) }
}

// 批量管理:按「管理单元」勾选删除 —— 单项目 或 整部小说(series:书名=全部集)。
// 多集的独立管理(删某一集)在剧集弹窗里做,首页只按书整删。
const batch = ref(false)
const sel = ref(new Set())
function toggleSel(id) {
  if (sel.value.has(id)) sel.value.delete(id)
  else sel.value.add(id)
  sel.value = new Set(sel.value) // 触发响应
}
function selUnitCount() {
  let n = 0
  for (const u of sel.value) n += u.startsWith('series:') ? seriesGroups.value.groups[u.slice(7)]?.length || 0 : 1
  return n
}
// 全选/取消全选(管理单元 = 整书系列 + 单项目)
const allUnits = computed(() => [
  ...Object.keys(seriesGroups.value.groups).map(k => 'series:' + k),
  ...seriesGroups.value.singles.map(p => p.id),
])
const allSelected = computed(() => allUnits.value.length > 0 && allUnits.value.every(u => sel.value.has(u)))
function toggleAllSel() {
  sel.value = allSelected.value ? new Set() : new Set(allUnits.value)
}
async function batchDelete() {
  if (!sel.value.size) return
  const units = sel.value.size
  const n = selUnitCount()
  try {
    await ElMessageBox.confirm(
      `删除选中的 ${units} 个管理单元(共 ${n} 个项目)?` +
      (units < n ? ' 其中包含整部小说系列,该书全部分集将一并移除。' : '') +
      ' 项目数据(分镜/资产/镜头/成片)全部移除,不可恢复。',
      '批量删除', { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  let ok = 0, fail = 0
  for (const u of sel.value) {
    const ids = u.startsWith('series:') ? (seriesGroups.value.groups[u.slice(7)] || []).map(e => e.id) : [u]
    for (const id of ids) {
      try { await api.deleteProject(id); ok++ } catch { fail++ }
    }
  }
  sel.value = new Set()
  batch.value = false
  ElMessage.success(`已删除 ${ok} 个项目` + (fail ? `,${fail} 个失败` : ''))
  refreshProjects()
}

// 剧集分组:「主名·第N集·标题」归组显示
const seriesGroups = computed(() => {
  const re = /^(.+)·第\d+集/
  const groups = {}
  const singles = []
  for (const p of G.projects) {
    const m = p.name.match(re)
    if (m) {
      const key = m[1]
      groups[key] = groups[key] || []
      groups[key].push(p)
    } else singles.push(p)
  }
  for (const k of Object.keys(groups)) groups[k].sort((a, b) => a.name.localeCompare(b.name, 'zh'))
  return { groups, singles }
})

// 批量产线
const batchStat = ref(null)
let batchTimer = null
async function pollBatch() {
  try {
    const r = await api.novelBatchStatus()
    batchStat.value = r.running ? r : null
    if (r.running && !batchTimer) batchTimer = setInterval(async () => {
      try {
        const s = await api.novelBatchStatus()
        batchStat.value = s.running ? s : null
        if (!s.running) { refreshProjects() }
      } catch { /* */ }
    }, 8000)
    if (!r.running && batchTimer) { clearInterval(batchTimer); batchTimer = null; refreshProjects() }
  } catch { /* */ }
}
onMounted(pollBatch)
async function startBatch(prefix) {
  try {
    const r = await api.novelBatchStart(prefix)
    ElMessage.success(`批量产线已启动:共 ${r.total} 集,逐集串行生产`)
    pollBatch()
  } catch (e) { ElMessage.error(e.message) }
}
async function stopBatch() {
  try { await api.novelBatchStop(); ElMessage.info('已请求停止,当前集收尾后终止') } catch (e) { ElMessage.error(e.message) }
}

function openCreate() {
  newName.value = ''
  pickedFile.value = null
  dlg.value = true
}

function onDropFile(ev) {
  const dt = ev.dataTransfer
  if (!dt) return
  const files = dt.files
  if (files?.length) onPickFile({ target: { files, value: '' } })
}

function onPickFile(ev) {
  const f = ev.target.files?.[0]
  ev.target.value = ''
  if (!f) return
  if (f.size > 8 << 20) { ElMessage.error('文件过大(>8MB)'); return }
  const reader = new FileReader()
  reader.onload = () => {
    const text = String(reader.result || '')
    const ext = (f.name.split('.').pop() || '').toLowerCase()
    if (ext === 'json') {
      try {
        const j = JSON.parse(text)
        const plan = j.plan && Array.isArray(j.plan.shots) ? j.plan : (Array.isArray(j.shots) ? j : null)
        if (plan) {
          pickedFile.value = { name: f.name, text, kind: 'plan', plan,
            preview: `《${plan.title || '未命名'}》${plan.shots.length} 镜 / ${(plan.characters || []).length} 角色 / ${(plan.scenes || []).length} 场景` }
          return
        }
      } catch { /* 非 JSON 或结构不完整 → 当源文本 */ }
    }
    pickedFile.value = { name: f.name, text, kind: 'text',
      preview: text.replace(/\s+/g, ' ').slice(0, 120) + (text.length > 120 ? '…' : '') }
  }
  reader.readAsText(f, 'utf-8')
}

async function doCreate() {
  if (novelMode.value) { await doNovelSplit(); return }
  if (!newName.value.trim()) { ElMessage.warning('请输入项目名'); return }
  busy.value = true
  try {
    const id = newName.value.trim()
    await api.createProject(id)
    const useAI = aiMode.value && aiPrompt.value.trim()
    if (useAI) {
      // AI 现场创作:弹窗保持打开、按钮进入 loading(推理模型 1-3 分钟,
      // 期间日志 ticker 有 30 秒心跳),完成后才关弹窗进项目
      if (pickedFile.value) pickedFile.value = null // AI 模式不携带文件
      composing.value = true
      try {
        const r = await api.composeStory(id, aiPrompt.value.trim(), '')
        ElMessage.success(`AI 创作完成(${r.chars} 字),已写入「剧本」`)
      } catch (e) {
        ElMessage.warning(`项目已创建,但 AI 创作失败:${e.message}(可进入剧本页点「AI 现场创作」重试)`)
      } finally { composing.value = false }
      aiPrompt.value = ''
      dlg.value = false
    } else if (pickedFile.value?.kind === 'plan') {
      await api.importPlan(id, pickedFile.value.plan)
      ElMessage.success(`项目「${id}」已创建(分镜已导入)`)
      dlg.value = false
    } else if (pickedFile.value?.text) {
      await api.saveSource(id, pickedFile.value.text)
      ElMessage.success(`项目「${id}」已创建(源文本已载入)`)
      dlg.value = false
    } else {
      ElMessage.success(`项目「${id}」已创建`)
      dlg.value = false
    }
    await refreshProjects()
    await openProject(id)
  } catch (e) { ElMessage.error(e.message) } finally { busy.value = false }
}

async function openProject(id) {
  try { G.project = await api.project(id); G.view = 'project' }
  catch (e) { ElMessage.error(e.message) }
}

async function removeProject(p) {
  try {
    await ElMessageBox.confirm(
      `将删除项目「${p.name}」的全部数据(分镜/资产/镜头/成片),不可恢复。`, '删除项目',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  try { await api.deleteProject(p.id); ElMessage.success('已删除'); refreshProjects() }
  catch (e) { ElMessage.error(e.message) }
}

// 系列卡右上角删除:整部剧集合集(全部子项目)二次确认后逐集删除
// ---- 卡片直达:开始(启动/续跑管线)与停止(停管线+释放显存) ----
async function startSeriesBatch(prefix) {
  try { await api.novelBatchStart(prefix); ElMessage.success('批量产线已启动(从中断处续跑)') }
  catch (e) { ElMessage.error(e.message) }
}
async function stopSeriesBatch() {
  try {
    await api.novelBatchStop()
    for (const id of Object.keys(G.status?.running || {})) { try { await api.stop(id) } catch { /* 单个失败继续 */ } }
    await api.comfyFree()
    ElMessage.success('已停止管线并释放显存')
    refreshStatus()
  } catch (e) { ElMessage.error(e.message) }
}
async function startProjectRun(p) {
  try { await api.run(p.id, 'all'); ElMessage.success(`「${p.name}」管线已启动(续跑)`) }
  catch (e) { ElMessage.error(e.message) }
}
async function stopProjectRun(p) {
  try {
    await api.stop(p.id)
    await api.comfyFree()
    ElMessage.success('已停止管线并释放显存')
    refreshStatus()
  } catch (e) { ElMessage.error(e.message) }
}
async function removeSeries(prefix, eps) {
  if (batchStat && batchStat.prefix === prefix) {
    ElMessage.warning('该系列正在批量产线运行中,请先停止批量再删除')
    return
  }
  try {
    await ElMessageBox.confirm(
      `将删除剧集合集《${prefix}》全部 ${eps.length} 集项目(分镜/资产/镜头/成片,整部不可恢复)。`, '删除整部系列',
      { confirmButtonText: '删除整部', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  let ok = 0, fail = 0
  for (const e of eps) {
    try { await api.deleteProject(e.id); ok++ } catch { fail++ }
  }
  ElMessage.success(`已删除 ${ok} 集` + (fail ? `,${fail} 集失败` : ''))
  refreshProjects()
}

function stateChip(p) {
  if (p.running) return { t: `${phaseLabel(p.phase)} · 运行中`, c: 'run' }
  if (p.shots_total && p.shots_done >= p.shots_total) return { t: '已完成', c: 'ok' }
  if (p.shots_done > 0) return { t: `渲染 ${p.shots_done}/${p.shots_total}`, c: 'warn' }
  if (p.has_plan) return { t: '待渲染', c: '' }
  return { t: '待创作', c: '' }
}
</script>

<template>
  <div class="h-full overflow-y-auto p-6">
    <!-- Hero -->
    <div class="panel panel-glow p-7 mb-6 relative overflow-hidden">
      <div style="position:absolute;right:-30px;top:-46px;font-size:160px;opacity:.05;pointer-events:none">🎬</div>
      <div style="position:absolute;left:0;top:0;bottom:0;width:3px;background:linear-gradient(180deg,var(--mx-amber-hi),var(--mx-amber-deep),var(--mx-cyan))"></div>
      <div class="flex items-end gap-8 flex-wrap">
        <div class="flex-1 min-w-[320px]">
          <h1 class="text-2xl font-black m-0 mb-2 grad-text">从一段文字,到一部成片</h1>
          <p class="text-sm m-0" style="color:var(--mx-text2);max-width:640px;line-height:1.8">
            输入故事 → LLM 直出分镜与 H3 提示词 → 资产库复用定妆 →
            <b style="color:var(--mx-amber)">本地 ComfyUI</b> / <b style="color:var(--mx-cyan)">云端 H3 API</b>
            双引擎渲染 → 机械+VLM 双重质检 → 合成成片。资产一次定妆,全项目复用。
          </p>
        </div>
        <div class="flex gap-6 pb-1">
          <div class="text-center"><div class="stat-num">{{ totalUnits }}</div><div class="stat-label">项目</div></div>
          <div class="text-center"><div class="stat-num">{{ totalChars }}</div><div class="stat-label">角色</div></div>
          <div class="text-center"><div class="stat-num">{{ totalScenes }}</div><div class="stat-label">场景</div></div>
          <div class="text-center"><div class="stat-num">{{ doneShots }}<span style="font-size:15px;color:var(--mx-text3)">/{{ totalShots }}</span></div><div class="stat-label">镜头</div></div>
        </div>
        <div class="flex flex-col gap-2 items-stretch">
          <div class="flex items-center gap-2 justify-end">
            <span class="envchip"><i class="dot" :class="comfyLive ? 'ok' : 'warn'"></i>COMFY {{ comfyLive ? '在线' : '离线' }}</span>
            <el-tooltip :content="comfyLive ? '结束全部 ComfyUI 相关进程,释放显存' : '启动本地 ComfyUI 渲染引擎(加载模型约 1-2 分钟)'" placement="bottom">
            <el-button size="small" :type="comfyLive ? 'default' : 'primary'" plain :loading="comfyBusy" @click="comfyToggle">
              <el-icon class="mr-1"><VideoPause v-if="comfyLive" /><VideoPlay v-else /></el-icon>{{ comfyLive ? '停止引擎' : '启动引擎' }}
            </el-button>
            </el-tooltip>
          </div>
          <el-tooltip content="单集项目(粘贴文本/AI 创作/导方案)或整本小说一键拆集" placement="bottom">
            <el-button type="primary" size="large" @click="openCreate">
              <el-icon class="mr-1"><Plus /></el-icon>新建项目
            </el-button>
          </el-tooltip>
        </div>
      </div>
    </div>

    <!-- 项目网格 -->
    <div class="flex items-center mb-3">
      <h2 class="text-base font-bold m-0">我的项目</h2>
      <span class="timecode ml-3">{{ totalUnits }} UNITS · {{ G.projects.length }} EPISODES</span>
      <div class="flex-1"></div>
      <template v-if="batch">
        <span class="text-xs mr-2" style="color:var(--mx-amber)">已选 {{ sel.size }} 单元 / {{ selUnitCount() }} 项目</span>
        <el-button size="small" plain @click="toggleAllSel">{{ allSelected ? '取消全选' : '全选' }}</el-button>
        <el-button size="small" type="danger" :disabled="!sel.size" @click="batchDelete">
          <el-icon class="mr-1"><Delete /></el-icon>删除选中
        </el-button>
        <el-button size="small" @click="batch = false; sel = new Set()">退出批量</el-button>
      </template>
      <el-tooltip v-else content="按书/按项目勾选,批量删除管理单元" placement="top">
        <el-button size="small" plain :disabled="!G.projects.length" @click="batch = true">
          <el-icon class="mr-1"><Operation /></el-icon>批量管理
        </el-button>
      </el-tooltip>
    </div>

    <!-- 批量模式提示条 -->
    <el-alert v-if="batch" type="info" :closable="false" class="mb-3"
      title="批量管理模式:整部小说按书勾选(一键删全部分集),单项目按卡勾选;删除某一集请在剧集合集弹窗里单独操作。" />

    <div v-if="!G.projects.length && !novelJobs.some(j => j.state === 'running' || j.state === 'failed')"
         class="panel empty-zone">
      <div class="ez-ico"><el-icon :size="26"><Film /></el-icon></div>
      <div class="ez-title">还没有项目</div>
      <p class="ez-desc">点击右上「新建项目」:粘贴故事文本、让 AI 现场创作,或导入整本小说一键拆集成多集短视频。</p>
    </div>

    <div v-if="batch || seriesGroups.singles.length || Object.keys(seriesGroups.groups).length
             || (!batch && novelJobs.some(j => j.state === 'running' || j.state === 'failed'))"
         class="grid gap-5" style="grid-template-columns:repeat(auto-fill,minmax(252px,1fr))">
      <!-- 拆集进行中:书级占位卡(提交即出现,分集陆续填充) -->
      <div v-for="j in novelJobs.filter(x => x.state === 'running' || x.state === 'failed')"
           :key="'job-' + j.series" class="shot-card proj-card" style="border-style:dashed">
        <div class="proj-cover">
          <div class="absolute inset-0 flex flex-col items-center justify-center gap-2">
            <el-icon :size="30" :color="j.state === 'running' ? 'var(--mx-amber)' : 'var(--mx-red)'">
              <Loading v-if="j.state === 'running'" class="is-loading" /><CircleCloseFilled v-else />
            </el-icon>
            <span class="timecode">{{ j.state === 'running' ? 'AI 拆集中' : '拆集失败' }}</span>
          </div>
          <button v-if="j.state === 'failed'" class="cover-del" title="移除失败占位卡" @click.stop="clearJob(j)">
            <el-icon :size="13"><Delete /></el-icon>
          </button>
        </div>
        <div class="px-3.5 py-3 flex flex-col gap-2">
          <div class="flex items-center gap-2">
            <b class="truncate flex-1 text-sm">《{{ j.series }}》</b>
            <span class="timecode" style="font-size:10px" :style="j.state === 'failed' ? 'color:var(--mx-red)' : ''">
              {{ j.state === 'running' ? (j.created ? ('已建 ' + j.created + ' 集') : '通读中…') : '失败' }}
            </span>
          </div>
          <div class="text-xs truncate" :style="{ color: j.state === 'failed' ? 'var(--mx-red)' : 'var(--mx-text3)' }">{{ j.message }}</div>
        </div>
      </div>

      <!-- 剧集系列卡(点击打开逐集弹窗) -->
      <div v-if="!batch" v-for="(eps, prefix) in seriesGroups.groups" :key="'s-' + prefix"
           class="shot-card proj-card cursor-pointer" :title="'打开《' + prefix + '》剧集管理(逐集进入/删除/批量产线)'" @click="openSeries(prefix, eps)">
        <div class="proj-cover">
          <template v-if="eps.find(e => e.cover)">
            <img class="cover-fit" :src="api.fileURL(eps.find(e => e.cover).id, eps.find(e => e.cover).cover)" alt="" aria-hidden="true" />
            <img class="cover-full" :src="api.fileURL(eps.find(e => e.cover).id, eps.find(e => e.cover).cover)" alt="" style="filter:saturate(1.06)" />
          </template>
          <div v-else class="absolute inset-0 flex flex-col items-center justify-center gap-2">
            <el-icon :size="30" color="var(--mx-text3)"><Film /></el-icon>
            <span class="timecode">{{ eps.length }} 集</span>
          </div>
          <div class="absolute inset-0 flex items-center justify-center" style="pointer-events:none">
            <div class="px-3 py-1.5 rounded-lg flex items-center gap-2"
                 style="background:rgba(6,8,14,.55);border:1px solid rgba(255,180,84,.4);backdrop-filter:blur(4px)">
              <el-icon :size="16" color="var(--mx-amber-hi)"><Folder /></el-icon>
              <span class="text-xs" style="color:var(--mx-amber-hi)">剧集合集</span>
            </div>
          </div>
          <!-- 封面右上:批量运行灯(左)与删除整部(右),批量运行中禁用删除 -->
          <i v-if="batchStat && batchStat.prefix === prefix" class="dot run"
             style="position:absolute;top:12px;right:106px"></i>
          <button class="cover-del" style="right:74px" title="开始:启动批量产线(从中断处续跑)"
                  :disabled="!!(batchStat && batchStat.running)" @click.stop="startSeriesBatch(prefix)">
            <el-icon :size="13"><VideoPlay /></el-icon>
          </button>
          <button class="cover-del" style="right:42px" title="停止:停止管线并释放显存"
                  :disabled="!(batchStat && batchStat.prefix === prefix)" @click.stop="stopSeriesBatch()">
            <el-icon :size="13"><VideoPause /></el-icon>
          </button>
          <button class="cover-del" title="删除整部系列(全部集)" :disabled="!!(batchStat && batchStat.prefix === prefix)"
                  @click.stop="removeSeries(prefix, eps)">
            <el-icon :size="13"><Delete /></el-icon>
          </button>
        </div>
        <div class="px-3.5 py-3 flex flex-col gap-2">
          <div class="flex items-center gap-2">
            <b class="truncate flex-1 text-sm">《{{ prefix }}》</b>
            <span class="timecode" style="font-size:10px">
              {{ (batchStat && batchStat.prefix === prefix) ? ('批量中 ' + batchStat.current + '/' + batchStat.total) : (eps.filter(e => e.shots_total && e.shots_done >= e.shots_total).length + '/' + eps.length + ' 集') }}
            </span>
          </div>
          <el-progress
            :percentage="Math.round(eps.reduce((a, e) => a + (e.shots_done || 0), 0) * 100 / Math.max(1, eps.reduce((a, e) => a + (e.shots_total || 0), 0)))"
            :stroke-width="5" :show-text="false" :color="'linear-gradient(90deg, var(--mx-amber-hi), var(--mx-amber-deep))'" />
        </div>
      </div>

      <!-- 批量模式:系列=一个管理单元(勾=整书删除) -->
      <div v-if="batch" v-for="(eps, prefix) in seriesGroups.groups" :key="'bs-' + prefix"
           class="shot-card proj-card cursor-pointer relative" @click="toggleSel('series:' + prefix)">
        <div v-if="batch" class="absolute top-2 left-2 z-10" @click.stop="toggleSel('series:' + prefix)">
          <el-checkbox :model-value="sel.has('series:' + prefix)" size="large" />
        </div>
        <div class="proj-cover">
          <div class="absolute inset-0 flex flex-col items-center justify-center gap-2">
            <el-icon :size="30" color="var(--mx-text3)"><Folder /></el-icon>
            <span class="timecode">{{ eps.length }} 集</span>
          </div>
        </div>
        <div class="px-3.5 py-3 flex flex-col gap-2">
          <div class="flex items-center gap-2">
            <b class="truncate flex-1 text-sm">《{{ prefix }}》</b>
            <span class="timecode" style="font-size:10px">整部系列</span>
          </div>
        </div>
      </div>

      <div v-for="p in (batch ? seriesGroups.singles : seriesGroups.singles)" :key="p.id" class="shot-card proj-card cursor-pointer relative"
           :title="batch ? '勾选加入批量删除' : '进入「' + p.name + '」工作台'" @click="batch ? toggleSel(p.id) : openProject(p.id)">
        <div v-if="batch" class="absolute top-2 left-2 z-10" @click.stop="toggleSel(p.id)">
          <el-checkbox :model-value="sel.has(p.id)" size="large" />
        </div>
        <div class="proj-cover">
          <template v-if="p.cover">
            <img class="cover-fit" :src="api.fileURL(p.id, p.cover)" alt="" aria-hidden="true" />
            <img class="cover-full" :src="api.fileURL(p.id, p.cover)" alt="" style="filter:saturate(1.06)" />
          </template>
          <div v-else class="absolute inset-0 flex flex-col items-center justify-center gap-2">
            <el-icon :size="30" color="var(--mx-text3)"><Film /></el-icon>
            <span class="timecode">{{ p.has_plan ? p.shots_total + ' 镜' : 'NEW' }}</span>
          </div>
          <!-- 封面右上:状态灯(左)与删除(右)保持间距,不重叠 -->
          <i class="dot" :class="stateChip(p).c" style="position:absolute;top:12px;right:106px"></i>
          <button class="cover-del" style="right:74px" title="开始:启动管线(开始/续跑)"
                  :disabled="!!G.status?.running?.[p.id]" @click.stop="startProjectRun(p)">
            <el-icon :size="13"><VideoPlay /></el-icon>
          </button>
          <button class="cover-del" style="right:42px" title="停止:停止管线并释放显存"
                  :disabled="!G.status?.running?.[p.id]" @click.stop="stopProjectRun(p)">
            <el-icon :size="13"><VideoPause /></el-icon>
          </button>
          <button v-if="!batch" class="cover-del" title="删除项目" @click.stop="removeProject(p)">
            <el-icon :size="13"><Delete /></el-icon>
          </button>
        </div>
        <div class="px-3.5 py-3 flex flex-col gap-2">
          <div class="flex items-center gap-2">
            <b class="truncate flex-1 text-sm">{{ p.name }}</b>
            <span class="timecode" style="font-size:10px">{{ stateChip(p).t }}</span>
          </div>
          <el-progress v-if="p.shots_total" :percentage="Math.round(p.shots_done * 100 / p.shots_total)"
                       :stroke-width="5" :show-text="false"
                       :color="'linear-gradient(90deg, var(--mx-amber-hi), var(--mx-amber-deep))'" />
        </div>
      </div>
    </div>

    <!-- 剧集合集弹窗:逐集管理 -->
    <el-dialog :model-value="!!seriesDlg" :title="seriesDlg ? ('《' + seriesDlg.prefix + '》· 剧集合集(' + seriesDlg.eps.length + ' 集)') : ''"
               width="640px" @update:model-value="seriesDlg = null">
      <template v-if="seriesDlg">
        <div class="flex items-center gap-3 mb-3">
          <el-progress style="flex:1"
            :percentage="Math.round(seriesDlg.eps.reduce((a, e) => a + (e.shots_done || 0), 0) * 100 / Math.max(1, seriesDlg.eps.reduce((a, e) => a + (e.shots_total || 0), 0)))"
            :stroke-width="6" :color="'linear-gradient(90deg, var(--mx-amber-hi), var(--mx-amber-deep))'" />
          <span class="timecode">{{ seriesDlg.eps.filter(e => e.shots_total && e.shots_done >= e.shots_total).length }}/{{ seriesDlg.eps.length }} 集完成</span>
        </div>
        <div class="flex flex-col gap-2" style="max-height:46vh;overflow-y:auto">
          <div v-for="e in seriesDlg.eps" :key="e.id"
               class="flex items-center gap-3 p-2.5 rounded-lg cursor-pointer ep-row"
               @click="seriesDlg = null; openProject(e.id)">
            <span class="timecode" style="width:44px">{{ (e.name.match(/·第(\d+)集/) || [])[1] || '—' }}</span>
            <i class="dot" :class="e.running ? 'run' : (e.shots_total && e.shots_done >= e.shots_total) ? 'ok' : (e.shots_done > 0 ? 'warn' : '')"></i>
            <div class="flex-1 min-w-0">
              <div class="text-sm truncate">{{ e.name.split('·').slice(2).join('·') || e.name }}</div>
              <div class="text-xs" style="color:var(--mx-text3)">{{ e.running ? (phaseLabel(e.phase) + ' · 运行中') : (e.shots_total ? ('镜头 ' + e.shots_done + '/' + e.shots_total) : '待创作') }}</div>
            </div>
            <el-tooltip content="删除本集(仅这一集)" placement="top">
              <el-button size="small" text type="danger" @click.stop="delEpisode(e)"><el-icon><Delete /></el-icon></el-button>
            </el-tooltip>
            <el-button size="small" text type="primary" @click.stop="seriesDlg = null; openProject(e.id)"><el-icon><ArrowRight /></el-icon>进入</el-button>
          </div>
        </div>
      </template>
      <template #footer>
        <template v-if="batchStat && seriesDlg && batchStat.prefix === seriesDlg.prefix">
          <span class="text-xs mr-2 flex items-center gap-1.5" style="color:var(--mx-amber)"><i class="dot run"></i>批量中 第 {{ batchStat.current }}/{{ batchStat.total }} 集(已成片自动跳过,支持中断续跑)</span>
          <el-button type="danger" plain @click="stopBatch"><el-icon class="mr-1"><VideoPause /></el-icon>停止批量</el-button>
        </template>
        <el-button v-else type="primary" plain :disabled="!seriesDlg || !!batchStat" @click="seriesDlg && startBatch(seriesDlg.prefix)">
          <el-icon class="mr-1"><VideoCameraFilled /></el-icon>{{ batchStat ? '批量运行中…' : '批量产线(断点续跑)' }}
        </el-button>
        <el-button @click="seriesDlg = null">关闭</el-button>
      </template>
    </el-dialog>

    <!-- 新建项目:可选上传 txt / md / json -->
    <el-dialog v-model="dlg" title="新建短视频项目" width="560px"
               :close-on-click-modal="!composing" :close-on-press-escape="!composing" :show-close="!composing"
               v-loading="composing" element-loading-text="AI 创作中(1-3 分钟),日志有实时心跳…">
      <el-form label-position="top">
        <el-form-item label="项目名(中文 / 字母 / 数字 / _-)">
          <el-input v-model="newName" maxlength="40" :placeholder="novelMode ? '书名(可留空,AI 自动从小说识别)' : '如:天庭外卖员-第二集'" @keyup.enter="doCreate" />
        </el-form-item>
        <el-form-item v-if="!novelMode" label="AI 现场创作(只给需求,LLM 即时发挥写完整故事)">
          <div class="w-full flex flex-col gap-2">
            <el-switch v-model="aiMode" :disabled="novelMode" active-text="由 AI 创作" inactive-text="自己提供文本" />
            <el-input v-if="aiMode" v-model="aiPrompt" type="textarea" :rows="3" maxlength="300" show-word-limit
              placeholder="一句话或一个点子,如:外卖员送餐到天庭,玉帝当场包下他所有订单,搞笑爽文" />
          </div>
        </el-form-item>
        <el-form-item label="整本小说批量(拆成多集短视频,逐集产线)">
          <div class="w-full flex flex-col gap-2">
            <el-switch v-model="novelMode" :disabled="aiMode" active-text="整本小说模式" inactive-text="单集模式" />
            <template v-if="novelMode">
              <div class="flex gap-2 items-center">
                <el-input v-model="novelText" type="textarea" :rows="5" placeholder="粘贴整本小说/长文…&#10;LLM 将按叙事节拍拆成多集,每集一个短视频项目(同名角色自动复用定妆照)"
                  style="flex:1" />
                <div class="flex flex-col gap-2">
                  <input ref="novelInput" type="file" accept=".txt,.md" hidden @change="onPickNovel" />
                  <input ref="novelDirInput" type="file" webkitdirectory hidden @change="onPickNovelDir" />
                  <input ref="novelCoverInput" type="file" accept="image/png,image/jpeg,image/webp" hidden @change="onPickNovelCover" />
                  <el-button size="small" @click="novelInput?.click()"><el-icon class="mr-1"><Upload /></el-icon>导入 txt / md</el-button>
                  <el-button size="small" @click="novelDirInput?.click()"><el-icon class="mr-1"><FolderOpened /></el-icon>导入目录</el-button>
                  <span class="text-xs" style="color:var(--mx-text3)">{{ novelText.length ? novelText.length + ' 字' : '' }}</span>
                </div>
              </div>
              <div class="flex items-center gap-3" style="--wails-drop-target: drop" @mx-filedrop="onCoverNativeDrop">
                <div v-if="novelCover || novelCoverPath" class="flex items-center gap-2 p-1.5 rounded-lg" style="border:1px solid var(--mx-line-hi)">
                  <img v-if="novelCoverURL" :src="novelCoverURL" style="width:34px;height:46px;object-fit:cover;border-radius:5px" />
                  <el-button text size="small" @click="novelCover = null; novelCoverPath = ''"><el-icon><Close /></el-icon></el-button>
                </div>
                <el-button v-else size="small" plain @click="novelCoverInput?.click()">
                  <el-icon class="mr-1"><Picture /></el-icon>选择封面(可选,可拖入)
                </el-button>
                <span class="text-xs" style="color:var(--mx-text3)">封面应用为每集项目封面</span>
              </div>
              <div class="text-xs" style="color:var(--mx-text3)">「导入目录」合并章节;若所选目录(或其上层)含「封面」文件夹,其中的图片自动识别为系列封面;书名留空 AI 自动识别;拆集 2-6 分钟(日志有实时心跳)</div>
            </template>
          </div>
        </el-form-item>
        <el-form-item label="导入文件(可选)" v-if="!aiMode && !novelMode">
          <div v-if="!pickedFile" class="upload-zone" @click="fileInput?.click()" @dragover.prevent @drop.prevent="onDropFile">
            <el-icon :size="26" color="var(--mx-text3)"><UploadFilled /></el-icon>
            <div class="text-xs mt-1" style="color:var(--mx-text2)">点击或拖入 <b>.txt / .md / .json</b></div>
            <div class="text-xs" style="color:var(--mx-text3)">txt/md 作为剧本源文本;json 识别为分镜方案直接导入</div>
          </div>
          <div v-else class="upload-zone picked">
            <div class="flex items-center gap-2 w-full">
              <el-icon :size="18" :color="pickedFile.kind === 'plan' ? 'var(--mx-green)' : 'var(--mx-cyan)'">
                <Tickets v-if="pickedFile.kind === 'plan'" /><Document v-else />
              </el-icon>
              <div class="flex-1 min-w-0">
                <div class="text-xs font-bold">{{ pickedFile.name }}
                  <el-tag size="small" :type="pickedFile.kind === 'plan' ? 'success' : 'info'" class="ml-1">
                    {{ pickedFile.kind === 'plan' ? '分镜方案' : '源文本' }}
                  </el-tag>
                </div>
                <div class="text-xs truncate" style="color:var(--mx-text3)">{{ pickedFile.preview }}</div>
              </div>
              <el-button text size="small" @click="pickedFile = null"><el-icon><Close /></el-icon></el-button>
            </div>
          </div>
          <input ref="fileInput" type="file" accept=".txt,.md,.json" hidden @change="onPickFile" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dlg = false">取消</el-button>
        <el-button type="primary" :loading="busy || composing || splitting"
          :disabled="(!newName.trim() && !novelMode) || (aiMode && !aiPrompt.trim()) || (novelMode && novelText.trim().length < 200)"
          @click="doCreate">
          {{ splitting ? '拆集中(2-6 分钟)…' : composing ? 'AI 创作中(1-3 分钟)…' : novelMode ? '拆集并创建多集项目' : '创建项目' }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>
