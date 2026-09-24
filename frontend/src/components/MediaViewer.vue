<!-- 全屏媒体预览器:视频/大图 lightbox,镜头间键盘导航 -->
<script setup>
import { G, api } from '../store'
import { computed, ref, onMounted, onUnmounted, watch } from 'vue'

const visible = computed({
  get: () => !!G.viewer,
  set: (v) => { if (!v) G.viewer = null },
})
const V = computed(() => G.viewer)
const cur = computed(() => V.value?.items?.[V.value.index ?? 0])

function go(d) {
  if (!V.value) return
  const n = V.value.items.length
  V.value.index = ((V.value.index + d) % n + n) % n
}
function onKey(e) {
  if (!visible.value) return
  if (e.key === 'Escape') visible.value = false
  if (e.key === 'ArrowLeft') go(-1)
  if (e.key === 'ArrowRight') go(1)
}
onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <teleport to="body">
    <div v-if="visible && cur" class="mv-mask" @click.self="visible = false">
      <div class="mv-shell panel-glow">
        <!-- 顶栏 -->
        <div class="flex items-center gap-3 px-5 h-14 flex-none" style="border-bottom:1px solid var(--mx-line)">
          <span class="timecode" style="color:var(--mx-amber)">{{ cur.kindLabel || 'PREVIEW' }}</span>
          <b class="text-[15px] truncate">{{ cur.title }}</b>
          <span v-if="cur.meta" class="timecode truncate hidden md:inline">{{ cur.meta }}</span>
          <div class="flex-1"></div>
          <span v-if="V.items.length > 1" class="timecode">{{ V.index + 1 }} / {{ V.items.length }} · ← → 切换</span>
          <el-button circle text @click="visible = false"><el-icon><Close /></el-icon></el-button>
        </div>
        <!-- 媒体区 -->
        <div class="flex-1 flex items-center justify-center relative" style="min-height:0;background:var(--mx-bg0)">
          <video v-if="cur.type === 'video'" :src="cur.src" controls autoplay playsinline
                 class="mv-media" style="object-fit:contain" />
          <img v-else-if="cur.type === 'image'" :src="cur.src" class="mv-media" style="object-fit:contain" />
          <button v-if="V.items.length > 1" class="mv-nav left" @click="go(-1)"><el-icon :size="26"><ArrowLeft /></el-icon></button>
          <button v-if="V.items.length > 1" class="mv-nav right" @click="go(1)"><el-icon :size="26"><ArrowRight /></el-icon></button>
        </div>
        <!-- 信息脚栏 -->
        <div v-if="cur.foot" class="px-5 py-3 flex-none text-xs" style="border-top:1px solid var(--mx-line);color:var(--mx-text2);max-height:120px;overflow:auto">
          {{ cur.foot }}
        </div>
      </div>
    </div>
  </teleport>
</template>

<style scoped>
.mv-mask {
  position: fixed; inset: 0;
  /* 层级(2026-09-12 修「多级弹窗层级异常」):Element 的弹层计数器从 2000 起、
     每多开一层 +1,而全屏预览器原来也是 2000 —— 从「资产库详情」「分镜卡」这类
     弹窗/抽屉里点图打开预览时,预览器被压在弹窗底下(看不见也点不到)。
     现取 2700:高于弹层计数器与运行日志悬浮窗(2600),低于永远最高的 Tip(3000)。 */
  z-index: 2700;
  background: color-mix(in srgb, var(--mx-bg0) 82%, transparent);
  backdrop-filter: blur(10px);
  display: flex; align-items: center; justify-content: center;
  padding: 32px;
}
.mv-shell {
  width: min(92vw, 1100px);
  height: min(90vh, 880px);
  display: flex; flex-direction: column;
  background: linear-gradient(170deg, var(--mx-bg3), var(--mx-bg1));
  border: 1px solid var(--mx-line-hi);
  border-radius: 16px;
  overflow: hidden;
  animation: mvin .22s cubic-bezier(.2,.9,.3,1.2);
}
@keyframes mvin { from { transform: scale(.96); opacity: 0; } to { transform: scale(1); opacity: 1; } }
.mv-media { max-width: 100%; max-height: 100%; }
.mv-nav {
  position: absolute; top: 50%; transform: translateY(-50%);
  width: 46px; height: 46px; border-radius: 50%;
  border: 1px solid var(--mx-line-hi);
  background: color-mix(in srgb, var(--mx-bg1) 70%, transparent); color: var(--mx-text2);
  display: flex; align-items: center; justify-content: center;
  cursor: pointer; transition: all .2s;
}
.mv-nav:hover { color: var(--mx-amber); border-color: var(--mx-amber); background: rgba(16,20,31,.95); }
.mv-nav.left { left: 14px; }
.mv-nav.right { right: 14px; }
</style>
