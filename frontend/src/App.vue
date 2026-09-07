<script setup>
import { onMounted, onUnmounted, computed } from 'vue'
import { G, api, connectEvents, refreshStatus, refreshProjects } from './store'
import { ElMessageBox } from 'element-plus'
import HomeView from './views/HomeView.vue'
import ProjectView from './views/ProjectView.vue'
import SettingsView from './views/SettingsView.vue'
import LogConsole from './components/LogConsole.vue'
import ParamsDrawer from './components/ParamsDrawer.vue'
import PromptDrawer from './components/PromptDrawer.vue'

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
</script>

<template>
  <div class="app flex flex-col h-full">
    <!-- 顶栏 -->
    <header class="topbar flex items-center gap-4 px-5 h-12 flex-none">
      <div class="flex items-center gap-2 cursor-pointer" @click="G.view = 'home'; G.project = null">
        <span class="brand">ManJuX</span>
        <span class="timecode">STUDIO CONSOLE</span>
      </div>

      <nav class="flex items-center gap-1 ml-2">
        <el-button text :class="{ 'nav-active': G.view === 'home' }" @click="G.view = 'home'; G.project = null">
          <el-icon><HomeFilled /></el-icon><span class="ml-1">项目</span>
        </el-button>
        <el-button text :disabled="!G.project" :class="{ 'nav-active': G.view === 'project' }" @click="G.view = 'project'">
          <el-icon><Film /></el-icon><span class="ml-1">工作台</span>
        </el-button>
        <el-button text :class="{ 'nav-active': G.view === 'settings' }" @click="G.view = 'settings'">
          <el-icon><Setting /></el-icon><span class="ml-1">设置</span>
        </el-button>
      </nav>

      <div class="flex-1"></div>

      <!-- 环境状态灯 -->
      <div class="flex items-center gap-4" v-if="env" title="环境状态">
        <span class="flex items-center gap-1.5 text-xs" title="文本大模型(LLM)">
          <i class="dot" :class="env.llm_ready ? 'ok' : 'bad'"></i>LLM
        </span>
        <span class="flex items-center gap-1.5 text-xs" title="本地 ComfyUI">
          <i class="dot" :class="env.comfy_live ? 'ok' : 'warn'"></i>ComfyUI
        </span>
        <span class="flex items-center gap-1.5 text-xs" title="MiniMax 云端 API">
          <i class="dot" :class="env.minimax_ready ? 'ok' : 'warn'"></i>H3 API
        </span>
        <span class="flex items-center gap-1.5 text-xs" title="ffmpeg">
          <i class="dot" :class="env.ffmpeg ? 'ok' : 'bad'"></i>ffmpeg
        </span>
      </div>

      <el-divider direction="vertical" />
      <span class="timecode">{{ G.status?.version || '…' }}</span>

      <el-tooltip content="运行日志" placement="bottom">
        <el-button circle size="small" @click="G.logPanelOpen = !G.logPanelOpen">
          <el-icon><Monitor /></el-icon>
        </el-button>
      </el-tooltip>
    </header>
    <div class="film-strip flex-none"></div>

    <!-- 主区 -->
    <main class="flex-1 overflow-hidden">
      <HomeView v-if="G.view === 'home'" />
      <ProjectView v-else-if="G.view === 'project' && G.project" />
      <SettingsView v-else-if="G.view === 'settings'" />
    </main>

    <LogConsole v-if="G.logPanelOpen" />
    <ParamsDrawer />
    <PromptDrawer />
  </div>
</template>

<style scoped>
.nav-active { color: var(--mx-orange) !important; }
:deep(.el-button.nav-active .el-icon) { color: var(--mx-orange); }
</style>
