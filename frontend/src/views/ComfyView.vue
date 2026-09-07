<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const st = ref(null)
const loading = ref(false)
const acting = ref('')

// 版本管理器
const vers = ref(null)
const verLoading = ref(false)
const checking = ref(false)

// 应用内 WEB 窗口(iframe 全屏弹层)
const webOpen = ref(false)
const webURL = ref('')

async function refresh() {
  loading.value = true
  try { st.value = await fetch('/api/comfy/admin').then(r => r.json()) }
  catch (e) { ElMessage.error('状态获取失败') }
  loading.value = false
}
async function refreshVersions(online = false) {
  if (online) checking.value = true
  else verLoading.value = true
  try {
    const r = await fetch(`/api/comfy/versions${online ? '?online=1' : ''}`)
    vers.value = await r.json()
    if (!r.ok) throw new Error(vers.value?.error || `HTTP ${r.status}`)
  } catch (e) { ElMessage.error('版本信息获取失败:' + e.message) }
  checking.value = false
  verLoading.value = false
}
onMounted(() => { refresh(); refreshVersions(false) })

async function act(action, tip) {
  if (tip) {
    try { await ElMessageBox.confirm(tip, '确认操作', { confirmButtonText: '执行', cancelButtonText: '取消', type: 'warning' }) }
    catch { return }
  }
  acting.value = action
  try {
    const r = await fetch(`/api/comfy/${action}`, { method: 'POST' })
    const d = await r.json()
    if (!r.ok) throw new Error(d.error)
    ElMessage.success('已执行')
    setTimeout(refresh, action === 'start' ? 1500 : 400)
  } catch (e) { ElMessage.error(e.message) } finally { acting.value = '' }
}

// WEB 窗口:应用内弹层 / 系统默认浏览器 两种方式
function openWebApp() {
  webURL.value = (st.value?.base_url || 'http://127.0.0.1:8190') + '/'
  webOpen.value = true
}
async function openWebBrowser() {
  try {
    const r = await fetch('/api/comfy/open-web', { method: 'POST' })
    const d = await r.json()
    if (!r.ok) throw new Error(d.error)
    ElMessage.success('已在系统默认浏览器打开')
  } catch (e) { ElMessage.error(e.message) }
}

const STATUS_META = {
  latest:  { label: '最新', cls: 'ok' },
  behind:  { label: '有更新', cls: 'warn' },
  unknown: { label: '未检查', cls: '' },
  local:   { label: '本地文件', cls: '' },
  norepo:  { label: '非 Git 安装', cls: '' },
  missing: { label: '未命中', cls: 'bad' },
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
          <el-button v-if="!st.alive" type="primary" :loading="acting==='start'" @click="act('start')">
            <el-icon class="mr-1"><VideoPlay /></el-icon>启动引擎
          </el-button>
          <el-button v-if="st.alive" :disabled="!st.owned" @click="act('stop','停止 ComfyUI?运行中的任务将中断。')">
            <el-icon class="mr-1"><VideoPause /></el-icon>停止{{ st.owned ? '' : '(外部启动)' }}
          </el-button>
          <el-dropdown v-if="st.alive" @command="(c) => c === 'app' ? openWebApp() : openWebBrowser()">
            <el-button type="primary" plain>
              <el-icon class="mr-1"><Link /></el-icon>打开 WEB 窗口<el-icon class="ml-1"><ArrowDown /></el-icon>
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="app"><el-icon><FullScreen /></el-icon>应用内弹窗窗口</el-dropdown-item>
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
              <div class="text-sm">队列任务</div>
              <div class="text-xs" style="color:var(--mx-text3)">清空等待中的任务(不影响运行中)</div>
            </div>
            <el-button size="small" :disabled="!st.pending" @click="act('queue-clear')">清空队列</el-button>
          </div>
          <el-divider style="margin:6px 0" />
          <div class="flex items-center gap-3">
            <div class="flex-1">
              <div class="text-sm">中断当前任务</div>
              <div class="text-xs" style="color:var(--mx-text3)">中止正在执行的渲染(可重试)</div>
            </div>
            <el-button size="small" type="warning" plain :disabled="!st.running" @click="act('interrupt','中断当前执行的任务?')">中断</el-button>
          </div>
          <el-divider style="margin:6px 0" />
          <div class="flex items-center gap-3">
            <div class="flex-1">
              <div class="text-sm">释放显存</div>
              <div class="text-xs" style="color:var(--mx-text3)">卸载已加载模型(/free,长任务前清理)</div>
            </div>
            <el-button size="small" :disabled="!st.alive" @click="act('free')">释放</el-button>
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
          <div class="text-xs mt-2 p-3 rounded-lg" style="background:rgba(83,216,255,.05);color:var(--mx-text2)">
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
             :style="{ background: m.ok ? 'rgba(61,220,132,.05)' : (m.name ? 'rgba(255,107,107,.06)' : 'transparent'), border: '1px solid var(--mx-line)' }">
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
      <div class="text-xs mt-3" style="color:var(--mx-text3)">配置在「设置 → ComfyUI」修改;未命中会导致渲染提交 400(模型名不在列表)。</div>
    </div>

    <!-- 版本管理器:本体 / 插件 / 模型 VS 在线最新 -->
    <div class="panel p-5 mt-5" v-if="vers">
      <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2 flex-wrap">
        <el-icon color="var(--mx-purple)"><Stamp /></el-icon>版本管理器
        <span class="text-xs font-normal" style="color:var(--mx-text3)">本地版本 VS 在线最新(GitHub;联网检查较慢请稍候)</span>
        <div class="flex-1"></div>
        <el-button size="small" :loading="verLoading" circle @click="refreshVersions(false)"><el-icon><Refresh /></el-icon></el-button>
        <el-button size="small" type="primary" plain :loading="checking" @click="refreshVersions(true)">
          <el-icon class="mr-1"><Cloudy /></el-icon>检查更新
        </el-button>
      </h3>

      <el-table :data="[{ ...vers.core?.[0], _kind: '本体' }, ...(vers.nodes||[]).map(n => ({ ...n, _kind: '插件' }))]"
        size="small" style="--el-table-border-color: var(--mx-line); width: 100%">
        <el-table-column label="类型" width="64">
          <template #default="{ row }">
            <span class="timecode">{{ row._kind }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="name" label="名称" min-width="210" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="text-xs">{{ row.name }}</div>
            <div class="text-xs" style="color:var(--mx-text3)" v-if="row.detail">{{ row.detail }}</div>
          </template>
        </el-table-column>
        <el-table-column prop="local" label="本地版本" width="130">
          <template #default="{ row }">
            <span class="font-mono text-xs">{{ row.local || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="remote" label="在线最新" width="130">
          <template #default="{ row }">
            <span class="font-mono text-xs" :style="{ color: row.status === 'behind' ? 'var(--mx-amber)' : 'var(--mx-text2)' }">{{ row.remote || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <span class="ver-badge" :class="STATUS_META[row.status]?.cls">
              <i class="dot" :class="STATUS_META[row.status]?.cls || ''" v-if="row.status !== 'unknown'"></i>{{ STATUS_META[row.status]?.label || row.status }}
            </span>
          </template>
        </el-table-column>
        <el-table-column prop="repo" label="仓库" width="200" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="text-xs" style="color:var(--mx-text3)">{{ row.repo || '—' }}</span>
          </template>
        </el-table-column>
      </el-table>

      <div class="text-xs mt-3 flex items-center gap-1.5" style="color:var(--mx-text3)">
        <el-icon :size="13"><InfoFilled /></el-icon>
        模型文件无统一在线版本源,按本地文件(大小/日期)管理,见上方「模型清单核对」;更新模型请在设置中更换文件。
      </div>
    </div>

    <!-- 应用内 WEB 窗口 -->
    <el-dialog v-model="webOpen" fullscreen :show-close="false" class="webwin"
      style="background: var(--mx-bg1)" destroy-on-close>
      <template #header>
        <div class="flex items-center gap-3 w-full" style="padding-right: 12px">
          <el-icon color="var(--mx-amber)"><Platform /></el-icon>
          <span class="text-sm font-bold">ComfyUI WEB 工作台</span>
          <span class="timecode">{{ webURL }}</span>
          <div class="flex-1"></div>
          <el-button size="small" text @click="openWebBrowser()">在系统浏览器打开</el-button>
          <el-button size="small" type="primary" @click="webOpen = false">关闭弹窗</el-button>
        </div>
      </template>
      <iframe :src="webURL" style="width:100%; height: calc(100vh - 110px); border: none; background:#111"
        v-if="webOpen" referrerpolicy="no-referrer"></iframe>
    </el-dialog>
  </div>
</template>
