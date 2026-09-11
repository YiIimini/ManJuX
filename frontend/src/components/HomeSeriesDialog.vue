<script setup>
// 剧集合集弹窗:逐集管理(进入/单独删除/批量产线)
// seriesDlg = {prefix, eps} | null;批量产线状态与业务动作留在 HomeView
import { phaseLabel } from '../store'

const seriesDlg = defineModel('seriesDlg', { default: null })
defineProps({
  batchStat: { type: Object, default: null },   // 批量产线状态 {prefix,current,total,running}
})
defineEmits(['open-project', 'del-episode', 'start-batch', 'stop-batch'])
</script>

<template>
  <el-dialog :model-value="!!seriesDlg" :title="seriesDlg ? ('《' + seriesDlg.prefix + '》· 剧集合集(' + seriesDlg.eps.length + ' 集)') : ''"
             width="640px" @update:model-value="seriesDlg = null">
    <template v-if="seriesDlg">
      <div class="flex items-center gap-3 mb-3">
        <el-progress style="flex:1"
          :percentage="Math.round(seriesDlg.eps.reduce((a, e) => a + (e.shots_done || 0), 0) * 100 / Math.max(1, seriesDlg.eps.reduce((a, e) => a + (e.shots_total || 0), 0)))"
          :stroke-width="6" :color="'linear-gradient(90deg, var(--mx-amber-hi), var(--mx-amber-deep))'" />
        <span class="timecode">{{ seriesDlg.eps.filter(e => e.shots_total && e.shots_done >= e.shots_total).length }}/{{ seriesDlg.eps.length }} 集完成</span>
      </div>
      <div class="flex flex-col gap-2" style="max-height:46vh;overflow-y:auto">
        <div v-for="e in seriesDlg.eps" :key="e.id"
             class="flex items-center gap-3 p-2.5 rounded-lg cursor-pointer ep-row"
             @click="seriesDlg = null; $emit('open-project', e.id)">
          <span class="timecode" style="width:44px">{{ (e.name.match(/·第(\d+)集/) || [])[1] || '—' }}</span>
          <i class="dot" :class="e.running ? 'run' : (e.shots_total && e.shots_done >= e.shots_total) ? 'ok' : (e.shots_done > 0 ? 'warn' : '')"></i>
          <div class="flex-1 min-w-0">
            <div class="text-sm truncate">{{ e.name.split('·').slice(2).join('·') || e.name }}</div>
            <div class="text-xs" style="color:var(--mx-text3)">{{ e.running ? (phaseLabel(e.phase) + ' · 运行中') : (e.shots_total ? ('镜头 ' + e.shots_done + '/' + e.shots_total) : '待创作') }}</div>
          </div>
          <el-tooltip content="删除本集(仅这一集)" placement="top">
            <el-button size="small" text type="danger" @click.stop="$emit('del-episode', e)"><el-icon><Delete /></el-icon></el-button>
          </el-tooltip>
          <el-button size="small" text type="primary" @click.stop="seriesDlg = null; $emit('open-project', e.id)"><el-icon><ArrowRight /></el-icon>进入</el-button>
        </div>
      </div>
    </template>
    <template #footer>
      <template v-if="batchStat && seriesDlg && batchStat.prefix === seriesDlg.prefix">
        <span class="text-xs mr-2 flex items-center gap-1.5" style="color:var(--mx-amber)"><i class="dot run"></i>批量中 第 {{ batchStat.current }}/{{ batchStat.total }} 集(已成片自动跳过,支持中断续跑)</span>
        <el-button type="danger" plain @click="$emit('stop-batch')"><el-icon class="mr-1"><VideoPause /></el-icon>停止批量</el-button>
      </template>
      <el-button v-else type="primary" plain :disabled="!seriesDlg || !!batchStat" @click="seriesDlg && $emit('start-batch', seriesDlg.prefix)">
        <el-icon class="mr-1"><VideoCameraFilled /></el-icon>{{ batchStat ? '批量运行中…' : '批量产线(断点续跑)' }}
      </el-button>
      <el-button @click="seriesDlg = null">关闭</el-button>
    </template>
  </el-dialog>
</template>
