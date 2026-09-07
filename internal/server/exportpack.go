package server

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// exportClipPack 导出剪辑素材包(剪映友好):按镜命名的片段 + 成片 + SRT + 导入说明,zip 打包。
func (s *Server) exportClipPack(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	proj, ok := s.store.Get(id)
	if !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	plan, err := s.store.LoadPlan(id)
	if err != nil {
		writeErr(w, 400, fmt.Errorf("尚未生成分镜方案"))
		return
	}
	manifest, err := s.store.LoadManifest(id)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	outDir := filepath.Join(s.store.Dir(id), "output")
	_ = os.MkdirAll(outDir, 0o755)
	zipPath := filepath.Join(outDir, "剪辑素材包.zip")

	zf, err := os.Create(zipPath + ".tmp")
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	zw := zip.NewWriter(zf)

	addFile := func(src, name string, required bool) error {
		f, err := os.Open(src)
		if err != nil {
			if required {
				return fmt.Errorf("缺少 %s", name)
			}
			return nil
		}
		defer f.Close()
		ze, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			return err
		}
		_, err = io.Copy(ze, f)
		return err
	}

	// 按镜编号命名片段(剪映批量导入后顺序清晰)
	for _, shot := range plan.Shots {
		m, ok := manifest[strconv.Itoa(shot.ID)]
		if !ok || m.Status != "done" || m.File == "" {
			continue
		}
		src := filepath.Join(s.store.Dir(id), m.File)
		label := fmt.Sprintf("镜头%02d", shot.ID)
		if err := addFile(src, label+filepath.Ext(src), false); err == nil {
			_ = label
		}
	}
	_ = proj
	_ = addFile(filepath.Join(outDir, "final.mp4"), "成片_final.mp4", false)
	_ = addFile(filepath.Join(outDir, "final.srt"), "字幕_final.srt", false)

	readme := "ManJuX 剪辑素材包导入说明(剪映)\n" +
		"====================================\n\n" +
		"1) 打开剪映专业版 → 新建草稿(画幅按项目设置,如 9:16);\n" +
		"2) 「媒体」→「导入」→ 全选本包内所有 镜头XX.mp4(已按镜序命名,拖入时间线即为正序);\n" +
		"3) 字幕:「字幕」→「导入字幕」→ 选择 字幕_final.srt(时间轴已对齐);\n" +
		"4) 各镜头自带 H3 原生配音与环境音,无需另行配音;如需精修可替换 成片_final.mp4 整片使用;\n" +
		"5) 二次创作建议:镜头片段支持重新排序/裁剪,SRT 可按需微调。\n\n" +
		"注:完整剪映草稿(draft_content.json)自动生成对剪映版本敏感,本包采用通用素材+SRT 方案,任意版本剪映可导入。\n"
	if ze, err := zw.Create("导入说明.txt"); err == nil {
		_, _ = ze.Write([]byte(readme))
	}

	if err := zw.Close(); err != nil {
		zf.Close()
		os.Remove(zipPath + ".tmp")
		writeErr(w, 500, err)
		return
	}
	zf.Close()
	if err := os.Rename(zipPath+".tmp", zipPath); err != nil {
		writeErr(w, 500, err)
		return
	}
	st, _ := os.Stat(zipPath)
	writeJSON(w, 200, map[string]any{
		"ok": true, "file": "output/剪辑素材包.zip",
		"size_mb": float64(st.Size()) / 1e6,
	})
}
