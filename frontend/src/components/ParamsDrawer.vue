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
  if (p) Object.assign(form, JSON.parse(JSON.stringify(p)))
}, { immediate: true })

const titles = {
  script: '剧本分镜 · 调优',
  assets: '资产定妆 · 调优',
  render: '渲染出片 · 调优',
  qc: '质检 · 调优',
  assemble: '合成成片 · 调优',
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
        <el-form-item label="目标镜头数(3-12)">
          <el-input-number v-model="form.script.max_shots" :min="1" :max="20" style="width:100%" />
        </el-form-item>
        <el-form-item label="目标总时长(秒)">
          <el-input-number v-model="form.script.total_seconds" :min="12" :max="180" :step="6" style="width:100%" />
        </el-form-item>
        <el-form-item label="风格附加要求(可空)">
          <el-input v-model="form.script.style_hint" type="textarea" :rows="3"
            placeholder="例如:国风仙侠、赛博朋克霓虹、治愈日系动画…" />
        </el-form-item>
      </el-form>
    </div>

    <div v-else-if="stageId === 'assets'" class="flex flex-col gap-4">
      <el-form label-position="top">
        <el-form-item label="参考图来源">
          <el-select v-model="form.assets.source">
            <el-option value="krea2" label="Krea-2 自动生成(推荐,需本地 ComfyUI)" />
            <el-option value="upload" label="手动上传(锁脸最稳)" />
            <el-option value="skip" label="跳过(全部镜头纯文生 t2va)" />
          </el-select>
        </el-form-item>
      </el-form>
      <el-alert type="info" :closable="false"
        title="Krea-2 生成:角色半身竖幅 832×1248 / 场景按项目画幅;已有资产自动复用,可上传替换。" />
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
        <el-form-item label="加速 LoRA(Turbo)">
          <el-switch v-model="form.render.turbo" />
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
            <el-option value="fade" label="闪黑叠化(0.5s)" />
            <el-option value="dissolve" label="交叉叠化(0.5s)" />
          </el-select>
        </el-form-item>
        <el-form-item label="烧录字幕(台词金色/旁白白色)">
          <el-switch v-model="form.assemble.burn_subtitles" />
        </el-form-item>
        <el-form-item label="目标响度(LUFS)">
          <el-slider v-model="form.assemble.loudness" :min="-30" :max="-6" :step="1" show-input />
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
