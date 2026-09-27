<script setup>
import { G, api, refreshProject, phaseLabel, sourceLabel, sourceTone } from '../store'
import { computed, ref, reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import PipelineBar from '../components/PipelineBar.vue'
import ShotCard from '../components/ShotCard.vue'

const tab = ref('script')
const sourceText = ref('')
const sourceLoaded = ref(false)
const importInput = ref(null)
const aiDlg = ref(false)
const aiPrompt = ref('')
const aiBusy = ref(false)
async function aiCompose() {
  if (!aiPrompt.value.trim()) { ElMessage.warning('请填写创作需求'); return }
  aiBusy.value = true
  try {
    const r = await api.composeStory(pj.value.project.id, aiPrompt.value.trim(), '')
    sourceText.value = (await api.project(pj.value.project.id)).source || sourceText.value
    G.project = await api.project(pj.value.project.id)
    ElMessage.success(`AI 创作完成(${r.chars} 字),已载入源文本`)
    aiDlg.value = false
    aiPrompt.value = ''
  } catch (e) { ElMessage.error(e.message) } finally { aiBusy.value = false }
}

const pj = computed(() => G.project)
const plan = computed(() => pj.value?.plan || null)
const manifest = computed(() => pj.value?.manifest || {})
const assets = computed(() => pj.value?.assets || {})
const running = computed(() => pj.value?.project?.running)

// 首次进入载入源文本
if (pj.value && !sourceLoaded.value) {
  sourceText.value = pj.value.source || ''
  sourceLoaded.value = true
}

const charsWithAsset = computed(() =>
  (plan.value?.characters || []).map(c => ({ ...c, asset: assets.value['char:' + c.id] })))
const scenesWithAsset = computed(() =>
  (plan.value?.scenes || []).map(s => ({ ...s, asset: assets.value['scene:' + s.id] })))
// 道具卡（2026-09-27 用户：「工作台中的资产也要展示道具」）。资产 id 用 `prop:<id>`，
// 与角色/场景同一套命名；后端与资产库早就支持这一类（生成侧 2026-09-27 才补上出图）。
const propsWithAsset = computed(() =>
  (plan.value?.props || []).map(p => ({ ...p, asset: assets.value['prop:' + p.id] })))

// ---------- 造型档归人(技能侧清单 §4.2) ----------
//
// 技能侧把"造型随剧情变"落成**一角色多张卡**:主卡 +「翻篇·收容所」「翻篇·归零」…
// (id 形如 char_01v1 / char_01v4)。按卡平铺显示,用户看到的就是"怎么多出来一堆新角色";
// 而这些卡**本来就是同一个人**,只是换了一身衣服/一个状态。
//
// 判据由后端下发(pj.char_families,与 pj.char_kinds 同一手法)——前端**不自己抄一份**
// "名字前缀 / id 的 vK 后缀"的规则:抄一份必然与后端漂移,而漂移的表现是"界面显示成两个人、
// 渲染按一个人派生",最难查。后端没下发时(老版本/无方案)按"各自成组"退化成原样显示。
const charGroups = computed(() => {
  const fams = pj.value?.char_families || {}
  const groups = new Map()
  for (const c of charsWithAsset.value) {
    const info = fams[c.id] || {}
    const key = info.family || ('solo:' + c.id)
    let g = groups.get(key)
    if (!g) { g = { key, base: info.base || c.name, items: [] }; groups.set(key, g) }
    g.items.push({ ...c, variant: info.variant || '', master: !!info.master })
  }
  for (const g of groups.values()) {
    // 组内主卡在前(与资产阶段的生成顺序一致:主卡先出图,变体从它派生)
    g.items.sort((a, b) => (b.master ? 1 : 0) - (a.master ? 1 : 0))
  }
  return [...groups.values()]
})

// charPeople 这一集有几位**角色**(造型档算同一个人);charCards 是给模板的扁平列表:
// 多档的人前面插一行分组头,单人(绝大多数)不插 —— 观感与原先完全一致。
const charPeople = computed(() => charGroups.value.length)
const charCards = computed(() => {
  const out = []
  for (const g of charGroups.value) {
    if (g.items.length > 1) out.push({ head: true, key: 'head:' + g.key, base: g.base, count: g.items.length })
    for (const it of g.items) out.push({ head: false, key: it.id, c: it })
  }
  return out
})

// 进度环状态色:运行=琥珀呼吸 / 全完成=绿 / 有失败=红
const ringColor = computed(() => {
  const failed = Object.values(pj.value?.manifest || {}).some(m => m.status === 'failed')
  if (failed) return 'var(--mx-red)'
  if (ringPct.value >= 100) return 'var(--mx-green)'
  return 'url(#ringGrad)'
})
const ringPct = computed(() => totalShots.value ? Math.round(doneShots.value * 100 / totalShots.value) : 0)
const ringDash = computed(() => {
  const C = 2 * Math.PI * 21
  const p = ringPct.value / 100
  return `${(C * p).toFixed(1)} ${C.toFixed(1)}`
})

async function saveSource() {
  try {
    await api.saveSource(pj.value.project.id, sourceText.value)
    ElMessage.success('源文本已保存')
  } catch (e) { ElMessage.error(e.message) }
}

// 导入 txt/md:读入文件文本填入源文本
function onImportFile(ev) {
  const f = ev.target.files?.[0]
  ev.target.value = ''
  if (!f) return
  if (f.size > 8 << 20) { ElMessage.error('文件过大(>8MB)'); return }
  const reader = new FileReader()
  reader.onload = async () => {
    sourceText.value = String(reader.result || '')
    try {
      await api.saveSource(pj.value.project.id, sourceText.value)
      ElMessage.success(`已导入「${f.name}」(${sourceText.value.length} 字)并保存`)
    } catch (e) { ElMessage.success(`已导入「${f.name}」(记得点保存)`) }
  }
  reader.readAsText(f, 'utf-8')
}

async function runPhase(phase) {
  try {
    await api.run(pj.value.project.id, phase)
    ElMessage.success('已启动')
    refreshProject()
  } catch (e) { ElMessage.error(e.message) }
}

function upload(kind, id, name) {
  const input = document.createElement('input')
  input.type = 'file'
  input.accept = 'image/png,image/jpeg,image/webp'
  input.onchange = async () => {
    const f = input.files?.[0]
    if (!f) return
    try {
      await api.uploadAsset(pj.value.project.id, kind, id, name, f)
      ElMessage.success(`「${name}」参考图已上传`)
      refreshProject()
    } catch (e) { ElMessage.error(e.message) }
  }
  input.click()
}

async function removeAsset(assetId) {
  const label = assetId.startsWith('char:') ? '该角色的定妆照' : '该场景的参考图'
  try {
    await ElMessageBox.confirm(
      `将删除${label}(资产)并同步移除跨项目复用?项目内其它引用同步失效,可随时重新上传/生成。`, '删除资产',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  try {
    await api.deleteAsset(pj.value.project.id, assetId)
    ElMessage.success('已删除')
    refreshProject()
  } catch (e) { ElMessage.error(e.message) }
}

async function openDir() {
  // 文件级定位:优先选中 2K/标准成片文件,无成片则打开 output 目录
  const rel = pj.value.output?.final_2k || pj.value.output?.final || 'output'
  try { await api.openExplorer(rel, pj.value.project.id) }
  catch (e) { ElMessage.error(e.message) }
}

// 打开资产所在目录(资源管理器选中该文件)
async function openAssetDir(file) {
  if (!file) return
  try { await api.openExplorer(file, pj.value.project.id) }
  catch (e) { ElMessage.error(e.message) }
}

// 资产大图预览
// ---- 视频截帧定妆:H3 成片形象优于生成式定妆照,直接截帧替换(与成片零偏差) ----
const frameDlg = reactive({ open: false, kind: '', assetId: '', name: '', shots: [], shot: 0, frames: [], pick: -1, busy: false })
function openFrameDlg(kind, assetId, name) {
  const plan = pj.value?.plan
  if (!plan) { ElMessage.warning('尚未生成分镜方案'); return }
  const charId = kind === 'characters' ? assetId.slice(5) : ''
  const shots = (plan.shots || []).filter(s => {
    const m = manifest.value[String(s.id)]
    if (!m || m.status !== 'done') return false
    if (charId) return (s.characters || []).includes(charId) // 只列该角色出场的已渲染镜头
    return true
  })
  if (!shots.length) { ElMessage.warning(kind === 'characters' ? '该角色还没有已渲染的镜头' : '还没有已渲染的镜头'); return }
  frameDlg.kind = kind; frameDlg.assetId = assetId; frameDlg.name = name
  frameDlg.shots = shots.map(s => ({ id: s.id, label: `镜 ${s.id} · ${Math.round((manifest.value[String(s.id)]?.duration || 7))} 秒` }))
  frameDlg.shot = shots[0].id; frameDlg.frames = []; frameDlg.pick = -1; frameDlg.open = true
  loadFramePreview()
}
async function loadFramePreview() {
  frameDlg.frames = []; frameDlg.pick = -1; frameDlg.busy = true
  try {
    const r = await api.assetFramePreview(pj.value.project.id, frameDlg.assetId, frameDlg.shot)
    frameDlg.frames = (r.frames || []).map(f => ({ url: api.fileURL(pj.value.project.id, f), ratio: [0.25, 0.5, 0.75][frameDlg.frames.length] }))
    if (frameDlg.frames.length) frameDlg.pick = 1 // 默认选中中间帧
  } catch (e) { ElMessage.error(e.message) } finally { frameDlg.busy = false }
}
async function applyFrame() {
  if (frameDlg.pick < 0) { ElMessage.warning('请先选择一帧'); return }
  frameDlg.busy = true
  try {
    await api.assetFrameApply(pj.value.project.id, frameDlg.assetId, frameDlg.shot, frameDlg.frames[frameDlg.pick].ratio)
    ElMessage.success(`「${frameDlg.name}」定妆已替换为视频截帧`)
    frameDlg.open = false
    refreshProject()
  } catch (e) { ElMessage.error(e.message) } finally { frameDlg.busy = false }
}
function previewAsset(name, file) {
  G.viewer = {
    index: 0,
    items: [{ type: 'image', src: api.fileURL(pj.value.project.id, file),
      title: name, meta: '角色定妆照', kindLabel: 'ASSET' }],
  }
}

const exporting = ref(false)
async function exportPack() {
  exporting.value = true
  try {
    const d = await api.exportPack(pj.value.project.id)
    ElMessage.success(`素材包已生成(${d.size_mb?.toFixed(1) || '?'}MB),见项目 output 目录`)
    openDir()
  } catch (e) { ElMessage.error(e.message) } finally { exporting.value = false }
}

async function upgrade2K() {
  try {
    await ElMessageBox.confirm(
      '将把 768P 成片送云端 In-context 再生成 2K(按量计费,约数分钟)。继续?', '云端 2K 定稿',
      { confirmButtonText: '升级', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  try {
    await api.upgrade2k(pj.value.project.id, 0)
    ElMessage.success('2K 定稿已提交,进度见底部日志')
  } catch (e) { ElMessage.error(e.message) }
}

// ---------- 单镜云端 2K 产物:独立导出文件(乙7 选项 A:只导出) ----------
//
// 这些文件落在 <项目>/clips/2k/shot_NNN_2k.mp4,是"付了云额度却看不见"的那一批:
// 合成只收 manifest.File,所以它们**不进镜头清单、不参与合成**,成片里这一镜仍是 768p。
// 这里给它们一个列得出、看得见、拿得走的入口 —— 并且把上面那句话写在界面里,
// 免得用户以为点了 2K 就等于成片变 2K。
const exports2k = reactive({ open: false, busy: false, items: [] })
async function openExports2k() {
  exports2k.open = true
  exports2k.busy = true
  try {
    exports2k.items = await api.outputs2k(pj.value.project.id)
  } catch (e) { ElMessage.error('2K 产物读取失败:' + e.message) }
  finally { exports2k.busy = false }
}
// 预览:与其余产物走同一个全屏预览器(可拖动播放,走 Range)
function preview2k(it) {
  G.viewer = {
    index: 0,
    items: [{
      type: 'video', src: api.output2kURL(pj.value.project.id, it.name),
      title: `镜头 ${it.shot} · 2K 导出`, meta: `${it.size_mb.toFixed(1)}MB · ${it.updated_at}`,
      foot: '这是独立导出文件:不进镜头清单、不参与合成,成片这一镜仍为 768p。',
      kindLabel: '2K',
    }],
  }
}

const totalShots = computed(() => plan.value?.shots?.length || 0)
const doneShots = computed(() =>
  Object.values(manifest.value).filter(m => m.status === 'done').length)
</script>

<template>
  <div class="flex h-full min-h-0">
    <!-- 左:项目主体 -->
    <div class="flex-1 min-w-0 overflow-y-auto">
      <div class="p-4 pb-0">
        <div class="flex items-center gap-3 mb-3">
          <el-button text size="small" @click="G.view='home'; G.project=null">
            <el-icon><ArrowLeft /></el-icon>项目
          </el-button>
          <h2 class="text-base font-bold m-0">{{ pj.project.name }}</h2>
          <el-tag v-if="plan" size="small" effect="plain">《{{ plan.title }}》</el-tag>
          <span v-if="running" class="flex items-center gap-1.5 text-xs" style="color:var(--mx-amber)">
            <i class="dot run"></i>{{ phaseLabel(pj.project.phase) }}运行中
          </span>
          <div class="flex-1"></div>
          <!-- 产线进度环:渐变描边 + 中心百分比 -->
          <div v-if="totalShots" class="ring-wrap" :title="`镜头完成 ${doneShots}/${totalShots}`">
            <svg width="52" height="52" viewBox="0 0 52 52" class="ring-svg">
              <defs>
                <linearGradient id="ringGrad" x1="0" y1="0" x2="1" y2="1">
                  <stop offset="0%" stop-color="var(--mx-amber-hi)" />
                  <stop offset="100%" stop-color="var(--mx-amber-deep)" />
                </linearGradient>
              </defs>
              <circle cx="26" cy="26" r="21" fill="none" stroke="var(--mx-line)" stroke-width="4.5" />
              <circle cx="26" cy="26" r="21" fill="none" :stroke="ringColor" stroke-width="4.5"
                      stroke-linecap="round" :stroke-dasharray="ringDash" transform="rotate(-90 26 26)"
                      style="transition: stroke-dasharray .6s ease; filter: drop-shadow(0 0 4px color-mix(in srgb, var(--mx-amber) 45%, transparent))" />
              <text x="26" y="30" text-anchor="middle" class="ring-pct">{{ ringPct }}</text>
            </svg>
            <span class="ring-label">{{ doneShots }}/{{ totalShots }} 镜</span>
          </div>
        </div>
      </div>

    <!-- Tabs -->
    <div class="p-4">
      <el-tabs v-model="tab" class="mx-tabs">
        <!-- 剧本 -->
        <el-tab-pane label="剧本" name="script">
          <div class="grid gap-4" style="grid-template-columns: 1fr 340px">
            <div class="panel p-4 flex flex-col" style="min-height: 420px">
              <div class="flex items-center gap-2 mb-2">
                <b>源文本</b><span class="text-xs" style="color:var(--mx-text2)">故事 / 创意 / 剧本大纲(中文)</span>
                <div class="flex-1"></div>
                <input ref="importInput" type="file" accept=".txt,.md" hidden @change="onImportFile" />
                <el-button size="small" @click="importInput?.click()">
                  <el-icon class="mr-1"><Upload /></el-icon>导入 txt / md
                </el-button>
                <el-button size="small" type="primary" plain @click="aiDlg = true">
                  <el-icon class="mr-1"><MagicStick /></el-icon>AI 现场创作
                </el-button>
                <el-tooltip content="保存源文本(「生成分镜方案」的输入)" placement="top">
                  <el-button size="small" @click="saveSource">保存</el-button>
                </el-tooltip>
              </div>
              <el-input v-model="sourceText" type="textarea" :rows="16" resize="none"
                        placeholder="写下你的故事。例如:深夜的修仙都市,外卖员林凡接到一单送往云顶天宫的订单…&#10;&#10;LLM 将据此直出分镜与完整 H3 提示词(六段式/三段式,对白原生配音)。"
                        style="flex:1" :input-style="{ fontFamily: 'inherit' }" />
            </div>
            <div class="flex flex-col gap-4">
              <div class="panel p-4">
                <b class="text-sm">运行「剧本分镜」</b>
                <p class="text-xs mt-2 mb-3" style="color:var(--mx-text2)">
                  LLM 直出:角色卡 / 场景卡 / 分镜表 + 每镜完整 H3 提示词(对白逐字保留、原生配音)。
                </p>
                <el-tooltip content="LLM 直出角色卡/场景卡/分镜表与每镜 H3 提示词(已有方案会被跳过)" placement="top">
                  <el-button type="primary" :disabled="running" @click="runPhase('script')">
                    <el-icon class="mr-1"><EditPen /></el-icon>生成分镜方案
                  </el-button>
                </el-tooltip>
              </div>
              <div v-if="plan" class="panel p-4 text-sm flex flex-col gap-2">
                <div><b style="color:var(--mx-amber)">《{{ plan.title }}》</b></div>
                <div style="color:var(--mx-text2)">{{ plan.logline }}</div>
                <div class="timecode" style="word-break:break-all">{{ plan.style }}</div>
              </div>
            </div>
          </div>
        </el-tab-pane>

        <!-- 分镜故事板 -->
        <el-tab-pane :label="`分镜(${totalShots})`" name="storyboard" :disabled="!plan">
          <div class="grid gap-5" style="grid-template-columns:repeat(auto-fill,minmax(230px,1fr))">
            <ShotCard v-for="s in plan?.shots || []" :key="s.id"
                      :shot="s" :manifest-item="manifest[String(s.id)]" />
          </div>
        </el-tab-pane>

        <!-- 资产 -->
        <el-tab-pane label="资产" name="assets" :disabled="!plan">
          <div class="flex flex-col gap-6">
            <section>
              <h3 class="text-sm font-bold mb-3 flex items-center gap-2">
                <el-icon><User /></el-icon>角色定妆照
                <span class="text-xs font-normal" style="color:var(--mx-text2)">
                  {{ charPeople }} 位角色<template v-if="charsWithAsset.length !== charPeople"> · {{ charsWithAsset.length }} 张造型卡(同一角色的多档算一个人)</template>
                  · 半身正脸构图 · 锁脸参考(Ref2VA &lt;Picture 1&gt;)
                </span>
              </h3>
              <div class="grid gap-5" style="grid-template-columns:repeat(auto-fill,minmax(180px,1fr))">
                <template v-for="row in charCards" :key="row.key">
                  <!-- 同一个人的第 2..N 个造型档:插一行分组头,并说清"这是一个人、按场切换" -->
                  <div v-if="row.head"
                       style="grid-column:1/-1;display:flex;align-items:center;gap:8px;font-size:12px;color:var(--mx-text2);padding:2px 0 0">
                    <el-icon><User /></el-icon>
                    <b>「{{ row.base }}」</b>
                    <span>{{ row.count }} 个造型档 —— 同一个人按场切换的服装/状态,不是 {{ row.count }} 个新角色</span>
                  </div>
                  <div v-else class="shot-card p-3 flex flex-col gap-2">
                    <div class="flex items-center gap-2">
                      <b class="text-sm truncate">{{ row.c.name }}</b>
                      <el-tag v-if="row.c.variant" size="small" effect="plain" type="info">{{ row.c.variant }}</el-tag>
                      <el-tag v-if="row.c.asset?.source === 'library'" size="small" type="success" effect="plain">库复用</el-tag>
                      <el-tag v-else size="small" :type="sourceTone(row.c.asset?.source)" effect="plain">{{ sourceLabel(row.c.asset?.source) }}</el-tag>
                      <i class="dot" :class="row.c.asset ? 'ok' : 'warn'"></i>
                    </div>
                    <div style="aspect-ratio:3/4;background:var(--mx-bg2);border-radius:8px;overflow:hidden" class="relative flex items-center justify-center cursor-pointer"
                         :title="'点击上传「' + row.c.name + '」定妆照(png/jpg/webp);同名资产自动复用库'" @click="upload('characters', 'char:'+row.c.id, row.c.name)">
                      <img v-if="row.c.asset" :src="api.fileURL(pj.project.id, row.c.asset.file)"
                           style="width:100%;height:100%;object-fit:cover" />
                      <div v-else class="text-center text-xs p-2" style="color:var(--mx-text2)">
                        <el-icon :size="22"><Plus /></el-icon><div class="mt-1">上传定妆照</div>
                      </div>
                      <div v-if="row.c.asset" class="cover-acts">
                        <button class="cover-act" title="从视频截帧定妆(与成片形象一致)" @click.stop="openFrameDlg('characters', 'char:'+row.c.id, row.c.name)">
                          <el-icon :size="13"><Camera /></el-icon>
                        </button>
                        <button class="cover-act" title="预览大图" @click.stop="previewAsset(row.c.name, row.c.asset.file)">
                          <el-icon :size="13"><ZoomIn /></el-icon>
                        </button>
                        <button class="cover-act" title="打开所在目录" @click.stop="openAssetDir(row.c.asset.file)">
                          <el-icon :size="13"><FolderOpened /></el-icon>
                        </button>
                        <button class="cover-act danger" title="删除定妆照" @click.stop="removeAsset('char:'+row.c.id)">
                          <el-icon :size="13"><Delete /></el-icon>
                        </button>
                      </div>
                    </div>
                    <div class="text-xs" style="color:var(--mx-text2)">{{ row.c.description }}</div>
                  </div>
                </template>
              </div>
            </section>
            <section>
              <h3 class="text-sm font-bold mb-3 flex items-center gap-2">
                <el-icon><MapLocation /></el-icon>场景参考图
                <span class="text-xs font-normal" style="color:var(--mx-text2)">空镜锚定 &amp; 氛围参考(&lt;Picture 2&gt;)</span>
              </h3>
              <div class="grid gap-5" style="grid-template-columns:repeat(auto-fill,minmax(220px,1fr))">
                <div v-for="s in scenesWithAsset" :key="s.id" class="shot-card p-3 flex flex-col gap-2">
                  <div class="flex items-center gap-2">
                    <b class="text-sm truncate">{{ s.name }}</b>
                    <el-tag v-if="s.asset?.source === 'library'" size="small" type="success" effect="plain">库复用</el-tag>
                    <el-tag v-else size="small" :type="sourceTone(s.asset?.source)" effect="plain">{{ sourceLabel(s.asset?.source) }}</el-tag>
                    <i class="dot" :class="s.asset ? 'ok' : 'warn'"></i>
                  </div>
                  <div style="aspect-ratio:16/9;background:var(--mx-bg2);border-radius:8px;overflow:hidden" class="relative flex items-center justify-center cursor-pointer"
                       :title="'点击上传「' + s.name + '」场景参考图(png/jpg/webp)'" @click="upload('scenes', 'scene:'+s.id, s.name)">
                    <img v-if="s.asset" :src="api.fileURL(pj.project.id, s.asset.file)"
                         style="width:100%;height:100%;object-fit:cover" />
                    <div v-else class="text-center text-xs p-2" style="color:var(--mx-text2)">
                      <el-icon :size="22"><Plus /></el-icon><div class="mt-1">上传场景图</div>
                    </div>
                    <div v-if="s.asset" class="cover-acts">
                      <button class="cover-act" title="从视频截帧定妆(与成片形象一致)" @click.stop="openFrameDlg('scenes', 'scene:'+s.id, s.name)">
                        <el-icon :size="13"><Camera /></el-icon>
                      </button>
                      <button class="cover-act" title="删除场景图" @click.stop="removeAsset('scene:'+s.id)">
                        <el-icon :size="13"><Delete /></el-icon>
                      </button>
                    </div>
                  </div>
                  <div class="text-xs" style="color:var(--mx-text2)">{{ s.description }}</div>
                </div>
              </div>
            </section>

            <!-- 道具图（2026-09-27 用户：「工作台中的资产也要展示道具」）。
                 口径要说清，否则用户会问"为什么这件道具没进参考图"：
                 道具图是**人审资产**（1:1 白底单件，核对这件东西跨镜长得一不一样），
                 **不进 H3 的参考图集合** —— 官方那句 "Props never get a picture; describe
                 props in text only" 说的是参考图顺序，refs.go 也只带角色与场景。
                 生成侧 2026-09-27 才补上出图（此前这一类只被读进方案、从没出过图）。 -->
            <section>
              <h3 class="text-sm font-bold mb-3 flex items-center gap-2">
                <el-icon><Suitcase /></el-icon>道具图
                <span class="text-xs font-normal" style="color:var(--mx-text2)">
                  {{ propsWithAsset.length }} 件 · 1:1 白底单件 ·
                  人审与跨镜一致性核对用<span style="color:var(--mx-text3)">（不进 H3 参考图：官方口径是道具只写文字）</span>
                </span>
              </h3>
              <div v-if="propsWithAsset.length" class="grid gap-5"
                   style="grid-template-columns:repeat(auto-fill,minmax(160px,1fr))">
                <div v-for="p in propsWithAsset" :key="p.id" class="shot-card p-3 flex flex-col gap-2">
                  <div class="flex items-center gap-2">
                    <b class="text-sm truncate">{{ p.name }}</b>
                    <el-tag v-if="p.asset?.source === 'library'" size="small" type="success" effect="plain">库复用</el-tag>
                    <el-tag v-else-if="p.asset" size="small" :type="sourceTone(p.asset?.source)" effect="plain">{{ sourceLabel(p.asset?.source) }}</el-tag>
                    <i class="dot" :class="p.asset ? 'ok' : 'warn'"></i>
                  </div>
                  <div style="aspect-ratio:1/1;background:var(--mx-bg2);border-radius:8px;overflow:hidden"
                       class="relative flex items-center justify-center cursor-pointer"
                       :title="'点击上传「' + p.name + '」道具图(png/jpg/webp);同名资产自动复用库'"
                       @click="upload('props', 'prop:'+p.id, p.name)">
                    <img v-if="p.asset" :src="api.fileURL(pj.project.id, p.asset.file)"
                         style="width:100%;height:100%;object-fit:cover" />
                    <div v-else class="text-center text-xs p-2" style="color:var(--mx-text2)">
                      <el-icon :size="22"><Plus /></el-icon><div class="mt-1">上传道具图</div>
                    </div>
                    <div v-if="p.asset" class="cover-acts">
                      <button class="cover-act" title="预览大图" @click.stop="previewAsset(p.name, p.asset.file)">
                        <el-icon :size="13"><ZoomIn /></el-icon>
                      </button>
                      <button class="cover-act" title="打开所在目录" @click.stop="openAssetDir(p.asset.file)">
                        <el-icon :size="13"><FolderOpened /></el-icon>
                      </button>
                      <button class="cover-act danger" title="删除道具图" @click.stop="removeAsset('prop:'+p.id)">
                        <el-icon :size="13"><Delete /></el-icon>
                      </button>
                    </div>
                  </div>
                  <div class="text-xs" style="color:var(--mx-text2)">{{ p.description }}</div>
                  <div v-if="p.en" class="text-xs timecode truncate" :title="p.en">{{ p.en }}</div>
                </div>
              </div>
              <div v-else class="text-xs" style="color:var(--mx-text3)">
                这份方案里没有道具卡（技能侧的结构化明细没给 props，或这是更早的方案）。
                道具图也可以直接在「资产库 → 道具」里入库，新项目遇到同名道具会自动复用、免生成。
              </div>
            </section>
          </div>
        </el-tab-pane>

        <!-- 成片 -->
        <el-tab-pane label="成片" name="output">
          <div class="grid gap-4" style="grid-template-columns:minmax(300px,420px) 1fr">
            <div class="panel p-4">
              <div v-if="pj.output?.ready_2k || pj.output?.ready">
                <video v-if="pj.output?.ready_2k"
                       :src="api.fileURL(pj.project.id, pj.output.final_2k) + '?v=' + (pj.project.updated_at||'')"
                       controls playsinline
                       class="w-full rounded-lg bg-black" style="aspect-ratio:9/16" />
                <video v-else
                       :src="api.fileURL(pj.project.id, pj.output.final) + '?v=' + (pj.project.updated_at||'')"
                       controls playsinline
                       class="w-full rounded-lg bg-black" style="aspect-ratio:9/16" />
                <div class="mt-2 text-center">
                  <el-tag v-if="pj.output?.ready_2k" size="small" type="warning" effect="dark">2K 定稿版</el-tag>
                  <el-tag v-else size="small" effect="plain">768P 标准版</el-tag>
                </div>
              </div>
              <div v-else class="text-xs p-10 text-center" style="color:var(--mx-text2)">
                渲染完成后,运行「合成成片」阶段即可在此预览与导出
              </div>
            </div>
            <div class="flex flex-col gap-4">
              <div class="panel p-4">
                <b class="text-sm">合成参数</b>
                <p class="text-xs mt-2" style="color:var(--mx-text2)">
                  转场 / 字幕 / 响度可在管线卡片「质检」「合成成片」的 ⚙ 调优入口修改(项目级独立生效)。
                </p>
                <div class="flex gap-2 mt-3 flex-wrap">
                  <el-tooltip content="拼接镜头+烧录字幕+响度归一,输出 output/final.mp4" placement="top">
                    <el-button type="primary" :disabled="running || !totalShots" @click="runPhase('assemble')">
                      <el-icon class="mr-1"><Film /></el-icon>合成成片
                    </el-button>
                  </el-tooltip>
                  <el-tooltip content="时长/音轨/亮度机械质检(黑屏防线),报告在下方" placement="top">
                    <el-button :disabled="running || !totalShots" @click="runPhase('qc')">
                      <el-icon class="mr-1"><CircleCheck /></el-icon>质检
                    </el-button>
                  </el-tooltip>
                  <el-tooltip content="本地 768P 成片送云端 regeneration 原生重生成 2K(消耗云额度)" placement="top">
                    <el-button type="warning" plain :disabled="running || !pj.output?.ready" @click="upgrade2K">
                      <el-icon class="mr-1"><MagicStick /></el-icon>升级 2K
                    </el-button>
                  </el-tooltip>
                  <el-tooltip content="单镜云端 2K 产物(clips/2k):独立导出文件,可预览/下载;不进镜头清单、不参与合成" placement="top">
                    <el-button plain @click="openExports2k">
                      <el-icon class="mr-1"><Download /></el-icon>2K 导出
                    </el-button>
                  </el-tooltip>
                  <el-tooltip content="打包逐镜片段+成片+字幕 zip(剪映导入友好)到项目 output 目录" placement="top">
                    <el-button :disabled="!totalShots" :loading="exporting" @click="exportPack">
                      <el-icon class="mr-1"><Suitcase /></el-icon>导出剪辑素材包
                    </el-button>
                  </el-tooltip>
                  <el-tooltip content="在资源管理器中打开项目 output 目录(优先选中成片)" placement="top">
                    <el-button @click="openDir"><el-icon class="mr-1"><FolderOpened /></el-icon>打开目录</el-button>
                  </el-tooltip>
                </div>
              </div>
              <div v-if="pj.qc" class="panel p-4">
                <b class="text-sm">质检报告</b>
                <div class="mt-2 flex flex-col gap-1 text-xs">
                  <div v-for="(r, k) in pj.qc" :key="k" class="flex items-center gap-2">
                    <i class="dot" :class="r.ok ? 'ok' : 'bad'"></i>
                    <span>镜头 {{ k }}</span>
                    <span style="color:var(--mx-text2)">{{ r.duration.toFixed(1) }}s · 暗像素 {{ (r.dark_ratio*100).toFixed(0) }}% · {{ r.has_audio ? '有音轨' : '无音轨' }}</span>
                    <span v-if="!r.ok" style="color:var(--mx-red)">{{ (r.problems||[]).join(';') }}</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </el-tab-pane>
      </el-tabs>
    </div>
    </div>

    <!-- AI 现场创作:按需求即时发挥 -->
    <el-dialog v-model="aiDlg" title="AI 现场创作故事" width="520px" append-to-body>
      <el-input v-model="aiPrompt" type="textarea" :rows="4" maxlength="300" show-word-limit
        placeholder="一句话或一个点子,如:修仙界开网约车,专渡雷劫,接单的是个社恐剑仙" />
      <div class="text-xs mt-2" style="color:var(--mx-text3)">
        深度推理模型创作约需 1-3 分钟(日志有实时心跳);完成 后自动写入源文本并保存。
      </div>
      <template #footer>
        <el-button @click="aiDlg = false">取消</el-button>
        <el-button type="primary" :loading="aiBusy" :disabled="!aiPrompt.trim()" @click="aiCompose">
          {{ aiBusy ? '创作中(1-3 分钟)…' : '开始创作' }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 右:悬浮产线(管线节点竖排) -->
    <aside class="pipe-rail-wrap">
      <PipelineBar :phase="pj.project.phase || ''" />
    </aside>
  </div>

    <!-- 视频截帧定妆 -->
    <el-dialog v-model="frameDlg.open" :title="`视频截帧定妆 · ${frameDlg.name}`" width="560px" append-to-body>
      <div class="flex items-center gap-3 mb-4">
        <span class="text-xs" style="color:var(--mx-text2)">选择镜头</span>
        <el-select v-model="frameDlg.shot" size="small" style="width:180px" @change="loadFramePreview">
          <el-option v-for="s in frameDlg.shots" :key="s.id" :value="s.id" :label="s.label" />
        </el-select>
        <span class="text-xs" style="color:var(--mx-text3)">H3 成片形象直接锚定为定妆照,后续集复用天然一致</span>
      </div>
      <div v-loading="frameDlg.busy" class="grid gap-3" style="grid-template-columns:repeat(3,1fr);min-height:200px">
        <div v-for="(f, i) in frameDlg.frames" :key="i"
             class="cursor-pointer rounded-lg overflow-hidden relative" style="border:2px solid var(--mx-line)"
             :style="frameDlg.pick === i ? 'border-color:var(--mx-accent, var(--mx-amber))' : ''" @click="frameDlg.pick = i">
          <img :src="f.url + '?t=' + Date.now()" style="width:100%;aspect-ratio:3/4;object-fit:cover;display:block" />
          <div class="absolute bottom-0 left-0 right-0 text-center text-xs py-0.5" style="background:color-mix(in srgb, var(--mx-bg0) 58%, transparent);color:var(--mx-text)">{{ Math.round(f.ratio * 100) }}%</div>
        </div>
        <div v-if="!frameDlg.busy && !frameDlg.frames.length" class="col-span-3 text-center text-xs py-10" style="color:var(--mx-text3)">截帧中…</div>
      </div>
      <template #footer>
        <el-button @click="frameDlg.open = false">取消</el-button>
        <el-button type="primary" :disabled="frameDlg.pick < 0" :loading="frameDlg.busy" @click="applyFrame">替换定妆并入库</el-button>
      </template>
    </el-dialog>

    <!-- 单镜云端 2K 产物 · 导出(不进成片,成片这一镜仍是 768p) -->
    <el-dialog v-model="exports2k.open" title="单镜云端 2K 产物 · 导出" width="640px" append-to-body>
      <el-alert type="warning" :closable="false" class="mb-4"
        title="这是独立导出文件:不进镜头清单、不参与合成,成片这一镜仍为 768p。" />
      <div class="text-xs mb-3" style="color:var(--mx-text2)">
        在分镜卡或渲染详情里点「云端 2K 定稿(需先渲出)」才会生成这种文件,
        落盘在项目的 clips/2k/ 目录下,按镜号命名(shot_NNN_2k.mp4)。
        它只服务于"把某一镜单独拿去用"(剪辑/投稿/比对),不会改变成片里那一镜的分辨率。
      </div>
      <div v-loading="exports2k.busy" style="min-height:120px">
        <div v-if="!exports2k.busy && !exports2k.items.length" class="text-xs py-8 text-center" style="color:var(--mx-text3)">
          还没有 2K 产物。在分镜卡里对已渲染完成的镜头点「云端 2K 定稿」,完成后这里就会出现。
        </div>
        <div v-for="it in exports2k.items" :key="it.name" class="panel p-3 flex items-center gap-3 mb-2">
          <el-icon :size="20" color="var(--mx-purple)"><MagicStick /></el-icon>
          <div class="flex-1 min-w-0">
            <div class="text-sm">镜头 {{ it.shot }} <span class="timecode">{{ it.name }}</span></div>
            <div class="timecode">{{ it.size_mb.toFixed(1) }}MB · {{ it.updated_at }}</div>
          </div>
          <el-button size="small" text @click="preview2k(it)"><el-icon><View /></el-icon></el-button>
          <!-- 下载用 tag="a":直接交给浏览器的原生下载(带 Content-Disposition 语义的
               download 属性),而不是在 JS 里造 Blob —— 产物可能上百 MB,不该先读进内存。 -->
          <el-button size="small" text tag="a" :href="api.output2kURL(pj.project.id, it.name)"
                     :download="it.name" title="下载该文件"><el-icon><Download /></el-icon></el-button>
        </div>
      </div>
      <template #footer>
        <el-button @click="exports2k.open = false">关闭</el-button>
      </template>
    </el-dialog>
</template>
