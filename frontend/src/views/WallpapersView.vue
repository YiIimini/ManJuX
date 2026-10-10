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
          <el-select v-model="sizeIdx" size="small" style="width:100%">
            <el-option v-for="(s, i) in sizes" :key="s.w + 'x' + s.h" :label="s.label" :value="i" />
          </el-select>
        </div>
        <div class="flex items-center gap-3">
          <span class="timecode" style="font-size:10px;width:52px">张数</span>
          <el-input-number v-model="count" size="small" :min="1" :max="4" />
          <span class="timecode" style="font-size:10px">seed</span>
          <el-input-number v-model="seed" size="small" :min="0" :controls="false" style="width:110px" />
        </div>
        <el-checkbox v-model="both"
                     title="勾上：同一条词，手机（竖版）与桌面（横版）各出一套 —— 两端各按自己的画幅">同时生成手机 + 桌面</el-checkbox>

        <el-button type="primary" size="small" :loading="busy" @click="generate">
          {{ busy ? '出图中…' : '生成壁纸' }}
        </el-button>
        <div class="text-xs" style="color:var(--mx-text3)">
          出图在本机 ComfyUI 上跑；{{ both ? '两端' : '单端' }} × {{ count }} 张，按 {{ curSizeLabel }} 出。
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
          <div class="text-xs mt-1" style="color:var(--mx-text3)">{{ desk.note }}</div>
          <div v-if="desk.native_note" class="text-xs mt-1" style="color:var(--mx-warn, #d19a66)">{{ desk.native_note }}</div>
        </div>
        <div class="flex-1 overflow-y-auto tree-scroll">
          <div v-if="!items.length" class="text-xs py-8 text-center" style="color:var(--mx-text3)">
            这一类还没有壁纸 —— 左边写好描述与提示词，点「生成壁纸」
          </div>
          <div class="wp-grid">
            <div v-for="it in items" :key="it.file" class="wp-item">
              <img :src="imgURL(it.file)" :alt="it.prompt"
                   :title="`${it.w}×${it.h} · seed ${it.seed}\n${it.prompt}`"
                   @click="view(it)" />
              <div class="flex items-center gap-2 mt-1">
                <span class="timecode" style="font-size:10px">{{ it.w }}×{{ it.h }}</span>
                <span v-if="isCurrent(it)" class="wp-badge" title="系统当前的桌面壁纸就是这张">系统壁纸</span>
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
import { ref, computed, onMounted } from 'vue'
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
const meta = ref({ sizes: { mobile: [], desktop: [] }, llm_ready: true, hint: '' })
const bust = ref(Date.now())

// 系统桌面壁纸的状态（后端说是什么就是什么；前端只负责显示与转述）
const desk = ref({
  supported: true, mode: 'single', running: false, file: '', current: '',
  current_in_library: false, current_name: '', interval_min: 30, shuffle: false,
  order_label: '', note: '', native_note: '',
})
const deskMin = ref(30)
const deskShuffle = ref(false)
const deskTip = computed(() => (desk.value.running
  ? `平台正在轮换：每 ${deskMin.value} 分钟换一张（${deskShuffle.value ? '随机' : '顺序'}）—— 只在平台运行期间生效，退出平台就停在最后一张`
  : '点它开始轮换：平台自己按间隔换桌面壁纸（不动 Windows 那套幻灯片配置）'))

const items = computed(() => (kind.value === 'mobile' ? mobile.value : desktop.value))
const total = computed(() => mobile.value.length + desktop.value.length)
const sizes = computed(() => (meta.value.sizes && meta.value.sizes[kind.value]) || [])
const curSize = computed(() => sizes.value[sizeIdx.value] || { w: 0, h: 0, label: '' })
const curSizeLabel = computed(() => curSize.value.label || '默认档')

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
  } catch (e) { ElMessage.error(e.message) }
}

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

/* 「当前系统壁纸」小角标 */
.wp-badge {
  font-size: 10px;
  padding: 0 4px;
  border-radius: 3px;
  color: var(--mx-accent, #7aa2f7);
  border: 1px solid var(--mx-accent, #7aa2f7);
}
</style>
