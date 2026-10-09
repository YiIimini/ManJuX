# 视觉风格卡 · 鬼刀式东方星海（本书专用）

> **适用优先级**：用户 2026-10-08 点名「**鬼刀风格**」→ 按技能口径「用户在某次创作里点名别的风格时，以当次要求为准」，
> **本书的画面口径以本卡为准**；技能自带的 `视觉风格卡-3D写实动漫脸.md` 仍留在 `设定集/` 作快照与兜底。
> **不点名作者**：本卡只按**特征**描述（绘画感、雾雪、巨剑、华丽首饰、月白冷光＋一处灯金），
> 提示词里**不出现任何在世创作者姓名**（云端生图平台会把真人/人名当敏感项拦）。

## 一、一句话风格串（`立项.json` 的 `渲染风格串` / `art_style` 直接用）

```
PAINTERLY CINEMATIC EASTERN COSMIC FANTASY: a semi-realistic hand-painted digital illustration
finish with visible brush texture and fine film grain; luminous frost-blue, moon-white and
pale-gold palette; misty volumetric light, dramatic rim light, drifting frost motes; ornate
filigree jewellery, flowing silk, drifting hair; aged bronze, pale jade and cold-forged steel
with visible craft; snow plains under a deep night sky, towers of bronze and jade. Cool shadows
with one warm lantern-gold accent per frame. This is an eastern cosmic fantasy, NOT neon
cyberpunk, NOT western medieval fantasy, NOT flat anime cel-shading, NOT a plastic 3D render.
```

**为什么串长这样**：`cfg=1.0` 时**负面提示词权重为零**（技能默认后端），所以禁区必须写成
**正向排除句**（"是 X，**不是** Y"）——上面最后一句四个 NOT 就是护栏本身，不是装饰。

## 二、鬼刀风格的四条可执行特征（写作与出图都照这四条落）

| 特征 | 落到文字上（写手照写） | 落到画面上（出图照写） |
|---|---|---|
| **绘画感** | 写光不写滤镜：光从哪来、在哪停、哪一处暗下去 | hand-painted finish, visible brush texture, film grain（**不是**平涂赛璐璐） |
| **雾与雪** | 每一场戏都要有空气里的东西：霜粒、白气、雾墙、灯下的尘 | misty volumetric light, drifting frost motes, snow haze |
| **华丽与旧** | 首饰、纹样、玉件、青铜包浆——新东西也要有磨损 | ornate filigree jewellery, aged bronze, pale jade, worn leather |
| **冷里一点金** | 每章只给**一处暖**（灯炉、热汤、团子身上的霜纹亮一下） | cool shadows with ONE warm lantern-gold accent per frame |

## 三、本书禁区（出现即打回）

- **霓虹／赛博／义体／全息界面／数据光带**——未来的造物一律写青铜、老玉、霜铁、绢、灯。
- **西式中世纪**（城堡、铠甲、骑士纹章）。
- **平涂动漫脸**（赛璐璐、粗勾线、大眼小鼻）；本书走**半写实绘画感**。
- **塑料 3D 感**（油亮高光、假材质）。
- **现代物件**（T恤、牛仔裤、眼镜、手表、拉链、塑料瓶、印刷招牌）。

## 四、人物脸的写法（与技能 `章节写作规范` §4.1 并用）

- **正角**：写**绘画感的美型**——眉骨有阴影、眼尾有光、皮肤有毛孔级的暗部，**不写"大眼睛""水汪汪"**。
  例：「一张半写实的脸，眼尾被灯光扫出一道亮边，眉骨下面压着阴影」。
- **反角**：一律给**正向形状**（骨相／皮相／神态三路），**禁用一切主角脸词**；反角不等于丑。
- **萌兽（主角阿糖）**：写**毛、耳、尾、霜纹**四件可画物：白绒的走向、耳尖抖一下、尾巴像一片霜叶翻过来、
  眉心霜纹由白转淡蓝＝她变冷（本书的"血量条"）。
- **Q 版**：只给正角（阿糖／姜晚舟／霍青崖／白芨／小灯）；**反派不给 Q 版**，内心走画外音。

## 五、封面口径（阶段6 照此出图）

- 竖版 2:3（768×1152）；**招牌道具＝古灯炉**必须一眼可辨（一人高的青铜玉炉，炉心一朵霜白灯焰，
  炉沿有一圈被熏黑的旧铜边）。
- **画面里不放任何文字**（书名/作者名一律后期排版；此前有书栽在"纸上有字→乱码"）。
- 情绪＝冷里一点金：霜蓝的星海雪原，中心一团灯金，团子趴在炉沿上，一张巴掌大的脸回看镜头。
