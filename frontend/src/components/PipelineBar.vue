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
      const have = Object.keys(pj.assets || {}).filter(k => !k.includes('_chibi')).length
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
    const label = PHASES.find(p => p.id === id)?.name || '全流程'
    ElMessage.success(`「${label}」已启动`)
    refreshProject()
  } catch (e) { ElMessage.error(e.message) }
}
async function stopAll() {
  try {
    const r = await api.stop(project.value.id)
    if (r && r.stopped === false) {
      ElMessage.info('管线已在此前结束,状态已同步')
      refreshProject()
    } else {
      ElMessage.info('已请求停止,正在收尾…')
    }
  } catch (e) { ElMessage.error(e.message) }
}
async function runAll() { await runStage('all') }
</script>

<template>
  <div class="pipe-rail">
    <div class="pipe-rail-title">
      <span class="rec-dot" v-if="project?.running"></span>
      <span>产线 PIPELINE</span>
    </div>

    <div v-for="(s, i) in PHASES" :key="s.id" class="stage-card stage-rail" :class="stageState(s.id)">
      <div class="rail-head">
        <div class="stage-ico sm" :class="stageState(s.id)">
          <el-icon :size="15"><component :is="s.icon" /></el-icon>
        </div>
        <span class="stage-num sm">0{{ i + 1 }}</span>
        <b class="rail-name" :class="stageState(s.id)" @click="G.stageDetail = s.id" title="打开详情管理">{{ s.name }}</b>
        <i class="dot" :class="stageState(s.id) === 'active' ? 'run' : stageState(s.id) === 'done' ? 'ok' : stageState(s.id) === 'bad' ? 'bad' : stageState(s.id) === 'warn' ? 'warn' : ''"></i>
        <!-- 右上角:详情管理 / 参数调优 -->
        <div class="rail-tools">
          <button class="rail-tool" title="详情管理" @click="G.stageDetail = s.id"><el-icon :size="12"><View /></el-icon></button>
          <button class="rail-tool" title="参数调优" :disabled="project?.running" @click="G.paramsDrawer = s.id"><el-icon :size="12"><Operation /></el-icon></button>
        </div>
      </div>
      <div class="rail-count">{{ countOf(s.id)?.label }}</div>
      <div class="stage-meter">
        <div class="stage-meter-fill" :style="{ width: (countOf(s.id)?.pct || 0) + '%' }"></div>
      </div>
      <button class="rail-run" :disabled="project?.running" @click="runStage(s.id)">
        运行<el-icon :size="11" class="ml-0.5"><CaretRight /></el-icon>
      </button>
    </div>

    <!-- 全流程直通 -->
    <div class="stage-card stage-rail stage-runall">
      <div class="rail-head">
        <div class="stage-ico sm active" style="color:var(--mx-amber-hi)">
          <el-icon :size="15"><VideoCameraFilled /></el-icon>
        </div>
        <b class="rail-name">全流程直通</b>
      </div>
      <div class="rail-count">分镜→定妆→渲染→质检→成片</div>
      <div class="rail-runall-btns">
        <el-button type="primary" size="small" class="flex-1" :disabled="project?.running" @click="runAll">
          <el-icon class="mr-0.5"><CaretRight /></el-icon>一键全流程
        </el-button>
        <el-button size="small" type="danger" plain :disabled="!project?.running" @click="stopAll" style="flex:none">
          <el-icon><VideoPause /></el-icon>
        </el-button>
      </div>
    </div>
  </div>
</template>
