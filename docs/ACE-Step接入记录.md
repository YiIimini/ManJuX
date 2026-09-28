# ACE-Step 1.5 接入记录（配乐生成）

## 为什么用它

- 本地跑、**Apache-2.0**（可商用）、ComfyUI **原生支持**（`comfy_extras/nodes_ace.py`，不需要第三方节点）。
- H3 的片段自带同期声（`overall_soundscape`），缺的正是**成片那条连续 BGM** —— 官方 qc-checklist STEP 8
  要求"一条连续 BGM + 人声下闪避"，平台侧的铺底与闪避已经做好（`compose/bgm.go`），缺的是"曲子从哪来"。

## 模型文件（Comfy Org 官方 repack，ComfyUI 原生布局）

| 文件 | 大小 | 目录 |
|---|---|---|
| `acestep_v1.5_turbo.safetensors` | 4.46 GB | `models/diffusion_models/` |
| `qwen_0.6b_ace15.safetensors` | 1.11 GB | `models/text_encoders/` |
| `qwen_4b_ace15.safetensors` | **7.80 GB** | `models/text_encoders/` |
| `ace_1.5_vae.safetensors` | 0.31 GB | `models/vae/` |

合计 **13.68 GB**。下载源：`hf-mirror.com/Comfy-Org/ace_step_1.5_ComfyUI_files/resolve/main/split_files/…`
（官方地址在 huggingface.co，本机走 hf-mirror 更快）。

> **纠正记一笔**：我最初以为 4B 编码器是"可选、先不装"—— 查蓝本后确认**错了**：
> 官方蓝本 `Text to Audio (ACE-Step 1.5).json` 用的是 `DualCLIPLoader`，其默认值就是
> `["qwen_0.6b_ace15.safetensors", "qwen_4b_ace15.safetensors", "ace", "default"]`
> ⇒ **两个编码器都必需**。以文件为准，不以印象为准。

## 节点契约（来自本地 ComfyUI 源码与官方蓝本，不是猜的）

- `UNETLoader`：`acestep_v1.5_turbo.safetensors` → MODEL
- `DualCLIPLoader`：两个 qwen 编码器 + **`type="ace"`** → CLIP
- `VAELoader`：`ace_1.5_vae.safetensors` → VAE
- `TextEncodeAceStepAudio1.5`（正）：`clip, tags, lyrics, seed, bpm, duration, timesignature, language, keyscale, generate_audio_codes, cfg_scale, temperature, top_p, top_k, min_p` → CONDITIONING
- `ConditioningZeroOut`：负向
- `EmptyAceStep1.5LatentAudio`：`seconds, batch_size` → LATENT
- `KSampler` → LATENT；`ModelSamplingAuraFlow` 挂在 model 上
- `VAEDecodeAudio` → AUDIO；`SaveAudioMP3` → 落盘

## 配乐入资产库（已实现，`internal/library` + `pipeline.bgmPathOf`）

配乐按**需求**存：条目名 = `library.MusicEntryName(需求描述)`（标点折空格、按词合并、截 60 字）。
解析优先级：`assemble.bgm` 显式文件 ＞ 库内同需求条目 ＞ （生成后回填库）。
