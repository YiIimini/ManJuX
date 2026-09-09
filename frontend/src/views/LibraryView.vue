<script setup>
import { G, api, fileToB64 } from '../store'
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

// 批量删除
const batch = ref(false)
const sel = ref(new Set())
function toggleSel(name) {
  if (sel.value.has(name)) sel.value.delete(name)
  else sel.value.add(name)
  sel.value = new Set(sel.value)
}
const allSelected = computed(() => list.value.length > 0 && list.value.every(e => sel.value.has(e.name)))
function toggleAllSel() {
  sel.value = allSelected.value ? new Set() : new Set(list.value.map(e => e.name))
}
async function batchDelete() {
  if (!sel.value.size) return
  try {
    await ElMessageBox.confirm(`从资产库删除选中的 ${sel.value.size} 项?不影响已使用它们的项目,只影响后续复用。`, '批量删除库资产',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  let ok = 0, fail = 0
  for (const n of sel.value) {
    try {
      await fetch(`/api/library/${kind.value}/${encodeURIComponent(n)}`, { method: 'DELETE' })
      ok++
    } catch { fail++ }
  }
  sel.value = new Set()
  batch.value = false
  ElMessage.success(`已删除 ${ok} 项` + (fail ? `,${fail} 个失败` : ''))
  refresh()
}

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
const createImageURL = ref('')
watch(createImage, (f, oldF) => {
  // 模板里不能直接调 URL.createObjectURL:URL 不在 Vue 模板全局白名单,
  // 编译成 _ctx.URL → 渲染即抛错,缩略图从未显示过(2026-09-09 实证)
  if (oldF && createImageURL.value) { try { URL.revokeObjectURL(createImageURL.value) } catch {} }
  createImageURL.value = f ? URL.createObjectURL(f) : ''
})
const createNativePath = ref(null) // 桌面拖放(wails 原生)拿到的本机绝对路径
const createInput = ref(null)
function onPickImg(ev) {
  const f = ev.target.files?.[0]
  ev.target.value = ''
  if (!f) return
  if (!/^image\/(png|jpe?g|webp)/.test(f.type)) { ElMessage.error('仅支持 png/jpg/webp'); return }
  createImage.value = f
  createNativePath.value = null
}
// 桌面模式拖放:App.vue 把 wails file-drop 事件转发到带 --wails-drop-target 的元素
function onNativeDrop(ev) {
  const p = ev.detail?.[0]
  if (!p) return
  if (!/\.(png|jpe?g|webp)$/i.test(p)) { ElMessage.error('仅支持 png/jpg/webp'); return }
  createNativePath.value = p
  createImage.value = null
  createForm.mode = 'upload'
}
async function doCreate() {
  if (!createForm.name.trim()) { ElMessage.warning('请填写角色名'); return }
  createBusy.value = true
  try {
    // JSON + base64/路径双通道:桌面 webview 的 wails 代理会截断 multipart
    // 大文件(2026-09-09「源图不存在」根因),浏览器模式同样可用
    const body = { name: createForm.name.trim(), description: createForm.description }
    if (createForm.mode === 'upload') {
      if (createNativePath.value) {
        body.image_path = createNativePath.value
      } else if (createImage.value) {
        body.image_b64 = await fileToB64(createImage.value)
        body.filename = createImage.value.name
      } else {
        ElMessage.warning('请选择或拖入上传图片'); createBusy.value = false; return
      }
    } else {
      if (!createForm.image_prompt.trim()) { ElMessage.warning('请填写生成提示词'); createBusy.value = false; return }
      body.image_prompt = createForm.image_prompt.trim()
    }
    const r = await fetch('/api/library/characters', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    const d = await r.json()
    if (!r.ok) throw new Error(d.error)
    ElMessage.success(`角色「${d.entry.name}」已入库`)
    createOpen.value = false
    createForm.name = ''; createForm.description = ''; createForm.image_prompt = ''; createImage.value = null; createNativePath.value = null
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
      <template v-if="batch">
        <span class="text-xs mr-2" style="color:var(--mx-amber)">已选 {{ sel.size }} 项</span>
        <el-button size="small" plain @click="toggleAllSel">{{ allSelected ? '取消全选' : '全选' }}</el-button>
        <el-button size="small" type="danger" :disabled="!sel.size" @click="batchDelete">
          <el-icon class="mr-1"><Delete /></el-icon>删除选中
        </el-button>
        <el-button size="small" @click="batch = false; sel = new Set()">退出批量</el-button>
      </template>
      <el-button v-else size="small" plain :disabled="!list.length" @click="batch = true; sel = new Set()">
        <el-icon class="mr-1"><Operation /></el-icon>批量管理
      </el-button>
      <el-tooltip content="重新读取资产库(复用计数排序)" placement="bottom">
        <el-button size="small" @click="refresh" :loading="loading">刷新</el-button>
      </el-tooltip>
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
        <el-tooltip content="随机题材×随机气质,AI 原创角色并 Krea-2 定妆直接入库(约 1 分钟)" placement="top">
          <el-button type="primary" :loading="gachaBusy" @click="gacha">
            <el-icon class="mr-1"><Trophy /></el-icon>{{ gachaBusy ? '抽卡中(约 1 分钟)…' : '抽一张' }}
          </el-button>
        </el-tooltip>
      </div>
      <el-divider direction="vertical" style="height:34px" />
      <el-tooltip content="手动建角色:上传现成图片或填提示词 Krea-2 生成后入库" placement="top">
        <el-button plain @click="createOpen = true">
          <el-icon class="mr-1"><Plus /></el-icon>自定义新建(上传图 / 提示词生成)
        </el-button>
      </el-tooltip>
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

    <div v-if="!list.length" class="panel empty-zone">
      <div class="ez-ico"><el-icon :size="26"><Collection /></el-icon></div>
      <div class="ez-title">资产库还是空的</div>
      <p class="ez-desc">在任意项目跑一次「资产定妆」(Krea-2 生成或手动上传),角色与场景会自动入库;之后新项目遇到同名资产即可一键复用。</p>
    </div>

    <div class="grid gap-5" style="grid-template-columns:repeat(auto-fill,minmax(190px,1fr))">
      <div v-for="e in list" :key="e.kind + e.name" class="shot-card p-3 flex flex-col gap-2 relative"
           :class="{ 'sel-on': sel.has(e.name) }" @click="batch && toggleSel(e.name)">
        <div v-if="batch" class="absolute top-2 left-2 z-10 bg-black/40 rounded px-1" @click.stop="toggleSel(e.name)">
          <el-checkbox :model-value="sel.has(e.name)" size="large" />
        </div>
        <div class="cursor-zoom-in relative" style="aspect-ratio:3/4;background:var(--mx-bg2);border-radius:10px;overflow:hidden" @click="G.viewer={index:0,items:[{type:'image',src:`/api/library/${e.kind}/${encodeURIComponent(e.name)}/image`,title:e.name,meta:(e.source==='krea2'?'Krea-2':'上传')+` · 复用 ${e.used_count} 次`,kindLabel:'LIBRARY'}]}">
          <img :src="`/api/library/${e.kind}/${encodeURIComponent(e.name)}/image`"
               style="width:100%;height:100%;object-fit:cover" loading="lazy"
               @error="$event.target.style.display='none'"
               @load="$event.target.style.display=''" />
          <div class="absolute inset-0 flex items-center justify-center opacity-0 hover:opacity-100 transition-opacity" style="background:rgba(4,6,10,.4)">
            <el-icon :size="22" color="var(--mx-amber-hi)"><ZoomIn /></el-icon>
          </div>
          <button class="cover-del" title="删除该库资产" @click.stop="remove(e)">
            <el-icon :size="13"><Delete /></el-icon>
          </button>
        </div>
        <div class="flex items-center gap-2">
          <b class="text-sm truncate flex-1">{{ e.name }}</b>
          <el-tag size="small" effect="plain" :type="e.source === 'krea2' ? 'warning' : 'info'">
            {{ e.source === 'krea2' ? 'Krea-2' : '上传' }}
          </el-tag>
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
          <div v-if="!createImage && !createNativePath" class="upload-zone" style="--wails-drop-target: drop"
               @click="createInput?.click()" @dragover.prevent @drop.prevent="createImage = $event.dataTransfer?.files?.[0] || null"
               @mx-filedrop="onNativeDrop">
            <el-icon :size="24" color="var(--mx-text3)"><UploadFilled /></el-icon>
            <div class="text-xs" style="color:var(--mx-text2)">点击或拖入图片</div>
          </div>
          <div v-else class="flex items-center gap-3 p-2 rounded-lg" style="border:1px solid var(--mx-line-hi)">
            <img v-if="createImage" :src="createImageURL" style="width:46px;height:58px;object-fit:cover;border-radius:6px" />
            <img v-else-if="createNativePath" :src="'/api/thumb?path=' + encodeURIComponent(createNativePath)" style="width:46px;height:58px;object-fit:cover;border-radius:6px" />
            <span class="text-xs flex-1 truncate">{{ createNativePath || createImage.name }}</span>
            <el-button text size="small" @click="createImage = null; createNativePath = null"><el-icon><Close /></el-icon></el-button>
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
