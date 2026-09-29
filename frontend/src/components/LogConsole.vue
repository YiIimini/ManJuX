<script setup>
import { G, api, fmtTime, logDot, phaseLabel } from '../store'
import { computed, ref, onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'

// 一键停止:运行中的管线(通常 0 或 1 个;批量产线归并为单管线逐集推进)
const runningIds = computed(() => Object.keys(G.status?.running || {}))
async function confirmStop() {
  const ids = runningIds.value.slice()
  if (!ids.length) return
  const names = ids.map(projName).filter(Boolean).join('、')
  try {
    await ElMessageBox.confirm(
      `确定停止当前运行中的管线?${names ? '(『' + names + '』)' : ''}渲染中的任务将被中断,已完成阶段保留,修复问题后可续跑。`,
      '一键停止', { confirmButtonText: '停止', cancelButtonText: '取消', type: 'warning' })
  } catch { return }
  let ok = 0
  for (const id of ids) {
    try { await api.stop(id); ok++ } catch { /* 单个失败继续 */ }
  }
  if (ok) ElMessage.success('已停止 ' + ok + ' 条管线')
}

// 项目名(事件流是全局的,跨项目运行都要能分清归属)
const projName = (id) => {
  if (!id) return ''
  const p = (G.projects || []).find(x => x.id === id)
  return p ? p.name : id
}
const curProj = () => G.project?.project?.id

// 倒序显示:最新事件恒在顶部,无需自动滚动(数据层 G.logs 仍时序追加,
// 底栏 ticker 与增量轮询不受影响)
const reversed = computed(() => {
  const arr = G.logs.slice()
  arr.reverse()
  return arr
})

// ---- 按**管线节点**分组（2026-09-29 用户要求：「运行日志一眼看明白、分类配色、
//      可收起展开、成功的分类自动收起」）----
//
// 为什么要分组：一条管线跑一集会产生**几百条**事件（每 30 秒一条进度 + 每镜若干条），
// 平铺成一列时"这一集现在走到哪、哪一步出过问题"要靠人自己扒。
// 分组口径 = **同一项目 + 同一阶段**的连续事件（阶段是后端事件自带的 phase 字段，
// 与界面上的五阶段一致），跨项目运行也分得清。
//
// 配色按阶段给（见 PHASE_TONE），与左侧阶段栏同一套色，扫一眼就知道是哪一步在说话。
//
// 【自动收起】默认规则不是"记住用户点过什么"，而是**按状态算**：
//   · 最新的那一组（正在跑的那一步）默认展开；
//   · 出过 warn/error 的组默认展开（问题要一直看得见）；
//   · 其余（已经顺利完成、被后来者顶下去的阶段）默认收起。
// 于是"成功的分类自动收起"是**自然结果**，不需要任何 watcher 去监听"阶段完成"事件。
// 用户手动展开/收起过的那一组，以他点过的为准（manual 表）。
const PHASE_TONE = {
  script: 'var(--mx-cyan)',
  assets: 'var(--mx-purple)',
  render: 'var(--mx-yellow)',
  qc: 'var(--mx-green)',
  assemble: 'var(--mx-green)',
  novel: 'var(--mx-cyan)',
  llm: 'var(--mx-purple)',
  pipeline: 'var(--mx-text2)',
  system: 'var(--mx-text3)',
  comfy: 'var(--mx-text2)',
}
// 严重度排序:分组头按**组内最严重**的那条着色(info < success < warn < error)。
const LEVEL_RANK = { info: 0, success: 1, warn: 2, error: 3 }
function worstLevel(a, b) {
  return (LEVEL_RANK[b] ?? 0) > (LEVEL_RANK[a] ?? 0) ? b : a
}
const manual = ref({}) // groupId → bool(用户点过的那一组以他为准)

const groups = computed(() => {
  const out = []
  for (const l of reversed.value) {
    const key = (l.project_id || '') + '|' + (l.phase || '')
    const cur = out[out.length - 1]
    if (cur && cur.key === key) {
      cur.events.push(l)
      cur.level = worstLevel(cur.level, l.level)
      continue
    }
    out.push({
      key,
      // 组 id 取**最旧**那条的 seq:一组在长（新事件从头部插入），最旧的 seq 不变，
      // 手动展开状态不会每来一条事件就被重置
      id: key + '#' + l.seq,
      phase: l.phase || '',
      project: l.project_id || '',
      events: [l],
      level: l.level,
    })
  }
  return out.map((g, i) => {
    const hasProblem = g.level === 'warn' || g.level === 'error'
    const open = manual.value[g.id] !== undefined ? manual.value[g.id] : (i === 0 || hasProblem)
    // **已经跑完的那一组标绿**（2026-09-29 用户要求："已经完成的节点大类显示绿色"）：
    // "完成"的判据不是"有没有 success 事件"，而是**它已经不是最新那一组了**
    // —— 阶段跑完就被下一步顶下去，颜色随之从阶段色变成绿色；出过问题的仍是黄/红。
    const done = i > 0 && !hasProblem
    const tone = hasProblem
      ? (g.level === 'error' ? 'var(--mx-red)' : 'var(--mx-yellow)')
      : (done ? 'var(--mx-green)' : (PHASE_TONE[g.phase] || 'var(--mx-text2)'))
    return { ...g, count: g.events.length, open, tone, hasProblem, done }
  })
})
function toggleGroup(g) {
  manual.value = { ...manual.value, [g.id]: !g.open }
}

// groupTip 鼠标停在组头时给的**详情气泡**（用户要求："鼠标移上去显示详细的Tip气泡信息"）：
// 这一组是什么阶段、哪个项目、多少条、按级别各多少、最新一条说了什么。
function groupTip(g) {
  const c = { info: 0, success: 0, warn: 0, error: 0 }
  for (const e of g.events) c[e.level] = (c[e.level] || 0) + 1
  return [
    `${phaseLabel(g.phase) || '系统'} · ${g.count} 条`,
    g.project ? `项目：${projName(g.project)}` : '全局事件（不属于某个项目）',
    `成功 ${c.success} · 提示 ${c.info} · 警告 ${c.warn} · 失败 ${c.error}`,
    g.done ? '状态：已完成（被后续阶段顶下去，已自动收起）' : g.hasProblem ? '状态：有问题，点开看详情' : '状态：进行中',
    `最新：${g.events[0].message}`,
  ].join('\n')
}

// ---- 弹窗:标题栏拖动 + 右下角调大小(非模态,不挡主界面) ----
const root = ref(null)
const pos = ref(null) // null = 默认右下角锚定;拖动后为 {left, top}
const size = ref({ w: 780, h: 440 })
let drag = null
let rsz = null

function onDragDown(e) {
  if (e.button !== 0) return
  const r = root.value.getBoundingClientRect()
  pos.value = { left: r.left, top: r.top }
  drag = { x: e.clientX, y: e.clientY, left: r.left, top: r.top }
  window.addEventListener('mousemove', onDragMove)
  window.addEventListener('mouseup', onDragUp)
  e.preventDefault()
}
function onDragMove(e) {
  if (!drag) return
  const left = Math.min(Math.max(0, drag.left + e.clientX - drag.x), window.innerWidth - 80)
  const top = Math.min(Math.max(0, drag.top + e.clientY - drag.y), window.innerHeight - 40)
  pos.value = { left, top }
}
function onDragUp() {
  drag = null
  window.removeEventListener('mousemove', onDragMove)
  window.removeEventListener('mouseup', onDragUp)
}
function onRszDown(e) {
  if (e.button !== 0) return
  rsz = { x: e.clientX, y: e.clientY, w: size.value.w, h: size.value.h }
  window.addEventListener('mousemove', onRszMove)
  window.addEventListener('mouseup', onRszUp)
  e.preventDefault()
  e.stopPropagation()
}
function onRszMove(e) {
  if (!rsz) return
  size.value = {
    w: Math.min(Math.max(420, rsz.w + e.clientX - rsz.x), window.innerWidth - 16),
    h: Math.min(Math.max(220, rsz.h + e.clientY - rsz.y), window.innerHeight - 16),
  }
}
function onRszUp() {
  rsz = null
  window.removeEventListener('mousemove', onRszMove)
  window.removeEventListener('mouseup', onRszUp)
}
onBeforeUnmount(() => {
  window.removeEventListener('mousemove', onDragMove)
  window.removeEventListener('mouseup', onDragUp)
  window.removeEventListener('mousemove', onRszMove)
  window.removeEventListener('mouseup', onRszUp)
})
</script>

<template>
  <div ref="root" class="log-console log-pop" :style="{
    width: size.w + 'px', height: size.h + 'px',
    ...(pos ? { left: pos.left + 'px', top: pos.top + 'px', right: 'auto', bottom: 'auto', transform: 'none' } : {}),
  }">
      <div class="log-pop-bar" title="拖动移动" @mousedown="onDragDown">
        <span class="tick-dot" :class="logDot"></span>
        <span class="timecode" :style="{ color: logDot === 'ok' ? 'var(--mx-green)' : logDot === 'bad' ? 'var(--mx-red)' : logDot === 'warn' ? 'var(--mx-yellow)' : 'var(--mx-text3)' }">记录中</span>
        <span class="timecode">运行日志 · 全局实时</span>
        <span class="timecode" v-if="G.logs.length">{{ G.logs.length }} 条事件</span>
        <div class="flex-1"></div>
        <el-tooltip :content="runningIds.length ? '一键停止:停止当前运行中的管线(二次确认)' : '当前无运行中的管线'" placement="bottom">
          <el-button text size="small" class="logbar-ico" :disabled="!runningIds.length"
                     :style="runningIds.length ? 'color:var(--mx-red)' : ''" @click="confirmStop" @mousedown.stop>
            <el-icon :size="14"><VideoPause /></el-icon>
          </el-button>
        </el-tooltip>
        <el-tooltip content="清空当前日志显示(磁盘运行史不受影响)" placement="bottom">
          <el-button text size="small" class="logbar-ico" @click="G.logs = []" @mousedown.stop><el-icon :size="14"><Delete /></el-icon></el-button>
        </el-tooltip>
        <el-tooltip content="收起日志面板(底栏日志条可再次展开)" placement="bottom">
          <el-button text size="small" class="logbar-ico" @click="G.logPanelOpen = false" @mousedown.stop><el-icon :size="14"><ArrowDown /></el-icon></el-button>
        </el-tooltip>
      </div>
      <div class="flex-1 overflow-y-auto py-1.5">
        <div v-if="!G.logs.length" class="log-line info"><span class="t">--:--:--</span><span class="tl-dot"></span><span class="body"><span class="m" style="color:var(--mx-text3)">等待管线事件…(运行任意阶段后实时输出)</span></span></div>
        <!-- 分组渲染（2026-09-29 用户要求：一眼看明白 / 分类配色 / 可收起 / 成功的自动收起）。
             每组 = 同一项目 + 同一阶段的连续事件；组头点一下收起/展开。
             key 必须用**稳定标识**（组 id 由最旧那条的 seq 拼成），不能用数组下标 ——
             这条列表是**倒序**的，来一条新事件整列都往后挪一位，用下标当 key 会让 Vue
             认为"每一行都变了"，把最多 800 行全部重新 patch（2026-09-29 性能审计 #4）。 -->
        <div v-for="g in groups" :key="g.id" class="log-group">
          <el-tooltip placement="left-start" :show-after="300" effect="dark">
            <template #content>
              <div class="gtip">{{ groupTip(g) }}</div>
            </template>
            <div class="log-ghead" :class="[g.level, { done: g.done }]" :style="{ '--tone': g.tone }"
                 :title="''" @click="toggleGroup(g)">
              <el-icon :size="12" class="chev" :class="{ open: g.open }"><ArrowRight /></el-icon>
              <span class="gdot" :class="[g.level, { done: g.done }]"></span>
              <span class="gname">{{ phaseLabel(g.phase) || '系统' }}</span>
              <span class="pj" :class="{ cur: g.project === curProj() }" v-if="g.project">[{{ projName(g.project) }}]</span>
              <span class="gcount">{{ g.count }} 条</span>
              <div class="flex-1"></div>
              <span class="gflag" v-if="g.hasProblem">有问题</span>
              <span class="gflag done-flag" v-else-if="g.done">完成</span>
            </div>
          </el-tooltip>
          <!-- 收起时把"最后一条"**整条换行显示**（2026-09-29 用户要求：最后的小字直接换行显示）：
               以前是单行省略号，长消息只看得到开头几个字，等于没看到。 -->
          <div v-if="!g.open" class="gprev-wrap" @click="toggleGroup(g)">
            <span class="gprev">{{ g.events[0].message }}</span>
          </div>
          <template v-if="g.open">
            <div v-for="l in g.events" :key="l.seq" class="log-line" :class="l.level">
              <span class="t">{{ fmtTime(l.at) }}</span>
              <span class="tl-dot"></span>
              <span class="body">
                <span class="pj" :class="{ cur: l.project_id === curProj() }" v-if="l.project_id && !g.project">[{{ projName(l.project_id) }}]</span>
                <span class="m">{{ l.message }}</span>
              </span>
            </div>
          </template>
        </div>
      </div>
      <div class="log-rsz" title="拖动调大小" @mousedown="onRszDown"></div>
    </div>
</template>

<style scoped>
.pj { color: var(--mx-purple); margin-right: 8px; }
.pj.cur { color: var(--mx-amber); }
.logbar-ico { padding: 4px 7px !important; }
.logbar-ico:hover { color: var(--mx-amber-hi) !important; background: color-mix(in srgb, var(--mx-amber) 10%, transparent) !important; }

/* ---- 分组头（2026-09-29）：一行看清"哪一步、多少条、有没有问题"，点一下收起 ---- */
.log-group { border-bottom: 1px solid color-mix(in srgb, var(--mx-line) 55%, transparent); }
.log-group:last-child { border-bottom: none; }
.log-ghead {
  display: flex; align-items: center; gap: 6px;
  padding: 3px 10px 3px 6px; cursor: pointer; user-select: none;
  background: color-mix(in srgb, var(--tone) 7%, transparent);
  border-left: 2px solid color-mix(in srgb, var(--tone) 65%, transparent);
}
.log-ghead:hover { background: color-mix(in srgb, var(--tone) 13%, transparent); }
.log-ghead.warn { border-left-color: var(--mx-yellow); background: color-mix(in srgb, var(--mx-yellow) 9%, transparent); }
.log-ghead.error { border-left-color: var(--mx-red); background: color-mix(in srgb, var(--mx-red) 10%, transparent); }
/* 已完成的那一组：整条走绿（用户要求「已经完成的节点大类显示绿色」） */
.log-ghead.done { border-left-color: color-mix(in srgb, var(--mx-green) 70%, transparent); background: color-mix(in srgb, var(--mx-green) 6%, transparent); }
.log-ghead .chev { color: var(--mx-text3); transition: transform .12s ease; }
.log-ghead .chev.open { transform: rotate(90deg); }
.log-ghead .gdot { width: 6px; height: 6px; border-radius: 50%; flex: none; background: var(--tone); }
.log-ghead .gdot.warn { background: var(--mx-yellow); }
.log-ghead .gdot.error { background: var(--mx-red); }
.log-ghead .gdot.success, .log-ghead .gdot.done { background: var(--mx-green); }
.log-ghead .gname { color: var(--tone); font-size: 12px; font-weight: 600; letter-spacing: .02em; }
.log-ghead.done .gname { color: color-mix(in srgb, var(--mx-green) 82%, var(--mx-text)); }
.log-ghead .gcount { color: var(--mx-text3); font-size: 11px; }
.log-ghead .gflag { color: var(--mx-yellow); font-size: 11px; }
.log-ghead.error .gflag { color: var(--mx-red); }
.log-ghead .done-flag { color: color-mix(in srgb, var(--mx-green) 75%, var(--mx-text3)); }

/* 收起时的那条"最后消息"：**整条换行显示**（用户要求），不再单行省略号 */
.gprev-wrap { padding: 2px 10px 6px 26px; cursor: pointer; }
.gprev-wrap:hover { background: color-mix(in srgb, var(--mx-amber) 5%, transparent); }
.gprev {
  color: var(--mx-text3); font-size: 11px; line-height: 1.5;
  display: block; white-space: normal; word-break: break-word; overflow-wrap: anywhere;
}
/* 组头详情气泡：多行纯文本（\n 由 white-space:pre-line 生效） */
.gtip { white-space: pre-line; max-width: 420px; line-height: 1.6; font-size: 12px; }
</style>
