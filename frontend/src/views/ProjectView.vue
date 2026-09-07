<script setup>
import { G, api, refreshProject } from '../store'
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import PipelineBar from '../components/PipelineBar.vue'
import ShotCard from '../components/ShotCard.vue'

const tab = ref('script')
const sourceText = ref('')
const sourceLoaded = ref(false)

const pj = computed(() => G.project)
const plan = computed(() => pj.value?.plan || null)
const manifest = computed(() => pj.value?.manifest || {})
const assets = computed(() => pj.value?.assets || {})
const running = computed(() => pj.value?.project?.running)

// 首次进入载入源文本
if (pj.value && !sourceLoaded.value) {
  sourceText.value = pj.value.source || ''
  sourceLoaded.value = true
}

const charsWithAsset = computed(() =>
  (plan.value?.characters || []).map(c => ({ ...c, asset: assets.value['char:' + c.id] })))
const scenesWithAsset = computed(() =>
  (plan.value?.scenes || []).map(s => ({ ...s, asset: assets.value['scene:' + s.id] })))

async function saveSource() {
  try {
    await api.saveSource(pj.value.project.id, sourceText.value)
    ElMessage.success('源文本已保存')
  } catch (e) { ElMessage.error(e.message) }
}

async function runPhase(phase) {
  try {
    await api.run(pj.value.project.id, phase)
    ElMessage.success('已启动')
    refreshProject()
  } catch (e) { ElMessage.error(e.message) }
}

function upload(kind, id, name) {
  const input = document.createElement('input')
  input.type = 'file'
  input.accept = 'image/png,image/jpeg,image/webp'
  input.onchange = async () => {
    const f = input.files?.[0]
    if (!f) return
    try {
      await api.uploadAsset(pj.value.project.id, kind, id, name, f)
      ElMessage.success(`「${name}」参考图已上传`)
      refreshProject()
    } catch (e) { ElMessage.error(e.message) }
  }
  input.click()
}

async function removeAsset(assetId) {
  try {
    await api.deleteAsset(pj.value.project.id, assetId)
    refreshProject()
  } catch (e) { ElMessage.error(e.message) }
}

async function openDir() {
  try { await api.openExplorer('output', pj.value.project.id) }
  catch (e) { ElMessage.error(e.message) }
}

async function upgrade2K() {
  try {
    await ElMessageBox.confirm(
      '将把 768P 成片送云端 In-context 再生成 2K(按量计费,约数分钟)。继续?', '云端 2K 定稿',
      { confirmButtonText: '升级', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  try {
    await api.upgrade2k(pj.value.project.id, 0)
    ElMessage.success('2K 定稿已提交,进度见底部日志')
  } catch (e) { ElMessage.error(e.message) }
}

const totalShots = computed(() => plan.value?.shots?.length || 0)
const doneShots = computed(() =>
  Object.values(manifest.value).filter(m => m.status === 'done').length)
</script>

<template>
  <div class="h-full overflow-y-auto">
    <!-- 项目头 + 管线 -->
    <div class="p-4 pb-0">
      <div class="flex items-center gap-3 mb-3">
        <el-button text size="small" @click="G.view='home'; G.project=null">
          <el-icon><ArrowLeft /></el-icon>项目
        </el-button>
        <h2 class="text-base font-bold m-0">{{ pj.project.name }}</h2>
        <el-tag v-if="plan" size="small" effect="plain">《{{ plan.title }}》</el-tag>
        <span v-if="running" class="flex items-center gap-1.5 text-xs" style="color:var(--mx-orange)">
          <i class="dot run"></i>{{ pj.project.phase }} 阶段运行中
        </span>
        <div class="flex-1"></div>
        <span class="timecode" v-if="totalShots">镜头 {{ doneShots }}/{{ totalShots }}</span>
        <el-progress v-if="totalShots" type="circle" :width="34" :stroke-width="4"
                     :percentage="Math.round(doneShots*100/totalShots)" />
      </div>
      <PipelineBar :phase="pj.project.phase || ''" />
    </div>

    <!-- Tabs -->
    <div class="p-4">
      <el-tabs v-model="tab" class="mx-tabs">
        <!-- 剧本 -->
        <el-tab-pane label="剧本" name="script">
          <div class="grid gap-4" style="grid-template-columns: 1fr 340px">
            <div class="panel p-4 flex flex-col" style="min-height: 420px">
              <div class="flex items-center gap-2 mb-2">
                <b>源文本</b><span class="text-xs" style="color:var(--mx-text-2)">故事 / 创意 / 剧本大纲(中文)</span>
                <div class="flex-1"></div>
                <el-button size="small" @click="saveSource">保存</el-button>
              </div>
              <el-input v-model="sourceText" type="textarea" :rows="16" resize="none"
                        placeholder="写下你的故事。例如:深夜的修仙都市,外卖员林凡接到一单送往云顶天宫的订单…&#10;&#10;LLM 将据此直出分镜与完整 H3 提示词(六段式/三段式,对白原生配音)。"
                        style="flex:1" :input-style="{ fontFamily: 'inherit' }" />
            </div>
            <div class="flex flex-col gap-4">
              <div class="panel p-4">
                <b class="text-sm">运行「剧本分镜」</b>
                <p class="text-xs mt-2 mb-3" style="color:var(--mx-text-2)">
                  LLM 直出:角色卡 / 场景卡 / 分镜表 + 每镜完整 H3 提示词(对白逐字保留、原生配音)。
                </p>
                <el-button type="primary" :disabled="running" @click="runPhase('script')">
                  <el-icon class="mr-1"><EditPen /></el-icon>生成分镜方案
                </el-button>
              </div>
              <div v-if="plan" class="panel p-4 text-sm flex flex-col gap-2">
                <div><b style="color:var(--mx-orange)">《{{ plan.title }}》</b></div>
                <div style="color:var(--mx-text-2)">{{ plan.logline }}</div>
                <div class="timecode" style="word-break:break-all">{{ plan.style }}</div>
              </div>
            </div>
          </div>
        </el-tab-pane>

        <!-- 分镜故事板 -->
        <el-tab-pane :label="`分镜(${totalShots})`" name="storyboard" :disabled="!plan">
          <div class="grid gap-4" style="grid-template-columns:repeat(auto-fill,minmax(230px,1fr))">
            <ShotCard v-for="s in plan?.shots || []" :key="s.id"
                      :shot="s" :manifest-item="manifest[String(s.id)]" />
          </div>
        </el-tab-pane>

        <!-- 资产 -->
        <el-tab-pane label="资产" name="assets" :disabled="!plan">
          <div class="flex flex-col gap-6">
            <section>
              <h3 class="text-sm font-bold mb-3 flex items-center gap-2">
                <el-icon><User /></el-icon>角色定妆照
                <span class="text-xs font-normal" style="color:var(--mx-text-2)">半身正脸构图 · 锁脸参考(Ref2VA &lt;Picture 1&gt;)</span>
              </h3>
              <div class="grid gap-4" style="grid-template-columns:repeat(auto-fill,minmax(180px,1fr))">
                <div v-for="c in charsWithAsset" :key="c.id" class="shot-card p-3 flex flex-col gap-2">
                  <div class="flex items-center gap-2">
                    <b class="text-sm truncate">{{ c.name }}</b>
                    <i class="dot" :class="c.asset ? 'ok' : 'warn'"></i>
                    <div class="flex-1"></div>
                    <el-button v-if="c.asset" text size="small" @click="removeAsset('char:'+c.id)"><el-icon><Delete /></el-icon></el-button>
                  </div>
                  <div style="aspect-ratio:3/4;background:var(--mx-bg-2);border-radius:8px;overflow:hidden" class="flex items-center justify-center cursor-pointer"
                       @click="upload('characters', 'char:'+c.id, c.name)">
                    <img v-if="c.asset" :src="api.fileURL(pj.project.id, c.asset.file)"
                         style="width:100%;height:100%;object-fit:cover" />
                    <div v-else class="text-center text-xs p-2" style="color:var(--mx-text-2)">
                      <el-icon :size="22"><Plus /></el-icon><div class="mt-1">上传定妆照</div>
                    </div>
                  </div>
                  <div class="text-xs" style="color:var(--mx-text-2)">{{ c.description }}</div>
                </div>
              </div>
            </section>
            <section>
              <h3 class="text-sm font-bold mb-3 flex items-center gap-2">
                <el-icon><MapLocation /></el-icon>场景参考图
                <span class="text-xs font-normal" style="color:var(--mx-text-2)">空镜锚定 &amp; 氛围参考(&lt;Picture 2&gt;)</span>
              </h3>
              <div class="grid gap-4" style="grid-template-columns:repeat(auto-fill,minmax(220px,1fr))">
                <div v-for="s in scenesWithAsset" :key="s.id" class="shot-card p-3 flex flex-col gap-2">
                  <div class="flex items-center gap-2">
                    <b class="text-sm truncate">{{ s.name }}</b>
                    <i class="dot" :class="s.asset ? 'ok' : 'warn'"></i>
                    <div class="flex-1"></div>
                    <el-button v-if="s.asset" text size="small" @click="removeAsset('scene:'+s.id)"><el-icon><Delete /></el-icon></el-button>
                  </div>
                  <div style="aspect-ratio:16/9;background:var(--mx-bg-2);border-radius:8px;overflow:hidden" class="flex items-center justify-center cursor-pointer"
                       @click="upload('scenes', 'scene:'+s.id, s.name)">
                    <img v-if="s.asset" :src="api.fileURL(pj.project.id, s.asset.file)"
                         style="width:100%;height:100%;object-fit:cover" />
                    <div v-else class="text-center text-xs p-2" style="color:var(--mx-text-2)">
                      <el-icon :size="22"><Plus /></el-icon><div class="mt-1">上传场景图</div>
                    </div>
                  </div>
                  <div class="text-xs" style="color:var(--mx-text-2)">{{ s.description }}</div>
                </div>
              </div>
            </section>
          </div>
        </el-tab-pane>

        <!-- 成片 -->
        <el-tab-pane label="成片" name="output">
          <div class="grid gap-4" style="grid-template-columns:minmax(300px,420px) 1fr">
            <div class="panel p-4">
              <div v-if="pj.output?.ready_2k || pj.output?.ready">
                <video v-if="pj.output?.ready_2k"
                       :src="api.fileURL(pj.project.id, pj.output.final_2k) + '?v=' + (pj.project.updated_at||'')"
                       controls playsinline
                       class="w-full rounded-lg bg-black" style="aspect-ratio:9/16" />
                <video v-else
                       :src="api.fileURL(pj.project.id, pj.output.final) + '?v=' + (pj.project.updated_at||'')"
                       controls playsinline
                       class="w-full rounded-lg bg-black" style="aspect-ratio:9/16" />
                <div class="mt-2 text-center">
                  <el-tag v-if="pj.output?.ready_2k" size="small" type="warning" effect="dark">2K 定稿版</el-tag>
                  <el-tag v-else size="small" effect="plain">768P 标准版</el-tag>
                </div>
              </div>
              <div v-else class="text-xs p-10 text-center" style="color:var(--mx-text-2)">
                渲染完成后,运行「合成成片」阶段即可在此预览与导出
              </div>
            </div>
            <div class="flex flex-col gap-4">
              <div class="panel p-4">
                <b class="text-sm">合成参数</b>
                <p class="text-xs mt-2" style="color:var(--mx-text-2)">
                  转场 / 字幕 / 响度可在管线卡片「质检」「合成成片」的 ⚙ 调优入口修改(项目级独立生效)。
                </p>
                <div class="flex gap-2 mt-3 flex-wrap">
                  <el-button type="primary" :disabled="running || !totalShots" @click="runPhase('assemble')">
                    <el-icon class="mr-1"><Film /></el-icon>合成成片
                  </el-button>
                  <el-button :disabled="running || !totalShots" @click="runPhase('qc')">
                    <el-icon class="mr-1"><CircleCheck /></el-icon>质检
                  </el-button>
                  <el-button type="warning" plain :disabled="running || !pj.output?.ready" @click="upgrade2K"
                            title="本地 768P 成片送云端 regeneration 原生重生成 2K(消耗云额度)">
                    <el-icon class="mr-1"><MagicStick /></el-icon>升级 2K
                  </el-button>
                  <el-button @click="openDir"><el-icon class="mr-1"><FolderOpened /></el-icon>打开目录</el-button>
                </div>
              </div>
              <div v-if="pj.qc" class="panel p-4">
                <b class="text-sm">质检报告</b>
                <div class="mt-2 flex flex-col gap-1 text-xs">
                  <div v-for="(r, k) in pj.qc" :key="k" class="flex items-center gap-2">
                    <i class="dot" :class="r.ok ? 'ok' : 'bad'"></i>
                    <span>镜头 {{ k }}</span>
                    <span style="color:var(--mx-text-2)">{{ r.duration.toFixed(1) }}s · 暗像素 {{ (r.dark_ratio*100).toFixed(0) }}% · {{ r.has_audio ? '有音轨' : '无音轨' }}</span>
                    <span v-if="!r.ok" style="color:var(--mx-red)">{{ (r.problems||[]).join(';') }}</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </el-tab-pane>
      </el-tabs>
    </div>
  </div>
</template>
