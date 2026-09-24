<!-- 管线阶段详情管理:script/assets/render/qc/assemble 五面板 -->
<script setup>
import { G, PHASES, api, refreshProject } from '../store'
import { computed, reactive, watch, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

const visible = computed({
  get: () => !!G.stageDetail,
  set: (v) => { if (!v) G.stageDetail = null },
})
const phase = computed(() => G.stageDetail)
const pj = computed(() => G.project)
const plan = computed(() => pj.value?.plan)
const manifest = computed(() => pj.value?.manifest || {})
const assets = computed(() => pj.value?.assets || {})
const qcRep = computed(() => pj.value?.qc || null)
const busy = reactive({ regen: '', run: false })

watch(phase, () => { /* 打开即取最新 */ if (phase.value) refreshProject() })

const titles = { script: '剧本分镜 · 详情', assets: '资产定妆 · 详情', render: '渲染出片 · 详情', qc: '质检 · 详情', assemble: '合成成片 · 详情' }

const charList = computed(() => (plan.value?.characters || []).map(c => ({ ...c, asset: assets.value['char:' + c.id] })))
const sceneList = computed(() => (plan.value?.scenes || []).map(s => ({ ...s, asset: assets.value['scene:' + s.id] })))

// 实体类型清单(后端 /api/meta 的单一真源):决定这张角色卡走哪套渲染方法。
// 兜底用本地简表,保证后端未就绪时选择器仍可用。
const FALLBACK_KINDS = [
  { key: 'human', name: '人', hint: '人物:半身正面定妆照 + 原地转身' },
  { key: 'object', name: '物', hint: '器物/机关/傀儡/雕像等非生命体:整件居中取景 + 转台自转,并禁止拟人化' },
  { key: 'spirit_pet', name: '灵宠', hint: '小型灵物/宠物:全身兽形取景,保留自身解剖结构' },
  { key: 'beast', name: '妖兽', hint: '大型或凶猛的非人生物:全身取景,体型体量照原文' },
  { key: 'incorporeal', name: '无实体', hint: '鬼魂/灵体/意识/投影:半透明形体,禁止落地成影与实体化' },
  { key: 'chibi', name: 'Q版', hint: 'Q 版/大头身比例:按 Q 版取景与转身渲染' },
]
const kindOptions = computed(() => (G.metaEntityKinds?.length ? G.metaEntityKinds : FALLBACK_KINDS))
// 角色卡上的类型:有显式 kind 用它,没有则按与后端一致的关键词规则**显示推断结果**
// (后端 ResolveEntityKind 也是"显式优先、缺失则推断",这里保持同口径,免得 UI 显示
// 成"人"而渲染按"物"走 —— 那会让人以为改动没生效)。
function inferKind(c) {
  const hay = `${c.name || ''}\n${c.description || ''}\n${c.imagePrompt || ''}`.toLowerCase()
  const hit = (arr) => arr.some(w => hay.includes(w))
  if (hit(['鬼魂', '幽灵', '幽魂', '魂魄', '灵体', '虚影', '残影', '幻影', '无形', '无实体', '半透明', '投影', '意识体', 'ghost', 'phantom', 'incorporeal', 'translucent spirit'])) return 'incorporeal'
  if (hit(['铜雀', '器物', '机关', '傀儡', '木偶', '偶人', '人偶', '提线', '纸人', '雕像', '石像', '铜像', '灯笼', '机械', '铆钉', '齿轮', 'puppet', 'marionette', 'doll', 'statue', 'automaton', 'mechanical', 'clockwork', 'artefact', 'artifact', 'golem'])) return 'object'
  if (hit(['妖兽', '魔兽', '妖怪', '巨龙', '麒麟', '巨狼', '巨蟒', '猛兽', '凶兽', 'demon', 'monster', 'beast', 'dragon', 'serpent'])) return 'beast'
  if (hit(['灵宠', '宠物', '小兽', '灵狐', '灵鹤', '灵蝶', '灵雀', '幼兽', 'spirit pet', 'familiar'])) return 'spirit_pet'
  if (hit(['q版', 'q 版', '呆萌', '大头身', 'chibi', 'super deformed'])) return 'chibi'
  return 'human'
}
function kindOf(c) {
  const k = String(c.kind || '').trim()
  return kindOptions.value.some(o => o.key === k) ? k : inferKind(c)
}
function kindName(c) {
  const k = kindOf(c)
  return kindOptions.value.find(o => o.key === k)?.name || k
}
// 这张定妆照是不是**按当前类型**渲染的:资产表里存着当时装配出来的 image_prompt,
// 它若不含当前类型的标记串,就说明图是按旧类型(或旧版本)出的 —— 典型症状是
// "卡片上写着物、图却是一个人",必须先重出再渲染,否则错的图会被当第一参考喂回去。
function sheetStale(c) {
  const prompt = c.asset?.image_prompt
  if (!prompt) return false
  const marker = kindOptions.value.find(o => o.key === kindOf(c))?.marker
  return !!marker && !String(prompt).includes(marker)
}
async function setKind(c, key) {
  try {
    const r = await api.putCharacter(pj.value.project.id, { id: c.id, kind: key })
    ElMessage.success(`「${c.name}」类型已改为 ${r?.name || key}`)
    await refreshProject()
    ElMessageBox.confirm(
      '类型已保存,但**已生成**的定妆照/多视角图仍是按旧类型渲染的(比如铜雀那张还是人像)。要重出定妆照请点这张卡右侧的 ✨(Krea-2 重新生成),多视角会随后按新类型重建。',
      '下一步:重新生成定妆照', { confirmButtonText: '知道了', showCancelButton: false, type: 'warning' },
    ).catch(() => {})
  } catch (e) { ElMessage.error(e.message) }
}

function viewAsset(a, kind, name) {
  if (!a?.file) return
  G.viewer = { index: 0, items: [{ type: 'image', src: api.fileURL(pj.value.project.id, a.file), title: name, meta: a.source === 'library' ? '库复用' : (a.source === 'krea2' ? 'Krea-2' : '上传'), kindLabel: 'ASSET' }] }
}
async function regen(assetId) {
  busy.regen = assetId
  try {
    await api.regenAsset(pj.value.project.id, assetId)
    ElMessage.success('已重新生成')
    refreshProject()
  } catch (e) { ElMessage.error(e.message) } finally { busy.regen = '' }
}
async function rerender(shotId, engine) {
  try {
    await api.run(pj.value.project.id, 'render', [shotId], engine)
    ElMessage.success(`镜头 ${shotId} 已提交`)
    refreshProject()
  } catch (e) { ElMessage.error(e.message) }
}
function viewShot(sh) {
  const m = manifest.value[String(sh.id)]
  if (m?.status !== 'done' || !m?.file) return
  // 全部镜头进入预览列表,可 ←→ 连续浏览
  const items = (plan.value.shots || []).filter(s => manifest.value[String(s.id)]?.status === 'done' && manifest.value[String(s.id)]?.file)
    .map(s => {
      const mm = manifest.value[String(s.id)]
      return {
        type: 'video', src: api.fileURL(pj.value.project.id, mm.file),
        title: `镜头 ${s.id} · ${s.shot_size}`, meta: `${s.mode.toUpperCase()} · ${s.duration}s · ${mm.engine === 'comfy' ? '本地' : '云端'}`,
        foot: [s.action, s.narration, ...(s.dialogue || []).map(d => `${d.speaker}:${d.text}`)].filter(Boolean).join('\n'),
        kindLabel: 'SHOT',
      }
    })
  const idx = Math.max(0, items.findIndex(x => x.title.startsWith(`镜头 ${sh.id} `)))
  G.viewer = { items, index: idx }
}
async function delAsset(assetId) {
  try { await api.deleteAsset(pj.value.project.id, assetId); refreshProject() }
  catch (e) { ElMessage.error(e.message) }
}
async function runPhase(p) {
  try { await api.run(pj.value.project.id, p); ElMessage.success('已启动'); refreshProject() }
  catch (e) { ElMessage.error(e.message) }
}
async function upgrade2K() {
  try { await api.upgrade2k(pj.value.project.id, 0); ElMessage.success('2K 定稿已提交') }
  catch (e) { ElMessage.error(e.message) }
}
async function exportPack() {
  try {
    const d = await api.exportPack(pj.value.project.id)
    ElMessage.success(`素材包 ${d.size_mb.toFixed(1)}MB 已生成`)
  } catch (e) { ElMessage.error(e.message) }
}
const stLabel = { pending: '待渲', rendering: '渲染中', done: '完成', failed: '失败', stale: '过期' }

// 出场角色名(渲染列表标注)
function castOf(sh) {
  const chars = plan.value?.characters || []
  return (sh.characters || []).map(cid => chars.find(c => c.id === cid)?.name).filter(Boolean)
}
</script>

<template>
  <el-drawer v-model="visible" :title="titles[phase] || ''" size="620px" append-to-body>
    <!-- ▎剧本分镜 -->
    <div v-if="phase === 'script'" class="flex flex-col gap-4">
      <template v-if="plan">
        <div class="panel p-4">
          <div class="flex items-center gap-2 mb-1"><b>《{{ plan.title }}》</b></div>
          <div class="text-xs" style="color:var(--mx-text2)">{{ plan.logline }}</div>
          <div class="timecode mt-2" style="word-break:break-all">{{ plan.style }}</div>
        </div>
        <div class="flex items-center justify-between">
          <span class="text-sm">{{ plan.characters.length }} 角色 · {{ plan.scenes.length }} 场景 · {{ plan.shots.length }} 镜头</span>
          <el-button size="small" type="primary" :disabled="pj.project.running" @click="runPhase('script')">
            <el-icon class="mr-1"><RefreshRight /></el-icon>重新生成方案
          </el-button>
        </div>
        <el-alert type="info" :closable="false" title="修改源文本与风格请在「剧本」标签页操作;重新生成会覆盖现有方案(已渲镜头按指纹自动判断是否复用)。" />
      </template>
      <div v-else class="p-8 text-center text-sm" style="color:var(--mx-text3)">尚未生成方案</div>
    </div>

    <!-- ▎资产定妆 -->
    <div v-else-if="phase === 'assets'" class="flex flex-col gap-3">
      <div class="flex items-center justify-between">
        <span class="text-sm">{{ charList.length + sceneList.length }} 项资产</span>
        <el-button size="small" :disabled="pj.project.running" @click="runPhase('assets')"><el-icon class="mr-1"><Refresh /></el-icon>盘点/补齐</el-button>
      </div>
      <div v-for="c in charList" :key="c.id" class="panel p-3 flex items-center gap-3">
        <div class="w-12 h-16 rounded-lg overflow-hidden flex-none cursor-zoom-in" style="background:var(--mx-bg2)" @click="viewAsset(c.asset,'characters',c.name)">
          <img v-if="c.asset" :src="api.fileURL(pj.project.id, c.asset.file)" class="w-full h-full object-cover" />
        </div>
        <div class="flex-1 min-w-0">
          <div class="flex items-center gap-2 flex-wrap">
            <b class="text-sm">{{ c.name }}</b>
            <el-select :model-value="kindOf(c)" size="small" style="width:92px" @change="(v)=>setKind(c,v)">
              <el-option v-for="o in kindOptions" :key="o.key" :value="o.key" :label="o.name">
                <span>{{ o.name }}</span>
                <span class="text-xs" style="color:var(--mx-text3);margin-left:8px">{{ o.hint }}</span>
              </el-option>
            </el-select>
            <el-tag size="small" effect="plain" :type="c.asset?.source==='library'?'success':(c.asset?.source==='krea2'?'warning':'info')">
              {{ c.asset ? (c.asset.source==='library'?'库复用':c.asset.source==='krea2'?'Krea-2':'上传') : '缺失' }}
            </el-tag>
            <el-tooltip v-if="sheetStale(c)" content="这张定妆照是按旧类型(或旧版本)渲染的 —— 请点右侧 ✨ 重新生成后再渲染,否则错的图会被当作第一参考喂回去" placement="top">
              <el-tag size="small" effect="dark" type="danger">定妆照待重出</el-tag>
            </el-tooltip>
          </div>
          <div class="text-xs truncate mt-1" style="color:var(--mx-text3)">{{ c.description }}</div>
        </div>
        <el-button size="small" text :loading="busy.regen==='char:'+c.id" @click="regen('char:'+c.id)" title="Krea-2 重新生成"><el-icon><MagicStick /></el-icon></el-button>
        <el-button v-if="c.asset" size="small" text @click="delAsset('char:'+c.id)"><el-icon><Delete /></el-icon></el-button>
      </div>
      <div v-for="sc in sceneList" :key="sc.id" class="panel p-3 flex items-center gap-3">
        <div class="w-16 h-10 rounded-lg overflow-hidden flex-none cursor-zoom-in" style="background:var(--mx-bg2)" @click="viewAsset(sc.asset,'scenes',sc.name)">
          <img v-if="sc.asset" :src="api.fileURL(pj.project.id, sc.asset.file)" class="w-full h-full object-cover" />
        </div>
        <div class="flex-1 min-w-0">
          <div class="flex items-center gap-2"><b class="text-sm">{{ sc.name }}</b>
            <el-tag size="small" effect="plain" :type="sc.asset?.source==='library'?'success':(sc.asset?.source==='krea2'?'warning':'info')">
              {{ sc.asset ? (sc.asset.source==='library'?'库复用':sc.asset.source==='krea2'?'Krea-2':'上传') : '缺失' }}
            </el-tag>
          </div>
          <div class="text-xs truncate mt-1" style="color:var(--mx-text3)">{{ sc.description }}</div>
        </div>
        <el-button size="small" text :loading="busy.regen==='scene:'+sc.id" @click="regen('scene:'+sc.id)"><el-icon><MagicStick /></el-icon></el-button>
        <el-button v-if="sc.asset" size="small" text @click="delAsset('scene:'+sc.id)"><el-icon><Delete /></el-icon></el-button>
      </div>
    </div>

    <!-- ▎渲染出片 -->
    <div v-else-if="phase === 'render'" class="flex flex-col gap-2">
      <div class="flex items-center justify-between mb-1">
        <span class="text-sm">{{ Object.values(manifest).filter(m=>m.status==='done').length }}/{{ plan?.shots.length || 0 }} 镜完成</span>
        <el-dropdown trigger="click" @command="(e)=>runPhase('render'+(e?'&engine='+e:''))" v-if="false"></el-dropdown>
        <el-button size="small" :disabled="pj.project.running" @click="runPhase('render')"><el-icon class="mr-1"><VideoPlay /></el-icon>渲染缺失镜头</el-button>
      </div>
      <div v-for="sh in plan?.shots || []" :key="sh.id" class="panel p-3 flex items-center gap-3">
        <span class="stage-num" style="font-size:18px;width:34px">{{ String(sh.id).padStart(2,'0') }}</span>
        <div class="flex-1 min-w-0">
          <div class="flex items-center gap-2 flex-wrap">
            <i class="dot" :class="manifest[String(sh.id)]?.status==='done'?'ok':manifest[String(sh.id)]?.status==='rendering'?'run':manifest[String(sh.id)]?.status==='failed'?'bad':''"></i>
            <span class="text-xs">{{ stLabel[manifest[String(sh.id)]?.status] || '待渲' }}</span>
            <span class="badge-mode" :class="sh.mode">{{ sh.mode?.toUpperCase() }}</span>
            <span class="timecode">{{ sh.duration }}s · {{ sh.shot_size }}</span>
            <span v-if="manifest[String(sh.id)]?.engine" class="timecode" style="color:var(--mx-cyan)">{{ manifest[String(sh.id)].engine==='comfy'?'本地':'云端' }}</span>
            <span v-for="n in castOf(sh)" :key="n" class="cast-chip"><el-icon :size="10"><User /></el-icon>{{ n }}</span>
          </div>
          <div v-if="manifest[String(sh.id)]?.error" class="text-xs mt-1" style="color:var(--mx-red)">{{ manifest[String(sh.id)].error }}</div>
        </div>
        <el-button v-if="manifest[String(sh.id)]?.status==='done'" size="small" text @click="viewShot(sh)" title="全屏预览"><el-icon><View /></el-icon></el-button>
        <el-button size="small" text @click="G.tuneShot = sh" title="画布微调" :disabled="pj.project.running"><el-icon><Brush /></el-icon></el-button>
        <el-dropdown trigger="click" @command="(cmd)=>cmd==='2k'?api.upgrade2k(pj.project.id, sh.id).then(()=>ElMessage.success('2K 定稿已提交')).catch(e=>ElMessage.error(e.message)):rerender(sh.id, cmd)">
          <el-button size="small" text type="primary" :disabled="pj.project.running"><el-icon><RefreshRight /></el-icon></el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="">重渲 · 按项目</el-dropdown-item>
              <el-dropdown-item command="comfy">重渲 · 本地</el-dropdown-item>
              <el-dropdown-item command="api">重渲 · 云端</el-dropdown-item>
              <el-dropdown-item divided command="2k" :disabled="manifest[String(sh.id)]?.status!=='done'">云端 2K 定稿</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </div>

    <!-- ▎质检 -->
    <div v-else-if="phase === 'qc'" class="flex flex-col gap-3">
      <div class="flex items-center justify-between">
        <span class="text-sm">{{ qcRep ? Object.keys(qcRep).length + ' 镜报告' : '暂无报告' }}</span>
        <el-button size="small" :disabled="pj.project.running" @click="runPhase('qc')"><el-icon class="mr-1"><Refresh /></el-icon>重跑质检</el-button>
      </div>
      <div v-for="(r, k) in qcRep || {}" :key="k" class="panel p-3">
        <div class="flex items-center gap-2">
          <i class="dot" :class="r.ok ? 'ok' : 'bad'"></i>
          <b class="text-sm">镜头 {{ k }}</b>
          <span class="timecode">{{ r.duration?.toFixed(1) }}s · 暗 {{ (r.dark_ratio*100).toFixed(0) }}% · {{ r.has_audio ? '有音轨' : '无音轨' }}</span>
          <div class="flex-1"></div>
          <el-tag v-if="r.vlm" size="small" :type="r.vlm.pass ? 'success' : 'warning'" effect="plain">VLM {{ r.vlm.score }}</el-tag>
        </div>
        <div v-if="(r.problems||[]).length" class="text-xs mt-1.5" style="color:var(--mx-red)">{{ r.problems.join(';') }}</div>
        <div v-if="r.vlm && !r.vlm.pass" class="text-xs mt-1.5 p-2 rounded" style="background:var(--mx-yellow-soft);color:var(--mx-yellow)">
          {{ (r.vlm.issues||[]).join(';') }}<template v-if="r.vlm.advice"> — {{ r.vlm.advice }}</template>
        </div>
      </div>
      <el-alert v-if="!qcRep" type="info" :closable="false" title="渲染完成后运行质检阶段生成报告;VLM 智能判分在「质检 ⚙」开启。" />
    </div>

    <!-- ▎合成成片 -->
    <div v-else-if="phase === 'assemble'" class="flex flex-col gap-3">
      <div class="flex gap-2 flex-wrap">
        <el-button size="small" type="primary" :disabled="pj.project.running || !plan?.shots?.length" @click="runPhase('assemble')"><el-icon class="mr-1"><Film /></el-icon>重新合成</el-button>
        <el-button size="small" type="warning" plain :disabled="!pj.output?.ready" @click="upgrade2K"><el-icon class="mr-1"><MagicStick /></el-icon>升级 2K</el-button>
        <el-button size="small" @click="exportPack"><el-icon class="mr-1"><Suitcase /></el-icon>导出素材包</el-button>
      </div>
      <div class="panel p-3 flex items-center gap-3">
        <el-icon :size="20" color="var(--mx-amber)"><VideoPlay /></el-icon>
        <div class="flex-1">
          <div class="text-sm">final.mp4 <el-tag v-if="pj.output?.ready" size="small" type="success" effect="plain">已生成</el-tag></div>
          <div class="timecode">768P 标准版</div>
        </div>
        <el-button v-if="pj.output?.ready" size="small" text @click="G.viewer={index:0,items:[{type:'video',src:api.fileURL(pj.project.id,'output/final.mp4'),title:'《'+ (plan?.title||'') +'》 · 768P',kindLabel:'FINAL'}]}"><el-icon><View /></el-icon></el-button>
      </div>
      <div class="panel p-3 flex items-center gap-3">
        <el-icon :size="20" color="var(--mx-purple)"><MagicStick /></el-icon>
        <div class="flex-1">
          <div class="text-sm">final_2k.mp4 <el-tag v-if="pj.output?.ready_2k" size="small" type="warning" effect="plain">2K</el-tag></div>
          <div class="timecode">云端 In-context 再生定稿版</div>
        </div>
        <el-button v-if="pj.output?.ready_2k" size="small" text @click="G.viewer={index:0,items:[{type:'video',src:api.fileURL(pj.project.id,'output/final_2k.mp4'),title:'《'+(plan?.title||'')+'》 · 2K',kindLabel:'FINAL'}]}"><el-icon><View /></el-icon></el-button>
      </div>
      <el-alert type="info" :closable="false" :title="`产物目录:output/(final.mp4 · final_2k.mp4 · final.srt · 剪辑素材包.zip);转场/字幕/响度参数在「合成成片 ⚙」。`" />
    </div>
  </el-drawer>
</template>
