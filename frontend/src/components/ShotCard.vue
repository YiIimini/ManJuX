<script setup>
import { G, api, refreshProject } from '../store'
import { computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const props = defineProps({ shot: Object, manifestItem: Object })

const pj = computed(() => G.project)
const running = computed(() => pj.value?.project?.running)
const idx = computed(() => (props.shot?.id ?? 1) - 1)

// 已渲染才能进全屏预览;未渲染/失败/渲染中不打开,绝不跳到其它镜头的片子
function openViewer() {
  const mf = props.manifestItem
  if (!mf || mf.status !== 'done' || !mf.file) {
    ElMessage.info(`镜头 ${props.shot.id}${mf?.status === 'rendering' ? ' 正在渲染中' : mf?.status === 'failed' ? ' 渲染失败' : ' 尚未渲染'},渲染完成即可预览`)
    return
  }
  const shots = pj.value?.plan?.shots || []
  const mfs = pj.value?.manifest || {}
  const items = shots
    .filter(s => mfs[String(s.id)]?.status === 'done' && mfs[String(s.id)]?.file)
    .map(s => {
      const m = mfs[String(s.id)]
      return {
        type: 'video', src: api.fileURL(pj.value.project.id, m.file),
        title: `镜头 ${s.id} · ${s.shot_size}`,
        meta: `${s.mode.toUpperCase()} · ${s.duration}s · ${m.engine === 'comfy' ? '本地' : '云端'}`,
        foot: [s.action, s.narration, ...(s.dialogue || []).map(d => `${d.speaker}:${d.text}`)].filter(Boolean).join('\n'),
        kindLabel: 'SHOT',
      }
    })
  if (!items.length) return
  G.viewer = { items, index: items.findIndex(x => x.title.startsWith(`镜头 ${props.shot.id} `)) }
}

// 删除单镜:该镜方案与已渲染产物一并移除,成片作废(需重新合成),其它镜头不受影响
async function removeShot() {
  if (running.value) return
  try {
    await ElMessageBox.confirm(
      `删除镜头 ${props.shot.id}?该镜的方案与已渲染产物将一并移除,成片需重新合成;其它镜头不受影响。`, '删除镜头',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  try {
    const r = await fetch(`/api/projects/${encodeURIComponent(pj.value.project.id)}/shot/${props.shot.id}`, { method: 'DELETE' })
    const d = await r.json().catch(() => ({}))
    if (!r.ok) throw new Error(d.error || `HTTP ${r.status}`)
    ElMessage.success(`镜头 ${props.shot.id} 已删除`)
    refreshProject()
  } catch (e) { ElMessage.error(e.message) }
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

// 出场角色/场景名(分镜卡标注:id → 名称)
const castNames = computed(() => {
  const plan = pj.value?.plan
  if (!plan?.characters) return []
  return (props.shot.characters || [])
    .map(cid => plan.characters.find(c => c.id === cid)?.name)
    .filter(Boolean)
})
const sceneName = computed(() => {
  const plan = pj.value?.plan
  return plan?.scenes?.find(s => s.id === props.shot.scene)?.name || ''
})
</script>

<template>
  <div class="shot-card">
    <div class="relative cursor-pointer" style="aspect-ratio:9/16;background:var(--mx-bg0)"
         @click="openViewer" :title="manifestItem?.status === 'done' ? '点击全屏预览(可左右切换已渲镜头)' : '尚未渲染,渲染完成后可预览'">
      <video v-if="manifestItem?.status === 'done' && manifestItem?.file"
             :src="api.fileURL(pj.project.id, manifestItem.file) + '?v=' + (manifestItem.updated_at || '')"
             preload="metadata" playsinline muted
             style="width:100%;height:100%;object-fit:contain;background:#000" />
      <div v-else class="absolute inset-0 flex flex-col items-center justify-center gap-2" style="color:var(--mx-text3)">
        <el-icon :size="30"><Picture /></el-icon>
        <span class="timecode">SHOT {{ String(shot.id).padStart(2, '0') }}</span>
        <span class="timecode" v-if="manifestItem?.status === 'rendering'" style="color:var(--mx-amber)">渲染中…</span>
      </div>
      <div v-if="manifestItem?.status === 'done'" class="absolute inset-0 flex items-center justify-center opacity-0 hover:opacity-100 transition-opacity"
           style="background:rgba(4,6,10,.35)">
        <div class="w-12 h-12 rounded-full flex items-center justify-center" style="background:rgba(6,8,14,.6);border:1px solid rgba(255,180,84,.5)">
          <el-icon :size="20" color="var(--mx-amber-hi)"><VideoPlay /></el-icon>
        </div>
      </div>
      <div class="absolute top-2 left-2 flex gap-1.5 items-center pointer-events-none">
        <span class="badge-mode" :class="shot.mode">{{ shot.mode?.toUpperCase() }}</span>
        <span class="badge-mode dur">{{ shot.duration }}s</span>
      </div>
      <!-- 右上:删除本镜(方案+产物),运行中禁用 -->
      <button class="cover-del" title="删除该镜头(方案与产物)" :disabled="running" @click.stop="removeShot">
        <el-icon :size="13"><Delete /></el-icon>
      </button>
    </div>
    <div class="p-3 flex flex-col gap-2">
      <div class="flex items-center gap-2 text-xs">
        <b>镜 {{ shot.id }}</b>
        <el-tag size="small" :type="statusTag.type" effect="dark">{{ statusTag.text }}</el-tag>
        <span v-if="manifestItem?.engine" class="timecode">{{ manifestItem.engine === 'comfy' ? '本地' : '云端' }}</span>
      </div>
      <div class="text-xs leading-relaxed" style="color:var(--mx-text2)">
        <div class="flex items-center gap-1.5 flex-wrap mb-1.5">
          <el-tooltip v-for="n in castNames" :key="n" :content="`出场角色:${n}`" placement="top">
            <span class="cast-chip"><el-icon :size="10"><User /></el-icon>{{ n }}</span>
          </el-tooltip>
          <el-tooltip v-if="sceneName" :content="`场景:${sceneName}`" placement="top">
            <span class="cast-chip scene"><el-icon :size="10"><Location /></el-icon>{{ sceneName }}</span>
          </el-tooltip>
        </div>
        <div><b style="color:var(--mx-cyan)">{{ shot.shot_size }}</b> · {{ shot.camera }}</div>
        <div class="mt-1">{{ shot.action }}</div>
        <div v-if="dialogues" class="mt-1" style="color:var(--mx-amber-hi);white-space:pre-line">{{ dialogues }}</div>
        <div v-if="shot.narration" class="mt-1" style="font-style:italic">旁白:{{ shot.narration }}</div>
      </div>
      <div v-if="manifestItem?.status === 'failed' && manifestItem?.error"
           class="text-xs p-2 rounded-lg" style="background:rgba(255,107,107,.07);color:var(--mx-red)">
        {{ manifestItem.error }}
      </div>
      <!-- 底部操作条:等宽三钮(提示词/微调 + 主操作 渲染|重渲 下拉),运行中整体禁用 -->
      <div class="card-ops">
        <el-tooltip content="查看完整 H3 提示词" placement="top">
          <button class="card-act" @click="G.promptShot = shot"><el-icon :size="13"><Document /></el-icon><span>提示词</span></button>
        </el-tooltip>
        <el-tooltip content="画布微调(提示词/时长/seed/引擎)" placement="top">
          <button class="card-act" :disabled="running" @click="G.tuneShot = shot"><el-icon :size="13"><Brush /></el-icon><span>微调</span></button>
        </el-tooltip>
        <el-dropdown trigger="click" :disabled="running" @command="rerender">
          <button class="card-act primary" :disabled="running" @click.stop>
            <el-icon :size="13"><RefreshRight /></el-icon>
            <span>{{ manifestItem?.status === 'done' ? '重渲' : '渲染' }}</span>
            <el-icon :size="10"><ArrowDown /></el-icon>
          </button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="">按项目配置</el-dropdown-item>
              <el-dropdown-item command="comfy">本地 ComfyUI</el-dropdown-item>
              <el-dropdown-item command="api">云端 MiniMax</el-dropdown-item>
              <el-dropdown-item divided command="api2k" :disabled="manifestItem?.status!=='done'">云端 2K 定稿(需先渲出)</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </div>
  </div>
</template>
