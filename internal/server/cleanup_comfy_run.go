package server

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cleanup_comfy_run.go ComfyUI **运行时目录**的整扫（input / output）。
//
// 【2026-09-27 用户指示】「一键清理 还需要清理 D:\Ai\ComfyUI\ComfyUI\input 和 output 这两个目录」。
//
// 此前只清两样：`output/manjux/` 与 input 里 **24 小时前的图片** ⇒ ComfyUI 自己出的图、
// 别的扩展名（.json/.mp4/latent）、近 24 小时的副本全部留着 —— 用户看到的是"点了清理，
// 这两个目录还占着几个 G"。这里改成**整目录扫**，但仍保留三条安全边界：
//
//	① 只删 `cut` 之前的文件：正在跑的那一镜刚上传/刚写出的东西不能动；
//	② input 的子目录**不动**（旧决定：可能是用户自己放的素材）；output 的子目录按同规则清文件；
//	③ 文件清空后顺手删掉**空目录**（不占空间，但会让人以为还有东西）。
//
// 为什么单列一个文件：这两条规则是"用户要求的清理范围"，与上面那些"平台自己的产物"不同类，
// 混在 400 行的 handler 里下次改范围又要重新读一遍上下文。
// busy 为真（本机还有管线在跑）时，最后那条 `output/manjux` 整删**跳过**并如实报出 ——
// 那一片正是正在渲染的那一镜的产物落点（2026-09-30 深度审计）。
func sweepRunDir(dir, kind string, cut time.Time, busy bool) (n int, size int64, skippedManjux bool) {
	if strings.TrimSpace(dir) == "" {
		return 0, 0, false
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return 0, 0, false
	}
	// 平台自己的产物目录（output/manjux）整删：没有回收价值。
	//
	// 【2026-09-30 深度审计】**管线在跑时不能整删**：这一片是本次渲染的产物落点 ——
	// 引擎提交时写 `filename_prefix: manjux/shot_…`（还有 manjux/<id>_seam、manjux/edit、
	// 放大的 manjux/<prefix>），完成后**立刻**按 `GET /view?subfolder=manjux` 取回来。
	// 它原来连 `cut` 都不看（无条件 RemoveAll），于是"渲染到一半点了一键清理"的症状是：
	// 取产物时 404 → 报「产物下载失败」，那一镜的 3–5 分钟 GPU 白烧，而报出来的原因
	// 完全对不上（用户看到的是"下载失败"，不是"清理删了它"）。
	// 跳过要**如实播报**：清理是用户的显式动作，默默少清等于让他以为清干净了。
	manjux := filepath.Join(dir, "manjux")
	if fi, err := os.Stat(manjux); err == nil && fi.IsDir() {
		if busy {
			skippedManjux = true
		} else {
			_ = filepath.WalkDir(manjux, func(p string, e os.DirEntry, err error) error {
				if err != nil || e.IsDir() {
					return nil
				}
				if info, e2 := e.Info(); e2 == nil {
					n++
					size += info.Size()
				}
				return nil
			})
			_ = os.RemoveAll(manjux)
		}
	}

	subdirs := map[string]bool{} // 记下见过的子目录，收尾时删空的
	_ = filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err != nil || p == dir {
			return nil
		}
		if e.IsDir() {
			if p == manjux {
				// 平台自己的产物目录在上面单独处理过（busy 时是**故意不碰**）——
				// 这里必须连里面都不进：否则下方"按 1 小时界限清文件"照样会删掉
				// 正在渲染的那一镜的产物，跳过就白跳了。
				return filepath.SkipDir
			}
			if kind == "input" {
				// input 的子目录一律不动（可能是用户自己的素材）——用 SkipDir 连里面都不进
				return filepath.SkipDir
			}
			subdirs[p] = true
			return nil
		}
		info, ierr := e.Info()
		if ierr != nil || info.ModTime().After(cut) {
			return nil // 还在用：不动
		}
		if os.Remove(p) == nil {
			n++
			size += info.Size()
		}
		return nil
	})

	// 收尾：删掉空目录（深的先删）
	dirs := make([]string, 0, len(subdirs))
	for d := range subdirs {
		dirs = append(dirs, d)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i]) // 非空会失败，正是我们想要的
	}
	return n, size, skippedManjux
}
