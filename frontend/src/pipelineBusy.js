// 一次性 GPU 操作的"管线忙"提示(乙5 的**温和版**:不禁用,只告知)。
//
// 背景:局部重绘 / 音频驱动 / 单资产重生成**不参与管线互斥**
// (pipeline.Coordinator 的运行锁只覆盖管线本身)。管线跑着时点这些入口,
// 两条链会抢同一张显卡 —— 两边都变慢,还可能撞显存。
//
// 为什么选"提示"而不是"禁用":这些入口存在的意义正是"随时修一镜",
// 禁用会把那条路堵死(用户要么等整条管线跑完,要么先停掉)。把知情权给用户:
// 说清楚代价,点确认照常提交。
//
// 判据用已有的 /api/status(它的 running 是 map[项目ID]运行态),**提交前现读一次**
// 而不是吃 15s 轮询的缓存 —— 这一步的全部价值就是"确知此刻",拿可能滞后的快照
// 判断会得出反的结论(管线刚停却提示占用,或刚开始却没提示)。
import { G, api } from './store'
import { ElMessageBox } from 'element-plus'

// PIPELINE_BUSY_TEXT 提示正文。单独提出来是为了有闸门钉住口径
// (它必须同时说清"在渲染""会互相拖慢"与"显存/算力在抢"三件事)。
export const PIPELINE_BUSY_TEXT = '管线正在渲染,同时跑会互相拖慢(显存与算力都在抢),确定继续?'

// pipelineRunning 当前是否有管线在跑。
//
// 读状态失败(服务重启中/网络抖动)时退回内存快照,并且**不把它当成"忙"** ——
// 拿一个失败的网络请求去挡用户的点击,比漏一次提示更糟。
export async function pipelineRunning() {
  try {
    const st = await api.status()
    G.status = st // 顺手刷新底栏状态灯,不额外多一次请求
    return Object.keys(st?.running || {}).length > 0
  } catch {
    return Object.keys(G.status?.running || {}).length > 0
  }
}

// confirmGpuContention 提交前确认。返回 true = 继续提交。
//
// title 用调用方的动作名(如「局部重绘 · 镜头 3」),让用户知道**是哪一次操作**
// 要抢显卡,而不是一句无主语的警告。
export async function confirmGpuContention(title = '该操作会占用 GPU') {
  if (!(await pipelineRunning())) return true
  try {
    await ElMessageBox.confirm(PIPELINE_BUSY_TEXT, title, {
      confirmButtonText: '继续', cancelButtonText: '取消', type: 'warning',
    })
    return true
  } catch {
    return false // 取消 = 不提交
  }
}
