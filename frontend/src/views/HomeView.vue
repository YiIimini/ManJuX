<script setup>
import { G, api, refreshProjects, refreshStatus } from '../store'
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const creating = ref(false)

// ComfyUI 引擎启停(首页快捷控制)
const comfyBusy = ref(false)
const comfyLive = computed(() => !!G.status?.env?.comfy_live)
async function comfyToggle() {
  if (comfyLive.value) {
    try { await ElMessageBox.confirm('停止 ComfyUI?运行中的任务将中断。', '确认', { type: 'warning' }) }
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
const novelCoverInput = ref(null)
const novelCoverURL = computed(() => novelCover.value ? URL.createObjectURL(novelCover.value) : '')
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
}
async function doNovelSplit() {
  if (novelText.value.trim().length < 200) { ElMessage.warning('小说正文太短(至少 200 字)'); return }
  splitting.value = true
  try {
    // 书名留空 → 后端用 LLM 从小说内容自动识别
    const r = await api.novelSplit(newName.value.trim() || '', novelText.value, 0)
    // 封面:上传到每集项目(系列卡取第一集封面,单集卡也各自显示)
    if (novelCover.value) {
      let okc = 0
      for (const e of r.episodes) {
        try {
          const fd = new FormData()
          fd.append('file', novelCover.value)
          const cr = await fetch(`/api/projects/${encodeURIComponent(e.project)}/cover`, { method: 'POST', body: fd })
          if (cr.ok) okc++
        } catch { /* 单集失败不阻塞 */ }
      }
      if (okc) ElMessage.success(`封面已应用到 ${okc} 集`)
    }
    ElMessage.success(`拆集完成:《${r.book}》共 ${r.episodes.length} 集,点击「剧集合集」卡片管理并批量生产`)
    novelText.value = ''
    novelCover.value = null
    dlg.value = false
    await refreshProjects()
  } catch (e) { ElMessage.error(e.message) } finally { splitting.value = false }
}

const totalShots = computed(() => G.projects.reduce((a, p) => a + (p.shots_total || 0), 0))
const doneShots = computed(() => G.projects.reduce((a, p) => a + (p.shots_done || 0), 0))

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

function stateChip(p) {
  if (p.running) return { t: `运行中 · ${p.phase}`, c: 'run' }
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
        <div class="flex gap-7 pb-1">
          <div class="text-center"><div class="stat-num">{{ G.projects.length }}</div><div class="stat-label">项目</div></div>
          <div class="text-center"><div class="stat-num">{{ doneShots }}<span style="font-size:15px;color:var(--mx-text3)">/{{ totalShots }}</span></div><div class="stat-label">镜头</div></div>
        </div>
        <div class="flex flex-col gap-2 items-stretch">
          <div class="flex items-center gap-2 justify-end">
            <span class="envchip"><i class="dot" :class="comfyLive ? 'ok' : 'warn'"></i>COMFY {{ comfyLive ? '在线' : '离线' }}</span>
            <el-button size="small" :type="comfyLive ? 'default' : 'primary'" plain :loading="comfyBusy" @click="comfyToggle">
              <el-icon class="mr-1"><VideoPause v-if="comfyLive" /><VideoPlay v-else /></el-icon>{{ comfyLive ? '停止引擎' : '启动引擎' }}
            </el-button>
          </div>
          <el-button type="primary" size="large" @click="openCreate">
            <el-icon class="mr-1"><Plus /></el-icon>新建项目
          </el-button>
        </div>
      </div>
    </div>

    <!-- 项目网格 -->
    <div class="flex items-center mb-3">
      <h2 class="text-base font-bold m-0">我的项目</h2>
      <span class="timecode ml-3">{{ G.projects.length }} PROJECTS</span>
      <div class="flex-1"></div>
      <template v-if="batch">
        <span class="text-xs mr-2" style="color:var(--mx-amber)">已选 {{ sel.size }} 单元 / {{ selUnitCount() }} 项目</span>
        <el-button size="small" type="danger" :disabled="!sel.size" @click="batchDelete">
          <el-icon class="mr-1"><Delete /></el-icon>删除选中
        </el-button>
        <el-button size="small" @click="batch = false; sel = new Set()">退出批量</el-button>
      </template>
      <el-button v-else size="small" plain :disabled="!G.projects.length" @click="batch = true">
        <el-icon class="mr-1"><Operation /></el-icon>批量管理
      </el-button>
    </div>

    <!-- 批量模式提示条 -->
    <el-alert v-if="batch" type="info" :closable="false" class="mb-3"
      title="批量管理模式:整部小说按书勾选(一键删全部分集),单项目按卡勾选;删除某一集请在剧集合集弹窗里单独操作。" />

    <div v-if="!G.projects.length" class="panel p-14 text-center" style="color:var(--mx-text3)">
      还没有项目 —— 点击「新建项目」开始第一条短视频
    </div>

    <div v-if="batch || seriesGroups.singles.length || Object.keys(seriesGroups.groups).length" class="grid gap-4" style="grid-template-columns:repeat(auto-fill,minmax(252px,1fr))">
      <!-- 剧集系列卡(点击打开逐集弹窗) -->
      <div v-if="!batch" v-for="(eps, prefix) in seriesGroups.groups" :key="'s-' + prefix"
           class="shot-card proj-card cursor-pointer" @click="openSeries(prefix, eps)">
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
          <div class="absolute top-2.5 right-2.5 flex items-center gap-1.5">
            <i class="dot run" v-if="batchStat && batchStat.prefix === prefix"></i>
          </div>
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

      <div v-for="p in (batch ? seriesGroups.singles : seriesGroups.singles)" :key="p.id" class="shot-card proj-card cursor-pointer relative" @click="batch ? toggleSel(p.id) : openProject(p.id)">
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
          <div class="absolute top-2.5 right-2.5">
            <i class="dot" :class="stateChip(p).c"></i>
          </div>
        </div>
        <div class="px-3.5 py-3 flex flex-col gap-2">
          <div class="flex items-center gap-2">
            <b class="truncate flex-1 text-sm">{{ p.name }}</b>
            <span class="timecode" style="font-size:10px">{{ stateChip(p).t }}</span>
            <el-button text size="small" @click.stop="removeProject(p)"><el-icon :size="13"><Delete /></el-icon></el-button>
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
              <div class="text-xs" style="color:var(--mx-text3)">{{ e.running ? ('运行中 · ' + e.phase) : (e.shots_total ? ('镜头 ' + e.shots_done + '/' + e.shots_total) : '待创作') }}</div>
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
              <div class="flex items-center gap-3">
                <div v-if="novelCover" class="flex items-center gap-2 p-1.5 rounded-lg" style="border:1px solid var(--mx-line-hi)">
                  <img :src="novelCoverURL" style="width:34px;height:46px;object-fit:cover;border-radius:5px" />
                  <el-button text size="small" @click="novelCover = null"><el-icon><Close /></el-icon></el-button>
                </div>
                <el-button v-else size="small" plain @click="novelCoverInput?.click()">
                  <el-icon class="mr-1"><Picture /></el-icon>选择封面(可选)
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
