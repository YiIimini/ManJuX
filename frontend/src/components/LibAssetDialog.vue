<script setup>
// 资产详情弹窗 —— **资产库与工作台共用同一个组件**（2026-09-29 用户：
// 「详细弹窗不用单独做啊（没有适配），直接资产库里的详情弹窗拿来用啊」）。
//
// 此前工作台那边我另写了一份"只读呈现"，结果是两套样式、两套行为，用户一眼看出没适配。
// 现在只有这一份：图片墙（定妆照 + 多视角/三视图板/转台图）+ 中文描述 + 英文提示词 +
// 运营操作（抽卡 / 补全多视角 / AI 生成描述 / 保存 / 删除）。
//
// 库里有这条 → 走库（详情、图片、操作全在）；库里没有（工作台里手动上传的资产、库被清过）→
// 退回项目内那张图 + 说明"这条不在资产库"，操作按钮置灰而不是点了报错。
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { G, api, sourceLabel } from '../store'
import { confirmGpuContention } from '../pipelineBusy'

const props = defineProps({
  open: { type: Boolean, default: false },
  kind: { type: String, default: 'characters' }, // characters / scenes / props
  libKey: { type: String, default: '' },          // 库内键：卡片 id 或名字
  name: { type: String, default: '' },
  // —— 库里没有这条时的兜底（工作台传项目内的那张图）——
  projectId: { type: String, default: '' },
  assetFile: { type: String, default: '' },
  description: { type: String, default: '' },
  imagePrompt: { type: String, default: '' },
  source: { type: String, default: '' },
})
const emit = defineEmits(['update:open', 'changed'])

const detail = ref(null)
const inLib = ref(true)
// resolvedKey 真正**查到了**这条库目的那个键（2026-09-29 用户报「工作台里场景与道具的详情弹窗
// 图片显示不正常」）。库内键有两种口径：角色的 id 是裸的（`char_qiqi`），
// 而场景/道具是带前缀的（`scene:scene_maintenance_bay` / `prop:prop_mingpai`）。
// 详情取数做了"换下一个键"的兜底，但图片地址此前**一直用最初那个键**拼 —— 于是场景/道具
// 明明查到了条目，图片 URL 还是 404（角色恰好是裸 id，所以只有场景与道具坏）。
// 现在统一用"查到的那把钥匙"，拼图与后续操作都跟它走。
const resolvedKey = ref('')
const editDesc = ref('')
const editPrompt = ref('')
const metaBusy = ref(false)
const descBusy = ref(false)
const viewsBusy = ref(false)
const regenBusy = ref(false)

const kindWord = computed(() => ({ characters: '角色', scenes: '场景', props: '道具' }[props.kind] || '资产'))
// hasAngleRef 该条目有没有**角度参考**（渲染时作第二张参考的那张图）。
// 判据与卡片上的"单视角"标记同一套（LibraryView 的 cardHasViews 调它自己的同名函数）——
// `_board`（三视图板，现在的默认角度参考）必须算进去。
function hasAngleRef(images) {
  return (images || []).some((f) => /_board|_view_|_turnaround/.test(String(f)))
}
const hasViews = computed(() => hasAngleRef(detail.value?.images))

// detailImages 要展示的图片清单：库条目给全部（定妆照 + 多视角 + 板子），
// 库里没有时退回项目内那张图。
const detailImages = computed(() => {
  const d = detail.value
  if (d?.images?.length) return d.images
  if (d?.image) return [d.image]
  if (!inLib.value && props.projectId && props.assetFile) return [props.assetFile]
  return []
})

function imgURL(file) {
  if (!inLib.value) {
    return props.projectId && props.assetFile ? api.fileURL(props.projectId, props.assetFile) : ''
  }
  const key = keyOf()
  const base = `/api/library/${encodeURIComponent(props.kind)}/${encodeURIComponent(key)}/image`
  if (!file) return base
  return `${base}?file=${encodeURIComponent(String(file).split('/').pop())}`
}
function keyOf() {
  // 优先用"查到的那把钥匙"；还没查到（或不在库里）时退回调用方给的键/名字。
  return String(resolvedKey.value || props.libKey || props.name || '').trim()
}
// keyCandidates 依次尝试的库内键：调用方给的键 → 带 kind 前缀的那一种 → 名字。
// 三种口径都试，是因为库条目的 id 口径按 kind 不同（见上面 resolvedKey 的注释）。
function keyCandidates() {
  const base = String(props.libKey || '').trim()
  const pref = { characters: 'char', scenes: 'scene', props: 'prop' }[props.kind] || ''
  const out = []
  for (const k of [base, pref && base && !base.includes(':') ? pref + ':' + base : '', String(props.name || '').trim()]) {
    if (k && !out.includes(k)) out.push(k)
  }
  return out
}
// labelOf 按文件名与 kind 推断用途（定妆照/角色板/转台图/场景图/道具图），一眼看出哪张是哪张。
//
// 【2026-10-10 用户「三视图更换为角色板显示更详细」】把「三视图板」改叫**角色板**
// （它本来就是 production reference sheet：正/侧/背三个全身 + 6 个头角度 + 6 个局部细节，
// 16:9·2K）；标签里连**内容**一起说清，不用再猜"三视图"到底画了什么。
function labelOf(file) {
  const b = String(file).split('/').pop() || ''
  if (b.includes('_turnaround')) return '转台图'
  if (b.includes('_board')) return '角色板（正/侧/背 + 头部细节）'
  if (b.includes('_view_threequarter')) return '四分之三'
  if (b.includes('_view_side')) return '侧面'
  if (b.includes('_view_back')) return '背面'
  if (b.includes('_view_')) return '多视角'
  if (props.kind === 'scenes') return '场景图'
  if (props.kind === 'props') return '道具图'
  return '定妆照'
}
const IMG_BOX_H = '190px'
// 角色板（原「三视图板」）是**一张图 15 格**：190px 高的缩略图看不清格子里的东西，
// 所以它（以及同型的转台图/场景板）占**整行**、并给更高的框 —— 用户要的「显示更详细」。
const BOARD_BOX_H = '260px'
function tileRatio(file) {
  const b = String(file).split('/').pop() || ''
  if (b.includes('_board') || b.includes('_turnaround')) return '16/9'
  if (props.kind === 'scenes') return '16/9' // 场景板是横幅，塞进 3/4 会被压成一条
  if (props.kind === 'props') return '1/1'   // 道具是白底方图
  return '3/4'
}
// wideTile 该不该占整行（横向大图：角色板/转台图/场景板）。
function wideTile(file) {
  return tileRatio(file) === '16/9'
}
async function load() {
  detail.value = null
  inLib.value = true
  resolvedKey.value = ''
  if (!props.open) return
  editDesc.value = props.description || ''
  editPrompt.value = props.imagePrompt || ''
  const candidates = keyCandidates()
  if (!candidates.length) {
    // 项目里手动传的资产、连名字都没有匹配键：直接按兜底展示
    inLib.value = false
    return
  }
  for (const key of candidates) {
    try {
      const d = await api.libraryDetail(props.kind, key)
      detail.value = { ...d, kind: props.kind }
      resolvedKey.value = key // ← 拼图与后续操作都跟这把钥匙走（场景/道具的 id 带前缀）
      inLib.value = true
      editDesc.value = d.description || editDesc.value
      editPrompt.value = d.image_prompt || editPrompt.value
      return
    } catch { /* 换下一个键 */ }
  }
  inLib.value = false // 库里没有这条 —— 只读展示项目内的图
}
watch(() => props.open, load)
watch(() => [props.kind, props.libKey, props.name], () => { if (props.open) load() })

function previewDetail(i) {
  G.viewer = {
    index: i,
    items: detailImages.value.map((f) => ({ type: 'image', src: imgURL(f), title: `${props.name} · ${labelOf(f)}` })),
  }
}
const cur = () => ({ kind: props.kind, name: keyOf() })
function refreshAll() { emit('changed') }

async function doViews() {
  const { kind, name } = cur()
  viewsBusy.value = true
  try {
    const j = await api.libraryViews(kind, name)
    if (j && j.skipped) ElMessage.info(j.message || '已有多视角图')
    else ElMessage.success('多视角已就绪:渲染将自动把它作为该角色的第二张参考')
    await load()
    refreshAll()
  } catch (e) { ElMessage.error('补全多视角失败:' + e.message) }
  viewsBusy.value = false
}

async function doDescribe() {
  const { kind, name } = cur()
  descBusy.value = true
  try {
    const j = await api.libraryDescribe(kind, name)
    const e = j.entry || j
    editDesc.value = e.description || ''
    editPrompt.value = e.image_prompt || ''
    detail.value = { ...detail.value, ...e, kind }
    ElMessage.success(j.via === 'vlm' ? '已按图片生成描述与提示词(视觉模型)' : '已生成描述与提示词(文本模型)')
    refreshAll()
  } catch (e) { ElMessage.error('生成失败:' + e.message) }
  descBusy.value = false
}

async function saveMeta() {
  const { kind, name } = cur()
  metaBusy.value = true
  try {
    const e = await api.librarySetMeta(kind, name, { description: editDesc.value, image_prompt: editPrompt.value })
    detail.value = { ...detail.value, ...e, kind }
    ElMessage.success('描述与提示词已保存')
    refreshAll()
  } catch (err) { ElMessage.error('保存失败:' + err.message) }
  metaBusy.value = false
}

async function regenDetail() {
  const { kind, name } = cur()
  if (!(await confirmGpuContention(`资产库 · 重新抽卡「${props.name}」`))) return
  regenBusy.value = true
  try {
    const j = await api.libraryRegen(kind, name)
    detail.value = { ...detail.value, ...j, kind }
    editPrompt.value = detail.value.image_prompt || ''
    editDesc.value = detail.value.description || ''
    ElMessage.success('已抽到新的一张(旧的多视角图已清除,可点「补全多视角」重建)')
    refreshAll()
  } catch (e) { ElMessage.error('抽卡失败:' + e.message) }
  regenBusy.value = false
}

async function removeDetail() {
  const { kind, name } = cur()
  try {
    await api.libraryDelete(kind, name)
    ElMessage.success('已从资产库删除')
    emit('update:open', false)
    refreshAll()
  } catch (e) { ElMessage.error('删除失败:' + e.message) }
}
</script>

<template>
  <el-dialog :model-value="open" width="820px" append-to-body
             :title="`资产详情 · ${name || kindWord}`" @update:model-value="emit('update:open', $event)">
    <div class="flex flex-col gap-4">
      <el-alert v-if="!inLib" type="info" :closable="false" show-icon
                title="这条资产不在资产库里（工作台手动上传 / 已从库中删除）—— 下面是项目内的那张图，库侧操作不可用" />
      <div>
        <div class="flex items-center gap-2 mb-2 flex-wrap">
          <span class="text-xs" style="color:var(--mx-text3)">
            全部图片({{ detailImages.length }} 张:定妆照 + 多视角/转台图)· 点击可放大
          </span>
          <div class="flex-1"></div>
          <template v-if="kind === 'characters' && inLib">
            <el-tag v-if="hasViews" size="small" type="success" effect="plain">
              含角度参考(角色板/转台图,渲染时作第二张参考)
            </el-tag>
            <template v-else>
              <span class="text-xs" style="color:var(--mx-amber)">
                缺角度参考 —— 只有正面照(渲染时侧面/背面只能靠模型自己编)
              </span>
              <el-tooltip content="可选:H3 原地转身视频 → 抽帧转台图(约数分钟)。默认不需要 —— 角度参考已由角色板接手" placement="top">
                <el-button size="small" type="primary" plain :loading="viewsBusy" @click="doViews">
                  <el-icon class="mr-1"><MagicStick /></el-icon>
                  {{ viewsBusy ? '多视角生成中(约数分钟)…' : '补全多视角' }}
                </el-button>
              </el-tooltip>
            </template>
          </template>
        </div>
        <!-- 图片区：**按这一块的高度显示、整体居中**（2026-09-27 用户两轮反馈的结论）：
             格子定高、宽按产物比例算（定妆照 3/4、角色板 16:9）；**角色板/转台图/场景板占整行、
             框更高**（一张图 15 格，缩略图看不清细节 —— 用户 2026-10-10「三视图更换为角色板显示更详细」）。
             整排水平居中、图用 contain 完整显示。 -->
        <div class="flex flex-wrap gap-3" style="justify-content:center">
          <div v-for="(f, i) in detailImages" :key="f"
               class="cursor-zoom-in relative rounded-lg overflow-hidden"
               :style="'height:' + (wideTile(f) ? BOARD_BOX_H : IMG_BOX_H) +
                 ';aspect-ratio:' + tileRatio(f) +
                 (wideTile(f) ? ';flex-basis:100%' : '') +
                 ';background:var(--mx-bg2);border:1px solid var(--mx-line)'"
               @click="previewDetail(i)">
            <img :src="imgURL(f)" style="width:100%;height:100%;object-fit:contain;object-position:center" loading="lazy" />
            <span class="absolute text-xs px-1 py-0.5"
                  style="left:0;right:0;bottom:0;background:color-mix(in srgb, var(--mx-bg0) 58%, transparent);color:var(--mx-text)">{{ labelOf(f) }}</span>
          </div>
          <el-empty v-if="!detailImages.length" description="这条资产还没有图" :image-size="60" />
        </div>
      </div>
      <div>
        <div class="flex items-center gap-2 mb-1">
          <span class="text-xs" style="color:var(--mx-text3)">中文描述(跨项目一致性用)</span>
          <div class="flex-1"></div>
          <el-tooltip content="让 AI 依据这张定妆照生成中文描述与英文提示词(配了视觉模型就读图,否则用文本模型)" placement="top">
            <el-button size="small" text :loading="descBusy" :disabled="!inLib" @click="doDescribe">
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
        来源:{{ sourceLabel(detail?.source || source) }} ·
        首次入库:{{ detail?.source_project || '-' }} ·
        已被 {{ detail?.used_count ?? 0 }} 个项目复用
        <span v-if="detail?.aliases?.length">
          <br />同一角色的其它叫法(分集分镜换称呼也不会重出定妆照):{{ detail.aliases.join('、') }}
        </span>
      </div>
    </div>
    <template #footer>
      <el-button plain @click="emit('update:open', false)">关闭</el-button>
      <el-tooltip content="把上面编辑过的中文描述与英文提示词写入资产库" placement="top">
        <el-button :loading="metaBusy" :disabled="!inLib" @click="saveMeta">保存描述/提示词</el-button>
      </el-tooltip>
      <el-tooltip content="同一角色再摇一张定妆照(描述与提示词不变,只换随机种子)" placement="top">
        <el-button type="primary" :loading="regenBusy" :disabled="!inLib" @click="regenDetail">
          <el-icon class="mr-1"><Trophy /></el-icon>{{ regenBusy ? '抽卡中(约 1 分钟)…' : '抽卡(换一张定妆照)' }}
        </el-button>
      </el-tooltip>
      <el-button type="danger" plain :disabled="!inLib" @click="removeDetail">
        <el-icon class="mr-1"><Delete /></el-icon>删除该资产
      </el-button>
    </template>
  </el-dialog>
</template>
