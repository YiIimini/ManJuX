package project

import (
	"os"
	"path/filepath"
)

// 成片路径的**唯一真源**。
//
// 【为什么要收口（2026-09-28 全面审计 · 用户：「交付的逻辑搞的跟个啥一样」）】
// 这条判据此前散落在四处，各写各的 `os.Stat(output/final.mp4)`：
//
//	server/handlers_project.go   项目接口的"成片就绪"标记
//	server/outputs.go            作品库列表
//	server/novel_split.go        批量产线的"断点续跑跳过"
//	pipeline/upgrade2k.go        2K 升级的输入选择
//
// 散落本身还不是最要命的 —— 最要命的是**管线自己从来没用过它**：
// `execute` 末尾只要各阶段没返回 error 就报「✔ 全流程完成」，于是"跑完了但没成片"
// 会被当成完成，批量产线据此推进下一集，用户最后看到一堆"完成"却没有片子。
// 现在统一从这里取，管线收尾也必须查它。

// FinalNames 成片的两种出口。768P 在前：它先产出，2K 是它的升级版。
var FinalNames = []string{"final.mp4", "final_2k.mp4"}

// DeliveredFinal 项目里是否已有成片；返回命中的文件（相对项目根，slash 分隔）与是否存在。
//
// 判据：存在、不是目录、且**大小 > 0**。0 字节的 final.mp4 是"合成写到一半就没了"，
// 把它当成成片会让批量产线跳过整整一集（最坏的一种假完成：跳过的集再也不会被跑）。
func DeliveredFinal(dir string) (string, bool) {
	for _, name := range FinalNames {
		rel := filepath.Join("output", name)
		st, err := os.Stat(filepath.Join(dir, rel))
		if err == nil && !st.IsDir() && st.Size() > 0 {
			return filepath.ToSlash(rel), true
		}
	}
	return "", false
}
