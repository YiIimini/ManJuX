// qcLines.js 质检报告里三类结论的**界面摘要**（语音对账 / 音色核对 / 画面文字）。
//
// 【为什么要有这个文件】（2026-10-08 用户：「好好再深度检查排查问题，别总是我在使用的时候让我发现啊」）
//
// 后端早就在算这些结论（`qc_report.json` 的 `speech` / `voice` / `text_detect`），也随
// `GET /api/projects/{id}` 原样下发到浏览器，**但两个质检渲染点只画了 ok/时长/暗像素/有音轨/problems/vlm**
// —— 于是"音色配错了（女的配男音）""同一句念了两遍""画面里被烧了字"在界面上**一个都看不到**，
// 只有当时那次会话的日志流里有（而运行日志还会被一键清理清掉）。
//
// 三态必须分开显示（与后端 `Checked/Skipped/Error` 同一口径）：
//   - 跳过/未判 ⇒ 写清"没验/不判 + 为什么"（**不是**"验过且干净"）
//   - 失败      ⇒ "未判定，不当作通过"
//   - 判负      ⇒ 把 findings 的 detail 逐条摆出来（那是给人看的动作指引）

function detailText(list) {
  return (list || [])
    .map((f) => (f && (f.detail || f.kind)) || '')
    .filter(Boolean)
    .join('；')
}

function genderZH(g) {
  if (g === 'female') return '女声'
  if (g === 'male') return '男声'
  return ''
}

// speechLine 语音核对（ASR：念的是不是剧本里那几句）。
export function speechLine(r) {
  const s = r && r.speech
  if (!s) return ''
  if (s.error) return '语音核对失败（未判定，不当作通过）：' + s.error
  if (s.skipped) return '语音未验：' + s.skipped
  if (s.checked === false) return '语音未验'
  const n = (s.findings || []).length
  if (n > 0) return '语音 ✗' + n + ' 处：' + detailText(s.findings)
  return '语音 ✔（ASR 转出 ' + (s.segments || 0) + ' 段，与剧本对得上）'
}

// voiceLine 音色核对（卡上是男声还是女声 × 音轨实测基频）。
export function voiceLine(r) {
  const v = r && r.voice
  if (!v) return ''
  if (v.error) return '音色核对失败（未判定，不当作通过）：' + v.error
  if (v.skipped) return '音色未判：' + v.skipped
  if (v.checked === false) return '音色未判'
  const hz = v.median_hz ? Math.round(v.median_hz) + ' Hz' : '—'
  const n = (v.findings || []).length
  if (n > 0) return '音色 ✗' + n + ' 处（实测 ' + hz + '）：' + detailText(v.findings)
  return '音色 ✔（卡上' + (genderZH(v.expected) || '未标性别') + '，实测中位基频 ' + hz + '）'
}

// textLine 画面文字检测（模型往画面里烧字/招牌/UI 的防线）。
export function textLine(r) {
  const t = r && r.text_detect
  if (!t) return ''
  if (t.error) return '画面文字检测失败（未判定，不当作通过）：' + t.error
  if (t.skipped) return '画面文字未测：' + t.skipped
  if (t.checked === false) return '画面文字未测'
  if (t.has_text) {
    const frames = (t.hit_frames || []).join('/')
    return '画面文字 ✗（命中帧 ' + frames + (t.max_confidence ? '，置信 ' + t.max_confidence : '') + '）' + (t.note ? '：' + t.note : '')
  }
  return '画面文字 ✔（采样 ' + (t.sampled_frames || 0) + ' 帧未见烧字）'
}

// qcProblemLines 一镜的**全部**可见结论（按"该谁动手"排序：判负在前，未验/未判在后）。
export function qcProblemLines(r) {
  const out = []
  if (!r) return out
  const v = voiceLine(r)
  if (v && v.indexOf('✗') >= 0) out.push(v)
  const s = speechLine(r)
  if (s && s.indexOf('✗') >= 0) out.push(s)
  const t = textLine(r)
  if (t && t.indexOf('✗') >= 0) out.push(t)
  if (v && v.indexOf('✗') < 0) out.push(v)
  if (s && s.indexOf('✗') < 0) out.push(s)
  if (t && t.indexOf('✗') < 0) out.push(t)
  return out
}
