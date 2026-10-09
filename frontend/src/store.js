import { reactive, computed } from 'vue'

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
  tick: { library: 0, outputs: 0, comfy: 0, settings: 0, novels: 0 }, // 顶栏「刷新」触发各视图自刷新
  metaStyles: [],          // /api/meta 的风格清单(key+name);空=用内置兜底
  metaPhases: [],          // /api/meta 的阶段清单
  metaEntityKinds: [],     // /api/meta 的实体类型清单(key+name+hint);角色卡的渲染方法选择器用
  metaUpscale: {},         // /api/meta 的放大默认值 {default_type,default_model,default_ltx_vae}
                           // 权重文件名的事实来源在后端(它决定 ComfyUI 模型目录里该有什么),
                           // 前端只回显 —— 不在这里再写一份文件名字面量。
  metaSources: [],         // /api/meta 的资产来源词汇表 {key,name}
                           // 同一类分叉的另一例:来源由后端写入,中文名也必须由后端给 ——
                           // 前端原先在各处三元猜,只认三种值,Q 版定妆/视频截帧/抽卡
                           // 全被显示成"上传"(2026-09-24 用户报「Krea-2 字样写死了」)。
})

// 阶段清单的**兜底**副本(后端 /api/meta 是唯一真源)。
//
// 为什么还要留一份:后端未就绪时(启动瞬间/接口异常)流水线栏不能空着。
// 但真源必须只有一个 —— 见 phases():只要 metaPhases 有值就用后端的,
// 前端这份只提供"图标/描述"这类纯展示信息,阶段 id 与名称以后端为准。
// 后端加了新阶段而这里没跟上时,用户的界面照旧能看到它(而不是"没这个阶段")。
export const PHASES = [
  { id: 'script',   name: '剧本分镜', icon: 'EditPen',      desc: 'LLM 直出角色/场景/分镜与完整 H3 提示词' },
  { id: 'assets',   name: '资产定妆', icon: 'PictureFilled', desc: '角色定妆照与场景参考图(上传/盘点)' },
  { id: 'render',   name: '渲染出片', icon: 'VideoPlay',    desc: '本地 ComfyUI / MiniMax 云端 API 双引擎' },
  { id: 'qc',       name: '质检',     icon: 'CircleCheck',  desc: '时长/音轨/亮度机械质检(黑屏防线)' },
  { id: 'assemble', name: '合成成片', icon: 'Film',         desc: '拼接+字幕+响度归一+faststart' },
]

// phases 当前生效的阶段清单:后端优先,前端兜底补齐展示字段。
export function phases() {
  const meta = Array.isArray(G.metaPhases) ? G.metaPhases : []
  if (!meta.length) return PHASES
  return meta.map(p => {
    const local = PHASES.find(x => x.id === p.id) || {}
    return {
      id: p.id,
      name: p.name || local.name || p.id,
      icon: local.icon || 'Document',
      desc: p.desc || local.desc || '',
    }
  })
}

// sourceLabel 资产来源的中文名:**唯一实现点**。
//
// 后端写入 source,也由后端给中文名(/api/meta 的 asset_sources)。前端不再自己
// 猜 —— 原先七处三元只认三种值,Q 版定妆(krea2-chibi)、视频截帧(video-frame)、
// 抽卡(gacha)全被显示成"上传",显示的内容与事实不符。
//
// 兜底表只在 meta 未就绪时用;认不出的 key **原样返回**,让人看出"这是另一种来源",
// 而不是统一说成"上传"。
//
// 这张表是**后端的副本**,所以两件事必须一起改:换图像模型后新增的来源 key
// (krea2 → qwen-image-2.1)在这里补一份,顺序/取值与 project/source_label.go 对齐。
// 有闸门盯着(server 的 TestFrontendSourceFallbackMatchesBackend):前端表里出现
// 后端词汇表没有的 key 就变红。
const FALLBACK_SOURCE_NAMES = {
  'qwen-image-2.1': 'Qwen-Image-2.1', 'qwen-image-2.1-chibi': 'Qwen-Image-2.1 · Q版',
  'z-image': 'Z-Image Turbo', 'z-image-chibi': 'Z-Image Turbo · Q版',
  'qwen-image': 'Qwen-Image', 'qwen-image-chibi': 'Qwen-Image · Q版',
  krea2: 'Krea-2', 'krea2-chibi': 'Krea-2 · Q版',
  library: '库复用', upload: '上传', 'upload-whitebg': '上传 · 白底化',
  'video-frame': '视频截帧', gacha: '抽卡',
}
export function sourceLabel(src) {
  if (!src) return '未知'
  const hit = (G.metaSources || []).find(s => s.key === src)
  if (hit) return hit.name
  return FALLBACK_SOURCE_NAMES[src] || src
}

// sourceTone 标签配色的语义分类(纯展示):库复用=绿、本地生成=橙、其余=灰。
//
// **按"是不是生成出来的"分类,不列具体模型 key** —— 2026-09-25 图像模型从 Krea-2
// 换到 Qwen-Image-2.1 时,这里原来写死的 krea2/krea2-chibi 立刻失效,新资产全变成灰的。
// 家族 key 会一直增加,分类规则不会。
export function sourceTone(src) {
  if (src === 'library') return 'success'
  if (src === 'video-frame' || src === 'upload' || src === 'upload-whitebg') return 'info'
  return 'warning'
}

// phase 显示名:all/upgrade2k 等非阶段值映射为人话,具体阶段用中文名(后端清单优先)
export function phaseLabel(p) {
  if (!p) return ''
  const m = { all: '全流程', upgrade2k: '2K 精修', compose: 'AI 创作', pipeline: '管线', novel: '小说产线', system: '系统', comfy: '引擎' }
  return m[p] || phases().find(x => x.id === p)?.name || p
}

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
  // 导入项目包目录（单集包或整个系列根）—— 技能端产出的包还在别处时由平台主动拉进来，
  // 用户不必先手动拷进 data/projects。
  importPackage: (path) => req('POST', '/api/projects/import-package', { path }),
  createProject: (name) => req('POST', '/api/projects', { name }),
  project: (id) => req('GET', `/api/projects/${encodeURIComponent(id)}`),
  deleteProject: (id) => req('DELETE', `/api/projects/${encodeURIComponent(id)}`),
  saveSource: (id, text) => req('PUT', `/api/projects/${encodeURIComponent(id)}/source`, { text }),
  putParams: (id, params) => req('PUT', `/api/projects/${encodeURIComponent(id)}/params`, params),
  run: (id, phase, only, engine, seed) => req('POST', `/api/projects/${encodeURIComponent(id)}/run`, { phase, only, engine, seed }),
  stop: (id) => req('POST', `/api/projects/${encodeURIComponent(id)}/stop`),
  comfyFree: () => req('POST', '/api/comfy/free'),
  upgrade2k: (id, shot) => req('POST', `/api/projects/${encodeURIComponent(id)}/upgrade2k`, { shot }),
  regenAsset: (id, assetId) => req('POST', `/api/projects/${encodeURIComponent(id)}/assets/regen`, { asset_id: assetId }),
  // 视频截帧定妆:此前 api 层漏了这两个方法(后端路由与后端实现一直都在),
  // ProjectView 调用即抛 TypeError,又被 catch 吞成一句英文报错 ——
  // 整条「从已渲染镜头截帧替换定妆」的链路实际不可用。
  assetFramePreview: (id, assetId, shot) =>
    req('POST', `/api/projects/${encodeURIComponent(id)}/assets/${encodeURIComponent(assetId)}/frame-preview`, { shot_id: shot }),
  assetFrameApply: (id, assetId, shot, ratio) =>
    req('POST', `/api/projects/${encodeURIComponent(id)}/assets/${encodeURIComponent(assetId)}/frame-apply`, { shot_id: shot, ratio }),
  importPlan: (id, plan) => req('POST', `/api/projects/${encodeURIComponent(id)}/import-plan`, { plan }),
  // 角色卡编辑(目前用于改**实体类型**:人/物/灵宠/妖兽/无实体/Q版 —— 决定走哪套渲染方法)
  putCharacter: (id, payload) => req('PUT', `/api/projects/${encodeURIComponent(id)}/character`, payload),
  // styles/styleHint：导入制作时选的渲染风格（预设 key 多选 + 自由文本）。
  // 后端据此拼出**权威风格串**写进每集输入的「全局风格:」行；空数组 = 沿用原书/立项的风格串。
  novelProduce: (path, max, styles, styleHint) => req('POST', '/api/novels/produce', {
    path, max_episodes: max, styles: styles || [], style_hint: styleHint || '',
  }),
  novelProduceScript: (path) => req('POST', '/api/novels/produce-script', { path }),
  // 封面重做(可配置):config 取初始值 → redo 出 N 张**候选** → apply 挑定后才落盘
  novelCoverConfig: (path) => req('GET', '/api/novels/cover-config?path=' + encodeURIComponent(path)),
  novelCoverRedo: (body) => req('POST', '/api/novels/cover-redo', body),
  novelCoverApply: (path, file) => req('POST', '/api/novels/cover-apply', { path, file }),
  novelSplit: (name, text, max) => req('POST', '/api/novel-split', { name, text, max_episodes: max }),
  novelBatchStart: (prefix) => req('POST', '/api/novel-batch', { prefix }),
  novelBatchStop: () => req('DELETE', '/api/novel-batch'),
  novelBatchStatus: () => req('GET', '/api/novel-batch'),
  composeStory: (id, prompt, styleHint) => req('POST', `/api/projects/${encodeURIComponent(id)}/compose-story`, { prompt, style_hint: styleHint }),
  uploadAsset: (id, kind, assetId, name, file) => {
    // 桌面 webview 经 wails 代理发 multipart 会把大文件截成 0 字节
    // (2026-09-09 实测),统一改 JSON + base64;浏览器模式同样可用。
    return fileToB64(file).then(b64 =>
      req('POST', `/api/projects/${encodeURIComponent(id)}/assets`, {
        kind, asset_id: assetId, name,
        image_b64: b64, filename: file.name,
      }))
  },
  deleteAsset: (id, assetId) => req('DELETE', `/api/projects/${encodeURIComponent(id)}/assets/${encodeURIComponent(assetId)}`),
  openExplorer: (path, project) => req('POST', '/api/open', { path, project }),
  fileURL: (id, rel) => `/api/projects/${encodeURIComponent(id)}/files/${rel}`,
  // 单镜云端 2K 产物(乙7):**独立导出文件** —— 不进镜头清单、不参与合成,
  // 成片里这一镜仍是 768p。落盘在 <项目>/clips/2k/shot_NNN_2k.mp4,由后端列出、
  // 按文件名取回(取回的名字后端用白名单校验,前端只负责拼 URL)。
  // 为什么不用通配的 fileURL:那条路由面向"项目内任意文件",而这里是**允许被
  // 用户点开/下载**的一类文件,形状由后端钉死才安全。
  outputs2k: (id) => req('GET', `/api/projects/${encodeURIComponent(id)}/outputs/2k`),
  output2kURL: (id, name) =>
    `/api/projects/${encodeURIComponent(id)}/outputs/2k/${encodeURIComponent(name)}`,
  sysmon: () => req('GET', '/api/sysmon'),
  window: (action) => req('POST', `/api/window/${encodeURIComponent(action)}`),
  quit: () => req('POST', '/api/quit'),

  // ── 以下为原先散落在各视图里的裸 fetch,统一收口到 api 层 ──
  // 收口的意义:错误处理、URL 编码、请求头三件事只写一遍;
  // 之前每处各写各的(有的查 r.ok、有的只 .then(r=>r.json())、有的静默 catch)。
  comfyAdmin: () => req('GET', '/api/comfy/admin'),
  comfyVersions: (online) => req('GET', `/api/comfy/versions${online ? '?online=1' : ''}`),
  // Sage 加速:状态查询 / 一键安装(按本机 torch+CUDA 匹配官方推荐发布源的轮子)
  comfySage: () => req('GET', '/api/comfy/sage'),
  comfySageInstall: () => req('POST', '/api/comfy/sage-install'),
  // 引擎动作入口(后端路由 POST /api/comfy/{action},支持 start/stop/queue-clear/interrupt/free)。
  // 此前 api 层漏了 comfyAction,首页与「引擎管理」页的启停按钮一调用就抛
  // TypeError: api.comfyAction is not a function,又被 catch 吞成含糊报错 —— 引擎启停实际不可用。
  comfyAction: (action) => req('POST', `/api/comfy/${encodeURIComponent(action)}`),
  comfyOpenAppWin: () => req('POST', '/api/comfy/open-appwin'),
  comfyOpenWeb: () => req('POST', '/api/comfy/open-web'),
  browse: (path) => req('GET', '/api/browse?path=' + encodeURIComponent(path)),
  cleanup: () => req('POST', '/api/cleanup'),
  openUrl: (url) => req('POST', '/api/open-url', { url }),
  library: () => req('GET', '/api/library'),
  libraryDetail: (kind, name) => req('GET', `/api/library/${encodeURIComponent(kind)}/${encodeURIComponent(name)}`),
  libraryRegen: (kind, name) => req('POST', `/api/library/${encodeURIComponent(kind)}/${encodeURIComponent(name)}/regen`),
  // 补全多视角(定妆照 → H3 原地转身 → 转台图;每个角色数分钟,后端一次性返回)
  libraryViews: (kind, name) => req('POST', `/api/library/${encodeURIComponent(kind)}/${encodeURIComponent(name)}/views`),
  // AI 生成中文描述与英文提示词(配了 VLM 就让它看图写,否则退回文本模型)
  libraryDescribe: (kind, name) => req('POST', `/api/library/${encodeURIComponent(kind)}/${encodeURIComponent(name)}/describe`),
  librarySetMeta: (kind, name, payload) => req('PUT', `/api/library/${encodeURIComponent(kind)}/${encodeURIComponent(name)}`, payload),
  libraryDelete: (kind, name) => req('DELETE', `/api/library/${encodeURIComponent(kind)}/${encodeURIComponent(name)}`),
  libraryGacha: () => req('POST', '/api/library/gacha'),
  // 库资产创建:按分类走同一条路(characters 走完整定妆流程,props/scenes 上传图入库)
  libraryCreateAsset: (kind, payload) => req('POST', `/api/library/${encodeURIComponent(kind)}`, payload),
  putShot: (id, shot) => req('PUT', `/api/projects/${encodeURIComponent(id)}/shot`, shot),
  deleteShot: (id, shotID) => req('DELETE', `/api/projects/${encodeURIComponent(id)}/shot/${shotID}`),
  // 局部重绘：target＝要重绘的**区域**（英文名词短语，SAM3 的词表）；
  // prompt＝**修正后的画面描述**（可选）——不传就沿用原提示词，于是只能限区域、改不了内容。
  maskFix: (id, shotID, target, prompt, objectIndex) =>
    req('POST', `/api/projects/${encodeURIComponent(id)}/shot/${shotID}/mask-fix`,
      { target, prompt: prompt || '', object_index: objectIndex || '' }),
  audioDrive: (id, shotID, audioPath) => req('POST', `/api/projects/${encodeURIComponent(id)}/shot/${shotID}/audio-drive`, { audio_path: audioPath }),
  // 2× 放大后处理:传 opts({type, model, ltx_vae}) 覆盖默认;不传则后端用 esrgan + 4x-UltraSharp。
  // 结果作为独立文件产出,原镜头不动,音轨沿用源片。
  upscale: (id, shotID, opts) => req('POST', `/api/projects/${encodeURIComponent(id)}/shot/${shotID}/upscale`, opts || {}),
  uploadCover: (id, payload) => req('POST', `/api/projects/${encodeURIComponent(id)}/cover`, payload),
  exportPack: (id) => req('POST', `/api/projects/${encodeURIComponent(id)}/export-pack`),
  // what 必须发：后端只认 body 里的 final/final2k/all，不带 body 会 400
  // （2026-09-30 审计：此前一个字节都不发 ⇒ 作品库的「删除成片」与批量删除**从来没成功过**）。
  deleteOutput: (projectID, what) => req('DELETE', `/api/projects/${encodeURIComponent(projectID)}/output`, { what: what || 'all' }),
  novelJobs: () => req('GET', '/api/novel-jobs'),
  novelJobDelete: (series) => req('DELETE', '/api/novel-jobs?series=' + encodeURIComponent(series)),
  novels: (withHidden) => req('GET', '/api/novels' + (withHidden ? '?hidden=1' : '')),
  novelHidden: (path, hidden) => req('POST', '/api/novels/hidden', { path, hidden }),
  novelDetail: (path) => req('GET', '/api/novels/detail?path=' + encodeURIComponent(path)),
  // 剧本脚本/角色/场景(读技能端已产出的脚本包;episode 给出时带这一集的镜头明细)
  novelScript: (path, episode) => req('GET', '/api/novels/script?path=' + encodeURIComponent(path) +
    (episode ? '&episode=' + encodeURIComponent(episode) : '')),
  novelRead: (path) => req('GET', '/api/novels/read?path=' + encodeURIComponent(path)),
  novelDelete: (path) => req('DELETE', '/api/novels?path=' + encodeURIComponent(path)),
  openPath: (payload) => req('POST', '/api/open', payload),
  outputs: () => req('GET', '/api/outputs'),
  testConn: (target) => req('POST', '/api/test', { target }),
}

// ---------- 后端元信息(阶段 / 风格清单的单一真源) ----------
// 有哪些阶段与哪些风格由后端决定;前端只保留颜色、描述这类纯展示信息。
// 拉取失败时静默回退内置清单 —— 后端未就绪不该让参数抽屉不可用。
export async function loadMeta() {
  try {
    const m = await req('GET', '/api/meta')
    if (Array.isArray(m?.styles)) G.metaStyles = m.styles
    if (Array.isArray(m?.phases)) G.metaPhases = m.phases
    // 实体类型清单(角色卡渲染方法):同样以后端为准,前端不硬编 key
    if (Array.isArray(m?.entity_kinds)) G.metaEntityKinds = m.entity_kinds
  if (Array.isArray(m?.asset_sources)) G.metaSources = m.asset_sources
    if (m?.upscale && typeof m.upscale === 'object') G.metaUpscale = m.upscale
  } catch { /* 用内置兜底 */ }
}

// ---------- 图片上传载荷(wails 管对 multipart 有截断,改 JSON+base64) ----------
// 超大图先在画布压缩(最长边 ≤1600,JPEG q0.92),避免几 MB 照片撑爆
// JSON 请求体;小图/透明 PNG 保持原样。
export function fileToB64(file) {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onerror = () => reject(new Error('图片读取失败,请改用「点击选择文件」重新选择'))
    r.onload = () => {
      const dataURL = String(r.result || '')
      const img = new Image()
      img.onerror = () => resolve(dataURL) // 解不出尺寸就原样上,后端兜底
      img.onload = () => {
        try {
          const MAX = 1600
          if (img.width <= MAX && img.height <= MAX && (dataURL.length < 1.5 * 1024 * 1024)) return resolve(dataURL)
          const scale = Math.min(1, MAX / Math.max(img.width, img.height))
          const cv = document.createElement('canvas')
          cv.width = Math.round(img.width * scale)
          cv.height = Math.round(img.height * scale)
          cv.getContext('2d').drawImage(img, 0, 0, cv.width, cv.height)
          resolve(cv.toDataURL('image/jpeg', 0.92))
        } catch { resolve(dataURL) }
      }
      img.src = dataURL
    }
    r.readAsDataURL(file)
  })
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
    // 必须先 close:EventSource 自身在断开后会自动重连,这里再手动新建一个,
    // 两条重连路径就会叠加 —— 每 error 一次多一条连接,长跑会话连接数持续累积。
    es.close()
    G.eventsOpen = false
    setTimeout(connectEvents, 3000)
  }
}

function pushEvent(ev) {
  if (ev.seq && ev.seq <= G.logSeq) return // 轮询去重
  // 序号跳号 = 中间的事件**确实丢了**(慢订阅者丢帧,或环形历史 500 条已滚过)。
  // 旧实现对此毫无察觉:日志面板看起来是连续的,用户以为"就这些日志"。
  // 不假装没发生 —— 插一条醒目的缺口提示(游标还没建立时不报,避免首帧误判)。
  if (ev.seq && G.logSeq > 0 && ev.seq > G.logSeq + 1) {
    G.logs.push({
      seq: G.logSeq, phase: 'system', level: 'warn', progress: ev.progress || 0,
      message: `⚠ 有 ${ev.seq - G.logSeq - 1} 条日志未送达(已被丢弃或滚出缓冲区);`
        + '完整记录见 data/logs/manjux.log',
      at: ev.at,
    })
  }
  if (ev.seq) G.logSeq = ev.seq
  G.logs.push(ev)
  if (G.logs.length > 800) G.logs.splice(0, G.logs.length - 800)
  G.eventsOpen = true
  if (ev.phase === 'comfy') {
    // 引擎启停事件(手动启停/退出):底栏灯、首页 chip、管理页即时同步,不等 15s 轮询
    refreshStatus()
    G.tick.comfy++
  }
  if (ev.phase === 'novel' && ['success', 'error', 'warn'].includes(ev.level)) {
    refreshProjects() // 拆集建项目/批量进度 → 首页实时出现分集
  }
  if (ev.project_id && G.project && ev.project_id === G.project.project.id) {
    // 当前项目的 success/error/warn(阶段完成/单镜完成/失败/停止)→ 立即刷新详情;
    // 单阶段运行不发 pipeline 级结束事件,阶段级 success 就是运行结束信号
    if (['success', 'error', 'warn'].includes(ev.level)) {
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


// 日志状态灯(全局唯一逻辑,ticker 与日志面板共用):
// 管线运行中=绿;停止/中断(warn)=橙;故障(error)=红;待命=暗
export const logDot = computed(() => {
  const running = G.status?.running && Object.keys(G.status.running).length > 0
  if (running) return 'ok'
  const last = G.logs.length ? G.logs[G.logs.length - 1] : null
  if (last?.level === 'error') return 'bad'
  if (last?.level === 'warn') return 'warn'
  return 'idle'
})
