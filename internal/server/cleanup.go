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
	"sort"
	"strings"
	"time"
)

type cleanupResult struct {
	Items   []string `json:"items"`
	FreedMB float64  `json:"freedMB"`
	// Skipped 因为"管线正在跑"而**故意没清**的项（2026-09-29 审计 P3-10）。
	// 清理是用户的显式动作，默默少清一项等于让他以为清干净了。
	Skipped []string `json:"skipped,omitempty"`
}

func (s *Server) handleCleanup(w http.ResponseWriter, r *http.Request) {
	res := &cleanupResult{Items: []string{}}
	add := func(label string, size int64) {
		res.Items = append(res.Items, label)
		res.FreedMB += float64(size) / 1024 / 1024
	}

	// ① ComfyUI **运行时目录整扫**（输出中间产物 ＋ 参考图上传副本）
	//
	// 【2026-09-27 用户指示】「一键清理 还需要清理 D:\Ai\ComfyUI\ComfyUI\input 和 output 这两个目录」。
	// 此前只清两样东西：`output/manjux/`（平台自己的中间产物）与 input 里**24 小时前的图片** ——
	// 其余一律留着：ComfyUI 自己出的图、别的扩展名（.json/.mp4/latent）、近 24 小时的副本。
	// 用户看到的就是"点了清理，这两个目录还占着几个 G"。
	//
	// 现在的边界（安全第一，宁可少删）：
	//   ① **只删 1 小时前的文件**：正在跑的那一镜刚上传/刚写出的东西不能被删（此前 input 的 24 小时
	//      窗口太保守，用户要的是"能用"而不是"绝对不误删"；1 小时足够覆盖任何在跑的任务）；
	//   ② **input 的子目录不动**（旧决定保留：可能是用户自己放的素材）；output 的子目录按同样规则清文件；
	//   ③ 文件清空后顺手删掉**空目录**（空目录不占空间但会让人以为还有东西）；
	//   ④ `output/manjux/` 仍整删（平台自己的产物，无回收价值）。
	g := s.cfg.Get()
	var outCount int
	var outSize int64
	sweepCut := time.Now().Add(-time.Hour)
	for _, name := range []string{"output", "input"} {
		for _, dir := range comfyRunDirs(g.Comfy.RootDir, name) {
			n, size := sweepRunDir(dir, name, sweepCut)
			outCount += n
			outSize += size
		}
	}
	if outCount > 0 {
		add(fmt.Sprintf("ComfyUI input/output 运行时文件 %d 个（1 小时前的）", outCount), outSize)
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
	// (项目号只参与 hash、不进文件名)—— 只清一侧会让数据永久滞留。
	// 判据:仍被某个现存 sidecar(.ok)引用的保留,其余一律清理。
	//
	// 2026-09-24 修(用户报「一键清理也要清掉 \ComfyUI\shared\models\conditioning\」):
	// 旧实现只拼 `<root>/ComfyUI/models/conditioning`,而本机模型在
	// `<root>/ComfyUI/shared/models/`(extra_model_paths.yaml 的 base_path)——
	// 于是真正堆着 838 文件 / 17.4GB 的那份目录从未被清理,按钮却报"清理完成"。
	// 现在对所有**实际存在**的布局逐一清理(标准布局与 shared 布局可能各有一批残留)。
	live := collectLiveCondNames(condDir)
	var ptCount int
	var ptSize int64
	var ptDirs []string
	for _, ptDir := range comfyModelDirs(g.Comfy.RootDir, "conditioning") {
		n, sz, err := cleanCondCacheFiles(ptDir, live)
		if err != nil {
			continue
		}
		if n > 0 {
			ptCount += n
			ptSize += sz
			ptDirs = append(ptDirs, ptDir)
		}
	}
	if ptCount > 0 {
		add(fmt.Sprintf("条件缓存数据文件 %d 个(.pt;目录 %s)", ptCount, strings.Join(ptDirs, "、")), ptSize)
	}

	// ⑤b 活项目里**过期**的条件缓存（2026-09-29 性能审计 #8）。
	//
	// ③ 清"项目已删的孤儿"、⑤ 清"没被任何 .ok 引用的数据"，但**仍在被引用的旧条目**
	// 从来没人清：改一次提示词重渲，同一镜会生成新的 `s{shot}_{hash}`，旧的那份
	// sidecar 还在、.pt 也还在 —— 一集 60 镜就是 60 个 .pt（每个 20MB 上下），
	// 40 集 ≈ 2400 个。代码注释里记的历史实测是 **838 文件 / 17.4GB**。
	//
	// 判据取"每个项目保留最近 N 份"：缓存是**可重算**的（下次渲染会重新预编码），
	// 留最近的那批就够"接着渲"用；更早的条目只有在"把镜头改回原样再渲"时才会命中，
	// 而那种情况下重算一次的代价远小于长期占着几 GB。
	liveOld := collectStaleCondNames(condDir, condCacheKeepPerProject)
	var oldCount int
	var oldSize int64
	for _, ptDir := range comfyModelDirs(g.Comfy.RootDir, "conditioning") {
		for name := range liveOld {
			p := filepath.Join(ptDir, name+".pt")
			st, err := os.Stat(p)
			if err != nil {
				continue
			}
			if os.Remove(p) == nil {
				oldCount++
				oldSize += st.Size()
			}
		}
	}
	if oldCount > 0 {
		// 同步清掉 sidecar（否则下次会按"有 .ok 没 .pt"当成缓存损坏）
		removeCondSidecars(condDir, liveOld)
		add(fmt.Sprintf("过期条件缓存 %d 个(.pt;每项目只留最近 %d 份)", oldCount, condCacheKeepPerProject), oldSize)
	}

	// ④ 系统临时文件(mx-* 前缀,应用自身产生的)
	//
	// 【2026-09-29 运行时审计 P3-10】**运行时不能无条件清这一片**：
	// `mx-audio-*.pcm`（合成阶段做语音边界检测，写完**立刻** `os.ReadFile`）与
	// `mx-frame-*.png`（截帧）都落在删除范围内。渲染/合成进行中用户点一下「一键清理」，
	// 症状是随机性的"解码音轨失败 → 回退中点切"或"截帧产物不可读"—— 事后完全对不上因。
	// 所以管线在跑时**跳过**这一项并如实说明（清理是用户的显式动作，默默少清要讲出来）。
	tmpDir := os.TempDir()
	busy := s.pipe != nil && s.pipe.HasRunning()
	if ents, err := os.ReadDir(tmpDir); err == nil && !busy {
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
	} else if busy {
		res.Skipped = append(res.Skipped,
			"系统临时文件（mx-*）：**本机还有管线在跑**，那里可能有正在用的音轨/抽帧中间产物 —— 等它跑完再清")
	}

	// ⑥（已并入 ①）：ComfyUI input 里的参考图上传副本曾经在这里单独清（只清 24 小时前的**图片**）。
	// 2026-09-27 用户要求把 input/output 两个目录整个清干净之后，它已被 ① 的整扫覆盖 ——
	// 留着就是"同一个目录两处各清一遍"，改范围时必然漏改一处（本仓栽过多次的老坑）。
	// ⑦ 资产阶段的转身视频中间产物(<base>_views/ 目录):抽帧后视频本身不再作参考,
	// 单个 5-30MB(2026-09-12 实测)。**只删视频与目录**,同目录的
	// <base>_view_*.png / <base>_turnaround.png 是渲染真正要用的参考图,必须保留。
	var viewDirs int
	var viewSize int64
	for _, p := range s.store.List() {
		pat := filepath.Join(s.store.Dir(p.ID), "assets", "characters", "*_views")
		for _, d := range globOrEmpty(pat) {
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				viewSize += dirSize(d)
				viewDirs++
				_ = os.RemoveAll(d)
			}
		}
	}
	if viewDirs > 0 {
		add(fmt.Sprintf("转身视频中间目录 %d 个(多视角参考图已保留)", viewDirs), viewSize)
	}

	// ⑧ 本次迭代留下的临时对照目录与诊断日志(应用自身产物)
	if st, err := os.Stat(filepath.Join(s.dataDir(), "_scratch")); err == nil && st.IsDir() {
		sz := dirSize(filepath.Join(s.dataDir(), "_scratch"))
		_ = os.RemoveAll(filepath.Join(s.dataDir(), "_scratch"))
		add("临时对照目录 data/_scratch", sz)
	}
	if st, err := os.Stat(filepath.Join(s.dataDir(), "_sb_build")); err == nil && st.IsDir() {
		sz := dirSize(filepath.Join(s.dataDir(), "_sb_build"))
		_ = os.RemoveAll(filepath.Join(s.dataDir(), "_sb_build"))
		add("临时构建目录 data/_sb_build", sz)
	}

	// ⑨ 运行日志(用户 2026-09-26:「右上角一键清理也需要清理掉运行日志」)。
	//
	// 走注入的 Reset —— **不能**在 server 侧自己删/改名:`manjux.log` 正被日志写句柄持有,
	// 改名之后写入会继续落到那个已改名的文件上(新建的空文件永远空着,日志却在别处继续长),
	// 删除同理。清空后紧接着那行「一键清理完成」就写在刚清空的日志里,留痕。
	if s.logs != nil {
		freed, archives, err := s.logs.Reset()
		switch {
		case err != nil:
			res.Items = append(res.Items, fmt.Sprintf("运行日志清理失败:%v", err))
		default:
			label := "运行日志 data/logs/manjux.log(已清空"
			if archives > 0 {
				label += fmt.Sprintf(",连同 %d 份归档", archives)
			}
			add(label+")", freed)
		}
	}

	// ⑩ 引擎日志:ComfyUI 自己的 logs/comfyui.log —— 它的写句柄在**外部进程**手里,
	// 我们只有路径,所以既不删也不改名,只在超过 8MB 时原地截断(失败就跳过:那是引擎的
	// 日志,不该让本应用的清理报错)。刻意不无条件清空:引擎日志是渲染排障的第一现场。
	if root := strings.TrimSpace(g.Comfy.RootDir); root != "" {
		comfyLog := filepath.Join(root, "logs", "comfyui.log")
		if st, err := os.Stat(comfyLog); err == nil && st.Size() >= 8<<20 {
			if os.Truncate(comfyLog, 0) == nil {
				add(fmt.Sprintf("引擎日志 logs/comfyui.log 截断(%.1fMB)", float64(st.Size())/1024/1024), 0)
			}
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
// condCacheKeepPerProject 每个项目保留最近多少份条件缓存（见 ⑤b 的说明）。
//
// 120 ≈ 两集的量：够"接着上次渲到的地方继续"用，而更早的条目只有在"把镜头改回原样
// 再渲一次"时才会命中 —— 那种情况下重算一次的代价远小于长期占着几 GB。
const condCacheKeepPerProject = 120

// collectStaleCondNames 找出"活项目里**过期**的条件缓存名"（每个项目保留最近 keep 份，
// 更早的返回）。返回的是 `.ok` 的基名（= `.pt` 的文件名主干），调用方据此删两侧。
//
// 只处理**仍存在**的项目目录：项目已删的整目录由上面的 ③ 清掉。
// 排序按 sidecar 的修改时间（写入时就是它被用到的时刻）。
func collectStaleCondNames(condDir string, keep int) map[string]int {
	stale := map[string]int{} // 缓存名 → 无用（值占位，调用方只关心 key）
	projs, err := os.ReadDir(condDir)
	if err != nil {
		return stale
	}
	for _, pj := range projs {
		if !pj.IsDir() {
			continue
		}
		dir := filepath.Join(condDir, pj.Name())
		ents, rerr := os.ReadDir(dir)
		if rerr != nil {
			continue
		}
		type okFile struct {
			name string
			mod  time.Time
		}
		var oks []okFile
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".ok") {
				continue
			}
			info, ierr := e.Info()
			if ierr != nil {
				continue
			}
			oks = append(oks, okFile{name: e.Name(), mod: info.ModTime()})
		}
		if len(oks) <= keep {
			continue
		}
		sort.Slice(oks, func(i, j int) bool { return oks[i].mod.After(oks[j].mod) })
		for _, o := range oks[keep:] {
			stale[strings.TrimSuffix(o.name, ".ok")] = 1
		}
	}
	return stale
}

// removeCondSidecars 删掉这些缓存名对应的 sidecar（`.ok` 与当前版本的键标记不动）。
func removeCondSidecars(condDir string, names map[string]int) {
	projs, err := os.ReadDir(condDir)
	if err != nil {
		return
	}
	for _, pj := range projs {
		if !pj.IsDir() {
			continue
		}
		for name := range names {
			_ = os.Remove(filepath.Join(condDir, pj.Name(), name+".ok"))
		}
	}
}

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

// globOrEmpty 容错的 Glob(模式非法/无权限时返回空列表而不是 nil 恐慌)。
func globOrEmpty(pattern string) []string {
	m, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return m
}

// dirSize 递归统计目录占用(用于清理前如实上报释放量)。
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		if fi, ierr := e.Info(); ierr == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}
