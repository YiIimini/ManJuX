<script setup>
import { G } from '../store'
import { computed } from 'vue'

const visible = computed({
  get: () => !!G.promptShot,
  set: (v) => { if (!v) G.promptShot = null },
})
const shot = computed(() => G.promptShot)
</script>

<template>
  <el-drawer v-model="visible" size="520px" append-to-body>
    <template #header>
      <div class="flex items-center gap-2">
        <b>镜头 {{ shot?.id }} · H3 提示词</b>
        <span v-if="shot" class="badge-mode" :class="shot.mode">{{ shot.mode?.toUpperCase() }}</span>
      </div>
    </template>
    <pre class="text-xs leading-relaxed p-3 rounded-lg" style="white-space:pre-wrap;word-break:break-word;background:var(--mx-bg1);color:var(--mx-text2);font-family:var(--mx-mono)">{{ shot?.h3_prompt }}</pre>
    <div class="text-xs mt-3" style="color:var(--mx-text2)">
      提示词由 LLM 按 MiniMax-H3 官方规范直出(Ref2VA 六段式 / T2VA 三段式);对白逐字保留于 &lt;d&gt; 标签,由 H3 原生配音。
      修改请回到「剧本分镜」阶段调整源文本或风格要求后重新生成。
    </div>
  </el-drawer>
</template>
