<script setup>
import { ref, computed, onMounted, watch } from 'vue'
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

// ---- 分集分组管理:「书名·第NN集·标题」的成片按书名归组(组头折叠),单项目平铺 ----
const seriesRe = /^(.+)·第\d+集/
const seriesGroups = computed(() => {
  const groups = {}
  const singles = []
  for (const it of list.value) {
    const m = (it.project || '').match(seriesRe)
    if (m) (groups[m[1]] = groups[m[1]] || []).push(it)
    else singles.push(it)
  }
  const names = Object.keys(groups).sort((a, b) => a.localeCompare(b, 'zh'))
  for (const k of names) groups[k].sort((a, b) => a.project.localeCompare(b.project, 'zh'))
  return { names, groups, singles }
})
// 折叠状态持久化(刷新/重进不丢)
const collapsed = ref(JSON.parse(localStorage.getItem('mx-out-groups') || '{}'))
function toggleGroup(k) {
  collapsed.value = { ...collapsed.value, [k]: !collapsed.value[k] }
  localStorage.setItem('mx-out-groups', JSON.stringify(collapsed.value))
}
// 统一渲染段:系列组(带头) + 单项目段
const sections = computed(() => {
  const sec = []
  for (const n of seriesGroups.value.names) {
    sec.push({ key: 's' + n, header: n, items: seriesGroups.value.groups[n] })
  }
  if (seriesGroups.value.singles.length) sec.push({ key: 'singles', items: seriesGroups.value.singles })
  return sec
})
// 集数编号(两位,与剧集弹窗/首页项目编号对齐;非系列返回空)
function epNo(it) {
  const m = (it.project || '').match(/·第(\d+)集/)
  return m ? String(Number(m[1])).padStart(2, '0') : ''
}

// 批量删除成片产物
const batch = ref(false)
const sel = ref(new Set())
function batchSelectGroup(sec) {
  batch.value = true
  sel.value = new Set(sec.items.map(i => i.project))
  ElMessage.info(`已勾选《${sec.header}》${sec.items.length} 集,确认后点「删除选中」`)
}
function toggleSel(id) {
  if (sel.value.has(id)) sel.value.delete(id)
  else sel.value.add(id)
  sel.value = new Set(sel.value)
}
const allSelected = computed(() => list.value.length > 0 && list.value.every(it => sel.value.has(it.project)))
function toggleAllSel() {
  sel.value = allSelected.value ? new Set() : new Set(list.value.map(it => it.project))
}
async function batchDelete() {
  if (!sel.value.size) return
  try {
    await ElMessageBox.confirm(`删除选中的 ${sel.value.size} 个项目的成片产物?项目与镜头产物不受影响。`, '批量删除成片',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  let ok = 0, fail = 0
  for (const pid of sel.value) {
    try {
      await fetch(`/api/projects/${encodeURIComponent(pid)}/output`, {
        method: 'DELETE', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ what: 'all' }),
      })
      ok++
    } catch { fail++ }
  }
  sel.value = new Set()
  batch.value = false
  ElMessage.success(`已删除 ${ok} 项` + (fail ? `,${fail} 个失败` : ''))
  refresh()
}
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
        <template v-if="batch">
          <span class="text-xs mr-2" style="color:var(--mx-amber)">已选 {{ sel.size }} 项</span>
          <el-button size="small" plain @click="toggleAllSel">{{ allSelected ? '取消全选' : '全选' }}</el-button>
          <el-button size="small" type="danger" :disabled="!sel.size" @click="batchDelete">
            <el-icon class="mr-1"><Delete /></el-icon>删除选中
          </el-button>
          <el-button size="small" @click="batch = false; sel = new Set()">退出批量</el-button>
        </template>
        <el-tooltip v-else content="勾选多个项目批量删除成片产物" placement="top">
          <el-button size="small" plain :disabled="!list.length" @click="batch = true; sel = new Set()">
            <el-icon class="mr-1"><Operation /></el-icon>批量管理
          </el-button>
        </el-tooltip>
        <el-button size="small" circle @click="refresh" :loading="loading"><el-icon><Refresh /></el-icon></el-button>
      </div>
    </div>

    <div v-if="!list.length" class="panel empty-zone">
      <div class="ez-ico"><el-icon :size="26"><VideoCamera /></el-icon></div>
      <div class="ez-title">还没有成片</div>
      <p class="ez-desc">完成任意项目的「合成成片」阶段后,成片会按剧集分组集中出现在这里,可播放、升 2K、导出素材包。</p>
    </div>

    <!-- 分集分组:剧集(书名·第NN集)成片按书归组,组头可折叠;单项目平铺 -->
    <template v-for="sec in sections" :key="sec.key">
      <!-- 系列组头 -->
      <div v-if="sec.header" class="panel px-4 py-2.5 flex items-center gap-3 cursor-pointer select-none"
           style="margin:14px 0 12px" @click="toggleGroup(sec.header)" :title="collapsed[sec.header] ? '展开该集成片' : '收起该集成片'">
        <el-icon :size="16" color="var(--mx-amber)"><Folder /></el-icon>
        <b class="text-sm">《{{ sec.header }}》</b>
        <span class="timecode">{{ sec.items.length }} 集 · {{ sec.items.filter(x => x.final || x.final_2k).length }} 有成片</span>
        <div class="flex-1"></div>
        <button class="text-xs flex items-center gap-1 px-2 py-1 rounded"
                style="color:var(--mx-text3);border:1px solid var(--mx-line);background:transparent"
                title="勾选本组全部项目进入批量删除" @click.stop="batchSelectGroup(sec)">
          <el-icon :size="12"><Delete /></el-icon>批量删除
        </button>
        <span class="text-xs" style="color:var(--mx-text3)">{{ collapsed[sec.header] ? '展开' : '收起' }}</span>
        <el-icon :size="13" style="transition:transform .18s" :style="collapsed[sec.header] ? '' : 'transform:rotate(180deg)'"><ArrowUp /></el-icon>
      </div>
      <div v-show="sec.header ? !collapsed[sec.header] : true" class="grid gap-5"
           :class="sec.header ? '' : 'mt-3'"
           style="grid-template-columns:repeat(auto-fill,minmax(286px,1fr))">
        <div v-for="it in sec.items" :key="it.project" class="shot-card proj-card relative"
             @click="batch && toggleSel(it.project)">
        <div v-if="batch" class="absolute top-2 left-2 z-10 bg-black/40 rounded px-1" @click.stop="toggleSel(it.project)">
          <el-checkbox :model-value="sel.has(it.project)" size="large" />
        </div>
        <!-- 封面:项目定妆照;点击播放标准版 -->
        <div class="proj-cover cursor-pointer" @click="it.final && play(it, 'std')">
          <template v-if="it.cover">
            <img class="cover-fit" :src="api.fileURL(it.project, it.cover)" alt="" aria-hidden="true" />
            <img class="cover-full" :src="api.fileURL(it.project, it.cover)" alt="" style="filter:saturate(1.06)" />
          </template>
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
            <span v-if="epNo(it)" class="badge-mode ep">第 {{ epNo(it) }} 集</span>
            <span v-if="it.final_2k" class="badge-mode">2K</span>
            <span v-if="it.final" class="badge-mode t2va">768P</span>
          </div>
          <!-- 删除:封面右上角 -->
          <button class="cover-del" @click.stop="removeOut(it,'all','全部成片产物')"
                  :disabled="!it.final && !it.final_2k" title="删除成片产物">
            <el-icon :size="13"><Delete /></el-icon>
          </button>
          <div v-if="it.final" class="absolute bottom-3 right-3 timecode px-2 py-0.5 rounded"
               style="background:rgba(6,8,14,.7)">{{ fmtDur(it.duration) }}</div>
        </div>
        <div class="px-3.5 py-3 flex flex-col gap-2">
          <div class="flex items-center gap-2">
            <b class="truncate flex-1 text-sm">《{{ it.title }}》</b>
            <span class="timecode">{{ it.shots_done }}/{{ it.shots }} 镜</span>
          </div>
          <div class="text-xs" style="color:var(--mx-text3)">
            {{ it.updated_at }}
            <template v-if="it.final"> · {{ it.size_mb.toFixed(1) }}MB</template>
            <template v-if="it.final_2k"> · 2K {{ it.size_2k_mb.toFixed(1) }}MB</template>
            <template v-if="it.pack"> · 素材包✓</template>
          </div>
          <div class="flex gap-2 flex-wrap">
            <el-tooltip content="全屏播放 768P 标准版成片" placement="top">
              <el-button size="small" type="primary" :disabled="!it.final" @click="play(it,'std')"><el-icon class="mr-1"><VideoPlay /></el-icon>播放</el-button>
            </el-tooltip>
            <el-tooltip content="播放云端 2K 定稿版(若有)" placement="top">
              <el-button size="small" :disabled="!it.final_2k" @click="play(it,'2k')"><el-icon class="mr-1"><MagicStick /></el-icon>2K</el-button>
            </el-tooltip>
            <el-tooltip content="打开该项目的工作台(分镜/资产/产线)" placement="top">
              <el-button size="small" @click="enter(it)"><el-icon class="mr-1"><EditPen /></el-icon>进入工作台</el-button>
            </el-tooltip>
          </div>
        </div>
      </div>
      </div>
    </template>
  </div>
</template>
