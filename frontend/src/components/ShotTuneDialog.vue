<!-- 单分镜画布微调:预览 + 提示词/时长/模式编辑 + seed + 引擎直渲 -->
<script setup>
import { G, api, refreshProject } from '../store'
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { cloudSeedWarning } from '../renderHints'

const visible = computed({
  get: () => !!G.tuneShot,
  set: (v) => { if (!v) G.tuneShot = null },
})
const shot = computed(() => G.tuneShot)
const pj = computed(() => G.project)
const mf = computed(() => pj.value?.manifest?.[String(shot.value?.id)])
const running = computed(() => pj.value?.project?.running)

const form = reactive({ h3_prompt: '', duration: 7, mode: 'ref2va', seed: 0, engine: '' })
const saving = ref(false)
const rendering = ref(false)
const seedRandom = ref(Math.floor(Math.random() * 20000) + 1000)

watch(shot, (s) => {
  if (!s) return
  form.h3_prompt = s.h3_prompt || ''
  form.duration = s.duration || 7
  form.mode = s.mode || 'ref2va'
  form.seed = 0
  seedRandom.value = Math.floor(Math.random() * 20000) + 1000
}, { immediate: true })

const dirty = computed(() =>
  form.h3_prompt !== (shot.value?.h3_prompt || '') ||
  form.duration !== (shot.value?.duration || 7) ||
  form.mode !== (shot.value?.mode || 'ref2va'))

// 云端 + 固定 seed = 不可复现(乙9)。这个弹窗的全部意义就是「同参数反复微调对比」,
// 而云端请求体里没有 seed 字段 —— 不看这条提示,用户会以为自己在做受控对比,
// 实际每次都是新样本,排查方向从一开始就是错的。
// 条件与文案与项目参数抽屉共用同一份实现(renderHints.cloudSeedWarning)。
const cloudSeedNote = computed(() => cloudSeedWarning(form.engine, form.seed))

async function save() {
  saving.value = true
  try {
    // api.putShot 的第二个参数就是请求体本身(api 层内部已带 method/headers);
    // 此前这里把整份 fetch options 塞了进去,后端只收到 method/headers/body 三个键,
    // shot_id 缺失 → 400「参数错误(shot_id 必填)」,微调保存实际从未成功过。
    await api.putShot(pj.value.project.id, {
      shot_id: shot.value.id, h3_prompt: form.h3_prompt, duration: form.duration, mode: form.mode,
    })
    ElMessage.success(`镜头 ${shot.value.id} 微调已保存(该镜指纹已变,重渲生效)`)
    refreshProject()
  } catch (e) { ElMessage.error(e.message) } finally { saving.value = false }
}

async function render() {
  rendering.value = true
  try {
    if (dirty.value) await save()
    await api.run(pj.value.project.id, 'render', [shot.value.id], form.engine || undefined, form.seed || undefined)
    ElMessage.success(`镜头 ${shot.value.id} 已按微调参数重渲`)
    G.tuneShot = null
    refreshProject()
  } catch (e) { ElMessage.error(e.message) } finally { rendering.value = false }
}
</script>

<template>
  <el-dialog v-model="visible" :title="`镜头 ${shot?.id} · 画布微调`" width="880px" top="6vh" destroy-on-close>
    <div class="grid gap-4" style="grid-template-columns: 300px 1fr">
      <!-- 左:画布预览 -->
      <div class="flex flex-col gap-2">
        <div class="tune-canvas">
          <video v-if="mf?.status === 'done' && mf.file"
                 :src="api.fileURL(pj.project.id, mf.file) + '?v=' + (mf.updated_at || '')"
                 controls playsinline style="width:100%;height:100%;object-fit:contain;background:#000" />
          <div v-else class="w-full h-full flex flex-col items-center justify-center gap-2" style="color:var(--mx-text3)">
            <el-icon :size="34"><VideoCamera /></el-icon>
            <span class="timecode">{{ mf?.status === 'rendering' ? '渲染中…' : '尚未渲染' }}</span>
          </div>
        </div>
        <div class="flex items-center gap-2 text-xs" v-if="mf">
          <el-tag size="small" :type="mf.status === 'done' ? 'success' : mf.status === 'failed' ? 'danger' : 'info'">
            {{ mf.status === 'done' ? '完成' : mf.status === 'rendering' ? '渲染中' : mf.status === 'failed' ? '失败' : '待渲' }}
          </el-tag>
          <span class="timecode">{{ mf.engine === 'comfy' ? '本地' : mf.engine ? '云端' : '' }}</span>
          <span class="timecode" v-if="mf.error" style="color:var(--mx-red)">{{ mf.error.slice(0, 60) }}</span>
        </div>
      </div>

      <!-- 右:微调面板 -->
      <div class="flex flex-col gap-3">
        <el-form label-position="top" size="small">
          <div class="grid gap-x-5" style="grid-template-columns:1fr 1fr 1fr">
            <el-form-item label="时长(秒)">
              <el-input-number v-model="form.duration" :min="4" :max="10" style="width:100%" />
            </el-form-item>
            <el-form-item label="生成模式">
              <el-select v-model="form.mode">
                <el-option value="ref2va" label="参考图(ref2va)" />
                <el-option value="t2va" label="纯文生(t2va)" />
              </el-select>
            </el-form-item>
            <el-form-item label="引擎(本次)">
              <el-select v-model="form.engine">
                <el-option value="" label="按项目配置" />
                <el-option value="auto" label="自动" />
                <el-option value="comfy" label="本地 ComfyUI" />
                <el-option value="api" label="云端 API" />
              </el-select>
            </el-form-item>
          </div>
          <el-form-item label="Seed(0=随机;固定值可在同参数下反复微调对比)">
            <div class="flex gap-2 w-full">
              <el-input-number v-model="form.seed" :min="0" :max="2147483647" style="flex:1" />
              <el-button size="small" @click="form.seed = seedRandom">随机 {{ seedRandom }}</el-button>
            </div>
          </el-form-item>
          <el-alert v-if="cloudSeedNote" type="warning" :closable="false" :title="cloudSeedNote" class="mb-3" />
          <el-form-item label="H3 提示词(六段式;修改后该镜自动按新指纹重渲)">
            <el-input v-model="form.h3_prompt" type="textarea" :rows="10" style="font-family:var(--mx-mono);font-size:11.5px" />
          </el-form-item>
        </el-form>
      </div>
    </div>
    <template #footer>
      <span class="text-xs mr-2" style="color:var(--mx-text3)" v-if="dirty">有未保存修改</span>
      <el-button @click="visible = false">关闭</el-button>
      <el-button :loading="saving" :disabled="running || !dirty" @click="save">保存微调</el-button>
      <el-button type="primary" :loading="rendering" :disabled="running" @click="render">
        <el-icon class="mr-1"><CaretRight /></el-icon>保存并重渲本镜
      </el-button>
    </template>
  </el-dialog>
</template>
