// useCharGroups 角色「造型家族」分组 —— **全前端唯一实现**。
//
// ## 为什么抽出来（2026-09-29 代码健康审计 P2-12）
//
// 这段逻辑此前在三个地方各写一遍：
//
//	· `views/ProjectView.vue`      —— 项目页
//	· `components/StageDetailDrawer.vue` —— 阶段详情抽屉（与上一份**逐字相同**）
//	· `views/LibraryView.vue`      —— 资产库（**已经漂移**：solo 键是 `'solo:' + kind + name`
//	  而不是 `'solo:' + id`）
//
// 三处的注释都写着"家族判据由后端下发…前端不自己抄一份规则（抄了必然漂移）"——
// 规则确实没抄（`family` 字段来自后端 `char_families`），但**分组算法**抄了三份，
// 而漂移的后果恰恰是最难查的那类："界面显示成两个人、渲染按一个人派生"。
//
// ## 契约
//
// 入参：`items`（必须带 `id` 与 `name`）+ `charFamilies`（后端下发的
// `{ [cardId]: { family, base, variant, master } }`，可缺省）。
// 出参：`[{ key, base, items: [{...item, variant, master}] }]`，组内**主卡在前**
// （与资产阶段的生成顺序一致：主卡先出图，变体从它派生）。
//
// 后端没下发时（老版本 / 无方案）按"各自成组"退化成原样显示 —— 与三份旧实现一致。
//
// ## 为什么 solo 键用 id 而不是 name
//
// 同名不同人是**合法数据**（两个孩子都叫"老张"）。用 name 拼 solo 键会把两个人并成一组，
// 界面显示成"一个人有两个造型档"，而渲染按两个人各出一张图 —— 又是一处"界面与渲染不一致"。
// `LibraryView` 那份漂移正是这个形态。
export function groupCharFamilies(items, charFamilies) {
  const fams = charFamilies || {}
  const groups = new Map()
  for (const c of items || []) {
    const info = fams[c.id] || {}
    const key = info.family || ('solo:' + c.id)
    let g = groups.get(key)
    if (!g) {
      g = { key, base: info.base || c.name, items: [] }
      groups.set(key, g)
    }
    g.items.push({ ...c, variant: info.variant || '', master: !!info.master })
  }
  for (const g of groups.values()) {
    g.items.sort((a, b) => (b.master ? 1 : 0) - (a.master ? 1 : 0))
  }
  return [...groups.values()]
}

// isMultiVariant 这一组是不是"同一个人有多档造型"（决定界面要不要插分组头）。
export function isMultiVariant(group) {
  return !!group && Array.isArray(group.items) && group.items.length > 1
}
