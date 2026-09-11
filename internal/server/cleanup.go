// cleanup.go 一键清理:扫描并删除运行垃圾(不影响任何项目数据/成片)。
// 清理范围:①ComfyUI output/manjux 中的上传副本与中间产物(定妆照上传的
// asset_*、渲染中间 shot_* .mp4、接缝 latent seam_*.safetensors)——已有
// 项目成片在项目自己的 output/ 目录,不受影响;②项目内失败合成现场
// (output/compose-*failed);③孤儿 cond_cache 子目录(项目已删但缓存
// 残留);④系统临时文件(mx-* 前缀)。
package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type cleanupResult struct {
	Items   []string `json:"items"`
	FreedMB float64  `json:"freedMB"`
}

func (s *Server) handleCleanup(w http.ResponseWriter, r *http.Request) {
	res := &cleanupResult{Items: []string{}}
	add := func(label string, size int64) {
		res.Items = append(res.Items, label)
		res.FreedMB += float64(size) / 1024 / 1024
	}

	// ① ComfyUI output/manjux 中的中间产物(不影响项目 output/ 里的成片)
	g := s.cfg.Get()
	manjuxOut := filepath.Join(g.Comfy.RootDir, "ComfyUI", "output", "manjux")
	if st, err := os.Stat(manjuxOut); err == nil && st.IsDir() {
		var size int64
		count := 0
		filepath.WalkDir(manjuxOut, func(p string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil
			}
			if fi, e2 := e.Info(); e2 == nil {
				size += fi.Size()
			}
			count++
			return nil
		})
		if count > 0 {
			if err := os.RemoveAll(manjuxOut); err == nil {
				add(fmt.Sprintf("ComfyUI 中间产物 %d 个文件", count), size)
			}
		}
	}

	// ② 项目内失败合成现场(compose-*failed)与截帧预览(_frames)
	var failedCount, frameCount int
	var failedSize int64
	for _, p := range s.store.List() {
		outDir := filepath.Join(s.store.Dir(p.ID), "output")
		if ents, err := os.ReadDir(outDir); err == nil {
			for _, e := range ents {
				if e.IsDir() && strings.HasPrefix(e.Name(), "compose-") && strings.HasSuffix(e.Name(), "-failed") {
					full := filepath.Join(outDir, e.Name())
					if st, _ := e.Info(); st != nil {
						failedSize += st.Size()
					}
					failedCount++
					_ = os.RemoveAll(full)
				}
			}
		}
		frames := filepath.Join(s.store.Dir(p.ID), "clips", "_frames")
		if st, err := os.Stat(frames); err == nil && st.IsDir() {
			_ = os.RemoveAll(frames)
			frameCount++
		}
	}
	if failedCount > 0 {
		add(fmt.Sprintf("失败合成现场 %d 个", failedCount), failedSize)
	}
	if frameCount > 0 {
		add(fmt.Sprintf("截帧预览缓存 %d 个", frameCount), 0)
	}

	// ③ 孤儿 cond_cache(项目已删但 cond_cache/{id}/ 残留)
	condDir := filepath.Join(s.dataDir(), "cond_cache")
	if ents, err := os.ReadDir(condDir); err == nil {
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			// 项目 ID = 目录名去掉 "manjux_" 前缀
			projID := strings.TrimPrefix(e.Name(), "manjux_")
			if _, exists := s.store.Get(projID); !exists {
				full := filepath.Join(condDir, e.Name())
				var size int64
				filepath.WalkDir(full, func(p string, en os.DirEntry, err error) error {
					if err == nil && !en.IsDir() {
						if fi, e2 := en.Info(); e2 == nil {
							size += fi.Size()
						}
					}
					return nil
				})
				_ = os.RemoveAll(full)
				add(fmt.Sprintf("孤儿条件缓存「%s」", projID), size)
			}
		}
	}

	// ⑤ 条件缓存**数据文件**(ComfyUI 侧 models/conditioning/*.pt)。
	// ③ 只清了 sidecar 记账,而 .pt 数据在 ComfyUI 侧、文件名又只有 s{shot}_{hash}
	// (项目号只参与 hash、不进文件名)—— 只清一侧会让数据永久滞留。实测该目录曾
	// 堆到 227 个文件 / 4.9GB,其中 206 个是早期 manjux_ 命名格式留下的孤儿。
	// 判据:仍被某个现存 sidecar(.ok)引用的保留,其余一律清理。
	ptDir := filepath.Join(g.Comfy.RootDir, "ComfyUI", "models", "conditioning")
	if ptCount, ptSize, err := cleanCondCacheFiles(ptDir, collectLiveCondNames(condDir)); err == nil && ptCount > 0 {
		add(fmt.Sprintf("条件缓存数据文件 %d 个(.pt)", ptCount), ptSize)
	}

	// ④ 系统临时文件(mx-* 前缀,应用自身产生的)
	tmpDir := os.TempDir()
	if ents, err := os.ReadDir(tmpDir); err == nil {
		var tmpCount int
		var tmpSize int64
		for _, e := range ents {
			name := e.Name()
			if strings.HasPrefix(name, "mx-") || strings.HasPrefix(name, "mx_") {
				full := filepath.Join(tmpDir, name)
				if st, _ := e.Info(); st != nil {
					tmpSize += st.Size()
				}
				tmpCount++
				_ = os.RemoveAll(full)
			}
		}
		if tmpCount > 0 {
			add(fmt.Sprintf("系统临时文件 %d 个", tmpCount), tmpSize)
		}
	}

	s.opLog("🧹 一键清理完成:%d 项,释放 %.1f MB", len(res.Items), res.FreedMB)
	writeJSON(w, 200, res)
}

// dataDir 返回数据根目录。
func (s *Server) dataDir() string {
	return s.cfg.Get().DataDir
}

// collectLiveCondNames 收集所有项目 sidecar 中仍在使用的缓存名(.ok 文件名去扩展名)。
// 这些名字对应的 .pt 是"活的",清理时必须保留。
func collectLiveCondNames(condDir string) map[string]bool {
	live := map[string]bool{}
	projs, err := os.ReadDir(condDir)
	if err != nil {
		return live
	}
	for _, pj := range projs {
		if !pj.IsDir() {
			continue
		}
		oks, err := os.ReadDir(filepath.Join(condDir, pj.Name()))
		if err != nil {
			continue
		}
		for _, ok := range oks {
			if !ok.IsDir() && strings.HasSuffix(ok.Name(), ".ok") {
				live[strings.TrimSuffix(ok.Name(), ".ok")] = true
			}
		}
	}
	return live
}

// cleanCondCacheFiles 删除条件缓存数据目录中**不再被任何 sidecar 引用**的 .pt。
//
// 为什么必须单独做这一步:sidecar 记账在本应用侧(data/cond_cache),而 .pt 数据
// 在 ComfyUI 侧(models/conditioning),且 .pt 文件名只有 s{shot}_{hash} —— 项目号
// 只参与 hash 计算、不体现在文件名里,所以**无法按项目名匹配**,只能靠 sidecar 的
// .ok 文件名反查。旧实现只清 sidecar,导致数据文件永久滞留(实测 227 个 / 4.9GB,
// 其中 206 个是早期 manjux_ 命名格式的孤儿)。
//
// 目录不存在或不可读返回错误,由调用方决定是否提示(不影响其它清理项)。
// 返回 (删除数, 释放字节, 错误)。
func cleanCondCacheFiles(ptDir string, live map[string]bool) (int, int64, error) {
	ents, err := os.ReadDir(ptDir)
	if err != nil {
		return 0, 0, err
	}
	var count int
	var size int64
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pt") {
			continue
		}
		if live[strings.TrimSuffix(e.Name(), ".pt")] {
			continue // 仍被某个现存项目引用
		}
		if fi, ierr := e.Info(); ierr == nil {
			size += fi.Size()
		}
		if os.Remove(filepath.Join(ptDir, e.Name())) == nil {
			count++
		}
	}
	return count, size, nil
}
