<script setup>
import { G, fmtTime, logDot, phaseLabel } from '../store'
import { nextTick, watch, ref, onBeforeUnmount } from 'vue'

// 项目名(事件流是全局的,跨项目运行都要能分清归属)
const projName = (id) => {
  if (!id) return ''
  const p = (G.projects || []).find(x => x.id === id)
  return p ? p.name : id
}
const curProj = () => G.project?.project?.id

const box = ref(null)
const autoscroll = ref(true)

watch(() => G.logs.length, async () => {
  if (!autoscroll.value) return
  await nextTick()
  if (box.value) box.value.scrollTop = box.value.scrollHeight
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
    ...(pos ? { left: pos.left + 'px', top: pos.top + 'px', right: 'auto', bottom: 'auto' } : {}),
  }">
      <div class="log-pop-bar" title="拖动移动" @mousedown="onDragDown">
        <span class="tick-dot" :class="logDot"></span>
        <span class="timecode" :style="{ color: logDot === 'ok' ? 'var(--mx-green)' : logDot === 'bad' ? 'var(--mx-red)' : logDot === 'warn' ? 'var(--mx-yellow)' : 'var(--mx-text3)' }">REC</span>
        <span class="timecode">RUNTIME LOG · GLOBAL LIVE</span>
        <span class="timecode" v-if="G.logs.length">{{ G.logs.length }} EVENTS</span>
        <div class="flex-1"></div>
        <el-checkbox v-model="autoscroll" size="small" label="自动滚动" @mousedown.stop />
        <el-button text size="small" @click="G.logs = []" @mousedown.stop>清空</el-button>
        <el-button text size="small" @click="G.logPanelOpen = false" @mousedown.stop>收起 ✕</el-button>
      </div>
      <div ref="box" class="flex-1 overflow-y-auto py-1.5">
        <div v-if="!G.logs.length" class="log-line info"><span class="t">--:--:--</span><span class="m" style="color:#46536f">等待管线事件…(运行任意阶段后实时输出)</span></div>
        <div v-for="(l, i) in G.logs" :key="i" class="log-line" :class="l.level">
          <span class="t">{{ fmtTime(l.at) }}</span>
          <span class="pj" :class="{ cur: l.project_id === curProj() }" v-if="l.project_id">[{{ projName(l.project_id) }}]</span>
          <span class="ph" v-if="l.phase">[{{ phaseLabel(l.phase) }}]</span>
          <span class="m">{{ l.message }}</span>
        </div>
      </div>
      <div class="log-rsz" title="拖动调大小" @mousedown="onRszDown"></div>
    </div>
</template>

<style scoped>
.pj { color: var(--mx-purple); margin-right: 8px; }
.pj.cur { color: var(--mx-amber); }
</style>
