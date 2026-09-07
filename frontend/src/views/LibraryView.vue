<script setup>
import { G, api } from '../store'
import { onMounted, ref, reactive, computed, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const lib = ref({ characters: [], scenes: [] })
const kind = ref('characters')
const loading = ref(false)

async function refresh() {
  loading.value = true
  try {
    const r = await fetch('/api/library')
    const d = await r.json().catch(() => ({}))
    if (!r.ok) throw new Error(d.error || `服务响应 ${r.status}`)
    lib.value = d.characters || d.scenes ? d : { characters: [], scenes: [] }
  } catch (e) { ElMessage.error('资产库读取失败:' + e.message) }
  loading.value = false
}
onMounted(refresh)
watch(() => G.tick.library, refresh)

const list = computed(() => lib.value[kind.value] || [])

const kindLabel = { characters: '角色', scenes: '场景' }

// ---- 抽卡 & 自定义新建(仅角色)----
const gachaBusy = ref(false)
const gachaResult = ref(null) // {entry, theme, trait}
async function gacha() {
  gachaBusy.value = true
  gachaResult.value = null
  try {
    const r = await fetch('/api/library/gacha', { method: 'POST' })
    const d = await r.json()
    if (!r.ok) throw new Error(d.error)
    gachaResult.value = d
    ElMessage.success(`抽到「${d.entry.name}」!题材:${d.theme}`)
    refresh()
  } catch (e) { ElMessage.error(e.message) } finally { gachaBusy.value = false }
}

const createOpen = ref(false)
const createBusy = ref(false)
const createForm = reactive({ name: '', description: '', image_prompt: '', mode: 'generate' })
const createImage = ref(null)
const createInput = ref(null)
function onPickImg(ev) {
  const f = ev.target.files?.[0]
  ev.target.value = ''
  if (!f) return
  if (!/^image\/(png|jpe?g|webp)/.test(f.type)) { ElMessage.error('仅支持 png/jpg/webp'); return }
  createImage.value = f
}
async function doCreate() {
  if (!createForm.name.trim()) { ElMessage.warning('请填写角色名'); return }
  createBusy.value = true
  try {
    const fd = new FormData()
    fd.append('name', createForm.name.trim())
    fd.append('description', createForm.description)
    if (createForm.mode === 'upload') {
      if (!createImage.value) { ElMessage.warning('请选择上传图片'); createBusy.value = false; return }
      fd.append('image', createImage.value)
    } else {
      if (!createForm.image_prompt.trim()) { ElMessage.warning('请填写生成提示词'); createBusy.value = false; return }
      fd.append('image_prompt', createForm.image_prompt.trim())
    }
    const r = await fetch('/api/library/characters', { method: 'POST', body: fd })
    const d = await r.json()
    if (!r.ok) throw new Error(d.error)
    ElMessage.success(`角色「${d.entry.name}」已入库`)
    createOpen.value = false
    createForm.name = ''; createForm.description = ''; createForm.image_prompt = ''; createImage.value = null
    kind.value = 'characters'
    refresh()
  } catch (e) { ElMessage.error(e.message) } finally { createBusy.value = false }
}

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

    <!-- 角色运营:抽卡 / 自定义新建 -->
    <div v-if="kind === 'characters'" class="panel p-4 mb-4 flex items-center gap-4 flex-wrap">
      <div class="flex items-center gap-3 flex-1 min-w-[300px]">
        <div class="stage-ico active" style="width:38px;height:38px"><el-icon :size="18"><MagicStick /></el-icon></div>
        <div>
          <div class="text-sm"><b>角色抽卡</b> <span class="timecode">GACHA</span></div>
          <div class="text-xs" style="color:var(--mx-text3)">随机题材 × 随机气质,LLM 原创角色 + Krea-2 定妆直接入库</div>
        </div>
        <div class="flex-1"></div>
        <el-button type="primary" :loading="gachaBusy" @click="gacha">
          <el-icon class="mr-1"><Trophy /></el-icon>{{ gachaBusy ? '抽卡中(约 1 分钟)…' : '抽一张' }}
        </el-button>
      </div>
      <el-divider direction="vertical" style="height:34px" />
      <el-button plain @click="createOpen = true">
        <el-icon class="mr-1"><Plus /></el-icon>自定义新建(上传图 / 提示词生成)
      </el-button>
      <div v-if="gachaResult" class="w-full flex items-center gap-2 text-xs p-2 rounded-lg"
           style="background:rgba(255,180,84,.06);border:1px solid rgba(255,180,84,.25)">
        <img :src="`/api/library/characters/${encodeURIComponent(gachaResult.entry.name)}/image`"
             style="width:34px;height:44px;object-fit:cover;border-radius:6px" />
        <div class="flex-1 min-w-0">
          <b style="color:var(--mx-amber-hi)">{{ gachaResult.entry.name }}</b>
          <span style="color:var(--mx-text3)"> · {{ gachaResult.theme }} · {{ gachaResult.trait }}</span>
          <div class="truncate" style="color:var(--mx-text2)">{{ gachaResult.entry.description }}</div>
        </div>
        <el-button text size="small" @click="gachaResult = null"><el-icon><Close /></el-icon></el-button>
      </div>
    </div>

    <div v-if="!list.length" class="panel p-10 text-center" style="color:var(--mx-text-2)">
      库还是空的 —— 在任意项目跑一次「资产定妆」(Krea-2 生成或上传),资产会自动出现在这里
    </div>

    <div class="grid gap-4" style="grid-template-columns:repeat(auto-fill,minmax(190px,1fr))">
      <div v-for="e in list" :key="e.kind + e.name" class="shot-card p-3 flex flex-col gap-2">
        <div class="cursor-zoom-in relative" style="aspect-ratio:3/4;background:var(--mx-bg2);border-radius:10px;overflow:hidden" @click="G.viewer={index:0,items:[{type:'image',src:`/api/library/${e.kind}/${encodeURIComponent(e.name)}/image`,title:e.name,meta:(e.source==='krea2'?'Krea-2':'上传')+` · 复用 ${e.used_count} 次`,kindLabel:'LIBRARY'}]}">
          <img :src="`/api/library/${e.kind}/${encodeURIComponent(e.name)}/image`"
               style="width:100%;height:100%;object-fit:cover" loading="lazy"
               @error="$event.target.style.display='none'"
               @load="$event.target.style.display=''" />
          <div class="absolute inset-0 flex items-center justify-center opacity-0 hover:opacity-100 transition-opacity" style="background:rgba(4,6,10,.4)">
            <el-icon :size="22" color="var(--mx-amber-hi)"><ZoomIn /></el-icon>
          </div>
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
  

    <!-- 自定义新建角色:上传图或提示词生成 -->
    <el-dialog v-model="createOpen" title="自定义新建角色" width="560px">
      <el-form label-position="top">
        <el-form-item label="角色名(同名自动覆盖更新)">
          <el-input v-model="createForm.name" maxlength="20" placeholder="如:林凡" />
        </el-form-item>
        <el-form-item label="外貌/服装/气质描述(中文,供跨项目一致性)">
          <el-input v-model="createForm.description" type="textarea" :rows="2" placeholder="20岁黑发少年,青色布衣,眼神明亮…" />
        </el-form-item>
        <el-form-item label="定妆照来源">
          <el-radio-group v-model="createForm.mode">
            <el-radio-button value="generate">提示词生成(Krea-2)</el-radio-button>
            <el-radio-button value="upload">上传参考图片</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="createForm.mode === 'generate'" label="生成提示词(英文,半身定妆照:面部+发型+服装+光线)">
          <el-input v-model="createForm.image_prompt" type="textarea" :rows="3"
            placeholder="A 20-year-old Chinese young man, short black hair, cyan hanfu robe, bright determined eyes, cinematic soft lighting, upper-body portrait" />
        </el-form-item>
        <el-form-item v-else label="参考图片(png / jpg / webp)">
          <div v-if="!createImage" class="upload-zone" @click="createInput?.click()" @dragover.prevent @drop.prevent="createImage = $event.dataTransfer?.files?.[0] || null">
            <el-icon :size="24" color="var(--mx-text3)"><UploadFilled /></el-icon>
            <div class="text-xs" style="color:var(--mx-text2)">点击或拖入图片</div>
          </div>
          <div v-else class="flex items-center gap-3 p-2 rounded-lg" style="border:1px solid var(--mx-line-hi)">
            <img :src="URL.createObjectURL(createImage)" style="width:46px;height:58px;object-fit:cover;border-radius:6px" />
            <span class="text-xs flex-1 truncate">{{ createImage.name }}</span>
            <el-button text size="small" @click="createImage = null"><el-icon><Close /></el-icon></el-button>
          </div>
          <input ref="createInput" type="file" accept="image/png,image/jpeg,image/webp" hidden @change="onPickImg" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createOpen = false">取消</el-button>
        <el-button type="primary" :loading="createBusy" @click="doCreate">
          {{ createForm.mode === 'upload' ? '上传入库' : '生成并入库(约 1 分钟)' }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>
