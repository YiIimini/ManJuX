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

// 阶段计数与进度(卡面数据,一眼看清产线状态)
function countOf(id) {
  const pj = G.project
  if (!pj) return { label: '—', pct: 0 }
  switch (id) {
    case 'script':
      return pj.plan ? { label: pj.plan.shots.length + ' 镜方案', pct: 100 } : { label: '待生成', pct: 0 }
    case 'assets': {
      if (!pj.plan) return { label: '待定妆', pct: 0 }
      if (pj.project.params?.assets?.source === 'skip') return { label: '已跳过', pct: 100 }
      const total = pj.plan.characters.length + pj.plan.scenes.length
      const have = Object.keys(pj.assets || {}).length
      return { label: have + '/' + total + ' 定妆', pct: total ? Math.min(100, Math.round(have * 100 / total)) : 0 }
    }
    case 'render': {
      if (!pj.plan) return { label: '待渲染', pct: 0 }
      const total = pj.plan.shots.length
      const done = Object.values(pj.manifest || {}).filter(m => m.status === 'done').length
      return { label: done + '/' + total + ' 镜', pct: total ? Math.round(done * 100 / total) : 0 }
    }
    case 'qc': {
      if (!pj.qc) return { label: '待质检', pct: 0 }
      const list = Object.values(pj.qc)
      const ok = list.filter(r => r.ok).length
      return { label: ok + '/' + list.length + ' 通过', pct: list.length ? Math.round(ok * 100 / list.length) : 0 }
    }
    case 'assemble':
      return pj.output?.ready ? { label: '成片就绪', pct: 100 } : { label: '未合成', pct: 0 }
  }
  return { label: '—', pct: 0 }
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
  <div class="stage-grid">
    <div v-for="(s, i) in PHASES" :key="s.id" class="stage-card stage-v3" :class="stageState(s.id)">
      <div class="stage-v3-head">
        <div class="stage-ico" :class="stageState(s.id)">
          <el-icon :size="18"><component :is="s.icon" /></el-icon>
        </div>
        <div class="flex-1 min-w-0">
          <div class="flex items-center gap-2">
            <span class="stage-num">0{{ i + 1 }}</span>
            <b class="text-[14px] cursor-pointer hover:text-[var(--mx-amber)] transition-colors"
               @click="G.stageDetail = s.id" title="打开详情管理">{{ s.name }}</b>
            <i class="dot" :class="stageState(s.id) === 'active' ? 'run' : stageState(s.id) === 'done' ? 'ok' : stageState(s.id) === 'bad' ? 'bad' : stageState(s.id) === 'warn' ? 'warn' : ''"></i>
          </div>
        </div>
      </div>
      <div class="stage-v3-desc">{{ s.desc }}</div>
      <div class="stage-meter">
        <div class="stage-meter-fill" :style="{ width: (countOf(s.id)?.pct || 0) + '%' }"></div>
      </div>
      <div class="stage-v3-foot">
        <span class="timecode" :style="{ color: stageState(s.id) === 'done' ? 'var(--mx-green)' : 'var(--mx-text3)' }">
          {{ countOf(s.id)?.label }}
        </span>
        <div class="flex gap-1">
          <el-tooltip content="详情管理" placement="top">
            <button class="seg" @click="G.stageDetail = s.id"><el-icon :size="13"><View /></el-icon></button>
          </el-tooltip>
          <el-tooltip content="参数调优" placement="top">
            <button class="seg" :disabled="project?.running" @click="G.paramsDrawer = s.id"><el-icon :size="13"><Operation /></el-icon></button>
          </el-tooltip>
          <button class="seg runbtn" :disabled="project?.running" @click="runStage(s.id)" title="独立运行本阶段">
            运行<el-icon :size="11" class="ml-0.5"><CaretRight /></el-icon>
          </button>
        </div>
      </div>
    </div>

    <!-- 一键全流程(主操作卡) -->
    <div class="stage-card stage-v3 stage-runall">
      <div class="stage-v3-head">
        <div class="stage-ico active" style="color:var(--mx-amber-hi)">
          <el-icon :size="18"><VideoCameraFilled /></el-icon>
        </div>
        <div class="flex-1">
          <div class="flex items-center gap-2">
            <b class="text-[14px]">全流程直通</b>
          </div>
        </div>
      </div>
      <div class="stage-v3-desc">文本 → 分镜 → 定妆 → 渲染 → 质检 → 成片,断点续跑</div>
      <div class="flex flex-col gap-2 mt-auto pt-2.5">
        <el-button type="primary" :disabled="project?.running" @click="runAll" class="w-full">
          <el-icon class="mr-1"><CaretRight /></el-icon>一键全流程
        </el-button>
        <el-button size="small" type="danger" plain :disabled="!project?.running" @click="stopAll" class="w-full">
          <el-icon class="mr-1"><VideoPause /></el-icon>停止运行
        </el-button>
      </div>
    </div>
  </div>
</template>
