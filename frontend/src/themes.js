// 主题与玻璃拟态个性化:9 套主题色板(1 套内置默认 + 8 套由配色海报推导)+ 卡片玻璃透明度滑块。
// 主题 = 覆盖 --mx-* 语义变量;玻璃 = 面板底色透明度 + backdrop-filter。
// 偏好存 localStorage(纯视觉偏好,每机独立),启动时 App.vue 调 applyPrefs()。
//
// 【当前构成(2026-09-24 用户定调)】
//   - 极简墨黑(mono-noir)= **默认主题**,也是唯一保留下来的旧配色;
//   - 其余 8 套由 D:\Others\配色\ 的 8 张四色渐变海报推导而来,每套 4 个原色
//     (海报色值逐张 OCR + 像素采样双向核对,误差 ≤3/255)。
//
// 【8 套新主题怎么来的(推导规则,便于日后增补)】
//   输入:海报从左到右的 4 个 hex。逐套:
//     1. deep   = 4 色里最暗的那个 → 决定**背景色相**(低饱和 + 4 档极暗亮度 bg0..bg3);
//     2. accent = 从其余 3 色里选一个(先按"饱和度/亮度可用性"归一化);
//        **8 套的强调色是一次全局最优解**:穷举 3^8 组合,最大化 8 个强调色之间的
//        最小成对 ΔE(CIE76),并列时取更鲜艳的一套 —— 因为逐套各自挑"最好看的"时,
//        这些暖色海报有 6 套都会落在金/橘上,主题之间根本分不出来;
//     3. amber-hi / amber-deep = accent 同色相加亮/压暗(UI 里它们是
//        linear-gradient(amber-hi → amber-deep) 的两端,必须同色相,否则按钮会跳色);
//     4. cyan / purple = 剩下两色按与 accent 的色相距离分配(次级/数据色);
//     5. text/text2/text3 = 近白/中灰/暗灰,**强制满足对比度下限**
//        (≥16 / ≥4.5 / ≥3.0 对 bg2);onAccent↔amber 与 amber↔bg2 也各自有下限
//        (否则按钮"糊"在暗底里:实测某套紫被夹到 L=0.52 时只有 3.4);
//     6. green / red / yellow = **语义色固定不随主题变**(成功/失败/警告必须一眼认得出,
//        旧 6 套主题本来也是同一套绿红黄);
//     7. swatch = 海报上的 4 个原色(主题卡上就显示这 4 个)。
//   全套数值由一次性生成器算出后落成活数据(生成器不入库),改配色只需按上述规则重算。

export const THEMES = [
  {
    id: 'mono-noir', name: '极简墨黑', desc: '纯黑高对比 × 银白(默认)',
    swatch: ['#f2f4f8', '#9aa5b5', '#5d6dff'],
    vars: {
      '--mx-bg0': '#000000', '--mx-bg1': '#08080a', '--mx-bg2': '#101013', '--mx-bg3': '#18181c',
      '--mx-line': '#1f1f24', '--mx-line-hi': '#33333c',
      '--mx-amber': '#e8ebf2', '--mx-amber-hi': '#ffffff', '--mx-amber-deep': '#9aa5b5',
      '--mx-cyan': '#5d6dff', '--mx-green': '#3ddc84', '--mx-red': '#ff5f5f',
      '--mx-yellow': '#ffd166', '--mx-purple': '#9aa5b5',
      '--mx-text': '#f5f6fa', '--mx-text2': '#a2a9b8', '--mx-text3': '#5c6270',
      '--mx-glow1': 'rgba(160, 170, 190, 0.46)', '--mx-glow2': 'rgba(93, 109, 255, 0.34)',
      '--mx-glow3': 'rgba(154, 165, 181, 0.26)',
      // 补上:此前缺这一项,主题卡选中框的辉光永远用 :root 的琥珀橙 —— 与黑白主题冲突
      '--mx-glow-amber': '0 0 24px rgba(200, 208, 224, .18)',
    },
    onAccent: '#08080a', // 强调色背景上的文字色(反转显示)
    panelRGB: '16, 16, 19',
  },
  {
    id: 'cream-gold-teal', name: '奶油金青', desc: '奶油粉金 × 青瓷蓝',
    swatch: ['#F1A4C5', '#FCC65C', '#327F7E', '#002A62'],
    vars: {
      '--mx-bg0': '#06080C',
      '--mx-bg1': '#090D13',
      '--mx-bg2': '#0F161F',
      '--mx-bg3': '#16202C',
      '--mx-line': '#1E2939',
      '--mx-line-hi': '#2C3F59',
      '--mx-amber': '#F6C25A',
      '--mx-amber-hi': '#F8E5BF',
      '--mx-amber-deep': '#DB9306',
      '--mx-cyan': '#5EC9C8',
      '--mx-green': '#3DDC84',
      '--mx-red': '#FF6B6B',
      '--mx-yellow': '#FFD166',
      '--mx-purple': '#EF95BB',
      '--mx-text': '#F5F4F2',
      '--mx-text2': '#A0ACBA',
      '--mx-text3': '#5E728D',
      '--mx-glow1': 'rgba(246, 194, 90, 0.46)',
      '--mx-glow2': 'rgba(94, 201, 200, 0.34)',
      '--mx-glow3': 'rgba(239, 149, 187, 0.26)',
      '--mx-glow-amber': '0 0 24px rgba(246, 194, 90, 0.18)',
    },
    onAccent: '#090D13',
    panelRGB: '15, 22, 31',
  },
  {
    id: 'sky-blossom', name: '晴空粉霞', desc: '晴空冰蓝 × 樱粉霞光',
    swatch: ['#A2D9F1', '#FFAED3', '#1C90B8', '#00193E'],
    vars: {
      '--mx-bg0': '#06080C',
      '--mx-bg1': '#090D13',
      '--mx-bg2': '#0F151F',
      '--mx-bg3': '#161F2C',
      '--mx-line': '#1E2939',
      '--mx-line-hi': '#2C3E59',
      '--mx-amber': '#F65AA2',
      '--mx-amber-hi': '#F8BFD9',
      '--mx-amber-deep': '#DB0667',
      '--mx-cyan': '#45BAE3',
      '--mx-green': '#3DDC84',
      '--mx-red': '#FF6B6B',
      '--mx-yellow': '#FFD166',
      '--mx-purple': '#95D4EF',
      '--mx-text': '#F5F2F3',
      '--mx-text2': '#A0ABBA',
      '--mx-text3': '#5E718D',
      '--mx-glow1': 'rgba(246, 90, 162, 0.46)',
      '--mx-glow2': 'rgba(69, 186, 227, 0.34)',
      '--mx-glow3': 'rgba(149, 212, 239, 0.26)',
      '--mx-glow-amber': '0 0 24px rgba(246, 90, 162, 0.18)',
    },
    onAccent: '#090D13',
    panelRGB: '15, 21, 31',
  },
  {
    id: 'iris-neon', name: '鸢尾蓝调', desc: '鸢尾蓝 × 洋红霓虹',
    swatch: ['#B6CDFF', '#FF89BF', '#BF43A8', '#0A0553'],
    vars: {
      '--mx-bg0': '#06060C',
      '--mx-bg1': '#0A0913',
      '--mx-bg2': '#100F1F',
      '--mx-bg3': '#17162C',
      '--mx-line': '#201E39',
      '--mx-line-hi': '#2F2C59',
      '--mx-amber': '#5A8BF6',
      '--mx-amber-hi': '#BFD1F8',
      '--mx-amber-deep': '#0649DB',
      '--mx-cyan': '#F471AD',
      '--mx-green': '#3DDC84',
      '--mx-red': '#FF6B6B',
      '--mx-yellow': '#FFD166',
      '--mx-purple': '#CE6EBC',
      '--mx-text': '#F2F3F5',
      '--mx-text2': '#A2A0BA',
      '--mx-text3': '#615E8D',
      '--mx-glow1': 'rgba(90, 139, 246, 0.46)',
      '--mx-glow2': 'rgba(244, 113, 173, 0.34)',
      '--mx-glow3': 'rgba(206, 110, 188, 0.26)',
      '--mx-glow-amber': '0 0 24px rgba(90, 139, 246, 0.18)',
    },
    onAccent: '#0A0913',
    panelRGB: '16, 15, 31',
  },
  {
    id: 'sunset-mint', name: '薄荷落日', desc: '薄荷青玉 × 落日金',
    swatch: ['#FD7A9B', '#F5BA5B', '#1EA9A2', '#177B58'],
    vars: {
      '--mx-bg0': '#060C0A',
      '--mx-bg1': '#09130F',
      '--mx-bg2': '#0F1F19',
      '--mx-bg3': '#162C25',
      '--mx-line': '#1E392F',
      '--mx-line-hi': '#2C5949',
      '--mx-amber': '#2FDAD1',
      '--mx-amber-hi': '#8DE2DE',
      '--mx-amber-deep': '#148F89',
      '--mx-cyan': '#F47192',
      '--mx-green': '#3DDC84',
      '--mx-red': '#FF6B6B',
      '--mx-yellow': '#FFD166',
      '--mx-purple': '#EEB862',
      '--mx-text': '#F2F5F5',
      '--mx-text2': '#A0BAB1',
      '--mx-text3': '#5E8D7C',
      '--mx-glow1': 'rgba(47, 218, 209, 0.46)',
      '--mx-glow2': 'rgba(244, 113, 146, 0.34)',
      '--mx-glow3': 'rgba(238, 184, 98, 0.26)',
      '--mx-glow-amber': '0 0 24px rgba(47, 218, 209, 0.18)',
    },
    onAccent: '#09130F',
    panelRGB: '15, 31, 25',
  },
  {
    id: 'coral-glacier', name: '珊瑚冰川', desc: '冰川青蓝 × 珊瑚玫红',
    swatch: ['#8CEEF5', '#FF8979', '#E4468B', '#0B2C4B'],
    vars: {
      '--mx-bg0': '#06090C',
      '--mx-bg1': '#090E13',
      '--mx-bg2': '#0F171F',
      '--mx-bg3': '#16222C',
      '--mx-line': '#1E2C39',
      '--mx-line-hi': '#2C4359',
      '--mx-amber': '#F66D5A',
      '--mx-amber-hi': '#F8C6BF',
      '--mx-amber-deep': '#DB1F06',
      '--mx-cyan': '#72EAF3',
      '--mx-green': '#3DDC84',
      '--mx-red': '#FF6B6B',
      '--mx-yellow': '#FFD166',
      '--mx-purple': '#E65695',
      '--mx-text': '#F5F3F2',
      '--mx-text2': '#A0AEBA',
      '--mx-text3': '#5E768D',
      '--mx-glow1': 'rgba(246, 109, 90, 0.46)',
      '--mx-glow2': 'rgba(114, 234, 243, 0.34)',
      '--mx-glow3': 'rgba(230, 86, 149, 0.26)',
      '--mx-glow-amber': '0 0 24px rgba(246, 109, 90, 0.18)',
    },
    onAccent: '#090E13',
    panelRGB: '15, 23, 31',
  },
  {
    id: 'ember-sea', name: '烬海蓝焰', desc: '深海靛蓝 × 焰橘朱红',
    swatch: ['#ADE0FF', '#FFBC79', '#CF331E', '#3E011E'],
    vars: {
      '--mx-bg0': '#0C0609',
      '--mx-bg1': '#13090E',
      '--mx-bg2': '#1F0F17',
      '--mx-bg3': '#2C1621',
      '--mx-line': '#391E2B',
      '--mx-line-hi': '#592C41',
      '--mx-amber': '#5ABBF6',
      '--mx-amber-hi': '#BFE2F8',
      '--mx-amber-deep': '#068ADB',
      '--mx-cyan': '#F4B371',
      '--mx-green': '#3DDC84',
      '--mx-red': '#FF6B6B',
      '--mx-yellow': '#FFD166',
      '--mx-purple': '#E66756',
      '--mx-text': '#F2F4F5',
      '--mx-text2': '#BAA0AD',
      '--mx-text3': '#8D5E74',
      '--mx-glow1': 'rgba(90, 187, 246, 0.46)',
      '--mx-glow2': 'rgba(244, 179, 113, 0.34)',
      '--mx-glow3': 'rgba(230, 103, 86, 0.26)',
      '--mx-glow-amber': '0 0 24px rgba(90, 187, 246, 0.18)',
    },
    onAccent: '#13090E',
    panelRGB: '31, 15, 23',
  },
  {
    id: 'blush-orchid', name: '柔粉兰紫', desc: '柔粉胭脂 × 兰紫夜',
    swatch: ['#FAB4B5', '#7B7DDB', '#7C43A7', '#330146'],
    vars: {
      '--mx-bg0': '#0A060C',
      '--mx-bg1': '#100913',
      '--mx-bg2': '#1A0F1F',
      '--mx-bg3': '#26162C',
      '--mx-line': '#311E39',
      '--mx-line-hi': '#4C2C59',
      '--mx-amber': '#A56ECF',
      '--mx-amber-hi': '#D4BFE3',
      '--mx-amber-deep': '#6E2D9F',
      '--mx-cyan': '#F47173',
      '--mx-green': '#3DDC84',
      '--mx-red': '#FF6B6B',
      '--mx-yellow': '#FFD166',
      '--mx-purple': '#7B7DDB',
      '--mx-text': '#F4F2F5',
      '--mx-text2': '#B3A0BA',
      '--mx-text3': '#805E8D',
      '--mx-glow1': 'rgba(165, 110, 207, 0.46)',
      '--mx-glow2': 'rgba(244, 113, 115, 0.34)',
      '--mx-glow3': 'rgba(123, 125, 219, 0.26)',
      '--mx-glow-amber': '0 0 24px rgba(165, 110, 207, 0.18)',
    },
    onAccent: '#100913',
    panelRGB: '26, 15, 31',
  },
  {
    id: 'peach-lake', name: '蜜桃湖蓝', desc: '蜜桃暖橙 × 湖蓝碧水',
    swatch: ['#FFCBA6', '#53BEAC', '#336A98', '#0B1E2C'],
    vars: {
      '--mx-bg0': '#06090C',
      '--mx-bg1': '#0A0F12',
      '--mx-bg2': '#10181E',
      '--mx-bg3': '#17232B',
      '--mx-line': '#202D37',
      '--mx-line-hi': '#2E4556',
      '--mx-amber': '#F69B5A',
      '--mx-amber-hi': '#F8D6BF',
      '--mx-amber-deep': '#DB5E06',
      '--mx-cyan': '#5E99C9',
      '--mx-green': '#3DDC84',
      '--mx-red': '#FF6B6B',
      '--mx-yellow': '#FFD166',
      '--mx-purple': '#72CABB',
      '--mx-text': '#F5F3F2',
      '--mx-text2': '#A0AFBA',
      '--mx-text3': '#5E798D',
      '--mx-glow1': 'rgba(246, 155, 90, 0.46)',
      '--mx-glow2': 'rgba(94, 153, 201, 0.34)',
      '--mx-glow3': 'rgba(114, 202, 187, 0.26)',
      '--mx-glow-amber': '0 0 24px rgba(246, 155, 90, 0.18)',
    },
    onAccent: '#0A0F12',
    panelRGB: '16, 24, 30',
  },
]

// DEFAULT_THEME_ID 默认主题:极简墨黑。
// 单一真源 —— themes.js 的兜底、SettingsView 的初值与"偏好里存着一个已删除主题"的迁移
// 都从这里取,免得散落的字面量('midnight-amber')在主题被删后变成"打不开的主题"。
export const DEFAULT_THEME_ID = 'mono-noir'

export function themeById(id) {
  return THEMES.find(t => t.id === id) || THEMES.find(t => t.id === DEFAULT_THEME_ID) || THEMES[0]
}

// ---------- 偏好读写与应用 ----------
const KEY = 'mx-appearance'

export function loadPrefs() {
  try { return JSON.parse(localStorage.getItem(KEY)) || {} } catch { return {} }
}

export function savePrefs(p) {
  localStorage.setItem(KEY, JSON.stringify(p))
}

// applyTheme 应用主题与玻璃设置并落盘。
// themeId: 主题 id;glass: 玻璃开关;glassAlpha: 面板不透明度 0.22-0.9(滑块)。
export function applyTheme({ themeId, glass, glassAlpha } = {}) {
  const prefs = { ...loadPrefs() }
  if (themeId !== undefined) prefs.theme = themeId
  if (glass !== undefined) prefs.glass = glass
  if (glassAlpha !== undefined) prefs.glassAlpha = glassAlpha
  // 存量偏好可能指向已删除的主题:落回默认并把偏好修正掉(否则每次启动都要兜一次)
  const t = themeById(prefs.theme)
  if (prefs.theme !== t.id) prefs.theme = t.id
  savePrefs(prefs)

  const root = document.documentElement
  for (const [k, v] of Object.entries(t.vars)) root.style.setProperty(k, v)
  // 强调色上的文字反转色(主按钮/激活 chip 等,随主题适配)
  root.style.setProperty('--mx-on-accent', t.onAccent)
  // --mx-accent:Element Plus 那一整套派生色(theme.css)都挂在这个名字上,
  // 主题里只维护 --mx-amber,这里转发一次,免得两个名字各写一份还可能不一致
  root.style.setProperty('--mx-accent', t.vars['--mx-amber'])
  root.setAttribute('data-theme', t.id)

  const on = !!prefs.glass
  // 玻璃开关也反映到 DOM 上:边缘高光/投影这类"玻璃配件"只在开启时叠加
  // (只靠 CSS 变量做不到——面板底色是变量,材质不是)
  root.setAttribute('data-glass', on ? 'on' : 'off')
  const a = Math.min(0.88, Math.max(0.18, Number(prefs.glassAlpha) || 0.45))
  // 白纱(顶亮底暗):玻璃的"材质本体"直接焊进面板变量里,而不是靠一堆
  // `[data-glass=on] .xxx` 选择器逐个点名 —— 变量在哪儿用,玻璃就在哪儿生效,
  // 新增面板/卡片不必回头改 CSS(旧写法漏了书卡/日志条这些,质感就不统一)。
  const veil = 'linear-gradient(180deg, rgba(255,255,255,.13), rgba(255,255,255,.045) 52%, rgba(255,255,255,.02))'
  if (on) {
    root.style.setProperty('--mx-panel-bg', `${veil}, rgba(${t.panelRGB}, ${a})`)
    root.style.setProperty('--mx-panel-bg2', `${veil}, rgba(${t.panelRGB}, ${Math.min(1, a + 0.14)})`)
    // 模糊要够重才"像玻璃";brightness 把背后的氛围色提亮一点,玻璃才"透"
    root.style.setProperty('--mx-bd-filter', 'blur(20px) saturate(1.5) brightness(1.15)')
    root.style.setProperty('--mx-bd-border', 'rgba(255, 255, 255, .13)')
  } else {
    root.style.setProperty('--mx-panel-bg', `rgb(${t.panelRGB})`)
    root.style.setProperty('--mx-panel-bg2', `rgb(${t.panelRGB})`)
    root.style.setProperty('--mx-bd-filter', 'none')
    root.style.setProperty('--mx-bd-border', 'var(--mx-line)')
  }
  return { theme: t, prefs }
}
