<!-- 小说库:扫描设置目录,书卡 + 弹窗(章节文件管理 + 正文阅读) + 一键视频创作 -->
<script setup>
import { G, api, refreshProjects } from '../store'
import { ref, computed, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const root = ref('')
const hint = ref('')
const novels = ref([])
const loading = ref(false)

async function refresh() {
  loading.value = true
  try {
    const r = await fetch('/api/novels').then(x => x.json())
    root.value = r.root || ''
    hint.value = r.hint || ''
    novels.value = r.novels || []
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
        node = { type: 'dir', name: seg, key: path, children: [] }
        cur.push(node)
      }
      cur = node.children
    }
    cur.push({ type: 'file', name: file, ch })
  }
  // 排序:目录在前,同级按名
  const sortRec = (arr) => {
    arr.sort((a, b) => (a.type === b.type) ? a.name.localeCompare(b.name, 'zh')
      : (a.type === 'dir' ? -1 : 1))
    arr.forEach(x => x.type === 'dir' && sortRec(x.children))
  }
  sortRec(tree)
  return tree
}
async function openDetail(n) {
  try {
    const r = await fetch('/api/novels/detail?path=' + encodeURIComponent(n.path)).then(x => x.json())
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
    const r = await fetch('/api/novels/read?path=' + encodeURIComponent(ch.path)).then(x => x.json())
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
    await fetch('/api/open', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: n.type === 'file' ? n.path : n.path, project: '' , absolute: true }),
    })
  } catch { /* /api/open 需要 project——后端走 DataDir 白名单;小说目录默认就在 data/Novel 下 */ }
}

const fmtChars = (n) => n >= 10000 ? (n / 10000).toFixed(1) + ' 万字' : n + ' 字'
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
        <div class="text-center"><div class="stat-num">{{ novels.length }}</div><div class="stat-label">小说</div></div>
        <el-button size="small" circle :loading="loading" @click="refresh"><el-icon><Refresh /></el-icon></el-button>
      </div>
    </div>

    <div v-if="!novels.length" class="panel p-14 text-center" style="color:var(--mx-text3)">
      目录里还没有小说 —— 放入小说文件夹(推荐结构:书名/全本/*.md + 书名/封面/*.png + 书名/正文/分卷章节)
    </div>

    <div class="grid gap-4" style="grid-template-columns:repeat(auto-fill,minmax(216px,1fr))">
      <div v-for="n in novels" :key="n.path" class="shot-card cursor-pointer" @click="openDetail(n)">
        <div class="relative" style="aspect-ratio:3/4;background:var(--mx-bg2);border-radius:10px 10px 0 0;overflow:hidden">
          <img v-if="n.cover" :src="'/api/novels/cover?path=' + encodeURIComponent(n.cover)"
               style="width:100%;height:100%;object-fit:cover" loading="lazy" />
          <div v-else class="absolute inset-0 flex flex-col items-center justify-center gap-2">
            <el-icon :size="34" color="var(--mx-text3)"><Reading /></el-icon>
            <span class="timecode">{{ n.type === 'dir' ? '文件夹' : '单文件' }}</span>
          </div>
          <el-tag v-if="n.produced" size="small" type="success" effect="dark" class="absolute top-2 right-2">已创作</el-tag>
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
              <template v-for="node in chapTree" :key="node.key || node.ch?.path">
                <!-- 目录节点(卷):收起/展开 -->
                <div v-if="node.type === 'dir'" class="tree-dir" @click="toggleDir(node.key)">
                  <el-icon :size="11" color="var(--mx-amber)"
                           :style="{ transform: expandedDirs.has(node.key) ? 'rotate(90deg)' : '' }"><CaretRight /></el-icon>
                  <span class="truncate">{{ node.name }}</span>
                  <span class="timecode" style="font-size:9px;margin-left:auto">{{ node.children.length }}</span>
                </div>
                <template v-if="node.type === 'dir' && expandedDirs.has(node.key)">
                  <div v-for="sub in node.children" :key="sub.key || sub.ch?.path">
                    <div v-if="sub.type === 'dir'" class="tree-dir" style="padding-left:26px" @click="toggleDir(sub.key)">
                      <el-icon :size="11" color="var(--mx-amber)"
                               :style="{ transform: expandedDirs.has(sub.key) ? 'rotate(90deg)' : '' }"><CaretRight /></el-icon>
                      <span class="truncate">{{ sub.name }}</span>
                    </div>
                    <div v-if="sub.type === 'dir' && expandedDirs.has(sub.key)"
                         v-for="lf in sub.children" :key="lf.ch.path"
                         class="tree-leaf" :class="{ on: reading && reading.name === lf.ch.name }"
                         @click="readChapter(lf.ch)">
                      <span class="truncate">{{ lf.name }}</span>
                      <span class="timecode" style="font-size:9px">{{ lf.ch.kb }}KB</span>
                    </div>
                    <div v-else-if="sub.type === 'file'"
                         class="tree-leaf" :class="{ on: reading && reading.name === sub.ch.name }"
                         @click="readChapter(sub.ch)">
                      <span class="truncate">{{ sub.name }}</span>
                      <span class="timecode" style="font-size:9px">{{ sub.ch.kb }}KB</span>
                    </div>
                  </div>
                </template>
                <!-- 顶层文件(全本等) -->
                <div v-else-if="node.type === 'file'"
                     class="tree-leaf" :class="{ on: reading && reading.name === node.ch.name }"
                     @click="readChapter(node.ch)">
                  <el-icon :size="11" color="var(--mx-text3)"><Document /></el-icon>
                  <span class="truncate">{{ node.name }}</span>
                  <span class="timecode" style="font-size:9px">{{ node.ch.kb }}KB</span>
                </div>
              </template>
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
        <el-button type="primary" :loading="producing" :disabled="dlg && dlg.novel.produced"
                   @click="dlg && produce(dlg.novel)">
          {{ dlg && dlg.novel.produced ? '已创作(勿重复)' : '一键视频创作' }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>
