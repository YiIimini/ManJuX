package server

import "github.com/YiIimini/ManJuX/internal/engine/comfyengine"

// ComfyUI 目录布局解析(薄委托)。
//
// 实现已收口到 `comfyengine.ModelDirs / RunDirs` —— 同一件"模型放在哪"的知识
// 曾在清理、`modelFileExists`、管理页三处各写一遍且互不一致(清理漏了 shared 布局
// 导致 17.4GB 清不掉;modelFileExists 漏了同一布局导致 express/quality 档静默降级)。
// 这里只保留包内调用名,避免清理逻辑到处写包名。

func comfyModelDirs(rootDir, sub string) []string { return comfyengine.ModelDirs(rootDir, sub) }

func comfyRunDirs(rootDir, name string) []string { return comfyengine.RunDirs(rootDir, name) }
