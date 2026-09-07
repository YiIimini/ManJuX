<script setup>
import { onMounted, onUnmounted, computed, ref, reactive } from 'vue'
import { G, api, connectEvents, refreshStatus, refreshProjects } from './store'
import { applyTheme } from './themes'
import { ElMessageBox } from 'element-plus'
import HomeView from './views/HomeView.vue'
import ProjectView from './views/ProjectView.vue'
import SettingsView from './views/SettingsView.vue'
import LibraryView from './views/LibraryView.vue'
import LogConsole from './components/LogConsole.vue'
import ParamsDrawer from './components/ParamsDrawer.vue'
import PromptDrawer from './components/PromptDrawer.vue'
import OutputsView from './views/OutputsView.vue'
import ComfyView from './views/ComfyView.vue'
import MediaViewer from './components/MediaViewer.vue'
import StageDetailDrawer from './components/StageDetailDrawer.vue'

let timer = null
let sysTimer = null
const sysmon = reactive({ cpu: 0, ramPct: 0, ramUsed: 0, ramTotal: 0, gpu: null, coreN: 0 })
const maxed = ref(false)
const collapse = ref(localStorage.getItem('mx-side') === '1')

const NAV = [
  { id: 'home',     label: '项目',   icon: 'HomeFilled' },
  { id: 'project',  label: '工作台', icon: 'Film' },
  { id: 'library',  label: '资产库', icon: 'Collection' },
  { id: 'outputs',  label: '作品库', icon: 'VideoCamera' },
  { id: 'comfy',    label: 'ComfyUI', icon: 'Cpu' },
  { id: 'settings', label: '设置',   icon: 'Setting' },
]

const nav = (v) => {
  G.view = v
  if (v === 'home') G.project = null
}
const toggleSide = () => {
  collapse.value = !collapse.value
  localStorage.setItem('mx-side', collapse.value ? '1' : '0')
}
const win = async (action) => {
  try {
    if (action === 'close') {
      const running = G.status?.running && Object.keys(G.status.running).length > 0
      if (running) {
        await ElMessageBox.confirm(
          '有项目管线正在运行,退出将中止渲染。确定退出吗?',
          '确认退出 ManJuX',
          { confirmButtonText: '仍要退出', cancelButtonText: '继续运行', type: 'warning' })
      }
      await api.quit()
      return
    }
    const r = await api.window(action)
    if (action === 'maximise') maxed.value = !!r.maximised
  } catch { /* 取消或服务未就绪 */ }
}
const gb = (b) => (b / 1024 ** 3).toFixed(1)
const lastLog = computed(() => G.logs.length ? G.logs[G.logs.length - 1] : null)
const verLabel = computed(() => {
  const v = (G.status?.version || '').replace(/^v+/i, '')
  return v ? 'V' + v : 'V…'
})

onMounted(() => {
  applyTheme() // 主题与玻璃偏好(启动即恢复)
  connectEvents()
  refreshStatus()
  refreshProjects()
  timer = setInterval(() => { refreshStatus(); refreshProjects() }, 15000)
  sysTimer = setInterval(async () => {
    try {
      const s = await api.sysmon()
      sysmon.cpu = s.cpu; sysmon.ramPct = s.ramPct
      sysmon.ramUsed = s.ramUsed; sysmon.ramTotal = s.ramTotal
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
        <el-tooltip v-for="n in NAV" :key="n.id" :content="n.label" :disabled="!collapse" placement="right" :show-after="300">
          <button class="side-item" :class="{ on: G.view === n.id }" :disabled="n.id === 'project' && !G.project" @click="nav(n.id)">
            <el-icon :size="17"><component :is="n.icon" /></el-icon>
            <span class="side-label" v-if="!collapse">{{ n.label }}</span>
          </button>
        </el-tooltip>
      </nav>

      <div class="flex-1"></div>

      <!-- 工具区:刷新 / 设置 -->
      <div class="side-tools">
        <el-tooltip content="刷新界面" :disabled="!collapse" placement="right">
          <button class="side-tool" @click="location.reload()">
            <el-icon :size="16"><RefreshRight /></el-icon>
            <span class="side-label" v-if="!collapse">刷新</span>
          </button>
        </el-tooltip>
        <el-tooltip content="设置" :disabled="!collapse" placement="right">
          <button class="side-tool" :class="{ on: G.view === 'settings' }" @click="nav('settings')">
            <el-icon :size="16"><Setting /></el-icon>
            <span class="side-label" v-if="!collapse">设置</span>
          </button>
        </el-tooltip>
        <el-tooltip :content="collapse ? '展开导航' : '收起导航'" placement="right">
          <button class="side-tool" @click="toggleSide">
            <el-icon :size="16"><component :is="collapse ? 'Expand' : 'Fold'" /></el-icon>
          </button>
        </el-tooltip>
      </div>

      <!-- 窗口控制(无边框自绘) -->
      <div class="side-winctl">
        <button class="winbtn" title="最小化" @click="win('minimise')">
          <svg width="11" height="11" viewBox="0 0 11 11"><rect x="1" y="5" width="9" height="1.2" fill="currentColor"/></svg>
        </button>
        <button class="winbtn" :title="maxed ? '还原' : '最大化'" @click="win('maximise')">
          <svg width="11" height="11" viewBox="0 0 11 11"><rect x="1.5" y="1.5" width="8" height="8" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>
        </button>
        <button class="winbtn close" title="关闭" @click="win('close')">
          <svg width="11" height="11" viewBox="0 0 11 11"><path d="M1.5 1.5l8 8M9.5 1.5l-8 8" stroke="currentColor" stroke-width="1.3"/></svg>
        </button>
      </div>
    </aside>

    <!-- ▎右侧主列 -->
    <div class="maincol">
      <!-- 顶栏:中央系统状态;空白区为窗口拖动区 -->
      <header class="topbar2" style="--wails-draggable:drag" @dblclick.self="win('maximise')">
        <div class="sysbar" v-if="sysmon.ramTotal">
          <span class="sys-chip" :title="`${sysmon.coreN} 逻辑核心`">
            <i class="dot" :class="sysmon.cpu > 85 ? 'bad' : 'ok'"></i>CPU
            <b :class="{ hot: sysmon.cpu > 85 }">{{ sysmon.cpu.toFixed(0) }}%</b>
          </span>
          <span class="sys-chip" title="内存占用">
            <i class="dot" :class="sysmon.ramPct > 90 ? 'bad' : 'ok'"></i>RAM
            <b :class="{ hot: sysmon.ramPct > 90 }">{{ sysmon.ramPct.toFixed(0) }}%</b>
            <em>{{ gb(sysmon.ramUsed) }}/{{ gb(sysmon.ramTotal) }}G</em>
          </span>
          <span class="sys-chip" v-if="sysmon.gpu" :title="sysmon.gpu.name">
            <i class="dot" :class="sysmon.gpu.util > 95 ? 'bad' : 'ok'"></i>GPU
            <b :class="{ hot: sysmon.gpu.util > 95 }">{{ sysmon.gpu.util.toFixed(0) }}%</b>
            <em>{{ (sysmon.gpu.vramU / 1024).toFixed(1) }}/{{ (sysmon.gpu.vramT / 1024).toFixed(0) }}G</em>
            <em :class="{ hot: sysmon.gpu.temp > 85 }">{{ sysmon.gpu.temp.toFixed(0) }}℃</em>
          </span>
          <span class="sys-chip dim" v-else title="未检测到 NVIDIA GPU">
            <i class="dot warn"></i>GPU <em>—</em>
          </span>
        </div>
      </header>

      <main class="flex-1 overflow-hidden">
        <HomeView v-if="G.view === 'home'" />
        <ProjectView v-else-if="G.view === 'project' && G.project" />
        <LibraryView v-else-if="G.view === 'library'" />
        <OutputsView v-else-if="G.view === 'outputs'" />
        <ComfyView v-else-if="G.view === 'comfy'" />
        <SettingsView v-else-if="G.view === 'settings'" />
      </main>

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
        <div class="vdiv"></div>
        <span class="timecode">{{ verLabel }}</span>
        <div class="flex-1"></div>
        <!-- 运行日志 ticker:最新一条,点击展开/收起日志面板 -->
        <button class="logticker" :class="{ open: G.logPanelOpen }" @click="G.logPanelOpen = !G.logPanelOpen"
          title="运行日志(点击展开)">
          <span class="rec-dot" v-if="lastLog"></span>
          <span class="tick-text" v-if="lastLog" :class="lastLog.level">{{ lastLog.message }}</span>
          <span class="tick-text" v-else style="color:#46536f">运行日志待命…</span>
          <el-icon :size="12"><ArrowUp v-if="G.logPanelOpen" /><ArrowDown v-else /></el-icon>
        </button>
      </footer>

      <!-- 悬浮日志面板(底部状态栏上方弹出,随侧栏收起/展开自适应) -->
      <Transition name="logslide">
        <LogConsole v-if="G.logPanelOpen" />
      </Transition>
    </div>

    <ParamsDrawer />
    <PromptDrawer />
    <StageDetailDrawer />
    <MediaViewer />
  </div>
</template>
