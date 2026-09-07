<script setup>
import { onMounted, onUnmounted, computed } from 'vue'
import { G, api, connectEvents, refreshStatus, refreshProjects } from './store'
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
onMounted(() => {
  connectEvents()
  refreshStatus()
  refreshProjects()
  timer = setInterval(() => { refreshStatus(); refreshProjects() }, 15000)
  // 桌面模式:管线运行中拦截关窗 → 此处二次确认
  if (window.runtime?.EventsOn) {
    window.runtime.EventsOn('confirm-quit', async () => {
      try {
        await ElMessageBox.confirm(
          '有项目管线正在运行,退出将中止渲染。确定退出吗?',
          '确认退出 ManJuX',
          { confirmButtonText: '仍要退出', cancelButtonText: '继续运行', type: 'warning' })
        await fetch('/api/quit', { method: 'POST' })
      } catch { /* 取消=继续运行 */ }
    })
  }
})
onUnmounted(() => clearInterval(timer))

const env = computed(() => G.status?.env)
const nav = (v) => { G.view = v; if (v === 'home') G.project = null }
</script>

<template>
  <div class="app flex flex-col h-full">
    <!-- ▎顶栏 -->
    <header class="topbar flex items-center gap-4 px-5 flex-none">
      <div class="flex items-center gap-2.5 cursor-pointer select-none" @click="nav('home')">
        <svg width="26" height="26" viewBox="0 0 48 48" fill="none">
          <rect x="3" y="3" width="42" height="42" rx="10" fill="#10141F" stroke="url(#g1)" stroke-width="2.5"/>
          <path d="M15 32V16l6.5 9L28 16v16" stroke="#FFD08A" stroke-width="3.5" stroke-linecap="round" stroke-linejoin="round"/>
          <path d="M32 16v16" stroke="#53D8FF" stroke-width="3.5" stroke-linecap="round"/>
          <defs><linearGradient id="g1" x1="0" y1="0" x2="48" y2="48">
            <stop stop-color="#FFD08A"/><stop offset="1" stop-color="#F97A1F"/>
          </linearGradient></defs>
        </svg>
        <div class="leading-none">
          <div class="brand">ManJuX</div>
          <div class="brand-sub mt-0.5">Studio Console</div>
        </div>
      </div>

      <!-- 分段导航 -->
      <nav class="navseg ml-3">
        <button class="seg" :class="{ on: G.view === 'home' }" @click="nav('home')">
          <el-icon :size="14"><HomeFilled /></el-icon>项目
        </button>
        <button class="seg" :class="{ on: G.view === 'project' }" :disabled="!G.project" @click="G.view = 'project'">
          <el-icon :size="14"><Film /></el-icon>工作台
        </button>
        <button class="seg" :class="{ on: G.view === 'library' }" @click="G.view = 'library'">
          <el-icon :size="14"><Collection /></el-icon>资产库
        </button>
        <button class="seg" :class="{ on: G.view === 'outputs' }" @click="G.view = 'outputs'">
          <el-icon :size="14"><Film /></el-icon>作品库
        </button>
        <button class="seg" :class="{ on: G.view === 'comfy' }" @click="G.view = 'comfy'">
          <el-icon :size="14"><Cpu /></el-icon>ComfyUI
        </button>
        <button class="seg" :class="{ on: G.view === 'settings' }" @click="G.view = 'settings'">
          <el-icon :size="14"><Setting /></el-icon>设置
        </button>
      </nav>

      <div class="flex-1"></div>

      <!-- 环境状态 chip 组 -->
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
      <span class="timecode">{{ G.status?.version || '…' }}</span>

      <el-tooltip content="运行日志" placement="bottom">
        <button class="seg" style="padding:6px 9px" @click="G.logPanelOpen = !G.logPanelOpen">
          <el-icon :size="15"><Monitor /></el-icon>
        </button>
      </el-tooltip>
    </header>
    <div class="film-strip flex-none"></div>

    <!-- ▎主区 -->
    <main class="flex-1 overflow-hidden">
      <HomeView v-if="G.view === 'home'" />
      <ProjectView v-else-if="G.view === 'project' && G.project" />
      <LibraryView v-else-if="G.view === 'library'" />
      <OutputsView v-else-if="G.view === 'outputs'" />
      <ComfyView v-else-if="G.view === 'comfy'" />
      <SettingsView v-else-if="G.view === 'settings'" />
    </main>

    <LogConsole v-if="G.logPanelOpen" />
    <ParamsDrawer />
    <PromptDrawer />
    <StageDetailDrawer />
    <MediaViewer />
  </div>
</template>
