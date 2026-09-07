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
    const r = await fetch(`/api/projects/${encodeURIComponent(pj.value.project.id)}/export-pack`, { method: 'POST' })
    const d = await r.json(); if (!r.ok) throw new Error(d.error)
    ElMessage.success(`素材包 ${d.size_mb.toFixed(1)}MB 已生成`)
  } catch (e) { ElMessage.error(e.message) }
}
const stLabel = { pending: '待渲', rendering: '渲染中', done: '完成', failed: '失败', stale: '过期' }
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
          <div class="flex items-center gap-2">
            <b class="text-sm">{{ c.name }}</b>
            <el-tag size="small" effect="plain" :type="c.asset?.source==='library'?'success':(c.asset?.source==='krea2'?'warning':'info')">
              {{ c.asset ? (c.asset.source==='library'?'库复用':c.asset.source==='krea2'?'Krea-2':'上传') : '缺失' }}
            </el-tag>
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
          </div>
          <div v-if="manifest[String(sh.id)]?.error" class="text-xs mt-1" style="color:var(--mx-red)">{{ manifest[String(sh.id)].error }}</div>
        </div>
        <el-button v-if="manifest[String(sh.id)]?.status==='done'" size="small" text @click="viewShot(sh)" title="全屏预览"><el-icon><View /></el-icon></el-button>
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
        <div v-if="r.vlm && !r.vlm.pass" class="text-xs mt-1.5 p-2 rounded" style="background:rgba(255,209,102,.06);color:var(--mx-yellow)">
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
