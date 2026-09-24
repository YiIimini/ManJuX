<script setup>
import { G, api, refreshStatus } from '../store'
import { Reading } from '@element-plus/icons-vue'
import { THEMES, DEFAULT_THEME_ID, themeById, loadPrefs, applyTheme } from '../themes'
import { reactive, onMounted, onBeforeUnmount, ref, computed, watch } from 'vue'
import { ElMessage } from 'element-plus'

// form 的**完整默认形状**:模板里 v-model="form.xxx.yyy" 的每一层都必须在首帧
// 就存在。此前 vlm 段是新加的 UI 但 form 的初值里没有 vlm 键 —— 首帧渲染
// form.vlm.base_url 立刻抛 "Cannot read properties of undefined (reading
// 'base_url')",而且 getSettings 返回前会连抛三次(base_url/model/api_key),
// 用户看到的就是设置页一进去满屏「界面错误」。
const emptyForm = () => ({
  server_port: 8790, data_dir: '', novel_dir: '', auto_open_ui: true,
  llm: { base_url: '', api_key: '', model: '', timeout_seconds: 600 },
  comfy: {
    base_url: '', root_dir: '', auto_start: true, idle_stop_min: 10, cond_cache: true,
    reserve_vram_gb: 1, sage_attention: true,
  },
  minimax: { base_url: '', api_key: '', model: '', resolution: '768P' },
  vlm: { base_url: '', api_key: '', model: '' },
  ff: { ffmpeg_path: '', ffprobe_path: '' },
  render: { default_engine: 'auto', fade_sec: 0.5, shot_timeout: 45 },
  split: {
    chunk_size: 12000, concurrency: 3, max_orig_chars: 9000,
    prev_summary: 400, brief_min: 400, brief_max: 700, shot_limit: 30,
  },
})
const form = reactive(emptyForm())
// fill 逐层合并而不是整体替换:后端字段比模板多(comfy.unet_ref2va 等模型文件名、
// minimax.resolution…)时必须原样带上保存,整体替换会把后端新增字段从这次提交里
// 抹掉;而模板用到的层则由 emptyForm 兜底,后端少给哪一层都不会再抛 undefined。
function fill(s) {
  if (!s || typeof s !== 'object') return
  for (const [k, v] of Object.entries(s)) {
    if (v && typeof v === 'object' && !Array.isArray(v)) {
      form[k] = Object.assign({}, form[k] || {}, v)
    } else {
      form[k] = v
    }
  }
}
const testing = ref('')
// ---- 自动保存:任何字段变更即标记 dirty;离开页面(切导航/关窗)自动落盘 ----
// 修复「改了超时/模型没点保存,切页回来就丢」的体验(2026-09-09 反馈)
const dirty = ref(false)
let savedSnap = ''
const isDirty = () => JSON.stringify(form) !== savedSnap
watch(form, () => { dirty.value = isDirty() }, { deep: true })
async function loadSettings(force = false) {
  if (dirty.value && !force) return // 有未保存修改时不拉旧值覆盖(顶栏刷新也不打断编辑)
  try {
    const s = await api.getSettings()
    fill(s)
    // 三态字段在前端一律落成布尔:后端 nil = 未表态(自动),若原样交给 el-switch,
    // 开关会显示成"关"而实际在生效(用户看到的就是"明明开着却显示关")。
    form.comfy.sage_attention = form.comfy.sage_attention !== false
    savedSnap = JSON.stringify(form)
    dirty.value = false
  } catch (e) { ElMessage.error(e.message) }
}
watch(() => G.tick.settings, () => loadSettings())

// 个性化:主题与玻璃拟态(纯前端偏好,即时生效即时保存)
const appear = reactive({ theme: DEFAULT_THEME_ID, glass: false, glassAlpha: 0.72 })
const curTheme = computed(() => themeById(appear.theme))
function pickTheme(id) {
  appear.theme = id
  applyTheme({ themeId: id })
}
function onGlass(v) {
  applyTheme({ glass: v })
}
function onAlpha(v) {
  applyTheme({ glassAlpha: v })
}

onMounted(async () => {
  const p = loadPrefs()
  // 存量偏好可能指向已删除的主题(旧版有 6 套,现在只保留极简墨黑 + 8 套新配色):
  // 解析不到的 id 一律回落到默认,否则卡片列表里一个都点不亮
  appear.theme = themeById(p.theme).id
  appear.glass = !!p.glass
  appear.glassAlpha = p.glassAlpha || 0.72
  await loadSettings(true) // 首次进入强制拉一次(此前有 2 个 onMounted 各拉一次,重复请求)
})

async function save() {
  try {
    await api.putSettings(form)
    savedSnap = JSON.stringify(form)
    dirty.value = false
    ElMessage.success('设置已保存')
    refreshStatus()
  } catch (e) { ElMessage.error(e.message) }
}
// 离开设置页时若有未保存修改自动落盘(切页/切视图/刷新前)
onBeforeUnmount(() => { if (dirty.value) save() })

// 悬浮条:一键测试全部依赖
const testingAll = ref(false)
async function testAll() {
  testingAll.value = true
  let fail = 0
  for (const t of ['llm', 'comfy', 'minimax', 'vlm']) {
    testing.value = t
    try {
      await api.testConn(t)
    } catch (e) { fail++; ElMessage.error(e.message) }
  }
  testing.value = ''
  if (!fail) ElMessage.success('全部依赖连接正常 ✓')
  else ElMessage.warning(`${fail} 项连接异常,详见消息`)
  testingAll.value = false
}

async function testConn(target) {
  testing.value = target
  try {
    const d = await api.testConn(target)
    ElMessage.success(d.message || '连接正常')
  } catch (e) { ElMessage.error(e.message) } finally { testing.value = '' }
}
</script>

<template>
  <div class="h-full overflow-y-auto p-6">
    <div class="max-w-[860px] mx-auto flex flex-col gap-5">
      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
          <el-icon color="var(--mx-amber)"><Brush /></el-icon>个性化外观
        </h3>
        <div class="theme-grid mb-4">
          <div v-for="t in THEMES" :key="t.id" class="theme-card" :class="{ on: appear.theme === t.id }" @click="pickTheme(t.id)">
            <div class="theme-swatch">
              <i v-for="c in t.swatch" :key="c" :style="{ background: `linear-gradient(135deg, ${c}, ${c}cc)` }"></i>
            </div>
            <div class="theme-name">
              {{ t.name }}
              <span v-if="t.id === DEFAULT_THEME_ID" class="theme-default">默认</span>
            </div>
            <div class="theme-desc">{{ t.desc }}</div>
            <el-icon v-if="appear.theme === t.id" class="theme-check"><CircleCheckFilled /></el-icon>
          </div>
        </div>
        <el-form label-position="top">
          <div class="grid gap-x-8" style="grid-template-columns: 1fr 1.6fr">
            <el-form-item label="流体玻璃效果(卡片/弹层/侧栏半透明毛玻璃)">
              <el-switch v-model="appear.glass" @change="onGlass" />
            </el-form-item>
            <el-form-item label="玻璃不透明度(越低越通透,拖动实时预览)">
              <div class="flex items-center gap-4 w-full">
                <el-slider v-model="appear.glassAlpha" :min="0.3" :max="0.98" :step="0.02"
                  :disabled="!appear.glass" :format-tooltip="v => (v * 100).toFixed(0) + '%'"
                  style="flex:1" @input="onAlpha" />
                <span class="font-mono text-xs" style="color:var(--mx-text2); width:40px">{{ (appear.glassAlpha * 100).toFixed(0) }}%</span>
              </div>
            </el-form-item>
          </div>
        </el-form>
        <div class="glass-preview" v-if="appear.glass">
          <span>FLUID GLASS PREVIEW</span>
          <i v-for="c in (curTheme?.swatch || [])" :key="c" :style="{ background: c, width: '18px', height: '18px', borderRadius: '6px' }"></i>
        </div>
      </div>

      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
          <el-icon color="var(--mx-cyan)"><ChatDotRound /></el-icon>文本大模型(LLM · OpenAI 兼容)
        </h3>
        <el-form label-position="top" class="grid gap-x-6" style="grid-template-columns:1fr 1fr">
          <el-form-item label="Base URL">
            <el-input v-model="form.llm.base_url" placeholder="https://api.deepseek.com/v1" />
          </el-form-item>
          <el-form-item label="模型(可选推荐或输入自定义模型 ID)">
            <el-select v-model="form.llm.model" filterable allow-create default-first-option
              placeholder="选择或输入模型 ID" style="width:100%">
              <el-option-group label="DeepSeek(当前接入)">
                <el-option value="deepseek-flash" label="deepseek-flash · V4.1-Flash,官方推荐(速度快/费用低/支持视觉),分镜 30-90 秒" />
              </el-option-group>
              <el-option-group label="其它(需对应平台 Base URL)">
                <el-option value="MiniMaxi-Text-01" label="MiniMaxi-Text-01 · MiniMax 文本模型" />
                <el-option value="glm-4.6" label="glm-4.6 · 智谱" />
                <el-option value="glm-4.5-air" label="glm-4.5-air · 智谱" />
                <el-option value="qwen-plus" label="qwen-plus · 阿里" />
                <el-option value="gpt-4o-mini" label="gpt-4o-mini · OpenAI" />
              </el-option-group>
            </el-select>
          </el-form-item>
          <el-form-item label="API Key" class="col-span-2">
            <div class="flex gap-2 w-full">
              <el-input v-model="form.llm.api_key" show-password placeholder="sk-…(本地存储于 data/settings.json)" style="flex:1" />
              <el-tooltip content="发一次对话请求验证 LLM 连通与密钥" placement="top">
                <el-button :loading="testing==='llm'" @click="testConn('llm')">测试</el-button>
              </el-tooltip>
            </div>
          </el-form-item>
          <el-form-item label="超时(秒;推理模型分镜直出建议 ≥600)" class="col-span-2">
            <!-- v-model 键须与后端 JSON 键一致:timeout_seconds(曾误用 timeout,
                 致改动从未落盘、「设置莫名丢失」假象——2026-09-09 根因) -->
            <el-input-number v-model="form.llm.timeout_seconds" :min="60" :max="1800" :step="60" style="width:100%" />
          </el-form-item>
        </el-form>
      </div>

      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
          <el-icon color="var(--mx-amber)"><Cpu /></el-icon>本地 ComfyUI 引擎(H3-Base)
        </h3>
        <el-form label-position="top" class="grid gap-x-6" style="grid-template-columns:1fr 1fr">
          <el-form-item label="服务地址">
            <el-input v-model="form.comfy.base_url" placeholder="http://127.0.0.1:8190" />
          </el-form-item>
          <el-form-item label="根目录(含 ComfyUI/ 与 standalone-env/)">
            <el-input v-model="form.comfy.root_dir" placeholder="D:\Ai\ManJuX\comfyui" />
          </el-form-item>
          <el-form-item label="未运行时自动代管启动">
            <el-switch v-model="form.comfy.auto_start" />
          </el-form-item>
          <el-form-item label="空闲自动退出(分钟;管线结束后无任务自动停引擎释放显存,0=常驻)">
            <el-input-number v-model="form.comfy.idle_stop_min" :min="0" :max="240" :step="5" style="width:100%" />
          </el-form-item>
          <el-form-item label="显存预留(GB;引擎启动时留给系统/浏览器,防渲染时整机卡顿,0=自定)">
            <!-- :precision="0" 不只是美观:该字段后端是 int,而 el-input-number 默认允许
                 手输小数,一旦落成 2.5 保存就会被 json.Unmarshal 拒掉(400),
                 若以任何方式写进 settings.json 更会让启动 fatalIf 直接退出。 -->
            <el-input-number v-model="form.comfy.reserve_vram_gb" :min="0" :max="8" :step="1" :precision="0" style="width:100%" />
          </el-form-item>
          <el-form-item label="Sage 注意力加速(官方推荐:生成约快一倍、显存占用更低;需已装 sageattention 包)">
            <el-switch v-model="form.comfy.sage_attention" />
          </el-form-item>
          <el-form-item label="两阶段渲染(条件缓存 .pt)">
            <el-switch v-model="form.comfy.cond_cache" />
          </el-form-item>
          <el-form-item label="连通性" class="col-span-2">
            <el-tooltip content="探测 ComfyUI 服务地址连通性(未运行时显示离线属正常)" placement="top">
              <el-button :loading="testing==='comfy'" @click="testConn('comfy')">测试 ComfyUI 连接</el-button>
            </el-tooltip>
            <span class="text-xs ml-2" style="color:var(--mx-text2)">模型文件配置已移至「ComfyUI → 模型清单核对」</span>
          </el-form-item>
        </el-form>
      </div>

      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
          <el-icon color="var(--mx-cyan)"><Cloudy /></el-icon>MiniMax H3 云端 API
        </h3>
        <el-form label-position="top" class="grid gap-x-6" style="grid-template-columns:1fr 1fr">
          <el-form-item label="Base URL">
            <el-select v-model="form.minimax.base_url">
              <el-option value="https://api.minimax.cn" label="国内(api.minimax.cn)" />
              <el-option value="https://api.minimaxi.com" label="国际(api.minimaxi.com)" />
            </el-select>
          </el-form-item>
          <el-form-item label="模型">
            <el-select v-model="form.minimax.model">
              <el-option value="MiniMax-H3" label="MiniMax-H3(768P/2K)" />
              <el-option value="MiniMax-H3-Max" label="MiniMax-H3-Max(480P/768P,高速)" />
            </el-select>
          </el-form-item>
          <el-form-item label="API Key" class="col-span-2">
            <div class="flex gap-2 w-full">
              <el-input v-model="form.minimax.api_key" show-password placeholder="eyJ…(platform.minimaxi.com 创建)" style="flex:1" />
              <el-tooltip content="验证 MiniMax H3 云端 API 密钥可用性" placement="top">
                <el-button :loading="testing==='minimax'" @click="testConn('minimax')">测试</el-button>
              </el-tooltip>
            </div>
          </el-form-item>
        </el-form>
      </div>

      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
          <el-icon color="var(--mx-green)"><View /></el-icon>视觉大模型(VLM 智能审片)
        </h3>
        <el-form label-position="top" class="grid gap-x-6" style="grid-template-columns:1fr 1fr">
          <el-form-item label="Base URL">
            <el-input v-model="form.vlm.base_url" placeholder="https://open.bigmodel.cn/api/paas/v4" />
          </el-form-item>
          <el-form-item label="模型">
            <el-input v-model="form.vlm.model" placeholder="glm-4.6v-flash(智谱免费)" />
          </el-form-item>
          <el-form-item label="API Key" class="col-span-2">
            <div class="flex gap-2 w-full">
              <el-input v-model="form.vlm.api_key" show-password placeholder="智谱 BigModel Key" style="flex:1" />
              <el-tooltip content="验证视觉大模型(智能审片)连通性" placement="top">
                <el-button :loading="testing==='vlm'" @click="testConn('vlm')">测试</el-button>
              </el-tooltip>
            </div>
          </el-form-item>
        </el-form>
      </div>

      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
          <el-icon><VideoPlay /></el-icon>渲染引擎
        </h3>
        <el-form label-position="top" class="grid gap-x-6" style="grid-template-columns:1fr 1fr">
          <el-form-item label="新项目默认引擎">
            <el-select v-model="form.render.default_engine">
              <el-option value="auto" label="自动(本地优先,不可用自动切云端,省额度)" />
              <el-option value="comfy" label="本地 ComfyUI(免费,768p)" />
              <el-option value="api" label="MiniMax 云端 API(纯云端,按量计费)" />
            </el-select>
          </el-form-item>
          <el-form-item label="转场时长(秒;cut/fade/dissolve 叠化)">
            <el-input-number v-model="form.render.fade_sec" :min="0.1" :max="3" :step="0.1" :precision="1" style="width:100%" />
          </el-form-item>
          <el-form-item label="单镜渲染超时(分钟;超时自动释放显存重试)">
            <el-input-number v-model="form.render.shot_timeout" :min="10" :max="120" style="width:100%" />
          </el-form-item>
          <div class="text-xs flex items-end pb-2" style="color:var(--mx-text2)">
            已有项目的引擎不受影响,可在项目内「渲染出片 ⚙」单独调整。
          </div>
        </el-form>
      </div>

      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
          <el-icon><Reading /></el-icon>小说拆集
        </h3>
        <el-form label-position="top" class="grid gap-x-6" style="grid-template-columns:1fr 1fr">
          <el-form-item label="分段大小(字/段;全书超此值按章节切段并行拆解)">
            <el-input-number v-model="form.split.chunk_size" :min="10000" :max="80000" :step="5000" style="width:100%" />
          </el-form-item>
          <el-form-item label="并行拆集并发数(同时调 LLM 的段数)">
            <el-input-number v-model="form.split.concurrency" :min="1" :max="6" style="width:100%" />
          </el-form-item>
          <el-form-item label="每集附原文上限(字;附原文给分镜 LLM 看全文改编)">
            <el-input-number v-model="form.split.max_orig_chars" :min="3000" :max="20000" :step="1000" style="width:100%" />
          </el-form-item>
          <el-form-item label="前情提要截断(字;给下集的上集剧情摘要长度)">
            <el-input-number v-model="form.split.prev_summary" :min="100" :max="1000" :step="50" style="width:100%" />
          </el-form-item>
          <el-form-item label="每集大纲字数下限">
            <el-input-number v-model="form.split.brief_min" :min="200" :max="1000" :step="50" style="width:100%" />
          </el-form-item>
          <el-form-item label="每集大纲字数上限">
            <el-input-number v-model="form.split.brief_max" :min="400" :max="1500" :step="100" style="width:100%" />
          </el-form-item>
          <el-form-item label="镜头数安全上限(0=LLM 按内容自决)">
            <el-input-number v-model="form.split.shot_limit" :min="0" :max="50" style="width:100%" />
          </el-form-item>
        </el-form>
      </div>

      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
          <el-icon><Monitor /></el-icon>应用
        </h3>
        <el-form label-position="top" class="grid gap-x-6" style="grid-template-columns:1fr 1fr">
          <el-form-item label="HTTP 端口(占用自动 +1)">
            <el-input-number v-model="form.server_port" :min="1024" :max="65535" style="width:100%" />
          </el-form-item>
          <el-form-item label="启动时自动打开工作台">
            <el-switch v-model="form.auto_open_ui" />
          </el-form-item>
          <el-form-item label="小说库目录(小说库页扫描;书=子文件夹,推荐 全本/封面/正文 结构)" class="col-span-2">
            <el-input v-model="form.novel_dir" placeholder="D:/Ai/ManJuX/data/Novel" />
          </el-form-item>
          <el-form-item label="数据目录" class="col-span-2">
            <el-input v-model="form.data_dir" disabled />
          </el-form-item>
          <el-form-item label="ffmpeg 路径(空=PATH)">
            <el-input v-model="form.ff.ffmpeg_path" placeholder="ffmpeg" />
          </el-form-item>
          <el-form-item label="ffprobe 路径(空=PATH)">
            <el-input v-model="form.ff.ffprobe_path" placeholder="ffprobe" />
          </el-form-item>
        </el-form>
      </div>

      <div class="flex justify-end gap-2 items-center">
        <span v-if="dirty" class="text-xs mr-1" style="color:var(--mx-amber)">● 有未保存修改(离开页面自动保存)</span>
        <span v-else class="text-xs mr-1" style="color:var(--mx-text3)">修改自动保存,切页不丢失</span>
        <el-button @click="testAll" :loading="testingAll">测试全部</el-button>
        <el-button type="primary" @click="save">
          <el-icon class="mr-1"><Check /></el-icon>保存设置
        </el-button>
      </div>
    </div>

    <!-- 悬浮操作条:右侧跟随滚动 -->
    <div class="float-actions">
      <el-tooltip content="依次测试 LLM / ComfyUI / MiniMax / VLM 连通性" placement="left">
        <button class="fa-btn" :disabled="testingAll || !!testing" @click="testAll">
          <el-icon :size="18"><Connection /></el-icon>测试
        </button>
      </el-tooltip>
      <el-tooltip content="保存全部设置" placement="left">
        <button class="fa-btn primary" @click="save">
          <el-icon :size="18"><Check /></el-icon>保存
        </button>
      </el-tooltip>
    </div>
  </div>
</template>
