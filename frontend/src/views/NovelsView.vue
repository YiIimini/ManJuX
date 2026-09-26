<!-- 小说库:扫描设置目录,书卡 + 弹窗(章节文件管理 + 正文阅读) + 一键视频创作 -->
<script setup>
import { G, api, refreshProjects } from '../store'
import { ref, computed, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ChapterTree from '../components/ChapterTree.vue'

const root = ref('')
const hint = ref('')
const novels = ref([])
const loading = ref(false)
// 隐藏作品(界面状态,存 sidecar;见后端 novels_hidden.go):默认不列出,这里给一个开关把它请回来
const withHidden = ref(false)
const hiddenCount = ref(0)

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
  } catch (e) { ElMessage.error(e.message) }
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

// 卡片右上菜单的命令出口。收成一个出口而不是三个 @click:
// 以后再加动作只改这里 + 菜单项,卡片模板不必再堆@click.stop。
function onCardCmd(cmd, n) {
  if (cmd === 'produce') return produce(n)
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
          <img v-if="n.cover" :src="'/api/novels/cover?path=' + encodeURIComponent(n.cover)"
               style="width:100%;height:100%;object-fit:cover" loading="lazy" />
          <div v-else class="absolute inset-0 flex flex-col items-center justify-center gap-2">
            <el-icon :size="34" color="var(--mx-text3)"><Reading /></el-icon>
            <span class="timecode">{{ n.type === 'dir' ? '文件夹' : '单文件' }}</span>
          </div>
          <el-tag v-if="n.produced" size="small" type="success" effect="dark" class="absolute top-2 left-2">已创作</el-tag>
          <el-tag v-else-if="n.hidden" size="small" type="info" effect="dark" class="absolute top-2 left-2">已隐藏</el-tag>
          <!-- 右上:作品菜单(视频制作 / 隐藏 / 删除)。
               为什么收成一个菜单:原先只有一个删除按钮,而"隐藏"是比"删除"更常用的动作 ——
               把不可逆的删除放在最容易误点的一格,本身就不对;菜单里删除仍是最后一项并带二次确认。 -->
          <el-dropdown trigger="click" placement="bottom-end" @command="(c) => onCardCmd(c, n)">
            <button class="cover-menu" title="作品菜单" @click.stop>
              <el-icon :size="13"><MoreFilled /></el-icon>
            </button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="produce" :disabled="producing">
                  <el-icon><VideoCamera /></el-icon>视频制作
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

    <!-- 阅读详情弹窗:章节文件管理 + 正文 -->
    <el-dialog :model-value="!!dlg" :title="dlg ? '《' + dlg.novel.name + '》· 阅读详情' : ''"
               width="860px" top="5vh" @update:model-value="dlg = null">
      <template v-if="dlg">
        <div class="flex items-center gap-3 mb-3 text-xs" style="color:var(--mx-text2)">
          <span>{{ fmtChars(dlg.novel.chars) }}</span>
          <span>· {{ dlg.chapters.length }} 个章节文件</span>
          <div class="flex-1"></div>
          <el-tag v-if="dlg.novel.produced" size="small" type="success">已创作(首页剧集合集)</el-tag>
        </div>
        <div class="flex gap-3" style="min-height:400px">
          <!-- 主区:正文阅读 -->
          <div class="panel p-4 flex flex-col flex-1" style="max-height:60vh;min-width:0">
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
          <div v-if="!navCollapsed" class="nav-tree flex flex-col" style="max-height:60vh">
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
    </el-dialog>
  </div>
</template>
