<script setup>
import { onMounted, onUnmounted, computed, ref, reactive } from 'vue'
import { G, api, connectEvents, refreshStatus, refreshProjects, refreshProject, logDot } from './store'
import { ElMessageBox } from 'element-plus'
import { applyTheme } from './themes'
import HomeView from './views/HomeView.vue'
import ProjectView from './views/ProjectView.vue'
import SettingsView from './views/SettingsView.vue'
import LibraryView from './views/LibraryView.vue'
import LogConsole from './components/LogConsole.vue'
import ParamsDrawer from './components/ParamsDrawer.vue'
import PromptDrawer from './components/PromptDrawer.vue'
import OutputsView from './views/OutputsView.vue'
import NovelsView from './views/NovelsView.vue'
import ComfyView from './views/ComfyView.vue'
import MediaViewer from './components/MediaViewer.vue'
import StageDetailDrawer from './components/StageDetailDrawer.vue'
import ShotTuneDialog from './components/ShotTuneDialog.vue'

let timer = null
let sysTimer = null
const sysmon = reactive({ cpu: 0, cores: [], ramPct: 0, ramUsed: 0, ramTotal: 0, ramAvail: 0, gpu: null, coreN: 0 })
const maxed = ref(false)
const collapse = ref(localStorage.getItem('mx-side') === '1')

// 导航基础定义;用户可在「编辑菜单」里自定义名称与顺序(存 localStorage)
const NAV_BASE = [
  { id: 'home',    label: '项目',    icon: 'HomeFilled' },
  { id: 'novels',  label: '小说库',  icon: 'Reading' },
  { id: 'project', label: '工作台',  icon: 'Film' },
  { id: 'library', label: '资产库',  icon: 'Collection' },
  { id: 'outputs', label: '作品库',  icon: 'VideoCamera' },
  { id: 'comfy',   label: 'ComfyUI', icon: 'Cpu' },
]
const navPrefs = JSON.parse(localStorage.getItem('mx-nav') || 'null')
const navList = computed(() => {
  if (!Array.isArray(navPrefs) || !navPrefs.length) return NAV_BASE
  const map = new Map(NAV_BASE.map(n => [n.id, n]))
  const out = []
  for (const p of navPrefs) {
    const base = map.get(p.id)
    if (base) { out.push({ ...base, label: (p.label || base.label).trim() || base.label }); map.delete(p.id) }
  }
  for (const rest of map.values()) out.push(rest) // 新增项兜底追加
  return out
})

// 编辑菜单弹窗
const navEdit = ref(false)
const navDraft = ref([]) // [{id, label}]
function openNavEdit() {
  navDraft.value = navList.value.map(n => ({ id: n.id, label: n.label }))
  navEdit.value = true
}
function navMove(i, d) {
  const j = i + d
  if (j < 0 || j >= navDraft.value.length) return
  const arr = navDraft.value
  ;[arr[i], arr[j]] = [arr[j], arr[i]]
  navDraft.value = [...arr]
}
function navReset() {
  navDraft.value = NAV_BASE.map(n => ({ id: n.id, label: n.label }))
}
function navSave() {
  localStorage.setItem('mx-nav', JSON.stringify(navDraft.value))
  location.reload() // 简单可靠:重载即生效(偏好已落盘)
}

const nav = (v) => {
  G.view = v
  if (v === 'home') G.project = null
}
const toggleSide = () => {
  collapse.value = !collapse.value
  localStorage.setItem('mx-side', collapse.value ? '1' : '0')
}

// 刷新当前视图内容(非整页 reload):home 拉项目表,project 拉详情,其余视图 tick 自刷新
function refreshCurrent() {
  refreshStatus()
  switch (G.view) {
    case 'home': refreshProjects(); break
    case 'project': refreshProject(); break
    case 'library': G.tick.library++; break
    case 'outputs': G.tick.outputs++; break
    case 'comfy': G.tick.comfy++; break
    case 'settings': G.tick.settings++; break
    case 'novels': G.tick.novels++; break
  }
}

const win = async (action) => {
  try {
    if (action === 'close') {
      closeDlg.value = true
      return
    }
    const r = await api.window(action)
    if (action === 'maximise') maxed.value = !!r.maximised
  } catch { /* 取消或服务未就绪 */ }
}

// 关闭确认:后台运行 / 直接退出
const closeDlg = ref(false)
const runningCount = computed(() => G.status?.running ? Object.keys(G.status.running).length : 0)
async function hideToTray() {
  closeDlg.value = false
  try { await api.window('hide') } catch { /* ignore */ }
}
async function quitNow() {
  closeDlg.value = false
  try { await api.quit() } catch { /* ignore */ }
}
const gb = (b) => (b / 1024 ** 3).toFixed(1)
const lastLog = computed(() => G.logs.length ? G.logs[G.logs.length - 1] : null)
const projName = (id) => {
  const p = (G.projects || []).find(x => x.id === id)
  return p ? p.name : id
}
const verLabel = computed(() => {
  const v = (G.status?.version || '').replace(/^v+/i, '')
  return v ? 'V' + v : 'V…'
})
// 占用分级:60/85 双阈值 → 绿/琥珀/红
const loadCls = (v) => v > 85 ? 'hot' : (v > 60 ? 'warm' : 'cool')
// 传感器无数据([N/A])统一显示 —
const na = (s) => (s || '—').replaceAll('[N/A]', '—')

onMounted(() => {
  applyTheme() // 主题与玻璃偏好(启动即恢复)
  connectEvents()
  refreshStatus()
  refreshProjects()
  timer = setInterval(() => { refreshStatus(); refreshProjects() }, 15000)
  sysTimer = setInterval(async () => {
    try {
      const s = await api.sysmon()
      sysmon.cpu = s.cpu; sysmon.cores = s.cores || []; sysmon.ramPct = s.ramPct
      sysmon.ramUsed = s.ramUsed; sysmon.ramTotal = s.ramTotal; sysmon.ramAvail = s.ramAvail
      sysmon.gpu = s.gpu || null; sysmon.coreN = s.coreN || 0
    } catch { /* 服务重启中 */ }
  }, 2000)
  // 桌面模式:管线运行中拦截关窗(Alt+F4 / 后端事件)→ 二次确认
  if (window.runtime?.EventsOn) {
    window.runtime.EventsOn('confirm-quit', async () => {
      try {
        await ElMessageBox.confirm(
          '有项目管线正在运行,退出将中止渲染。确定退出吗?',
          '确认退出 ManJuX',
          { confirmButtonText: '仍要退出', cancelButtonText: '继续运行', type: 'warning' })
        await api.quit()
      } catch { /* 取消=继续运行 */ }
    })
  }
})
onUnmounted(() => { clearInterval(timer); clearInterval(sysTimer) })

const env = computed(() => G.status?.env)
</script>

<template>
  <div class="shell">
    <!-- ▎左侧导航 -->
    <aside class="sidebar" :class="{ collapsed: collapse }">
      <div class="side-brand" @click="nav('home')">
        <svg width="30" height="30" viewBox="0 0 48 48" fill="none" class="flex-none">
          <rect x="3" y="3" width="42" height="42" rx="10" fill="#10141F" stroke="url(#g1)" stroke-width="2.5"/>
          <path d="M15 32V16l6.5 9L28 16v16" stroke="#FFD08A" stroke-width="3.5" stroke-linecap="round" stroke-linejoin="round"/>
          <path d="M32 16v16" stroke="#53D8FF" stroke-width="3.5" stroke-linecap="round"/>
          <defs><linearGradient id="g1" x1="0" y1="0" x2="48" y2="48">
            <stop stop-color="#FFD08A"/><stop offset="1" stop-color="#F97A1F"/>
          </linearGradient></defs>
        </svg>
        <div class="side-title" v-if="!collapse">
          <div class="brand">ManJuX</div>
          <div class="brand-sub">Studio Console</div>
        </div>
      </div>

      <nav class="side-nav">
        <el-tooltip v-for="n in navList" :key="n.id" :content="n.label" :disabled="!collapse" placement="right" :show-after="300">
          <button class="side-item" :class="{ on: G.view === n.id }" :disabled="n.id === 'project' && !G.project" @click="nav(n.id)">
            <el-icon :size="17"><component :is="n.icon" /></el-icon>
            <span class="side-label" v-if="!collapse">{{ n.label }}</span>
          </button>
        </el-tooltip>
      </nav>

      <div class="flex-1"></div>

      <div class="side-tools">
        <!-- 收起/展开保持原样;编辑菜单(纯图标)固定在其右侧,行内右对齐 -->
        <div v-if="!collapse" class="flex items-center w-full">
          <el-tooltip content="收起导航" placement="top">
            <button class="side-tool" style="flex:1" @click="toggleSide">
              <el-icon :size="16"><Fold /></el-icon>
              <span class="side-label">收起</span>
            </button>
          </el-tooltip>
          <el-tooltip content="编辑菜单(自定义名称与顺序)" placement="top">
            <button class="side-tool" style="padding:8px 10px" @click="openNavEdit">
              <el-icon :size="16"><Menu /></el-icon>
            </button>
          </el-tooltip>
        </div>
        <div v-else class="flex items-center w-full">
          <el-tooltip content="展开导航" placement="right">
            <button class="side-tool" style="flex:1" @click="toggleSide">
              <el-icon :size="16"><Expand /></el-icon>
            </button>
          </el-tooltip>
          <el-tooltip content="编辑菜单(自定义名称与顺序)" placement="right">
            <button class="side-tool" style="padding:8px 0" @click="openNavEdit">
              <el-icon :size="16"><Menu /></el-icon>
            </button>
          </el-tooltip>
        </div>
      </div>
    </aside>

    <!-- ▎右侧主列 -->
    <div class="maincol">
      <!-- 顶栏:中央系统硬件状态;空白区为窗口拖动区;右上窗口控制 -->
      <header class="topbar2" style="--wails-draggable:drag">
        <div class="topbar-left"></div>
        <div class="sysbar" v-if="sysmon.ramTotal">
          <el-tooltip placement="bottom" :show-after="200">
            <template #content>
              <div class="sys-tip">
                <b>CPU {{ sysmon.cpu.toFixed(1) }}%</b> · {{ sysmon.coreN }} 逻辑核心
                <div class="core-grid">
                  <i v-for="(c, i) in sysmon.cores" :key="i" :style="{ height: Math.max(8, c) + '%' }"
                     :class="c > 85 ? 'h' : (c > 60 ? 'w' : '')" :title="'核心' + (i + 1) + ' ' + c + '%'"></i>
                </div>
                <span class="dim">每核占用实时(悬停单格看数值)</span>
              </div>
            </template>
            <div class="sys-mod" :class="loadCls(sysmon.cpu)">
              <svg class="sys-ico" viewBox="0 0 24 24" fill="none"><rect x="6" y="6" width="12" height="12" rx="2.5" stroke="currentColor" stroke-width="1.8"/><path d="M10 3v2M14 3v2M10 19v2M14 19v2M3 10h2M3 14h2M19 10h2M19 14h2" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>
              <span class="sys-v">{{ sysmon.cpu.toFixed(0) }}<i>%</i></span>
              <span class="sys-bar"><b :style="{ width: sysmon.cpu + '%' }"></b></span>
            </div>
          </el-tooltip>
          <el-tooltip placement="bottom" :show-after="200">
            <template #content>
              <div class="sys-tip">
                <b>内存 {{ sysmon.ramPct.toFixed(1) }}%</b>
                <span>已用 {{ gb(sysmon.ramUsed) }} GB / 总量 {{ gb(sysmon.ramTotal) }} GB</span>
                <span>可用 {{ gb(sysmon.ramAvail) }} GB</span>
              </div>
            </template>
            <div class="sys-mod" :class="loadCls(sysmon.ramPct)">
              <svg class="sys-ico" viewBox="0 0 24 24" fill="none"><rect x="4" y="7" width="16" height="10" rx="2.5" stroke="currentColor" stroke-width="1.8"/><path d="M8 7v4M12 7v4M16 7v4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>
              <span class="sys-v">{{ sysmon.ramPct.toFixed(0) }}<i>%</i></span>
              <span class="sys-bar"><b :style="{ width: sysmon.ramPct + '%' }"></b></span>
              <span class="sys-sub">{{ gb(sysmon.ramUsed) }}/{{ gb(sysmon.ramTotal) }}G</span>
            </div>
          </el-tooltip>
          <el-tooltip v-if="sysmon.gpu" placement="bottom" :show-after="200">
            <template #content>
              <div class="sys-tip">
                <b>{{ sysmon.gpu.name || 'GPU' }}</b>
                <span>计算占用 {{ sysmon.gpu.util.toFixed(0) }}% · 显存带宽 {{ sysmon.gpu.memUtil.toFixed(0) }}%</span>
                <span>显存 {{ (sysmon.gpu.vramU / 1024).toFixed(2) }} / {{ (sysmon.gpu.vramT / 1024).toFixed(1) }} GB({{ (sysmon.gpu.vramU * 100 / Math.max(1, sysmon.gpu.vramT)).toFixed(0) }}%)</span>
                <span>温度 {{ sysmon.gpu.temp.toFixed(0) }}℃ · 功耗 {{ na(sysmon.gpu.power) }} · 风扇 {{ na(sysmon.gpu.fan) }}</span>
              </div>
            </template>
            <div class="sys-mod" :class="loadCls(sysmon.gpu.util)">
              <svg class="sys-ico" viewBox="0 0 24 24" fill="none"><path d="M3 8.5A2.5 2.5 0 0 1 5.5 6h11A2.5 2.5 0 0 1 19 8.5V15a3 3 0 0 1-3 3h-6.5A3.5 3.5 0 0 1 6 14.5V13H4.5A1.5 1.5 0 0 1 3 11.5v-3Z" stroke="currentColor" stroke-width="1.8"/><circle cx="13.5" cy="11" r="2.4" stroke="currentColor" stroke-width="1.8"/><path d="M19 9h2M19 12h2" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>
              <span class="sys-v">{{ sysmon.gpu.util.toFixed(0) }}<i>%</i></span>
              <span class="sys-bar"><b :style="{ width: sysmon.gpu.util + '%' }"></b></span>
              <span class="sys-sub">{{ (sysmon.gpu.vramU / 1024).toFixed(1) }}/{{ (sysmon.gpu.vramT / 1024).toFixed(0) }}G</span>
              <span class="sys-temp" :class="{ hot: sysmon.gpu.temp > 85 }">{{ sysmon.gpu.temp.toFixed(0) }}°</span>
            </div>
          </el-tooltip>
          <div class="sys-mod dim" v-else title="未检测到 NVIDIA GPU">
            <svg class="sys-ico" viewBox="0 0 24 24" fill="none"><path d="M3 8.5A2.5 2.5 0 0 1 5.5 6h11A2.5 2.5 0 0 1 19 8.5V15a3 3 0 0 1-3 3h-6.5A3.5 3.5 0 0 1 6 14.5V13H4.5A1.5 1.5 0 0 1 3 11.5v-3Z" stroke="currentColor" stroke-width="1.8"/></svg>
            <span class="sys-v sys-na">GPU<i>—</i></span>
          </div>
        </div>

        <!-- 右上:刷新 / 设置 / 最小化 / 最大化 / 关闭 -->
        <div class="topctl">
          <el-tooltip content="刷新当前页面内容" placement="bottom">
            <button class="topbtn" @click="refreshCurrent">
              <el-icon :size="15"><RefreshRight /></el-icon>
            </button>
          </el-tooltip>
          <el-tooltip content="设置" placement="bottom">
            <button class="topbtn" :class="{ on: G.view === 'settings' }" @click="nav('settings')">
              <el-icon :size="15"><Setting /></el-icon>
            </button>
          </el-tooltip>
          <span class="topctl-sep"></span>
          <button class="topbtn" title="最小化" @click="win('minimise')">
            <svg width="12" height="12" viewBox="0 0 12 12"><rect x="1.5" y="5.5" width="9" height="1.2" fill="currentColor"/></svg>
          </button>
          <button class="topbtn" :title="maxed ? '还原' : '最大化'" @click="win('maximise')">
            <svg v-if="!maxed" width="12" height="12" viewBox="0 0 12 12"><rect x="1.8" y="1.8" width="8.4" height="8.4" rx="1" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>
            <svg v-else width="12" height="12" viewBox="0 0 12 12"><rect x="1.5" y="3.5" width="7" height="7" rx="1" fill="none" stroke="currentColor" stroke-width="1.2"/><path d="M3.6 3.6V2.4A.9.9 0 0 1 4.5 1.5h5.1A.9.9 0 0 1 10.5 2.4v5.1a.9.9 0 0 1-.9.9H8.4" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>
          </button>
          <button class="topbtn close" title="关闭" @click="win('close')">
            <svg width="12" height="12" viewBox="0 0 12 12"><path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.3"/></svg>
          </button>
        </div>
      </header>

      <div class="flex-1 flex flex-col min-w-0">
        <main class="flex-1 overflow-hidden">

        <HomeView v-if="G.view === 'home'" />
        <ProjectView v-else-if="G.view === 'project' && G.project" />
        <LibraryView v-else-if="G.view === 'library'" />
        <NovelsView v-else-if="G.view === 'novels'" />
        <OutputsView v-else-if="G.view === 'outputs'" />
        <ComfyView v-else-if="G.view === 'comfy'" />
        <SettingsView v-else-if="G.view === 'settings'" />

        </main>
        <!-- 日志面板:内容列内嵌(展开压内容,不遮右侧产线栏) -->
        <Transition name="logslide">
          <LogConsole v-if="G.logPanelOpen" />
        </Transition>
      </div>

      <!-- 底部状态栏 -->
      <footer class="statusbar">
        <div class="flex items-center gap-1.5" v-if="env">
          <span class="envchip" title="文本大模型(剧本直出)">
            <i class="dot" :class="env.llm_ready ? 'ok' : 'bad'"></i>LLM
          </span>
          <span class="envchip" title="本地 ComfyUI 渲染引擎">
            <i class="dot" :class="env.comfy_live ? 'ok' : 'warn'"></i>COMFY
          </span>
          <span class="envchip" title="MiniMax H3 云端 API">
            <i class="dot" :class="env.minimax_ready ? 'ok' : 'warn'"></i>H3·API
          </span>
          <span class="envchip" title="ffmpeg(合成/质检)">
            <i class="dot" :class="env.ffmpeg ? 'ok' : 'bad'"></i>FFMPEG
          </span>
        </div>
        <div class="flex-1"></div>
        <!-- 运行日志 ticker:最新一条,点击展开/收起日志面板 -->
        <button class="logticker" :class="{ open: G.logPanelOpen }" @click="G.logPanelOpen = !G.logPanelOpen"
          title="运行日志(点击展开)">
          <span class="tick-dot" :class="logDot" v-if="G.eventsOpen"></span>
          <span class="tick-text" v-if="!G.eventsOpen" style="color:var(--mx-yellow)">日志连接中断,重连中…</span>
          <span class="tick-text" v-else-if="lastLog" :class="lastLog.level">
            <template v-if="lastLog.project_id && lastLog.project_id !== (G.project?.project?.id)">【{{ projName(lastLog.project_id) }}】</template>{{ lastLog.message }}
          </span>
          <span class="tick-text" v-else style="color:#46536f">运行日志待命…</span>
          <el-icon :size="12"><ArrowUp v-if="G.logPanelOpen" /><ArrowDown v-else /></el-icon>
        </button>
        <div class="vdiv"></div>
        <span class="timecode verchip">{{ verLabel }}</span>
      </footer>

    </div>

    <ParamsDrawer />
    <PromptDrawer />
    <StageDetailDrawer />
    <ShotTuneDialog />
    <MediaViewer />

    <!-- 编辑左侧导航:自定义名称与顺序 -->
    <el-dialog v-model="navEdit" title="编辑左侧菜单" width="420px" append-to-body>
      <div class="flex flex-col gap-2">
        <div v-for="(n, i) in navDraft" :key="n.id" class="flex items-center gap-2">
          <span class="timecode" style="width:20px">{{ i + 1 }}</span>
          <el-input v-model="n.label" maxlength="12" :placeholder="NAV_BASE.find(b => b.id === n.id)?.label" />
          <el-button text size="small" :disabled="i === 0" @click="navMove(i, -1)"><el-icon><ArrowUp /></el-icon></el-button>
          <el-button text size="small" :disabled="i === navDraft.length - 1" @click="navMove(i, 1)"><el-icon><ArrowDown /></el-icon></el-button>
        </div>
      </div>
      <div class="text-xs mt-3" style="color:var(--mx-text3)">名称留空则用默认名;设置入口固定在下方工具区,不参与排序。</div>
      <template #footer>
        <el-button @click="navReset">恢复默认</el-button>
        <el-button @click="navEdit = false">取消</el-button>
        <el-button type="primary" @click="navSave">保存</el-button>
      </template>
    </el-dialog>

    <!-- 关闭确认:后台运行 / 直接退出 -->
    <el-dialog v-model="closeDlg" title="关闭 ManJuX" width="480px" append-to-body>
      <div v-if="runningCount > 0" class="text-xs p-3 mb-4 rounded-lg"
           style="background:rgba(255,180,84,.08);border:1px solid rgba(255,180,84,.35);color:var(--mx-amber-hi)">
        ⚠ 有 {{ runningCount }} 个项目管线正在运行,直接退出将中止渲染任务。
      </div>
      <div class="grid gap-3" style="grid-template-columns:1fr 1fr">
        <button class="close-opt" @click="hideToTray">
          <el-icon :size="26" color="var(--mx-cyan)"><Moon /></el-icon>
          <b>最小化后台运行</b>
          <span>窗口隐藏,管线继续跑;再点应用图标自动唤回</span>
        </button>
        <button class="close-opt danger" @click="quitNow">
          <el-icon :size="26" color="var(--mx-red)"><SwitchButton /></el-icon>
          <b>直接退出</b>
          <span>停止全部管线,ComfyUI 引擎同步退出</span>
        </button>
      </div>
      <template #footer>
        <el-button @click="closeDlg = false">取消</el-button>
      </template>
    </el-dialog>
  </div>
</template>
