<script setup>
import { G, fmtTime, logDot, phaseLabel } from '../store'

// 项目名(事件流是全局的,跨项目运行都要能分清归属)
const projName = (id) => {
  if (!id) return ''
  const p = (G.projects || []).find(x => x.id === id)
  return p ? p.name : id
}
const curProj = () => G.project?.project?.id
import { nextTick, watch, ref } from 'vue'

const box = ref(null)
const autoscroll = ref(true)

watch(() => G.logs.length, async () => {
  if (!autoscroll.value) return
  await nextTick()
  if (box.value) box.value.scrollTop = box.value.scrollHeight
})
</script>

<template>
  <div class="log-console flex flex-col flex-none" style="height: 216px">
    <div class="flex items-center gap-3 px-4 h-9 flex-none" style="border-bottom:1px solid var(--mx-line)">
      <span class="tick-dot" :class="logDot"></span>
      <span class="timecode" :style="{ color: logDot === 'ok' ? 'var(--mx-green)' : logDot === 'bad' ? 'var(--mx-red)' : logDot === 'warn' ? 'var(--mx-yellow)' : 'var(--mx-text3)' }">REC</span>
      <span class="timecode">RUNTIME LOG · GLOBAL LIVE(全局事件流)</span>
      <span class="timecode" v-if="G.logs.length">{{ G.logs.length }} EVENTS</span>
      <div class="flex-1"></div>
      <el-checkbox v-model="autoscroll" size="small" label="自动滚动" />
      <el-button text size="small" @click="G.logs = []">清空</el-button>
      <el-button text size="small" @click="G.logPanelOpen = false">收起 ▾</el-button>
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
  </div>
</template>

<style scoped>
.pj { color: var(--mx-purple); margin-right: 8px; }
.pj.cur { color: var(--mx-amber); }
</style>
