# 兼容性与钉版清单

> 首次建档:2026-09-11(实测采集)。**版本会漂移,渲染出问题时先从这里比对。**
>
> 最近更新:2026-09-27(**同一批实测的口径更正**:第八节里 2026-09-24 的耗时/显存是「参考图被静默丢弃」的口径,带参考图的真实数字见 `docs/ComfyUI侧交付-加速路线与显存回退-2026-09-27.md` §2;另交付了 TaoMate 三步 LoRA 与 w4a8 一键切换脚本)。
> 上次更新:2026-09-26(ComfyUI v0.37.2 → **v0.37.4**,第一节版本行与第二节 commit 表同步,第八节新增升级记录;并首次写明**渲染根节点是本体内置**这条机制 —— 见第二节末)。
> 上次更新:2026-09-24 二次更新(ComfyUI v0.35.1 → **v0.37.2**,第二节 commit 表整表刷新,第八节新增一条 ComfyUI 升级记录,第九节改为「只 `shared_current` 一段生效」)。
> 上次更新:2026-09-24(第四节补 LTX 视频 VAE;第一节新增「ManJuX 自身构建栈」表并记入
> Wails `v2.16.0`;第五、七节补上第三方 fork 的约束;第八节新增 Wails 升级记录)。
> 此前更新:2026-09-12(第一节版本行复核为 v0.35.1;第八节状态改为「升级已完成并保持」;
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
| ComfyUI | **v0.37.4**(`git describe` = `v0.37.4`,HEAD `8ff6dc38`,`comfyui_version.py` = 0.37.4;2026-09-26 升级,回退基线 `backup_before_v0.37.4` = `830232b8`;再往前 `backup_before_v0.37.2` = `856a922b`) |
| Python | **3.13.12**(`comfyui/standalone-env`) |
| PyTorch | **2.11.0+cu130**(CUDA 13.0) |
| OS | Windows 10/11 |
| 形态 | 自包含:`comfyui/` 内含 ComfyUI 本体 + Python 环境 + 模型 |

### ManJuX 自身构建栈(2026-09-24 记入)

渲染出问题看上面那张表,**编译打包出问题看这张**:

| 组件 | 版本 | 说明 |
|---|---|---|
| Go | **1.27.1**(便携装,`D:\Ai\Tools\go`) | `go.mod` 声明 `go 1.25.0` |
| Wails CLI | **v2.16.0**(`D:\Ai\Tools\gopath\bin\wails.exe`) | 必须与下面这行同版本,否则每次构建告警 |
| Wails 运行时 | **v2.16.0**(`go.mod` 的 `github.com/wailsapp/wails/v2`) | 2026-09-24 由 v2.15.0 升上来 |
| go-webview2 | 上游 `v1.0.22` + **本地补丁**(见 `third_party/README.md`) | `go.mod` 有 `replace` 指向本地 fork |
| 前端 | Vue 3 + Vite 6 + Element Plus + Tailwind | 无需独立 node,用 DSH 自带 pnpm |
| 打包 | `scripts/build.ps1`(产出 exe)/ `scripts/release.ps1`(构建+提交+tag+推送) | 自适配便携工具链,可用 `MANJUX_GOROOT`/`MANJUX_GOPATH`/`MANJUX_PNPM` 覆盖 |
| 自检 | `scripts/check.ps1`(gofmt/vet/build/test/前端构建/第三方补丁在位 六道) | 提交前跑一遍 |

## 二、自定义节点包(实测 commit)

ManJuX 的渲染图**直接依赖**下列节点类,任何升级改名都会让渲染在运行期失败:

| 包 | 当前 commit (2026-09-24 全表刷新) | 被谁使用 |
|---|---|---|
| ComfyUI-H3-Motion-Context | `5335715`(未变) | 镜间接缝(`MiniMaxH3MotionContext*` 四个节点) |
| ComfyUI-H3-ConditioningCache | `4eead31`(原 `239d21e`,2026-09-24 整改;回退点 `239d21e`) | 两阶段条件缓存(`MiniMaxH3CondSave/Load`) |
| ComfyUI-MiniMax-H3-PDD-Acc | `311a65d`(未变) | quality 加速档(Acc-8Step LoRA;它注册的是 `MiniMaxH3PDDAcc*` 四个 —— `MiniMaxH3SigmaShift` 实为本体内置,此行原记法已更正) |
| ComfyUI-MiniMaxH3-Easy | `61ceb3c`(旧表记 `d100200`,实测早已不符,本次按实测改回) | H3 相关辅助节点 |
| ComfyUI-KJNodes | `d3cfe21`(旧 `5710537`) | 通用图像节点 |
| ComfyUI_IPAdapter_plus | `a0f451a`(未变;**2026-09-27 已停用**) | 参考图相关;ManJuX go/json **零引用**、上游 2025-04 起未更新 → 目录改名 `ComfyUI_IPAdapter_plus.disabled`(ComfyUI 跳过 `*.disabled`)。要恢复:去掉后缀,并把新路径补进 `safe.directory`(本机仓 owner 是另一台机器的 SID,改名会触发 `dubious ownership`) |
| rgthree-comfy | `449c58fc`(原 `2c5342a`,2026-09-26 升级 6 个提交) | 图工具 |
| was-node-suite-comfyui | `8f80db30`(原 `eb772ed`,2026-09-26 升级 1 个提交:新增 Execution Gate Controlboard / Fast Generate Text;视频加载器改为默认读全部帧) | 通用图像节点 |
| MaskVidExperiments | `c32ed8c`(旧 `e5c4450`) | **局部重绘**(遮罩裁切/回贴) |
| comfyui-vrgamedevgirl | `8c49237b`(2026-09-26 `75d4370`→`81a33b20` 29 个提交;2026-09-27 再 →`8c49237b` 16 个提交,均为许可/文档与向导字段顺序) | 音频处理节点;该包只注册 `VRGDG_*`(**265** 个;2026-09-27 补齐 librosa 与 ComfyUI-LTXVideo 后,启动时 3 个失败子模块归零),ManJuX 图**零引用** |
| ComfyUI-LTXVideo | `61ee82b`(**2026-09-27 新装**,Lightricks 官方,72 MB) | 官方 LTX 节点包(83 个节点)。**装它的直接原因是 VRGDG**:`VRGDG_LTXLoopingSampler` 按**目录名**反射加载 `ComfyUI-LTXVideo` 包、再取 `looping_sampler`/`easy_samplers`/`latents` 三个子模块,包不在就整块子模块 import 失败。ManJuX 图**不引用**它 |

> 上表 commit 只作**回退参照**:平时无需钉版,出问题时可 `git -C <插件目录> checkout <commit>` 回退。
> 完整 **18** 个节点(含上表之外的 FaceRefine / Turbo / Director / audio-T8 / Spectrum / latent-Upscaler / LTXVideo)
> 的基线快照见 `D:\Ai\ComfyUI\_maintenance\20260926-upgrade\baseline-repos.json`(最新)、
> `D:\Ai\ComfyUI\_maintenance\20260924-upgrade\baseline-repos.json`(上一版)。
>
> **2026-09-27 pip 侧两笔**(为消掉 VRGDG 的启动报错;装前装后逐项比对,关键版本**零漂移**):
> ① `librosa 1.0.0`(+ numba 0.67 / llvmlite 0.49 / scikit-learn / soundfile / soxr / pooch)——VRGDG 的 `.nodes` 与
> `.GeneralVideoNodes` 需要它(`librosa.load`、`feature.rms`、`beat.beat_track`、`onset.onset_strength`);
> ② `ComfyUI-LTXVideo` 的 `requirements.txt`(新增 `diffusers 0.40` / `timm 1.0.30` / `colour-science 0.4.7` /
> `OpenImageIO 3.1.17` / `ninja 1.11`)。落地位置=ComfyUI 自己的 venv `D:\Ai\ComfyUI\standalone-env\Lib\site-packages`。
> 未动的:`numpy 2.5.1` / `scipy 1.18.0` / `torch 2.11.0+cu130` / `torchvision` / `torchaudio` / `transformers 5.14.1` /
> `tokenizers` / `huggingface_hub 1.25.1` / `safetensors` / `Pillow`。
> ⚠️ 补的是**导入依赖**,不是 LTX 权重:VRGDG 的 LTX 相关节点现在能加载,真要跑仍需 LTX 主模型权重(本机没有,属大件,未下)。
>
> ⚠️ **渲染根节点是本体内置,不在插件里(2026-09-26 实测确认)**:`MiniMaxH3ReferenceToVideo` /
> `MiniMaxH3ImageToVideo` / `MiniMaxH3SigmaShift` / `EmptyMiniMaxH3LatentAV` 全部来自
> `comfy_extras/nodes_minimax_h3.py` —— 在 `/object_info` 里查它们的 `python_module` 即可验证;插件只提供各自前缀的类
> (PDD-Acc → `MiniMaxH3PDDAcc*`、Motion-Context → `MiniMaxH3MotionContext*`、Turbo → `MiniMaxH3Turbo*`)。
> **含义:升级 ComfyUI 本体就会改到渲染行为,而升级插件通常不会**(除非动了类名)。
> 因此本节的钉版表只覆盖「插件那一半」;本体那一半靠 `git describe` + 该文件的 sha256 比对来控制。

> **was-node-suite 的破坏性改名(2026-09-24 升级带入)**:该包把 25 个 id 加了 `WAS`/`WASNS` 前缀,
> 并删除 `DownloadImage` / `Image Send HTTP` / `Text Random Prompt` / `True Random.org Number Generator`
> / `Noodle Soup Pantry Refresh` 五类节点。已核对 ManJuX 侧 Go 源码:图形契约里**没有**引用任何 `WAS*` 节点,
> 故此次改名对渲染无影响;但若日后手工导入含上述旧 id 的工作流,需先在 ComfyUI 前端重新选节点。

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

- `ComfyUI-H3-ConditioningCache`(`4eead31`,2026-09-24 整改后)是 ManJuX **正在使用**的条件缓存,与上述冲突无关;整改只动了缓存写入与读取校验(原子写/越界拒绝/错误可执行/日志即时),**节点类名、输入输出与 .pt 载荷格式未变**,故本表其余结论不受影响。
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
(更省事的自动化验收:`scripts/check.ps1` 只是编译期闸门,**不含 GUI 验收**;
要验 GUI 就启动 exe 后看 `data/logs/manjux.log` 是否出现
`[WebView2] Environment created successfully` 与 `[manjux] 桌面窗口就绪`。)

### 依赖里有本地补丁(勿删勿覆盖)

`go.mod` 末尾有一行 `replace github.com/wailsapp/go-webview2 => ./third_party/go-webview2`
—— 那是**打了补丁的 fork,不是可以随手清掉的残留**。上游把原生文件对话框开闭期的瞬时
焦点错误当致命错误(`os.Exit(1)` 整进程退出),本地补丁把它降级为日志。
删掉 `replace`、或按 fork 自己的 README 重新同步上游,都会让"应用莫名整进程退出"复发。
补丁内容、自检命令、升级后的必做项见 **`third_party/README.md`**。

## 六、渲染前体检(代码侧护栏)

`internal/engine/comfyengine/preflight.go` 在每次渲染开始前自动核对:

1. **节点存在性** —— 按本次用到的模式检查 graph 根节点(Ref2V / I2V / SigmaShift)是否在 ComfyUI 侧存在,缺失即阻止渲染并给出中文提示(而不是跑到一半炸出 `node_errors`)。引擎不可达时跳过,避免误判。
2. **冲突包检测** —— 发现 `custom_nodes/` 下存在未禁用的 `MiniMaxH3-Cache` 时告警。

配套:`internal/engine/comfyengine/graph_contract_test.go` 提供**工作流图自身**的契约测试
(结构层 / 悬空链接 / 模型链同源 / 帧数网格 / 接缝链完整性),提交前即可发现图的问题。

## 七、维护约定

- 升级任一插件后,请更新上表 commit(`git -C <目录> rev-parse --short HEAD`)。
- **升级 Wails 后**三件事一起做:①确认 `go.mod` 与 CLI 同版本(否则每次构建告警);
  ②按 `third_party/README.md` 的自检命令确认 go-webview2 补丁仍在;③在本文件第八节记一条升级记录。
- **单独跑 `scripts/build.ps1` 时务必显式给 `MANJUX_VERSION`**:不带它时脚本用
  `git describe --tags` 推版本号,而本仓 tag 落后于实际版本(最新 tag `v0.34.4`,
  `version.go` 已是 `v0.35.0`),于是会把 `wails.json`/`frontend/package.json`/`version.go`
  **一起改回更旧的号**并把工作区弄脏。正确姿势:
  `$env:MANJUX_VERSION="v0.35.0"; powershell -File scripts\build.ps1`。
  (`release.ps1` 因为显式注入,天然躲开这个坑。)
- `data/rollback_points.txt` 已废弃,保留仅为历史参照。
- 发现新的冲突时,请按第三节的格式补充**机制说明**(为什么冲突、影响哪些模式、怎么绕过),而不只写症状。

## 八、升级记录

### 2026-09-26:ComfyUI 本体 v0.37.2 → v0.37.4 + 三个插件整表刷新(**已生效**)

**为什么升**:上游已发 v0.37.3 / v0.37.4,本地停在 v0.37.2。注意 `github/master` 与发行线**已经分叉**
——master 领先 39 个提交,但发行线把 master 上两个高风险改动**回滚**了(`ming-image` 支持、
workflow-templates 0.11.70),所以目标取**发行 tag `v0.37.4` 而不是 master**。

**实测改动范围**:`v0.37.2..v0.37.4` 共 11 个提交、只动 5 个文件,全部在**云 API 节点**
(`comfy_api_nodes/nodes_bytedance.py`、`nodes_openai.py`、`apis/bytedance.py`)+ 版本号;
**未触碰任何渲染路径**(无 minimax / sampl / attention / quant / model_management / vae / clip / ldm),
`requirements.txt` 亦无变化 —— **故本轮不装任何 pip 包**。master 线上的 frontend 1.53.6 / templates 0.11.70
是被发行线回滚掉的,不追。

**兼容性硬证据**:`comfy_extras/nodes_minimax_h3.py` 升级前后均为 **31,155 字节、sha256 逐位相同**
→ H3 生成链行为不变。

**同时升级的三个插件**(均为 fast-forward;升级前各在本目录建 `backup_before_20260926` 分支):

| 包 | commit | 提交数 | 备注 |
|---|---|---|---|
| comfyui-vrgamedevgirl | `75d4370a` → `81a33b20` | 29 | 该包只注册 `VRGDG_*`;**ManJuX 图零引用** —— 包内出现的 `MiniMaxH3*` 全在它自己的 tests/scripts 里,是引用不是注册 |
| rgthree-comfy | `2c5342a8` → `449c58fc` | 6 | Image Comparer 输出修复、fast group toggle 限制保持等 |
| was-node-suite-comfyui | `eb772eda` → `8f80db30` | 1 | 新增 Execution Gate Controlboard / Fast Generate Text;**视频加载器改为默认读全部帧** |

**验证**(启动 8199 实测:启动日志 + `/object_info`):本体报告 **0.37.4**、共注册 **2423** 个节点;
ManJuX 阻断级 10 个节点**缺失 0**;三个升级包分别加载 231 / 24 / 463 个节点(was-node 由 461 → 463,
正是该提交新增的两个);**无任何 traceback**。vrgamedevgirl 仍报 `librosa` / `ComfyUI-LTXVideo` 三个子模块缺失
—— **升级前就如此,非本轮引入**。

> **追记(2026-09-27 晚,已修)**:上面那段「仍报三个子模块缺失」当时是**待办**。已补装:① `librosa 1.0.0`;
> ② 官方 `ComfyUI-LTXVideo` 包(`61ee82b`)及其 requirements。复测启动日志:`[VRGDG] ... **265 nodes, 0 failed submodule(s)**`
> —— `Some submodules failed to import` 整段已消失;`ComfyUI-LTXVideo` 自身 83 个节点加载正常;
> 全站节点数 **2386 → 2503**,ManJuX 阻断级 10 个节点**缺失仍为 0**。组件层不受影响:本次只加依赖与新包,**未改现有底模与平台设置**。


**回退方法**:本体 `git checkout 830232b8`(分支 `backup_before_v0.37.4`);插件在各目录 `git checkout backup_before_20260926`。
新基线快照:`D:\Ai\ComfyUI\_maintenance\20260926-upgrade\baseline-repos.json`。

**顺带确认(未改)**:模型侧无新版可升 —— Singularity 仍是最新 v1.3(两份:在用 + 备用)、`lightx2v` Turbo LoRA
最新仍是 v1.2、Qwen-Image-Edit-2511 四件套刚按上游 sha256 装齐。

### 2026-09-24:H3 底模换成 Minimax-h3_Singularity v1.3(ref2va 槽,**已生效**)

**它是什么**:[WarmBloodAban/Minimax-h3_Singularity](https://huggingface.co/WarmBloodAban/Minimax-h3_Singularity)
——AIGC-Singularity 团队把 H3 的 `ref`/`fl`/`b25-49` 等多个检查点融合后,再做三天高步数深度微调 + 精确剪枝
得到的社区微调底模(Apache-2.0,base_model = MiniMaxAI/MiniMax-H3)。官方列出的针对性改进:
HDR 训练减少高速动作的拖影 / 中远景人脸不再崩 / **去掉重度"上油"光泽** / 剑斗与武打动态更强 /
法术粒子等 VFX 更好 / 运镜响应更好。README 称"100% 保留原模型的提示词遵循与风格广度"。
推荐搭配本机已在用的 `minimax_h3_ref2v_turbo_4step_v0.1` 加速 LoRA。

**为什么值得换**:它的第二条与第三条正好命中本项目两条已明确的画风要求 ——
用户 2026-09-24 定调「角色一律动漫化、不要真人脸,画面要**哑光不油腻**」,而基线 H3 的
"缎面高光衬衫 + 磨皮脸"正是当时报障的对象。

**落地文件**(`shared/models/diffusion_models/`,两个都已按上游 sha256 逐位校验):

| 文件 | 大小 | 用途 |
|---|---|---|
| `Minimax-h3_Singularity_ref2va_Pruned_v1.3_int8.safetensors` | 19.53 GiB | **已启用**(作者推荐档) |
| `Minimax-h3_Singularity_ref2va_v1.3_Pruned_w4a8.safetensors` | 10.96 GiB | 备用,未启用(见下) |

**实测(768×1344 / 124 帧 / 8 步 / turbo LoRA / `reserve_vram_gb=4`;复现脚本见
`D:\Ai\ComfyUI\_maintenance\20260924-upgrade\profile-vram.py`)**:

| 底模 | 单镜耗时(两次) | 峰值显存 |
|---|---|---|
<!-- 口径（2026-09-27 整改单 P0-1 补）：**下表全部是"参考图没有进模型"的 T2V 口径** ——
     带 3 张参考图的 ref2va 交付口径是 **310.1 s / 22,186 MiB(90.7%)**（见下方注记）。
     相对结论（哪条底模更快、省多少盘）不受影响；**绝对耗时/显存不得跨口径引用**。 -->
| 原 `MiniMax_H3_ref2va_pruned_int8_convrot` | 223.0s / 214.2s | 19,551–19,554 MiB(79.9%) |
| **Singularity v1.3 int8** | 202.5s / 213.9s | 19,554–19,582 MiB(80.0%) |
| Singularity v1.3 w4a8 | **179.7s** | 19,103 MiB(78.1%) |

> <span style="color:#e05252">**⚠️ 口径更正(2026-09-27)**:上表这批数字是**参考图没有进模型**时测的 ——当时的提交键写成聚合数组 `ref_images=[...]`,ComfyUI 只认点号键 `ref_images.ref_image_0`,聚合数组被**静默丢成空 dict**(不报错)。用点号键重测同一张图(同 seed / 同参数 / 3 张参考图 / 同 reserve):**int8+8步 = 310.1s / 峰值 22,186 MiB(90.7%)**,w4a8+8步 = 273.8s / 21,508 MiB(87.9%)。→ **本表的相对结论(int8 与 w4a8 的差距、Singularity 与原版的差距)依然成立,但绝对数字不可对外引用。**已用帧指纹互证参考图确实生效(同 seed 同参数下逐帧 md5 全不同)。详见 `docs/ComfyUI侧交付-加速路线与显存回退-2026-09-27.md`。</span>

**画质**:同一 prompt / 同一 seed / 同一组参考图,抽帧对比 —— 基线版衬衫是缎面高光、皮肤偏磨皮;
Singularity 两版都是哑光棉布(可见织物褶皱)且皮肤纹理更自然。客观代理指标(拉普拉斯方差,
高频细节量):基线 54.5 / 46.9,**Singularity int8 111.1 / 99.8(约 2 倍)**,w4a8 仅 46.2 / 37.5。
⚠️ 三组帧的构图并不相同(换了权重,同 seed 也出不同样本),所以这是**倾向性证据不是受控测量**;
但"int8 细节约为基线两倍 / w4a8 明显低于基线"两条差距足够大,方向可信。

**为什么不用 w4a8**(虽然快 16%、省显存):4-bit 权重的代价正好落在高频细节上(上表),
而本项目要的是中远景人脸与材质细节。**它是可选项不是默认**:要试的话改
`settings.json` 的 `unet_ref2va` 即可 —— 代码侧已经铺好(见下条)。

**为什么只换 ref2va 槽**:Singularity 只发布 ref2va 权重。实测它**也能跑**
`MiniMaxH3ImageToVideo`(t2v/fl2va 图,success,208.8s,峰值 80.0%),所以统一两个槽位在技术上可行;
但 `turbo_lora_fl2va` 是为 fl2va 底模训的,与 ref2va 血统的 Singularity 配对是**错配**
(作者推荐的是 `ref2v` 那条 LoRA)。要统一得先把"按镜头模式选 LoRA"改成"按底模血统选 LoRA",
那是另一件事,故本轮只换 ref2va 槽(生产里绝大多数镜头都是 ref2va)。

**代码侧配套**:`engine.go` 里质量档的量化守卫由 `strings.Contains(unet,"int8")` 改为
`isQuantizedBase(unet)`(排除 int8 / w4a8 / nvfp4 / mxfp8,`fp8_scaled` 保持原行为)——
否则选 quality 档时会把 Acc-8Step LoRA 挂到 w4a8 底模上(与 int8 同类的不兼容)。

**验证**:`scripts/check.ps1` 六道全过;重建后启动实测 `ManJuX v0.37.2`、
`[WebView2] Environment created successfully`、窗口就绪,`GET /api/settings` 返回
`unet_ref2va = Minimax-h3_Singularity_ref2va_Pruned_v1.3_int8.safetensors`。

**回滚**:`settings.json` 的 `unet_ref2va` 改回 `MiniMax_H3_ref2va_pruned_int8_convrot.safetensors`
(原文件未删,仍在 `shared/models/diffusion_models/`)。

### 2026-09-24:设置页「显存预留」步进与后端 `int` 类型不匹配(**已修复并重建**)

**症状**:设置页里「显存预留(GB)」的输入框步进是 `0.5`,点两下就能得到 `2.5`。
点保存时后端 `readBody` → `json.Unmarshal` 把 `2.5` 塞进 `ReserveVRAMGB int` 会报
`cannot unmarshal number 2.5 into Go struct field ... of type int`,接口返回 400,
**设置整份存不进去**;而该值一旦以任何方式落进 `settings.json`(手改、旧版本残留),
下次启动会在 `main.go` 的 `fatalIf(err, "加载设置失败")` 上**直接退出**。

**根因**:控件粒度(0.5)与存储类型(`int`,单位 GB)不是同一个契约。
审计过全应用:Go 数值字段里只有这一处不匹配 ——
`render.fade_sec` 是 `float64` 配 `:precision="1"`,
project 侧的 `qc.min_duration`/`max_duration`/`dark_ratio_*` 也都是 `float64`,均无问题。

**修复**(`frontend/src/views/SettingsView.vue`):该控件由
`:step="0.5"` 改为 `:step="1" :precision="0"`。
只改步进不够 —— `el-input-number` 默认允许**手输**小数,`:precision="0"` 才能把输入也
归整到整数(同文件 `fade_sec` 已有 `:precision="1"` 的先例,不是新引入的写法)。

**验证**:`scripts/check.ps1` 六道全过(gofmt / vet / build / test 10 包 / vite build /
go-webview2 补丁在位);`build.ps1` 重建后启动实测:日志出现
`[WebView2] Environment created successfully` 与 `[manjux] 桌面窗口就绪`,
窗口标题「ManJuX · 短视频创作工作台」,`GET /api/settings` 返回 `reserve_vram_gb=4`、
`novel_dir=D:\Ai\小说` 完好 —— 说明带 `int` 的 settings 读取链路正常。

**回滚**:源码 `git revert`,或直接用同目录的
`ManJuX-vramfix-rollback-20260924.exe.bak`(改名前那个 exe,去掉 `.bak` 即用)。

### 2026-09-24:渲染显存优化 —— `reserve_vram_gb` 1 → 4(**已生效**)

**问题**:视频渲染时显存长期顶在 92% 上下,最低只剩 ~1.5 GB 空闲,桌面/浏览器随时可能被挤。

**测量方法**(可复现):`D:\Ai\ComfyUI\_maintenance\20260924-upgrade\profile-vram.py`
按 ManJuX 同款启动参数拉起 ComfyUI,跑真实 H3 工作流,1 秒采样一次 `nvidia-smi`。
基准用 `api-workflows/h3-t2v-768p-crf16.json`(1344×768 / 124 帧 / 8 步),
与 `settings.json` 的 `render 768×1344 steps 8` 对得上。

**结论**(1344×768,各两次热态运行;**口径：无参考图**——带参考图的交付口径见文末注记):

| `reserve_vram_gb`(= `--vram-headroom`) | 峰值显存 | 峰值占比 | 最低空闲 | 单镜耗时 |
|---|---|---|---|---|
| **1(原值)** | 22,623 / 22,626 MiB | 92.5% | 1.51 GB | 181.2 / 190.2 s |
| 2 | 21,563 MiB | 88.1% | 2.58 GB | 60.3 s(832×480 口径) |
| 3 | 20,570 MiB | 84.1% | 3.57 GB | 60.3 s(832×480 口径) |
| **4(新值)** | 19,551 / 19,551 MiB | **79.9%** | **4.59 GB** | 181.2 / 184.0 s |
| 5 | 18,491 MiB | 75.6% | 5.65 GB | 63.3 s(832×480 口径)—— 开始掉速 |

**峰值与预留量近乎 1:1 线性**(多留 1 GB ≈ 少占 1 GB),而**到 4 GB 为止耗时完全不涨**
(181.2 s vs 181.2 s),5 GB 才开始掉约 5%。所以取 **4**:峰值 −3.0 GB、空闲 +3.0 GB、零速度代价。

> <span style="color:#e08b3c">**口径注记(2026-09-27)**：上表的耗时/显存是 **T2V 工作流**口径（`h3-t2v-768p-crf16.json`，
> 按构造就没有参考图），**不是 ref2va 交付口径**。带参考图的真实数字：**1344×768 / 124 帧 / 8 步 = 310 s、
> 峰值 22,186 MiB（90.7%）**（2026-09-27 实测，见
> `docs/ComfyUI侧交付-加速路线与显存回退-2026-09-27.md` §2）。**相对结论仍然成立**（峰值与预留量的
> 线性关系、4 GB 以内不掉速都是同条件对比），但**绝对耗时/峰值不得跨口径引用** —— 这正是那份交付单
> 新增的第 6 条判据。</span>

**改动**:`data/settings.json` 的 `comfy.reserve_vram_gb`: `1` → `4`
(UI 设置项「显存预留(GB)」,范围 0–8)。改前备份 `settings.json.bak-20260924-before-lora-v12`
(该备份同时是本日两次模型换装前的原始件)。

**顺带否掉的方案**(均实测,无收益或有害):

| 参数 | 实测峰值 | 结论 |
|---|---|---|
| `--reserve-vram 3` | 22,395 MiB(−0.19 GB) | 远不如 `--vram-headroom`,不用 |
| `--disable-comfy-compiler` | 21,371 MiB(单跑) | 与 headroom 并用时落在噪声内,不值得改 Go 源码重建 |
| `--fast-disk` | 22,556 MiB | 对显存无收益 |
| `--disable-pinned-memory` | 22,589 MiB | 管的是系统内存,不是显存 |

**注意事项**:本次只覆盖主渲染路径(条件编码 → 采样 → VAE 解码)。LTX 2× 隐空间放大 /
二采重采样属于更重的阶段且与主模型不共存(有 `--disable-smart-memory`),理论上有余量;
若日后发现那几个阶段变慢,把 `reserve_vram_gb` 降回 3 即可。

### 2026-09-24:ComfyUI v0.35.1 → v0.37.2 + 6 个插件同步升级(**升级已完成并保持**)

**动机**:本体落后上游 94 个提交(v0.35.2 / v0.36.0 / v0.37.0-2 三个版本没跟上),
而 H3 生态插件(Director +124、vrgamedevgirl +1056)已经在按新内核写代码 —— 继续钉在 v0.35.1
迟早出现「插件的新代码调用不到内核新接口」的运行期失败,属于必须一起动的那类。

**执行**:

1. **基线**:`D:\Ai\ComfyUI\_maintenance\20260924-upgrade\` 存 `baseline-repos.json`(全部仓库 HEAD)、
   `pip-freeze-before.txt`(134 行依赖快照);本体打 `backup_before_v0.37.2` 分支指向 `856a922b`。
2. **插件**:`git fetch` + `merge --ff-only`,逐个判断有无上游新提交。网络直连 github.com 不通
   (`curl` 走不通、`git` 亦不通),实际可用通路是 **系统代理 `127.0.0.1:7897`**(`Invoke-WebRequest`
   自动走、`curl`/`git` 不会),故对直连失败的仓库用 `git -c http.proxy=… fetch` 补拉
   (试过补 `gh-proxy` 的 `mirror` 远端,git 协议下会挂住,已放弃)。
   升级:KJNodes +1、audio-T8 +4、vrgamedevgirl +1056、Director +124、MaskVidExperiments +1、
   was-node-suite +15;其余 10 个本就最新。
3. **本体**:恢复被删的示例占位文件 → `git checkout v0.37.2`。
4. **依赖**:只装 `requirements.txt` 里真正变了的 5 个包(frontend 1.51.10→1.52.7、
   workflow-templates 0.11.59→0.11.69、embedded-docs 0.5.11→0.5.12、comfy-kitchen 0.2.33→0.2.35、
   comfy-aimdo 0.5.3→0.5.5)。**没有**跑整份 `pip install -r requirements.txt` ——
   那会把 `torch`/`torchvision`/`torchaudio` 按 PyPI 默认重装,极可能顶掉本机的
   `2.11.0+cu130` 专用轮子。
5. **配置**:`extra_model_paths.yaml` 里指向不存在的 `D:/Ai/ManJuX/comfyui/shared/` 那一段
   整段注释(见第九节)。

**升级后验证(逐项实测)**:

| 检查 | 结果 |
|---|---|
| 启动 | 10 秒内 ready(`/system_stats` 200),`[INFO] ComfyUI version: 0.37.2` |
| 节点导入 | 日志中 `Cannot import` / `IMPORT FAILED` / `Traceback` **零条** |
| 节点总数 | **2419** 个类(v0.35.1 时为 1929) |
| ManJuX 依赖节点 | 逐条核对 `preflight.go` 与 Go 源码里出现的 **44 个类,全部存在**(见下表) |
| 模型可见性 | `shared_current` 段 27 条搜索路径全部指向 `D:/Ai/ComfyUI/shared/models/*` |
| 资源 | `Total VRAM 24435 MB` / `pytorch 2.11.0+cu130`,与升级前一致 |

| 分组 | 节点 | 结果 |
|---|---|---|
| H3 核心 | ReferenceToVideo / ImageToVideo / SigmaShift | ✓ |
| 条件缓存 | CondSave / CondLoad | ✓ |
| 镜间接缝 | MotionContext × 4(含 LoadLatent / SaveLatent / Trim) | ✓ |
| SAM3 跟踪 | VideoTrack / TrackToMask | ✓ |
| Mask 处理 | MVEx SubjectCrop / SubjectUncrop / MaskToLatentSpace / LatentMaskToMask | ✓ |
| PDD 加速 | MiniMaxH3PDDAccApply | ✓ |
| 图放大链路 | LatentUpscaleModelLoader / UpscaleModelLoader / LTXVLatentUpsampler | ✓ |
| 基础 I/O | UNETLoader / CLIPLoader / VAELoader / LoadAudio / LoadVideo / VAEDecodeAudio 等 | ✓ |

**同批完成的模型换装(细节见第九节)**:

| 文件 | 从 | 到 |
|---|---|---|
| `loras/minimax_h3_fl2v_turbo_4step_*_768p_comfyui_bf16.safetensors` | v1.1 | **v1.2**(音频质量改善) |
| `vae/minimax_h3_video_vae_int8_convrot.safetensors` | 3,171,670,912 字节(旧量化) | **2,811,065,184 字节**(上游 2026-09-15 现行版) |

**已知副作用(不影响渲染,记录备查)**:v0.37 的素材库表结构迁移 `0007_record_content_split`
把旧素材库的**手工标签、用户元数据、预览图、重命名记录**清空重建了,旧库保留在
`ComfyUI/user/comfyui.db.bkp`。这只涉及 ComfyUI 新版「素材库」UI 的元数据,
与 ManJuX 的项目数据(`D:\Ai\Datas\ManJuX\data`)无关。

**回滚方式**:本体 `git -C D:\Ai\ComfyUI\ComfyUI checkout backup_before_v0.37.2`;
依赖按 `pip-freeze-before.txt` 逐条 `pip install <pin>`;插件按 `baseline-repos.json` 逐个
`git -C <目录> checkout <head>`;模型改回第九节列出的旧文件名即可(两个旧文件都留在原地)。

### 2026-09-24:Wails v2.15.0 → v2.16.0(**升级已完成并保持**)

**动机**:消除构建期一直存在的版本错位告警 ——
`Warning: go.mod is using Wails '2.15.0' but the CLI is 'v2.16.0'`。CLI 是 2026-09-23 装工具链时
用 `go install ...@latest` 拿到的 v2.16.0,而 `go.mod` 还停在 v2.15.0。官方口径是两者同步,
不同步时 CLI 生成绑定/资源的行为与运行时未必对得上,属于"平时没事、出事很难查"的那类隐患。

**执行**:`go get github.com/wailsapp/wails/v2@v2.16.0` + `go mod tidy`。
只动了 `go.mod`/`go.sum` 两个文件 —— `go mod tidy` 未改 `github.com/wailsapp/go-webview2 v1.0.22`
那一行,也就是说 `replace` 指向的本地 fork 基线版本正好就是 v2.16.0 所要求的,**补丁位点没变**。

**升级后验证(逐项实测)**:

| 检查 | 结果 |
|---|---|
| `go vet` / `go build` / `go test` | 全过(gofmt 与前端构建亦过,`scripts/check.ps1` 六道全绿) |
| 构建告警 | **已消失**(这正是本次升级的目标) |
| 前端绑定 | `wails build` 重新生成后 `git status` 无变化 → 2.16 生成的绑定与 2.15 完全一致,前端无需跟着改 |
| 窗口 | 0.7s 内出窗口,标题「ManJuX · 短视频创作工作台」 |
| WebView2 | 25 个 `msedgewebview2` 子进程 + 日志 `[WebView2] Environment created successfully` → 新 Go WebView2Loader 工作正常 |
| 界面渲染 | `PrintWindow(PW_RENDERFULLCONTENT)` 截图确认首屏正常:左侧导航、首页 hero、空态、底栏 COMFY/LLM/H3.API/FFMPEG 指示灯与版本号全部正确 |
| go-webview2 补丁 | 仍在(自检命令见 `third_party/README.md`) |

**回滚方式**:`go get github.com/wailsapp/wails/v2@v2.15.0` + `go mod tidy`,然后重跑 `scripts/check.ps1`。

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

## 九、放大模型(2026-09-12 已落地;2026-09-24 补齐 LTX 视频 VAE)

| 目录 | 文件 | 用途 | 大小 |
|---|---|---|---|
| `comfyui/shared/models/latent_upscale_models/` | `ltx-2-spatial-upscaler-x2-1.0.safetensors` | LTX 2× 隐空间放大 | 949.6 MB |
| `comfyui/shared/models/upscale_models/` | `4x-UltraSharp.safetensors` | RTX / ESRGAN 帧级放大(ManJuX 的 RTX 放大档) | 63.8 MB |
| `comfyui/shared/models/vae/` | `LTX2_video_vae_bf16.safetensors` | LTX 隐空间放大链路的**视频 VAE**(`VAEEncodeTiled`/`VAEDecodeTiled`) | 2331.7 MB |

前两个目录在 2026-09-11 建档时空缺,2026-09-12 就位;**第三个是 2026-09-24 才补上的** ——
在此之前 LTX 隐空间那条链路虽然代码与图契约测试都齐备,却因为缺这个 VAE 而永远跑不通
(报错要等 ComfyUI 执行到 `VAELoader` 才抛出来)。现在提交放大前会先做权重体检
(`UpscaleAssets`),缺文件会直接点名并给出目标目录。

**VAE 取哪一份**:同源仓库(Kijai/LTXV2_comfy)里还有个 `LTX2_video_vae_old_bf16`,
是作者注明"当初从蒸馏权重里抽错了、细节更差"的旧版;**正确且细节更好的是不带 `old` 的这个**。
判定该文件确实能被 ComfyUI 认成 LTX 视频 VAE 的依据:`sd.py` 的 `lightricks ltxv` 分支
判据键 `decoder.up_blocks.0.res_blocks.0.conv1.conv.weight` 命中、`latent_channels=128`、
带 `per_channel_statistics`(`LTXVLatentUpsampler` 必需)且 encoder/decoder 齐全。
注意官方 `Lightricks/LTX-2` 仓库里的 `vae/diffusion_pytorch_model.safetensors` 是
**diffusers 布局**,键名对不上 ComfyUI 的分支判据,不能直接丢进 `models/vae/`。

注意位置是 `comfyui/shared/models/`(**不是** `comfyui/ComfyUI/models/`),由
`comfyui/ComfyUI/extra_model_paths.yaml` 注册。该文件里原本有两段:`shared` 段的
base_path 是 `D:/Ai/ManJuX/comfyui/shared/`(部署形态下的路径),`shared_current` 段
指向 `D:/Ai/ComfyUI/shared/`(本机实际目录)—— ComfyUI 会合并多段加载,所以**两边都生效**。

> **2026-09-24 变更**:本机已不存在 `D:/Ai/ManJuX`(ManJuX 现在装在 `D:\Ai\Datas\ManJuX`,
> 其 `settings.json` 里 `comfy.root_dir` 直接就是 `D:\Ai\ComfyUI`,没有 `comfyui/shared` 这一层)。
> 那段旧的 `shared` 每次启动都会白扫 29 条无效搜索路径,已**整段注释保留**(不删除,便于复现部署形态),
> 现在**只有 `shared_current` 一段生效**,指向的仍是同一批模型文件,行为不变。

引擎层实现见 `internal/engine/comfyengine/upscalegraph.go`。

已就位的 LoRA(H3 加速链完整):

- `MiniMax-H3-FL2VA-Acc-8Step_pruned_comfy.safetensors`(quality 档)
- `MiniMax-H3-Ref2VA-Acc-8Step_pruned_comfy.safetensors`(quality 档)
- `minimax_h3_fl2v_turbo_4step_v1.2_768p_comfyui_bf16.safetensors`(turbo 档 · **当前生效**)
- `minimax_h3_ref2v_turbo_4step_v0.1_comfyui_bf16.safetensors`(turbo 档)

> **2026-09-24 换装**:fl2v turbo LoRA 由 v1.1 → **v1.2**
> (1,956,193,000 字节,sha256 `c8168ebc…`,与上游一致)。官方 v1.2 相对 v1.1 专门改善音频质量
> (更干净、伪影更少),步数/分辨率不变(4 步 / video shift 6 / audio shift 3 / Euler / ≤768p)。
> `settings.json` 的 `turbo_lora_fl2va` 已同步指向 v1.2;v1.1 文件保留在原地
> (回退就是改回那一行)。**注意 `ref2va` 无 v1.2**,4 步档最新仍是 v0.1。

### express 极速档的 int8 视频 VAE(2026-09-24 换装)

`vae/minimax_h3_video_vae_int8_convrot.safetensors` 由 **3,171,670,912 → 2,811,065,184 字节**
(sha256 `52a2c8c7…`)。本机那份是更早的另一种量化;Comfy-Org 于 2026-09-15 重新上传了现行版本。
换装后实测:文件被 `folder_paths` 正常列出、能经真实 `nodes.VAELoader.load_vae` 加载
(`latent_channels=24`、`latent_dim=3`、下采样 16×16)。
旧文件就地保留为 `….safetensors.bak-old-3171M` —— 扩展名不是 `.safetensors`,**不会被扫描进模型列表**
(已实测列表里没有它),要回退直接改回文件名即可。

> 该文件只影响 `express` 极速预览档(`engine.go:516`,有 fp16 回退);turbo / quality 档走的是
> `minimax_h3_video_vae_fp16`,未受影响。

### 2026-09-25:H3 提示词口径以**官方指南**为准(用户要求"参考全网找解决方案,别想当然")

**官方来源**(写代码改提示词前先查这三处):

| 用途 | 链接 |
|---|---|
| 基础模式(T2VA/I2VA/FL2VA/L2VA) | https://github.com/MiniMax-AI/MiniMax-H3/blob/main/skills/h3-prompt-writing/references/base-en.txt |
| 全参考模式(R2V,含音频标记表) | https://github.com/MiniMax-AI/MiniMax-H3/blob/main/skills/h3-prompt-writing/references/ref-en.txt |
| ComfyUI 官方提示词指南(含社区实测) | https://docs.comfy.org/zh/tutorials/video/minimax/minimax-h3-prompt-guide |
| 官方 skills(提示词技能包) | https://github.com/MiniMax-AI/MiniMax-H3/tree/main/skills |

**据此纠正/补齐的四件事**(前两件是我此前"想当然"改错的):

1. **画面文字不是"一律禁止",而是引号枚举制**(官方基础指南 §4.5):画面里可见的
   招牌/横幅/标签/字幕/霓虹文字都要放进**英文双引号**、逐字保留原字与标点、不翻译;
   ComfyUI 官方页记载的社区实测:"把每段文字都用引号枚举出来能保持文字清晰可读,
   **未定义的文字才容易变成乱码符号**"。→ 原先那句"画面内一律不得出现文字"已改为
   "该出现的必须引号逐字给出;引号之外不得有文字"。
2. **音频驱动必须带三个官方标记**(全参考指南 §2.4/§3/§4.2):`<Audio N>` 定义 +
   summary 前缀 `[audio reuse]` + retention 标记 `fully_copy`。ComfyUI 官方页的社区实测:
   **标记不正确时,H3 只把音频当音色参考,口型会与歌词脱节** —— 我们的音频驱动此前
   一个标记都没写,这正是「人物配音错误异常」的官方解。
3. **配乐与音景回到官方语义**:`non_diegetic_music` = 没有非叙事配乐时写 `N/A`,
   有则写乐器+速度+节奏+动态,**不用抽象情绪词**;`overall_soundscape` 官方是 **1-4 句**,
   只有"全片明确要求静音"才写 N/A。(把"每镜都配乐"压下去仍是用户诉求,做法是
   "不要每镜都配",而不是自造"大多数镜头没有配乐"这种统计。)
4. **说话人规范**(官方基础指南 §4.4):说话人用稳定 ID `(S1)`/`(S2)`,**跨镜头保持同一 ID**、
   从不发声者不给 ID、齐声用复合 ID `(S1,S2)`;声线身份短语放在 `<d>` **之前**、
   首次出现时给出一次(类型/年龄/性别/画内画外/音高/音色/语速/口音);台词跨剪切用
   `<scenetrans>`(两段都标 + 明说音频连续)、被片尾截断用 `<cutoff>`。
   —— `<scenetrans>`/`<cutoff>` 此前完全没用上,而它们直接决定"台词跨切听感"。
5. **台词的字词与标点分开管**(官方基础指南 §4.4 + 全参考指南 §5.4):**字词**逐字保留、
   不翻译不改写;**标点**是唯一例外 —— 统一成 `,` `.` `?` `!` 这类基本书写符号,去掉重复的
   波浪号 / emoji / 项目符号 / 装饰性标点,陈述句、疑问句、感叹句在 `</d>` 前分别以
   `.` `?` `!` 结尾;听不清的片段写 `[unclear]`,**不许猜也不许改写**。此前我写的是
   "保留原标点",与官方相反;而标点与不可辨片段正是"配音听着不对"的一个来源。
6. **音景句数回到官方区间,"别吵"改成取低端**(官方基础指南 §4.6 / §4.7):
   `overall_soundscape` = **1-4 句**、一段连续文字,只写环境声与物理/非语言人声,
   **不得重复台词、演唱与叙事内音乐**,只有"全片明确静音"才写 `N/A`;
   `non_diegetic_music` = **1-3 句**。用户"成片不能吵"的诉求写成
   **取区间低端(1-2 句、低电平底噪)**,而不是把官方区间私自改成 1-2 句
   —— 那是我上一轮的自造口径,已纠正。

**取证方式**:本机网络当前访问不到 huggingface.co(请求全部 TypeError),因此官方指南一律
改引**本机实测取到正文**的 GitHub 原始路径(两次拉取均 HTTP 200);HF 侧只保留模型仓库页,
不再指向具体文件。`internal/pipeline/official_guide_test.go` 的 `TestCitedGuideURLsAreVerified`
把这个约束钉住了:提示词权威相关的 URL 必须落在已验证清单内,冒出新的未验证链接会让
`check.ps1` 直接挂掉。

### 2026-09-25:角色渲染漂移的根因 —— 提示词在描述它看不见的参考图张数

**现象**(用户报「角色渲染咋出现漂移啊,看看镜头6主角形象」):第01集镜头6,主角顾青禾的成品脸
比定妆照更窄更长、眉毛更重、更年长;而定妆照上**没有画**的细节(右眉尾浅疤、皮筋外那枚黄铜"禾"扣)
反而被画了出来 —— 模型转去照着**文字**重画,而不是照着那张脸复制。

**取证**:`data/logs/manjux.log` 实测记录 ——

```
镜头 6/33 渲染开始(ref2va,7s,seed=4223164940,参考图 3 张:角色 2 + 其中拼合转身图 1 + 场景 1)
```

而 plan 里的 subject_definitions 写的是「Two pictures are attached in total, one character front
sheet and the scene」,并把 `<Picture 2>` 说成"场景图"。**实际第 2 张是顾青禾自己的拼合转身图**,
第 3 张才是场景。于是那一行 `<Picture 2>: weak_reference - scene layout and mood referenced,
camera free` 正好落在**她自己的脸上**:模型据此拿到"这张脸可以自由重画"的许可,真正的场景图
成了无标签野图,定妆照"唯一身份权威"的地位被稀释。

**根因**:张数取决于该角色有没有拼合转身图,而这一步只有渲染器知道(见 `RefPlan`),
LLM 只能猜 —— 多视角一开,每个镜头都必然多一张,猜错就把编号整体错位。
`refs.go` 早就写明"少一张图会让编号整体前移……",但只在**缺**图时告警,**多**一张反而没人管。

**修法**(与既有"同一职责收口"同一条思路:**谁装配参考图,谁写编号**):
1. `RefPlan` 取代 `AssembleShotRefs` 作为调用面,额外给出每张图的身份归属
   (`character_front` / `character_angles` / `character_front_in_strip` / `scene`);
   `engine.RefImage.Kind` 保持 `character`/`scene` 不变(引擎语义不动)。
2. `refmanifest.go` 的 `RefManifest` 按实际装配结果写出**权威清单**附在提示词尾部,
   明确声明它**覆盖**上文任何张数/编号的说法,并写死:拼合图是同一个角色的另一组角度、
   不是场景、不参与取脸(脸只认定妆照)。
3. 导演提示词同时被要求**不得断言总张数、不得断言场景是第几张** —— 它看不见的东西不写。
4. 四条 ref2va 出像素路径(主渲染 / 单镜 2K / 局部重绘 / 音频驱动)全部成对装配清单。

### 2026-09-25:图像生成弃用 Krea-2,换 Qwen-Image-2.1(全网核对后的选型)

用户定调:「Krea-2 出图这么多问题,那就不用它生图,另外全网找最优的解决方案」。

**先说我为什么认为 Krea-2 该退役**(都有实测或官方依据,不是感觉):
1. 定妆照**漏画卡片要求的身份记号** —— 顾青禾卡片写了「右眉尾一道浅疤」「皮筋外套一枚拇指盖大的
   黄铜扣,扣面刻极小的'禾'字」,出图上两样都没有;脸型也偏软偏圆,与卡片写的
   「窄长鹅蛋脸尖下巴、麦色晒黑皮肤」不符。
2. 社区已知的**编码器改写提示词**问题:Krea-2 用的是 Qwen3-VL **对齐版**文本编码器,
   社区记录它会"改写/忽略提示词的一部分",对应的解法是换 abliterated 编码器(安装级替换)。
   出处:<https://github.com/charliecpeterson/comfyui_mcp/blob/main/skills/prompt-krea2/references/model-variants.md>
3. 我们此前的采样档还是**自选**的 `euler/normal`;同一份记录里官方倾向是 `er_sde/simple`。

**选型对比(依据见下)**:

| 候选 | 文本遵循 / 中文 | 生成+编辑 | 体积 | 结论 |
|---|---|---|---|---|
| Krea-2 Turbo | 编码器会改写指令 | 否 | 12B fp8 | 退役(保留可选,兼容存量项目) |
| Z-Image Turbo | 文字可读,偏"审美平滑"、简化背景 | 否 | 6B | **兜底**:权重已在本机(零下载) |
| **Qwen-Image-2.1** | 生成与编辑同一权重,中文文字精度最高 | **是** | 7B int8 | **新默认** |

依据:
- ComfyUI 官方博客《Qwen-Image-2.1 in ComfyUI:Open-Weight Image Generation and Editing, Now with
  Transparency》(7B / MMDiT / **原生 2K** / **RGBA** / 最多 **10 张参考图** / **生成与编辑同一个权重**)
  <https://blog.comfy.org/p/qwen-image-21-in-comfyui-open-weight>
- 《Qwen-Image-2512 vs. Z-Image Turbo:5-Prompt Benchmark》:指令遵循上 Qwen 更"字面",
  Z-Image 会给一层"审美平滑";文字渲染上两者都能出字,但 Qwen 能**精确拼出指定字符串与标点**
  <https://qwen-image-2512.com/blog/qwen-image-2512-vs-turbo-benchmark>
- **本机 ComfyUI 自带的官方工作流模板**是连线的权威来源(离线可查,不依赖网络):
  `comfyui_workflow_templates_json/templates/image_qwen_image_2_1_t2i.json`
  → UNETLoader(`qwen_image_2.1_int8_convrot.safetensors`)+ CLIPLoader(**type=`qwen_image`**,
  `qwen3vl_8b_int8_convrot.safetensors`)+ VAELoader(`qwen_image_2.1_vae_bf16.safetensors`)+
  **TextEncodeQwenImage21**(单节点出 positive/negative)+ EmptyLatentImage + KSampler
  (**25 步 / cfg 1 / euler / simple**)+ VAEDecode;
  `image_z_image_turbo.json` → CLIPLoader type=`lumina2`(`qwen_3_4b`)+ `ae.safetensors` +
  **ModelSamplingAuraFlow shift=3** + 8 步 `res_multistep/simple`。

**取证与落地**:
- 权重走 **ModelScope**(HF 本机不可达),仓库 `Comfy-Org/Qwen-Image-2.1`,int8_convrot 组合
  共约 16.2 GB(unet 6.76 GB + 编码器 8.71 GB + VAE 0.63 GB)。下载脚本
  `scripts/dev/fetch-qwen-image-2-1.ps1`:先读 ModelScope API 拿**精确字节数**,再 `curl -C -`
  断点续传,收尾逐文件比对大小(截断会被判失败),日志 `%TEMP%\vis\qwen21.log`。
- 装配收口:新增 `internal/engine/comfyengine/imagefamily.go` —— **一族一行**(编码器类型 /
  采样档 / 是否要位移节点 / 是否用 2.1 的单条件节点),`imagegen.go` 只读表拼图。
  换模型从此是改表,不是改代码。
- `config.image_family` 新字段 + 默认值改为 Qwen-Image-2.1;存量 settings.json 里
  三件套仍是**旧 Krea-2 默认**且没写过 family 时自动迁移,用户手工换过就一个字不动。
- 闸门 `imagefamily_test.go`:config 默认与常数一致(两处字面量必须同步)、
  键名推断、四族连线逐条断言(qwen 2.1 的 25 步/euler/simple/单条件节点/竖幅空 latent、
  Z-Image 的 lumina2 + AuraShift 3 + res_multistep、Krea-2 的 type=krea2 + er_sde/simple)、
  img2img 仍走 LoadImage→VAEEncode。

### 2026-09-25:外貌漂移「19 镜后面主角的头发飘逸了」——文字对、画面漂

**现象**(用户报):第01集从第 19 镜起,主角顾青禾的头发变长变散、额前垂下一大绺;
马尾上的黄铜「禾」扣不见;夹克下面那件背心被简化成一件深色内搭。

**取证**:
1. **逐镜核对提示词**:26 镜**每一镜都写着** `short low ponytail tied with a worn rubber
   band and held by a thumb-sized brass ring engraved with a tiny almost-worn "禾" character`、
   `reflective strip`。文字没有任何一镜写错 —— 所以这不是"LLM 写飘了"。
2. **抽帧对比**(定妆照 vs 镜20/22):定妆照是短低马尾 + 黄铜扣 + 夹克下压背心;
   镜20/22 三样全变。漂移方向是"丢掉装饰",不是"换成另一个人":脸型、眼睛、腕环都还在。
3. **与参考图张数无关**:日志显示镜16-19、21-25 是 2 张参考图(定妆照+场景),
   镜20/26 是 3 张(多一张拼合转身图)—— **两边都漂**,所以不是上一轮"近景/特写不附拼合图"
   改出来的。真正的规律是**画面一忙就丢装饰**:19 镜起剧情进入折纸、搬箱、甩上箱子的动作段。

**修法**:渲染侧身份锚定新增【外貌不变项(逐帧核对,不得随剧情放松)】—— 把外貌从"形容词"
提升为"身份事实",三条各对应一种真实出现过的漂移:
- **发长**:参考图上是短发就是短发,不得随时间变长、变散、变成飘动的长发;
- **配件**:角色卡写到的每一件都要在参考图显示它的位置上、以同样的颜色与材质出现,
  不许因为画面太满就拿掉;
- **服装层数**:参考图几层就是几层,不许简化成一件,不许把背心画成内搭。

只对有出场角色的镜头注入(纯空镜没有"外貌"可锁),幂等只出现一次。

**配套工具**:`cmd/renderone` —— 走生产同一条 `Coordinator.Start`(带 only 过滤)只重渲指定
镜头。改一次提示词要验证时,整集重跑 30 分钟、单镜 7 分钟（**无参考图口径**；带参考图的 ref2va
实测 **310 s / 124 帧 / 8 步**，即约 5 分钟一镜，`docs/ComfyUI侧交付-加速路线与显存回退-2026-09-27.md` §2）。

**运维坑(实测)**:桌面端**退出时会杀掉全部 ComfyUI 进程**("退出:停止全部管线并结束
ComfyUI 全部进程")。所以用 `renderone` 做无头重渲时,别同时开着/关掉那个 app ——
20:19 那一枪就是这么废的:渲染进行到 2 分钟时 app 被退出,ComfyUI 被一起杀了。

---

## 2026-09-26 · 定妆照与成片"不在同一个视觉世界"(漂移的真根因:风格归属)

### 现象(真机端到端实测,求你们骂我·第01集)

跑通导入即开跑之后,连续看到两件"各自都对、合起来错"的事:

| 环节 | 实际产出 |
|---|---|
| 技能端 plan.style | `PHOTOREAL 3D CINEMATIC WORLD, SEMI-STYLISED ANIME-ADJACENT FACES, matte finish` |
| 定妆照(characters_char_char_01.png) | **二维赛璐璐动画脸**(平涂、干净线稿) |
| 成片(clips/shot_001.mp4 抽帧) | **3D 半写实 CG**(真实皮肤材质、体积光) |

视频侧按 `styleAndMediumBlock` 的 STYLE AUTHORITY 忠实执行了脚本风格;定妆照侧却被
`styleExpand` 里一支老分支(`facesOn && photorealRequested`)覆盖成"Chinese donghua …
clean cel shading"。于是**参考图与渲染不在同一个视觉世界**:H3 拿一张二维平涂脸去渲染
3D 写实人物,身份只能靠猜 —— 用户长期报的"脸/头发漂移"正是这条链的末端。

### 修法

- 上游风格点名写实/3D/真人时,**画风归上游**,镜头动漫化开关不再覆盖
  (`prompts.go::styleExpand` 首支改为 `styleIsPhotoreal`;同时**不再叠加** `SheetLook`
  动画介质块 —— 同一段提示词里两个介质打架,模型只会二选一地糊);
- 保留法务底线:任何风格下都写明"不得像真人/演员/公众人物";
- 开关在**上游没给风格**时仍然决定兜底画风(2026-09-24 的"不要真人脸"没有被推翻);
- 测试:`TestStyleExpandKeepsUpstreamPhotorealWhenFacesOn`(用真机那条风格串原样钉住)
  + `TestStyleExpandAnimeFallbackStillApplies`(兜底未被改坏)。

### 真机验证

清掉被污染的定妆照与资产库条目(备份在 `%TEMP%\manjux_stylefix_backup_*`)、重编译、
再次导入后,同一角色重新出图:**semi-realistic 3D CG**——材质、皮肤、布料的写实度与成片
一致,袖口卷折、粗布鞋、胳膊上的新口子等简报细节全部在位。参考图从此与成片同介质。

### 顺带修掉的两处"日志造假"(用户就此报过两次)

- `Qwen-Image-2.1` 跑的图,日志写"Krea-2 生成" → 改从当前家族取;新增结构性闸门
  `TestNoHardcodedModelNameInUserFacingStrings`(变异探针已验证会变红)。
- `✔ 已生成:场景「临水城」(人)` → 场景没有实体类型,零值 fallback 成了"人";
  改为只对角色挂类型(`entityKindSuffix`)。

### 操作提醒

**换风格/换模型后,旧定妆照必须清理**:资产库按"名字+类型"复用,不看提示词 —
不清理的话,新的风格约束出来了也会被旧的二维定妆照直接复用掉(本次即如此:
库、项目 assets 两处都要清)。
