<script setup>
// 新建项目弹窗:三套互斥模式(单集文本/AI 现场创作、整本小说拆集)
// 弹窗内部只负责参数录入(含文件读取),创建/拆集的业务与 API 调用留在 HomeView
import { ref } from 'vue'
import { ElMessage } from 'element-plus'

const dlg = defineModel('dlg', { default: false })
const newName = defineModel('newName', { default: '' })
const pickedFile = defineModel('pickedFile', { default: null })     // {name,text,kind,plan?,preview}
const aiMode = defineModel('aiMode', { default: false })
const aiPrompt = defineModel('aiPrompt', { default: '' })
const novelMode = defineModel('novelMode', { default: false })
const novelText = defineModel('novelText', { default: '' })
const novelCover = defineModel('novelCover', { default: null })     // File:系列封面
const novelCoverPath = defineModel('novelCoverPath', { default: '' }) // 桌面拖放的本机封面路径

defineProps({
  // 注意:Vue 会把 :novel-cover-url 驼峰化为 novelCoverUrl,故此处不能写成
  // novelCoverURL(全大写缩写不匹配 → 属性静默变成 HTML attr,封面缩略图不显示)
  novelCoverUrl: { type: String, default: '' },
  busy: { type: Boolean, default: false },
  composing: { type: Boolean, default: false },
  splitting: { type: Boolean, default: false },
})
defineEmits(['create'])

// 弹窗内的原生文件输入(仅用于触发 .click(),数据回写 v-model)
const fileInput = ref(null)
const novelInput = ref(null)
const novelDirInput = ref(null)
const novelCoverInput = ref(null)

function onCoverNativeDrop(ev) {
  const p = ev.detail?.[0]
  if (!p) return
  if (!/\.(png|jpe?g|webp)$/i.test(p)) { ElMessage.error('仅支持 png/jpg/webp'); return }
  novelCoverPath.value = p
  novelCover.value = null
}
function onPickNovel(ev) {
  const f = ev.target.files?.[0]
  ev.target.value = ''
  if (!f) return
  if (f.size > 20 << 20) { ElMessage.error('文件过大(>20MB)'); return }
  const rd = new FileReader()
  rd.onload = () => { novelText.value = String(rd.result || '') }
  rd.readAsText(f, 'utf-8')
}
// 目录导入:合并目录内 txt/md 章节(按路径排序);
// 路径含「封面」目录的图片自动识别为系列封面(选包含小说与封面文件夹的上层目录即可)。
function onPickNovelDir(ev) {
  const files = Array.from(ev.target.files || [])
  ev.target.value = ''
  if (!files.length) return
  const chapters = files.filter(f => /\.(txt|md)$/i.test(f.name) && !f.webkitRelativePath.includes('封面'))
  const cover = files.find(f => /\.(png|jpe?g|webp)$/i.test(f.name) && (f.webkitRelativePath || '').includes('封面'))
  if (!chapters.length) { ElMessage.warning('所选目录中没有 txt/md 章节文件'); return }
  chapters.sort((a, b) => (a.webkitRelativePath || a.name).localeCompare(b.webkitRelativePath || b.name, 'zh'))
  if (cover) novelCover.value = cover
  let done = 0
  let text = ''
  for (const ch of chapters) {
    const rd = new FileReader()
    rd.onload = () => {
      text += (text ? '\n\n' : '') + String(rd.result || '')
      if (++done === chapters.length) novelText.value = text
    }
    rd.readAsText(ch, 'utf-8')
  }
  ElMessage.info(`已读入 ${chapters.length} 个章节文件` + (cover ? ',并识别到封面' : ''))
}
function onPickNovelCover(ev) {
  const f = ev.target.files?.[0]
  ev.target.value = ''
  if (!f) return
  if (!/^image\/(png|jpe?g|webp)/.test(f.type)) { ElMessage.error('仅支持 png/jpg/webp'); return }
  novelCover.value = f
  novelCoverPath.value = ''
}
function onDropFile(ev) {
  const dt = ev.dataTransfer
  if (!dt) return
  const files = dt.files
  if (files?.length) onPickFile({ target: { files, value: '' } })
}
function onPickFile(ev) {
  const f = ev.target.files?.[0]
  ev.target.value = ''
  if (!f) return
  if (f.size > 8 << 20) { ElMessage.error('文件过大(>8MB)'); return }
  const reader = new FileReader()
  reader.onload = () => {
    const text = String(reader.result || '')
    const ext = (f.name.split('.').pop() || '').toLowerCase()
    if (ext === 'json') {
      try {
        const j = JSON.parse(text)
        const plan = j.plan && Array.isArray(j.plan.shots) ? j.plan : (Array.isArray(j.shots) ? j : null)
        if (plan) {
          pickedFile.value = { name: f.name, text, kind: 'plan', plan,
            preview: `《${plan.title || '未命名'}》${plan.shots.length} 镜 / ${(plan.characters || []).length} 角色 / ${(plan.scenes || []).length} 场景 / ${(plan.props || []).length} 道具` }
          return
        }
      } catch { /* 非 JSON 或结构不完整 → 当源文本 */ }
    }
    pickedFile.value = { name: f.name, text, kind: 'text',
      preview: text.replace(/\s+/g, ' ').slice(0, 120) + (text.length > 120 ? '…' : '') }
  }
  reader.readAsText(f, 'utf-8')
}
</script>

<template>
  <!-- 新建项目:可选上传 txt / md / json -->
  <el-dialog v-model="dlg" title="新建短视频项目" width="560px"
             :close-on-click-modal="!composing" :close-on-press-escape="!composing" :show-close="!composing"
             v-loading="composing" element-loading-text="AI 创作中(1-3 分钟),日志有实时心跳…">
    <el-form label-position="top">
      <el-form-item label="项目名(中文 / 字母 / 数字 / _-)">
        <el-input v-model="newName" maxlength="40" :placeholder="novelMode ? '书名(可留空,AI 自动从小说识别)' : '如:天庭外卖员-第二集'" @keyup.enter="$emit('create')" />
      </el-form-item>
      <el-form-item v-if="!novelMode" label="AI 现场创作(只给需求,LLM 即时发挥写完整故事)">
        <div class="w-full flex flex-col gap-2">
          <el-switch v-model="aiMode" :disabled="novelMode" active-text="由 AI 创作" inactive-text="自己提供文本" />
          <el-input v-if="aiMode" v-model="aiPrompt" type="textarea" :rows="3" maxlength="300" show-word-limit
            placeholder="一句话或一个点子,如:外卖员送餐到天庭,玉帝当场包下他所有订单,搞笑爽文" />
        </div>
      </el-form-item>
      <el-form-item label="整本小说批量(拆成多集短视频,逐集产线)">
        <div class="w-full flex flex-col gap-2">
          <el-switch v-model="novelMode" :disabled="aiMode" active-text="整本小说模式" inactive-text="单集模式" />
          <template v-if="novelMode">
            <div class="flex gap-2 items-center">
              <el-input v-model="novelText" type="textarea" :rows="5" placeholder="粘贴整本小说/长文…&#10;LLM 将按叙事节拍拆成多集,每集一个短视频项目(同名角色自动复用定妆照)"
                style="flex:1" />
              <div class="flex flex-col gap-2">
                <input ref="novelInput" type="file" accept=".txt,.md" hidden @change="onPickNovel" />
                <input ref="novelDirInput" type="file" webkitdirectory hidden @change="onPickNovelDir" />
                <input ref="novelCoverInput" type="file" accept="image/png,image/jpeg,image/webp" hidden @change="onPickNovelCover" />
                <el-button size="small" @click="novelInput?.click()"><el-icon class="mr-1"><Upload /></el-icon>导入 txt / md</el-button>
                <el-button size="small" @click="novelDirInput?.click()"><el-icon class="mr-1"><FolderOpened /></el-icon>导入目录</el-button>
                <span class="text-xs" style="color:var(--mx-text3)">{{ novelText.length ? novelText.length + ' 字' : '' }}</span>
              </div>
            </div>
            <div class="flex items-center gap-3" style="--wails-drop-target: drop" @mx-filedrop="onCoverNativeDrop">
              <div v-if="novelCover || novelCoverPath" class="flex items-center gap-2 p-1.5 rounded-lg" style="border:1px solid var(--mx-line-hi)">
                <img v-if="novelCoverUrl" :src="novelCoverUrl" style="width:34px;height:46px;object-fit:cover;border-radius:5px" />
                <el-button text size="small" @click="novelCover = null; novelCoverPath = ''"><el-icon><Close /></el-icon></el-button>
              </div>
              <el-button v-else size="small" plain @click="novelCoverInput?.click()">
                <el-icon class="mr-1"><Picture /></el-icon>选择封面(可选,可拖入)
              </el-button>
              <span class="text-xs" style="color:var(--mx-text3)">封面应用为每集项目封面</span>
            </div>
            <div class="text-xs" style="color:var(--mx-text3)">「导入目录」合并章节;若所选目录(或其上层)含「封面」文件夹,其中的图片自动识别为系列封面;书名留空 AI 自动识别;拆集 2-6 分钟(日志有实时心跳)</div>
          </template>
        </div>
      </el-form-item>
      <el-form-item label="导入文件(可选)" v-if="!aiMode && !novelMode">
        <div v-if="!pickedFile" class="upload-zone" @click="fileInput?.click()" @dragover.prevent @drop.prevent="onDropFile">
          <el-icon :size="26" color="var(--mx-text3)"><UploadFilled /></el-icon>
          <div class="text-xs mt-1" style="color:var(--mx-text2)">点击或拖入 <b>.txt / .md / .json</b></div>
          <div class="text-xs" style="color:var(--mx-text3)">txt/md 作为剧本源文本;json 识别为分镜方案直接导入</div>
        </div>
        <div v-else class="upload-zone picked">
          <div class="flex items-center gap-2 w-full">
            <el-icon :size="18" :color="pickedFile.kind === 'plan' ? 'var(--mx-green)' : 'var(--mx-cyan)'">
              <Tickets v-if="pickedFile.kind === 'plan'" /><Document v-else />
            </el-icon>
            <div class="flex-1 min-w-0">
              <div class="text-xs font-bold">{{ pickedFile.name }}
                <el-tag size="small" :type="pickedFile.kind === 'plan' ? 'success' : 'info'" class="ml-1">
                  {{ pickedFile.kind === 'plan' ? '分镜方案' : '源文本' }}
                </el-tag>
              </div>
              <div class="text-xs truncate" style="color:var(--mx-text3)">{{ pickedFile.preview }}</div>
            </div>
            <el-button text size="small" @click="pickedFile = null"><el-icon><Close /></el-icon></el-button>
          </div>
        </div>
        <input ref="fileInput" type="file" accept=".txt,.md,.json" hidden @change="onPickFile" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="dlg = false">取消</el-button>
      <el-button type="primary" :loading="busy || composing || splitting"
        :disabled="(!newName.trim() && !novelMode) || (aiMode && !aiPrompt.trim()) || (novelMode && novelText.trim().length < 200)"
        @click="$emit('create')">
        {{ splitting ? '拆集中(2-6 分钟)…' : composing ? 'AI 创作中(1-3 分钟)…' : novelMode ? '拆集并创建多集项目' : '创建项目' }}
      </el-button>
    </template>
  </el-dialog>
</template>
