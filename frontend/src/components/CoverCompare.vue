<!-- 封面对比:左边旧封面,右边新候选(用户 2026-09-26:「重做的封面放大预览左侧需要显示旧的,
     右侧显示新的,这样才好对比」)。

     为什么不用 el-image-viewer:它是**单张**查看器 —— 一次只看得到一张,"比原来好在哪"
     恰恰是挑封面时唯一要回答的问题。并排 + 同一视角,才是能下判断的形态。

     三个设计取舍:
       ① 左右共用同一套缩放/平移(不是各管各的):两张图宽高比一致时,放大后左右指着的是
          同一个部位 —— 这才叫对比;各缩放各的,眼睛还得自己做换算。
       ② 右侧才是候选,且候选可原地换(← →):作者的真实动作是"拿新的一张跟旧封面比",
          所以旧的那侧固定不动,新的那侧来回翻。
       ③ 「用这张」放在这里:比完就能直接定稿,不必关掉再回弹窗里找那张图。
-->
<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'

const props = defineProps({
  title: { type: String, default: '' },      // 书名(标题里显示)
  oldUrl: { type: String, default: '' },     // 现封面地址(空 = 这本书还没有封面)
  items: { type: Array, default: () => [] }, // 候选 [{file, url, seed}]
  index: { type: Number, default: 0 },       // 从第几张看起
})
const emit = defineEmits(['close', 'apply'])

const i = ref(props.index)
const cur = computed(() => props.items[i.value] || null)

// 缩放/平移:左右两侧共用 → 同宽高比时同一处对得上
const scale = ref(1)
const tx = ref(0)
const ty = ref(0)
const viewStyle = computed(() => ({ transform: `translate(${tx.value}px, ${ty.value}px) scale(${scale.value})` }))
const MIN_SCALE = 0.4
const MAX_SCALE = 6
function zoomBy(f) {
  scale.value = Math.min(MAX_SCALE, Math.max(MIN_SCALE, +(scale.value * f).toFixed(3)))
}
function reset() { scale.value = 1; tx.value = 0; ty.value = 0 }
function onWheel(e) { zoomBy(e.deltaY < 0 ? 1.12 : 1 / 1.12) }

// 拖动平移(按住左键拖;松开在 window 上收尾,鼠标划出面板也不会"粘住")
let drag = null
function onMove(e) {
  if (!drag) return
  tx.value = drag.tx + (e.clientX - drag.x)
  ty.value = drag.ty + (e.clientY - drag.y)
}
function onUp() {
  drag = null
  window.removeEventListener('mousemove', onMove)
  window.removeEventListener('mouseup', onUp)
}
function onDown(e) {
  if (e.button !== 0) return
  drag = { x: e.clientX, y: e.clientY, tx: tx.value, ty: ty.value }
  window.addEventListener('mousemove', onMove)
  window.addEventListener('mouseup', onUp)
  e.preventDefault()
}

function step(d) {
  const n = props.items.length
  if (!n) return
  i.value = (i.value + d + n) % n
}

// 尺寸直接在图上量(候选是刚出的图,后端只回了 file/seed —— 尺寸从 naturalWidth 拿最准,
// 也不必为此再扩接口)。
// ⚠ 传的是**侧别字符串**,不是 ref:模板里 setup 的 ref 会被自动解包,measure($event, oldSize)
// 传进去的是空字符串,再写 .value 就是「Cannot create property 'value' on string」——
// 图照常显示、界面看着没事,只在控制台静默报错(实测就是这么栽的)。
const oldSize = ref('')
const newSize = ref('')
function measure(e, side) {
  const el = e && e.target
  if (!el || !el.naturalWidth) return
  const v = el.naturalWidth + '×' + el.naturalHeight
  if (side === 'old') oldSize.value = v
  else newSize.value = v
}

function onKey(e) {
  if (e.key === 'Escape') { e.stopPropagation(); emit('close') }
  else if (e.key === 'ArrowLeft') step(-1)
  else if (e.key === 'ArrowRight') step(1)
  else if (e.key === '0') reset()
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => { window.removeEventListener('keydown', onKey); onUp() })
</script>

<template>
  <Teleport to="body">
    <div class="cc-mask" @click.self="emit('close')">
      <div class="cc-shell">
        <div class="cc-head">
          <el-icon :size="14" color="var(--mx-amber)"><Picture /></el-icon>
          <b class="text-sm">封面对比</b>
          <span v-if="title" class="timecode">《{{ title }}》</span>
          <div class="flex-1"></div>
          <span class="timecode">候选 {{ items.length ? i + 1 : 0 }} / {{ items.length }}</span>
          <button class="cc-btn cc-x" title="关闭(Esc)" @click="emit('close')">
            <el-icon :size="15"><Close /></el-icon>
          </button>
        </div>

        <div class="cc-body" @wheel.prevent="onWheel" @mousedown="onDown" @dblclick="reset">
          <!-- 左:旧(现封面)。它固定不动 —— 比的是"新的这张值不值得换掉它" -->
          <div class="cc-pane">
            <div class="cc-tag">
              <span>旧封面</span>
              <s v-if="oldSize">{{ oldSize }}</s>
            </div>
            <div class="cc-stage">
              <img v-if="oldUrl" :src="oldUrl" :style="viewStyle" draggable="false"
                   alt="旧封面" @load="measure($event, 'old')" />
              <div v-else class="cc-empty">
                <el-icon :size="26"><Picture /></el-icon>
                <span>这本书还没有封面</span>
                <span class="timecode">生成一张「用这张」之后,它就成了左边的对比对象</span>
              </div>
            </div>
          </div>
          <!-- 右:新(候选) -->
          <div class="cc-pane">
            <div class="cc-tag new">
              <span>候选</span>
              <s v-if="cur && cur.seed">seed {{ cur.seed }}</s>
              <s v-if="newSize">{{ newSize }}</s>
            </div>
            <div class="cc-stage">
              <img v-if="cur" :src="cur.url" :style="viewStyle" draggable="false"
                   alt="候选封面" @load="measure($event, 'new')" />
              <div v-else class="cc-empty">
                <el-icon :size="26"><Picture /></el-icon>
                <span>还没有候选</span>
              </div>
            </div>
          </div>
        </div>

        <div class="cc-foot">
          <button class="cc-btn" :disabled="items.length < 2" title="上一张候选(←)" @click="step(-1)">
            <el-icon :size="13"><ArrowLeft /></el-icon>
          </button>
          <button class="cc-btn" :disabled="items.length < 2" title="下一张候选(→)" @click="step(1)">
            <el-icon :size="13"><ArrowRight /></el-icon>
          </button>
          <span class="cc-sep"></span>
          <button class="cc-btn" title="缩小" @click="zoomBy(1 / 1.25)"><el-icon :size="13"><ZoomOut /></el-icon></button>
          <span class="timecode cc-pct">{{ Math.round(scale * 100) }}%</span>
          <button class="cc-btn" title="放大" @click="zoomBy(1.25)"><el-icon :size="13"><ZoomIn /></el-icon></button>
          <button class="cc-btn" title="复位(双击画面 / 数字 0)" @click="reset">复位</button>
          <div class="flex-1"></div>
          <span class="timecode cc-hint">左右同视角:滚轮缩放 · 拖动平移 · ← → 换候选 · Esc 关闭</span>
          <el-button type="primary" size="small" :disabled="!cur" @click="emit('apply', cur)">用这张</el-button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
/* 蒙层口径：**压暗 + 柔化，不遮挡**（2026-09-27 用户报障后统一，数值见 style.css 的 --mx-scrim*）。
   这里用 strong 那一档：新旧封面要互相分得清，需要比普通预览更强的分离。
   早先是 bg1 88% → bg0 95% 的渐变（近乎全黑），桌面壳里会把后面的应用整块盖掉。 */
.cc-mask {
  position: fixed;
  inset: 0;
  z-index: 4000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 22px;
  background:
    var(--mx-scrim-strong),
    color-mix(in srgb, var(--mx-amber) 5%, transparent);
  backdrop-filter: blur(8px) saturate(.9);
}
.cc-shell {
  width: min(96vw, 1360px);
  height: min(92vh, 920px);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: linear-gradient(170deg, var(--mx-bg2), var(--mx-bg1));
  border: 1px solid var(--mx-line-hi);
  border-radius: 14px;
  box-shadow: 0 24px 70px color-mix(in srgb, var(--mx-bg0) 80%, transparent);
}
.cc-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  border-bottom: 1px solid var(--mx-line);
}
.cc-body {
  flex: 1;
  min-height: 0;
  display: flex;
  cursor: grab;
  user-select: none;
}
.cc-body:active { cursor: grabbing; }
.cc-pane {
  flex: 1;
  min-width: 0;
  position: relative;
  display: flex;
  overflow: hidden;
  background: color-mix(in srgb, var(--mx-bg0) 55%, transparent);
}
/* 中缝:一条线把"旧"和"新"分开,别让两张图糊成一片 */
.cc-pane + .cc-pane { border-left: 1px solid var(--mx-line-hi); }
.cc-stage {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}
.cc-stage img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
  display: block;
  border-radius: 8px;
  /* 与放大预览同一套:accent 细边 + 光晕 + 深投影,暗调封面才有边界 */
  box-shadow:
    0 0 0 1px color-mix(in srgb, var(--mx-amber) 32%, transparent),
    0 0 90px color-mix(in srgb, var(--mx-amber) 14%, transparent),
    0 22px 60px color-mix(in srgb, var(--mx-bg0) 78%, transparent);
}
.cc-tag {
  position: absolute;
  top: 10px;
  left: 10px;
  z-index: 2;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 3px 9px;
  border-radius: 999px;
  border: 1px solid var(--mx-line);
  background: color-mix(in srgb, var(--mx-bg0) 66%, transparent);
  backdrop-filter: blur(4px);
  font-family: var(--mx-mono);
  font-size: 11px;
  color: var(--mx-text2);
}
.cc-tag.new {
  color: var(--mx-amber);
  border-color: color-mix(in srgb, var(--mx-amber) 38%, transparent);
}
.cc-tag s { text-decoration: none; opacity: .75; }
.cc-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  color: var(--mx-text3);
  font-size: 12px;
  text-align: center;
}
.cc-foot {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 9px 12px;
  border-top: 1px solid var(--mx-line);
}
.cc-sep {
  width: 1px;
  height: 18px;
  background: var(--mx-line-hi);
  margin: 0 2px;
}
.cc-btn {
  height: 28px;
  min-width: 30px;
  padding: 0 8px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  border: 1px solid var(--mx-line-hi);
  border-radius: 8px;
  background: var(--mx-hover-soft);
  color: var(--mx-text2);
  font-size: 12px;
  cursor: pointer;
  transition: all .15s;
}
.cc-btn:hover:not(:disabled) {
  color: var(--mx-text);
  border-color: color-mix(in srgb, var(--mx-amber) 45%, transparent);
}
.cc-btn:disabled { opacity: .35; cursor: not-allowed; }
.cc-x { border-color: transparent; background: transparent; }
.cc-x:hover { color: var(--mx-red); border-color: color-mix(in srgb, var(--mx-red) 45%, transparent); }
.cc-pct { min-width: 44px; text-align: center; }
</style>
