<script setup>
import { G, api, refreshProjects } from '../store'
import { ref, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const creating = ref(false)

const totalShots = computed(() => G.projects.reduce((a, p) => a + (p.shots_total || 0), 0))
const doneShots = computed(() => G.projects.reduce((a, p) => a + (p.shots_done || 0), 0))

async function createProject() {
  const { value } = await ElMessageBox.prompt('项目名(中文/字母/数字/_-)', '新建短视频项目', {
    confirmButtonText: '创建', cancelButtonText: '取消',
    inputPattern: /^[\u4e00-\u9fa5A-Za-z0-9_-]{1,40}$/,
    inputErrorMessage: '仅允许中文、字母、数字、下划线、连字符(≤40 字符)',
  }).catch(() => ({ value: '' }))
  if (!value) return
  try {
    await api.createProject(value)
    ElMessage.success(`项目「${value}」已创建`)
    await refreshProjects()
    await openProject(value)
  } catch (e) { ElMessage.error(e.message) }
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
        <el-button type="primary" size="large" @click="createProject">
          <el-icon class="mr-1"><Plus /></el-icon>新建项目
        </el-button>
      </div>
    </div>

    <!-- 项目网格 -->
    <div class="flex items-center mb-3">
      <h2 class="text-base font-bold m-0">我的项目</h2>
      <span class="timecode ml-3">{{ G.projects.length }} PROJECTS</span>
    </div>

    <div v-if="!G.projects.length" class="panel p-14 text-center" style="color:var(--mx-text3)">
      还没有项目 —— 点击「新建项目」开始第一条短视频
    </div>

    <div class="grid gap-5" style="grid-template-columns:repeat(auto-fill,minmax(300px,1fr))">
      <div v-for="p in G.projects" :key="p.id" class="shot-card proj-card cursor-pointer" @click="openProject(p.id)">
        <div class="proj-cover">
          <div class="absolute inset-0 flex flex-col items-center justify-center gap-2">
            <el-icon :size="34" color="var(--mx-text3)"><Film /></el-icon>
            <span class="timecode">{{ p.has_plan ? p.shots_total + ' 镜' : 'NEW' }}</span>
          </div>
          <div class="absolute top-3 right-3">
            <i class="dot" :class="stateChip(p).c"></i>
          </div>
        </div>
        <div class="p-4 flex flex-col gap-2.5">
          <div class="flex items-center gap-2">
            <b class="truncate flex-1 text-[15px]">{{ p.name }}</b>
            <span class="timecode">{{ stateChip(p).t }}</span>
            <el-button text size="small" @click.stop="removeProject(p)"><el-icon><Delete /></el-icon></el-button>
          </div>
          <el-progress v-if="p.shots_total" :percentage="Math.round(p.shots_done * 100 / p.shots_total)"
                       :stroke-width="6" :show-text="false"
                       :color="'linear-gradient(90deg, var(--mx-amber-hi), var(--mx-amber-deep))'" />
        </div>
      </div>
    </div>
  </div>
</template>
