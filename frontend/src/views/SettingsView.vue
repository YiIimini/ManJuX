<script setup>
import { G, api, refreshStatus } from '../store'
import { reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'

const form = reactive({
  server_port: 8790, data_dir: '', auto_open_ui: true,
  llm: {}, comfy: {}, minimax: {}, ff: {}, render: {},
})

onMounted(async () => {
  try {
    const s = await api.getSettings()
    Object.assign(form, s)
  } catch (e) { ElMessage.error(e.message) }
})

async function save() {
  try {
    await api.putSettings(form)
    ElMessage.success('设置已保存')
    refreshStatus()
  } catch (e) { ElMessage.error(e.message) }
}

function testLLM() {
  ElMessage.info('LLM 连通性将在下次「剧本分镜」运行时验证;也可在此直接保存后运行任意项目。')
}
</script>

<template>
  <div class="h-full overflow-y-auto p-6">
    <div class="max-w-[860px] mx-auto flex flex-col gap-5">
      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
          <el-icon color="var(--mx-cyan)"><ChatDotRound /></el-icon>文本大模型(LLM · OpenAI 兼容)
        </h3>
        <el-form label-position="top" class="grid gap-x-6" style="grid-template-columns:1fr 1fr">
          <el-form-item label="Base URL">
            <el-input v-model="form.llm.base_url" placeholder="https://api.deepseek.com/v1" />
          </el-form-item>
          <el-form-item label="模型">
            <el-input v-model="form.llm.model" placeholder="deepseek-chat" />
          </el-form-item>
          <el-form-item label="API Key" class="col-span-2">
            <el-input v-model="form.llm.api_key" show-password placeholder="sk-…(本地存储于 data/settings.json)" />
          </el-form-item>
        </el-form>
      </div>

      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
          <el-icon color="var(--mx-orange)"><Cpu /></el-icon>本地 ComfyUI 引擎(H3-Base)
        </h3>
        <el-form label-position="top" class="grid gap-x-6" style="grid-template-columns:1fr 1fr">
          <el-form-item label="服务地址">
            <el-input v-model="form.comfy.base_url" placeholder="http://127.0.0.1:8190" />
          </el-form-item>
          <el-form-item label="根目录(含 ComfyUI/ 与 standalone-env/)">
            <el-input v-model="form.comfy.root_dir" placeholder="D:\Ai\ManJuX\comfyui" />
          </el-form-item>
          <el-form-item label="未运行时自动代管启动">
            <el-switch v-model="form.comfy.auto_start" />
          </el-form-item>
          <el-form-item label="两阶段渲染(条件缓存 .pt)">
            <el-switch v-model="form.comfy.cond_cache" />
          </el-form-item>
          <el-form-item label="UNET(Ref2VA)" class="col-span-2">
            <el-input v-model="form.comfy.unet_ref2va" />
          </el-form-item>
          <el-form-item label="UNET(FL2VA / T2VA)" class="col-span-2">
            <el-input v-model="form.comfy.unet_fl2va" />
          </el-form-item>
          <el-form-item label="Turbo LoRA(Ref2VA)">
            <el-input v-model="form.comfy.turbo_lora_ref2va" />
          </el-form-item>
          <el-form-item label="Turbo LoRA(FL2VA)">
            <el-input v-model="form.comfy.turbo_lora_fl2va" />
          </el-form-item>
          <el-form-item label="文本编码器(CLIP · qwen3vl)">
            <el-input v-model="form.comfy.clip" />
          </el-form-item>
          <el-form-item label="视频 VAE">
            <el-input v-model="form.comfy.video_vae" />
          </el-form-item>
          <el-form-item label="音频 VAE(缺失=黑屏)">
            <el-input v-model="form.comfy.audio_vae" />
          </el-form-item>
        </el-form>
      </div>

      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
          <el-icon color="var(--mx-cyan)"><Cloudy /></el-icon>MiniMax H3 云端 API
        </h3>
        <el-form label-position="top" class="grid gap-x-6" style="grid-template-columns:1fr 1fr">
          <el-form-item label="Base URL">
            <el-select v-model="form.minimax.base_url">
              <el-option value="https://api.minimax.cn" label="国内(api.minimax.cn)" />
              <el-option value="https://api.minimaxi.com" label="国际(api.minimaxi.com)" />
            </el-select>
          </el-form-item>
          <el-form-item label="模型">
            <el-select v-model="form.minimax.model">
              <el-option value="MiniMax-H3" label="MiniMax-H3(768P/2K)" />
              <el-option value="MiniMax-H3-Max" label="MiniMax-H3-Max(480P/768P,高速)" />
            </el-select>
          </el-form-item>
          <el-form-item label="API Key" class="col-span-2">
            <el-input v-model="form.minimax.api_key" show-password placeholder="eyJ…(platform.minimaxi.com 创建)" />
          </el-form-item>
        </el-form>
      </div>

      <div class="panel p-5">
        <h3 class="text-sm font-bold m-0 mb-4 flex items-center gap-2">
          <el-icon><Monitor /></el-icon>应用
        </h3>
        <el-form label-position="top" class="grid gap-x-6" style="grid-template-columns:1fr 1fr">
          <el-form-item label="HTTP 端口(占用自动 +1)">
            <el-input-number v-model="form.server_port" :min="1024" :max="65535" style="width:100%" />
          </el-form-item>
          <el-form-item label="启动时自动打开工作台">
            <el-switch v-model="form.auto_open_ui" />
          </el-form-item>
          <el-form-item label="数据目录" class="col-span-2">
            <el-input v-model="form.data_dir" disabled />
          </el-form-item>
          <el-form-item label="ffmpeg 路径(空=PATH)">
            <el-input v-model="form.ff.ffmpeg_path" placeholder="ffmpeg" />
          </el-form-item>
          <el-form-item label="ffprobe 路径(空=PATH)">
            <el-input v-model="form.ff.ffprobe_path" placeholder="ffprobe" />
          </el-form-item>
        </el-form>
      </div>

      <div class="flex justify-end gap-2">
        <el-button @click="testLLM">测试</el-button>
        <el-button type="primary" @click="save">
          <el-icon class="mr-1"><Check /></el-icon>保存设置
        </el-button>
      </div>
    </div>
  </div>
</template>
