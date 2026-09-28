<script setup>
import { G, api, refreshProjects, refreshStatus, fileToB64 } from '../store'
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import HomeProjectGrid from '../components/HomeProjectGrid.vue'
import HomeCreateDialog from '../components/HomeCreateDialog.vue'
import HomeSeriesDialog from '../components/HomeSeriesDialog.vue'

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
    const d = await api.comfyAction(act)
    ElMessage.success(act === 'start' ? 'ComfyUI 启动指令已下发,引擎预热中…' : 'ComfyUI 已停止')
    setTimeout(refreshStatus, act === 'start' ? 12000 : 1500)
  } catch (e) { ElMessage.error(e.message) } finally { comfyBusy.value = false }
}

// 新建项目(带文件上传:txt/md 源文本,json 分镜方案)
// 弹窗本体在 HomeCreateDialog(参数录入),此处只保留创建/拆集业务流程
const dlg = ref(false)
const newName = ref('')
const pickedFile = ref(null)   // {name, text, kind: 'text'|'plan', plan?, preview}
const busy = ref(false)
// AI 现场创作模式(曾漏定义三个 ref,致弹窗渲染崩溃/AI 开关失灵)
const aiMode = ref(false)
const aiPrompt = ref('')
const composing = ref(false)
// 整本小说批量模式
const novelMode = ref(false)
const novelText = ref('')
const splitting = ref(false)
const novelCover = ref(null)   // File:系列封面(目录「封面」文件夹自动识别 或 手动选择)
const novelCoverPath = ref('') // 桌面拖放(wails 原生)的本机封面路径
// computed 里调 createObjectURL 是错的:computed 会在每次依赖变化时重新求值,
// 于是每换一次封面就新建一个 blob URL 且永不释放。改为 watch 显式管理生命周期,
// 换图时让旧 URL 失效。
const novelCoverObjectURL = ref('')
watch(novelCover, (f) => {
  if (novelCoverObjectURL.value) URL.revokeObjectURL(novelCoverObjectURL.value)
  novelCoverObjectURL.value = f ? URL.createObjectURL(f) : ''
})
const novelCoverURL = computed(() => novelCoverObjectURL.value || (novelCoverPath.value ? '/api/thumb?path=' + encodeURIComponent(novelCoverPath.value) : ''))

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
            await api.uploadCover(e.project, payload)
            okc++
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
    const r = await api.novelJobs()
    novelJobs.value = r.jobs || []
  } catch { /* */ }
}
// 移除失败占位卡(仅清除提示;运行中的任务卡不可移除)
async function clearJob(j) {
  if (j.state !== 'failed') return
  try {
    await api.novelJobDelete(j.series)
    ElMessage.success('已移除失败占位卡')
    refreshJobs()
  } catch (e) { ElMessage.error(e.message) }
}
let jobsTimer = null
onMounted(() => {
  refreshJobs()
  jobsTimer = setInterval(refreshJobs, 8000)
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
// 统一清理。此前三处泄漏:jobsTimer 每次切回首页都会新增一个且永不回收
// (累积 N 个 8s 轮询并发打后端),批量运行中离开页面会让 batchTimer 永久轮询,
// 封面 blob URL 也从不释放。
onUnmounted(() => {
  if (jobsTimer) clearInterval(jobsTimer)
  if (batchTimer) { clearInterval(batchTimer); batchTimer = null }
  if (novelCoverObjectURL.value) URL.revokeObjectURL(novelCoverObjectURL.value)
})
async function startBatch(prefix) {
  try {
    const r = await api.novelBatchStart(prefix)
    ElMessage.success(`《${r.prefix || prefix}》批量产线已启动:共 ${r.total} 集,逐集串行生产`)
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

// openImport 导入技能端产出的**项目包目录**（单集包或整个系列根）。
//
// 为什么要有这个按钮：后端 `POST /api/projects/import-package` 一直是完整的
// （支持单集包、系列根、按 series.json 对账、整批校验后落盘），连用例都是按
// "用户点导入"写的 —— 但界面上没有入口，用户只能自己把包拷进 data/projects，
// 而那正是端点注释里说要避免的事。
const importing = ref(false)
async function openImport() {
  let dir = ''
  try {
    const r = await ElMessageBox.prompt(
      '填技能端产出的**项目包目录**（单集包＝含 plan/plan.json 的那一层；整个系列＝含 series.json 的根目录）。' +
      '平台会把包拷进数据目录并注册，不用你手动搬文件。',
      '导入项目包', { inputPlaceholder: '例如 D:\\Ai\\Datas\\漫剧制作\\输出\\名册之外，八万零一号', confirmButtonText: '导入', cancelButtonText: '取消' })
    dir = (r.value || '').trim()
  } catch { return }
  if (!dir) return
  importing.value = true
  try {
    await api.importPackage(dir)
    ElMessage.success('导入完成（逐集校验，坏包会单独报出原因）')
    await refreshProjects()
  } catch (e) {
    ElMessage.error('导入失败：' + e.message)
  } finally {
    importing.value = false
  }
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
  try { const r = await api.novelBatchStart(prefix); ElMessage.success(`《${r.prefix || prefix}》批量产线已启动(从中断处续跑)`) }
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
  // 批量产线正在跑这个系列时不许删(2026-09-24 修):旧写法是 `batchStat.prefix`,
  // 而 batchStat 是 ref —— `.prefix` 恒为 undefined,守卫**永不触发**,
  // 于是"正在逐集渲染"的整部系列能被一键删掉(把正在跑的集也删了)。
  // 脚本里读 ref 必须解引用 `.value`;模板会自动解包,别照抄模板写法。
  if (batchStat.value?.prefix === prefix) {
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
          <el-tooltip content="把技能端产出的项目包目录拉进来(单集包或整个系列根)" placement="bottom">
            <el-button size="large" plain :loading="importing" @click="openImport">
              <el-icon class="mr-1"><FolderOpened /></el-icon>导入项目包
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

    <!-- 项目卡网格:项目/剧集卡、批量勾选、拆集占位卡(子组件) -->
    <HomeProjectGrid
      :series-groups="seriesGroups"
      :novel-jobs="novelJobs"
      :batch="batch"
      :sel="sel"
      :batch-stat="batchStat"
      :running="G.status?.running || {}"
      @open-project="openProject"
      @open-series="openSeries"
      @toggle-sel="toggleSel"
      @clear-job="clearJob"
      @start-project-run="startProjectRun"
      @stop-project-run="stopProjectRun"
      @remove-project="removeProject"
      @start-series-batch="startSeriesBatch"
      @stop-series-batch="stopSeriesBatch"
      @remove-series="removeSeries" />

    <!-- 剧集合集弹窗(子组件) -->
    <HomeSeriesDialog v-model:series-dlg="seriesDlg" :batch-stat="batchStat"
      @open-project="openProject" @del-episode="delEpisode"
      @start-batch="startBatch" @stop-batch="stopBatch" />


    <!-- 新建项目弹窗(子组件:三模式参数录入) -->
    <HomeCreateDialog
      v-model:dlg="dlg"
      v-model:new-name="newName"
      v-model:picked-file="pickedFile"
      v-model:ai-mode="aiMode"
      v-model:ai-prompt="aiPrompt"
      v-model:novel-mode="novelMode"
      v-model:novel-text="novelText"
      v-model:novel-cover="novelCover"
      v-model:novel-cover-path="novelCoverPath"
      :novel-cover-url="novelCoverURL"
      :busy="busy" :composing="composing" :splitting="splitting"
      @create="doCreate" />
  </div>
</template>
