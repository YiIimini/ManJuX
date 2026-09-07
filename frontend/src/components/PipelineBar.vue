<script setup>
import { G, PHASES, api, refreshProject } from '../store'
import { computed } from 'vue'
import { ElMessage } from 'element-plus'

const props = defineProps({ phase: String, progress: Number })

const project = computed(() => G.project?.project)

function stageState(id) {
  const pj = G.project
  if (!pj) return ''
  if (pj.project.running) {
    if (props.phase === id) return 'active'
    const order = PHASES.map(p => p.id)
    const cur = order.indexOf(props.phase)
    const me = order.indexOf(id)
    if (me < cur) return 'done'
    return ''
  }
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
  if (id === 'assemble') return pj.output?.ready ? 'done' : (pj.output?.ready_2k ? 'done' : '')
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
      <div class="stage-card flex-1 min-w-[210px]" :class="stageState(s.id)" :title="s.desc">
        <div class="flex items-start gap-3">
          <span class="stage-num">0{{ i + 1 }}</span>
          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-2">
              <el-icon :size="17" :color="stageState(s.id) === 'active' ? 'var(--mx-amber)' : stageState(s.id) === 'done' ? 'var(--mx-green)' : 'var(--mx-text3)'">
                <component :is="s.icon" />
              </el-icon>
              <b class="text-[15px] cursor-pointer hover:text-[var(--mx-amber)] transition-colors" @click="G.stageDetail = s.id" title="打开详情管理">{{ s.name }}</b>
              <i class="dot" :class="stageState(s.id) === 'active' ? 'run' : stageState(s.id) === 'done' ? 'ok' : stageState(s.id) === 'bad' ? 'bad' : stageState(s.id) === 'warn' ? 'warn' : ''"></i>
              <div class="flex-1"></div>
              <el-tooltip content="详情管理" placement="top">
                <button class="seg" style="padding:4px 7px" @click="G.stageDetail = s.id">
                  <el-icon :size="13"><View /></el-icon>
                </button>
              </el-tooltip>
              <el-tooltip content="阶段参数调优" placement="top">
                <button class="seg" style="padding:4px 7px" @click="G.paramsDrawer = s.id" :disabled="project?.running">
                  <el-icon :size="13"><Operation /></el-icon>
                </button>
              </el-tooltip>
              <button class="seg" style="padding:4px 10px" :disabled="project?.running" @click="runStage(s.id)">
                运行<el-icon class="ml-0.5" :size="11"><CaretRight /></el-icon>
              </button>
            </div>
            <div class="text-xs mt-1.5 truncate" style="color:var(--mx-text3)">{{ s.desc }}</div>
          </div>
        </div>
      </div>
    </template>

    <div class="stage-link"></div>
    <div class="stage-card flex items-center justify-center min-w-[150px]" style="border-style:dashed;background:transparent">
      <div class="flex flex-col gap-2 w-full px-2">
        <el-button type="primary" :disabled="project?.running" @click="runAll">
          <el-icon class="mr-1"><VideoCameraFilled /></el-icon>一键全流程
        </el-button>
        <el-button size="small" type="danger" plain :disabled="!project?.running" @click="stopAll">
          <el-icon class="mr-1"><VideoPause /></el-icon>停止
        </el-button>
      </div>
    </div>
  </div>
</template>
