<script setup>
import { G, api, refreshProject } from '../store'
import { computed } from 'vue'
import { ElMessage } from 'element-plus'

const props = defineProps({ shot: Object, manifestItem: Object })

const pj = computed(() => G.project)
const running = computed(() => pj.value?.project?.running)

async function rerender(cmd) {
  if (cmd === 'api2k') {
    try {
      await api.upgrade2k(pj.value.project.id, props.shot.id)
      ElMessage.success(`镜头 ${props.shot.id} 已提交云端 2K 定稿`)
      refreshProject()
    } catch (e) { ElMessage.error(e.message) }
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
    case 'done': return { text: '✓ 已完成', type: 'success' }
    case 'rendering': return { text: '渲染中', type: 'warning' }
    case 'failed': return { text: '✕ 失败', type: 'danger' }
    default: return { text: '待渲染', type: 'info' }
  }
})

const dialogues = computed(() => (props.shot.dialogue || []).map(d => `${d.speaker}:${d.text}`).join('\n'))
</script>

<template>
  <div class="shot-card">
    <!-- 视频 / 占位 -->
    <div class="relative" style="aspect-ratio:9/16;background:var(--mx-bg-2)">
      <video v-if="manifestItem?.status === 'done' && manifestItem?.file"
             :src="api.fileURL(pj.project.id, manifestItem.file) + '?v=' + (manifestItem.updated_at || '')"
             controls preload="metadata" playsinline
             style="width:100%;height:100%;object-fit:contain;background:#000" />
      <div v-else class="absolute inset-0 flex flex-col items-center justify-center gap-2" style="color:var(--mx-text-2)">
        <el-icon :size="30"><Picture /></el-icon>
        <span class="text-xs">SHOT {{ String(shot.id).padStart(2, '0') }}</span>
      </div>
      <div class="absolute top-2 left-2 flex gap-1.5 items-center">
        <span class="badge-mode" :class="shot.mode">{{ shot.mode?.toUpperCase() }}</span>
        <span class="badge-mode" style="color:var(--mx-orange)">{{ shot.duration }}s</span>
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
        <el-tooltip content="单镜重渲(可选引擎)" placement="top">
          <el-dropdown trigger="click" :disabled="running" @command="rerender">
            <el-button text size="small" type="primary" :disabled="running" @click.stop>
              <el-icon><RefreshRight /></el-icon>
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="">按项目配置</el-dropdown-item>
                <el-dropdown-item command="comfy">本地 ComfyUI</el-dropdown-item>
                <el-dropdown-item command="api">云端 API</el-dropdown-item>
                <el-dropdown-item divided command="api2k">云端 2K 定稿</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </el-tooltip>
      </div>
      <div class="text-xs leading-relaxed" style="color:var(--mx-text-2)">
        <div><b style="color:var(--mx-cyan)">{{ shot.shot_size }}</b> · {{ shot.camera }}</div>
        <div class="mt-1">{{ shot.action }}</div>
        <div v-if="dialogues" class="mt-1" style="color:var(--mx-orange-2);white-space:pre-line">{{ dialogues }}</div>
        <div v-if="shot.narration" class="mt-1" style="font-style:italic">旁白:{{ shot.narration }}</div>
      </div>
      <div v-if="manifestItem?.status === 'failed' && manifestItem?.error"
           class="text-xs p-2 rounded" style="background:rgba(248,113,113,.08);color:var(--mx-red)">
        {{ manifestItem.error }}
      </div>
    </div>
  </div>
</template>
