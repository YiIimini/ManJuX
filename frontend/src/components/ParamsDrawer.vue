<script setup>
import { G, api, refreshProject } from '../store'
import { computed, reactive, watch } from 'vue'
import { ElMessage } from 'element-plus'

const visible = computed({
  get: () => !!G.paramsDrawer,
  set: (v) => { if (!v) G.paramsDrawer = null },
})
const stageId = computed(() => G.paramsDrawer)

const pj = computed(() => G.project)

const form = reactive({ script: {}, assets: {}, render: {}, qc: {}, assemble: {} })

watch(stageId, () => {
  const p = pj.value?.project?.params
  if (!p) return
  const copy = JSON.parse(JSON.stringify(p))
  // 逐层兜底:老项目快照里可能没有某个阶段(或某个新字段)。整体覆盖会让
  // 模板里的 v-model="form.qc.enabled" 这类表达式打到 undefined 上直接抛错,
  // 抽屉一开就弹「界面错误」。
  for (const k of Object.keys(form)) {
    form[k] = Object.assign({}, form[k] || {}, copy[k] || {})
  }
  // 多视角 / 参考图去冗余:缺省 = 开(与后端 ViewsOn()/CompactRefsOn() 的「未表态即开」同一语义)
  form.assets.views = form.assets.views !== false
  form.assets.compact_refs = form.assets.compact_refs === true // 默认关(只喂转台图会让锁脸变弱)
  // 多视角生成方式:缺省 = auto(与后端 ViewsSourceOf() 的「认不出就当 auto」同一语义)。
  // 必须在这里兜底成字符串:老项目快照里没有这个键,留 undefined 会让下拉显示空白,
  // 用户以为"没选",保存后仍是 auto 但界面看起来像坏了。
  form.assets.views_source = ['edit', 'h3'].includes(form.assets.views_source) ? form.assets.views_source : 'auto'
  // 角色动漫化 / 音频净化:同样「未表态即开」——老项目参数快照里没有这两个键,
  // 若不在这里兜底成 true,抽屉一开就会把用户亲手关掉(false)的开关也显示成开。
  form.script.stylized_faces = form.script.stylized_faces !== false
  form.assemble.denoise = form.assemble.denoise !== false
}, { immediate: true })

const titles = {
  script: '剧本分镜 · 调优',
  assets: '资产定妆 · 调优',
  render: '渲染出片 · 调优',
  qc: '质检 · 调优',
  assemble: '合成成片 · 调优',
}

// 渲染风格预设(与后端 pipeline.StylePresets 对齐;可多选组合)
const STYLE_PRESETS = [
  { key: 'urban_xianxia', name: '都市仙侠霓虹', sw: ['#f97a1f', '#53d8ff'] },
  { key: 'guoman_2d',     name: '国漫二维动画', sw: ['#ff6b6b', '#ffd166'] },
  { key: 'live_action',   name: '真人实拍质感', sw: ['#93a0bd', '#edf1fa'] },
  { key: 'pixar_3d',      name: '3D 皮克斯',   sw: ['#3ddc84', '#53d8ff'] },
  { key: 'ink_wash',      name: '水墨国风',    sw: ['#c9d4e8', '#5a6580'] },
  { key: 'cyberpunk',     name: '赛博朋克',    sw: ['#b28bff', '#ff2e88'] },
  { key: 'ghibli',        name: '吉卜力手绘',  sw: ['#8affc1', '#ffd166'] },
  { key: 'hongkong',      name: '港片胶片',    sw: ['#ffb454', '#12b886'] },
  { key: 'film_noir',     name: '黑色电影',    sw: ['#f2f4f8', '#33333c'] },
  { key: 'claymation',    name: '黏土定格',    sw: ['#ff9ecf', '#ffd166'] },
]

// 显示用清单:以后端 /api/meta 为准 —— 后端新增风格会自动出现在这里(用默认色),
// 后端删掉的风格也随之消失,不再需要两侧手工同步。颜色属纯展示信息,仍由前端决定。
// 后端未就绪(metaStyles 为空)时回退内置清单,保证抽屉始终可用。
const styleList = computed(() => {
  const meta = G.metaStyles || []
  if (!meta.length) return STYLE_PRESETS
  const byKey = new Map(STYLE_PRESETS.map(s => [s.key, s]))
  return meta.map(m => {
    const local = byKey.get(m.key)
    return { key: m.key, name: m.name || local?.name || m.key, sw: local?.sw || ['#8a93a8', '#c9d4e8'] }
  })
})

const hasStyle = (k) => (form.script.styles || []).includes(k)
function toggleStyle(k) {
  if (!Array.isArray(form.script.styles)) form.script.styles = []
  const i = form.script.styles.indexOf(k)
  if (i >= 0) form.script.styles.splice(i, 1)
  else form.script.styles.push(k)
}

async function save() {
  try {
    const saved = await api.putParams(pj.value.project.id, form)
    if (pj.value.project) pj.value.project.params = saved
    ElMessage.success('阶段参数已保存(仅本项目生效)')
    G.paramsDrawer = null
    refreshProject()
  } catch (e) { ElMessage.error(e.message) }
}
</script>

<template>
  <el-drawer v-model="visible" :title="titles[stageId] || '参数调优'" size="400px" append-to-body>
    <div v-if="stageId === 'script'" class="flex flex-col gap-4">
      <el-form label-position="top">
        <el-form-item label="目标镜头数(0=按内容自动,最多 120)">
          <el-input-number v-model="form.script.max_shots" :min="0" :max="120" style="width:100%" />
        </el-form-item>
        <el-form-item label="目标总时长(秒,0=按镜头数自动)">
          <el-input-number v-model="form.script.total_seconds" :min="0" :max="300" :step="6" style="width:100%" />
        </el-form-item>
      </el-form>
      <div>
        <div class="text-xs mb-2">
          <b>渲染视频风格</b>
          <span style="color:var(--mx-text3)">(可多选组合,决定全片视觉基调;已选 {{ (form.script.styles || []).length }} 项)</span>
        </div>
        <div class="style-chip-grid">
          <button v-for="st in styleList" :key="st.key" class="style-chip" :class="{ on: hasStyle(st.key) }" @click="toggleStyle(st.key)">
            <span class="sw"><i :style="{ background: st.sw[0] }"></i><i :style="{ background: st.sw[1] }"></i></span>
            {{ st.name }}
          </button>
        </div>
      </div>
      <el-form label-position="top">
        <el-form-item label="风格附加要求(自由文本,与上方预设叠加)">
          <el-input v-model="form.script.style_hint" type="textarea" :rows="3"
            placeholder="例如:偏冷色调、雨夜氛围、手持镜头感…" />
        </el-form-item>
        <el-form-item label="角色动漫化(非真人脸 + 哑光画风,避免影射真实人物/侵权)">
          <el-switch v-model="form.script.stylized_faces" />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            **未指定风格时的兜底画风**:方案里没有风格设定时,按动画化渲染(手绘线稿 + 赛璐璐平涂 + 哑光肤色),
            压掉"油光/汗湿反光"的油腻感。**风格由上游(小说技能端)在方案里指定,指定后本开关不参与画风** ——
            平台只负责照它渲染并保证全片一致。要拍真人剧就在方案里写真人/写实风格。
          </div>
        </el-form-item>
        <el-form-item label="Q 版形象(定妆时同步生成角色 Q 版;内心活动镜头的表达素材)">
          <el-switch v-model="form.script.chibi" />
        </el-form-item>
      </el-form>
    </div>

    <div v-else-if="stageId === 'assets'" class="flex flex-col gap-4">
      <el-form label-position="top">
        <el-form-item label="参考图来源">
          <el-select v-model="form.assets.source">
            <el-option value="krea2" label="本地模型自动生成(推荐,需本地 ComfyUI)" />
            <el-option value="upload" label="手动上传(锁脸最稳)" />
            <el-option value="skip" label="跳过(全部镜头纯文生 t2va)" />
          </el-select>
        </el-form-item>
        <el-form-item label="角色多视角转台图(渲染时作该角色第二张参考;缺了渲染前自动补)">
          <el-switch v-model="form.assets.views" />
        </el-form-item>
        <el-form-item label="多视角生成方式">
          <el-select v-model="form.assets.views_source">
            <el-option value="auto" label="自动(编辑模型就绪就用图像编辑,否则 H3 转身;推荐)" />
            <el-option value="edit" label="图像编辑(Qwen-Image-Edit-2511:只转角度,不改画风)" />
            <el-option value="h3" label="H3 转身视频(旧路径:会重新设计角色,身份校验多半拒绝)" />
          </el-select>
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            图像编辑拿定妆照本体改一个角度,身份/配色/画风按像素保住,每张几秒;它才是"转台图与定妆照是同一人"的那条路。
            权重没下齐时自动退回 H3 转身视频(每个角色数分钟,且可能被身份校验拒绝)。
          </div>
        </el-form-item>
        <el-form-item label="参考图去冗余(只喂转台图省一张;注意:拼合图缩放后脸部细节变弱,锁脸会变差,默认关)">
          <el-switch v-model="form.assets.compact_refs" />
        </el-form-item>
      </el-form>
      <el-alert type="info" :closable="false"
        title="本地模型自动生成:角色半身竖幅 832×1248 / 场景按项目画幅;已有资产自动复用,可上传替换。用的是设置里配置的图像模型(默认 Qwen-Image-2.1)。" />
      <el-alert type="warning" :closable="false"
        title="多视角:角色只有单张正面照时,渲染前会自动补一次(编辑路径每张数秒;退回 H3 转身则每个角色数分钟,仅首次;补完写回资产库,后续项目直接复用)。不想要这个耗时可以关掉。" />
    </div>

    <div v-else-if="stageId === 'render'" class="flex flex-col gap-4">
      <el-form label-position="top">
        <el-form-item label="渲染引擎">
          <el-select v-model="form.render.engine">
            <el-option value="auto" label="自动(本地优先,不可用切云端)" />
            <el-option value="comfy" label="本地 ComfyUI(H3-Base 768p)" />
            <el-option value="api" label="MiniMax 云端 API(按量计费)" />
          </el-select>
        </el-form-item>
        <el-form-item label="分辨率(32 整除;短边 ≤768)">
          <div class="flex gap-2 w-full">
            <el-input-number v-model="form.render.width" :step="32" :min="32" :max="1344" style="flex:1" />
            <el-input-number v-model="form.render.height" :step="32" :min="32" :max="1344" style="flex:1" />
          </div>
          <div class="flex gap-2 mt-2 w-full">
            <el-button size="small" @click="form.render.width = 768; form.render.height = 1344">9:16 竖屏</el-button>
            <el-button size="small" @click="form.render.width = 1344; form.render.height = 768">16:9 横屏</el-button>
            <el-button size="small" @click="form.render.width = 768; form.render.height = 768">1:1 方形</el-button>
          </div>
        </el-form-item>
        <el-form-item label="采样步数(turbo 4-8 / 基线 20)">
          <el-input-number v-model="form.render.steps" :min="1" :max="30" style="width:100%" />
        </el-form-item>
        <el-form-item label="Seed(0=每镜随机;固定值=全片画风一致)">
          <el-input-number v-model="form.render.seed" :min="0" :max="2147483647" style="width:100%" />
        </el-form-item>
        <el-form-item label="加速档(质量档=Acc-8Step,人物质感/清晰度显著优于Turbo,慢约20%)">
          <el-select v-model="form.render.accel">
            <el-option value="" label="Turbo 均衡(默认,官方4步蒸馏)" />
            <el-option value="quality" label="质量优先(Acc-8Step,成片推荐)" />
            <el-option value="express" label="极速预览(int8 VAE+4步,抽卡用)" />
          </el-select>
        </el-form-item>
        <el-form-item label="两阶段渲染(条件缓存,换 seed 不重编码)">
          <el-switch v-model="form.render.cond_cache" />
        </el-form-item>
        <el-form-item label="镜间连续接缝(MotionContext 22帧;仅本地引擎;同场景镜头更连贯)">
          <el-switch v-model="form.render.seam" />
        </el-form-item>
      </el-form>
    </div>

    <div v-else-if="stageId === 'qc'" class="flex flex-col gap-4">
      <el-form label-position="top">
        <el-form-item label="启用质检">
          <el-switch v-model="form.qc.enabled" />
        </el-form-item>
        <el-form-item label="VLM 智能审片(抽帧判分+重渲建议,需视觉模型)">
          <el-switch v-model="form.qc.enable_vlm" />
        </el-form-item>
        <el-form-item label="暗像素失败阈值(0-1)">
          <el-slider v-model="form.qc.dark_ratio_fail" :min="0.3" :max="0.95" :step="0.05" show-input />
        </el-form-item>
        <el-form-item label="暗像素警告阈值">
          <el-slider v-model="form.qc.dark_ratio_warn" :min="0.2" :max="0.9" :step="0.05" show-input />
        </el-form-item>
        <el-form-item label="单镜时长区间(秒)">
          <div class="flex gap-2 w-full">
            <el-input-number v-model="form.qc.min_duration" :min="1" :max="10" :step="0.5" style="flex:1" />
            <el-input-number v-model="form.qc.max_duration" :min="5" :max="60" :step="1" style="flex:1" />
          </div>
        </el-form-item>
      </el-form>
    </div>

    <div v-else-if="stageId === 'assemble'" class="flex flex-col gap-4">
      <el-form label-position="top">
        <el-form-item label="转场">
          <el-select v-model="form.assemble.transition">
            <el-option value="cut" label="硬切(最稳)" />
            <el-option value="fade" label="闪黑叠化(默认 0.5s,可调)" />
            <el-option value="dissolve" label="交叉叠化(默认 0.5s,可调)" />
          </el-select>
        </el-form-item>
        <el-form-item label="转场时长(秒;0 = 用设置页的「转场时长」)">
          <el-input-number v-model="form.assemble.fade_sec" :min="0" :max="3" :step="0.1" :precision="1" style="width:100%" />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            两镜画面重叠的时长,成片总长按此扣除 (n-1)×时长。留 0 表示跟随设置页的统一值。
          </div>
        </el-form-item>
        <el-form-item label="烧录字幕(台词金色/旁白白色)">
          <el-switch v-model="form.assemble.burn_subtitles" />
        </el-form-item>
        <el-form-item label="目标响度(LUFS)">
          <el-slider v-model="form.assemble.loudness" :min="-30" :max="-6" :step="1" show-input />
        </el-form-item>
        <el-form-item label="峰值上限(loudnorm 的真峰值 TP;0.92 ≈ -0.7 dBTP)">
          <el-slider v-model="form.assemble.peak" :min="0.5" :max="1" :step="0.01" show-input />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            越小越安全(不易在手机扬声器上削顶),越大越响。默认 0.92。
          </div>
        </el-form-item>
        <el-form-item label="音频净化(高通去隆隆声 + 频谱降噪去底噪/嘶声)">
          <el-switch v-model="form.assemble.denoise" />
          <div class="text-xs mt-1" style="color:var(--mx-text3)">
            默认开。成片响度按两遍法归一(先测量再线性增益),不会逐帧抽气;镜间音频切点会自动落在静音上,不再切掉台词尾巴。
          </div>
        </el-form-item>
        <el-form-item label="字幕字号(屏高 %)">
          <el-slider v-model="form.assemble.subtitle_size" :min="3" :max="9" :step="1" show-input />
        </el-form-item>
        <el-form-item label="云端 2K 定稿(合成后自动 regeneration 升 2K,消耗云额度)">
          <el-switch v-model="form.assemble.upgrade_2k" />
        </el-form-item>
      </el-form>
    </div>

    <template #footer>
      <el-button @click="G.paramsDrawer = null">取消</el-button>
      <el-button type="primary" @click="save">保存(项目级)</el-button>
    </template>
  </el-drawer>
</template>
