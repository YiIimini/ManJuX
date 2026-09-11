<script setup>
// 首页项目/剧集卡网格(纯展示 + 交互,状态与 API 调用全部留在 HomeView)
// 拆集占位卡 / 剧集系列卡 / 批量勾选卡 / 单项目卡四类卡片
import { api, phaseLabel } from '../store'

defineProps({
  seriesGroups: { type: Object, required: true },   // {groups: {前缀: [集]}, singles: [项目]}
  novelJobs: { type: Array, default: () => [] },    // 拆集任务(运行中/失败占位卡)
  batch: { type: Boolean, default: false },         // 批量管理模式
  sel: { required: true },                          // Set(已勾选的管理单元 key)
  batchStat: { type: Object, default: null },       // 批量产线状态 {prefix,current,total,running}
  running: { type: Object, default: () => ({}) },   // G.status.running(项目运行态)
})

defineEmits([
  'open-project', 'open-series', 'toggle-sel', 'clear-job',
  'start-project-run', 'stop-project-run', 'remove-project',
  'start-series-batch', 'stop-series-batch', 'remove-series',
])

// 卡片状态 chip(纯展示映射)
function stateChip(p) {
  if (p.running) return { t: `${phaseLabel(p.phase)} · 运行中`, c: 'run' }
  if (p.shots_total && p.shots_done >= p.shots_total) return { t: '已完成', c: 'ok' }
  if (p.shots_done > 0) return { t: `渲染 ${p.shots_done}/${p.shots_total}`, c: 'warn' }
  if (p.has_plan) return { t: '待渲染', c: '' }
  return { t: '待创作', c: '' }
}
</script>

<template>
  <div v-if="batch || seriesGroups.singles.length || Object.keys(seriesGroups.groups).length
           || (!batch && novelJobs.some(j => j.state === 'running' || j.state === 'failed'))"
       class="grid gap-5" style="grid-template-columns:repeat(auto-fill,minmax(252px,1fr))">
    <!-- 拆集进行中:书级占位卡(提交即出现,分集陆续填充) -->
    <div v-for="j in novelJobs.filter(x => x.state === 'running' || x.state === 'failed')"
         :key="'job-' + j.series" class="shot-card proj-card" style="border-style:dashed">
      <div class="proj-cover">
        <div class="absolute inset-0 flex flex-col items-center justify-center gap-2">
          <el-icon :size="30" :color="j.state === 'running' ? 'var(--mx-amber)' : 'var(--mx-red)'">
            <Loading v-if="j.state === 'running'" class="is-loading" /><CircleCloseFilled v-else />
          </el-icon>
          <span class="timecode">{{ j.state === 'running' ? 'AI 拆集中' : '拆集失败' }}</span>
        </div>
        <button v-if="j.state === 'failed'" class="cover-del" title="移除失败占位卡" @click.stop="$emit('clear-job', j)">
          <el-icon :size="13"><Delete /></el-icon>
        </button>
      </div>
      <div class="px-3.5 py-3 flex flex-col gap-2">
        <div class="flex items-center gap-2">
          <b class="truncate flex-1 text-sm">《{{ j.series }}》</b>
          <span class="timecode" style="font-size:10px" :style="j.state === 'failed' ? 'color:var(--mx-red)' : ''">
            {{ j.state === 'running' ? (j.created ? ('已建 ' + j.created + ' 集') : '通读中…') : '失败' }}
          </span>
        </div>
        <div class="text-xs truncate" :style="{ color: j.state === 'failed' ? 'var(--mx-red)' : 'var(--mx-text3)' }">{{ j.message }}</div>
      </div>
    </div>

    <!-- 剧集系列卡(点击打开逐集弹窗) -->
    <div v-if="!batch" v-for="(eps, prefix) in seriesGroups.groups" :key="'s-' + prefix"
         class="shot-card proj-card cursor-pointer" :title="'打开《' + prefix + '》剧集管理(逐集进入/删除/批量产线)'" @click="$emit('open-series', prefix, eps)">
      <div class="proj-cover">
        <template v-if="eps.find(e => e.cover)">
          <img class="cover-fit" :src="api.fileURL(eps.find(e => e.cover).id, eps.find(e => e.cover).cover)" alt="" aria-hidden="true" />
          <img class="cover-full" :src="api.fileURL(eps.find(e => e.cover).id, eps.find(e => e.cover).cover)" alt="" style="filter:saturate(1.06)" />
        </template>
        <div v-else class="absolute inset-0 flex flex-col items-center justify-center gap-2">
          <el-icon :size="30" color="var(--mx-text3)"><Film /></el-icon>
          <span class="timecode">{{ eps.length }} 集</span>
        </div>
        <div class="absolute inset-0 flex items-center justify-center" style="pointer-events:none">
          <div class="px-3 py-1.5 rounded-lg flex items-center gap-2"
               style="background:rgba(6,8,14,.55);border:1px solid rgba(255,180,84,.4);backdrop-filter:blur(4px)">
            <el-icon :size="16" color="var(--mx-amber-hi)"><Folder /></el-icon>
            <span class="text-xs" style="color:var(--mx-amber-hi)">剧集合集</span>
          </div>
        </div>
        <!-- 封面右上:批量运行灯(左)与删除整部(右),批量运行中禁用删除 -->
        <i v-if="batchStat && batchStat.prefix === prefix" class="dot run"
           style="position:absolute;top:12px;right:106px"></i>
        <button class="cover-del" style="right:74px" title="开始:启动批量产线(从中断处续跑)"
                :disabled="!!(batchStat && batchStat.running)" @click.stop="$emit('start-series-batch', prefix)">
          <el-icon :size="13"><VideoPlay /></el-icon>
        </button>
        <button class="cover-del" style="right:42px" title="停止:停止管线并释放显存"
                :disabled="!(batchStat && batchStat.prefix === prefix)" @click.stop="$emit('stop-series-batch')">
          <el-icon :size="13"><VideoPause /></el-icon>
        </button>
        <button class="cover-del" title="删除整部系列(全部集)" :disabled="!!(batchStat && batchStat.prefix === prefix)"
                @click.stop="$emit('remove-series', prefix, eps)">
          <el-icon :size="13"><Delete /></el-icon>
        </button>
      </div>
      <div class="px-3.5 py-3 flex flex-col gap-2">
        <div class="flex items-center gap-2">
          <b class="truncate flex-1 text-sm">《{{ prefix }}》</b>
          <span class="timecode" style="font-size:10px">
            {{ (batchStat && batchStat.prefix === prefix) ? ('批量中 ' + batchStat.current + '/' + batchStat.total) : (eps.filter(e => e.shots_total && e.shots_done >= e.shots_total).length + '/' + eps.length + ' 集') }}
          </span>
        </div>
        <el-progress
          :percentage="Math.round(eps.reduce((a, e) => a + (e.shots_done || 0), 0) * 100 / Math.max(1, eps.reduce((a, e) => a + (e.shots_total || 0), 0)))"
          :stroke-width="5" :show-text="false" :color="'linear-gradient(90deg, var(--mx-amber-hi), var(--mx-amber-deep))'" />
      </div>
    </div>

    <!-- 批量模式:系列=一个管理单元(勾=整书删除) -->
    <div v-if="batch" v-for="(eps, prefix) in seriesGroups.groups" :key="'bs-' + prefix"
         class="shot-card proj-card cursor-pointer relative" @click="$emit('toggle-sel', 'series:' + prefix)">
      <div v-if="batch" class="absolute top-2 left-2 z-10" @click.stop="$emit('toggle-sel', 'series:' + prefix)">
        <el-checkbox :model-value="sel.has('series:' + prefix)" size="large" />
      </div>
      <div class="proj-cover">
        <div class="absolute inset-0 flex flex-col items-center justify-center gap-2">
          <el-icon :size="30" color="var(--mx-text3)"><Folder /></el-icon>
          <span class="timecode">{{ eps.length }} 集</span>
        </div>
      </div>
      <div class="px-3.5 py-3 flex flex-col gap-2">
        <div class="flex items-center gap-2">
          <b class="truncate flex-1 text-sm">《{{ prefix }}》</b>
          <span class="timecode" style="font-size:10px">整部系列</span>
        </div>
      </div>
    </div>

    <div v-for="p in (batch ? seriesGroups.singles : seriesGroups.singles)" :key="p.id" class="shot-card proj-card cursor-pointer relative"
         :title="batch ? '勾选加入批量删除' : '进入「' + p.name + '」工作台'" @click="batch ? $emit('toggle-sel', p.id) : $emit('open-project', p.id)">
      <div v-if="batch" class="absolute top-2 left-2 z-10" @click.stop="$emit('toggle-sel', p.id)">
        <el-checkbox :model-value="sel.has(p.id)" size="large" />
      </div>
      <div class="proj-cover">
        <template v-if="p.cover">
          <img class="cover-fit" :src="api.fileURL(p.id, p.cover)" alt="" aria-hidden="true" />
          <img class="cover-full" :src="api.fileURL(p.id, p.cover)" alt="" style="filter:saturate(1.06)" />
        </template>
        <div v-else class="absolute inset-0 flex flex-col items-center justify-center gap-2">
          <el-icon :size="30" color="var(--mx-text3)"><Film /></el-icon>
          <span class="timecode">{{ p.has_plan ? p.shots_total + ' 镜' : 'NEW' }}</span>
        </div>
        <!-- 封面右上:状态灯(左)与删除(右)保持间距,不重叠 -->
        <i class="dot" :class="stateChip(p).c" style="position:absolute;top:12px;right:106px"></i>
        <button class="cover-del" style="right:74px" title="开始:启动管线(开始/续跑)"
                :disabled="!!running[p.id]" @click.stop="$emit('start-project-run', p)">
          <el-icon :size="13"><VideoPlay /></el-icon>
        </button>
        <button class="cover-del" style="right:42px" title="停止:停止管线并释放显存"
                :disabled="!running[p.id]" @click.stop="$emit('stop-project-run', p)">
          <el-icon :size="13"><VideoPause /></el-icon>
        </button>
        <button v-if="!batch" class="cover-del" title="删除项目" @click.stop="$emit('remove-project', p)">
          <el-icon :size="13"><Delete /></el-icon>
        </button>
      </div>
      <div class="px-3.5 py-3 flex flex-col gap-2">
        <div class="flex items-center gap-2">
          <b class="truncate flex-1 text-sm">{{ p.name }}</b>
          <span class="timecode" style="font-size:10px">{{ stateChip(p).t }}</span>
        </div>
        <el-progress v-if="p.shots_total" :percentage="Math.round(p.shots_done * 100 / p.shots_total)"
                     :stroke-width="5" :show-text="false"
                     :color="'linear-gradient(90deg, var(--mx-amber-hi), var(--mx-amber-deep))'" />
      </div>
    </div>
  </div>
</template>
