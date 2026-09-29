<script setup>
import { G, api, fileToB64, sourceLabel, sourceTone} from '../store'
import LibAssetDialog from '../components/LibAssetDialog.vue'
import { onMounted, ref, reactive, computed, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { confirmGpuContention } from '../pipelineBusy'
import { groupCharFamilies } from '../composables/useCharGroups'

// 分类**由后端决定**，前端不再写死清单。
//
// 【为什么改成这样】（2026-09-28）此前这里是 `ref({characters:[],scenes:[],props:[]})` 加一句
// 注释"与后端 library.Kinds 一致" —— 而后端新增 music（配乐进资产库）之后，那句注释**就是假的**：
// 后端按 Kinds 遍历、接口照常返回 music 分组，而前端连分类都不认识 ⇒ 用户根本看不到自己的配乐。
// 同类缺口当天还发现过一次（配乐参数界面上填不了）。修法不是"再同步一次清单"，而是**让它不需要同步**：
// 分类取接口返回的键，缺的只在 kindLabel 里补中文名（漏了也只是显示成键名，不会消失）。
const lib = ref({})
const kinds = computed(() => Object.keys(lib.value))
const kind = ref('characters')
const loading = ref(false)

// kindLabel 分类中文名(卡片、弹窗、提示语共用一处)。**新分类必须在这里补一行** ——
// 不补会显示成键名(music)，难看但不会像以前那样整类消失。
const kindLabel = { characters: '角色', scenes: '场景', props: '道具', music: '配乐' }
const kindWord = computed(() => kindLabel[kind.value] || kind.value)
const isMusic = computed(() => kind.value === 'music')

async function refresh() {
  loading.value = true
  try {
    const d = await api.library()
    // 直接吃后端的分组（缺哪一类都无所谓：没返回就是没有），不再逐个列举
    const out = {}
    for (const k of Object.keys(d || {})) {
      if (Array.isArray(d[k])) out[k] = d[k]
    }
    lib.value = out
    // 当前选中的分类在新数据里不存在（例如清空后）⇒ 落回第一个可用分类
    if (!out[kind.value] && kinds.value.length) kind.value = kinds.value[0]
  } catch (e) { ElMessage.error('资产库读取失败:' + e.message) }
  loading.value = false
}
onMounted(refresh)
watch(() => G.tick.library, refresh)

const list = computed(() => lib.value[kind.value] || [])

// ---------- 造型档归人(技能侧清单 §4.2) ----------
//
// 技能侧把"造型随剧情变"落成**一角色多张卡**:「翻篇·收容所」「翻篇·归零」…
// 库里于是会同时躺着同一个人的好几档定妆照。按卡平铺显示,用户看到的就是
// "怎么多出来好几个新角色";而这些卡本来就是同一个人换了一身衣服。
//
// 基名(family)由后端现算下发(条目名里「·」之前的部分)——前端不自己拆名字:
// 拆法只有一份实现才可能与产线一致(与 char_kinds 同一条教训)。
// 单档的角色(绝大多数)不插分组头,观感与原先完全一致。
// 分组算法见 `composables/useCharGroups.js`（**全前端唯一实现**）。
//
// 这一份此前**已经漂移**：solo 键写成 `'solo:' + e.kind + e.name`（用名字），
// 而另外两处用 `'solo:' + id` —— 同名不同人（合法数据）在这页会被并成一组，
// 界面显示成"一个人有两个造型档"，而渲染按两个人各出一张图。
const libGroups = computed(() => {
  if (kind.value !== 'characters') {
    // 非角色类（场景/道具）没有家族概念，按条目各自成组（保持原有观感）
    return (list.value || []).map(e => ({ key: 'solo:' + e.kind + e.name, base: e.name, items: [e] }))
  }
  // 角色：走共用实现。库里条目带的家族信息在 `e.family`（后端下发）
  const items = (list.value || []).map(e => ({ ...e, id: e.id || (e.kind + ':' + e.name) }))
  const fams = {}
  for (const e of list.value || []) {
    if (e && e.family) fams[e.id || (e.kind + ':' + e.name)] = { family: e.family, base: e.family }
  }
  return groupCharFamilies(items, fams)
})

// libCards 给模板的扁平列表:多档的人前面插一行分组头。
// 直接铺平条目对象(而不是包一层 {e: …}),模板里的取属性层级才与原先一致。
const libCards = computed(() => {
  const out = []
  for (const g of libGroups.value) {
    if (g.items.length > 1) {
      out.push({ _head: true, _key: 'head:' + g.key, _base: g.base, _count: g.items.length })
    }
    for (const e of g.items) out.push({ ...e, _head: false, _key: e.kind + e.name })
  }
  return out
})

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
const createForm = reactive({ name: '', description: '', image_prompt: '', mode: 'generate', whiten: true })
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
// openCreate 打开创建弹窗,并按当前分类选好默认来源:
// 角色可以"提示词生成",道具/场景只有上传一条路(代生成用的是角色定妆照那套取景,
// 拿来画道具会跑偏),所以默认落在上传模式,免得用户填完提示词才发现生成不了。
function openCreate() {
  createForm.mode = kind.value === 'characters' ? 'generate' : 'upload'
  createOpen.value = true
}
async function doCreate() {
  if (!createForm.name.trim()) { ElMessage.warning(`请填写${kindWord.value}名`); return }
  createBusy.value = true
  try {
    // JSON + base64/路径双通道:桌面 webview 的 wails 代理会截断 multipart
    // 大文件(2026-09-09「源图不存在」根因),浏览器模式同样可用
    const k = kind.value
    const body = { name: createForm.name.trim(), description: createForm.description }
    if (createForm.mode === 'upload') {
      // 白底化默认开(用户 2026-09-24 定调:上传图也走 Qwen-Image-Edit 生成白底角色);
      // 非角色分类后端不看这个字段(道具/场景的主图不当锁脸锚点,没必要付编辑开销)
      body.whiten = createForm.whiten
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
    const d = await api.libraryCreateAsset(k, body)
    // 白底化没做成时后端会如实回 note(降级为原图入库)—— 必须说出来,不能假装成功
    const done = `${kindLabel[k] || '资产'}「${d.entry.name}」已入库`
    if (d.note) ElMessage.warning(`${done}:${d.note}`)
    else ElMessage.success(done)
    createOpen.value = false
    createForm.name = ''; createForm.description = ''; createForm.image_prompt = ''; createImage.value = null; createNativePath.value = null
    kind.value = k
    refresh()
  } catch (e) { ElMessage.error(e.message) } finally { createBusy.value = false }
}

// ---------- 资产详情弹窗 ----------
// 展示该角色的**全部图片**(定妆照 + 多视角原图 + 转台图)、中文描述与英文提示词,
// 并提供管理操作。图片清单由后端 /api/library/{kind}/{name} 给出。
// 详情弹窗现在由**共用组件** LibAssetDialog.vue 承载（资产库与工作台同一份）——
// 这里只留"打开哪一条"这三个字段，以及它改完之后刷新列表。
const detailState = ref(null) // {kind, key, name} | null
const detailOpen = computed({
  get: () => !!detailState.value,
  set: (v) => { if (!v) detailState.value = null },
})
const detailKind = computed(() => detailState.value?.kind || 'characters')
const detailKey = computed(() => detailState.value?.key || '')
const detailName = computed(() => detailState.value?.name || '')

async function openDetail(e) {
  if (batch.value) return // 批量选择模式下,点击卡片是勾选而不是看详情
  detailState.value = { kind: e.kind, key: libKey(e), name: e.name }
}

// hasAngleRef 该条目有没有**角度参考**（渲染时作第二张参考的那张图）。
//
// 2026-09-27 起判据有三种产物（用户：「三视图不就是多视角的吗，相关"单视角"状态也要同步调整显示」）：
//   `_board`      三视图细节板 —— **现在的默认角度参考**（正/侧/背三个全身 + 6 个头部细节，16:9·2K）
//   `_turnaround` 转台图 —— 显式开了多视角的老项目
//   `_view_*`     逐张角度图（背面/侧面/四分之三）—— 更老的那条路
// 只认后两种会让"已经有板子"的角色被标成「只有单张正面照」—— 状态与事实相反，
// 用户会去点「补全多视角」，白烧一次 H3 转身。
// （详情弹窗里同一判据在 LibAssetDialog.vue —— 那两处必须是同一套。）
function hasAngleRef(images) {
  return (images || []).some((f) => /_board|_view_|_turnaround/.test(String(f)))
}

// libKey 拼库内地址时用**卡片 id**（审计 B13）。
//
// 为什么不用名字：同名不同人是合法数据（两个孩子都叫"老张"），而按名字访问
// **只能到第一条** —— 第二条在界面上看不到自己的图，点删除还会落到第一条身上。
// 后端 `Match` 已支持 id（只认 `char_`/`prop_`/`scene_` 前缀，中文名不会误判），
// 老条目没有 id 时退回名字，行为与从前一致。
function libKey(e) {
  return e && e.id ? e.id : e.name
}

// kindWordOf 分类的中文短名（封面角标用：角色/场景/道具/配乐）。
// 注意与页面上那个 `kindWord`（当前标签页的名字）区分 —— 同名会撞声明。
const KIND_WORD = { characters: '角色', scenes: '场景', props: '道具', music: '配乐' }
function kindWordOf(k) {
  return KIND_WORD[String(k || '')] || String(k || '')
}

// entityKindLabel 条目**生效实体类型**的中文短标签（人/器物/灵兽/无实体/Q 版）。
//
// 为什么要有它（2026-09-29 用户问「归墟环·维护舱主控语音」是角色？）：`characters` 这个
// 分类按**存储口径**收"一切有个体的东西"，里面混着非人 —— 不标出来，用户会以为那是一张
// 人脸卡（而它可能是舱顶一块扬声器圆盘）。判据取**平台判定的生效类型**（落在条目
// `entity_kind` 里的那一个），不是从名字里猜的。
//
// **人（human）不标** —— 那是绝大多数，标出来只是噪音；空值也不标（老条目没判过）。
const ENTITY_KIND_LABEL = {
  android: '人形机械', object: '器物', spirit_pet: '灵兽', beast: '妖兽', incorporeal: '无实体', chibi: 'Q 版',
}
const ENTITY_KIND_HINT = {
  android: '人形机械：有人形轮廓但不是人。定妆照取人形（半身正脸），身份锁锁在机械结构上（装甲分缝/关节/发光标记/材质/机身刻印），不锁人类肤色与年龄感',
  object: '这不是人物卡，是一件器物（定妆照按"整件产品图"取景，不做人脸身份锁）',
  spirit_pet: '这不是人物卡，是一只灵宠（按宠物取景，不做人脸身份锁）',
  beast: '这不是人物卡，是一只妖兽（按兽形取景，不做人脸身份锁）',
  incorporeal: '这不是人物卡：无实体（只有灯/光/声这类形态，画面里没有脸）',
  chibi: 'Q 版形象（内心活动镜头的演出素材，不是本人主卡）',
}
function entityKindLabel(e) {
  return ENTITY_KIND_LABEL[String(e?.entity_kind || '').trim()] || ''
}
function entityKindHint(e) {
  return ENTITY_KIND_HINT[String(e?.entity_kind || '').trim()] || ''
}

// cardHasViews 卡片上的多视角标记(与详情弹窗同一判据),让用户不用点开就知道
// 哪些角色还只有一张正面照。
function cardHasViews(e) {
  if (e.kind !== 'characters') return true
  return hasAngleRef(e.images) // 判据与详情弹窗同一处（见 hasAngleRef）
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
        角色/场景/道具一次入库,<b style="color:var(--mx-amber)">全项目复用</b>——新项目遇到同名资产自动引用(免生成、天然形象一致)。
        本地模型生成与手动上传的资产都会自动入库;复用计数排序。道具(器物/机关/法宝)在上方切到「道具」分类管理。
      </p>
    </div>
<!-- 角色运营:抽卡 / 自定义新建 -->
    <div v-if="kind === 'characters'" class="panel p-4 mb-4 flex items-center gap-4 flex-wrap">
      <div class="flex items-center gap-3 flex-1 min-w-[300px]">
        <div class="stage-ico active" style="width:38px;height:38px"><el-icon :size="18"><MagicStick /></el-icon></div>
        <div>
          <div class="text-sm"><b>角色抽卡</b> <span class="timecode">GACHA</span></div>
          <div class="text-xs" style="color:var(--mx-text3)">随机题材 × 随机气质,LLM 原创角色 + 本地模型定妆直接入库</div>
        </div>
        <div class="flex-1"></div>
        <el-tooltip content="随机题材×随机气质,AI 原创角色并本地模型定妆直接入库(约 1 分钟)" placement="top">
          <el-button type="primary" :loading="gachaBusy" @click="gacha">
            <el-icon class="mr-1"><Trophy /></el-icon>{{ gachaBusy ? '抽卡中(约 1 分钟)…' : '抽一张' }}
          </el-button>
        </el-tooltip>
      </div>
      <el-divider direction="vertical" style="height:34px" />
      <el-tooltip content="手动建角色:上传现成图片或填提示词本地模型生成后入库" placement="top">
        <el-button plain @click="openCreate">
          <el-icon class="mr-1"><Plus /></el-icon>自定义新建(上传图 / 提示词生成)
        </el-button>
      </el-tooltip>
      <div v-if="gachaResult" class="w-full flex items-center gap-2 text-xs p-2 rounded-lg"
           style="background:color-mix(in srgb, var(--mx-amber) 6%, transparent);border:1px solid color-mix(in srgb, var(--mx-amber) 25%, transparent)">
        <img :src="`/api/library/characters/${encodeURIComponent(libKey(gachaResult.entry))}/image`"
             style="width:34px;height:44px;object-fit:cover;border-radius:6px" />
        <div class="flex-1 min-w-0">
          <b style="color:var(--mx-amber-hi)">{{ gachaResult.entry.name }}</b>
          <span style="color:var(--mx-text3)"> · {{ gachaResult.theme }} · {{ gachaResult.trait }}</span>
          <div class="truncate" style="color:var(--mx-text2)">{{ gachaResult.entry.description }}</div>
        </div>
        <el-button text size="small" @click="gachaResult = null"><el-icon><Close /></el-icon></el-button>
      </div>
    </div>

    <!-- 配乐(2026-09-28 用户要求 BGM 进资产库管理)：这里**没有"新建"** —— 曲子由产线生成
         （或在「参数 → 合成」里指定现成文件）。这一栏只说明它从哪来、以及**怎么复用**，
         免得用户在这一页找"上传配乐"找不到，以为功能坏了。 -->
    <div v-else-if="kind === 'music'" class="panel p-4 mb-4 flex items-center gap-4 flex-wrap">
      <div class="flex items-center gap-3 flex-1 min-w-[300px]">
        <div class="stage-ico active" style="width:38px;height:38px">
          <el-icon :size="18"><Microphone /></el-icon>
        </div>
        <div>
          <div class="text-sm"><b>配乐</b> <span class="timecode">BGM</span></div>
          <div class="text-xs" style="color:var(--mx-text3)">
            按「需求描述」入库：同一份需求（换个标点写法也算）下次直接复用，不会重复生成
          </div>
        </div>
      </div>
      <div class="text-xs" style="color:var(--mx-text3);max-width:420px">
        配乐在「参数 → 合成」里填需求描述，由产线生成后自动入库；也可以直接指定现成的曲子文件。
      </div>
    </div>

    <!-- 道具(用户 2026-09-26:「资产库 新增一个 道具 管理页面」):
         器物/机关/法宝这类静物。没有抽卡、没有多视角、不白底化 —— 那些都是为"角色要当
         锁脸参考"设计的;道具这边就一件事:传一张图进来、写清材质配色、按名复用。 -->
    <div v-else-if="kind === 'props'" class="panel p-4 mb-4 flex items-center gap-4 flex-wrap">
      <div class="flex items-center gap-3 flex-1 min-w-[300px]">
        <div class="stage-ico active" style="width:38px;height:38px"><el-icon :size="18"><Suitcase /></el-icon></div>
        <div>
          <div class="text-sm"><b>道具库</b> <span class="timecode">PROPS</span></div>
          <div class="text-xs" style="color:var(--mx-text3)">器物 / 机关 / 法宝:上传一张图入库,自动看图补中文描述与英文提示词,重抽也按这条提示词来</div>
        </div>
        <div class="flex-1"></div>
        <el-tooltip content="上传一张道具图入库(自动补齐中文描述与英文提示词)" placement="top">
          <el-button type="primary" @click="openCreate">
            <el-icon class="mr-1"><Plus /></el-icon>上传道具
          </el-button>
        </el-tooltip>
      </div>
    </div>

    <div class="flex items-center gap-3 mb-4">
      <!-- 分类按钮**由后端分组生成**（新增分类自动出现，前端不必再同步清单 ——
           2026-09-28 加 music 时就是因为这里写死，用户看不到自己的配乐） -->
      <el-radio-group v-model="kind">
        <el-radio-button v-for="k in kinds" :key="k" :value="k">
          {{ kindLabel[k] || k }}({{ lib[k]?.length || 0 }})
        </el-radio-button>
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
      <p class="ez-desc">在任意项目跑一次「资产定妆」(本地模型生成或手动上传),角色与场景会自动入库;之后新项目遇到同名资产即可一键复用。</p>
    </div>

    <div class="grid gap-5" style="grid-template-columns:repeat(auto-fill,minmax(190px,1fr))">
      <template v-for="row in libCards" :key="row._key">
        <!-- 同一个人的第 2..N 个造型档:插一行分组头,说清"这是一个人、按场切换" -->
        <div v-if="row._head"
             style="grid-column:1/-1;display:flex;align-items:center;gap:8px;font-size:12px;color:var(--mx-text2);padding:2px 0 0">
          <el-icon><User /></el-icon>
          <b>「{{ row._base }}」</b>
          <span>{{ row._count }} 个造型档 —— 同一个人按场切换的服装/状态(声线与身份继承主卡),不是 {{ row._count }} 个新角色</span>
        </div>
        <div v-else class="shot-card p-3 flex flex-col gap-2 relative"
             :class="{ 'sel-on': sel.has(row.name) }" @click="batch && toggleSel(row.name)">
          <div v-if="batch" class="absolute top-2 left-2 z-10 bg-black/40 rounded px-1" @click.stop="toggleSel(row.name)">
            <el-checkbox :model-value="sel.has(row.name)" size="large" />
          </div>
          <!-- 配乐条目用**播放器**，不是图：<img> 指向一个 mp3 只会一片空白 -->
          <div v-if="row.kind === 'music'" class="relative p-3 rounded-lg flex flex-col gap-2"
               style="background:var(--mx-bg2);border:1px solid color-mix(in srgb, var(--mx-amber) 18%, transparent)">
            <div class="text-xs" style="color:var(--mx-text3);min-height:3.2em;display:-webkit-box;-webkit-line-clamp:3;-webkit-box-orient:vertical;overflow:hidden">
              {{ row.description || '(无描述)' }}
            </div>
            <audio :src="`/api/library/${row.kind}/${encodeURIComponent(libKey(row))}/image`"
                   controls preload="none" style="width:100%;height:32px" />
            <button class="cover-del" title="删除该库资产" @click.stop="remove(row)">
              <el-icon :size="13"><Delete /></el-icon>
            </button>
          </div>
          <div v-else class="cursor-zoom-in relative" style="aspect-ratio:3/4;background:var(--mx-bg2);border-radius:10px;overflow:hidden" @click="openDetail(row)">
            <img :src="`/api/library/${row.kind}/${encodeURIComponent(libKey(row))}/image`"
                 style="width:100%;height:100%;object-fit:cover" loading="lazy"
                 @error="$event.target.style.display='none'"
                 @load="$event.target.style.display=''" />
            <!-- 封面角标（2026-09-29 用户：「模型名称和分类名称可以显示到封面上，不用和人物名称挤一块」）：
                 分类在左上，模型/来源在左下 —— 名字那一行只留名字与状态标签。 -->
            <span class="cov-badge tl">{{ kindWordOf(row.kind) }}</span>
            <span class="cov-badge bl">{{ sourceLabel(row.source) }}</span>
            <div class="absolute inset-0 flex items-center justify-center opacity-0 hover:opacity-100 transition-opacity" style="background:color-mix(in srgb, var(--mx-bg0) 40%, transparent)">
              <el-icon :size="22" color="var(--mx-amber-hi)"><ZoomIn /></el-icon>
            </div>
            <button class="cover-del" title="删除该库资产" @click.stop="remove(row)">
              <el-icon :size="13"><Delete /></el-icon>
            </button>
          </div>
          <div class="flex items-center gap-2">
            <b class="text-sm truncate flex-1">{{ row.name }}</b>
            <!-- 实体类型的**诚实标签**（2026-09-29 用户问「归墟环·维护舱主控语音」是角色？）：
                 库里 characters 这个分类按存储口径收"一切有个体的东西"，其中有些**不是人**
                 （无实体/器物/灵兽）。不标出来，用户会以为那是一张人脸卡 ——
                 标签取的是**平台判定的生效类型**（落库在条目里），不是名字里猜的。 -->
            <el-tooltip v-if="row.kind === 'characters' && entityKindLabel(row)"
                        :content="entityKindHint(row)" placement="top">
              <el-tag size="small" type="info" effect="plain">{{ entityKindLabel(row) }}</el-tag>
            </el-tooltip>
            <el-tooltip v-if="row.kind === 'characters' && !cardHasViews(row)"
                        content="只有单张正面照,点开可「补全多视角」(渲染时该角色会多一张角度参考)" placement="top">
              <el-tag size="small" type="warning" effect="plain">单视角</el-tag>
            </el-tooltip>
          </div>
          <div class="text-xs" style="color:var(--mx-text2)">
            ♻ 复用 {{ row.used_count }} 次 · 来自「{{ row.source_project || '-' }}」
          </div>
        </div>
      </template>
    </div>
  

    <!-- 自定义新建库资产:角色可上传或提示词生成;道具/场景只有上传一条路
         (代生成用的是"半身定妆照"取景模板,画道具会跑偏 —— 后端也据此拒绝) -->
    <el-dialog v-model="createOpen" :title="`自定义新建${kindWord}`" width="560px">
      <el-form label-position="top">
        <el-form-item :label="`${kindWord}名(同名自动覆盖更新)`">
          <el-input v-model="createForm.name" maxlength="20"
                    :placeholder="kind === 'props' ? '如:旧铜镜 / 木匣' : (kind === 'scenes' ? '如:河神庙' : '如:林凡')" />
        </el-form-item>
        <el-form-item :label="kind === 'props' ? '外观/材质/配色描述(中文,供跨项目一致性)' : '外貌/服装/气质描述(中文,供跨项目一致性)'">
          <el-input v-model="createForm.description" type="textarea" :rows="2"
                    :placeholder="kind === 'props'
                      ? '旧铜镜,镜背浮雕缠枝纹,边缘磕碰、绿锈斑驳…'
                      : '20岁黑发少年,青色布衣,眼神明亮…'" />
        </el-form-item>
        <el-form-item :label="kind === 'props' ? '道具图来源' : '定妆照来源'">
          <el-radio-group v-model="createForm.mode">
            <el-radio-button v-if="kind === 'characters'" value="generate">提示词生成</el-radio-button>
            <el-radio-button value="upload">上传参考图片</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="createForm.mode === 'generate'" label="生成提示词(英文,半身定妆照:面部+发型+服装+光线)">
          <el-input v-model="createForm.image_prompt" type="textarea" :rows="3"
            placeholder="A 20-year-old Chinese young man, short black hair, cyan hanfu robe, bright determined eyes, cinematic soft lighting, upper-body portrait" />
        </el-form-item>
        <!-- 白底化:上传图常带花背景,而这张主图会被当参考图喂进渲染(花背景会把
             布景/光线一起带进成片)。默认勾选 —— 走 Qwen-Image-Edit-2511 只换背景、
             不动主体;编辑链路没就绪时后端会退回原图并在提示里说明原因。 -->
        <el-form-item v-if="createForm.mode === 'upload' && kind === 'characters'">
          <el-switch v-model="createForm.whiten" />
          <span class="text-xs ml-2" style="color:var(--mx-text2)">白底化(用 Qwen-Image-Edit 只换背景,不改主体)</span>
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            换成纯白背景便于当参考图;关闭则原图直接入库。没做成时会让原图入库并说明原因。
          </div>
        </el-form-item>
        <!-- 上传区:上传模式才显示。
             ⚠ 这里原先是 `<el-form-item v-else …>`(配在上面的白底化 v-if 上)—— v-if/v-else
             是**上传模式显示白底化、生成模式反而显示上传区**,上传模式下压根看不到上传控件
             (2026-09-26 新增道具分类时发现:道具没有"生成"这条路,一眼就露馅)。改成按
             `mode === 'upload'` 独立判断。 -->
        <el-form-item v-if="createForm.mode === 'upload'" label="参考图片(png / jpg / webp)">
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

    <!-- 资产详情：**与工作台共用同一个组件**（LibAssetDialog.vue）——
         此前资产库与工作台各写一份，样式与行为两套，用户一眼看出"没适配"。 -->
    <LibAssetDialog v-model:open="detailOpen" :kind="detailKind" :lib-key="detailKey"
                    :name="detailName" @changed="refresh" />
  </div>
</template>
