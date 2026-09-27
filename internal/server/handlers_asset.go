package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/YiIimini/ManJuX/internal/pipeline"
	"github.com/YiIimini/ManJuX/internal/project"
)

func (s *Server) uploadAsset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.store.Get(id); !ok {
		writeErr(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	// JSON(image_b64/image_path)为桌面模式主通道(见 upload_payload.go);
	// multipart 兼容浏览器模式。multipart 同样落临时文件再安装——wails
	// 资产代理对大体积 multipart 有截断,统一在服务端校验非空,坏文件
	// 直接报错而非落盘 0 字节。
	var kind, assetID, name, srcPath, ext string
	var cleanup func()
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var in struct {
			Kind    string `json:"kind"`     // characters / scenes / props
			AssetID string `json:"asset_id"` // 形如 char:xxx / scene:xxx
			Name    string `json:"name"`
			imagePayload
		}
		if err := readBody(r, &in); err != nil {
			writeErr(w, 400, err)
			return
		}
		kind, assetID, name = in.Kind, in.AssetID, in.Name
		p, cl, err := in.resolve()
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		srcPath, cleanup = p, cl
		ext = strings.ToLower(filepath.Ext(p))
	} else {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			writeErr(w, 400, err)
			return
		}
		kind = r.FormValue("kind")
		assetID = r.FormValue("asset_id")
		name = r.FormValue("name")
		file, hdr, err := r.FormFile("file")
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		defer file.Close()
		ext = strings.ToLower(filepath.Ext(hdr.Filename))
		tmp, terr := os.CreateTemp("", "mx-asset-*"+ext)
		if terr != nil {
			writeErr(w, 500, terr)
			return
		}
		defer os.Remove(tmp.Name())
		if _, ierr := io.Copy(tmp, file); ierr != nil {
			tmp.Close()
			writeErr(w, 500, ierr)
			return
		}
		tmp.Close()
		srcPath = tmp.Name()
	}
	switch kind {
	case "characters", "scenes", "props":
	default:
		writeErr(w, 400, fmt.Errorf("kind 必须是 characters/scenes/props"))
		return
	}
	if assetID == "" {
		writeErr(w, 400, fmt.Errorf("asset_id 不能为空"))
		return
	}
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp":
	default:
		writeErr(w, 400, fmt.Errorf("仅支持 png/jpg/webp"))
		return
	}
	raw, rerr := os.ReadFile(srcPath)
	if rerr != nil || len(raw) == 0 {
		writeErr(w, 400, fmt.Errorf("上传的图片为空或不可读(请重新选择文件)"))
		return
	}
	// 文件名安全化
	safeName := sanitizeFilename(name + "_" + assetID)
	if safeName == "" {
		safeName = "asset"
	}
	destDir := filepath.Join(s.store.Dir(id), "assets", kind)
	_ = os.MkdirAll(destDir, 0o755)
	dest := filepath.Join(destDir, safeName+ext)
	if werr := os.WriteFile(dest, raw, 0o644); werr != nil {
		writeErr(w, 500, werr)
		return
	}

	assets, _ := s.store.LoadAssets(id)
	rel, _ := filepath.Rel(s.store.Dir(id), dest)
	assets[assetID] = project.Asset{
		ID: assetID, Kind: kind, Name: name,
		File: filepath.ToSlash(rel), Source: "upload",
		Fingerprint: project.FingerprintFile(dest),
	}
	if err := s.store.SaveAssets(id, assets); err != nil {
		writeErr(w, 500, err)
		return
	}
	// 上传即入库:后续项目同名角色/场景自动复用
	if s.lib != nil {
		desc := r.FormValue("description")
		prompt := r.FormValue("image_prompt")
		plan, _ := s.store.LoadPlan(id)
		if plan != nil {
			if kind == "characters" {
				for _, c := range plan.Characters {
					if "char:"+c.ID == assetID {
						desc, prompt = c.Description, c.ImagePrompt
					}
				}
			} else if kind == "scenes" {
				// 场景提示词走**唯一装配点**(乙1):入库的元数据必须是"这套画风下的
				// 场景提示词",否则复用它的后续项目拿到的是裸提示词 —— 同一部剧里
				// 角色一套画风、场景另一套。proj 允许为 nil(拿不到即按平台默认)。
				proj, _ := s.store.Get(id)
				for _, sc := range plan.Scenes {
					if "scene:"+sc.ID == assetID {
						desc, prompt = sc.Description, pipeline.SceneImagePrompt(proj, plan, sc)
					}
				}
			} else if kind == "props" {
				// 道具同理（2026-09-27 补道具图那轮）：入库的元数据必须是**过装配点**的那份
				// （白底单件 + 全片风格 + 清汉字），否则后续项目按名字复用它时，
				// 拿到的是裸提示词 —— 同一件道具在别的集里就成了"另一种画风的另一种东西"。
				// 少了这一支的表现是"上传能成功、但入库元数据是空的"，最难看出来。
				proj, _ := s.store.Get(id)
				for _, pr := range plan.Props {
					if "prop:"+pr.ID == assetID {
						desc, prompt = pr.Description, pipeline.PropImagePrompt(proj, plan, pr)
					}
				}
			}
		}
		if _, err := s.lib.Put(kind, name, desc, prompt, dest, id, "upload"); err == nil {
			// 入库成功仅记录日志,不阻塞响应
		}
	}
	s.opLog("⬆ 已上传%s「%s」并入库(项目「%s」)", map[string]string{"characters": "角色定妆照", "scenes": "场景参考图", "props": "道具图"}[kind], name, id)
	writeJSON(w, 200, assets[assetID])
}

func (s *Server) deleteAsset(w http.ResponseWriter, r *http.Request) {
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
	if a.File != "" {
		_ = os.Remove(filepath.Join(s.store.Dir(id), a.File))
	}
	delete(assets, assetID)
	_ = s.store.SaveAssets(id, assets)
	s.opLog("🗑 已删除资产 %s(项目「%s」)", assetID, id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- 项目文件服务(播放/预览,防路径穿越)----------

func (s *Server) serveProjectFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rel := path.Clean(r.PathValue("path"))
	if strings.Contains(rel, "..") || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if _, ok := s.store.Get(id); !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	full := filepath.Join(s.store.Dir(id), filepath.FromSlash(rel))
	if _, err := os.Stat(full); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// 视频加 Range 支持(拖动播放)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, full)
}

// ---------- SSE ----------
