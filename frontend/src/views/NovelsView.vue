<!-- 小说库:扫描设置目录,书卡 + 弹窗(章节文件管理 + 正文阅读) + 一键视频创作 -->
<script setup>
import { G, api, refreshProjects } from '../store'
import { ref, computed, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox, ElLoading } from 'element-plus'
import ChapterTree from '../components/ChapterTree.vue'

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

// 一键视频创作(带重复验证,后端 409)
const producing = ref(false)
async function produce(n) {
  try {
    await ElMessageBox.confirm(
      `将《${n.name}》整本拆集成多集短视频并自动开始批量产线?(拆集 2-6 分钟,日志有实时心跳)`,
      '一键视频创作', { confirmButtonText: '开始创作', cancelButtonText: '取消', type: 'info' })
  } catch { return }
  producing.value = true
  try {
    const r = await api.novelProduce(n.path, 0)
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

// 脚本创作:直接导入技能端已产出的脚本包(manifest.json + plan/plan.json),
// **不走 LLM 拆集** —— 平台侧那一步单集 2-6 分钟深度推理,token 与时间全省。
// 与「视频制作」是两个人**口**:这个入口找不到包就明确报错,不悄悄回落到 LLM。
async function produceScript(n) {
  const ep = n.script?.episodes || 0
  try {
    await ElMessageBox.confirm(
      `将把《${n.name}》目录里已产出的脚本包直接导入(${ep} 集)并**自动开始批量产线**(资产定妆→渲染→质检→成片),不再跑 AI 拆集。` +
      `\n导入后到「项目」页即可开始资产与渲染;包不合契约会被整批拒收,不会留下半成品。`,
      '脚本创作(跳过 AI 拆集)', { confirmButtonText: '导入并开始生产', cancelButtonText: '取消', type: 'info' })
  } catch { return }
  try {
    const r = await api.novelProduceScript(n.path)
    const cnt = Array.isArray(r?.results) ? r.results.length : ep
    ElMessage.success(r?.message || `已导入 ${cnt} 集脚本,批量产线已启动`)
    await refresh()
    await refreshProjects()
    G.view = 'home'
  } catch (e) {
    ElMessage.error(e.message) // 契约拒收 / 找不到包:原样展示后端那句话
  }
}

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

// 卡片右上菜单的命令出口。收成一个出口而不是三个 @click:
// 以后再加动作只改这里 + 菜单项,卡片模板不必再堆@click.stop。
function onCardCmd(cmd, n) {
  if (cmd === 'script') return produceScript(n)
  if (cmd === 'produce') return produce(n)
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
          <el-tag v-if="n.script" size="small" type="warning" effect="dark" class="absolute top-2 left-2">
            脚本 {{ n.script.episodes }} 集
          </el-tag>
          <el-tag v-else-if="n.produced" size="small" type="success" effect="dark" class="absolute top-2 left-2">已创作</el-tag>
          <el-tag v-else-if="n.hidden" size="small" type="info" effect="dark" class="absolute top-2 left-2">已隐藏</el-tag>
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
                <el-dropdown-item command="script" :disabled="!n.script"
                                  :title="n.script ? '直接导入技能端已产出的脚本包(不再跑 AI 拆集)' : '这本书目录里没有技能端脚本包(需要 manifest.json + plan/plan.json)'">
                  <el-icon><Document /></el-icon>脚本创作{{ n.script ? `（${n.script.episodes} 集）` : '' }}
                </el-dropdown-item>
                <el-dropdown-item command="produce" divided :disabled="producing">
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
               width="880px" top="5vh" append-to-body>
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
            <span class="timecode" style="font-size:10px">点「用这张」替换现封面</span>
          </div>
          <div class="flex-1 overflow-y-auto tree-scroll">
            <div v-if="!covCands.length" class="text-xs py-8 text-center" style="color:var(--mx-text3)">
              还没有候选 —— 调好左边的参数,点「生成候选」
            </div>
            <div class="cand-grid">
              <div v-for="c in covCands" :key="c.file" class="cand-item">
                <img :src="candURL(c.file)" alt="" />
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
            <div v-else class="flex gap-3" :style="{ height: dlgH - 150 + 'px' }">
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
                </div>
                <div class="flex-1 overflow-y-auto tree-scroll">
                  <div v-for="s in scriptShots" :key="s.id" class="shot-row">
                    <div class="flex items-center gap-2">
                      <span class="timecode" style="min-width:38px">#{{ s.id }}</span>
                      <el-tag size="small" effect="plain">{{ s.size || '—' }}</el-tag>
                      <span class="timecode">{{ s.duration }}s</span>
                      <el-tag size="small" :type="s.mode === 'ref2va' ? 'warning' : 'info'" effect="plain">{{ s.mode }}</el-tag>
                    </div>
                    <div class="text-xs mt-1" style="white-space:pre-wrap">{{ s.action }}</div>
                    <div v-for="(d, i) in (s.dialogue || [])" :key="i" class="text-xs mt-1" style="color:var(--mx-amber)">💬 {{ d }}</div>
                    <div v-if="s.narration" class="text-xs mt-1" style="color:var(--mx-text3)">🎙 旁白:{{ s.narration }}</div>
                  </div>
                  <div v-if="!scriptShots.length" class="text-xs py-6 text-center" style="color:var(--mx-text3)">选一集看分镜</div>
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
        </el-tabs>
      </template>
      <template #footer>
        <el-button @click="dlg = null">关闭</el-button>
        <el-tooltip content="整本拆集成多集短视频项目并自动批量产线(拆集 2-6 分钟)" placement="top">
        <el-button type="primary" :loading="producing" :disabled="dlg && dlg.novel.produced"
                   @click="dlg && produce(dlg.novel)">
          {{ dlg && dlg.novel.produced ? '已创作(勿重复)' : '一键视频创作' }}
        </el-button>
        </el-tooltip>
      </template>
      <!-- 右下角拖动柄:改弹窗宽高(受窗口边界约束) -->
      <div class="novel-dlg-grip" :class="{ on: resizing }" title="拖动调整弹窗大小" @mousedown="startResize"></div>
    </el-dialog>
  </div>
</template>
