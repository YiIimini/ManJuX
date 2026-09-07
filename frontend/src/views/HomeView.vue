<script setup>
import { G, api, refreshProjects } from '../store'
import { ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const creating = ref(false)
const newName = ref('')

async function createProject() {
  const { value } = await ElMessageBox.prompt('项目名(中文/字母/数字/_-)', '新建短视频项目', {
    inputValue: newName.value,
    confirmButtonText: '创建',
    cancelButtonText: '取消',
    inputPattern: /^[\u4e00-\u9fa5A-Za-z0-9_-]{1,40}$/,
    inputErrorMessage: '仅允许中文、字母、数字、下划线、连字符(≤40 字符)',
  }).catch(() => ({ value: '' }))
  if (!value) return
  try {
    await api.createProject(value)
    newName.value = ''
    ElMessage.success(`项目「${value}」已创建`)
    await refreshProjects()
    await openProject(value)
  } catch (e) { ElMessage.error(e.message) }
}

async function openProject(id) {
  try {
    G.project = await api.project(id)
    G.view = 'project'
  } catch (e) { ElMessage.error(e.message) }
}

async function removeProject(p) {
  try {
    await ElMessageBox.confirm(
      `将删除项目「${p.name}」的全部数据(分镜/资产/镜头/成片),不可恢复。`, '删除项目',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  try {
    await api.deleteProject(p.id)
    ElMessage.success('已删除')
    refreshProjects()
  } catch (e) { ElMessage.error(e.message) }
}

function engineLabel(p) {
  if (p.running) return `运行中 · ${p.phase}`
  if (p.shots_total && p.shots_done >= p.shots_total) return '已完成'
  if (p.shots_done > 0) return `渲染 ${p.shots_done}/${p.shots_total}`
  if (p.has_plan) return '待渲染'
  return '待创作'
}
</script>

<template>
  <div class="h-full overflow-y-auto p-6">
    <!-- Hero -->
    <div class="panel p-6 mb-6 relative overflow-hidden">
      <div style="position:absolute;right:-40px;top:-40px;font-size:150px;opacity:.05;pointer-events:none">🎬</div>
      <h1 class="text-xl font-bold m-0 mb-2">从一段文字,到一部成片</h1>
      <p class="text-sm m-0" style="color:var(--mx-text-2);max-width:640px">
        输入故事/创意文本 → LLM 直出分镜与 H3 提示词 → 参考图资产 →
        <b style="color:var(--mx-orange)">本地 ComfyUI</b> 或 <b style="color:var(--mx-cyan)">MiniMax H3 云端 API</b>
        双引擎渲染(音画一体直出)→ 机械质检 → 合成成片。
      </p>
    </div>

    <!-- 项目列表 -->
    <div class="flex items-center mb-3">
      <h2 class="text-base font-bold m-0">我的项目</h2>
      <span class="text-xs ml-3" style="color:var(--mx-text-2)">{{ G.projects.length }} 个</span>
      <div class="flex-1"></div>
      <el-button type="primary" @click="createProject">
        <el-icon class="mr-1"><Plus /></el-icon>新建项目
      </el-button>
    </div>

    <div v-if="!G.projects.length" class="panel p-10 text-center" style="color:var(--mx-text-2)">
      还没有项目 —— 点击「新建项目」开始第一条短视频
    </div>

    <div class="grid gap-4" style="grid-template-columns:repeat(auto-fill,minmax(280px,1fr))">
      <div v-for="p in G.projects" :key="p.id" class="shot-card cursor-pointer p-4 flex flex-col gap-2"
           @click="openProject(p.id)">
        <div class="flex items-center gap-2">
          <i class="dot" :class="p.running ? 'run' : (p.shots_total && p.shots_done >= p.shots_total ? 'ok' : '')"></i>
          <span class="font-bold truncate">{{ p.name }}</span>
          <div class="flex-1"></div>
          <el-button text size="small" @click.stop="removeProject(p)" title="删除">
            <el-icon><Delete /></el-icon>
          </el-button>
        </div>
        <div class="timecode">{{ engineLabel(p) }}</div>
        <div class="flex items-center gap-2 text-xs" style="color:var(--mx-text-2)">
          <span>{{ p.has_plan ? `${p.shots_total} 镜` : '未出方案' }}</span>
          <el-progress v-if="p.shots_total" :percentage="Math.round(p.shots_done * 100 / p.shots_total)"
                       :stroke-width="6" style="flex:1" />
        </div>
      </div>
    </div>
  </div>
</template>
