# 借鉴 chatfire-AI/huobao-drama：逐条对照与落地（2026-10-10）

> 用户：「看一下有没有什么优势点的借鉴一下，进行完善优化」。
> 我**读了它的源码与提示词文件**（不是看 README 转述），逐条对照我们自己的现状；
> 能落地的当轮落地并配门，落不了地或我们**早就有了**的照实说。

## 一、我实际读了什么（可复核）

仓库：`chatfire-AI/huobao-drama`（默认分支 `master`，306 个文件，★15,916，最后更新 2026-10-05）。

| 文件 | 说明 |
|---|---|
| `backend/workspace/skills/prompt-generator/{character,scene,prop,video}-prompt/SKILL.md` | **按资产种类各一套提示词规范**（各 2.6–5.9 KB） |
| `backend/workspace/skills/storyboard-breaker/SKILL.md` | 分镜拆解规范 |
| `backend/src/services/video-prompts.ts` | 视频提示词批量生成 |
| `backend/src/services/final-prompt.ts` | 三类资产的"最终提示词"装配 |
| `backend/src/agents/tools/{storyboard,extract,image-prompt}-tools.ts` | Agent 工具层（增量改字段） |
| `backend/src/services/adapters/{minimax,volcengine,aliyun-wan}-video.ts` 等 | 云模型适配器 |
| `backend/workspace/prompts/*.{md,en.md,ja.md,ko.md}` | 四语言提示词 |

## 二、逐条对照（他们的做法 / 我们的现状 / 结论）

| 他们的优势点 | 我们现状 | 结论 |
|---|---|---|
| **按资产种类分口径**：character-prompt / scene-prompt / prop-prompt 三套 SKILL，各写各的介质与取景 | 只分了「场景 vs 其它」——**道具被算进"其它" = 人像那一套** | **照搬（本轮已做）** |
| **场景图 = 无人物空镜**：明写「不能出现任何的人（含背影、剪影、倒影、照片里的人），即使描述提到人物活动也必须剔除」 | 只有一句 `deserted and unpeopled` | **照搬（本轮已做）** |
| 道具 = 白底单品静物（`single product shot on pure white background`） | 已有（`propPlateClause`，1:1 白底单件 + 不画字 + 四材质混搭） | **已具备，不动** |
| 角色参考图 = **左侧正脸特写 + 右侧三视图**（一张图；"一致性 > 美观"；柔和均匀棚拍光） | 定妆照（胸像）与三视图板是**两张图**，口径散在注释里 | **登记为待办**（改布局会动到出图与参考图挂载，要单独一轮 + 真机 A/B） |
| 视频提示词 = **按 3 秒分段的秒级时间轴** + 每段一个镜头状态 + 段内可切镜 + 不跨场景 + 情绪必须写成可见动作 | 我们的 H3 官方六段式（`<Subject N>` + `[Shot N]` + `<d>` 台词块 + 逐刀时间戳），更贴我们的引擎 | **不照搬**（它这套是给云模型的长文本提示词；我们的格式由 H3 官方口径决定） |
| **`@角色名` → 确定性替换成"图片N"标记**，并在提示词第一行列出「出场人物/场景」 | 已有更强的形态：`PICTURE MANIFEST (authoritative)` —— **渲染器按实际挂载的图写清单，并声明它覆盖上文任何张数/编号**（`refmanifest.go`） | **已具备，不动** |
| **以实际落库为准判成败**（agent 跑完再读一次 DB，字段还是空就算失败） | 同型做法已有多处（`adoptRepaired` 只改那一镜、按落盘产物判交付） | **已具备** |
| Agent 工具层**增量改单字段**（"只传 storyboard_id 和 video_prompt，不要回传其它字段"） | 同型（修复只改那一镜/那一句） | **已具备** |
| 多语言提示词（中/英/日/韩） | 只有中文语境 | **不照搬**（用户群单一，多语言=四份要同步的散文） |
| 云模型适配器（minimax / 火山 / 阿里云） | 我们是**本地 ComfyUI + H3**（零按条计费） | **不照搬**（成本模型不同，见上一轮结论） |

## 三、本轮落地了什么（都带真机证据）

### 1）物件不该拿人像的介质词 —— 照搬"按资产种类分口径"

**真机证据**（盘上 `assets/asset_map.json` 的 `prop:prop_key`，一把铜钥匙，2857 字）：

```
… a single small brass key … in cinematic Chinese guofeng period-drama look …
RENDER STYLE (mandatory): <风格串> — next-generation 3D CG character render,
realistic skin shading with visible skin texture and natural facial highlights …
```

给一把钥匙要求 **"character render / realistic skin shading / facial highlights"** ——
这正是 huobao 把 prop-prompt 单独拆出去要避免的事。

**根因**：`internal/pipeline/prompts.go` 的 `styleExpandFor` 在写实/3D 分支里只分了
「场景 vs 其它」，`KindObject`（器物/道具）落进了人像那一支。

**修法**：加 `else if kind == KindObject` 支，给物件/静物那一套（`prop render` + 材质与自然光），
**场景支、人物支都不动**。

**门**：`internal/pipeline/prop_medium_kind_test.go`
- `TestStyleExpandObjectKindGetsObjectMedium`：用**真机那句风格串**喂 `styleExpandFor` ⇒ 物件必须含
  `prop render`、且**不得**出现 `character render` / `skin shading` / `facial highlights`；
  **同时反向针**：同一条串给 `KindHuman` 时**必须仍然**是人像那一套 —— 否则那不是"分派"，是把词删了。
- `TestSceneFramingExcludesEveryKindOfPerson`：场景取景口径必须含 `unpeopled`/`no silhouette`/`no reflection`/`no crowd`。

**反向验证**（`scratch/reverse_verify_kind_medium.ps1`）：**2/2 全红**
—— 关掉物件支 ⇒ 物件又拿回人像介质词、门红；拿掉 `no silhouette` ⇒ 场景口径门红。

### 2）场景图"任何形态的人都不许出现"

照抄他们的意思（换成我们已有的英文口径）：`no figure, no silhouette, no back-of-head,
no reflection in glass or water, no painted, carved or photographed portrait, no crowd in the far
distance — even when the brief mentions people being or doing something here`。

理由不是形式：场景图里出现"像人但不是本剧任何角色"的东西，**H3 会把它当身份参考**
（我们上一轮真机在成片里吃过同型故障）。

## 四、如实说明（不许含糊）

- **我没跑过 huobao-drama**，所以我不给"它出片比我们好"的背书。我借的是它的**设计分工**
  （每类资产一套口径、场景无人物），并逐条在**我们的真机产物**上验证修掉了什么。
- 它真正的强项其实是**组织形式**：每个环节一份可替换的 `SKILL.md` + 一层模型适配器，
  所以"换个视频模型"只改适配器。我们的引擎是本地 H3、提示词格式由官方口径钉死，
  这一层**搬不动也不该搬**；但"每类资产一套口径"这条分工，我们确实缺了一格（本轮补上）。
- 还剩一条值得单独做：**角色参考图的"左脸特写 + 右三视图同图"布局** —— 它会同时动到
  出图提示词、参考图挂载与 H3 的编号清单，必须配真机 A/B 才敢说"更好"，所以只登记、不当轮改。
