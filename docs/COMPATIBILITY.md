# 兼容性与钉版清单

> 首次建档:2026-09-11(实测采集)。**版本会漂移,渲染出问题时先从这里比对。**
>
> 最近更新:2026-09-12(第一节版本行复核为 v0.35.1;第八节状态改为「升级已完成并保持」;
> 第九节的两个放大模型已落地)。此前第七节的维护约定同样适用:**改了一处就回头改第一节**,
> 别让两处版本号打架 —— 本文件这次过时正是因为第一节与第八节各说一套。
>
> 建档动机:此前只有 `data/rollback_points.txt` 一份人工备忘,且**不被任何代码读取**——
> 建档当天的实测比对显示,其中 4 个插件的 commit 已全部变更(如 Motion-Context
> `e5f6f62` → `5335715`),备忘实质失效。本文件是它的正式替代。
>
> 做法参照 [ComfyUI-ALLinONE-MinimaxH3](https://github.com/LeonQ8/ComfyUI-ALLinONE-MinimaxH3)
> 的 COMPATIBILITY.md:把"开发与测试所依据的精确栈"写下来,并记录**已知冲突的机制**,
> 而不只是版本号。

## 一、实测环境(2026-09-11 建档,版本行 2026-09-12 复核)

| 组件 | 版本 |
|---|---|
| ComfyUI | **v0.35.1**(`git describe` = `v0.35.1`,HEAD `856a922b`,`comfyui_version.py` = 0.35.1;2026-09-12 复核) |
| Python | **3.13.12**(`comfyui/standalone-env`) |
| PyTorch | **2.11.0+cu130**(CUDA 13.0) |
| OS | Windows 10/11 |
| 形态 | 自包含:`comfyui/` 内含 ComfyUI 本体 + Python 环境 + 模型 |

## 二、自定义节点包(实测 commit)

ManJuX 的渲染图**直接依赖**下列节点类,任何升级改名都会让渲染在运行期失败:

| 包 | 当前 commit (2026-09-11) | 被谁使用 |
|---|---|---|
| ComfyUI-H3-Motion-Context | `5335715` | 镜间接缝(`MiniMaxH3MotionContext*` 四个节点) |
| ComfyUI-H3-ConditioningCache | `239d21e` | 两阶段条件缓存(`MiniMaxH3CondSave/Load`) |
| ComfyUI-MiniMax-H3-PDD-Acc | `311a65d` | quality 加速档(Acc-8Step LoRA / `MiniMaxH3SigmaShift`) |
| ComfyUI-MiniMaxH3-Easy | `d100200` | H3 相关辅助节点 |
| ComfyUI-KJNodes | `5710537` | 通用图像节点 |
| ComfyUI_IPAdapter_plus | `a0f451a` | 参考图相关 |
| rgthree-comfy | `2c5342a` | 图工具 |
| was-node-suite-comfyui | `c0598f6` | 通用图像节点 |
| MaskVidExperiments | `e5c4450` | **局部重绘**(遮罩裁切/回贴) |
| comfyui-vrgamedevgirl | `050ead7` | 音频处理节点 |

> 上表 commit 只作**回退参照**:平时无需钉版,出问题时可 `git -C <插件目录> checkout <commit>` 回退。

## 三、已知冲突(机制级记录)

### ComfyUI-MiniMaxH3-Cache 会破坏镜间接缝 —— **当前未安装,勿装**

这不是版本号冲突,而是**启动期 monkeypatch 的全局副作用**:

- 该包在 ComfyUI 启动时**全局替换** `MiniMaxH3Model._forward` 的签名,替换后丢掉了原生
  `denoise_mask` / `audio_denoise_mask` 参数与 `cond_audio` 段。
- Motion-Context 插件会**探测这些签名**,缺失即拒绝运行。
- 因此**即使工作流里没有任何 cache 节点**,只要该包装着,接缝链路就会失败。
- 上游 PR 只删了一处 `time_shift_slope` 调用,未恢复签名 —— **升级解决不了**。

**若已安装**:把插件目录改名以 `.disabled` 结尾(如 `ComfyUI-MiniMaxH3-Cache.disabled`)再重启 ComfyUI。
ManJuX 的渲染前体检会自动检测该目录并告警(见 `internal/engine/comfyengine/preflight.go`)。

### 同名易混淆:ComfyUI-H3-ConditioningCache ≠ ComfyUI-MiniMaxH3-Cache

- `ComfyUI-H3-ConditioningCache`(`239d21e`)是 ManJuX **正在使用**的条件缓存,与上述冲突无关。
- 冲突的是 `ComfyUI-MiniMaxH3-Cache`(lihaoyun6 系)。两者名字相似但完全不同,排查时勿混。

## 四、模型文件

官方 [Comfy-Org/MiniMax-H3](https://huggingface.co/Comfy-Org/MiniMax-H3),经设置页配置到 `comfyui/ComfyUI/models/` 对应子目录:

| 文件 | 目录 | 用途 |
|---|---|---|
| `MiniMax_H3_ref2va_pruned_int8_convrot.safetensors` | `diffusion_models/` | Ref2V 底模 |
| `MiniMax_H3_fl2va_pruned_int8_convrot.safetensors` | `diffusion_models/` | T2V/I2V/首末帧底模 |
| `qwen3vl_32b_minimax_h3_nvfp4_awq.safetensors` | `text_encoders/` | 文本编码器 |
| `minimax_h3_video_vae_fp16.safetensors` | `vae/` | 视频 VAE |
| `minimax_h3_audio_vae_fp32.safetensors` | `vae/` | 音频 VAE(缺失会导致黑屏) |

> `minimax_h3_video_vae_int8_convrot.safetensors`(express 极速档用)与
> `MiniMax-H3-*-Acc-8Step_pruned_comfy.safetensors`(quality 档用)由代码按存在性自动回退,
> 不配也能跑,只是拿不到对应档位。

## 五、本地验证构建(务必带 Wails build tags)

ManJuX 是 Wails 应用,**直接用 `go build` 编译出来的是「占位实现」**,运行时缺少真实
运行时并弹出提示框:

> Wails applications will not build without the correct build tags.
> Please use "wails build" or press "OK" to open the documentation on how to use "go build"

实测对比(2026-09-11):

| 命令 | 产物大小 | 运行表现 |
|---|---|---|
| `go build -o x.exe .` | 14.5 MB | **占位实现 → 运行即弹提示框** |
| `go build -tags "desktop,production" -o x.exe .` | 20.5 MB | 真实实现,主窗口正常、无提示框 |
| `wails build`(scripts/build.ps1 用的) | — | 自带 tags,正常 |

**做任何真机验证都用第二条**(或直接用 `wails build`)。验证方式:启动后枚举窗口标题,
应当只看到「ManJuX · 短视频创作工作台」这一个主窗口,不该出现 Wails 提示框。

## 六、渲染前体检(代码侧护栏)

`internal/engine/comfyengine/preflight.go` 在每次渲染开始前自动核对:

1. **节点存在性** —— 按本次用到的模式检查 graph 根节点(Ref2V / I2V / SigmaShift)是否在 ComfyUI 侧存在,缺失即阻止渲染并给出中文提示(而不是跑到一半炸出 `node_errors`)。引擎不可达时跳过,避免误判。
2. **冲突包检测** —— 发现 `custom_nodes/` 下存在未禁用的 `MiniMaxH3-Cache` 时告警。

配套:`internal/engine/comfyengine/graph_contract_test.go` 提供**工作流图自身**的契约测试
(结构层 / 悬空链接 / 模型链同源 / 帧数网格 / 接缝链完整性),提交前即可发现图的问题。

## 七、维护约定

- 升级任一插件后,请更新上表 commit(`git -C <目录> rev-parse --short HEAD`)。
- `data/rollback_points.txt` 已废弃,保留仅为历史参照。
- 发现新的冲突时,请按第三节的格式补充**机制说明**(为什么冲突、影响哪些模式、怎么绕过),而不只写症状。

## 八、升级记录

### 2026-09-11:ComfyUI v0.34.6 → v0.35.1(**升级已完成并保持**)

> 状态说明:升级已完成并**保持**。2026-09-12 复核确认:`git describe` = `v0.35.1`、
> HEAD = `856a922b`、`comfyui_version.py` = `0.35.1`,与第一节表格一致。
> 建档当天曾出现「收尾时 HEAD 被切回 v0.34.6」的疑点(外部触发的 checkout,不是升级动作本身),
> 该状态现已不存在 —— 当前栈稳定停在 v0.35.1,第一节与 README 两处版本号均已同步。
> 若日后确实需要停在旧版,请同时回改本节与第一节,别让两处版本号再打架。

**动机**:v0.35 含一条对本项目直接相关的修复 —— `Fix MiniMax H3 denoise masks`(#15988),
正是局部重绘(Mask)依赖的底层行为;同时带来 LTX 的 latent guide / 生成关键帧节点。

**执行**:恢复被删的示例文件 → `git checkout v0.35.1` → `pip install -r requirements.txt`
(comfy-kitchen 0.2.33 / frontend 1.51.10 / workflow-templates 0.11.59)。

**升级后验证(启动实测)**:ComfyUI 0.35.1 正常启动,共 1929 个节点,以下关键节点**全部存活**:

| 分组 | 节点 | 结果 |
|---|---|---|
| H3 核心 | ReferenceToVideo / ImageToVideo / SigmaShift | ✓ |
| 条件缓存 | CondSave / CondLoad | ✓ |
| 镜间接缝 | MotionContext × 4 | ✓ |
| SAM3 跟踪 | VideoTrack / TrackToMask | ✓ |
| Mask 处理 | MVEx SubjectCrop / SubjectUncrop / MaskToLatentSpace / LatentMaskToMask | ✓ |

**回滚方式**:`git -C comfyui/ComfyUI checkout v0.34.6`(依赖需同步 `pip install -r requirements.txt`)。

## 九、放大模型(2026-09-12 已落地)

| 目录 | 文件 | 用途 | 大小 |
|---|---|---|---|
| `comfyui/shared/models/latent_upscale_models/` | `ltx-2-spatial-upscaler-x2-1.0.safetensors` | LTX 2× 隐空间放大 | 949.6 MB |
| `comfyui/shared/models/upscale_models/` | `4x-UltraSharp.safetensors` | RTX / ESRGAN 帧级放大(ManJuX 的 RTX 放大档) | 63.8 MB |

两个目录在 2026-09-11 建档时空缺,现均已就位 —— LTX 2× 与帧级放大档不再缺模型。
注意位置是 `comfyui/shared/models/`(**不是** `comfyui/ComfyUI/models/`),由
`comfyui/ComfyUI/extra_model_paths.yaml` 的 `shared` 段注册(base_path `D:/Ai/ManJuX/comfyui/shared/`),
ComfyUI 启动时自动加载 —— 这也是桌面版与命令行版共享模型目录的方式。
引擎层实现见 `internal/engine/comfyengine/upscalegraph.go`。

已就位的 LoRA(H3 加速链完整):

- `MiniMax-H3-FL2VA-Acc-8Step_pruned_comfy.safetensors`(quality 档)
- `MiniMax-H3-Ref2VA-Acc-8Step_pruned_comfy.safetensors`(quality 档)
- `minimax_h3_fl2v_turbo_4step_v1.1_768p_comfyui_bf16.safetensors`(turbo 档)
- `minimax_h3_ref2v_turbo_4step_v0.1_comfyui_bf16.safetensors`(turbo 档)
