<script setup>
import { ref, reactive, computed, onMounted, onUnmounted, watch } from 'vue'
import { G, api } from '../store'
import { ElMessage, ElMessageBox } from 'element-plus'

const st = ref(null)
const loading = ref(false)
// Sage 加速:状态 + 一键安装(轮子与本机 torch 绑定,升级后需重装)
const sage = ref(null)
const sageBusy = ref(false)
// sageEnabled 状态灯:装了且当前设置下会被启用才算"绿"
const sageEnabled = computed(() => !!(sage.value?.installed && sage.value?.enabled))
async function loadSage() {
  try { sage.value = await api.comfySage() } catch { sage.value = null }
}
async function installSage() {
  sageBusy.value = true
  try {
    const d = await api.comfySageInstall()
    ElMessage.success(`Sage 加速已安装:${d.version}(${d.note})`)
    await loadSage()
  } catch (e) { ElMessage.error('安装失败:' + e.message) } finally { sageBusy.value = false }
}
const acting = ref('')

// 版本管理器
const vers = ref(null)
const verLoading = ref(false)
const checking = ref(false)


async function refresh() {
  loadSage()
  loading.value = true
  try { st.value = await api.comfyAdmin() }
  catch (e) { ElMessage.error('状态获取失败') }
  loading.value = false
}
async function refreshVersions(online = false, manual = false) {
  if (online) checking.value = true
  else verLoading.value = true
  try {
    vers.value = await api.comfyVersions(online)
    if (online && manual) {
      const core = vers.value?.core?.[0] || {}
      const rows = [...(vers.value.core || []), ...(vers.value.nodes || [])]
      const unknown = rows.filter(x => x.status === 'unknown' && x.repo).length
      const behind = rows.filter(x => x.status === 'behind').length
      if (core.remote || core.remoteCommit) {
        ElMessage.success(`版本对比完成${behind ? `:${behind} 项有更新` : ':全部最新'}${unknown ? `,${unknown} 项未获取到` : ''}`)
      } else {
        ElMessage.warning('在线版本获取失败:GitHub 限流或网络不通(本地版本不受影响,稍后自动重试)')
      }
    }
  } catch (e) {
    if (manual) ElMessage.error('版本检查失败:' + e.message)
  }
  checking.value = false
  verLoading.value = false
}
// 页面停留时自动刷新(8s):引擎在别处(首页/外部)启停后本页状态不陈旧;
// 卸载即停,不空转
let pollTimer = null
let retryTimer = null
let onlineTries = 0
// 自动补查:首轮联网对比后仍有仓未获取到(网络抖动/限流)→ 3s 后自动再拉,
// 最多 3 轮(后端失败不缓存,重试即真查)。用户不应看到大面积「未检查」。
function autoOnlinePassive() {
  onlineTries++
  if (onlineTries > 3 || !vers.value) return
  const rows = [ ...(vers.value.core || []), ...(vers.value.nodes || []) ]
  const unknown = rows.filter(r => r.status === 'unknown' && r.repo).length
  if (unknown > 0) {
    retryTimer = setTimeout(() => { refreshVersions(true) }, 3000)
  }
}
const rawRefreshVersions = refreshVersions
refreshVersions = async (online = false, manual = false) => {
  await rawRefreshVersions(online, manual)
  if (online) autoOnlinePassive()
}
onMounted(() => {
  refresh()
  refreshVersions(true)
  loadModels()
  pollTimer = setInterval(refresh, 8000)
})
onUnmounted(() => { if (pollTimer) clearInterval(pollTimer); if (retryTimer) clearTimeout(retryTimer) })
watch(() => G.tick.comfy, () => { refresh(); refreshVersions(false) })

async function act(action, tip) {
  if (tip) {
    try { await ElMessageBox.confirm(tip, '确认操作', { confirmButtonText: '执行', cancelButtonText: '取消', type: 'warning' }) }
    catch { return }
  }
  acting.value = action
  try {
    const d = await api.comfyAction(action)
    ElMessage.success(d.note || '已执行')
    setTimeout(refresh, action === 'start' ? 1500 : 400)
  } catch (e) { ElMessage.error(e.message) } finally { acting.value = '' }
}

// WEB 窗口:独立 App 窗口(Chrome/Edge --app 无地址栏桌面观感)/ 系统默认浏览器
async function openWebApp() {
  try {
    await api.comfyOpenAppWin()
    ElMessage.success('已打开 ComfyUI 独立窗口')
  } catch (e) { ElMessage.error(e.message) }
}
async function openWebBrowser() {
  try {
    await api.comfyOpenWeb()
    ElMessage.success('已在系统默认浏览器打开')
  } catch (e) { ElMessage.error(e.message) }
}

// ---- 模型文件配置(从设置页移入,引擎管理页更内聚) ----
const modelForm = reactive({})
const modelSaving = ref(false)
const modelSaved = ref(false)
// 模型字段定义(下拉选择用)
const modelFields = [
  { key: 'unet_ref2va', label: 'UNET(Ref2VA)', span2: true },
  { key: 'unet_fl2va', label: 'UNET(FL2VA / T2VA)', span2: true },
  { key: 'turbo_lora_ref2va', label: 'Turbo LoRA(Ref2VA)' },
  { key: 'turbo_lora_fl2va', label: 'Turbo LoRA(FL2VA)' },
  { key: 'clip', label: '文本编码器(CLIP · qwen3vl)' },
  { key: 'video_vae', label: '视频 VAE' },
  { key: 'audio_vae', label: '音频 VAE(缺失=黑屏)' },
  { key: 'image_unet', label: '定妆照模型(UNETLoader)' },
  { key: 'image_clip', label: '定妆照编码器(CLIPLoader,type 随家族自动选)' },
  { key: 'image_vae', label: '定妆照 VAE' },
  // 多视角图像编辑(Qwen-Image-Edit-2511):这套决定「角色多视角」走哪条路 ——
  // 齐了就自动用编辑路径(拿定妆照本体转角度,身份/画风按像素保住),缺了退回
  // H3 转身视频(会把角色重新设计,校验多半拒绝)。默认文件名与 Comfy-Org 官方
  // 打包一致,换 repack 权重时在这里改。
  { key: 'edit_unet', label: '多视角编辑 UNet(Qwen-Image-Edit-2511)', span2: true },
  { key: 'edit_clip', label: '多视角编辑 CLIP(Qwen2.5-VL-7B)' },
  { key: 'edit_vae', label: '多视角编辑 VAE(qwen_image_vae)' },
  { key: 'edit_turbo_lora', label: '多视角加速 LoRA(Lightning 4 步)' },
  { key: 'edit_angles_lora', label: '多视角转角度 LoRA(Multiple-Angles)' },
]
// 从 st.models 里拿 ComfyUI 实际注册的文件名清单(下拉选项)
function modelOptions(key) {
  const m = (st.value?.models || []).find(x => x.key === key)
  return m?.list || []
}

async function loadModels() {
  try {
    const d = await api.getSettings()
    Object.keys(modelForm).forEach(k => delete modelForm[k])
    Object.assign(modelForm, d.comfy || {})
  } catch { /* 静默 */ }
}
async function saveModels() {
  modelSaving.value = true
  try {
    const cur = await api.getSettings()
    const next = { ...cur, comfy: { ...cur.comfy, ...modelForm } }
    await api.putSettings(next)
    modelSaved.value = true
    setTimeout(() => modelSaved.value = false, 2000)
    refresh()
  } catch (e) { ElMessage.error(e.message) } finally { modelSaving.value = false }
}

// ---- 目录说明 ----
const comfyRoot = computed(() => st.value?.root_dir || 'D:/Ai/ManJuX/comfyui')
const dirInfo = computed(() => [
  { label: '模型根目录', icon: 'Box', path: comfyRoot.value + '/ComfyUI/models',
    desc: '所有 AI 模型文件(UNET/VAE/CLIP/LoRA 等)。设置页「ComfyUI」面板配置的模型名必须与这里的文件名一致。', color: 'var(--mx-amber)' },
  { label: '模型 → UNet(扩散主干)', icon: 'Cpu', path: comfyRoot.value + '/ComfyUI/models/diffusion_models',
    desc: 'H3 视频生成模型(Ref2VA/FL2VA int8 量化版)与图像模型(默认 Qwen-Image-2.1)。体积最大(~20GB/个)。', color: 'var(--mx-purple)' },
  { label: '模型 → VAE(编解码器)', icon: 'Film', path: comfyRoot.value + '/ComfyUI/models/vae',
    desc: '视频/音频 VAE,负责 latent ↔ 像素转换。缺失会导致黑屏或无音轨。', color: 'var(--mx-cyan)' },
  { label: '模型 → CLIP(文本编码)', icon: 'EditPen', path: comfyRoot.value + '/ComfyUI/models/clip',
    desc: '文本理解模型(qwen3vl),把提示词编码为 latent 条件。影响提示词服从度。', color: 'var(--mx-green)' },
  { label: '模型 → LoRA(加速补丁)', icon: 'Lightning', path: comfyRoot.value + '/ComfyUI/models/loras',
    desc: 'Turbo LoRA(4 步出图加速补丁)。影响生成速度与质量的平衡。', color: 'var(--mx-yellow)' },
  { label: '渲染输出目录', icon: 'FolderOpened', path: comfyRoot.value + '/ComfyUI/output',
    desc: 'ComfyUI 的原始输出(中间视频/上传的参考图副本/接缝 latent)。可用「一键清理」按钮释放空间,不影响已有成片。', color: 'var(--mx-red)' },
  { label: '自定义节点(插件)', icon: 'Grid', path: comfyRoot.value + '/ComfyUI/custom_nodes',
    desc: 'H3 增强插件(条件缓存/接缝连续/加速节点)。版本管理器可对比在线最新版。', color: 'var(--mx-text2)' },
  { label: 'ComfyUI 主程序', icon: 'Setting', path: comfyRoot.value + '/ComfyUI',
    desc: 'ComfyUI 本体(Git 仓库)。版本管理器显示当前版本与在线最新对比。', color: 'var(--mx-text3)' },
])
// ---- 应用内目录浏览(不依赖系统资源管理器) ----
const browseDlg = reactive({ open: false, path: '', entries: [], total: 0, loading: false })
async function openDir(path) {
  browseDlg.open = true
  browseDlg.path = path
  browseDlg.loading = true
  browseDlg.entries = []
  try {
    const d = await api.browse(path)
    browseDlg.entries = d.entries || []
    browseDlg.total = d.total || 0
  } catch (e) {
    ElMessage.error(e.message)
    browseDlg.open = false
  } finally { browseDlg.loading = false }
}
function fmtSize(b) {
  if (b <= 0) return ''
  if (b < 1024) return b + ' B'
  if (b < 1048576) return (b / 1024).toFixed(1) + ' KB'
  if (b < 1073741824) return (b / 1048576).toFixed(1) + ' MB'
  return (b / 1073741824).toFixed(2) + ' GB'
}

const STATUS_META = {
  latest:  { label: '最新', cls: 'ok' },
  behind:  { label: '有更新', cls: 'warn' },
  unknown: { label: '未检查', cls: '' },
  local:   { label: '本地文件', cls: '' },
  norepo:  { label: '非 Git 安装', cls: '' },
  missing: { label: '未命中', cls: 'bad' },
}
// 单元格着色:一致→双绿;落后→本地红 / 在线绿;未检查→灰
function verCellCls(row, which) {
  if (row.status === 'latest') return 'vc-green'
  if (row.status === 'behind') return which === 'local' ? 'vc-red' : 'vc-green'
  return 'vc-dim'
}
</script>

<template>
  <div class="h-full overflow-y-auto p-6" v-if="st">
    <!-- 状态总览 -->
    <div class="panel panel-glow p-6 mb-5 relative overflow-hidden">
      <div style="position:absolute;right:-30px;top:-30px;font-size:130px;opacity:.06;pointer-events:none">⚙️</div>
      <div class="flex items-center gap-4 flex-wrap">
        <i class="dot" :class="st.alive ? 'ok' : 'bad'" style="width:14px;height:14px"></i>
        <div>
          <h1 class="text-lg font-bold m-0">ComfyUI 引擎管理 <span class="timecode">ENGINE</span></h1>
          <div class="text-xs mt-1" style="color:var(--mx-text2)">
            {{ st.alive ? st.version : '未运行' }}
            <template v-if="st.gpu"> · {{ st.gpu }}</template>
            · 队列 <b style="color:var(--mx-amber)">{{ st.running }}</b> 运行 / <b style="color:var(--mx-cyan)">{{ st.pending }}</b> 等待
          </div>
        </div>
        <div class="flex-1"></div>
        <div class="flex gap-2 flex-wrap">
          <el-tooltip content="启动本地 ComfyUI 渲染引擎(加载模型约需 1-2 分钟)" placement="bottom">
          <el-button v-if="!st.alive" type="primary" :loading="acting==='start'" @click="act('start')">
            <el-icon class="mr-1"><VideoPlay /></el-icon>启动引擎
          </el-button>
          </el-tooltip>
          <el-tooltip content="结束全部 ComfyUI 相关进程(含外部启动的同安装实例)" placement="bottom">
          <el-button v-if="st.alive" :loading="acting==='stop'" @click="act('stop', '停止引擎?将结束全部 ComfyUI 进程,运行中的任务将中断。')">
            <el-icon class="mr-1"><VideoPause /></el-icon>停止引擎
          </el-button>
          </el-tooltip>
          <el-dropdown v-if="st.alive" @command="(c) => c === 'app' ? openWebApp() : openWebBrowser()">
            <el-button type="primary" plain>
              <el-icon class="mr-1"><Link /></el-icon>打开 WEB 窗口<el-icon class="ml-1"><ArrowDown /></el-icon>
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="app"><el-icon><FullScreen /></el-icon>独立窗口(推荐,无地址栏)</el-dropdown-item>
                <el-dropdown-item command="browser"><el-icon><ChromeFilled /></el-icon>系统默认浏览器</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
          <el-button size="small" circle @click="refresh" :loading="loading"><el-icon><Refresh /></el-icon></el-button>
        </div>
      </div>
    </div>

    <div class="grid gap-5" style="grid-template-columns:repeat(auto-fit,minmax(420px,1fr))">
      <!-- 运行控制 -->
      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2"><el-icon color="var(--mx-cyan)"><Odometer /></el-icon>运行控制</h3>
        <div class="flex flex-col gap-3">
          <div class="flex items-center gap-3">
            <div class="flex-1">
              <div class="text-sm">
                队列任务
                <!-- 队列数字查不到时必须说出来:显示成 0/0 会让人以为"引擎闲着",
                    而显存护栏正是按这个数字决定要不要卸权重(见后端 QueueKnown)。 -->
                <span v-if="st.alive && st.queue_known === false" class="text-xs ml-2" style="color:var(--mx-yellow)">状态未知</span>
                <span v-else-if="st.alive" class="text-xs ml-2 timecode">{{ st.queue_running || 0 }} 运行 / {{ st.queue_pending || 0 }} 等待</span>
              </div>
              <div class="text-xs" style="color:var(--mx-text3)">
                {{ st.alive && st.queue_known === false ? 'ComfyUI 没答上队列查询(可能正忙):此时不会自动释放显存' : '清空等待中的任务(不影响运行中)' }}
              </div>
            </div>
            <el-tooltip content="清空等待中的任务(不影响正在执行的任务)" placement="top">
              <el-button size="small" :disabled="!st.pending" :loading="acting==='queue-clear'" @click="act('queue-clear')">清空队列</el-button>
            </el-tooltip>
          </div>
          <el-divider style="margin:6px 0" />
          <div class="flex items-center gap-3">
            <div class="flex-1">
              <div class="text-sm">中断当前任务</div>
              <div class="text-xs" style="color:var(--mx-text3)">中止正在执行的渲染(可重试)</div>
            </div>
            <el-tooltip content="中止正在执行的渲染(该镜可重试)" placement="top">
              <el-button size="small" type="warning" plain :disabled="!st.running" :loading="acting==='interrupt'" @click="act('interrupt','中断当前执行的任务?')">中断</el-button>
            </el-tooltip>
          </div>
          <el-divider style="margin:6px 0" />
          <div class="flex items-center gap-3">
            <div class="flex-1">
              <div class="text-sm">释放显存</div>
              <div class="text-xs" style="color:var(--mx-text3)">卸载已加载模型(/free,长任务前清理)</div>
            </div>
            <el-tooltip content="卸载已加载模型释放显存(长任务前清理)" placement="top">
              <el-button size="small" :disabled="!st.alive" :loading="acting==='free'" @click="act('free')">释放</el-button>
            </el-tooltip>
          </div>
          <el-divider style="margin:6px 0" />
          <!-- Sage 加速:状态 + 一键安装。此前只有函数与 8 秒轮询、模板里一处都没用
               (README 却写着"管理页可一键安装" —— 典型的文档漂移 + 假入口)。
               现在真把入口做出来:标题行给状态,说明行给"为什么没启用",
               未装或未生效时给安装按钮(按本机 torch/CUDA 自动匹配官方轮子)。 -->
          <div class="flex items-start gap-3">
            <i class="dot" :class="sageEnabled ? 'ok' : 'warn'" style="margin-top:6px"></i>
            <div class="flex-1">
              <div class="text-sm">
                Sage Attention 加速
                <span class="timecode ml-2">{{ sage?.installed ? (sage.version || '已安装') : '未安装' }}</span>
              </div>
              <div class="text-xs" style="color:var(--mx-text3)">
                {{ sage?.detail || '按本机 torch + CUDA 匹配官方轮子;装好后重启引擎自动启用(官方实测约提速一倍)' }}
              </div>
              <div v-if="sage && (sage.torch_version || sage.cuda_version)" class="text-xs mt-1" style="color:var(--mx-text3)">
                本机 torch {{ sage.torch_version || '未知' }} · CUDA {{ sage.cuda_version || '未知' }}
              </div>
            </div>
            <el-tooltip :content="sage?.installed ? '重新安装/更新 Sage 加速(升级 ComfyUI 或 torch 后需要)' : '按本机 torch+CUDA 自动匹配并安装(只装这一个包,不动 torch)'" placement="top">
              <el-button size="small" :loading="sageBusy" @click="installSage">
                {{ sage?.installed ? '重装/更新' : '一键安装' }}
              </el-button>
            </el-tooltip>
          </div>
        </div>
      </div>

      <!-- H3 节点 -->
      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2"><el-icon color="var(--mx-purple)"><Grid /></el-icon>能力节点</h3>
        <div class="flex flex-col gap-2.5">
          <div v-for="n in st.nodes" :key="n.class" class="flex items-center gap-3">
            <i class="dot" :class="n.ok ? 'ok' : 'warn'"></i>
            <span class="text-sm flex-1">{{ n.label }}</span>
            <span class="timecode">{{ n.ok ? '已安装' : '未检测到' }}</span>
          </div>
          <div class="text-xs mt-2 p-3 rounded-lg" style="background:color-mix(in srgb, var(--mx-cyan) 5%, transparent);color:var(--mx-text2)">
            条件缓存:两阶段渲染(换 seed 不重编码);接缝:镜间 22 帧连续。缺失仅影响对应增强功能,基础渲染不受影响。
          </div>
        </div>
      </div>
    </div>

    <!-- 模型核对 -->
    <div class="panel p-5 mt-5">
      <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
        <el-icon color="var(--mx-amber)"><Files /></el-icon>模型清单核对
        <span class="text-xs font-normal" style="color:var(--mx-text3)">配置名 vs ComfyUI 实际注册(大小写敏感)</span>
      </h3>
      <div class="grid gap-2" style="grid-template-columns:repeat(auto-fill,minmax(440px,1fr))">
        <div v-for="m in st.models" :key="m.key" class="flex items-center gap-3 p-3 rounded-lg"
             :style="{ background: m.ok ? 'color-mix(in srgb, var(--mx-green) 5%, transparent)' : (m.name ? 'color-mix(in srgb, var(--mx-red) 6%, transparent)' : 'transparent'), border: '1px solid var(--mx-line)' }">
          <i class="dot" :class="m.ok ? 'ok' : (m.name ? 'bad' : '')"></i>
          <div class="flex-1 min-w-0">
            <div class="text-xs" style="color:var(--mx-text2)">{{ m.label }}</div>
            <div class="text-xs truncate tag-mono" :style="{ color: m.ok ? 'var(--mx-green)' : (m.name ? 'var(--mx-red)' : 'var(--mx-text3)') }">
              {{ m.name || '(未配置)' }}
            </div>
            <div v-if="!m.ok && m.list?.length" class="text-xs mt-1 truncate" style="color:var(--mx-text3)" :title="m.list.join('\n')">
              可选:{{ m.list.slice(0,3).join(' / ') }}{{ m.list.length>3 ? ` …等${m.list.length}项` : '' }}
            </div>
          </div>
        </div>
      </div>
      <div class="text-xs mt-3" style="color:var(--mx-text3)">未命中会导致渲染提交 400(模型名不在列表);在下方下拉选择(列表来自 ComfyUI 实际注册,避免手输错名)。</div>
      <!-- 模型文件配置:下拉选择(选项来自 ComfyUI 实际注册,防手输错名) -->
      <el-form label-position="top" class="grid gap-x-6 mt-4" style="grid-template-columns:1fr 1fr">
        <el-form-item v-for="f in modelFields" :key="f.key" :label="f.label" :class="f.span2 ? 'col-span-2' : ''">
          <el-select v-model="modelForm[f.key]" filterable allow-create default-first-option
                     placeholder="从 ComfyUI 已注册文件中选择" style="width:100%">
            <el-option v-for="v in modelOptions(f.key)" :key="v" :value="v" :label="v" />
          </el-select>
        </el-form-item>
        <el-form-item class="col-span-2">
          <el-button type="primary" :loading="modelSaving" @click="saveModels">保存模型配置</el-button>
          <span v-if="modelSaved" class="text-xs ml-2" style="color:var(--mx-green)">已保存</span>
        </el-form-item>
      </el-form>
    </div>

    <!-- 目录说明 -->
    <div class="panel p-5 mt-5">
      <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
        <el-icon color="var(--mx-cyan)"><FolderOpened /></el-icon>目录说明
        <span class="text-xs font-normal" style="color:var(--mx-text3)">ComfyUI 相关存储路径;点路径可打开所在文件夹</span>
      </h3>
      <div class="grid gap-3" style="grid-template-columns:repeat(auto-fill,minmax(340px,1fr))">
        <div v-for="d in dirInfo" :key="d.label" class="p-3 rounded-lg" style="border:1px solid var(--mx-line)">
          <div class="flex items-center gap-2 mb-1">
            <el-icon :size="14" :color="d.color || 'var(--mx-text3)'"><FolderOpened /></el-icon>
            <span class="text-xs font-bold">{{ d.label }}</span>
          </div>
          <div class="text-xs cursor-pointer truncate font-mono p-1 rounded"
               style="color:var(--mx-amber);background:color-mix(in srgb, var(--mx-amber) 6%, transparent)"
               :title="'点击打开: ' + d.path"
               @click="openDir(d.path)">{{ d.path }}</div>
          <div class="text-xs mt-1.5" style="color:var(--mx-text3);line-height:1.5">{{ d.desc }}</div>
          <div v-if="d.size" class="text-xs mt-1" :style="{ color: d.sizeColor || 'var(--mx-text2)' }">{{ d.size }}</div>
        </div>
      </div>
    </div>

    <!-- 版本管理器:本体 / 插件 / 模型 VS 在线最新 -->
    <div class="panel p-5 mt-5" v-if="vers">
      <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2 flex-wrap">
        <el-icon color="var(--mx-purple)"><Stamp /></el-icon>版本管理器
        <span class="text-xs font-normal" style="color:var(--mx-text3)">本地版本 VS 在线最新(GitHub);版本一致双绿,落后时本地红 / 在线绿</span>
        <div class="flex-1"></div>
        <el-tooltip content="重新读取本地版本(git tag / commit 与文件状态,不联网,秒出)" placement="top">
          <el-button size="small" :loading="verLoading" circle @click="refreshVersions(false)"><el-icon><Refresh /></el-icon></el-button>
        </el-tooltip>
        <el-tooltip content="联网对比 GitHub 上游仓库最新 Release / Tag(数秒;受网络影响个别仓库可能查询失败显示「未检查」)" placement="top">
          <el-button size="small" type="primary" plain :loading="checking" @click="refreshVersions(true, true)">
            <el-icon class="mr-1"><Cloudy /></el-icon>检查更新
          </el-button>
        </el-tooltip>
      </h3>

      <el-table :data="[{ ...vers.core?.[0], _kind: '本体' }, ...(vers.nodes||[]).map(n => ({ ...n, _kind: '插件' }))]"
        size="small" height="380"
        style="--el-table-border-color: var(--mx-line); width: 100%">
        <el-table-column label="类型" width="56" align="center" header-align="center">
          <template #default="{ row }">
            <span class="timecode">{{ row._kind }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="name" label="名称" min-width="240">
          <template #default="{ row }">
            <div class="text-xs cursor-help" :title="row.repo ? '仓库:https://github.com/' + row.repo : '无远程仓库(本地目录)'" style="text-decoration: underline dotted; text-underline-offset: 3px; text-decoration-color: var(--mx-text3);">{{ row.name }}</div>
            <div style="font-size:10px;color:var(--mx-text3);line-height:1.4" v-if="row.desc">{{ row.desc }}</div>
          </template>
        </el-table-column>
        <el-table-column prop="local" label="本地版本" width="128" align="center" header-align="center">
          <template #default="{ row }">
            <div class="flex flex-col">
              <span class="font-mono text-xs" :class="verCellCls(row, 'local')">{{ row.local !== row.localCommit ? (row.local || '—') : '—' }}</span>
              <span class="font-mono" style="font-size:10px;color:var(--mx-text3)">{{ row.localCommit ? '@' + row.localCommit : '—' }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="remote" label="在线最新" width="136" align="center" header-align="center">
          <template #default="{ row }">
            <div class="flex flex-col">
              <span class="font-mono text-xs" :class="verCellCls(row, 'remote')">{{ row.remote !== row.remoteCommit ? (row.remote || '—') : '—' }}</span>
              <span class="font-mono" style="font-size:10px;color:var(--mx-text3)">{{ row.remoteCommit ? '@' + row.remoteCommit : '—' }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="96" align="center" header-align="center">
          <template #default="{ row }">
            <span class="ver-badge" :class="STATUS_META[row.status]?.cls">
              <i class="dot" :class="STATUS_META[row.status]?.cls || ''" v-if="row.status !== 'unknown'"></i>{{ STATUS_META[row.status]?.label || row.status }}
            </span>
          </template>
        </el-table-column>
      </el-table>

      <div class="text-xs mt-3 flex items-center gap-1.5" style="color:var(--mx-text3)">
        <el-icon :size="13"><InfoFilled /></el-icon>
        模型文件无统一在线版本源,按本地文件(大小/日期)管理,见上方「模型清单核对」;更新模型请在设置中更换文件。
      </div>
    </div>

    <!-- 应用内目录浏览弹窗 -->
    <el-dialog v-model="browseDlg.open" :title="'📁 ' + browseDlg.path" width="680px" append-to-body>
      <div v-loading="browseDlg.loading" style="min-height:200px;max-height:480px;overflow-y:auto">
        <div v-if="!browseDlg.loading && !browseDlg.entries.length" class="text-center text-xs py-10" style="color:var(--mx-text3)">空目录</div>
        <div v-for="(e, i) in browseDlg.entries" :key="i"
             class="flex items-center gap-2 px-3 py-1.5 rounded cursor-pointer"
             style="border-bottom:1px solid var(--mx-line)"
             :style="i % 2 === 0 ? '' : 'background:var(--mx-hover-soft)'"
             @click="e.isDir && openDir(browseDlg.path + '/' + e.name)">
          <el-icon :size="14" :color="e.isDir ? 'var(--mx-amber)' : 'var(--mx-text3)'">
            <FolderOpened v-if="e.isDir" /><Document v-else />
          </el-icon>
          <span class="text-xs flex-1 truncate" :style="{ color: e.isDir ? 'var(--mx-text)' : 'var(--mx-text2)' }">{{ e.name }}</span>
          <span v-if="!e.isDir" class="text-xs" style="color:var(--mx-text3)">{{ fmtSize(e.size) }}</span>
        </div>
      </div>
      <div class="flex items-center gap-3 mt-3">
        <span class="text-xs" style="color:var(--mx-text3)">{{ browseDlg.total }} 项</span>
        <div class="flex-1"></div>
        <el-button size="small" @click="browseDlg.path && browseDlg.path.split('/').slice(0,-1).join('/') && openDir(browseDlg.path.split('/').slice(0,-1).join('/'))">上级</el-button>
        <el-button size="small" @click="browseDlg.open = false">关闭</el-button>
      </div>
    </el-dialog>
  </div>
</template>
