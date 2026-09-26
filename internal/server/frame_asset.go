// frame_asset.go 视频截帧定妆:H3 视频渲染的形象往往优于 Krea-2 定妆照,
// 直接从渲染完成的镜头截帧替换定妆——零偏差锚定成片真实形象,后续集
// 复用库时天然与视频一致(2026-09-10 用户定调)。
package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
)

type frameReq struct {
	ShotID int     `json:"shot_id"`
	Ratio  float64 `json:"ratio"` // 0.25/0.5/0.75
}

// clipPath 镜头产物路径(要求已渲染存在)。
func (s *Server) clipPath(id string, shotID int) (string, error) {
	clip := filepath.Join(s.store.Dir(id), "clips", fmt.Sprintf("shot_%03d.mp4", shotID))
	if _, err := os.Stat(clip); err != nil {
		return "", fmt.Errorf("镜头 %d 尚未渲染(无产物)", shotID)
	}
	return clip, nil
}

// extractFrame 从镜头产物按时长比例截一帧到 out(png)。
func (s *Server) extractFrame(ctx context.Context, id string, shotID int, ratio, dur float64, out string) error {
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	t := dur * ratio
	if t < 0.1 {
		t = 0.1
	}
	_, err := s.ff.Run(ctx, "-y", "-ss", fmt.Sprintf("%.3f", t), "-i",
		filepath.ToSlash(filepath.Join(s.store.Dir(id), "clips", fmt.Sprintf("shot_%03d.mp4", shotID))),
		"-frames:v", "1", "-q:v", "2", filepath.ToSlash(out))
	return err
}

// handleAssetFramePreview POST /api/projects/{id}/assets/{assetID}/frame-preview
// body {shot_id} → clips/_frames/ 下 3 张候选帧(25%/50%/75%),返回可直连路径。
func (s *Server) handleAssetFramePreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.store.Get(id); !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	var in frameReq
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	clip, err := s.clipPath(id, in.ShotID)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	info, err := s.ff.Probe(clip)
	if err != nil {
		writeErr(w, 500, fmt.Errorf("读取镜头时长失败: %w", err))
		return
	}
	dir := filepath.Join(s.store.Dir(id), "clips", "_frames")
	_ = os.MkdirAll(dir, 0o755)
	frames := []string{}
	for _, ratio := range []float64{0.25, 0.5, 0.75} {
		rel := path.Join("clips", "_frames", fmt.Sprintf("shot_%03d_%02d.png", in.ShotID, int(ratio*100)))
		out := filepath.Join(s.store.Dir(id), filepath.FromSlash(rel))
		if err := s.extractFrame(r.Context(), id, in.ShotID, ratio, info.Duration, out); err != nil {
			writeErr(w, 500, fmt.Errorf("截帧失败: %w", err))
			return
		}
		frames = append(frames, rel)
	}
	writeJSON(w, 200, map[string]any{"frames": frames, "duration": info.Duration})
}

// handleAssetFrameApply POST /api/projects/{id}/assets/{assetID}/frame-apply
// body {shot_id, ratio} → 截帧安装为该资产定妆并入库(source=video-frame)。
func (s *Server) handleAssetFrameApply(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	assetID := r.PathValue("assetID")
	assets, err := s.store.LoadAssets(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	a, ok := assets[assetID]
	if !ok {
		writeErr(w, 404, fmt.Errorf("资产不存在"))
		return
	}
	var in frameReq
	if err := readBody(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	clip, err := s.clipPath(id, in.ShotID)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	info, err := s.ff.Probe(clip)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("mx-frame-%d.png", os.Getpid()))
	if err := s.extractFrame(r.Context(), id, in.ShotID, in.Ratio, info.Duration, tmp); err != nil {
		writeErr(w, 500, fmt.Errorf("截帧失败: %w", err))
		return
	}
	defer os.Remove(tmp)

	kind := a.Kind
	safeName := sanitizeFilename(a.Name + "_" + assetID)
	if safeName == "" {
		safeName = "asset"
	}
	destDir := filepath.Join(s.store.Dir(id), "assets", kind)
	_ = os.MkdirAll(destDir, 0o755)
	dest := filepath.Join(destDir, safeName+".png")
	raw, rerr := os.ReadFile(tmp)
	if rerr != nil || len(raw) == 0 {
		writeErr(w, 500, fmt.Errorf("截帧产物不可读"))
		return
	}
	if werr := os.WriteFile(dest, raw, 0o644); werr != nil {
		writeErr(w, 500, werr)
		return
	}
	rel, _ := filepath.Rel(s.store.Dir(id), dest)
	a.File = filepath.ToSlash(rel)
	a.Source = "video-frame"
	a.Fingerprint = project.FingerprintFile(dest)
	assets[assetID] = a
	if err := s.store.SaveAssets(id, assets); err != nil {
		writeErr(w, 500, err)
		return
	}
	// 同步入库:后续项目复用的是视频锚定的形象
	if s.lib != nil {
		desc, prompt := a.ImagePrompt, a.ImagePrompt
		if plan, _ := s.store.LoadPlan(id); plan != nil {
			if kind == "characters" {
				for _, c := range plan.Characters {
					if "char:"+c.ID == assetID {
						desc, prompt = c.Description, c.ImagePrompt
					}
				}
			} else if kind == "scenes" {
				// 场景提示词走**唯一装配点**(乙1):截帧定妆的元数据要与产线同源,
				// 否则复用它的后续项目拿到裸提示词,场景画风又跑偏。
				proj, _ := s.store.Get(id)
				for _, sc := range plan.Scenes {
					if "scene:"+sc.ID == assetID {
						desc, prompt = sc.Description, pipeline.SceneImagePrompt(proj, plan, sc)
					}
				}
			}
		}
		_, _ = s.lib.Put(kind, a.Name, desc, prompt, dest, id, "video-frame")
	}
	s.opLog("🎬 视频截帧定妆:%s「%s」(镜 %d,%d%% 处)——与成片形象零偏差", map[string]string{"characters": "角色", "scenes": "场景", "props": "道具"}[kind], a.Name, in.ShotID, int(in.Ratio*100))
	writeJSON(w, 200, a)
}

// unused guard(保持与包内风格一致)
var _ = strings.TrimSpace
var _ = strconv.Itoa
