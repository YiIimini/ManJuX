<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { G } from '../store'
import { ElMessage, ElMessageBox } from 'element-plus'

const st = ref(null)
const loading = ref(false)
const acting = ref('')

// 版本管理器
const vers = ref(null)
const verLoading = ref(false)
const checking = ref(false)


async function refresh() {
  loading.value = true
  try { st.value = await fetch('/api/comfy/admin').then(r => r.json()) }
  catch (e) { ElMessage.error('状态获取失败') }
  loading.value = false
}
async function refreshVersions(online = false, manual = false) {
  if (online) checking.value = true
  else verLoading.value = true
  try {
    const r = await fetch(`/api/comfy/versions${online ? '?online=1' : ''}`)
    vers.value = await r.json()
    if (!r.ok) throw new Error(vers.value?.error || `HTTP ${r.status}`)
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
  refreshVersions(true) // 挂载即自动联网对比(有 10 分钟缓存,不重复查)
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
    const r = await fetch(`/api/comfy/${action}`, { method: 'POST' })
    const d = await r.json()
    if (!r.ok) throw new Error(d.error)
    ElMessage.success(d.note || '已执行')
    setTimeout(refresh, action === 'start' ? 1500 : 400)
  } catch (e) { ElMessage.error(e.message) } finally { acting.value = '' }
}

// WEB 窗口:独立 App 窗口(Chrome/Edge --app 无地址栏桌面观感)/ 系统默认浏览器
async function openWebApp() {
  try {
    const r = await fetch('/api/comfy/open-appwin', { method: 'POST' })
    const d = await r.json()
    if (!r.ok) throw new Error(d.error)
    ElMessage.success('已打开 ComfyUI 独立窗口')
  } catch (e) { ElMessage.error(e.message) }
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
              <div class="text-sm">队列任务</div>
              <div class="text-xs" style="color:var(--mx-text3)">清空等待中的任务(不影响运行中)</div>
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
            <div class="text-xs">{{ row.name }}</div>
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
  </div>
</template>
