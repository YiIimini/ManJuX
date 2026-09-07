<script setup>
import { G, api } from '../store'
import { onMounted, ref, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const lib = ref({ characters: [], scenes: [] })
const kind = ref('characters')
const loading = ref(false)

async function refresh() {
  loading.value = true
  try {
    lib.value = await fetch('/api/library').then(r => r.json())
  } catch (e) { ElMessage.error(e.message) }
  loading.value = false
}
onMounted(refresh)

const list = computed(() => lib.value[kind.value] || [])

const kindLabel = { characters: '角色', scenes: '场景' }

async function remove(e) {
  try {
    await ElMessageBox.confirm(
      `从资产库删除「${e.name}」?不影响已使用它的项目,只影响后续复用。`, '删除库资产',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  try {
    await fetch(`/api/library/${kind.value}/${encodeURIComponent(e.name)}`, { method: 'DELETE' })
    ElMessage.success('已删除')
    refresh()
  } catch (err) { ElMessage.error(err.message) }
}
</script>

<template>
  <div class="h-full overflow-y-auto p-6">
    <div class="panel p-5 mb-5 relative overflow-hidden">
      <h1 class="text-lg font-bold m-0 mb-1">资产库 <span class="timecode">ASSET LIBRARY</span></h1>
      <p class="text-sm m-0" style="color:var(--mx-text-2)">
        角色/场景一次定妆,<b style="color:var(--mx-orange)">全项目复用</b>——新项目遇到同名资产自动引用(免生成、天然形象一致)。
        Krea-2 生成与手动上传的资产都会自动入库;复用计数排序。
      </p>
    </div>

    <div class="flex items-center gap-3 mb-4">
      <el-radio-group v-model="kind">
        <el-radio-button value="characters">角色({{ lib.characters?.length || 0 }})</el-radio-button>
        <el-radio-button value="scenes">场景({{ lib.scenes?.length || 0 }})</el-radio-button>
      </el-radio-group>
      <div class="flex-1"></div>
      <el-button size="small" @click="refresh" :loading="loading">刷新</el-button>
    </div>

    <div v-if="!list.length" class="panel p-10 text-center" style="color:var(--mx-text-2)">
      库还是空的 —— 在任意项目跑一次「资产定妆」(Krea-2 生成或上传),资产会自动出现在这里
    </div>

    <div class="grid gap-4" style="grid-template-columns:repeat(auto-fill,minmax(190px,1fr))">
      <div v-for="e in list" :key="e.kind + e.name" class="shot-card p-3 flex flex-col gap-2">
        <div style="aspect-ratio:3/4;background:var(--mx-bg-2);border-radius:8px;overflow:hidden">
          <img :src="`/api/library/${e.kind}/${encodeURIComponent(e.name)}/image`"
               style="width:100%;height:100%;object-fit:cover" loading="lazy" />
        </div>
        <div class="flex items-center gap-2">
          <b class="text-sm truncate flex-1">{{ e.name }}</b>
          <el-tag size="small" effect="plain" :type="e.source === 'krea2' ? 'warning' : 'info'">
            {{ e.source === 'krea2' ? 'Krea-2' : '上传' }}
          </el-tag>
          <el-button text size="small" @click="remove(e)"><el-icon><Delete /></el-icon></el-button>
        </div>
        <div class="text-xs" style="color:var(--mx-text-2)">
          ♻ 复用 {{ e.used_count }} 次 · 来自「{{ e.source_project || '-' }}」
        </div>
      </div>
    </div>
  </div>
</template>
