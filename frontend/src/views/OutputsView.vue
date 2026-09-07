<script setup>
import { ref, onMounted, watch } from 'vue'
import { G, api, refreshProjects } from '../store'
import { ElMessage, ElMessageBox } from 'element-plus'

const list = ref([])
const loading = ref(false)

async function refresh() {
  loading.value = true
  try { list.value = await fetch('/api/outputs').then(r => r.json()) }
  catch { ElMessage.error('读取失败') }
  loading.value = false
}
onMounted(refresh)
watch(() => G.tick.outputs, refresh)

function play(item, which) {
  const src = which === '2k' ? item.final_2k : item.final
  G.viewer = {
    index: 0,
    items: [{
      type: 'video',
      src: api.fileURL(item.project, src),
      title: `《${item.title}》 ${which === '2k' ? '· 2K 定稿' : '· 768P'}`,
      meta: `${item.duration.toFixed(1)}s · ${item.shots_done}/${item.shots} 镜`,
      kindLabel: 'FINAL CUT',
    }],
  }
}
async function openDir(item) {
  try { await api.openExplorer('output', item.project) } catch (e) { ElMessage.error(e.message) }
}
async function removeOut(item, what, label) {
  try {
    await ElMessageBox.confirm(`删除「${item.title}」的${label}?项目与镜头产物不受影响。`, '删除成片',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  try {
    await fetch(`/api/projects/${encodeURIComponent(item.project)}/output`, {
      method: 'DELETE', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ what }),
    }).then(async r => { const d = await r.json(); if (!r.ok) throw new Error(d.error) })
    ElMessage.success('已删除')
    refresh()
  } catch (e) { ElMessage.error(e.message) }
}
function enter(item) {
  api.project(item.project).then(p => { G.project = p; G.view = 'project' }).catch(e => ElMessage.error(e.message))
}
const fmtDur = (s) => `${Math.floor(s / 60)}:${String(Math.round(s % 60)).padStart(2, '0')}`
</script>

<template>
  <div class="h-full overflow-y-auto p-6">
    <div class="panel panel-glow p-6 mb-5 relative overflow-hidden">
      <div style="position:absolute;right:-20px;top:-40px;font-size:130px;opacity:.06;pointer-events:none">🎞️</div>
      <div class="flex items-center gap-4 flex-wrap">
        <div>
          <h1 class="text-lg font-bold m-0">作品库 <span class="timecode">FINAL CUTS</span></h1>
          <p class="text-xs m-0 mt-1" style="color:var(--mx-text2)">全部项目的成片集中管理 · 多版本 · 全屏预览 · 素材导出</p>
        </div>
        <div class="flex-1"></div>
        <div class="flex gap-6 mr-2">
          <div class="text-center"><div class="stat-num">{{ list.length }}</div><div class="stat-label">项目</div></div>
          <div class="text-center"><div class="stat-num">{{ list.filter(x => x.final_2k).length }}</div><div class="stat-label">2K 版</div></div>
        </div>
        <el-button size="small" circle @click="refresh" :loading="loading"><el-icon><Refresh /></el-icon></el-button>
      </div>
    </div>

    <div v-if="!list.length" class="panel p-14 text-center" style="color:var(--mx-text3)">
      还没有成片 —— 完成任意项目的「合成成片」后,作品会出现在这里
    </div>

    <div class="grid gap-5" style="grid-template-columns:repeat(auto-fill,minmax(360px,1fr))">
      <div v-for="it in list" :key="it.project" class="shot-card proj-card">
        <!-- 封面:项目定妆照;点击播放标准版 -->
        <div class="proj-cover cursor-pointer" @click="it.final && play(it, 'std')">
          <img v-if="it.cover" :src="api.fileURL(it.project, it.cover)" alt=""
               style="position:absolute;inset:0;width:100%;height:100%;object-fit:cover;filter:saturate(1.08)" />
          <div v-else class="absolute inset-0 flex flex-col items-center justify-center gap-2" style="pointer-events:none">
            <el-icon :size="34" color="var(--mx-text3)"><Film /></el-icon>
            <span class="timecode">{{ it.shots ? it.shots + ' 镜' : 'NEW' }}</span>
          </div>
          <div class="absolute inset-0 flex items-center justify-center" style="pointer-events:none">
            <div v-if="it.final || it.final_2k" class="w-14 h-14 rounded-full flex items-center justify-center"
                 style="background:rgba(6,8,14,.55);border:1px solid rgba(255,180,84,.5);backdrop-filter:blur(4px)">
              <el-icon :size="22" color="var(--mx-amber-hi)"><VideoPlay /></el-icon>
            </div>
            <span v-else-if="it.cover" class="timecode">未合成</span>
          </div>
          <div class="absolute top-3 left-3 flex gap-1.5">
            <span v-if="it.final_2k" class="badge-mode">2K</span>
            <span v-if="it.final" class="badge-mode t2va">768P</span>
          </div>
          <div v-if="it.final" class="absolute bottom-3 right-3 timecode px-2 py-0.5 rounded"
               style="background:rgba(6,8,14,.7)">{{ fmtDur(it.duration) }}</div>
        </div>
        <div class="p-4 flex flex-col gap-2.5">
          <div class="flex items-center gap-2">
            <b class="truncate flex-1">《{{ it.title }}》</b>
            <span class="timecode">{{ it.shots_done }}/{{ it.shots }} 镜</span>
          </div>
          <div class="text-xs" style="color:var(--mx-text3)">
            {{ it.updated_at }}
            <template v-if="it.final"> · {{ it.size_mb.toFixed(1) }}MB</template>
            <template v-if="it.final_2k"> · 2K {{ it.size_2k_mb.toFixed(1) }}MB</template>
            <template v-if="it.pack"> · 素材包✓</template>
          </div>
          <div class="flex gap-2 flex-wrap pt-1">
            <el-button size="small" type="primary" :disabled="!it.final" @click="play(it,'std')"><el-icon class="mr-1"><VideoPlay /></el-icon>播放</el-button>
            <el-button size="small" :disabled="!it.final_2k" @click="play(it,'2k')"><el-icon class="mr-1"><MagicStick /></el-icon>2K</el-button>
            <el-button size="small" @click="enter(it)"><el-icon class="mr-1"><EditPen /></el-icon>进入项目</el-button>
            <el-button size="small" @click="openDir(it)"><el-icon class="mr-1"><FolderOpened /></el-icon>目录</el-button>
            <el-button size="small" type="danger" plain :disabled="!it.final && !it.final_2k" @click="removeOut(it,'all','全部成片产物')">
              <el-icon><Delete /></el-icon>
            </el-button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
