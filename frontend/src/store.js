import { reactive } from 'vue'

// ---------- 全局状态 ----------
export const G = reactive({
  view: 'home',            // home / project / settings
  status: null,            // /api/status
  settings: null,          // /api/settings
  projects: [],
  project: null,           // 当前项目详情 {project, plan, assets, manifest, source, qc}
  logs: [],                // SSE 事件
  eventsOpen: false,
  paramsDrawer: null,      // 打开调优的阶段 id
  stageDetail: null,        // 打开详情管理的阶段 id
  promptShot: null,        // 查看 H3 提示词的镜头
  viewer: null,             // 全屏媒体预览 {items:[{type,src,title,meta,foot,kindLabel}], index}
  tuneShot: null,           // 单镜画布微调的镜头对象
  logPanelOpen: false,
  logSeq: 0,                // 日志轮询游标(seq 去重)
  tick: { library: 0, outputs: 0, comfy: 0, settings: 0 }, // 顶栏「刷新」触发各视图自刷新
})

export const PHASES = [
  { id: 'script',   name: '剧本分镜', icon: 'EditPen',      desc: 'LLM 直出角色/场景/分镜与完整 H3 提示词' },
  { id: 'assets',   name: '资产定妆', icon: 'PictureFilled', desc: '角色定妆照与场景参考图(上传/盘点)' },
  { id: 'render',   name: '渲染出片', icon: 'VideoPlay',    desc: '本地 ComfyUI / MiniMax 云端 API 双引擎' },
  { id: 'qc',       name: '质检',     icon: 'CircleCheck',  desc: '时长/音轨/亮度机械质检(黑屏防线)' },
  { id: 'assemble', name: '合成成片', icon: 'Film',         desc: '拼接+字幕+响度归一+faststart' },
]

// ---------- API ----------
async function req(method, url, body) {
  const opt = { method, headers: {} }
  if (body !== undefined) {
    opt.headers['Content-Type'] = 'application/json'
    opt.body = JSON.stringify(body)
  }
  const r = await fetch(url, opt)
  const data = await r.json().catch(() => ({}))
  if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`)
  return data
}

export const api = {
  status: () => req('GET', '/api/status'),
  getSettings: () => req('GET', '/api/settings'),
  putSettings: (s) => req('PUT', '/api/settings', s),
  projects: () => req('GET', '/api/projects'),
  createProject: (name) => req('POST', '/api/projects', { name }),
  project: (id) => req('GET', `/api/projects/${encodeURIComponent(id)}`),
  deleteProject: (id) => req('DELETE', `/api/projects/${encodeURIComponent(id)}`),
  saveSource: (id, text) => req('PUT', `/api/projects/${encodeURIComponent(id)}/source`, { text }),
  putParams: (id, params) => req('PUT', `/api/projects/${encodeURIComponent(id)}/params`, params),
  run: (id, phase, only, engine, seed) => req('POST', `/api/projects/${encodeURIComponent(id)}/run`, { phase, only, engine, seed }),
  stop: (id) => req('POST', `/api/projects/${encodeURIComponent(id)}/stop`),
  upgrade2k: (id, shot) => req('POST', `/api/projects/${encodeURIComponent(id)}/upgrade2k`, { shot }),
  regenAsset: (id, assetId) => req('POST', `/api/projects/${encodeURIComponent(id)}/assets/regen`, { asset_id: assetId }),
  importPlan: (id, plan) => req('POST', `/api/projects/${encodeURIComponent(id)}/import-plan`, { plan }),
  composeStory: (id, prompt, styleHint) => req('POST', `/api/projects/${encodeURIComponent(id)}/compose-story`, { prompt, style_hint: styleHint }),
  uploadAsset: (id, kind, assetId, name, file) => {
    const fd = new FormData()
    fd.append('kind', kind); fd.append('asset_id', assetId)
    fd.append('name', name); fd.append('file', file)
    return fetch(`/api/projects/${encodeURIComponent(id)}/assets`, { method: 'POST', body: fd })
      .then(async r => { const d = await r.json().catch(() => ({})); if (!r.ok) throw new Error(d.error || `HTTP ${r.status}`); return d })
  },
  deleteAsset: (id, assetId) => req('DELETE', `/api/projects/${encodeURIComponent(id)}/assets/${encodeURIComponent(assetId)}`),
  openExplorer: (path, project) => req('POST', '/api/open', { path, project }),
  fileURL: (id, rel) => `/api/projects/${encodeURIComponent(id)}/files/${rel}`,
  sysmon: () => req('GET', '/api/sysmon'),
  window: (action) => req('POST', `/api/window/${encodeURIComponent(action)}`),
  quit: () => req('POST', '/api/quit'),
}

// ---------- 实时日志:SSE(浏览器) / 轮询(桌面 webview) ----------
// Wails webview 的自定义协议对 SSE 流式存在缓冲,桌面内改用 2s 增量轮询
// (seq 去重),彻底解决「日志不显示」;浏览器访问仍走 SSE。
export function connectEvents() {
  if (window.runtime?.EventsOn || window.runtime?.WindowShow) {
    startLogPolling()
    return
  }
  if (G.eventsOpen) return
  const es = new EventSource('/api/events')
  G.eventsOpen = true
  es.addEventListener('log', (e) => {
    try {
      const ev = JSON.parse(e.data)
      pushEvent(ev)
    } catch { /* ignore */ }
  })
  es.onerror = () => {
    G.eventsOpen = false
    setTimeout(connectEvents, 3000)
  }
}

function pushEvent(ev) {
  if (ev.seq && ev.seq <= G.logSeq) return // 轮询去重
  if (ev.seq) G.logSeq = ev.seq
  G.logs.push(ev)
  if (G.logs.length > 800) G.logs.splice(0, G.logs.length - 800)
  G.eventsOpen = true
  if (ev.project_id && G.project && ev.project_id === G.project.project.id) {
    // 管线级事件(success/error/warn,如「⏹ 已停止」「✔ 完成」)→ 立即刷新项目状态
    if (ev.phase === 'pipeline' && ['success', 'error', 'warn'].includes(ev.level)) {
      refreshProject()
      refreshStatus()
    }
  }
}

let pollTimer = null
function startLogPolling() {
  if (pollTimer) return
  G.eventsOpen = true
  const poll = async () => {
    try {
      const r = await fetch(`/api/logs?after=${G.logSeq || 0}`)
      if (!r.ok) return
      const d = await r.json()
      for (const ev of (d.events || [])) pushEvent(ev)
    } catch { /* 服务重启中 */ }
  }
  poll()
  pollTimer = setInterval(poll, 2000)
}

export async function refreshStatus() {
  try { G.status = await api.status() }
  catch {
    // 瞬时失败(隐藏/唤回/服务预热):2.5s 后补拉一次,避免底栏版本/环境灯长时间空白
    setTimeout(async () => {
      try { G.status = await api.status() } catch { /* 服务重启中 */ }
    }, 2500)
  }
}
export async function refreshProjects() {
  try { G.projects = await api.projects() } catch { }
}
export async function refreshProject() {
  if (!G.project) return
  try { G.project = await api.project(G.project.project.id) } catch { }
}

export function fmtTime(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
