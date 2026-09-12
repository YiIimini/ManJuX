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
    const d = await api.library()
    lib.value = d.characters || d.scenes ? d : { characters: [], scenes: [] }
  } catch (e) { ElMessage.error('资产库读取失败:' + e.message) }
  loading.value = false
}
onMounted(refresh)
watch(() => G.tick.library, refresh)

const list = computed(() => lib.value[kind.value] || [])

// ---------- 资产详情弹窗 ----------
// 展示该角色的**全部图片**(定妆照 + 多视角原图 + 转台图)、中文描述与英文提示词,
// 并提供管理操作。图片清单由后端 /api/library/{kind}/{name} 给出。
const detail = ref(null)
const detailBusy = ref(false)
const detailOpen = computed({
  get: () => !!detail.value,
  set: (v) => { if (!v) detail.value = null },
})

// 描述/提示词在弹窗里可编辑:AI 生成或手改后点「保存」落库(data/asset.json)。
const editDesc = ref('')
const editPrompt = ref('')
const metaBusy = ref(false)
const descBusy = ref(false)

async function reloadDetail(k, n) {
  try {
    const d = await api.libraryDetail(k, n)
    detail.value = { ...d, kind: k }
    editDesc.value = detail.value.description || ''
    editPrompt.value = detail.value.image_prompt || ''
  } catch (e) { ElMessage.error('读取资产详情失败:' + e.message) }
}

async function openDetail(e) {
  if (batch.value) return // 批量选择模式下,点击卡片是勾选而不是看详情
  detailBusy.value = true
  detail.value = { ...e } // 先开弹窗(卡片上的字段),再补详情,点击无空窗
  editDesc.value = e.description || ''
  editPrompt.value = e.image_prompt || ''
  try {
    await reloadDetail(e.kind, e.name)
  } catch (err) {
    ElMessage.error('读取资产详情失败:' + err.message)
  }
  detailBusy.value = false
}

// hasViews 该角色是否已有多视角参考图(多视角原图或转台图)。
// 没有 = 用这个角色渲染时只有一张正面照 —— 详情弹窗要给补全入口。
const hasViews = computed(() => {
  const d = detail.value
  if (!d) return false
  return (d.images || []).some((f) => /_view_|_turnaround/.test(String(f)))
})

// 补全多视角:一次 H3 转身渲染(数分钟),后端做完才返回,期间按钮保持 loading,
// 不能让用户以为"点了没反应"。
const viewsBusy = ref(false)
async function doViews() {
  const d = detail.value
  if (!d) return
  viewsBusy.value = true
  try {
    const j = await api.libraryViews(d.kind, d.name)
    if (j && j.skipped) ElMessage.info(j.message || '已有多视角图')
    else ElMessage.success('多视角已就绪:渲染将自动把它作为该角色的第二张参考')
    await reloadDetail(d.kind, d.name)
    refresh()
  } catch (e) {
    ElMessage.error('补全多视角失败:' + e.message)
  }
  viewsBusy.value = false
}

// AI 生成中文描述与英文提示词:配了视觉模型就让它读这张定妆照(描述的是图里的
// 角色本身),否则退回文本模型；结果直接落库并回填到输入框,可再手动改。
async function doDescribe() {
  const d = detail.value
  if (!d) return
  descBusy.value = true
  try {
    const j = await api.libraryDescribe(d.kind, d.name)
    const e = j.entry || j
    editDesc.value = e.description || ''
    editPrompt.value = e.image_prompt || ''
    detail.value = { ...detail.value, ...e }
    ElMessage.success(j.via === 'vlm' ? '已按图片生成描述与提示词(视觉模型)' : '已生成描述与提示词(文本模型)')
    refresh()
  } catch (e) {
    ElMessage.error('生成失败:' + e.message)
  }
  descBusy.value = false
}

async function saveMeta() {
  const d = detail.value
  if (!d) return
  metaBusy.value = true
  try {
    const e = await api.librarySetMeta(d.kind, d.name, {
      description: editDesc.value, image_prompt: editPrompt.value,
    })
    detail.value = { ...d, ...e }
    ElMessage.success('描述与提示词已保存')
    refresh()
  } catch (err) {
    ElMessage.error('保存失败:' + err.message)
  }
  metaBusy.value = false
}

// imgURL 拼库内图片地址;file 为空取主图,否则取指定图(只传基名,后端会校验)
function imgURL(e, file) {
  const base = `/api/library/${e.kind}/${encodeURIComponent(e.name)}/image`
  if (!file) return base
  return `${base}?file=${encodeURIComponent(String(file).split('/').pop())}`
}

// cardHasViews 卡片上的多视角标记(与详情弹窗同一判据),让用户不用点开就知道
// 哪些角色还只有一张正面照。
function cardHasViews(e) {
  if (e.kind !== 'characters') return true
  return (e.images || []).some((f) => /_view_|_turnaround/.test(String(f)))
}

// labelOf 按文件名推断图片用途,便于一眼看出哪张是定妆照、哪张是转台图
function labelOf(file) {
  const b = String(file).split('/').pop() || ''
  if (b.includes('_turnaround')) return '转台图'
  if (b.includes('_view_threequarter')) return '四分之三'
  if (b.includes('_view_side')) return '侧面'
  if (b.includes('_view_back')) return '背面'
  if (b.includes('_view_')) return '多视角'
  return '定妆照'
}

// 重抽(抽卡):同一角色换一张(提示词与描述不变,只换随机种子)。与页面顶部
// 「抽一张」的区别是:抽卡抽的是全新角色,重抽是对当前这个不满意再摇一次。
// 没有提示词的条目后端会先用 LLM 依描述推导一条,不再直接拒绝。
const regenBusy = ref(false)
async function regenDetail() {
  const d = detail.value
  if (!d) return
  regenBusy.value = true
  try {
    const j = await api.libraryRegen(d.kind, d.name)
    detail.value = { ...d, ...j, kind: d.kind }
    editPrompt.value = detail.value.image_prompt || ''
    editDesc.value = detail.value.description || ''
    ElMessage.success('已抽到新的一张(旧的多视角图已清除,可点「补全多视角」重建)')
    refresh()
  } catch (e) {
    ElMessage.error('抽卡失败:' + e.message)
  }
  regenBusy.value = false
}

// detailImages 详情弹窗要展示的图片清单(老条目没有 images 时退回主图)
const detailImages = computed(() => {
  const d = detail.value
  if (!d) return []
  return (d.images && d.images.length) ? d.images : (d.image ? [d.image] : [])
})

function previewDetail(i) {
  const d = detail.value
  if (!d) return
  G.viewer = {
    index: i,
    items: detailImages.value.map((f) => ({
      type: 'image', src: imgURL(d, f),
      title: `${d.name} · ${labelOf(f)}`,
      meta: labelOf(f), kindLabel: 'LIBRARY',
    })),
  }
}

async function removeDetail() {
  const d = detail.value
  if (!d) return
  try {
    await ElMessageBox.confirm(`删除库资产「${d.name}」?引用它的项目不受影响,只是后续不再复用。`,
      '确认删除', { type: 'warning' })
  } catch { return }
  await remove(d)
  detail.value = null
}

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
      await api.libraryDelete(kind.value, n)
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
    const d = await api.libraryGacha()
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
    const d = await api.libraryCreateCharacter(body)
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
    await api.libraryDelete(kind.value, e.name)
    ElMessage.success('已删除')
    refresh()
  } catch (err) { ElMessage.error(err.message) }
}
</script>

<template>
  <div class="h-full overflow-y-auto p-6">
    <div class="panel p-5 mb-5 relative overflow-hidden">
      <h1 class="text-lg font-bold m-0 mb-1">资产库 <span class="timecode">ASSET LIBRARY</span></h1>
      <p class="text-sm m-0" style="color:var(--mx-text2)">
        角色/场景一次定妆,<b style="color:var(--mx-amber)">全项目复用</b>——新项目遇到同名资产自动引用(免生成、天然形象一致)。
        Krea-2 生成与手动上传的资产都会自动入库;复用计数排序。
      </p>
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
        <div class="cursor-zoom-in relative" style="aspect-ratio:3/4;background:var(--mx-bg2);border-radius:10px;overflow:hidden" @click="openDetail(e)">
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
          <el-tooltip v-if="e.kind === 'characters' && !cardHasViews(e)"
                      content="只有单张正面照,点开可「补全多视角」(渲染时该角色会多一张角度参考)" placement="top">
            <el-tag size="small" type="warning" effect="plain">单视角</el-tag>
          </el-tooltip>
          <el-tag size="small" effect="plain" :type="e.source === 'krea2' ? 'warning' : 'info'">
            {{ e.source === 'krea2' ? 'Krea-2' : e.source === 'gacha' ? '抽卡' : '上传' }}
          </el-tag>
        </div>
        <div class="text-xs" style="color:var(--mx-text2)">
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

    <!-- 资产详情:全部图片(定妆照/多视角/转台图) + 中文描述 + 英文提示词 + 抽卡/补全 -->
    <el-dialog v-model="detailOpen" width="820px" append-to-body :title="detail ? `资产详情 · ${detail.name}` : '资产详情'">
      <div v-if="detail" class="flex flex-col gap-4">
        <div>
          <div class="flex items-center gap-2 mb-2 flex-wrap">
            <span class="text-xs" style="color:var(--mx-text3)">
              全部图片({{ detailImages.length }} 张:定妆照 + 多视角/转台图)· 点击可放大
            </span>
            <div class="flex-1"></div>
            <template v-if="detail.kind === 'characters'">
              <el-tag v-if="hasViews" size="small" type="success" effect="plain">
                含多视角参考(渲染时作第二张参考)
              </el-tag>
              <template v-else>
                <span class="text-xs" style="color:var(--mx-amber)">
                  只有单张正面照 —— 渲染时该角色拿不到多视角参考
                </span>
                <el-tooltip content="H3 原地转身视频 → 抽帧转台图(约数分钟);完成后自动写回资产库,渲染直接调用" placement="top">
                  <el-button size="small" type="primary" plain :loading="viewsBusy" @click="doViews">
                    <el-icon class="mr-1"><MagicStick /></el-icon>
                    {{ viewsBusy ? '多视角生成中(约数分钟)…' : '补全多视角' }}
                  </el-button>
                </el-tooltip>
              </template>
            </template>
          </div>
          <div class="grid gap-2" style="grid-template-columns:repeat(auto-fill,minmax(112px,1fr))">
            <div v-for="(f, i) in detailImages" :key="f"
                 class="cursor-zoom-in relative rounded-lg overflow-hidden"
                 style="aspect-ratio:3/4;background:var(--mx-bg2);border:1px solid var(--mx-line)"
                 @click="previewDetail(i)">
              <img :src="imgURL(detail, f)" style="width:100%;height:100%;object-fit:cover" loading="lazy" />
              <span class="absolute text-xs px-1 py-0.5"
                    style="left:0;right:0;bottom:0;background:rgba(0,0,0,.5);color:#fff">{{ labelOf(f) }}</span>
            </div>
          </div>
        </div>

        <div>
          <div class="flex items-center gap-2 mb-1">
            <span class="text-xs" style="color:var(--mx-text3)">中文描述(跨项目一致性用)</span>
            <div class="flex-1"></div>
            <el-tooltip content="让 AI 依据这张定妆照生成中文描述与英文提示词(配了视觉模型就读图,否则用文本模型)" placement="top">
              <el-button size="small" text :loading="descBusy" @click="doDescribe">
                <el-icon class="mr-1"><MagicStick /></el-icon>{{ descBusy ? '生成中…' : 'AI 生成描述/提示词' }}
              </el-button>
            </el-tooltip>
          </div>
          <el-input v-model="editDesc" type="textarea" :rows="3" resize="none"
                    placeholder="如:20岁黑发少年,青色布衣,眼神明亮…(为空时点右上「AI 生成」)" />
        </div>

        <div>
          <div class="flex items-center gap-2 mb-1">
            <span class="text-xs" style="color:var(--mx-text3)">英文提示词(定妆照;抽卡与重生成都按它出图)</span>
          </div>
          <el-input v-model="editPrompt" type="textarea" :rows="4" resize="none"
                    placeholder="A 20-year-old Chinese young man, short black hair, cyan hanfu robe…(为空时抽卡会先用描述推导)" />
        </div>

        <div class="text-xs" style="color:var(--mx-text3)">
          来源:{{ detail.source === 'krea2' ? 'Krea-2 生成' : detail.source === 'gacha' ? '抽卡' : '上传' }} ·
          首次入库:{{ detail.source_project || '-' }} ·
          已被 {{ detail.used_count }} 个项目复用
          <span v-if="detail.aliases?.length">
            <br />同一角色的其它叫法(分集分镜换称呼也不会重出定妆照):{{ detail.aliases.join('、') }}
          </span>
        </div>
      </div>
      <template #footer>
        <el-button plain @click="detailOpen = false">关闭</el-button>
        <el-tooltip content="把上面编辑过的中文描述与英文提示词写入资产库" placement="top">
          <el-button :loading="metaBusy" @click="saveMeta">保存描述/提示词</el-button>
        </el-tooltip>
        <el-tooltip content="同一角色再摇一张定妆照(描述与提示词不变,只换随机种子)" placement="top">
          <el-button type="primary" :loading="regenBusy" @click="regenDetail">
            <el-icon class="mr-1"><Trophy /></el-icon>{{ regenBusy ? '抽卡中(约 1 分钟)…' : '抽卡(换一张定妆照)' }}
          </el-button>
        </el-tooltip>
        <el-button type="danger" plain @click="removeDetail">
          <el-icon class="mr-1"><Delete /></el-icon>删除该资产
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>
