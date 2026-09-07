<script setup>
import { G, api, refreshProject } from '../store'
import { computed } from 'vue'
import { ElMessage } from 'element-plus'

const props = defineProps({ shot: Object, manifestItem: Object })

const pj = computed(() => G.project)
const running = computed(() => pj.value?.project?.running)
const idx = computed(() => (props.shot?.id ?? 1) - 1)

function openViewer() {
  const shots = pj.value?.plan?.shots || []
  const mf = pj.value?.manifest || {}
  const items = shots
    .filter(s => mf[String(s.id)]?.status === 'done' && mf[String(s.id)]?.file)
    .map(s => {
      const m = mf[String(s.id)]
      return {
        type: 'video', src: api.fileURL(pj.value.project.id, m.file),
        title: `镜头 ${s.id} · ${s.shot_size}`,
        meta: `${s.mode.toUpperCase()} · ${s.duration}s · ${m.engine === 'comfy' ? '本地' : '云端'}`,
        foot: [s.action, s.narration, ...(s.dialogue || []).map(d => `${d.speaker}:${d.text}`)].filter(Boolean).join('\n'),
        kindLabel: 'SHOT',
      }
    })
  if (!items.length) return
  G.viewer = { items, index: Math.max(0, items.findIndex(x => x.title.startsWith(`镜头 ${props.shot.id} `))) }
}

async function rerender(cmd) {
  if (cmd === 'api2k') {
    try { await api.upgrade2k(pj.value.project.id, props.shot.id); ElMessage.success(`镜头 ${props.shot.id} 已提交云端 2K 定稿`) }
    catch (e) { ElMessage.error(e.message) }
    return
  }
  try {
    await api.run(pj.value.project.id, 'render', [props.shot.id], cmd)
    ElMessage.success(`镜头 ${props.shot.id} 已提交重渲${cmd ? '(' + (cmd === 'api' ? '云端' : '本地') + ')' : ''}`)
    refreshProject()
  } catch (e) { ElMessage.error(e.message) }
}

const statusTag = computed(() => {
  const m = props.manifestItem
  if (!m) return { text: '待渲染', type: 'info' }
  switch (m.status) {
    case 'done': return { text: '✓ 完成', type: 'success' }
    case 'rendering': return { text: '渲染中', type: 'warning' }
    case 'failed': return { text: '✕ 失败', type: 'danger' }
    default: return { text: '待渲染', type: 'info' }
  }
})
const dialogues = computed(() => (props.shot.dialogue || []).map(d => `${d.speaker}:${d.text}`).join('\n'))
</script>

<template>
  <div class="shot-card">
    <div class="relative cursor-zoom-in" style="aspect-ratio:9/16;background:var(--mx-bg0)" @click="openViewer" title="点击全屏预览">
      <video v-if="manifestItem?.status === 'done' && manifestItem?.file"
             :src="api.fileURL(pj.project.id, manifestItem.file) + '?v=' + (manifestItem.updated_at || '')"
             preload="metadata" playsinline muted
             style="width:100%;height:100%;object-fit:contain;background:#000" />
      <div v-else class="absolute inset-0 flex flex-col items-center justify-center gap-2" style="color:var(--mx-text3)">
        <el-icon :size="30"><Picture /></el-icon>
        <span class="timecode">SHOT {{ String(shot.id).padStart(2, '0') }}</span>
      </div>
      <div class="absolute inset-0 flex items-center justify-center opacity-0 hover:opacity-100 transition-opacity"
           style="background:rgba(4,6,10,.35)">
        <div class="w-12 h-12 rounded-full flex items-center justify-center" style="background:rgba(6,8,14,.6);border:1px solid rgba(255,180,84,.5)">
          <el-icon :size="20" color="var(--mx-amber-hi)"><VideoPlay /></el-icon>
        </div>
      </div>
      <div class="absolute top-2 left-2 flex gap-1.5 items-center pointer-events-none">
        <span class="badge-mode" :class="shot.mode">{{ shot.mode?.toUpperCase() }}</span>
        <span class="badge-mode dur">{{ shot.duration }}s</span>
      </div>
    </div>
    <div class="p-3 flex flex-col gap-2">
      <div class="flex items-center gap-2 text-xs">
        <b>镜 {{ shot.id }}</b>
        <el-tag size="small" :type="statusTag.type" effect="dark">{{ statusTag.text }}</el-tag>
        <span v-if="manifestItem?.engine" class="timecode">{{ manifestItem.engine === 'comfy' ? '本地' : '云端' }}</span>
        <div class="flex-1"></div>
        <el-tooltip content="查看 H3 提示词" placement="top">
          <el-button text size="small" @click="G.promptShot = shot"><el-icon><Document /></el-icon></el-button>
        </el-tooltip>
        <el-dropdown trigger="click" :disabled="running" @command="rerender">
          <el-button text size="small" type="primary" :disabled="running" @click.stop>
            <el-icon><RefreshRight /></el-icon>
          </el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="">重渲 · 按项目</el-dropdown-item>
              <el-dropdown-item command="comfy">重渲 · 本地</el-dropdown-item>
              <el-dropdown-item command="api">重渲 · 云端</el-dropdown-item>
              <el-dropdown-item divided command="api2k" :disabled="manifestItem?.status!=='done'">云端 2K 定稿</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
      <div class="text-xs leading-relaxed" style="color:var(--mx-text2)">
        <div><b style="color:var(--mx-cyan)">{{ shot.shot_size }}</b> · {{ shot.camera }}</div>
        <div class="mt-1">{{ shot.action }}</div>
        <div v-if="dialogues" class="mt-1" style="color:var(--mx-amber-hi);white-space:pre-line">{{ dialogues }}</div>
        <div v-if="shot.narration" class="mt-1" style="font-style:italic">旁白:{{ shot.narration }}</div>
      </div>
      <div v-if="manifestItem?.status === 'failed' && manifestItem?.error"
           class="text-xs p-2 rounded-lg" style="background:rgba(255,107,107,.07);color:var(--mx-red)">
        {{ manifestItem.error }}
      </div>
    </div>
  </div>
</template>
