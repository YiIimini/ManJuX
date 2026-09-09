// 主题与玻璃拟态个性化:6 套主题色板 + 卡片玻璃透明度滑块。
// 主题 = 覆盖 --mx-* 语义变量;玻璃 = 面板底色透明度 + backdrop-filter。
// 偏好存 localStorage(纯视觉偏好,每机独立),启动时 App.vue 调 applyPrefs()。

export const THEMES = [
  {
    id: 'midnight-amber', name: '午夜琥珀', desc: '影院深蓝黑 × 琥珀橙',
    swatch: ['#ffd08a', '#f97a1f', '#53d8ff'],
    vars: {
      '--mx-bg0': '#06080e', '--mx-bg1': '#0a0d16', '--mx-bg2': '#10141f', '--mx-bg3': '#161b2b',
      '--mx-line': '#1e2537', '--mx-line-hi': '#2d3752',
      '--mx-amber': '#ffb454', '--mx-amber-hi': '#ffd08a', '--mx-amber-deep': '#f97a1f',
      '--mx-cyan': '#53d8ff', '--mx-green': '#3ddc84', '--mx-red': '#ff6b6b',
      '--mx-yellow': '#ffd166', '--mx-purple': '#b28bff',
      '--mx-text': '#edf1fa', '--mx-text2': '#93a0bd', '--mx-text3': '#5a6580',
      '--mx-glow1': 'rgba(255, 140, 60, .07)', '--mx-glow2': 'rgba(83, 216, 255, .06)',
    },
    onAccent: '#0a0d16', // 强调色背景上的文字色(反转显示)
    panelRGB: '16, 20, 33',
  },
  {
    id: 'aurora-cyan', name: '星河青', desc: '极夜蓝黑 × 冰青辉光',
    swatch: ['#8ceaff', '#23a8d5', '#b28bff'],
    vars: {
      '--mx-bg0': '#04070c', '--mx-bg1': '#071018', '--mx-bg2': '#0c1620', '--mx-bg3': '#12222e',
      '--mx-line': '#172734', '--mx-line-hi': '#274556',
      '--mx-amber': '#4cc9f0', '--mx-amber-hi': '#8ceaff', '--mx-amber-deep': '#1789b8',
      '--mx-cyan': '#b28bff', '--mx-green': '#3ddc84', '--mx-red': '#ff6b6b',
      '--mx-yellow': '#ffd166', '--mx-purple': '#7bf1ff',
      '--mx-text': '#eef7fc', '--mx-text2': '#8fadbf', '--mx-text3': '#54707f',
      '--mx-glow1': 'rgba(76, 201, 240, .08)', '--mx-glow2': 'rgba(178, 139, 255, .06)',
    },
    onAccent: '#04141c', // 强调色背景上的文字色(反转显示)
    panelRGB: '12, 22, 32',
  },
  {
    id: 'sunset-neon', name: '落日霓虹', desc: '暮紫黑 × 霓虹粉紫',
    swatch: ['#ff9ecf', '#c86bfa', '#7bf1ff'],
    vars: {
      '--mx-bg0': '#080510', '--mx-bg1': '#0e0a1a', '--mx-bg2': '#151024', '--mx-bg3': '#1d1732',
      '--mx-line': '#251c3d', '--mx-line-hi': '#3b2d5e',
      '--mx-amber': '#e77fff', '--mx-amber-hi': '#ffb3ec', '--mx-amber-deep': '#a855f7',
      '--mx-cyan': '#7bf1ff', '--mx-green': '#3ddc84', '--mx-red': '#ff5f8f',
      '--mx-yellow': '#ffd166', '--mx-purple': '#ff9ecf',
      '--mx-text': '#f6eefc', '--mx-text2': '#a896c4', '--mx-text3': '#665680',
      '--mx-glow1': 'rgba(200, 107, 250, .08)', '--mx-glow2': 'rgba(255, 158, 207, .06)',
    },
    onAccent: '#160a20', // 强调色背景上的文字色(反转显示)
    panelRGB: '21, 16, 36',
  },
  {
    id: 'emerald-forest', name: '森林翡翠', desc: '苔原墨绿 × 翡翠辉光',
    swatch: ['#8affc1', '#12b886', '#ffd166'],
    vars: {
      '--mx-bg0': '#040a07', '--mx-bg1': '#07120d', '--mx-bg2': '#0c1a13', '--mx-bg3': '#12241a',
      '--mx-line': '#16281e', '--mx-line-hi': '#2a4736',
      '--mx-amber': '#2fd98f', '--mx-amber-hi': '#8affc1', '--mx-amber-deep': '#0e9f6e',
      '--mx-cyan': '#ffd166', '--mx-green': '#3ddc84', '--mx-red': '#ff6b6b',
      '--mx-yellow': '#ffe08a', '--mx-purple': '#7bf1ff',
      '--mx-text': '#eefaf3', '--mx-text2': '#93b5a4', '--mx-text3': '#54705f',
      '--mx-glow1': 'rgba(47, 217, 143, .07)', '--mx-glow2': 'rgba(255, 209, 102, .05)',
    },
    onAccent: '#04120c', // 强调色背景上的文字色(反转显示)
    panelRGB: '12, 26, 19',
  },
  {
    id: 'sakura-cream', name: '樱花拿铁', desc: '暖褐黑 × 奶油樱粉',
    swatch: ['#ffc7d9', '#f78fb3', '#8ce0c0'],
    vars: {
      '--mx-bg0': '#0c0709', '--mx-bg1': '#140d10', '--mx-bg2': '#1c1318', '--mx-bg3': '#261a20',
      '--mx-line': '#2b1e25', '--mx-line-hi': '#453040',
      '--mx-amber': '#f78fb3', '--mx-amber-hi': '#ffc7d9', '--mx-amber-deep': '#d9648f',
      '--mx-cyan': '#8ce0c0', '--mx-green': '#3ddc84', '--mx-red': '#ff6b6b',
      '--mx-yellow': '#ffe08a', '--mx-purple': '#c9a7ff',
      '--mx-text': '#fbf0f4', '--mx-text2': '#c0a3b1', '--mx-text3': '#7d6470',
      '--mx-glow1': 'rgba(247, 143, 179, .08)', '--mx-glow2': 'rgba(140, 224, 192, .05)',
    },
    onAccent: '#140d10', // 强调色背景上的文字色(反转显示)
    panelRGB: '28, 19, 24',
  },
  {
    id: 'mono-noir', name: '极简墨黑', desc: '纯黑高对比 × 银白',
    swatch: ['#f2f4f8', '#9aa5b5', '#5d6dff'],
    vars: {
      '--mx-bg0': '#000000', '--mx-bg1': '#08080a', '--mx-bg2': '#101013', '--mx-bg3': '#18181c',
      '--mx-line': '#1f1f24', '--mx-line-hi': '#33333c',
      '--mx-amber': '#e8ebf2', '--mx-amber-hi': '#ffffff', '--mx-amber-deep': '#9aa5b5',
      '--mx-cyan': '#5d6dff', '--mx-green': '#3ddc84', '--mx-red': '#ff5f5f',
      '--mx-yellow': '#ffd166', '--mx-purple': '#9aa5b5',
      '--mx-text': '#f5f6fa', '--mx-text2': '#a2a9b8', '--mx-text3': '#5c6270',
      '--mx-glow1': 'rgba(160, 170, 190, .06)', '--mx-glow2': 'rgba(93, 109, 255, .05)',
    },
    onAccent: '#08080a', // 强调色背景上的文字色(反转显示)
    panelRGB: '16, 16, 19',
  },
]

// ---------- 偏好读写与应用 ----------
const KEY = 'mx-appearance'

export function loadPrefs() {
  try { return JSON.parse(localStorage.getItem(KEY)) || {} } catch { return {} }
}

export function savePrefs(p) {
  localStorage.setItem(KEY, JSON.stringify(p))
}

// applyTheme 应用主题与玻璃设置并落盘。
// themeId: 主题 id;glass: 玻璃开关;glassAlpha: 面板不透明度 0.30-1(滑块)。
export function applyTheme({ themeId, glass, glassAlpha } = {}) {
  const prefs = { ...loadPrefs() }
  if (themeId !== undefined) prefs.theme = themeId
  if (glass !== undefined) prefs.glass = glass
  if (glassAlpha !== undefined) prefs.glassAlpha = glassAlpha
  savePrefs(prefs)

  const t = THEMES.find(x => x.id === prefs.theme) || THEMES[0]
  const root = document.documentElement
  for (const [k, v] of Object.entries(t.vars)) root.style.setProperty(k, v)
  // 强调色上的文字反转色(主按钮/激活 chip 等,随主题适配)
  root.style.setProperty('--mx-on-accent', t.onAccent)
  root.setAttribute('data-theme', t.id)

  const on = !!prefs.glass
  const a = Math.min(1, Math.max(0.3, Number(prefs.glassAlpha) || 0.72))
  if (on) {
    root.style.setProperty('--mx-panel-bg', `rgba(${t.panelRGB}, ${a})`)
    root.style.setProperty('--mx-panel-bg2', `rgba(${t.panelRGB}, ${Math.min(1, a + 0.12)})`)
    root.style.setProperty('--mx-bd-filter', 'blur(18px) saturate(1.5)')
    root.style.setProperty('--mx-bd-border', 'rgba(255, 255, 255, .08)')
  } else {
    root.style.setProperty('--mx-panel-bg', `rgb(${t.panelRGB})`)
    root.style.setProperty('--mx-panel-bg2', `rgb(${t.panelRGB})`)
    root.style.setProperty('--mx-bd-filter', 'none')
    root.style.setProperty('--mx-bd-border', 'var(--mx-line)')
  }
  return { theme: t, prefs }
}