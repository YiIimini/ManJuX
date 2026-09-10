<script setup>
import { G, api, fmtTime, logDot, phaseLabel } from '../store'
import { computed, ref, onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

// 一键停止:运行中的管线(通常 0 或 1 个;批量产线归并为单管线逐集推进)
const runningIds = computed(() => Object.keys(G.status?.running || {}))
async function confirmStop() {
  const ids = runningIds.value.slice()
  if (!ids.length) return
  const names = ids.map(projName).filter(Boolean).join('、')
  try {
    await ElMessageBox.confirm(
      `确定停止当前运行中的管线?${names ? '(『' + names + '』)' : ''}渲染中的任务将被中断,已完成阶段保留,修复问题后可续跑。`,
      '一键停止', { confirmButtonText: '停止', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  let ok = 0
  for (const id of ids) {
    try { await api.stop(id); ok++ } catch { /* 单个失败继续 */ }
  }
  if (ok) ElMessage.success('已停止 ' + ok + ' 条管线')
}

// 项目名(事件流是全局的,跨项目运行都要能分清归属)
const projName = (id) => {
  if (!id) return ''
  const p = (G.projects || []).find(x => x.id === id)
  return p ? p.name : id
}
const curProj = () => G.project?.project?.id

// 倒序显示:最新事件恒在顶部,无需自动滚动(数据层 G.logs 仍时序追加,
// 底栏 ticker 与增量轮询不受影响)
const reversed = computed(() => {
  const arr = G.logs.slice()
  arr.reverse()
  return arr
})

// ---- 弹窗:标题栏拖动 + 右下角调大小(非模态,不挡主界面) ----
const root = ref(null)
const pos = ref(null) // null = 默认右下角锚定;拖动后为 {left, top}
const size = ref({ w: 780, h: 440 })
let drag = null
let rsz = null

function onDragDown(e) {
  if (e.button !== 0) return
  const r = root.value.getBoundingClientRect()
  pos.value = { left: r.left, top: r.top }
  drag = { x: e.clientX, y: e.clientY, left: r.left, top: r.top }
  window.addEventListener('mousemove', onDragMove)
  window.addEventListener('mouseup', onDragUp)
  e.preventDefault()
}
function onDragMove(e) {
  if (!drag) return
  const left = Math.min(Math.max(0, drag.left + e.clientX - drag.x), window.innerWidth - 80)
  const top = Math.min(Math.max(0, drag.top + e.clientY - drag.y), window.innerHeight - 40)
  pos.value = { left, top }
}
function onDragUp() {
  drag = null
  window.removeEventListener('mousemove', onDragMove)
  window.removeEventListener('mouseup', onDragUp)
}
function onRszDown(e) {
  if (e.button !== 0) return
  rsz = { x: e.clientX, y: e.clientY, w: size.value.w, h: size.value.h }
  window.addEventListener('mousemove', onRszMove)
  window.addEventListener('mouseup', onRszUp)
  e.preventDefault()
  e.stopPropagation()
}
function onRszMove(e) {
  if (!rsz) return
  size.value = {
    w: Math.min(Math.max(420, rsz.w + e.clientX - rsz.x), window.innerWidth - 16),
    h: Math.min(Math.max(220, rsz.h + e.clientY - rsz.y), window.innerHeight - 16),
  }
}
function onRszUp() {
  rsz = null
  window.removeEventListener('mousemove', onRszMove)
  window.removeEventListener('mouseup', onRszUp)
}
onBeforeUnmount(() => {
  window.removeEventListener('mousemove', onDragMove)
  window.removeEventListener('mouseup', onDragUp)
  window.removeEventListener('mousemove', onRszMove)
  window.removeEventListener('mouseup', onRszUp)
})
</script>

<template>
  <div ref="root" class="log-console log-pop" :style="{
    width: size.w + 'px', height: size.h + 'px',
    ...(pos ? { left: pos.left + 'px', top: pos.top + 'px', right: 'auto', bottom: 'auto', transform: 'none' } : {}),
  }">
      <div class="log-pop-bar" title="拖动移动" @mousedown="onDragDown">
        <span class="tick-dot" :class="logDot"></span>
        <span class="timecode" :style="{ color: logDot === 'ok' ? 'var(--mx-green)' : logDot === 'bad' ? 'var(--mx-red)' : logDot === 'warn' ? 'var(--mx-yellow)' : 'var(--mx-text3)' }">记录中</span>
        <span class="timecode">运行日志 · 全局实时</span>
        <span class="timecode" v-if="G.logs.length">{{ G.logs.length }} 条事件</span>
        <div class="flex-1"></div>
        <el-tooltip :content="runningIds.length ? '一键停止:停止当前运行中的管线(二次确认)' : '当前无运行中的管线'" placement="bottom">
          <el-button text size="small" class="logbar-ico" :disabled="!runningIds.length"
                     :style="runningIds.length ? 'color:var(--mx-red)' : ''" @click="confirmStop" @mousedown.stop>
            <el-icon :size="14"><VideoPause /></el-icon>
          </el-button>
        </el-tooltip>
        <el-tooltip content="清空当前日志显示(磁盘运行史不受影响)" placement="bottom">
          <el-button text size="small" class="logbar-ico" @click="G.logs = []" @mousedown.stop><el-icon :size="14"><Delete /></el-icon></el-button>
        </el-tooltip>
        <el-tooltip content="收起日志面板(底栏日志条可再次展开)" placement="bottom">
          <el-button text size="small" class="logbar-ico" @click="G.logPanelOpen = false" @mousedown.stop><el-icon :size="14"><ArrowDown /></el-icon></el-button>
        </el-tooltip>
      </div>
      <div class="flex-1 overflow-y-auto py-1.5">
        <div v-if="!G.logs.length" class="log-line info"><span class="t">--:--:--</span><span class="tl-dot"></span><span class="body"><span class="m" style="color:#46536f">等待管线事件…(运行任意阶段后实时输出)</span></span></div>
        <div v-for="(l, i) in reversed" :key="i" class="log-line" :class="l.level">
          <span class="t">{{ fmtTime(l.at) }}</span>
          <span class="tl-dot"></span>
          <span class="body">
            <span class="pj" :class="{ cur: l.project_id === curProj() }" v-if="l.project_id">[{{ projName(l.project_id) }}]</span>
            <span class="ph" v-if="l.phase">[{{ phaseLabel(l.phase) }}]</span>
            <span class="m">{{ l.message }}</span>
          </span>
        </div>
      </div>
      <div class="log-rsz" title="拖动调大小" @mousedown="onRszDown"></div>
    </div>
</template>

<style scoped>
.pj { color: var(--mx-purple); margin-right: 8px; }
.pj.cur { color: var(--mx-amber); }
.logbar-ico { padding: 4px 7px !important; }
.logbar-ico:hover { color: var(--mx-amber-hi) !important; background: rgba(255, 180, 84, .1) !important; }
</style>
