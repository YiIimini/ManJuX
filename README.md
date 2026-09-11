# ManJuX · 短视频创作工作台

> 从一段文字,到一部成片 —— 桌面级 AI 短视频生产线。
> 技术栈:Go(Wails v2 桌面框架)+ Vue 3 + Element Plus + Tailwind。

![ManJuX](build/appicon.png)

## 它能做什么

输入故事/创意文本 → **LLM 直出分镜与 H3 提示词** → 参考图资产 → **双引擎渲染**(本地 ComfyUI H3-Base / MiniMax H3 云端 API,音画一体直出)→ **机械质检**(黑屏防线)→ **合成成片**(字幕/响度/faststart)。

五阶段管线,每阶段可**独立运行、独立调参**(项目级参数快照),支持断点续跑、单镜重渲。

| 阶段 | 能力 |
|---|---|
| 剧本分镜 | LLM 直出角色卡/场景卡/分镜表 + 每镜完整 H3 提示词(Ref2VA 六段式/T2VA 三段式,对白原生配音);**镜数由内容决定、不设上限**(内容密的一章可正当需要 15-30 镜,不压并事件);思考型模型兼容(JSON mode 自动降级) |
| 资产定妆 | **Krea-2 自动生成**(角色 832×1248 半身/场景按画幅,~10s/张)/ 手动上传 / 跳过三级策略;已有资产自动复用;**角色自动补多视角转台图**(定妆照→H3 原地转身视频→抽帧拼接,渲染时作该角色的第二张参考) |
| 渲染出片 | **双引擎**:本地 ComfyUI(768p、Turbo LoRA、两阶段条件缓存·换图自动失效)/ 云端 MiniMax-H3 API(768P/2K);auto 本地优先自动切换;**镜头级引擎分派**;单镜失败不中断整批(断点续跑);**镜头级增强**:局部重绘(只重生成崩坏区域,其余画面逐帧保留)/ 音频驱动(配音驱动重渲,口型与节奏跟随音频) |
| 质检 | 时长/音轨/分辨率探测 + 抽帧亮度统计(黑屏防线);全流程中不阻断合成 |
| 合成成片 | 重编码拼接(硬切/闪黑/叠化 xfade)+ ASS 字幕(画幅自适应)+ loudnorm 响度(48kHz)+ faststart;**云端 2K 定稿**(regeneration,单镜/成片一键或自动) |

**桌面体验**:Wails 原生窗口(单实例)、渲染中关窗二次确认、实时 SSE 日志流、设置页一键测试连接(LLM/Comfy/MiniMax/VLM)。

**v0.4 扩展**:
- **跨项目资产库** —— 角色/场景一次定妆全局复用(Krea-2 生成与手动上传自动入库、同名秒级复用、复用计数、「资产库」页管理);系列作品天然形象一致;
- **VLM 智能审片** —— 抽帧送视觉大模型判分(默认智谱 glm-4.6v-flash 免费),输出问题清单与重渲建议,机械质检之上加内容级防线;
- **镜间连续接缝** —— MotionContext 22 帧钉入(项目级开关,仅本地引擎;上一镜无存档自动降级,云端/本地混合链可安全使用);
- **剪辑素材包导出** —— 按镜命名片段 + 成片 + 对齐 SRT + 导入说明一键打包(剪映友好,任意版本可导)。

**v0.30 能力升级**(详见 docs/):
- **整本小说批量产线** —— 小说库(目录导入/封面/阅读)→ 一键拆集(异步任务,提交即见占位卡,逐集实时刷新)→「书名·第NN集」子项目 → 批量产线逐集推进;按书分级管理 + 剧集弹窗单集操作;断点续跑三层保障(全流程跳过已完阶段/指纹记配置/批量跳过已成片集);
- **系列角色一致性** —— 后集分镜自动注入前集已确立的角色名与形象设定(书级 roster),配合资产库同名复用与角色锚定种子,跨集不换脸;
- **ComfyUI 引擎治理** —— 停止/退出全进程树杀(gopsutil 扫描,零残留)、空闲自动退出、代管 PID 持久化重启自动接管、版本管理与在线对比;
- **桌面体验** —— 运行日志悬浮窗(可拖动/调大小/一键停止管线)、批量管理全选/取消全选、图片上传拖放双通道(带缩略图预览)、六主题玻璃界面;
- **稳定性** —— WebView2 渲染期闪退根修(禁 webview GPU)、上传管线防截断(JSON+base64)、Tip 气泡最高层、合成 dissolve 转场修复、全子进程无黑窗。

**v0.34 能力升级**(详见 docs/):
- **拆集规则回归 1:1** —— **一章正好一集、集数=章数**,集名直接取该章标题原文(代码兜底强制覆盖 LLM 自起的文艺标题);镜数按内容给足、不设上限,不再靠「多拆集」换细度;
- **角色多视角转台图** —— 定妆照生成后自动用 H3 Ref2V 出「原地转身」短视频,抽 4 帧横向拼成转台图,渲染时自动作为该角色的第二张参考(仅在文件存在时追加,老项目参考图组成与渲染指纹不变);
- **镜头级增强** —— **局部重绘**(SAM3 跨帧跟踪目标 → 裁出 → H3 重绘 → 贴回原帧,只重生成手部/道具/人脸等崩坏区域,其余画面逐帧保留)与**音频驱动**(喂入定稿配音,H3 原生 `ref_audios` 条件驱动重渲,口型与节奏跟随音频);
- **资产库详情与重抽** —— 点击库卡片看该资产的**全部图片**(定妆照 + 转台图等关联图,存在 `_view_*` 多视角原图时一并收进来,每张带用途标签)、中文描述、英文提示词、来源与复用次数,可「重抽」(同角色换一张)或删除;
- **渲染前节点预检** —— 渲染开始前校验本次用到的 H3 根节点是否齐备(缺则明确阻止并给中文提示,不再跑到一半炸 `node_errors`),并检测已知冲突插件(只告警不阻断);
- **2× 放大后处理(引擎层已就绪)** —— LTX 隐空间 2×(含 8n+1 时间轴补齐、裁回原帧防时长漂移)与 ESRGAN 帧级 2× 两条链路,均保留源音轨;模型已就位(`ltx-2-spatial-upscaler-x2-1.0` / `4x-UltraSharp`),**界面入口尚未接入**;
- **ComfyUI v0.35.1** —— 含 `Fix MiniMax H3 denoise masks`(局部重绘依赖的底层修复);新增 MaskVidExperiments、comfyui-vrgamedevgirl 插件;
- **工程改造** —— 三个千行文件拆分(`pipeline.go`/`server.go`/`novels.go`)、引擎接口收口(`FreeVRAM`/`ClearQueue`/`Interrupt`/`GenerateImage` 提升进 `engine.Engine`)、前端请求全部收口到 api 层、测试函数 12→64。

## 快速开始

1. **双击 `ManJuX.exe`** —— 弹出桌面工作台窗口(单实例,重复启动自动唤起已有窗口)。
2. **设置**(右上角):填写 LLM(OpenAI 兼容,推荐 DeepSeek)与 MiniMax API Key;本地 ComfyUI 路径默认已适配本仓库 `comfyui/` 目录。
3. **新建项目** → 剧本页粘贴故事文本 → 「一键全流程」或逐阶段运行。
4. 渲染与合成进度在底部 **RUNTIME LOG** 实时滚动;分镜故事板逐镜点亮;成片页预览/导出。

## 目录规范(严谨,不乱放)

```
D:\Ai\ManJuX\
├── ManJuX.exe              # 交付桌面应用(构建产物副本)
├── comfyui\                # 本地 ComfyUI 完整环境(134G:模型+Python,不入库)
├── data\                   # 运行数据(不入库)
│   ├── settings.json       # 全局设置(API Key 等)
│   ├── logs\               # 应用日志 manjux.log / comfyui.log
│   ├── cond_cache\         # H3 条件缓存记账(清缓存时连 ComfyUI 侧 models/conditioning/*.pt 一并删,防磁盘堆积)
│   └── projects\<项目>\
│       ├── project.json    # 项目元数据 + 阶段参数(独立调优)
│       ├── source.txt      # 源文本
│       ├── plan\plan.json  # LLM 直出分镜方案
│       ├── assets\         # characters/scenes/props + asset_map.json
│       ├── clips\          # 镜头产物 NN.mp4 + manifest.json(断点/时效)
│       └── output\         # final.mp4 / final.srt / qc_report.json
├── main.go                 # Wails 桌面入口
├── internal\               # Go 模块(见 docs/架构.md)
├── frontend\               # Vue3 前端源码
├── build\                  # wails 图标/构建产物
├── scripts\                # build.ps1 / release.ps1
└── docs\                   # 文档
```

## 构建与发布

```powershell
# 日常构建(前端+桌面 exe)
powershell -ExecutionPolicy Bypass -File scripts\build.ps1

# 发布(自动构建 → commit → tag → 推送 GitHub 私有仓)
powershell -ExecutionPolicy Bypass -File scripts\release.ps1 -Note "说明"
```

依赖:Go 1.25+、Wails CLI(`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)、Node+pnpm、ffmpeg(PATH)。

## 环境要求

- 本地渲染:ComfyUI(本仓库 `comfyui/` 即完整环境,含 MiniMax-H3 int8 模型);应用可代管启动。
- 云端渲染:MiniMax 开放平台 API Key([platform.minimaxi.com](https://platform.minimaxi.com))。
- 剧本生成:任意 OpenAI 兼容 LLM(DeepSeek/GLM/Qwen/Kimi/本地 vLLM 均可)。

## 更多文档

- [docs/架构.md](docs/架构.md) —— 模块划分、引擎抽象、管线编排
- [docs/使用手册.md](docs/使用手册.md) —— 界面操作、阶段调优、常见问题
