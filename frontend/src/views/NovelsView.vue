<!-- 小说库:扫描设置目录,书卡 + 弹窗(章节文件管理 + 正文阅读) + 一键视频创作 -->
<script setup>
import { G, api, refreshProjects } from '../store'
import { ref, computed, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox, ElLoading } from 'element-plus'
import ChapterTree from '../components/ChapterTree.vue'
import CoverCompare from '../components/CoverCompare.vue'

const root = ref('')
const hint = ref('')
const novels = ref([])
const loading = ref(false)
// 隐藏作品(界面状态,存 sidecar;见后端 novels_hidden.go):默认不列出,这里给一个开关把它请回来
const withHidden = ref(false)
const hiddenCount = ref(0)
// 封面地址带一个时间戳:重做封面后文件路径不变,不加它浏览器会一直显示缓存里的旧封面
const coverBust = ref(Date.now())

async function refresh() {
  loading.value = true
  try {
    const d = await api.novels(withHidden.value)
    root.value = d.root || ''
    hint.value = d.hint || ''
    novels.value = d.novels || []
    hiddenCount.value = d.hidden_count || 0
  } catch (e) { ElMessage.error(e.message) }
  loading.value = false
}
onMounted(refresh)
watch(() => G.tick.novels, refresh)

// 弹窗:阅读详情(正文主区 + 右侧目录树导航栏,均可折叠)
const dlg = ref(null)       // {novel, chapters}
const reading = ref(null)   // {name, text, truncated, chars}
const readBusy = ref(false)
const navCollapsed = ref(false)          // 右侧目录栏收起
// 详情弹窗的分页:正文 / 剧本脚本 / 角色 / 场景(用户 2026-09-26:
// 「剧本脚本、角色、场景相关内容也需要可查看详情内容」)
const dlgTab = ref('read')
const script = ref(null)        // /api/novels/script 的汇总(集清单 + 角色 + 场景)
const scriptShots = ref([])     // 当前选中集的镜头明细
const scriptEp = ref('')
const scriptBusy = ref(false)

// voCastText 画外演出镜的"演出者"一行摘要（技能侧 2026-09-27 口径）。
//
// 名字在**本地**查（script 里已经有角色/道具清单），不在后端再拼一遍 ——
// 同一条数据两处各拼一次必然走散（"资产 id 的三种叫法"就是这么来的）。
function voCastText(s) {
  const cname = (id) => (script.value?.characters || []).find((c) => c.id === id)?.name || id
  const pname = (id) => (script.value?.props || []).find((p) => p.id === id)?.name || id
  if (s?.vo_kind === 'inner') {
    const ids = s.chibi_ids || []
    return ids.length ? ids.map(cname).join('、') : '（chibi_ids 为空 —— 这一镜没有 Q 版形象可选，页签会标出来）'
  }
  if (s?.vo_kind === 'system') {
    const a = s.system_assets || {}
    return `${cname(a.bot || '')} ＋ ${pname(a.panel || '')}`
  }
  return ''
}
const expandedDirs = ref(new Set())      // 展开的目录节点 key
const chapTree = ref([])
// 中文数字段→数值(卷二→2,卷二十三→23);非中文数字返回 null
function cnNumVal(str) {
  if (!/^[零一两二三四五六七八九十百]+$/.test(str)) return null
  const D = { 零: 0, 一: 1, 两: 2, 二: 2, 三: 3, 四: 4, 五: 5, 六: 6, 七: 7, 八: 8, 九: 9 }
  let tot = 0, cur = 0
  for (const c of str) {
    if (c in D) { cur = cur * 10 + D[c] }
    else { tot += (cur || 1) * (c === '十' ? 10 : 100); cur = 0 }
  }
  return tot + cur
}
// 自然排序:阿拉伯数字段与中文数字段都按数值比
// (第2章<第10章;卷二<卷十<卷二十三——拼音序会把卷八排卷二前)
function naturalCmp(a, b) {
  const RX = /(\d+|[零一两二三四五六七八九十百]+)/g
  const sa = String(a).split(RX)
  const sb = String(b).split(RX)
  for (let i = 0; i < Math.max(sa.length, sb.length); i++) {
    const xa = sa[i], xb = sb[i]
    if (xa === undefined) return -1
    if (xb === undefined) return 1
    if (xa === xb) continue
    if (i % 2 === 1) { // 分割捕获段(数字段)
      const va = /^\d+$/.test(xa) ? parseInt(xa, 10) : cnNumVal(xa)
      const vb = /^\d+$/.test(xb) ? parseInt(xb, 10) : cnNumVal(xb)
      if (va !== null && vb !== null) {
        const d = va - vb
        if (d) return d
        continue
      }
    }
    return xa.localeCompare(xb, 'zh')
  }
  return 0
}
function buildTree(chapters) {
  const tree = []
  for (const ch of chapters) {
    const parts = ch.name.split('/')
    const file = parts.pop()
    let cur = tree
    let path = ''
    for (const seg of parts) {
      path += (path ? '/' : '') + seg
      let node = cur.find(x => x.type === 'dir' && x.name === seg)
      if (!node) {
        node = { type: 'dir', name: seg, key: path, children: [] } // 存原名保证查找一致
        cur.push(node)
      }
      cur = node.children
    }
    cur.push({ type: 'file', name: prettyName(file), ch })
  }
  // 排序:目录在前;同级按自然正序(章节号数值序)
  const sortRec = (arr) => {
    arr.sort((a, b) => (a.type === b.type) ? naturalCmp(a.name, b.name)
      : (a.type === 'dir' ? -1 : 1))
    arr.forEach(x => x.type === 'dir' && sortRec(x.children))
  }
  sortRec(tree)
  // 唯一顶层目录(如「正文」)自动提升,不显示多余外壳层
  let nodes = tree
  if (tree.length === 1 && tree[0].type === 'dir') nodes = tree[0].children
  // 统一美化目录名(查找用原名,显示才替换)
  const pretty = (arr) => arr.forEach(x => {
    if (x.type === 'dir') { x.label = x.name.replace(/_/g, ' · '); pretty(x.children) }
    else { x.label = x.name }
  })
  pretty(nodes)
  return nodes
}
// 显示名美化:去文件扩展名;下划线换「 · 」
function prettyName(n) {
  return n.replace(/\.(md|txt)$/i, '').replace(/_/g, ' · ')
}
async function openDetail(n) {
  try {
    const r = await api.novelDetail(n.path)
    if (!r.novel) throw new Error(r.error || '读取失败')
    dlg.value = r
    reading.value = null
    chapTree.value = buildTree(r.chapters || [])
    // 默认全部目录展开
    const ex = new Set()
    const walk = (arr) => arr.forEach(x => { if (x.type === 'dir') { ex.add(x.key); walk(x.children) } })
    walk(chapTree.value)
    expandedDirs.value = ex
    navCollapsed.value = false
    // 剧本/角色/场景:异步补拉(它读的是技能端脚本包,可能不存在 —— 不挡阅读)
    dlgTab.value = 'read'
    script.value = null
    scriptShots.value = []
    scriptEp.value = ''
    loadScript()
  } catch (e) { ElMessage.error(e.message) }
}

// loadScript 拉「剧本脚本/角色/场景」汇总(不跑 LLM,只读已有脚本包)。
async function loadScript() {
  if (!dlg.value) return
  scriptBusy.value = true
  try {
    script.value = await api.novelScript(dlg.value.novel.path)
    if (script.value?.found && (script.value.episodes || []).length) {
      // 默认展开第一集:用户点「剧本脚本」要看的通常是"第01集到底拍了什么"
      await pickEpisode(script.value.episodes[0].dir)
    }
  } catch (e) { script.value = { found: false, hint: e.message } }
  finally { scriptBusy.value = false }
}

// pickEpisode 取某一集的镜头明细(40 集 × 60 镜一次全拉是几百 KB 的白等)
async function pickEpisode(dir) {
  if (!dlg.value) return
  scriptEp.value = dir
  try {
    const r = await api.novelScript(dlg.value.novel.path, dir)
    scriptShots.value = r.shots || []
  } catch (e) { ElMessage.error(e.message); scriptShots.value = [] }
}

// ---------- 详情弹窗:鼠标拖动改大小 ----------
// 为什么自己做而不是 CSS resize:需要同时受最小/最大尺寸约束,并且在拖动中实时生效;
// CSS `resize: both` 只能改元素盒、改不到 el-dialog 的宽度与内部布局高度。
const dlgW = ref(960)
const dlgH = ref(620)
const resizing = ref(false)
function startResize(e) {
  if (e.button !== 0) return
  const sx = e.clientX, sy = e.clientY
  const sw = dlgW.value, sh = dlgH.value
  resizing.value = true
  const move = (ev) => {
    const maxW = Math.max(560, window.innerWidth - 80)
    const maxH = Math.max(360, window.innerHeight - 100)
    dlgW.value = Math.min(maxW, Math.max(560, sw + (ev.clientX - sx)))
    dlgH.value = Math.min(maxH, Math.max(360, sh + (ev.clientY - sy)))
  }
  const up = () => {
    resizing.value = false
    window.removeEventListener('mousemove', move)
    window.removeEventListener('mouseup', up)
  }
  window.addEventListener('mousemove', move)
  window.addEventListener('mouseup', up)
  e.preventDefault()
}
function toggleDir(key) {
  if (expandedDirs.value.has(key)) expandedDirs.value.delete(key)
  else expandedDirs.value.add(key)
  expandedDirs.value = new Set(expandedDirs.value)
}
async function readChapter(ch) {
  readBusy.value = true
  try {
    const r = await api.novelRead(ch.path)
    reading.value = { name: ch.name, ...r }
  } catch (e) { ElMessage.error(e.message) } finally { readBusy.value = false }
}

// 脚本创作:直接导入技能端已产出的**渲染脚本包**(manifest.json + plan/plan.json),
// **不走 LLM 拆集** —— 平台侧那一步单集 2-6 分钟深度推理,token 与时间全省。
// 与「视频制作」是两个人**口**:这个入口找不到包就明确报错,不悄悄回落到 LLM。
//
// 2026-09-27 沿革:当天先按用户要求隐藏(技能侧停了渲染包产出),当晚技能侧恢复产出脚本
// 相关的交付物后按用户要求**回归**。回归时多了一句诚实提示:若技能侧已把盘上这些包标成
// 废弃(`漫剧脚本/_已废弃-说明.md`),导入的是**历史包**,正式渲染仍建议走「视频制作」。
async function produceScript(n) {
  const ep = n.detail?.episodes || n.script?.episodes || 0
  const src = n.detail
    ? `将从技能侧**结构化明细**（${n.detail.source}，${n.detail.episodes} 集 / ${n.detail.shots} 镜）直接建出集项目：每镜提示词、角色/场景/道具卡都从明细里取，不再跑 AI 拆集。`
    : `将把《${n.name}》目录里已产出的脚本包直接导入（${ep} 集）`
  const dep = (!n.detail && n.script?.deprecated)
    ? `\n⚠ 技能侧已把这本书的渲染包标为废弃（只留查看）——导入的是历史包；正式渲染建议走「视频制作」（平台 LLM 直出）。`
    : ''
  try {
    await ElMessageBox.confirm(
      src + '并**自动开始批量产线**（资产定妆→渲染→质检→成片）。' +
      dep +
      `\n导入后到「项目」页即可开始资产与渲染；不合契约会被整批拒收，不会留下半成品。`,
      '脚本创作(跳过 AI 拆集)', { confirmButtonText: '导入并开始生产', cancelButtonText: '取消', type: 'info' })
  } catch { return }
  try {
    const r = await api.novelProduceScript(n.path)
    const cnt = Array.isArray(r?.results) ? r.results.length : ep
    ElMessage.success(r?.message || `已导入 ${cnt} 集脚本，批量产线已启动`)
    await refresh()
    await refreshProjects()
    G.view = 'home'
  } catch (e) {
    ElMessage.error(e.message) // 契约拒收 / 找不到包:原样展示后端那句话
  }
}

// deliverables 技能侧交付物（素材三卡 + 分镜 markdown）拍平成一条清单：
// 页签上直接渲染成一排标签，点开即读。两类的排序由后端给（分镜按集号，素材按名字）。
const deliverables = computed(() => {
  const d = script.value?.deliverables
  if (!d) return []
  return [...(d.assets || []), ...(d.storyboard || [])]
})

// openDeliverable 读技能侧交付物(素材三卡 / 分镜 markdown)。
// 复用 /api/novels/read —— 它已经带"必须落在小说库内"的范围守卫,不必再开一个读文件端点。
const deliverable = ref(null)   // {name, text, truncated}
async function openDeliverable(d) {
  readBusy.value = true
  try {
    const r = await api.novelRead(d.path)
    deliverable.value = { name: d.rel || d.name, ...r }
  } catch (e) { ElMessage.error(e.message) } finally { readBusy.value = false }
}

const producing = ref(false)
// 风格选择框（「视频制作」那一步）：presets 多选 + 自由文本附加要求。
// 清单来自 G.metaStyles（后端 /api/meta 是单一真源）；后端没就绪时只留自由文本可用。
const produceDlg = ref(null)   // { novel }
const producePicks = ref([])   // 勾选的风格 key
const produceHint = ref('')    // 风格附加要求（自由文本）

// 一键视频创作(带重复验证,后端 409)
// 原书风格解析结果（GET /api/novels/style）：{found, style, field, card, card_chars, hint}
const produceScan = ref(null)
// 【2026-10-09 用户：「小说库导入视频制作时，用户需要选择对应的渲染风格，目前所有视频渲染风格
// 都是一样的」】这一步原来是"确认框 → 直接开跑"，风格**写死**取小说目录 立项.json 的 art_style。
// 现在先弹一个风格选择框（预设多选 + 自由文本），选择的串会由后端写进每集输入的
// 「全局风格:」行（= 全片/跨集的权威风格，不是"提示模型"）；不选则保持老行为。
function openProduce(n) {
  produceDlg.value = { novel: n }
  producePicks.value = []
  produceHint.value = ''
  produceScan.value = null
  // 解析这本书自带的"视频风格"字段（立项.json 的 art_style/渲染风格串/画风、设定集里的视觉风格卡）——
  // 用户 2026-10-09：「另外导入的小说也需要解析是否存在视频风格相关字段」。结果**显示在对话框里**，
  // 只写日志等于没解析。
  api.novelStyle(n.path).then(r => { produceScan.value = r }).catch(() => { produceScan.value = null })
}


// toggleProduceStyle 勾选/取消一个风格预设（多选组合，与项目参数抽屉同一语义）。
function toggleProduceStyle(key) {
  const i = producePicks.value.indexOf(key)
  if (i >= 0) producePicks.value.splice(i, 1)
  else producePicks.value.push(key)
}
// styleOptions 风格清单以后端 /api/meta 为**单一真源**（这里不再抄一份预设表，免得两边漂移）。
const styleOptions = computed(() => G.metaStyles || [])

async function confirmProduce() {
  const n = produceDlg.value && produceDlg.value.novel
  if (!n) return
  const picks = producePicks.value.slice()
  const hint = produceHint.value
  produceDlg.value = null
  await produce(n, picks, hint)
}

async function produce(n, styles, styleHint) {
  const picked = (styles || []).length
    ? `\n\n渲染风格：已选 ${styles.length} 项预设` + (styleHint ? ' + 附加要求' : '')
    : '\n\n渲染风格：沿用原书/立项的风格串（不覆盖）'
  try {
    await ElMessageBox.confirm(
      `将《${n.name}》整本拆集成多集短视频并自动开始批量产线?(拆集 2-6 分钟,日志有实时心跳)` + picked,
      '一键视频创作', { confirmButtonText: '开始创作', cancelButtonText: '取消', type: 'info' })
  } catch { return }
  producing.value = true
  try {
    const r = await api.novelProduce(n.path, 0, styles || [], styleHint || '')
    ElMessage.success(r.message || '已开始创作')
    dlg.value = null
    await refresh()
    await refreshProjects()
    G.view = 'home'
  } catch (e) {
    ElMessage.warning(e.message) // 409 重复等业务提示
  } finally { producing.value = false }
}

// 打开小说所在目录(资源管理器)
async function openDir(n) {
  try {
    await api.openPath({ path: n.path, project: '', absolute: true })
  } catch { /* /api/open 需要 project——后端走 DataDir 白名单;小说目录默认就在 data/Novel 下 */ }
}

// 删除整本小说(磁盘文件,二次确认;已创作的剧集项目不受影响)
async function removeNovel(n) {
  const scope = n.type === 'dir' ? '该书目录(全本/封面/正文等全部文件)' : '该小说文件'
  try {
    await ElMessageBox.confirm(
      `将删除《${n.name}》—— ${scope} 从磁盘移除,不可恢复。` +
      (n.produced ? '\n该书已创作的剧集项目不受影响,可到「项目」页另行管理。' : ''),
      '删除小说', { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  try {
    await api.novelDelete(n.path)
    ElMessage.success(`《${n.name}》已删除`)
    if (dlg.value && dlg.value.novel.path === n.path) dlg.value = null
    refresh()
  } catch (e) { ElMessage.error(e.message) }
}

const fmtChars = (n) => n >= 10000 ? (n / 10000).toFixed(1) + ' 万字' : n + ' 字'

// 隐藏 / 取消隐藏(不删文件:只是从库里收起来,随时可请回来)
async function toggleHidden(n) {
  try {
    const r = await api.novelHidden(n.path, !n.hidden)
    hiddenCount.value = r.hidden_count || 0
    ElMessage.success(r.hidden ? `《${n.name}》已隐藏(文件未动,可在右上「显示已隐藏」里找回)` : `《${n.name}》已取消隐藏`)
    await refresh()
  } catch (e) { ElMessage.error(e.message) }
}

// 「技能端脚本导入」(把技能侧已产出的脚本包导进来的入口)已于 2026-09-27 按用户要求移除 ——
// 用户定调「技能侧独立输出小说内容,不出脚本了,平台侧走 LLM 合理点」,盘上不会再有
// 技能端脚本包可导,这个入口无用。后端 /api/novels/produce-script 仍保留(不删能力)。
// 恢复方式:菜单里加回脚本导入项,这里加回导入函数,出口加回分支。

// 封面重做:**可配置弹窗**(用户 2026-09-26:「做成一个用户自定义配置弹窗啊,重做需要点改变的啊」)。
// 只换 seed 不叫重做 —— 同一个正向词重抽,构图几乎不变。所以把能"改变"的旋钮交给用户:
// 正向词(可改可追加)、尺寸、张数、seed(可复现上次那张)、是否把新词存回技能端留档。
// 三步走:读配置 → 出 N 张候选(不动现封面)→ 挑定后才落盘。
const covOpen = ref(false)
const covBusy = ref(false)
const covNovel = ref(null)
const covMeta = ref(null)      // 配置初始值(留档正向词/尺寸/上次 seed)
const covCands = ref([])       // 候选图
const covForm = ref({ prompt: '', append: '', w: 896, h: 1344, sizeKey: '896x1344', seed: 0, count: 1, savePrompt: false })

async function openCoverDialog(n) {
  covNovel.value = n
  covCands.value = []
  try {
    const c = await api.novelCoverConfig(n.path)
    covMeta.value = c
    covForm.value = {
      prompt: c.prompt || '',
      append: '',
      w: c.width || 896, h: c.height || 1344,
      sizeKey: `${c.width || 896}x${c.height || 1344}`,
      seed: 0,
      count: 1,
      savePrompt: false,
    }
    // 已有候选:重开弹窗接着挑(后端本来就在返回它,前端此前丢掉了 —— 于是每开一次弹窗都得
    // 重新出图才能比)。候选目录里的老图也能直接进对比视图,省一次 GPU。
    covCands.value = c.candidates || []
    covOpen.value = true
  } catch (e) { ElMessage.error(e.message) } // 没有正向词/越界:原样展示后端那句话
}

// 尺寸预设选择(自定义时用下面两个数字输入)
function onSizePick(key) {
  covForm.value.sizeKey = key
  const m = /^(\d+)x(\d+)$/.exec(key || '')
  if (m) { covForm.value.w = +m[1]; covForm.value.h = +m[2] }
}

async function genCovers() {
  if (!covNovel.value) return
  const f = covForm.value
  if (!f.prompt.trim()) { ElMessage.warning('正向词不能为空'); return }
  const loading = ElLoading.service({ text: '封面生成中(本机 Qwen-Image-2.1,每张约 15-40 秒)…' })
  covBusy.value = true
  try {
    const r = await api.novelCoverRedo({
      path: covNovel.value.path, prompt: f.prompt, append: f.append,
      width: f.w, height: f.h, seed: f.seed || 0, count: f.count || 1,
      save_prompt: !!f.savePrompt,
    })
    covCands.value = r.candidates || []
    ElMessage.success(`已生成 ${covCands.value.length} 张候选 —— 挑一张「用这张」才替换现封面`)
  } catch (e) { ElMessage.error(e.message) } finally { covBusy.value = false; loading.close() }
}

async function applyCover(file) {
  if (!covNovel.value) return
  try {
    const r = await api.novelCoverApply(covNovel.value.path, file)
    ElMessage.success(`封面已更新${r?.projects ? `,刷新 ${r.projects} 个集` : ''}${r?.backup ? ';旧封面已备份' : ''}`)
    covOpen.value = false
    coverBust.value = Date.now()
    await refresh()
  } catch (e) { ElMessage.error(e.message) }
}

// 候选图的预览地址(候选也在小说库内,走同一个只读接口)
function candURL(file) {
  return '/api/novels/cover?path=' + encodeURIComponent(file) + '&t=' + coverBust.value
}

// 封面对比:候选不再走"单张放大"(用户 2026-09-26:「重做的封面放大预览左侧需要显示旧的,
// 右侧显示新的,这样才好对比」)。挑封面的唯一问题是"它比现在这张好吗",单张全屏回答不了。
// 对比视图见 components/CoverCompare.vue:左旧右新、共用一套缩放平移、← → 换候选。
const cmpOpen = ref(false)
const cmpIndex = ref(0)
const cmpItems = computed(() => covCands.value.map(c => ({ file: c.file, url: candURL(c.file), seed: c.seed })))
function openCompare(i) {
  if (!covCands.value.length) return
  cmpIndex.value = Math.max(0, Math.min(i, covCands.value.length - 1))
  cmpOpen.value = true
}
// 左栏的"旧"= 本书现封面。coverBust 必须带:重做后封面文件名不变,不加它会显示缓存里的旧图。
function oldCoverURL() {
  const n = covNovel.value
  return n && n.cover ? '/api/novels/cover?path=' + encodeURIComponent(n.cover) + '&t=' + coverBust.value : ''
}
// 对比视图里直接定稿:比完就换,不用关掉回弹窗里再找那张
function onCompareApply(c) {
  cmpOpen.value = false
  if (c) applyCover(c.file)
}

// 图片放大预览(书卡封面走这条;候选走上面的对比视图)。
// 用 Element Plus 自带的 el-image-viewer:滚轮/按钮缩放、旋转、Esc 关闭。
// 它 Teleport 到 body、层级取自全局递增的弹层计数 —— teleported + 显式 z-index 一起给,
// 不吃弹窗的开窗顺序。
const viewer = ref(null)   // { list: [url…], index: 从第几张开始 }
function openViewer(list, index = 0) {
  const urls = (list || []).filter(Boolean)
  if (!urls.length) return
  viewer.value = { list: urls, index: Math.max(0, Math.min(index, urls.length - 1)) }
}
// 书卡封面:单张(它已是当前封面,看的是效果,不涉及挑图)
function viewCover(n) {
  openViewer(n.cover ? ['/api/novels/cover?path=' + encodeURIComponent(n.cover) + '&t=' + coverBust.value] : [])
}

// 卡片右上菜单的命令出口。收成一个出口而不是三个 @click:
// 以后再加动作只改这里 + 菜单项,卡片模板不必再堆@click.stop。
// (`script` 分支 2026-09-27 先随入口隐藏而移除,同日按用户要求随入口一起回归。)
function onCardCmd(cmd, n) {
  if (cmd === 'script') return produceScript(n)
  if (cmd === 'produce') return openProduce(n)
  if (cmd === 'cover') return openCoverDialog(n)
  if (cmd === 'hide') return toggleHidden(n)
  if (cmd === 'remove') return removeNovel(n)
}
</script>

<template>
  <div class="h-full overflow-y-auto p-6">
    <div class="panel panel-glow p-6 mb-5 relative overflow-hidden">
      <div style="position:absolute;right:-20px;top:-40px;font-size:130px;opacity:.06;pointer-events:none">📚</div>
      <div class="flex items-center gap-4 flex-wrap">
        <div>
          <h1 class="text-lg font-bold m-0">小说库 <span class="timecode">NOVEL LIBRARY</span></h1>
          <p class="text-xs m-0 mt-1" style="color:var(--mx-text2)">
            {{ root ? '扫描目录:' + root + ' · 点书卡看章节与正文 · 一键送入视频产线' : (hint || '未配置小说目录') }}
          </p>
        </div>
        <div class="flex-1"></div>
        <el-tooltip v-if="hiddenCount || withHidden" placement="bottom"
                    content="隐藏只是从库里收起来,文件不动;打开开关可把它们请回来">
          <el-switch v-model="withHidden" size="small"
                     :active-text="hiddenCount ? `显示已隐藏(${hiddenCount})` : '显示已隐藏'"
                     @change="refresh" />
        </el-tooltip>
        <div class="text-center"><div class="stat-num">{{ novels.length }}</div><div class="stat-label">小说</div></div>
        <el-tooltip content="重新扫描小说库目录(增删书后点此刷新)" placement="bottom">
          <el-button size="small" circle :loading="loading" @click="refresh"><el-icon><Refresh /></el-icon></el-button>
        </el-tooltip>
      </div>
    </div>

    <div v-if="!novels.length" class="panel empty-zone">
      <div class="ez-ico"><el-icon :size="26"><Reading /></el-icon></div>
      <div class="ez-title">小说库还没有内容</div>
      <p class="ez-desc">把小说放进扫描目录(推荐结构:书名/全本/*.md、书名/封面/*.png、书名/正文/分卷章节),这里会自动列出书卡。</p>
    </div>

    <div class="grid gap-5" style="grid-template-columns:repeat(auto-fill,minmax(216px,1fr))">
      <div v-for="n in novels" :key="n.path" class="shot-card cursor-pointer"
           :class="{ 'is-hidden': n.hidden }"
           :title="'查看《' + n.name + '》章节目录与正文,右上菜单可视频制作/隐藏/删除'" @click="openDetail(n)">
        <div class="relative" style="aspect-ratio:3/4;background:var(--mx-bg2);border-radius:10px 10px 0 0;overflow:hidden">
          <img v-if="n.cover" :src="'/api/novels/cover?path=' + encodeURIComponent(n.cover) + '&t=' + coverBust"
               style="width:100%;height:100%;object-fit:cover" loading="lazy" />
          <div v-else class="absolute inset-0 flex flex-col items-center justify-center gap-2">
            <el-icon :size="34" color="var(--mx-text3)"><Reading /></el-icon>
            <span class="timecode">{{ n.type === 'dir' ? '文件夹' : '单文件' }}</span>
          </div>
          <!-- 卡片角标：优先展示**结构化明细**（新的、可渲染的脚本产物），没有才退回旧渲染包 -->
          <el-tag v-if="n.detail" size="small" type="success" effect="dark" class="absolute top-2 left-2"
                  :title="`技能侧结构化明细：${n.detail.episodes} 集 / ${n.detail.shots} 镜（可直接建项目）`">
            明细 {{ n.detail.episodes }} 集
          </el-tag>
          <el-tag v-else-if="n.script" size="small" type="warning" effect="dark" class="absolute top-2 left-2"
                  :title="n.script.deprecated ? '旧渲染包（技能侧已标为废弃/历史包）' : '技能侧渲染脚本包'">
            脚本 {{ n.script.episodes }} 集
          </el-tag>
          <el-tag v-else-if="n.produced" size="small" type="success" effect="dark" class="absolute top-2 left-2">已创作</el-tag>
          <el-tag v-else-if="n.hidden" size="small" type="info" effect="dark" class="absolute top-2 left-2">已隐藏</el-tag>
          <!-- 右下:封面放大预览(封面重做后要能看清细节;卡片主体点击是打开详情,
               所以这里必须 stop,免得点放大顺手把详情弹窗也拉起来) -->
          <button v-if="n.cover" class="cover-zoom" title="放大预览封面" @click.stop="viewCover(n)">
            <el-icon :size="14"><ZoomIn /></el-icon>
          </button>
          <!-- 右上:作品菜单(视频制作 / 隐藏 / 删除)。
               为什么收成一个菜单:原先只有一个删除按钮,而"隐藏"是比"删除"更常用的动作 ——
               把不可逆的删除放在最容易误点的一格,本身就不对;菜单里删除仍是最后一项并带二次确认。 -->
          <el-dropdown class="cover-menu-wrap" trigger="click" placement="bottom-end"
                       @command="(c) => onCardCmd(c, n)">
            <button class="cover-menu" title="作品菜单" @click.stop>
              <el-icon :size="13"><MoreFilled /></el-icon>
            </button>
            <template #dropdown>
              <el-dropdown-menu>
                <!-- 「脚本创作」:导入技能侧的脚本产物并自动开始批量产线。
                     2026-09-27 晚起**优先走结构化明细**（`分镜/分镜明细.json`，每镜带 h3_prompt、
                     引用是 ID、三类卡内嵌）；没有明细时才退回旧渲染包，那时才标「历史包」。 -->
                <el-dropdown-item command="script" :disabled="!n.script && !n.detail"
                                  :title="n.detail
                                    ? `从技能侧结构化明细（${n.detail.source}，${n.detail.episodes} 集 / ${n.detail.shots} 镜）直接建项目并自动开产线`
                                    : (n.script
                                        ? (n.script.deprecated
                                            ? '这本书的渲染包已被技能侧标为废弃(只留查看)——导入的是历史包,正式渲染建议走「视频制作」'
                                            : '直接导入技能端已产出的脚本包并自动开始批量产线(不再跑 AI 拆集)')
                                        : '这本书目录里没有技能端脚本产物(需要 分镜/分镜明细.json,或旧的 manifest.json + plan/plan.json)')">
                  <el-icon><Document /></el-icon>脚本创作{{ n.detail ? `（${n.detail.episodes} 集）` : (n.script ? `（${n.script.episodes} 集）` : '') }}
                  <el-tag v-if="!n.detail && n.script?.deprecated" size="small" type="info" effect="plain" style="margin-left:6px">历史包</el-tag>
                  <el-tag v-else-if="n.detail" size="small" type="success" effect="plain" style="margin-left:6px">明细</el-tag>
                </el-dropdown-item>
                <el-dropdown-item command="produce" :disabled="producing">
                  <el-icon><VideoCamera /></el-icon>视频制作
                </el-dropdown-item>
                <el-dropdown-item command="cover"
                                  title="用封面目录里的正向词(pos-qwen21.txt)重新生成封面;旧封面自动备份,集项目封面一并刷新">
                  <el-icon><Picture /></el-icon>封面重做
                </el-dropdown-item>
                <el-dropdown-item command="hide" divided>
                  <el-icon><Hide /></el-icon>{{ n.hidden ? '取消隐藏' : '隐藏作品' }}
                </el-dropdown-item>
                <el-dropdown-item command="remove">
                  <el-icon><Delete /></el-icon>删除作品
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
        <div class="p-3 flex flex-col gap-1.5">
          <b class="text-sm truncate">《{{ n.name }}》</b>
          <span class="text-xs" style="color:var(--mx-text3)">{{ fmtChars(n.chars) }} · {{ n.chapters }} 章</span>
        </div>
      </div>
    </div>

    <!-- 封面重做:可配置弹窗(正向词/尺寸/张数/seed/存回留档 → 出候选 → 挑定落盘) -->
    <el-dialog v-model="covOpen" :title="covNovel ? `封面重做 ·《${covNovel.name}》` : '封面重做'"
               width="880px" top="5vh" append-to-body @close="cmpOpen = false">
      <div class="flex gap-3" style="min-height:300px">
        <!-- 左:参数 -->
        <div class="flex flex-col gap-2" style="width:340px;flex:none">
          <div class="text-xs" style="color:var(--mx-text3)">正向词(留档 <code>pos-qwen21.txt</code>;改了直接用改过的)</div>
          <el-input v-model="covForm.prompt" type="textarea" :rows="9" placeholder="英文正向词" />
          <div class="text-xs" style="color:var(--mx-text3)">附加要求(可选,追加到正向词末尾)</div>
          <el-input v-model="covForm.append" size="small" placeholder="例:黄昏逆光,更有烟火气;主角换成正脸" />
          <div class="flex items-center gap-2">
            <span class="text-xs" style="color:var(--mx-text3);width:52px">尺寸</span>
            <el-select :model-value="covForm.sizeKey" size="small" style="flex:1" @change="onSizePick">
              <el-option v-for="s in (covMeta?.sizes || [])" :key="s.w + 'x' + s.h"
                         :label="s.label" :value="s.w + 'x' + s.h" />
              <el-option label="自定义(右填宽高)" value="custom" />
            </el-select>
          </div>
          <div class="flex items-center gap-2">
            <span class="text-xs" style="color:var(--mx-text3);width:52px"></span>
            <el-input-number v-model="covForm.w" size="small" :min="512" :max="2048" :step="16" controls-position="right" style="width:110px" />
            <span class="text-xs">×</span>
            <el-input-number v-model="covForm.h" size="small" :min="512" :max="2048" :step="16" controls-position="right" style="width:110px" />
          </div>
          <div class="flex items-center gap-2">
            <span class="text-xs" style="color:var(--mx-text3);width:52px">张数</span>
            <el-select v-model="covForm.count" size="small" style="width:90px">
              <el-option v-for="n in [1, 2, 3, 4]" :key="n" :label="n + ' 张'" :value="n" />
            </el-select>
            <span class="text-xs" style="color:var(--mx-text3)">一次多张便于挑</span>
          </div>
          <div class="flex items-center gap-2">
            <span class="text-xs" style="color:var(--mx-text3);width:52px">seed</span>
            <el-input-number v-model="covForm.seed" size="small" :min="0" controls-position="right" style="width:150px" />
            <el-button v-if="covMeta?.last_seed" text size="small"
                       @click="covForm.seed = covMeta.last_seed">用上次 {{ covMeta.last_seed }}</el-button>
            <span class="text-xs" style="color:var(--mx-text3)">0 = 随机</span>
          </div>
          <el-checkbox v-model="covForm.savePrompt" size="small">
            同时把新正向词存回封面留档(旧词备份为 .bak)
          </el-checkbox>
          <el-button type="primary" :loading="covBusy" @click="genCovers">
            <el-icon><MagicStick /></el-icon>生成候选{{ covForm.count > 1 ? `（${covForm.count} 张）` : '' }}
          </el-button>
          <div class="text-xs" style="color:var(--mx-text3)">
            生成只写「候选」,**不动现封面**;挑定「用这张」才替换(旧封面自动备份,集项目封面一并刷新)。
          </div>
        </div>
        <!-- 右:候选 -->
        <div class="panel p-3 flex-1 flex flex-col" style="min-width:0">
          <div class="flex items-center gap-2 mb-2">
            <b class="text-xs">候选</b>
            <span class="timecode">{{ covCands.length }} 张</span>
            <div class="flex-1"></div>
            <span class="timecode" style="font-size:10px">点图对比(左旧右新) · 点「用这张」替换现封面</span>
          </div>
          <div class="flex-1 overflow-y-auto tree-scroll">
            <div v-if="!covCands.length" class="text-xs py-8 text-center" style="color:var(--mx-text3)">
              还没有候选 —— 调好左边的参数,点「生成候选」
            </div>
            <div class="cand-grid">
              <div v-for="(c, i) in covCands" :key="c.file" class="cand-item">
                <img :src="candURL(c.file)" alt="" title="点击与现封面并排对比(左旧右新)" @click="openCompare(i)" />
                <div class="flex items-center gap-2 mt-1">
                  <span class="timecode" style="font-size:10px">seed {{ c.seed }}</span>
                  <div class="flex-1"></div>
                  <el-button type="primary" size="small" @click="applyCover(c.file)">用这张</el-button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </el-dialog>

    <!-- 阅读详情弹窗:章节文件管理 + 正文 + 剧本脚本 + 角色 + 场景
         尺寸可拖(右下角拖柄):详情要看的既有正文也有分镜表,固定 860px 装不下。 -->
    <el-dialog :model-value="!!dlg" :title="dlg ? '《' + dlg.novel.name + '》· 阅读详情' : ''"
               :width="dlgW + 'px'" top="4vh" class="novel-dlg" @update:model-value="dlg = null">
      <template v-if="dlg">
        <div class="flex items-center gap-3 mb-2 text-xs" style="color:var(--mx-text2)">
          <span>{{ fmtChars(dlg.novel.chars) }}</span>
          <span>· {{ dlg.chapters.length }} 个章节文件</span>
          <span v-if="script?.found">· 脚本 {{ script.episodes.length }} 集</span>
          <div class="flex-1"></div>
          <el-tag v-if="dlg.novel.produced" size="small" type="success">已创作(首页剧集合集)</el-tag>
          <span class="timecode" style="font-size:10px" title="拖动右下角圆点调整弹窗大小">⇲ 可拖动改大小</span>
        </div>

        <el-tabs v-model="dlgTab" class="novel-dlg-tabs">
          <!-- ① 正文(原有阅读区) -->
          <el-tab-pane label="正文" name="read">
            <div class="flex gap-3" :style="{ height: dlgH - 150 + 'px' }">
              <div class="panel p-4 flex flex-col flex-1" style="min-width:0">
                <template v-if="reading">
                  <div class="flex items-center gap-2 mb-2">
                    <b class="text-xs truncate">{{ reading.name }}</b>
                    <span class="timecode">{{ reading.chars ? reading.chars.toLocaleString() + ' 字' : '' }}</span>
                    <span v-if="reading.truncated" class="timecode" style="color:var(--mx-yellow)">(过长已截断预览)</span>
                  </div>
                  <div class="text-xs flex-1 overflow-y-auto read-body" style="line-height:1.9;white-space:pre-wrap;color:var(--mx-text2)">{{ reading.text }}</div>
                </template>
                <div v-else class="flex-1 flex flex-col items-center justify-center gap-2" style="color:var(--mx-text3)">
                  <el-icon :size="30"><Reading /></el-icon>
                  <span class="text-xs">点击右侧目录中的章节阅读正文</span>
                </div>
              </div>

              <!-- 右侧独立目录栏(可收起/展开;目录节点可折叠) -->
              <div v-if="!navCollapsed" class="nav-tree flex flex-col" :style="{ maxHeight: dlgH - 150 + 'px' }">
                <div class="flex items-center gap-2 px-3 py-2 flex-none" style="border-bottom:1px solid var(--mx-line)">
                  <el-icon :size="13" color="var(--mx-amber)"><Folder /></el-icon>
                  <b class="text-xs flex-1">目录</b>
                  <span class="timecode" style="font-size:10px">{{ dlg.chapters.length }}</span>
                  <el-button text size="small" @click="navCollapsed = true" title="收起目录栏">
                    <el-icon :size="13"><ArrowRight /></el-icon>
                  </el-button>
                </div>
                <div class="flex-1 overflow-y-auto py-1.5 tree-scroll">
                  <ChapterTree :nodes="chapTree" :expanded="expandedDirs" :reading="reading"
                              @read="readChapter" @toggle="toggleDir" />
                </div>
              </div>
              <!-- 收起态:展开按钮 -->
              <div v-else class="flex flex-col align-center gap-2" style="width:34px;flex:none">
                <el-button text size="small" @click="navCollapsed = false" title="展开目录栏">
                  <el-icon :size="14"><ArrowLeft /></el-icon>
                </el-button>
                <span class="timecode" style="writing-mode:vertical-rl;font-size:10px">目录 {{ dlg.chapters.length }}</span>
              </div>
            </div>
          </el-tab-pane>

          <!-- ② 剧本脚本:集清单 + 该集分镜明细(只读已有脚本包,不跑 LLM) -->
          <el-tab-pane label="剧本脚本" name="script">
            <div v-if="scriptBusy" class="text-xs py-6 text-center" style="color:var(--mx-text3)">读取脚本包…</div>
            <div v-else-if="!script?.found" class="text-xs py-6 text-center" style="color:var(--mx-text3)">
              {{ script?.hint || '这本书目录里没有技能端脚本包' }}
            </div>
            <div v-else class="flex flex-col gap-2" :style="{ height: dlgH - 150 + 'px' }">
              <!-- 技能侧**交付物**(素材三卡 / 分镜 markdown):技能侧 v2.14 起的主交付面。
                   只读展示 —— 它们是给人读的,渲染输入仍是 plan.json 或平台 LLM 直出。 -->
              <div v-if="deliverables.length" class="panel px-3 py-2 flex items-center gap-2 flex-wrap flex-none">
                <b class="text-xs">技能侧交付</b>
                <el-tag v-for="d in deliverables" :key="d.path" size="small" effect="plain"
                        style="cursor:pointer" @click="openDeliverable(d)"
                        :title="`${d.rel} · ${d.mtime}`">{{ d.name }}</el-tag>
                <span class="text-xs" style="color:var(--mx-text3)">点开即读（不跑 LLM）</span>
              </div>
              <!-- 没有渲染包(只有上面的交付物):说明为什么不给分镜明细,别留一片空白 -->
              <div v-if="!(script.episodes || []).length"
                   class="text-xs py-6 text-center panel flex-1 flex items-center justify-center px-6"
                   style="color:var(--mx-text3)">
                {{ script.hint || '这本书没有渲染脚本包（manifest.json + plan/plan.json），所以没有结构化分镜明细' }}
              </div>
              <div v-else class="flex gap-3 flex-1" style="min-height:0">
              <!-- 集清单 -->
              <div class="nav-tree flex flex-col" style="width:190px;flex:none">
                <div class="flex items-center gap-2 px-3 py-2 flex-none" style="border-bottom:1px solid var(--mx-line)">
                  <b class="text-xs flex-1">集({{ script.episodes.length }})</b>
                  <span class="timecode" style="font-size:10px">{{ script.kind === 'series' ? '系列' : '单集' }}</span>
                </div>
                <div class="flex-1 overflow-y-auto py-1 tree-scroll">
                  <div v-for="e in script.episodes" :key="e.dir" class="ep-row" :class="{ on: scriptEp === e.dir }"
                       @click="pickEpisode(e.dir)">
                    <b class="text-xs truncate">{{ e.episode }}</b>
                    <span class="text-xs truncate" style="color:var(--mx-text3)">{{ e.title }}</span>
                    <span class="timecode" style="font-size:10px">{{ e.shots }} 镜</span>
                  </div>
                </div>
              </div>
              <!-- 该集镜头表 -->
              <div class="panel p-3 flex flex-col flex-1" style="min-width:0">
                <div class="flex items-center gap-2 mb-2">
                  <b class="text-xs">分镜明细</b>
                  <span class="timecode">{{ scriptShots.length }} 镜</span>
                  <span class="timecode" style="font-size:10px">景别 · 时长 · 台词/旁白</span>
                  <!-- 数据来源如实标出：结构化明细（技能侧新交，字段与契约同名、带主视角/钩子/尾帧）
                       与旧渲染包（plan.json，没有这三个字段）要在界面上分得清 -->
                  <span v-if="script.source" class="timecode" style="font-size:10px"
                        :title="script.source === '分镜明细.json'
                          ? '镜头明细来自技能侧结构化明细（带 view/hook_type/end_state/地标/光态）'
                          : '镜头明细来自渲染脚本包 plan.json（旧形态：没有主视角/钩子/尾帧）'">
                    {{ script.source === '分镜明细.json' ? '结构化明细' : '渲染包' }}
                  </span>
                </div>
                <div class="flex-1 overflow-y-auto tree-scroll">
                  <div v-for="s in scriptShots" :key="s.id" class="shot-row">
                    <div class="flex items-center gap-2 flex-wrap">
                      <span class="timecode" style="min-width:38px">#{{ s.id }}</span>
                      <el-tag size="small" effect="plain">{{ s.size || '—' }}</el-tag>
                      <span class="timecode">{{ s.duration }}s</span>
                      <el-tag size="small" :type="s.mode === 'ref2va' ? 'warning' : 'info'" effect="plain">{{ s.mode }}</el-tag>
                      <el-tag v-if="s.angle_zh" size="small" type="info" effect="plain"
                              title="机位角度（中文短标签；**不是**契约字段，不回写 plan）">{{ s.angle_zh }}</el-tag>
                      <el-tag v-if="s.view" size="small" type="info" effect="dark"
                              :title="'官方主视角（逐字）：' + s.view">官方视角</el-tag>
                      <el-tag v-else-if="s.view_note" size="small" effect="plain" :title="s.view_note">视角留空</el-tag>
                      <el-tag v-if="s.hook_type" size="small" type="success" effect="plain" title="官方受控钩子类型">{{ s.hook_type }}</el-tag>
                      <el-tag v-if="s.characters?.length" size="small" effect="plain" title="出场角色（决定参考图）">{{ s.characters.join('、') }}</el-tag>
                      <!-- 刻意不绑卡/不给脸：技能侧显式标了 unbound。**不是缺陷**，所以用素色标签，
                           并把 reason 翻成人话；详细说明在 unbound_hint 里（挂 tooltip） -->
                      <el-tag v-if="s.shot_size_note" size="small" type="info" effect="plain"
                              :title="s.shot_size_note">景别已调整</el-tag>
                      <!-- 画外演出（技能侧 2026-09-27 口径）：内心活动由 Q 版形象演出、系统由全息面板＋Q 版机器人演出。
                           标记出来是因为它决定**参考图**（这一镜画面里该出现谁）—— 人审时不该靠翻提示词正文才知道。
                           名字在本地查（characters/props 页签的数据就在手上），不在后端再拼一份。 -->
                      <el-tag v-if="s.vo_kind === 'inner'" size="small" type="warning" effect="plain"
                              :title="'内心活动：由 Q 版形象演出，本人闭嘴、不演这段 —— ' + voCastText(s)">
                        内心·Q版演出
                      </el-tag>
                      <el-tag v-else-if="s.vo_kind === 'system'" size="small" type="warning" effect="plain"
                              :title="'系统：全息面板（道具）＋ Q 版机器人（人物）同框，人不念系统台词 —— ' + voCastText(s)">
                        系统·面板＋机器人
                      </el-tag>
                      <el-tag v-else-if="s.vo_kind" size="small" type="danger" effect="plain"
                              :title="'vo_kind 取值不认识：' + s.vo_kind + '（只认 inner / system；认不出的会被当普通镜渲染）'">
                        vo_kind 不认识
                      </el-tag>
                      <el-tag v-if="s.unbound" size="small" type="info" effect="plain"
                              :title="s.unbound_hint || (s.unbound_reason === 'no_card' ? '画面里的人不在本集角色表（群演/一次性角色）：要让他露脸就得先建卡' : '刻意不给脸（背影/局部/群像）')">
                        {{ s.unbound_reason === 'no_card' ? '群演未建卡' : (s.unbound_reason === 'faceless_by_design' ? '刻意无脸' : '未绑卡') }}
                      </el-tag>
                      <el-tag v-if="s.size && !s.characters?.length && (s.size.includes('特写') || s.size.includes('近景'))"
                              size="small" type="danger" effect="dark" title="特写/近景却没有出场角色：这一档画面里几乎只有一张脸，没有卡就每次换一张新脸">
                        无卡露脸
                      </el-tag>
                    </div>
                    <div class="text-xs mt-1" style="white-space:pre-wrap">{{ s.action }}</div>
                    <div v-for="(d, i) in (s.dialogue || [])" :key="i" class="text-xs mt-1" style="color:var(--mx-amber)">💬 {{ d }}</div>
                    <div v-if="s.narration" class="text-xs mt-1" style="color:var(--mx-text3)">🎙 旁白:{{ s.narration }}</div>
                    <div v-if="s.end_state" class="text-xs mt-1" style="color:var(--mx-text3)">⏭ 尾帧:{{ s.end_state }}</div>
                    <div v-if="s.view" class="text-xs mt-1" style="color:var(--mx-text3)">🎥 {{ s.view }}</div>
                    <div v-if="s.landmarks || s.light" class="text-xs mt-1" style="color:var(--mx-text3)">
                      <span v-if="s.landmarks">📍 {{ s.landmarks }}</span>
                      <span v-if="s.light"> · 💡 {{ s.light }}</span>
                    </div>
                  </div>
                  <div v-if="!scriptShots.length" class="text-xs py-6 text-center" style="color:var(--mx-text3)">选一集看分镜</div>
                </div>
              </div>
              </div>
            </div>
          </el-tab-pane>

          <!-- ③ 角色 -->
          <el-tab-pane :label="`角色${script?.characters ? '（' + script.characters.length + '）' : ''}`" name="chars">
            <div class="overflow-y-auto" :style="{ height: dlgH - 150 + 'px' }">
              <div v-if="!script?.characters?.length" class="text-xs py-6 text-center" style="color:var(--mx-text3)">这本书的脚本里没有角色卡</div>
              <div v-for="c in (script?.characters || [])" :key="c.id || c.name" class="card-item">
                <div class="flex items-center gap-2">
                  <b class="text-sm">{{ c.name }}</b>
                  <el-tag size="small" effect="plain">{{ c.kind || 'human' }}</el-tag>
                  <span class="timecode" style="font-size:10px">出场 {{ c.in }} 集</span>
                  <span class="timecode" style="font-size:10px">{{ c.id }}</span>
                </div>
                <div class="text-xs mt-1" style="white-space:pre-wrap;color:var(--mx-text2)">{{ c.desc }}</div>
                <div v-if="c.voice" class="text-xs mt-1" style="color:var(--mx-amber)">🔊 声线:{{ c.voice }}</div>
                <details v-if="c.prompt" class="mt-1">
                  <summary class="text-xs" style="cursor:pointer;color:var(--mx-text3)">定妆照提示词</summary>
                  <div class="text-xs mt-1" style="white-space:pre-wrap;color:var(--mx-text3)">{{ c.prompt }}</div>
                </details>
              </div>
            </div>
          </el-tab-pane>

          <!-- ④ 场景 -->
          <el-tab-pane :label="`场景${script?.scenes ? '（' + script.scenes.length + '）' : ''}`" name="scenes">
            <div class="overflow-y-auto" :style="{ height: dlgH - 150 + 'px' }">
              <div v-if="!script?.scenes?.length" class="text-xs py-6 text-center" style="color:var(--mx-text3)">这本书的脚本里没有场景卡</div>
              <div v-for="s in (script?.scenes || [])" :key="s.id || s.name" class="card-item">
                <div class="flex items-center gap-2">
                  <b class="text-sm">{{ s.name }}</b>
                  <span class="timecode" style="font-size:10px">出现 {{ s.in }} 集</span>
                  <span class="timecode" style="font-size:10px">{{ s.id }}</span>
                </div>
                <div class="text-xs mt-1" style="white-space:pre-wrap;color:var(--mx-text2)">{{ s.desc }}</div>
              </div>
            </div>
          </el-tab-pane>

          <!-- ⑤ 道具(用户 2026-09-26:「在小说库的弹窗里新增 道具 TAG」)。
               技能侧的字段是逐步加的:老包只有 en(英文外观短语),新包才会有
               description / image_prompt —— 有什么显示什么,不为了还没吐出来的字段留空壳。 -->
          <el-tab-pane :label="`道具${script?.props ? '（' + script.props.length + '）' : ''}`" name="props">
            <div class="overflow-y-auto" :style="{ height: dlgH - 150 + 'px' }">
              <div v-if="!script?.props?.length" class="text-xs py-6 text-center" style="color:var(--mx-text3)">
                这本书的脚本里没有道具卡(技能侧输出道具之前,这一栏是空的)
              </div>
              <div v-for="p in (script?.props || [])" :key="p.id || p.name" class="card-item">
                <div class="flex items-center gap-2">
                  <b class="text-sm">{{ p.name }}</b>
                  <span class="timecode" style="font-size:10px">出现 {{ p.in }} 集</span>
                  <span class="timecode" style="font-size:10px">{{ p.id }}</span>
                </div>
                <div v-if="p.desc" class="text-xs mt-1" style="white-space:pre-wrap;color:var(--mx-text2)">{{ p.desc }}</div>
                <div v-else-if="p.en" class="text-xs mt-1" style="white-space:pre-wrap;color:var(--mx-text2)">{{ p.en }}</div>
                <details v-if="p.prompt" class="mt-1">
                  <summary class="text-xs" style="cursor:pointer;color:var(--mx-text3)">道具图提示词</summary>
                  <div class="text-xs mt-1" style="white-space:pre-wrap;color:var(--mx-text3)">{{ p.prompt }}</div>
                </details>
                <div v-else class="text-xs mt-1" style="color:var(--mx-text3)">技能侧还没输出这件道具的提示词</div>
              </div>
            </div>
          </el-tab-pane>
        </el-tabs>
      </template>
      <template #footer>
        <el-button @click="dlg = null">关闭</el-button>
        <el-tooltip content="整本拆集成多集短视频项目并自动批量产线(拆集 2-6 分钟)" placement="top">
        <el-button type="primary" :loading="producing" :disabled="dlg && dlg.novel.produced"
                   @click="dlg && openProduce(dlg.novel)">
          {{ dlg && dlg.novel.produced ? '已创作(勿重复)' : '一键视频创作' }}
        </el-button>
        </el-tooltip>
      </template>
      <!-- 右下角拖动柄:改弹窗宽高(受窗口边界约束) -->
      <div class="novel-dlg-grip" :class="{ on: resizing }" title="拖动调整弹窗大小" @mousedown="startResize"></div>
    </el-dialog>

    <!-- 视频制作的**渲染风格选择**（2026-10-09 用户：「小说库导入视频制作时，用户需要选择对应的
         渲染风格，目前所有视频渲染风格都是一样的」）。勾选的预设 + 附加要求会拼成一条权威风格串，
         由后端写进每集输入的「全局风格:」行（全片/跨集一致），**不选 = 沿用原书/立项的风格串**。 -->
    <el-dialog :model-value="!!produceDlg"
               :title="produceDlg ? `视频制作 ·《${produceDlg.novel.name}》` : '视频制作'"
               width="640px" top="8vh" append-to-body @close="produceDlg = null">
      <div class="flex flex-col gap-3">
        <div class="text-xs" style="color:var(--mx-text3)">
          渲染风格决定**全片视觉基调**（角色定妆照与每一镜都按它渲染）。
          不勾选 = 沿用这本书自带的风格串（改动前的行为）。
        </div>
        <!-- 原书风格解析结果（用户 2026-10-09：「导入的小说也需要解析是否存在视频风格相关字段」）：
             **不选**时用的就是它；扫不到就直说「这本书没有风格字段」，而不是让用户以为平台取了书的风格。 -->
        <div class="text-xs rounded p-2" style="background:var(--mx-fill, #f6f7f9); color:var(--mx-text2)">
          <template v-if="produceScan && produceScan.found">
            <b>原书风格</b>：{{ produceScan.style.slice(0, 120) }}{{ produceScan.style.length > 120 ? '…' : '' }}<br>
            <span style="color:var(--mx-text3)">来自 {{ produceScan.field }}<template v-if="produceScan.hint">；画风名：{{ produceScan.hint }}</template></span>
          </template>
          <template v-else-if="produceScan">
            <b>这本书里没有解析到视频风格字段</b>
            <span style="color:var(--mx-text3)">（立项.json 的 art_style / 渲染风格串 / 画风 与 设定集里的风格卡都没有）——
              建议在上面选一个预设，或写下附加要求；不选则由分镜会话自定。</span>
          </template>
          <template v-else>
            <span style="color:var(--mx-text3)">正在解析这本书的风格字段…</span>
          </template>
          <template v-if="produceScan && produceScan.card">
            <br><span style="color:var(--mx-text3)">另发现风格卡 设定集/{{ produceScan.card }}（{{ produceScan.card_chars }} 字）—— 本书画面口径以它为准。</span>
          </template>
        </div>
        <div v-if="styleOptions.length" class="flex flex-wrap gap-2">
          <el-button v-for="st in styleOptions" :key="st.key" size="small"
                     :type="producePicks.includes(st.key) ? 'primary' : ''"
                     @click="toggleProduceStyle(st.key)">{{ st.name }}</el-button>
        </div>
        <div v-else class="text-xs" style="color:var(--mx-text3)">
          风格清单未就绪（后端 /api/meta 没返回预设）：可以先不选，或只用下面的附加要求描述风格。
        </div>
        <div class="flex flex-col gap-1">
          <div class="text-xs">风格附加要求（自由文本，与上方预设叠加）</div>
          <el-input v-model="produceHint" type="textarea" :rows="3"
                    placeholder="例：冷暖对比要克制；夜景不要霓虹；皮肤要有真实纹理" />
        </div>
        <div class="text-xs" style="color:var(--mx-text3)">
          已选 {{ producePicks.length }} 项预设{{ produceHint ? ' + 附加要求' : '' }}。
          生成的风格串会写进每集输入的「全局风格:」行 —— 那是全片/跨集一致的**权威风格**，
          不是"给模型的一句提示"。日志里会如实播报最终用的串。
        </div>
      </div>
      <template #footer>
        <el-button @click="produceDlg = null">取消</el-button>
        <el-button type="primary" :loading="producing" @click="confirmProduce">开始创作</el-button>
      </template>
    </el-dialog>

    <!-- 封面对比:左旧右新(挑封面就是做 A/B 判断,见 CoverCompare.vue)。
         关掉封面重做弹窗时一并收起,免得留下一个"对着空列表"的对比视图。 -->
    <CoverCompare v-if="cmpOpen" :title="covNovel?.name" :old-url="oldCoverURL()"
                  :items="cmpItems" :index="cmpIndex"
                  @close="cmpOpen = false" @apply="onCompareApply" />

    <!-- 图片放大预览(书卡封面):滚轮缩放、旋转、Esc 关闭 -->
    <el-image-viewer v-if="viewer" :url-list="viewer.list" :initial-index="viewer.index"
                     :z-index="4000" teleported hide-on-click-modal show-progress
                     @close="viewer = null" />
    <!-- 技能侧交付物阅读(素材三卡 / 分镜 markdown):纯文本展示,与「正文」同一套观感 -->
    <el-dialog :model-value="!!deliverable" :title="deliverable ? deliverable.name : ''"
               width="900px" top="6vh" append-to-body @close="deliverable = null">
      <div v-if="deliverable" class="flex flex-col gap-2">
        <div class="text-xs" style="color:var(--mx-text3)">
          {{ deliverable.chars }} 字{{ deliverable.truncated ? '（过大已截断）' : '' }} · 技能侧产物，只读
        </div>
        <pre class="text-xs overflow-y-auto tree-scroll p-3"
             style="white-space:pre-wrap;max-height:70vh;background:var(--mx-panel-bg2);border-radius:8px">{{ deliverable.text }}</pre>
      </div>
    </el-dialog>
  </div>
</template>
