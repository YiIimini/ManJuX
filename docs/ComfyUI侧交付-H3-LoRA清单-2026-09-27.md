# ComfyUI 侧交付 · H3 LoRA 清单与档位映射（2026-09-27）

> **谁写给谁的**：ComfyUI 运维/资产方 → **平台侧（档位与设置负责人）**。
> **回答的问题**：H3 到底有哪些 LoRA 可用、哪个档该挂哪个、哪些**看起来在盘其实挂不上**。
> 表中每一项都是**读 safetensors 头部**核出来的（patch 数 / rank / 元数据 / 键前缀），不是照文件名猜的。
> 复核留档：`D:\Ai\ComfyUI\_maintenance\h3-lora-inventory.txt`、`h3-lora-headers.py`、`verify-new-h3-loras.py`。

---

## 0. 三句话结论

1. **能挂的**：turbo 4 步族（ref2v v0.1 / fl2v v1.1 / v1.2）、**turbo 8 步族（ref2v v1.0 / fl2v v1.0，今天新补）**、TaoMate 3 步。
2. **挂不上的**：Acc-8Step ×2、PDD-Acc ×2、**turbo v4 step600 EMA**——前者被平台的 `isQuantizedBase()` 挡住，后者的元数据**自己声明与 `pruned_*` 底座不兼容**（我们的 4 个 H3 底模全是剪枝版）。
3. **解锁这批"挂不上"的总开关只有一个**：引入**非剪枝** H3 底座（fp16/fp8 档，40 GB 级）。在那之前，它们都只是占盘资产。

---

## 1. 全量清单（本机持有，逐个核过）

### 1.1 `shared\models\loras\`（加速类）

| 文件 | 体积 | patches / rank | 文件头读出的锚定信息 | 用途 |
|---|---|---|---|---|
| `minimax_h3_ref2v_turbo_4step_v0.1_comfyui_bf16` | 1.96 GB | 208 / r128 | `base_model: Comfy-Org/MiniMax-H3 minimax_h3_fl2va_bf16`；`training_scale 0.0625`；Diffusers PEFT | **平台现行默认**（人物链）；4 步锚定 |
| `minimax_h3_ref2v_turbo_8step_v1.0_768p_comfyui_bf16` | 1.96 GB | 208 / r128 | `training_rank 128`、`target_format: ComfyUI generic LoRA`、`qkv_fusion` | **今天新补**；8 步锚定（与平台实际步数一致） |
| `minimax_h3_fl2v_turbo_4step_v1.1_768p_comfyui_bf16` | 1.96 GB | 208 / r128 | `training_rank 128` | 空镜 fl2v 链；4 步锚定 |
| `minimax_h3_fl2v_turbo_4step_v1.2_768p_comfyui_bf16` | 1.96 GB | 208 / r128 | `qkv_fusion`、`training_scale 0.0625` | fl2v 最新 4 步版 |
| `minimax_h3_fl2v_turbo_8step_v1.0_comfyui_bf16` | 1.96 GB | 208 / r128 | `base_model: Comfy-Org/MiniMax-H3 minimax_h3_fl2va_bf16` | **今天新补**；8 步锚定（官方重打包 `loras/` 里就有） |
| `TaoMate-H3-3step-ComfyUI` | 1.24 GB | 208 / r128 | 无 metadata | 3 步蒸馏（实测 310.1→156.4 s） |
| `MiniMax-H3-Ref2VA-Acc-8Step_pruned_comfy` | 1.73 GB | **258** / r64 | `lora_alpha 64`、`pdd_block_size 4`、"PDD head ban" | quality 档；**挂不上**（见 §2） |
| `MiniMax-H3-FL2VA-Acc-8Step_pruned_comfy` | 1.73 GB | 258 / r64 | 同上 | 同上 |
| `minimax_h3_turbo_v4_step600_ema_comfyui` | 0.78 GB | **259** / r16,64 | `conversion_source_file: minimax_h3_turbo_v4_step600_ema`；**`incompatible_base: MiniMax-H3 pruned_*`** | 今天新补；**剪枝底座挂不上** |

### 1.2 不在 `loras\` 但同属 H3 的

| 位置 | 体积 | patches | 说明 |
|---|---|---|---|
| `pdd_acc\minimax_h3_{ref2va,fl2va}_pdd_acc_8step_comfyui` | 1.66 GB ×2 | 258 | `source_repo: alibaba-pai/MiniMax-H3-Acc-LoRAs`、`license: apache-2.0`、`pdd_num_steps 32` —— **上游原版**（Kijai 那两份是它的转换） |
| OpenVDN `adapters\turbo_pruned_curve_fl2va\adapter_model` | 1.15 GB | ~259 | 元数据带 `sampler_steps: 8` + `adaln_t_table_raw_sha256: ac8727cd…`（曲线门禁指纹）；**只能经 VDN 编曲器加载** |
| OpenVDN `adapters\turbo\adapter_model` | 0.85 GB | ~363 | 键前缀 `norm_out/token_refiner/...`（**非** `diffusion_model.*`）→ 不是普通 LoRA |
| OpenVDN `adapters\default\adapter_model` | 0.33 GB | 208 | 同上 |
| `ComfyUI\models\semantic_bridge\t8_compat\{MiniMaxH3_SemanticBridge_v1,BUNNY_H3_ActionLogic_Bridge_V1}_T8_Compat` | 11 + 22 MB | 非 LoRA | 6 张量小 MLP（`architecture: SenseNova_H3_Distilled_Student_V3`，in/out 5120、hidden 512）；供 Director / T8 的 semantic-bridge 节点用。**今天新补** |

### 1.3 不是 H3 的（别混进来）
`Qwen-Image-Edit-2511-Lightning-4steps-V1.0-bf16`（850 MB）、`qwen-image-edit-2511-multiple-angles-lora`（295 MB）——属**图像编辑 2511 档**；平台设置里 `edit_turbo_lora` / `edit_angles_lora` **为空且应为空**。

---

## 2. 可用性筛选（这一节比清单本身重要）

| 约束 | 影响谁 | 依据 |
|---|---|---|
| **平台量化门**：`isQuantizedBase()` 对 `int8 / w4a8 / nvfp4 / mxfp8` 一律判为量化底模 → 禁止挂 Acc-8Step 系 | Acc-8Step ×2、PDD-Acc ×2（共 4 份、6.8 GB）**在本机是死的**；选 `quality` 档会**静默回退 Turbo** | 平台源码；本机 4 个 H3 底模全是 int8/w4a8 |
| **剪枝底座 adaln 宽度差异**：剪枝版 AdaLN input = 8，非剪枝 = 2688 | `turbo_v4_step600_ema` **自声明** `incompatible_base: MiniMax-H3 pruned_*` | 该文件自身元数据 |
| **曲线门禁**（VDN 侧）：只放行 `adaln_t_table = ac8727cd…`（fl2va_pruned_curve_v1） | VDN 附加件只服务 fl2va 曲线底座；stock ref2va 剪枝与 Singularity w4a8 被拒 | 实测（见《加速路线与显存回退》§4） |

> 三条合起来一句话：**在本机现有底模（全部剪枝 int8/w4a8）上，真正能挂的只有 4 步族 / 8 步族 / TaoMate 三类**。其余是"在盘但用不上"。

---

## 3. 档位映射建议 + 实测

### 3.1 建议映射（现状 vs 可用改进）

| 档位 | 现状挂载 | 建议 | 理由 |
|---|---|---|---|
| 交付默认（8 步） | **4 步锚定的** `ref2v_turbo_4step_v0.1` | 可换 **`ref2v_turbo_8step_v1.0`** | 锚定步数与实际步数一致；**代价实测 +4.9% 时间**（见 3.2） |
| 空镜 fl2v | `fl2v_turbo_4step_v1.1/v1.2` | 可换 **`fl2v_turbo_8step_v1.0`** | 同上；官方重打包里就带这一支 |
| 抽卡/筛方向 | （无） | **TaoMate 3 步**（1.98×） | 代价：步数少时参考图更主导画面，建议只做筛方向 |
| quality | Acc-8Step（在盘但挂不上） | **维持现状**（等于 Turbo） | 真要解锁必须换非剪枝底座 |

### 3.2 实测：4 步锚定 vs 8 步锚定（同底模、同 8 步、同 3 张参考图）

| 运行 | 挂载 | 耗时 | 峰值显存 | 最低 free |
|---|---|---|---|---|
| **A**（现行默认） | `ref2v_turbo_4step_v0.1` | **310.1 s** | 22,186 MiB (90.7%) | 1,952 MiB |
| **I**（本次新挂） | `ref2v_turbo_8step_v1.0` | **325.2 s** | 21,675 MiB (88.6%) | 2,463 MiB |

**画质**：各出 4 帧横排对照（`_maintenance\taomate-compare\{A-int8-turbo8,I-ref2v-8step-lora}.png`）——
**两者是同一场景（暗色木构厅堂）、同一人物、同一动作节拍，均无肉眼崩坏**。
<span style="color:#e08b3c">**所以不要把这次对照读成"换 8 步版画质更好"**：单次样本看不出"4 步 LoRA 跑 8 步"有可见损失，8 步版的价值目前只在**理论对齐**上。</span>
要定论必须在交付规格下做多镜 A/B + 明确评分口径，本表不承担这个结论。

---

## 4. 今天新增的 4 个文件（下载器已逐个校验 sha256）

| 文件 | 体积 | 来源 |
|---|---|---|
| `minimax_h3_ref2v_turbo_8step_v1.0_768p_comfyui_bf16` | 1,956,193,000 B | ModelScope `lightx2v/Minimax-h3-Turbo`（上游作者） |
| `minimax_h3_fl2v_turbo_8step_v1.0_comfyui_bf16` | 1,956,193,000 B | 同上（亦在官方 `Comfy-Org/MiniMax-H3` 的 `loras/`） |
| `minimax_h3_turbo_v4_step600_ema_comfyui` | 779,858,792 B | HF `t8star/minimax_h3_turbo_v4_step600_ema_…_compat_v001` |
| SemanticBridge ×2 | 11,024,432 / 22,046,928 B | HF `t8star/Semantic-Bridge-Comfy` |

**许可提醒**（沿用库内规矩）：H3 权重随 **MiniMax H3 Community License**（含地域条款）；`alibaba-pai` 那两份 Acc LoRA 自带 `apache-2.0`；社区转换件的许可状态与权重本身要分开看，商单前逐项确认。

---

## 5. 还没解决的 / 待拍板

| 项 | 状态 |
|---|---|
| `turbo_v4_step600_ema` 用不上 | 等**非剪枝底座**；T8 包引用它 100 次是因为它自己的流程用非剪枝件 |
| Acc-8Step / PDD-Acc（6.8 GB）闲置 | 同上（总开关＝非剪枝底座） |
| HyperFlow 8 步（`drbaph/Hyperflow-Comfyui`，3.9 GB + 需另装节点包） | **本轮未下**：它要独立的 Hyperflow 节点包，且 T8 文档把它标为实验线 |
| ref2v 侧没有 4 步 v1.1/v1.2 | **上游就是没有**（`lightx2v` 仓 19 个文件里 ref2v 只有 4step v0.1 与 8step v1.0）——不是我们漏下 |
| 是否把 8 步锚定版本设成默认 | **需平台侧决定**（涉及改档位语义与文案） |

---

## 6. 复核命令

```powershell
# 清单与头部解析（秒级，不加载张量）
& D:\Ai\ComfyUI\standalone-env\python.exe D:\Ai\ComfyUI\_maintenance\h3-lora-headers.py
# 今天新增四件的真身核对
& D:\Ai\ComfyUI\standalone-env\python.exe D:\Ai\ComfyUI\_maintenance\verify-new-h3-loras.py
# 缺件补齐（幂等：已完整的会跳过，sha256 由下载器校验）
pwsh -File D:\Ai\ComfyUI\_maintenance\get-h3-loras.ps1
```
