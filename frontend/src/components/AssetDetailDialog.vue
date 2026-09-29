<script setup>
// 资产详情弹窗（工作台「资产」页点图即开）。
//
// 【为什么单独抽一个组件，而不是把资产库那个弹窗整段搬过来】
// 资产库的详情弹窗带着一整套**运营操作**（抽卡/补全多视角/AI 写描述与提示词/删除）——
// 那些操作的前提是"这条资产在库里"。而工作台的资产可能**根本不在库里**（手动上传、
// 截帧入库失败、库被清过），把库侧操作搬过来只会给出点了报错的按钮。
// 所以这里做的是**同一套呈现**：图片墙（主图 + 多视角/三视图板）+ 中文描述 + 英文提示词，
// 图片按库条目的 `images` 全取；库里没有这条时退回项目内的那张图。
//
// 【层级】弹窗走 Element Plus 的弹层计数器（从 2000 起）。日志悬浮窗此前是 2600，
// 会盖住所有 el-dialog —— 已把它降到 1500（见 style.css 的 .log-pop）：常驻的旁观层
// 不该盖住用户主动打开的弹窗。
import { computed, ref, watch } from 'vue'
import { G, api, sourceLabel } from '../store'

const props = defineProps({
  open: { type: Boolean, default: false },
  kind: { type: String, default: 'characters' }, // characters / scenes / props
  name: { type: String, default: '' },
  libKey: { type: String, default: '' },   // 库内键（卡片 id 或名字）：按它取库条目的全部图片
  projectId: { type: String, default: '' }, // 库里没有这条时，用项目内的图
  assetFile: { type: String, default: '' },
  description: { type: String, default: '' },
  imagePrompt: { type: String, default: '' },
  source: { type: String, default: '' },
})
const emit = defineEmits(['update:open'])

const lib = ref(null)      // 库条目（拿得到就有全部图片）
const loading = ref(false)
const tried = ref('')

const kindWord = computed(() => ({ characters: '角色', scenes: '场景', props: '道具' }[props.kind] || '资产'))

// imgURL 库内图片地址（file 传基名，与资产库那边同一套口径）
function imgURL(file) {
  const base = `/api/library/${encodeURIComponent(props.kind)}/${encodeURIComponent(tried.value)}/image`
  const b = String(file || '').split('/').pop()
  return b ? `${base}?file=${encodeURIComponent(b)}` : base
}
function labelOf(file) {
  const b = String(file || '').split('/').pop() || ''
  if (b.includes('_board')) return '三视图细节板'
  if (b.includes('_turnaround')) return '多视角转台图'
  if (b.includes('_view_')) return '多视角原图'
  return '主图'
}
const images = computed(() => {
  if (lib.value?.images?.length) {
    return lib.value.images.map(f => ({ url: imgURL(f), label: labelOf(f), file: f }))
  }
  if (props.projectId && props.assetFile) {
    return [{ url: api.fileURL(props.projectId, props.assetFile), label: '主图', file: props.assetFile }]
  }
  return []
})
const desc = computed(() => lib.value?.description || props.description || '')
const prompt = computed(() => lib.value?.image_prompt || props.imagePrompt || '')

async function load() {
  lib.value = null
  tried.value = ''
  if (!props.open) return
  const key = String(props.libKey || props.name || '').trim()
  if (!key) return
  loading.value = true
  // 先按**卡片 id** 找（界面各处都用 id 关联），404 再退回名字 ——
  // 库条目的键可能是 id 也可能是名字（老条目按名字建目录），两条都试一次最稳。
  for (const k of [key, String(props.name || '').trim()].filter((v, i, a) => v && a.indexOf(v) === i)) {
    try {
      const d = await api.libraryDetail(props.kind, k)
      lib.value = d
      tried.value = k
      break
    } catch { /* 换下一个键 */ }
  }
  loading.value = false
}
watch(() => props.open, load, { immediate: true })

function preview(i) {
  G.viewer = { index: i, items: images.value.map(im => ({ type: 'image', src: im.url, title: `${props.name} · ${im.label}` })) }
}
</script>

<template>
  <el-dialog :model-value="open" :title="`${name} · ${kindWord}详情`" width="720px"
             top="6vh" append-to-body @update:model-value="emit('update:open', $event)">
    <div class="flex flex-col gap-4">
      <div v-if="images.length" class="grid gap-3" style="grid-template-columns:repeat(auto-fill,minmax(168px,1fr))">
        <div v-for="(im, i) in images" :key="im.file || i" class="ad-tile" @click="preview(i)">
          <img :src="im.url" :alt="im.label" loading="lazy" />
          <span class="ad-tag">{{ im.label }}</span>
        </div>
      </div>
      <el-empty v-else :description="loading ? '读取中…' : '这张资产还没有图（库里也没有它的条目）'" :image-size="60" />

      <div>
        <div class="ad-h">中文描述<span class="ad-src" v-if="source">{{ sourceLabel(source) }}</span></div>
        <div class="ad-body">{{ desc || '（无）' }}</div>
      </div>
      <div>
        <div class="ad-h">英文提示词（进图像模型的那一份）</div>
        <div class="ad-body ad-mono">{{ prompt || '（无）' }}</div>
      </div>
      <div class="text-xs" style="color:var(--mx-text3)">
        工作台这里是**只读呈现**；要抽卡 / 补全多视角 / 改描述与提示词，去「资产库」页点同一张卡。
      </div>
    </div>
    <template #footer><el-button @click="emit('update:open', false)">关闭</el-button></template>
  </el-dialog>
</template>

<style scoped>
.ad-tile {
  position: relative; border-radius: 8px; overflow: hidden; cursor: zoom-in;
  background: var(--mx-bg2); border: 1px solid var(--mx-line);
  aspect-ratio: 3/4; display: flex; align-items: center; justify-content: center;
}
.ad-tile img { width: 100%; height: 100%; object-fit: contain; }
.ad-tag {
  position: absolute; left: 6px; bottom: 6px; font-size: 10px; padding: 1px 6px; border-radius: 6px;
  background: color-mix(in srgb, var(--mx-bg0) 68%, transparent); color: var(--mx-text2);
}
.ad-h { font-size: 12px; color: var(--mx-text2); margin-bottom: 4px; display: flex; align-items: center; gap: 8px; }
.ad-src { font-size: 10px; color: var(--mx-text3); }
.ad-body {
  font-size: 12px; line-height: 1.7; color: var(--mx-text);
  background: var(--mx-bg2); border: 1px solid var(--mx-line); border-radius: 8px;
  padding: 8px 10px; max-height: 180px; overflow-y: auto; white-space: pre-wrap; word-break: break-word;
}
.ad-mono { font-family: var(--mx-mono); font-size: 11px; }
</style>
