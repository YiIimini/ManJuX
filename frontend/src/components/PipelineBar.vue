<script setup>
import { G, PHASES, api, refreshProject } from '../store'
import { computed } from 'vue'
import { ElMessage } from 'element-plus'

const props = defineProps({ phase: String, progress: Number })

const project = computed(() => G.project?.project)

function stageState(id) {
  const pj = G.project
  if (!pj) return ''
  if (pj.project.running && (props.phase === id || (props.phase === 'all' || props.phase === ''))) {
    if (pj.project.running) {
      // 运行中:精确到当前阶段高亮;全流程时按推进点亮
      if (props.phase === id) return 'active'
      const order = PHASES.map(p => p.id)
      const cur = order.indexOf(props.phase)
      const me = order.indexOf(id)
      if (me < cur) return 'done'
      return ''
    }
  }
  // 静态推断
  if (id === 'script') return pj.plan ? 'done' : ''
  if (id === 'assets') {
    if (!pj.plan) return ''
    const total = pj.plan.characters.length + pj.plan.scenes.length
    const have = Object.keys(pj.assets || {}).length
    return (pj.project.params?.assets?.source === 'skip' || (total > 0 && have >= total)) ? 'done' : 'warn'
  }
  if (id === 'render') {
    if (!pj.plan) return ''
    const total = pj.plan.shots.length
    const done = Object.values(pj.manifest || {}).filter(m => m.status === 'done').length
    return done >= total && total > 0 ? 'done' : (done > 0 ? 'warn' : '')
  }
  if (id === 'qc') {
    return pj.qc ? (Object.values(pj.qc).every(r => r.ok) ? 'done' : 'bad') : ''
  }
  if (id === 'assemble') return ''
  return ''
}

async function runStage(id) {
  if (!project.value) return
  try {
    await api.run(project.value.id, id)
    ElMessage.success(`「${PHASES.find(p => p.id === id).name}」已启动`)
    refreshProject()
  } catch (e) { ElMessage.error(e.message) }
}
async function stopAll() {
  try { await api.stop(project.value.id); ElMessage.info('已请求停止,正在收尾…') }
  catch (e) { ElMessage.error(e.message) }
}
async function runAll() { await runStage('all') }
</script>

<template>
  <div class="flex items-stretch gap-0 overflow-x-auto pb-1">
    <template v-for="(s, i) in PHASES" :key="s.id">
      <div v-if="i" class="stage-link"></div>
      <div class="stage-card flex-1 min-w-[190px]" :class="stageState(s.id)" :title="s.desc">
        <div class="flex items-center gap-2">
          <i class="dot" :class="stageState(s.id) === 'active' ? 'run' : (stageState(s.id) === 'done' ? 'ok' : stageState(s.id) === 'bad' ? 'bad' : stageState(s.id) === 'warn' ? 'warn' : '')"></i>
          <el-icon :size="16" :color="stageState(s.id) === 'active' ? 'var(--mx-orange)' : 'var(--mx-text-2)'">
            <component :is="s.icon" />
          </el-icon>
          <b class="text-sm">{{ s.name }}</b>
          <span class="timecode">0{{ i + 1 }}</span>
          <div class="flex-1"></div>
          <el-tooltip content="阶段参数调优" placement="top">
            <el-button text size="small" @click="G.paramsDrawer = s.id" :disabled="project?.running">
              <el-icon><Operation /></el-icon>
            </el-button>
          </el-tooltip>
          <el-button text size="small" type="primary" :disabled="project?.running" @click="runStage(s.id)">
            运行<el-icon class="ml-0.5"><CaretRight /></el-icon>
          </el-button>
        </div>
        <div class="text-xs mt-2 truncate" style="color:var(--mx-text-2)">{{ s.desc }}</div>
      </div>
    </template>

    <div class="stage-link"></div>
    <div class="stage-card flex flex-col justify-center min-w-[132px]" style="border-style:dashed">
      <el-button type="primary" :disabled="project?.running" @click="runAll">
        <el-icon class="mr-1"><VideoCameraFilled /></el-icon>一键全流程
      </el-button>
      <el-button class="mt-2" type="danger" plain size="small" :disabled="!project?.running" @click="stopAll">
        <el-icon class="mr-1"><VideoPause /></el-icon>停止
      </el-button>
    </div>
  </div>
</template>
