<template>
  <div class="p-3 flex flex-col gap-3" style="height:100%;min-height:0">
    <!-- 顶栏：分类切换 + 提示词口径 -->
    <div class="panel p-3 flex items-center gap-3 flex-wrap">
      <b class="text-sm">壁纸库</b>
      <el-radio-group v-model="kind" size="small">
        <el-radio-button value="mobile">手机壁纸</el-radio-button>
        <el-radio-button value="desktop">桌面壁纸</el-radio-button>
      </el-radio-group>
      <span class="timecode" style="font-size:10px">
        本分类 {{ items.length }} 张 · 共 {{ total }} 张
      </span>
      <div class="flex-1"></div>
      <span class="timecode" style="font-size:10px" :title="meta.hint">{{ meta.hint }}</span>
      <el-button size="small" @click="load">刷新</el-button>
    </div>

    <div class="flex gap-3 flex-1" style="min-height:0">
      <!-- 左：参数 -->
      <div class="panel p-3 flex flex-col gap-2" style="width:400px;flex:none;overflow-y:auto">
        <b class="text-xs">① 描述（一句话说清你想要什么画面）</b>
        <el-input v-model="desc" type="textarea" :rows="3"
                  placeholder="例：深夜的赛博城市天台，远处霓虹倒映在湿的地面上，安静、冷色调" />
        <div class="flex items-center gap-2">
          <el-button size="small" :disabled="!desc.trim() || meta.llm_ready === false"
                     :title="meta.llm_ready === false
                       ? '平台 LLM 没配置 —— 设置里填了 LLM 地址/密钥/模型之后，这里才能按你的描述写词'
                       : '让平台 LLM 按你的描述写一段英文提示词（写完可以自己改）'"
                     @click="writePrompt">AI 写词</el-button>
          <span class="timecode" style="font-size:10px">{{ promptFrom || '（还没写词）' }}</span>
        </div>
        <div v-if="promptNote" class="text-xs" style="color:var(--mx-text3)">{{ promptNote }}</div>

        <b class="text-xs mt-1">② 提示词（可编辑；英文，画面里别出现文字）</b>
        <el-input v-model="prompt" type="textarea" :rows="6" />

        <b class="text-xs mt-1">③ 参数</b>
        <div class="flex items-center gap-2">
          <span class="timecode" style="font-size:10px;width:52px">分辨率</span>
          <el-select v-model="sizeIdx" size="small" style="width:100%" @change="refreshSizeNote">
            <el-option v-for="(s, i) in sizes" :key="s.w + 'x' + s.h" :label="s.label" :value="i" />
            <el-option label="自定义…（自己填宽高）" :value="-1" />
          </el-select>
        </div>
        <!-- 自定义宽高：平台会**夹上限 + 对齐 16** ⇒ 实际出的尺寸由后端那一处口径算出来（前端不许自己算一套） -->
        <div v-if="sizeIdx === -1" class="flex items-center gap-2">
          <span class="timecode" style="font-size:10px;width:52px">宽 × 高</span>
          <el-input-number v-model="deskW" size="small" :min="meta.limits.min_side" :max="meta.limits.max_side"
                           :step="meta.limits.align" :controls="false" style="width:96px" @change="refreshSizeNote" />
          <span class="timecode">×</span>
          <el-input-number v-model="deskH" size="small" :min="meta.limits.min_side" :max="meta.limits.max_side"
                           :step="meta.limits.align" :controls="false" style="width:96px" @change="refreshSizeNote" />
        </div>
        <div class="flex items-center gap-2">
          <el-button size="small" :disabled="!meta.screen || !meta.screen.fit_w"
                     :title="screenTip" @click="useScreen">按本机屏幕</el-button>
          <span class="timecode" style="font-size:10px">{{ meta.screen && meta.screen.note ? meta.screen.note : '' }}</span>
        </div>
        <!-- 实际会出多少：只有在"与所选不一致"或"自定义"时才显示（后端算的，见 sizeNote） -->
        <div v-if="sizeNote" class="text-xs" style="color:var(--mx-warn, #d19a66)">{{ sizeNote }}</div>
        <div class="flex items-center gap-2">
          <span class="timecode" style="font-size:10px;width:52px">张数</span>
          <el-input-number v-model="count" size="small" :min="1" :max="4" />
          <span class="timecode" style="font-size:10px">seed</span>
          <el-input-number v-model="seed" size="small" :min="0" :controls="false" style="width:110px" />
        </div>
        <el-checkbox v-model="both"
                     title="勾上：同一条词，手机（竖版）与桌面（横版）各出一套 —— 两端各按自己的画幅">同时生成手机 + 桌面</el-checkbox>

        <!-- 输出目录：改的是"以后生成的图落在哪"；老目录里的图**不会被搬动**（这句话写在气泡里） -->
        <div class="flex items-center gap-2">
          <span class="timecode" style="font-size:10px;width:52px">输出目录</span>
          <span class="timecode flex-1" style="font-size:10px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap"
                :title="dirTip">{{ curDir.path }}</span>
          <el-button size="small" @click="pickDir">改目录</el-button>
          <el-button size="small" :disabled="!curDir.custom" @click="resetDir">恢复默认</el-button>
        </div>
        <div v-if="curDir.custom && curDir.exists === false" class="text-xs" style="color:var(--mx-warn, #d19a66)">
          这个输出目录现在还不在盘上 —— 生成时会自动建出来
        </div>

        <el-button type="primary" size="small" :loading="busy" @click="generate">
          {{ busy ? '出图中…' : '生成壁纸' }}
        </el-button>
        <div class="text-xs" style="color:var(--mx-text3)">
          出图在本机 ComfyUI 上跑；{{ both ? '两端' : '单端' }} × {{ count }} 张，按 {{ curSizeLabel }} 出，
          落在上面那个目录里。
        </div>
      </div>

      <!-- 右：网格 -->
      <div class="panel p-3 flex-1 flex flex-col" style="min-width:0">
        <div class="flex items-center gap-2 mb-2">
          <b class="text-xs">{{ kind === 'mobile' ? '手机壁纸' : '桌面壁纸' }}</b>
          <span class="timecode">{{ items.length }} 张</span>
          <div class="flex-1"></div>
          <span class="timecode" style="font-size:10px">点图放大 · 悬浮看提示词</span>
        </div>
        <!-- 桌面壁纸：把库里的图设成**本机系统桌面壁纸**（单图 + 平台自己的幻灯片轮换） -->
        <div v-if="kind === 'desktop'" class="desk-panel mb-2">
          <div class="flex items-center gap-2 flex-wrap">
            <span class="timecode" style="font-size:10px">系统壁纸</span>
            <span class="timecode" style="font-size:10px">
              {{ desk.supported === false ? '本平台不支持' : (desk.current_in_library ? '现在就是：' + desk.current_name : '现在不是库里的图') }}
            </span>
            <template v-if="desk.supported !== false">
              <div class="flex-1"></div>
              <span class="timecode" style="font-size:10px">间隔(分)</span>
              <el-input-number v-model="deskMin" size="small" :min="1" :max="1440" :controls="false" style="width:78px" />
              <el-checkbox v-model="deskShuffle" title="勾上：轮换时随机挑（不勾＝从早到晚按顺序换）">随机</el-checkbox>
              <el-button size="small" type="primary" :title="deskTip" @click="startDesk">开始轮换</el-button>
              <el-button size="small" :disabled="!desk.running" @click="stopDesk">停止轮换</el-button>
            </template>
          </div>
          <div v-if="desk.supported !== false" class="flex items-center gap-2 flex-wrap mt-1">
            <span class="timecode" style="font-size:10px" title="在缩略图右上角勾选＝只在这些里轮换；一个都不勾＝全部参与">参与轮换</span>
            <span class="timecode" style="font-size:10px">
              {{ desk.picks.length ? '已选 ' + desk.picks.length + ' / 共 ' + desk.count + ' 张' : '全部 ' + desk.count + ' 张' }}
            </span>
            <el-button size="small" text @click="pickAll">全选</el-button>
            <el-button size="small" text :disabled="!desk.picks.length" @click="pickNone">清空</el-button>
          </div>
          <div class="text-xs mt-1" style="color:var(--mx-text3)">{{ desk.note }}</div>
          <div v-if="desk.picks_note" class="text-xs mt-1" style="color:var(--mx-text3)">{{ desk.picks_note }}</div>
          <div v-if="desk.native_note" class="text-xs mt-1" style="color:var(--mx-warn, #d19a66)">{{ desk.native_note }}</div>
        </div>
        <div class="flex-1 overflow-y-auto tree-scroll">
          <div v-if="!items.length" class="text-xs py-8 text-center" style="color:var(--mx-text3)">
            这一类还没有壁纸 —— 左边写好描述与提示词，点「生成壁纸」
          </div>
          <div class="wp-grid">
            <div v-for="it in items" :key="it.file" class="wp-item">
              <div class="wp-thumb">
                <img :src="imgURL(it.file)" :alt="it.prompt"
                     :title="`${it.w}×${it.h} · seed ${it.seed}\n${it.prompt}`"
                     @click="view(it)" />
                <span v-if="isCurrent(it)" class="wp-badge" title="系统当前的桌面壁纸就是这张">系统壁纸</span>
                <el-checkbox v-if="kind === 'desktop'" class="wp-pick" :model-value="picked(it)"
                             title="勾上＝轮换只在这些里换（可多选；一个都不勾＝全部）"
                             @change="togglePick(it)" @click.stop />
              </div>
              <div class="flex items-center gap-2 mt-1">
                <span class="timecode" style="font-size:10px">{{ it.w }}×{{ it.h }}</span>
                <div class="flex-1"></div>
                <el-button v-if="kind === 'desktop'" size="small" text
                           title="把这张立刻设成当前系统桌面壁纸（单图）"
                           @click="setDesk(it)">设为壁纸</el-button>
                <el-button size="small" text type="danger" @click="remove(it)">删除</el-button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox, ElLoading } from 'element-plus'
import { api, G } from '../store.js'

// 分类两项与后端同一口径（mobile/desktop）。
const kind = ref('mobile')
const desc = ref('')
const prompt = ref('')
const promptFrom = ref('')
const promptNote = ref('')
const sizeIdx = ref(0)
const count = ref(1)
const seed = ref(0)
const both = ref(false)
const busy = ref(false)
const mobile = ref([])
const desktop = ref([])
// dirs/limits/screen 都由后端给：改过输出目录没有、硬口径是什么、本机屏幕多少 —— 前端一个字都不猜
const meta = ref({
  sizes: { mobile: [], desktop: [] }, llm_ready: true, hint: '',
  dirs: { mobile: {}, desktop: {} },
  limits: { max_side: 2048, min_side: 512, align: 16 },
  screen: { w: 0, h: 0, fit_w: 0, fit_h: 0, note: '' },
})
const bust = ref(Date.now())

// 自定义分辨率（sizeIdx === -1 时生效）+ "实际会出多少"（口径在后端那一处算）
const deskW = ref(1920)
const deskH = ref(1088)
const sizeNote = ref('')

// 系统桌面壁纸的状态（后端说是什么就是什么；前端只负责显示与转述）
const desk = ref({
  supported: true, mode: 'single', running: false, file: '', current: '',
  current_in_library: false, current_name: '', interval_min: 30, shuffle: false,
  order_label: '', note: '', native_note: '',
  // 轮换范围（后端唯一出处）：picks 为空 = 全部参与
  picks: [], picks_count: 0, picks_missing: [], picks_note: '', count: 0,
})
const deskMin = ref(30)
const deskShuffle = ref(false)
const deskTip = computed(() => (desk.value.running
  ? `平台正在轮换：每 ${deskMin.value} 分钟换一张（${deskShuffle.value ? '随机' : '顺序'}）—— 只在平台运行期间生效，退出平台就停在最后一张`
  : '点它开始轮换：平台自己按间隔换桌面壁纸（不动 Windows 那套幻灯片配置）'))

const items = computed(() => (kind.value === 'mobile' ? mobile.value : desktop.value))
const total = computed(() => mobile.value.length + desktop.value.length)
const sizes = computed(() => (meta.value.sizes && meta.value.sizes[kind.value]) || [])
// sizeIdx === -1 ⇒ 自定义（用 deskW/deskH）；否则用该分类的档位（档位尺寸由后端定义）
const curSize = computed(() => (sizeIdx.value === -1
  ? { w: Number(deskW.value) || 0, h: Number(deskH.value) || 0, label: `自定义 ${deskW.value}×${deskH.value}` }
  : (sizes.value[sizeIdx.value] || { w: 0, h: 0, label: '' })))
const curSizeLabel = computed(() => curSize.value.label || '默认档')

// 输出目录（后端说是什么就是什么）+ 两句话（气泡里）：改目录**不会搬动**老图
const curDir = computed(() => (meta.value.dirs && meta.value.dirs[kind.value]) || { path: '', default: '', custom: false, exists: false })
const dirTip = computed(() => {
  const d = curDir.value
  const base = `当前输出目录：${d.path}\n默认目录：${d.default}`
  return base + (d.custom
    ? '\n\n（改过）以后生成的图落在这里；**此前生成的图留在老目录里，不会被搬过去**'
    : '\n\n点「改目录」可以换成你自己的目录（例如 D:\\壁纸）')
})
const screenTip = computed(() => {
  const s = meta.value.screen || {}
  if (!s.fit_w) return '读不到本机屏幕分辨率（远程/无头会话）'
  return `${s.note}\n\n点它就把宽高填成这个尺寸（平台出图单边上限 ${meta.value.limits.max_side}，超过会被夹住）`
})

function imgURL(file) {
  return '/api/wallpapers/image?path=' + encodeURIComponent(file) + '&t=' + bust.value
}

async function load() {
  try {
    const r = await api.wallpapers()
    mobile.value = r.mobile || []
    desktop.value = r.desktop || []
    meta.value = r
    if (sizeIdx.value >= sizes.value.length) sizeIdx.value = 0
    // 自定义宽高的初值 = 本分类第一档（用户点「自定义」时不会看到 0×0）
    const first = sizes.value[0]
    if (first && !deskW.value && !deskH.value) { deskW.value = first.w; deskH.value = first.h }
    await refreshSizeNote()
  } catch (e) { ElMessage.error(e.message) }
}

// refreshSizeNote 问后端"这个尺寸实际会出成多少"（夹上限/对齐 16 都发生在那一处）。
// 为什么不让前端自己算：那样就有两份口径，迟早分叉（而且用户会看到"写着 3440、出的是 2048"）。
async function refreshSizeNote() {
  const w = curSize.value.w
  const h = curSize.value.h
  if (!w || !h) { sizeNote.value = ''; return }
  try {
    const r = await api.wallpaperSize(kind.value, w, h)
    // 只有"实际 ≠ 所选"时才值得提示（否则界面全是废话）
    sizeNote.value = (r.note && (Number(r.w) !== Number(w) || Number(r.h) !== Number(h)))
      ? `${r.note} ⇒ 实际出 ${r.w}×${r.h}`
      : ''
  } catch (e) { sizeNote.value = '' }
}

// useScreen 按本机屏幕比例填宽高（尺寸与说明都由后端算；读不到就置灰）
function useScreen() {
  const s = meta.value.screen || {}
  if (!s.fit_w) { ElMessage.warning(s.note || '读不到本机屏幕分辨率'); return }
  sizeIdx.value = -1
  deskW.value = s.fit_w
  deskH.value = s.fit_h
  refreshSizeNote()
  ElMessage.info(s.note || '已按本机屏幕填写')
}

// applyDir 改输出目录（dir 为空串 = 恢复默认）；后端会把"老图不搬动"如实说回来，这里照转。
async function applyDir(dir) {
  try {
    const r = await api.wallpaperDir(kind.value, dir)
    if (r && r.dirs) meta.value.dirs = r.dirs
    ElMessage.success(r && r.note ? r.note : '输出目录已更新')
  } catch (e) { ElMessage.error(e.message) }
}

// pickDir 选目录：桌面模式用 Wails 的原生目录选择器；浏览器里没有 ⇒ 回退到手输绝对路径。
async function pickDir() {
  const rt = window.runtime
  if (rt && typeof rt.OpenDirectoryDialog === 'function') {
    try {
      const picked = await rt.OpenDirectoryDialog({
        Title: '选壁纸输出目录', CanCreateDirectories: true, DefaultDirectory: curDir.value.path,
      })
      if (picked) await applyDir(picked)
      return
    } catch (e) {
      ElMessage.error('目录选择器打开失败：' + ((e && e.message) || e))
      return
    }
  }
  try {
    const r = await ElMessageBox.prompt(
      '填一个绝对路径（例：D:\\壁纸）。改完只影响**以后**生成的图 —— 老目录里的图不会被搬动。',
      '输出目录', { inputValue: curDir.value.path })
    if (r && r.value) await applyDir(r.value)
  } catch (e) { /* 用户取消 */ }
}

async function resetDir() { await applyDir('') }

// 换分类时：把自定义宽高重置成新类别的第一档，并重算"实际会出多少"
// （手机是竖版、桌面是横版，沿用上一类的宽高会变成"竖版尺寸出横版图"）
watch(kind, () => {
  const f = sizes.value[0]
  if (f) { deskW.value = f.w; deskH.value = f.h }
  refreshSizeNote()
})

async function writePrompt() {
  try {
    const r = await api.wallpaperPrompt(desc.value, kind.value)
    prompt.value = r.prompt || ''
    promptFrom.value = r.from || ''
    promptNote.value = r.note || ''
  } catch (e) { ElMessage.error(e.message) }
}

async function generate() {
  if (!prompt.value.trim()) { ElMessage.warning('先写提示词（或点「AI 写词」）'); return }
  busy.value = true
  const loading = ElLoading.service({ text: '正在本机出图（每张约 10-60 秒）…' })
  try {
    const r = await api.wallpaperGenerate({
      desc: desc.value, prompt: prompt.value, kind: kind.value,
      width: curSize.value.w, height: curSize.value.h,
      seed: seed.value || 0, count: count.value, both: both.value,
    })
    bust.value = Date.now()
    await load()
    ElMessage.success(`已生成 ${r.count || 0} 张壁纸${both.value ? '（两端各一套）' : ''}`)
  } catch (e) { ElMessage.error(e.message) } finally { busy.value = false; loading.close() }
}

async function remove(it) {
  try {
    await ElMessageBox.confirm('删除这张壁纸？', '确认', { type: 'warning' })
  } catch { return }
  try {
    await api.wallpaperDelete(it.file)
    bust.value = Date.now()
    await load()
  } catch (e) { ElMessage.error(e.message) }
}

// ── 系统桌面壁纸（只有桌面分类用得上） ──────────────────────────────────────
// 三条纪律：① 只有桌面分类给入口（后端对竖版会回 400，前端不该让用户白点一次）；
// ② 状态一律**转述**后端那句话说（模式、边界、系统自己那套幻灯片的提醒都不在前端编）；
// ③ 轮换是**平台自己**在换（app 运行期间），这一点写在气泡里，别让用户以为改了 Windows 设置。
async function loadDesk() {
  try {
    const r = await api.wallpaperDesktop()
    desk.value = r
    if (r.interval_min) deskMin.value = r.interval_min
    deskShuffle.value = !!r.shuffle
  } catch (e) { ElMessage.error(e.message) }
}

function baseName(p) { return String(p || '').split(/[\\/]/).pop() }

function isCurrent(it) {
  return kind.value === 'desktop' && desk.value.current_name === baseName(it.file)
}

async function setDesk(it) {
  try {
    desk.value = await api.wallpaperDesktopApply({ file: it.file, mode: 'single' })
    ElMessage.success('已设为系统桌面壁纸：' + baseName(it.file))
  } catch (e) { ElMessage.error(e.message) }
}

async function startDesk() {
  // 起点：上次那张（还在清单里就用它）→ 否则本分类第一张
  const f = (desk.value.file && items.value.some(x => x.file === desk.value.file))
    ? desk.value.file : ((items.value[0] || {}).file || '')
  if (!f) { ElMessage.warning('这一类还没有壁纸 —— 先在左边「生成壁纸」'); return }
  try {
    desk.value = await api.wallpaperDesktopApply({
      file: f, mode: 'slideshow', interval_min: deskMin.value, shuffle: deskShuffle.value,
    })
    ElMessage.success(`开始轮换：每 ${desk.value.interval_min} 分钟换一张（${desk.value.order_label}）`)
  } catch (e) { ElMessage.error(e.message) }
}

async function stopDesk() {
  try {
    desk.value = await api.wallpaperDesktopStop()
    ElMessage.success('已停止轮换（系统停在最后一张）')
  } catch (e) { ElMessage.error(e.message) }
}

// 轮换范围（后端唯一出处：picks 为空 = 全部参与）：
// 缩略图右上角那个勾选框就是"只在我勾的这几张里换"的入口 —— 勾选即写后端，立刻生效。
function picked(it) {
  return (desk.value.picks || []).some(p => baseName(p).toLowerCase() === baseName(it.file).toLowerCase())
}

async function savePicks(files) {
  try {
    desk.value = await api.wallpaperDesktopPicks(files)
    ElMessage.success(desk.value.picks.length
      ? `轮换范围：已选 ${desk.value.picks.length} 张`
      : '轮换范围：全部桌面壁纸')
  } catch (e) { ElMessage.error(e.message) }
}

async function togglePick(it) {
  const rest = (desk.value.picks || []).filter(p => baseName(p).toLowerCase() !== baseName(it.file).toLowerCase())
  if (rest.length === (desk.value.picks || []).length) rest.push(it.file) // 原来没勾 ⇒ 现在勾上
  await savePicks(rest)
}

async function pickAll() {
  if (!items.value.length) { ElMessage.warning('这一类还没有壁纸'); return }
  await savePicks(items.value.map(x => x.file))
}

async function pickNone() { await savePicks([]) }

function view(it) {
  // 复用全站的大图预览（与作品库/资产库同一套缩放平移）：items + index 是它认的形状
  G.viewer = {
    index: 0,
    items: [{
      type: 'image', src: imgURL(it.file), title: `${it.w}×${it.h}`,
      meta: it.from ? `正向词：${it.from}` : '', foot: it.prompt, kindLabel: 'WALLPAPER',
    }],
  }
}

onMounted(() => { load(); loadDesk() })
defineExpose({ load })
</script>

<style scoped>
/* 网格：按每张壁纸自己的宽高比排，竖版与横版混在一格里也看得清 */
.wp-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(168px, 1fr));
  gap: 10px;
}
.wp-item img {
  width: 100%;
  height: 200px;
  object-fit: cover;
  border-radius: 8px;
  border: 1px solid var(--mx-line);
  cursor: zoom-in;
  /* 底衬用主题底色兜底（裸色会被 theme_coverage 门拦下；var() 里的兜底值放行） */
  background: var(--mx-bg1, #0b0b0e);
}

/* 系统壁纸那一栏：描边和网格区分开（颜色一律走主题变量，裸色会被 theme_coverage 门拦下） */
.desk-panel {
  border: 1px solid var(--mx-line);
  border-radius: 8px;
  padding: 8px;
  background: var(--mx-bg2, #14141a);
}

/* 「当前系统壁纸」角标：**压在缩略图封面上**（2026-10-11 用户：「系统壁纸标签直接放图片封面上」）。
   原来它跟尺寸、按钮挤在同一行的 flex 里，窄卡片上会被挤成一字一行（看着像竖排字）—— 现在脱离文档流。 */
.wp-thumb {
  position: relative;
  line-height: 0;
}
.wp-badge {
  position: absolute;
  top: 6px;
  left: 6px;
  font-size: 10px;
  line-height: 16px;
  padding: 0 5px;
  border-radius: 3px;
  color: var(--mx-accent, #7aa2f7);
  border: 1px solid var(--mx-accent, #7aa2f7);
  background: var(--mx-bg1, #0b0b0e);
  opacity: 0.92;
}

/* 轮换范围的勾选框：同样压在封面上（右上角），不占布局宽度 */
.wp-pick {
  position: absolute;
  top: 2px;
  right: 4px;
  padding: 2px;
  border-radius: 3px;
  background: var(--mx-bg1, #0b0b0e);
  opacity: 0.9;
}
</style>
