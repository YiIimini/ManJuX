# ComfyUI 侧交付 · 本体升 v0.39.1 与模型补装（2026-10-08）

> **谁写给谁的**：ComfyUI 运维与节点维护方 → **平台侧（ManJuX）**。
> **对应留档（全部证据在这）**：`D:\Ai\ComfyUI\_maintenance\20261007-upgrade\升级记录-2026-10-08.md`
> ＋ 同目录 `evidence\`（可重跑脚本）、`state-before/after.txt`、`accept-after.txt`、`node-diff-before-after.txt`、
> `acc-{before,after,w6a8}.report.json`、`md5-before/after.txt`。
> **起因**：平台自己的版本检查（`data\logs\manjux.log` 2026-10-07 23:37）报「本体在线 v0.39.1@af89add，**落后 8 项**」。
> **本文只讲**：平台侧**要不要动、动哪里、怎么验**。逐条分「无需动作 / 可选 / 必做（小）」。

---

## 0. 一句话结论

**ComfyUI 侧已升到 `v0.39.1` 并把 18 个插件仓对齐上游，平台侧无需为"升级"改任何代码** ——
生产配方的真跑已复验：**同参同 seed，291.1 s → 256.5 s（−11.9%），规格/音轨不变**。
真要说平台侧该做什么，只有两类：**① 登记（必做、几分钟）**；**② 三件"可选"的增量能力**（要拍板才动）。

| # | 事项 | 平台侧要不要动 | 性质 |
|---|---|---|---|
| 1 | 登记本轮升到 `COMPATIBILITY.md` / `CHANGELOG.md` | **要**（§7 给了可粘贴文本） | 必做（小） |
| 2 | 画质基线标记 `q1` 是否升 `q2`（决定存量镜头要不要重出） | **已决定：不升**（见 §3） | 可选-0 |
| 3 | `w6a8` 备选档接线 | **已决定：不接**（实测无速度/显存优势） | 可选-1 |
| 4 | Fun ControlNet + 10 特效 embedding 接线 | **已决定：不接**（新能力、无已证优势） | 可选-2 |
| 5 | Acc-8Step 两种上游转换版取舍 | **已决定：不动**（当前零影响） | 可选-3 |
> 四项的**决定与理由**见 §3–§6 的开头「**结论（2026-10-10）**」段；都是"按证据判无优势 ⇒ 不做"，不是搁置。
> 另有 1 条自查：第三方轻量 VAE（LynnReal / LightVAE 一类）虽然新本体支持，但**非官方源 + 画质未验证** ⇒ 同样不接（§6 末尾）。
| — | TaoMate 3 步档 / 8 步族选路 / LoRA 与步数成对 | **已在平台落地，不用动** | 见 §2 |

---

## 1. 本轮 ComfyUI 侧做了什么（事实与证据）

| 项 | 升级前 → 升级后 | 证据 |
|---|---|---|
| 本体 | `v0.37.4 (8ff6dc38)` → **`v0.39.1 (de0125d9)`**（`HEAD == tag` 实测 True） | `accept-after.txt` §(3) |
| pip 依赖 | frontend `1.52.7→1.53.10`、templates `0.11.69→0.11.77`、docs `0.5.12→0.5.13`、kitchen `0.2.35→0.2.37` | `state-after.txt` §3 |
| 运行时 | **torch / torchvision / torchaudio / transformers / numpy / tokenizers / huggingface_hub / safetensors / Pillow / av / sageattention 逐项零漂移** | `state-before.txt` ↔ `state-after.txt` §3 diff |
| 插件 | 7 仓升到上游 HEAD（audio-T8 `6063fafb→1048066c`、rgthree `449c58fc→d7297b42`、was-node-suite `8f80db30→d1f76065`、Director `4b4fe6e1→a8f57b8e`、LTXVideo `61ee82b2→3bf3ca62`、MiniMaxH3-Easy `61ceb3cd→23ba347c`、vrgamedevgirl `8c49237b→3432b03f`）；**18 仓落后 0**。**2026-10-10 复核后续更**：这四家又推了新提交，已再升到上游 HEAD —— Director `a8f57b8e→b168de86`、audio-T8 `1048066c→b6be5652`、Spectrum `5161f045→806fe498`、was-node-suite `d1f76065→c78d0e3b`（其余 14 仓仍未动、仍最新） | `accept-after.txt` §(3) + `_maintenance\20261007-upgrade\升级记录-2026-10-08.md` §11 |
| 启动自检 | `IMPORT FAILED` **0**、traceback **0**、节点总数 `2503 → 2849` | `boot-scan-0.39.1.txt` |
| 平台在用节点类 | 阻断级 **10/10**、在用类 **54/54**；schema **零破坏性变更** | `node-diff-before-after.txt` |
| `data\settings.json` | **一个字节没动**（sha256 `5640B7C9…299` 前后相同） | `state-before/after.txt` §5 |
| **生产配方真跑**（768×1344 / 124 帧 / 8 步 / 8 步族 LoRA / 3 参考图 / seed 20260924） | **291.1 s → 256.5 s（−11.9%）**；峰值 19558 → 19552 MiB；两条都 `768x1344 / 24fps / 124 帧 / h264 + aac@32000 2ch / 5.167 s`，`ffprobe -v error` 无输出 | `acc-before/after.report.json` |
| 逐帧指纹 | 视频流 **124/124 帧全不同**；音频流 **162/162 帧逐字节相同** | `md5-before.txt` / `md5-after.txt` |
| **平台自己的生产路径也端到端跑通**（加做项，`cmd\renderone` 走 Coordinator → `comfyengine` → 平台自管的 ComfyUI(8190)） | `结果 completed  用时 8m0s`；自动补齐资产后以 **ref2va + 2 张参考图**出片；产物 `768x1344 / 24fps / 192 帧 / 8.000 s / h264 + aac@48000 2ch` | `_maintenance\20261007-upgrade\platform-path\renderone.log` + `shot_005.mp4` + `shot_005.ffprobe.json` |

**2026-10-10 复核（第二轮，起因：你这两天在平台里正常使用过）**：
① 本体 `v0.39.1` 仍是**最新 release tag**（`github/master` 仍比 tag 多 21 个提交，未验证 ⇒ 不动）；
② 4 个插件仓续更到上游 HEAD（见上表插件行），启动自检 **`IMPORT FAILED` 0、traceback 0、节点数 2849 → 2880**，
   阻断级 10/10、在用类 54/54 仍全在；schema 复核**无删除、无类型收窄**（新增的都是 optional 输入；
   仅 `was-node-suite` 的 `WASH3DecodeVideo` 新增了一个 **required** `frame_rate`，属硬破坏，
   **平台对这 4 个类 0 处引用** ⇒ 不影响 ManJuX）；
③ 再用生产配方真跑一条：`status=success`、**261.6 s**、峰值 19546 MiB、`768x1344 / 24fps / 124 帧 / h264 + aac@32000`；
④ **11 个在用/备选权重逐个与上游比对 = 10 SAME / 0 DIFF**（哈希级：本地 sha256 ↔ ModelScope 仓内 `Sha256`；TaoMate 不在 ModelScope，另有 HF LFS oid 记录）⇒ **模型侧没有该更新的**；
⑤ 网络提醒：本机对 HF 的线路**反复切换**（`huggingface.co` 的 DNS 会在真/假 IP 之间跳 —— 19:1x 解析出假 IP 不通、19:5x 又通了；`hf-mirror` 时通时断且慢）；
   已改成 **ModelScope 优先**（`www.modelscope.cn`，0.2–0.4 s 出清单、下载实测 **37.5 MB/s**），通道与用法固化在
   `D:\Ai\ComfyUI\_maintenance\hf-fetch\`（`ms_get.py` / `hf_routes.py` / `ms_verify.py` + README），并把用户级 `HF_ENDPOINT` 设为 hf-mirror 兜底。
⑥ 提醒：`ComfyUI\input` 会被平台资产链按同名刷新 —— 10-08 验收用的那三张 qiqi 参考图**已被你 10-10 的资产替换掉**，
   所以 10-10 这次验证换了参考图（只证明"仍能正常出片"，不是与 10-08 的同参对比）。

**为什么值得升**：v0.39.1 含 5 条 H3 直接相关修复 —— `2d6b7328`（提前释放 embedding 临时张量，降峰值）、
`912fca4f`（修 VAE `rms_rope` 在 offloaded `qk_norm_scale` 上崩溃）、`83071e1a`（VAE offload）、
`fc584aaa`（VAE 分块与相邻块混合）、`986c4d15`（支持 LynnReal 轻量 H3 VAE）。

**参数口径（照抄用）**：启动参数 = `comfyLaunchArgs`（`--disable-smart-memory --cache-none --vram-headroom 4 --use-sage-attention`）；
工作流按 `engine.go:1101 buildRenderGraph()` 逐节点对齐；复现脚本 `_maintenance\20261007-upgrade\evidence\run_acc.py`。

---

## 2. 先排掉四项：**平台侧已经落地，不要重复施工**

| 项 | 平台现状（落点） | 结论 |
|---|---|---|
| TaoMate 3 步档 | `engine.go:861` `expressTaoMateLoRA` + `engine.go:871 expressRecipe()`；设置页文案 `frontend\src\components\ParamsDrawer.vue:264`「极速预览/抽卡（TaoMate 3 步，约 2×；参考图会压过提示词，不要用于交付）」 | **2026-09-27 交付单那条已兑现**，本轮 ComfyUI 侧无新增需求 |
| 8 步族 LoRA 选路 | `engine.go:902 turbo8StepFile()` + `engine.go:925 turboRecipeFor()`（请求 ≥8 步且盘上有 8 步族就挂 8 步族；缺文件退回并播报） | 已落地；本机 8 步族文件在盘（`minimax_h3_ref2v_turbo_8step_v1.0_768p_comfyui_bf16`），实测真跑用的就是它 |
| 「LoRA 与步数成对」各档一致 | `engine.go:1010 accelLoraFor()` / `engine.go:1038 accelSteps()` / `engine.go:871 expressRecipe()` 三个决定点收口 | 已落地 |
| quality 档在量化底模上回退 turbo | `engine.go:962 accelQualityPlan()` + `engine.go:986 AccelTierNote()`（回退**会播报**） | 仍成立、且已可见；本轮**没有**为了它下 40 GB 级非剪枝底座 |

> 也就是说：**"最优方案"的档位接线这一层，平台侧没有欠账**。本轮升级只是把底层换到更新的本体。

---

## 3. 可选-0（最值钱的一条）：画质基线标记 `q1` 要不要升 `q2`

**结论（2026-10-10，按证据判）：不升 `q2`。** 理由是成本/收益实测反过来了 —— 我按平台自己的清单统计了现存产物：
`data\projects` 下 8 个工程里**只有 1 集（《离婚后，千亿前夫跪求复合·第01集》）渲过镜头，共 4 个**，
而且这 4 个片段的 `LastWriteTime` 是 **2026-10-10 17:43–18:21**（= 在 10-08 升到 v0.39.1 **之后**）⇒
**它们本来就已经是新本体出的**，改 `q2` 只会把这 4 个已经在最新本体上的产物白重渲一遍，收益为 0。
另外平台自己已经有一条更合适的机制：`legacyModel`（清单里没记渲染模型的旧产物会在换底模时被点名，由用户决定要不要全量重渲）——
它盯的正是"底模"这类真正的画质分水岭，而本轮**底模一个都没换**。想重出某一集时用 `cmd\renderone -shots …` 即可，不需要动 `q`。

* **现象/证据**：`internal\pipeline\stages_render.go:679 renderFingerprint()` 的指纹串是
  `prompt / mode / WxH / steps / seed / seam / [accel] / **"q1"**` + 参考图文件指纹 —— **不含 ComfyUI 版本号**。
  代码里的注释把 `q1` 定义为「采样环境版本（v1=2026-09-10 撤下 fp16_accumulation/ck-attention 后的画质基线；
  **画质敏感项再变更时递增，旧镜指纹失配自动重渲**）」。
* **本轮的事实**：升级后**音频流逐帧完全相同、视频流 124/124 帧全不同** ⇒ 变化集中在**视频侧路径**
  （VAE 解码 / 显存策略），音频路径没动。这是"画质敏感"的一类变化。
* **两个选项**：

  | 选项 | 做法 | 后果 |
  |---|---|---|
  | **A（默认，推荐）** | `q1` 不动 | 存量项目的已渲镜头**继续有效**（升级不会触发全量重渲）；**新渲的镜头**自动用新本体。用户若觉得某集要重出，按集/按镜重渲即可 |
  | B | `stages_render.go:700` 的 `"q1"` 改成 `"q2"` | **所有项目所有已渲镜头一次性判失效**（重渲成本 = 全量出片时间），换来"整片同一版本出图" |

* **判据（选 B 时的验收）**：改后跑 `cmd\preflight` + 任一集渲染，日志里应能看到该集镜头被重新渲（而不是命中旧产物）；
  并且**成片里不再混新旧两版本体出的镜头**。
* **我的建议**：**A**。理由：跨本体版本的画面本来就**不可复现**（实测两版视频帧全不同），
  而"混版本"的代价只在**同一集内**才有观感意义；按集重出比全量重出便宜得多。
  **这件事需要你（用户）拍板**，因为它直接决定"要不要把已有的片子重出一遍"。

---

## 4. 可选-1：`w6a8` 备选档（已在盘、已实测、**默认接线未动**）

**结论（2026-10-10）：不接。** 实测无优势 —— 生产规格同参：在用 int8 `256.5 s / 峰值 19552 MiB`，
w6a8 `261.9 s / 峰值 19552 MiB`（**慢 5.4 s、峰值完全相同**）。与库内 `w4a8` 的结论同型：本机峰值由 `--vram-headroom` 压在天花板，
权重体积换不来显存下降。**它现在只是"多一档可选"，不是更优**；文件与冒烟证据都在盘，随时可接（接入步骤见本节下文）。

* **落点（盘上已有）**：`D:\Ai\ComfyUI\shared\models\diffusion_models\`
  `minimax_h3_ref2va_pruned_w6a8.safetensors` / `minimax_h3_fl2va_pruned_w6a8.safetensors`（各 15,983,746,636 B，
  sha256 与上游 `Comfy-Org/MiniMax-H3` 的 LFS oid 逐一吻合：`ece96bbb…` / `ac746a2e…`）。
* **可用性（实测，不是"文件在"就算数）**：加载器认得这个格式
  （`Found quantization metadata version 1` / `Detected mixed precision quantization` / `Native ops: …w6a8_int8…`）；
  小规格真出片 `512×896 / 39 帧 / 2 步` **success 57.2 s**；生产规格同参真跑见下表。
* **生产规格实测（768×1344 / 124 帧 / 8 步 / 同 seed）**：

  | 底模 | 耗时 | 峰值 used | 最低 free | 平均 used | 产物 |
  |---|---|---|---|---|---|
  | 在用 `Singularity …_int8`（v0.39.1） | **256.5 s** | 19552 MiB | 4586 MiB | 15801 MiB | 2,796,442 B |
  | 新增 `…_pruned_w6a8` | 261.9 s | 19552 MiB | 4586 MiB | 15951 MiB | 3,254,971 B |

  ⇒ **w6a8 在本机既不快也**不省显存**（峰值完全相同）** —— 与库内 `w4a8` 的既有结论同型：
  峰值被 `--vram-headroom 4` 的余量策略压在 24 GB 天花板附近，**权重体积差换不来峰值下降**。
* **平台的动法（若要做）**：新增一个可选底模档 —— `internal\config\config.go:38` 那一族字段旁加一个
  `unet_ref2va_w6a8`，`engine.go` 的 `unetFor()` 按档选择（**默认仍指 int8**）。
  判据：设置页能选、真跑 `status=success`、`ffprobe` 规格一致。
* **建议**：**先不接**。它唯一的价值是"多一档可选"，而**它的画质差异我这里无法判定**（没有看图能力）。
  要接就得先做**同一 prompt / 同 seed 的 A/B 抽帧目视**（对比片已在 `ComfyUI\output\manjux_acc\shot_{after,w6a8}_00001_.mp4`）。

---

## 5. 可选-2：Fun ControlNet + 10 个特效 embedding（补丁与新资产已就位）

**结论（2026-10-10）：不接。** 这两样是**新能力**（运镜/姿态/局部控制；特效预设），不是对现有最优出片路径的改进；
而且要用它们必须先做同 prompt 的 A/B 抽帧目视（我这边没有看图能力）。当前看不到"接了就更优"的证据 ⇒ 权重与消费节点都留在盘，等有明确需求再接。

* **盘上新增**：
  * `shared\models\model_patches\minimax_h3_fun_controlnet_union_pruned_int8_convrot.safetensors`（2,296,635,360 B，
    sha256 `9c645c0a30…fed0c`）—— "补丁 + 消费节点"这一对是**齐的**；
  * `shared\models\embeddings\minimaxh3_*.safetensors` **10 个**（art_is_explosion / blooming_flowers / bullet_time /
    dark_magic / fire_breath / four_seasons / kiss_camera / spiral_ascent / storm_magic / truman_show，共 ≈10.7 MB）。
* **消费节点（本体自带，升级前后都在）**：`MiniMaxH3FunControlNetApply`（`comfy_extras\nodes_minimax_h3`）——
  输入 `model / model_patch / vae / strength / start_percent / end_percent`，
  可选 `control_video / mask / source_video`（`mask` 的 tooltip：`1 marks the regions to regenerate`）。
  ⇒ 可以做**运镜/姿态/局部重绘式控制**，属于**新能力**，平台当前 0 处引用。
* **判据（要接的时候）**：`ModelPatchLoader → MiniMaxH3FunControlNetApply → 采样链` 跑通一镜、`status=success`，
  并**同一 prompt 上有无 control_video 的 A/B 抽帧**（否则等于给画面加了一个未验证的输入）。
* **建议**：**先不接**。它是"能加功能"，不是"修当前问题"；且控制强度/适用面必须先做 A/B 目视。
  embedding 那一组更轻（只在提示词里点名词即可生效），要试的话建议**单镜试、别进交付默认**。

---

## 6. 可选-3：Acc-8Step 两种上游转换版并存（**当前零影响，建议先不动**）

**结论（2026-10-10）：不动。** 本机 4 个底模全是量化版 ⇒ 平台判据 `accelQualityPlan()` 已认定「量化底模与 Acc-8Step 不兼容」，
`quality` 档**必然回退 turbo** ⇒ 换哪一版转换对当前交付都是**零影响**；两版转换的差异（578 vs 570 张量、差 4 个 I64 与若干 F32）
也**没做过功能实测**，此时替换只增加未知风险。两份候选的 sha256 都归档在 `_maintenance\20261007-upgrade\`。

**另加一条自查（2026-10-10）**：新本体 `986c4d15` 那条"支持 LynnReal 轻量 MiniMax-H3 VAE"我去核了上游 ——
确实存在这类第三方轻量/细节 VAE（`corechan/MiniMax-H3-LightVAE`、`stdstu123/LynnReal-Onmi-light-vae`、
`speach1sdef178/MiniMax-H3-X2-Detail-VAE` 等），**官方 `Comfy-Org/MiniMax-H3` 的 vae 目录只有 fp16 / int8_convrot / audio_fp32 三件**。
轻量 VAE 的收益是**解码更快**（解码在整段里占比不小），但**非官方源 + 画质未验证**（典型是蒸馏/有损），
而 `video_vae` 是**交付档的显式设置**，换它属于本仓反复记过的"静默劣化"风险类别 ⇒ **不接**。
要用的话：先自己挑一集做同参 A/B 抽帧，确认画质能接受再改 `settings.json` 的 `video_vae`（改一行、可回退）。

* **现象/证据**（`_maintenance\20261007-upgrade\` 有全部 sha256 与结构比对）：

  | 版本 | 体积 | sha256 | 结构 |
  |---|---|---|---|
  | 本地在用 `MiniMax-H3-{Ref2VA,FL2VA}-Acc-8Step_pruned_comfy.safetensors` | 1,725,921,392 B | `6f18e1c2…` / `e97b813a…` | **578** tensors（BF16 518 / F32 56 / **I64 4**） |
  | `RunningHubAI/rh-*-acc-8step-pruned-comfy*`（`lastModified` 2026-09-21，归档在 `upstream-acc-converted\`） | 1,704,948,768 B | `94493775…` / `3193661d…` | **570** tensors（BF16 516 / F32 54 / I64 0） |
  | `A20labs/MiniMax-H3-Acc-LoRAs`（`lastModified` 2026-10-05，归档在 `upstream-raw\`） | 1,372,450,680 B | `111c82e6…` / `0b29be70…` | **PDD 源权重，未转换、不可直接挂载** |

  两者的 `lora_rank=64`、`converted_layout=comfyui_minimax_h3`、转换说明串**完全一致**；
  A20labs 那条"10-05 更新"经核**不是新权重** —— 该仓唯一提交是 `Duplicate from alibaba-pai/MiniMax-H3-Acc-LoRAs`（原仓 2026-08-27）。
* **为什么建议先不动**：该 LoRA 只在 `quality` 档用，而 `engine.go:962 accelQualityPlan()` 已判定
  「量化底模不兼容 Acc-8Step」⇒ 本机 4 个底模全量化，**quality 档必然回退 turbo**。
  即：换它对当前交付**零影响**，却要承担"换了未知转换器版本"的回归风险。
* **要真接的话（可复现步骤）**：① 备份在用文件；② 用 `upstream-acc-converted\` 里的同名文件替换；
  ③ 跑一镜 `quality` 档（需非量化底座，40 GB 级 —— **本机没有**）或至少跑一次 LoRA 加载冒烟；
  ④ 同参 A/B 抽帧目视。**在③④做完之前，不要进交付默认。**
* **判据**：若只是"想统一版本"，请把这条当**决策记录**而不是待办 —— 我在留档里已把两份都存好，随时可切。

---

## 7. 必做（小）：登记本轮到 `COMPATIBILITY.md` / `CHANGELOG.md`

> 这两个文件由**平台侧**改（我这边只新增了本文一个文件，避免两会话同改）。
> 下面是可以直接粘的文本（`COMPATIBILITY.md` 的 §八升级记录按时间倒序，记得插在最上面）。

**`docs\COMPATIBILITY.md` §八 升级记录（新增一条，倒序置顶）**

```markdown
### 2026-10-08:ComfyUI 本体 v0.37.4 → v0.39.1 + 7 个插件整表刷新 + 模型补装(**已生效**)

* 本体 `8ff6dc38` → **`de0125d9`(v0.39.1)**;pip 受控包 frontend 1.53.10 / templates 0.11.77 / docs 0.5.13 / kitchen 0.2.37;
  **torch / torchvision / torchaudio / transformers / numpy / tokenizers / huggingface_hub / safetensors / Pillow / av / sageattention 逐项零漂移**。
* 插件 7 仓升到上游 HEAD(audio-T8 / rgthree / was-node-suite / Director / LTXVideo / MiniMaxH3-Easy / vrgamedevgirl),
  18 仓落后 0;`/object_info` 2503 → 2849;`IMPORT FAILED` 0;平台阻断级 10 类 + 在用 54 类全在,schema 零破坏性变更。
* **生产配方真跑(768×1344/124 帧/8 步/8 步族 LoRA/3 参考图/seed 20260924):291.1 s → 256.5 s(−11.9%),
  峰值 19558 → 19552 MiB;规格与音轨不变**。逐帧指纹:视频流 124/124 帧全不同、音频流 162/162 帧逐字节相同。
* 模型补装:`minimax_h3_{ref2va,fl2va}_pruned_w6a8`(各 15.98 GB,sha256 对上游 LFS oid)、
  `minimax_h3_fun_controlnet_union_pruned_int8_convrot`(2.30 GB)、`embeddings\minimaxh3_*` 10 个。
  **`data\settings.json` 逐字节未动**;未删除/未覆盖任何在用权重。
* 回滚点:本体分支 `backup_before_v0.39.1`、被更新的 7 个插件仓各有 `backup_before_20261007`。
* 留档与全部证据:`D:\Ai\ComfyUI\_maintenance\20261007-upgrade\`(升级记录 + evidence/ + 三段实跑报告)。
* 未做/未验:**没升 github/master(比 tag 多 21 个提交)**;**画质未做人眼判定**(所有数字只覆盖耗时/显存/规格/指纹)。
```

**`docs\CHANGELOG.md` 新增条目（倒序置顶）**

```markdown
## ComfyUI 侧升级到 v0.39.1:同参真跑快 11.9%,音频路径逐帧一致(2026-10-08)

平台版本检查报「落后 8 项」后,ComfyUI 侧完成本体 `v0.37.4 → v0.39.1`(含 5 条 H3 直接相关修复:提前释放
embedding 临时张量降峰值、VAE offload 修复、VAE `rms_rope` 崩溃修复、VAE 分块混邻、支持 LynnReal 轻量 VAE)、
7 个插件升到上游 HEAD(共 18 仓落后 0)、并补装 w6a8 备选档 / Fun ControlNet 补丁 / 10 个特效 embedding。

**关键实测**(平台生产配方,768×1344/124 帧/8 步/8 步族 LoRA/3 参考图/seed 20260924,唯一差异是本体版本):
`291.1 s → 256.5 s(−11.9%)`,峰值 `19558 → 19552 MiB`,两条产物规格与音轨一致;逐帧指纹显示
**视频流全不同、音频流逐字节相同**(变化集中在视频侧路径)。`data\settings.json` 未动,存量镜头指纹不含本体
版本 ⇒ **升级不会让既有成片判失效**。

平台侧**无需改代码**:TaoMate 3 步档(`engine.go:871`)、8 步族选路(`engine.go:925`)、
LoRA 与步数成对(`accelLoraFor`/`accelSteps`)、quality 回退播报(`AccelTierNote`)都已在位。
待拍板项与可选接线见 `docs\ComfyUI侧交付-v0.39.1升级与模型补装-2026-10-08.md`。
```

---

## 8. 复验命令（平台侧复制即用）

```powershell
# ① 版本对齐(平台同一判据:本体 tag+commit、插件上游默认分支 HEAD)——期望「落后项 = 0」
pwsh -File D:\Ai\ComfyUI\_maintenance\20261007-upgrade\evidence\accept.ps1 -Tag verify
# ② 节点类与 schema(需要 8199 上有实例;期望 blocking 10/10、used 54/54、无 breaking change)
& D:\Ai\ComfyUI\standalone-env\python.exe -X utf8 D:\Ai\ComfyUI\_maintenance\20261007-upgrade\evidence\check_nodes.py present D:\Ai\ComfyUI\_maintenance\20261007-upgrade\acc-after.object_info.json
# ③ 设置项 ↔ 盘上文件(期望 ok=13 missing=0 empty=2;后两格为设计上就该空)
#    —— 由 ① 的第 (1) 节一并输出
# ④ 想自己重跑一遍生产配方(约 5 分钟,产出报告 JSON + 显存曲线)
& D:\Ai\ComfyUI\standalone-env\python.exe -X utf8 D:\Ai\ComfyUI\_maintenance\20261007-upgrade\evidence\run_acc.py after D:\Ai\ComfyUI\_maintenance\20261007-upgrade\wf-prod-ref2va-8step.json
# ⑤ 目视对比片(需要人眼)
#    _maintenance\20261007-upgrade\20261010-recheck\shot_after2_00001_.mp4（10-10 生产配方 768×1344/124 帧）
#    与 platform-path-shot_005.mp4（平台自己的生产路径产物 768×1344/192 帧）；要看别的段落按 §8 的 run_acc.py 重跑
```

---

## 9. 边界与未验事项（照实说）

1. **我没有改平台的任何文件**（本文是唯一新增）；`settings.json` 逐字节未动；不改 Go 代码 / frontend。

2. **绘画质结论**：我这里**没有"看画面"的能力**。报告里所有数字都是**耗时 / 显存 / 规格 / 帧指纹**，
   不构成"画质更好"的判断 —— 要看观感请按 §8-⑤ 打开留档里那两个 mp4。
3. **没升 master**：本体停在 release tag `v0.39.1`；`github/master`（`af89add6`）比它多 21 个提交，未经验证，本轮不动。
4. **没清理任何在用模型**：本轮只新增；`shared\models` 从 55 文件/201.2 GB → **83 文件/233.1 GB**。
5. **真跑用的输入是只读的**：归档第 01 集的 `h3_prompt` + `ComfyUI\input` 既有参考图；
   `data\_archived`、`data\小说`、`data\assets_library`（以及你自己的工程数据）一个字节没动。
6. **一条给你的提醒**：`data\projects` 下现在有 8 个工程（其中有 1 集渲过 4 个镜头）、`assets_library` 仍然为空 ——
   这些都是**你自己的数据，本轮一个字节没动**；本轮验收用的是"引擎侧同配方真跑 + 平台自己的 `cmd\renderone` 各一条"。

---

## 10. 两件"顺手查出来、供平台侧记账"的事（**不是要求你改**）

### 10.1 ComfyUI `input` 同名覆盖：那份"已知风险"这次被实测撞上了（良性）

* **平台文档里的原话**：`docs\剧本与渲染链审计-2026-10-06.md` §五-8 ——「局部重绘/音频驱动/放大按设计绕过管线互斥，
  与另一项目渲染并行时可能撞 ComfyUI input 同名文件（`client.go:102` `overwrite=true`）」，当时结论是"不确定是否真的发生"。
* **本次实测**：我为验收用的三张参考图，就是平台自己资产链放在 `D:\Ai\ComfyUI\ComfyUI\input` 的同名文件
  （`characters_char_char_qiqi.png` / `…_board_canvas.png` / `scenes_scene_scene_bay.png`）。
  跑 §1 那条平台路径时，平台**重新生成并上传了同名资产** ⇒ 同名的两张被覆盖：
  `characters_char_char_qiqi.png` `1,212,763 → 1,205,898 B`、`…_board_canvas.png` `916,480 → 909,409 B`
  （场景 `scenes_scene_scene_bay.png` 未被碰，另新增 `scenes_scene_scene_huishoucang.png`）。
* **影响**：① 验收产物本身已留档，结论不受影响；但**要逐字节复现那两段验收**就需要当时的参考图副本；
  ② 更实际的推论 —— **同名资产在 `input` 里是全局命名空间**：同一个项目重跑、或两处并行，都会互相覆盖上传物，
  而 ComfyUI 侧无版本、无告警。**要不要加"内容指纹命名/防覆盖"由你们定**（我这里只是把现象钉住）；
  现场快照见 `_maintenance\20261007-upgrade\platform-path\comfyui-input-after-run\`。

### 10.2 归档的 2026-09-29 第 01 集方案**当前门禁下渲不出来**（与本次升级无关）

* 用 `cmd\preflight` + `cmd\renderone` 实跑该归档集的**副本**，被平台自己的门拦住：
  ① **19 处 `dialogue[].text` 为空** ⇒ fatal（"渲染时模型没有台词可念，会自己瞎念"）——
     逐条核过是**真空串**，不是字段漂移；② 清掉空台词后又冒出 **17 处「引用了不存在的场景」** ⇒ fatal
     （该方案 `plan.scenes` 是**空的**，17 镜却都引用了 `回收舱`/`名册厅`）。
* 我在**副本**里按平台提示处理（删空台词 + 补 2 张最小场景卡，`platform-path\patch_scenes.py` 可复现）之后才跑通。
* **为什么告诉你**：这份归档正是 2026-09-29 因"污染与乱序"被归档的那批 —— 本次用现门禁**又量了一遍**它的坏法
  （台词空 + 场景卡缺），可以作为"归档决策正确"的旁证；**不需要你做任何事**，除非日后想复用它。
