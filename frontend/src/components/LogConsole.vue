<script setup>
import { G, fmtTime } from '../store'
import { nextTick, watch, ref } from 'vue'

const box = ref(null)
const autoscroll = ref(true)

watch(() => G.logs.length, async () => {
  if (!autoscroll.value) return
  await nextTick()
  if (box.value) box.value.scrollTop = box.value.scrollHeight
})

function onWheel() { /* 用户滚动时保持位置 */ }
</script>

<template>
  <div class="log-console flex flex-col flex-none" style="height: 208px">
    <div class="flex items-center gap-3 px-3 h-8 flex-none" style="border-bottom:1px solid var(--mx-line)">
      <span class="text-xs flex items-center gap-1.5">
        <i class="dot run"></i> RUNTIME LOG
      </span>
      <span class="text-xs" style="color: var(--mx-text-2)">{{ G.logs.length }} 条 · 实时 SSE</span>
      <div class="flex-1"></div>
      <el-checkbox v-model="autoscroll" size="small" label="自动滚动" />
      <el-button text size="small" @click="G.logs = []">清空</el-button>
      <el-button text size="small" @click="G.logPanelOpen = false">收起 ▾</el-button>
    </div>
    <div ref="box" class="flex-1 overflow-y-auto py-1" @wheel="onWheel">
      <div v-if="!G.logs.length" class="log-line info"><span class="t">--:--:--</span><span class="m" style="color:#57617a">等待管线事件…(新建项目并运行任意阶段后,这里将实时输出)</span></div>
      <div v-for="(l, i) in G.logs" :key="i" class="log-line" :class="l.level">
        <span class="t">{{ fmtTime(l.at) }}</span>
        <span class="ph" v-if="l.phase">[{{ l.phase }}]</span>
        <span class="m">{{ l.message }}</span>
      </div>
    </div>
  </div>
</template>
