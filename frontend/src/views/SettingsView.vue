<script setup>
import { G, api, refreshStatus } from '../store'
import { THEMES, loadPrefs, applyTheme } from '../themes'
import { reactive, onMounted, ref, computed, watch } from 'vue'
import { ElMessage } from 'element-plus'

const form = reactive({
  server_port: 8790, data_dir: '', auto_open_ui: true,
  llm: {}, comfy: {}, minimax: {}, ff: {}, render: {},
})
const testing = ref('')
async function loadSettings() {
  try {
    const s = await api.getSettings()
    Object.assign(form, s)
  } catch (e) { ElMessage.error(e.message) }
}
watch(() => G.tick.settings, loadSettings)

// 个性化:主题与玻璃拟态(纯前端偏好,即时生效即时保存)
const appear = reactive({ theme: 'midnight-amber', glass: false, glassAlpha: 0.72 })
onMounted(() => {
  const p = loadPrefs()
  appear.theme = p.theme || 'midnight-amber'
  appear.glass = !!p.glass
  appear.glassAlpha = p.glassAlpha || 0.72
})
const curTheme = computed(() => THEMES.find(t => t.id === appear.theme))
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
  try {
    const s = await api.getSettings()
    Object.assign(form, s)
  } catch (e) { ElMessage.error(e.message) }
})

async function save() {
  try {
    await api.putSettings(form)
    ElMessage.success('设置已保存')
    refreshStatus()
  } catch (e) { ElMessage.error(e.message) }
}

// 悬浮条:一键测试全部依赖
const testingAll = ref(false)
async function testAll() {
  testingAll.value = true
  let fail = 0
  for (const t of ['llm', 'comfy', 'minimax', 'vlm']) {
    testing.value = t
    try {
      const r = await fetch('/api/test', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ target: t })
      })
      const d = await r.json()
      if (!r.ok) { fail++; ElMessage.error(d.error || `HTTP ${r.status}`) }
    } catch { fail++ }
  }
  testing.value = ''
  if (!fail) ElMessage.success('全部依赖连接正常 ✓')
  else ElMessage.warning(`${fail} 项连接异常,详见消息`)
  testingAll.value = false
}

async function testConn(target) {
  testing.value = target
  try {
    const r = await fetch('/api/test', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ target })
    })
    const d = await r.json()
    if (!r.ok) throw new Error(d.error || `HTTP ${r.status}`)
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
            <div class="theme-name">{{ t.name }}</div>
            <div class="theme-desc">{{ t.desc }}</div>
            <el-icon v-if="appear.theme === t.id" class="theme-check"><CircleCheckFilled /></el-icon>
          </div>
        </div>
        <el-form label-position="top">
          <div class="grid gap-x-8" style="grid-template-columns: 1fr 1.6fr">
            <el-form-item label="流体玻璃效果(卡片/弹层/侧栏半透明毛玻璃)">
              <el-switch v-model="appear.glass" @change="onGlass" />
            </el-form-item>
            <el-form-item label="玻璃不透明度(越低越通透)">
              <div class="flex items-center gap-4 w-full">
                <el-slider v-model="appear.glassAlpha" :min="0.3" :max="0.98" :step="0.02"
                  :disabled="!appear.glass" :format-tooltip="v => (v * 100).toFixed(0) + '%'"
                  style="flex:1" @change="onAlpha" />
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
                <el-option value="deepseek-chat" label="deepseek-chat · 非推理,分镜直出约 30-60 秒(推荐)" />
                <el-option value="deepseek-reasoner" label="deepseek-reasoner · 深度推理,质量高但需 2-6 分钟" />
                <el-option value="deepseek-v4-flash" label="deepseek-v4-flash · 深度推理(当前),需 2-6 分钟" />
              </el-option-group>
              <el-option-group label="其它(需对应平台 Base URL)">
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
              <el-button :loading="testing==='llm'" @click="testConn('llm')">测试</el-button>
            </div>
          </el-form-item>
          <el-form-item label="超时(秒;推理模型分镜直出建议 ≥600)" class="col-span-2">
            <el-input-number v-model="form.llm.timeout" :min="60" :max="1800" :step="60" style="width:100%" />
          </el-form-item>
        </el-form>
      </div>

      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
          <el-icon color="var(--mx-orange)"><Cpu /></el-icon>本地 ComfyUI 引擎(H3-Base)
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
          <el-form-item label="两阶段渲染(条件缓存 .pt)">
            <el-switch v-model="form.comfy.cond_cache" />
          </el-form-item>
          <el-form-item label="UNET(Ref2VA)" class="col-span-2">
            <el-input v-model="form.comfy.unet_ref2va" />
          </el-form-item>
          <el-form-item label="UNET(FL2VA / T2VA)" class="col-span-2">
            <el-input v-model="form.comfy.unet_fl2va" />
          </el-form-item>
          <el-form-item label="Turbo LoRA(Ref2VA)">
            <el-input v-model="form.comfy.turbo_lora_ref2va" />
          </el-form-item>
          <el-form-item label="Turbo LoRA(FL2VA)">
            <el-input v-model="form.comfy.turbo_lora_fl2va" />
          </el-form-item>
          <el-form-item label="文本编码器(CLIP · qwen3vl)">
            <el-input v-model="form.comfy.clip" />
          </el-form-item>
          <el-form-item label="视频 VAE">
            <el-input v-model="form.comfy.video_vae" />
          </el-form-item>
          <el-form-item label="音频 VAE(缺失=黑屏)">
            <el-input v-model="form.comfy.audio_vae" />
          </el-form-item>
          <el-form-item label="定妆照模型(Krea-2 UNet)">
            <el-input v-model="form.comfy.image_unet" />
          </el-form-item>
          <el-form-item label="定妆照编码器(CLIPLoader type=krea2)">
            <el-input v-model="form.comfy.image_clip" />
          </el-form-item>
          <el-form-item label="定妆照 VAE">
            <el-input v-model="form.comfy.image_vae" />
          </el-form-item>
          <el-form-item label="连通性" class="col-span-2">
            <el-button :loading="testing==='comfy'" @click="testConn('comfy')">测试 ComfyUI 连接</el-button>
            <span class="text-xs ml-2" style="color:var(--mx-text-2)">未运行时渲染阶段会自动代管启动</span>
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
              <el-button :loading="testing==='minimax'" @click="testConn('minimax')">测试</el-button>
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
              <el-button :loading="testing==='vlm'" @click="testConn('vlm')">测试</el-button>
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
          <div class="text-xs flex items-end pb-2" style="color:var(--mx-text-2)">
            已有项目的引擎不受影响,可在项目内「渲染出片 ⚙」单独调整。
          </div>
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

      <div class="flex justify-end gap-2">
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
