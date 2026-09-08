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

// 弹窗:阅读详情
const dlg = ref(null)       // {novel, chapters}
const reading = ref(null)   // {name, text, truncated, chars}
const readBusy = ref(false)
async function openDetail(n) {
  try {
    const r = await fetch('/api/novels/detail?path=' + encodeURIComponent(n.path)).then(x => x.json())
    if (!r.novel) throw new Error(r.error || '读取失败')
    dlg.value = r
    reading.value = null
  } catch (e) { ElMessage.error(e.message) }
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
        <div class="grid gap-3" style="grid-template-columns:280px 1fr;min-height:380px">
          <!-- 左:章节文件 -->
          <div class="flex flex-col gap-1.5" style="max-height:56vh;overflow-y:auto">
            <div v-for="c in dlg.chapters" :key="c.path"
                 class="flex items-center gap-2 px-2.5 py-2 rounded-lg cursor-pointer ep-row"
                 :class="{ 'sel-on': reading && reading.name === c.name }"
                 @click="readChapter(c)">
              <el-icon :size="13" color="var(--mx-text3)"><Document /></el-icon>
              <span class="text-xs flex-1 truncate">{{ c.name }}</span>
              <span class="timecode" style="font-size:10px">{{ c.kb }}KB</span>
            </div>
          </div>
          <!-- 右:正文阅读 -->
          <div class="panel p-4 flex flex-col" style="max-height:56vh">
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
              <span class="text-xs">点击左侧章节文件阅读正文</span>
            </div>
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
