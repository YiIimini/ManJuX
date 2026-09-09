// upload_payload.go 图片上传载荷(JSON)。
//
// 背景:桌面 webview 经 wails 资产代理发 multipart 时,大体积文件体会被
// 截断(2026-09-09 实测:角色自定义上传报「源图不存在」,临时文件为 0 字节;
// 直连 HTTP 同处理器同尺寸正常,问题仅在 wails 转发管)。统一起见,前端
// 改走 JSON 双通道:点击选择 → FileReader 转 base64(超大图前端先压缩);
// 桌面拖放 → wails OnFileDrop 给出本机绝对路径,后端直接读盘。
// multipart 入口保留,浏览器模式不受影响。
package server

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// imagePayload 上传图片载荷:b64 与本机路径二选一。
type imagePayload struct {
	ImageB64  string `json:"image_b64"`  // dataURL 或纯 base64
	ImagePath string `json:"image_path"` // 本机绝对路径(wails OnFileDrop)
	Filename  string `json:"filename"`   // 原文件名(取扩展名用)
}

var imgExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true}

// resolve 把载荷落成服务器本地可读文件;cleanup 非 nil 时调用方用完须执行。
func (p imagePayload) resolve() (path string, cleanup func(), err error) {
	switch {
	case p.ImageB64 != "":
		b64 := p.ImageB64
		if strings.HasPrefix(b64, "data:") {
			if i := strings.IndexByte(b64, ','); i >= 0 {
				b64 = b64[i+1:]
			}
		}
		raw, e := base64.StdEncoding.DecodeString(b64)
		if e != nil {
			return "", nil, fmt.Errorf("图片 base64 解码失败: %w", e)
		}
		if len(raw) == 0 {
			return "", nil, fmt.Errorf("图片内容为空(选择文件后请重新选择再提交)")
		}
		ext := strings.ToLower(filepath.Ext(p.Filename))
		if !imgExts[ext] {
			ext = ".png"
		}
		tmp, e := os.CreateTemp("", "mx-up-*"+ext)
		if e != nil {
			return "", nil, e
		}
		if _, e := tmp.Write(raw); e != nil {
			tmp.Close()
			_ = os.Remove(tmp.Name())
			return "", nil, e
		}
		tmp.Close()
		return tmp.Name(), func() { _ = os.Remove(tmp.Name()) }, nil
	case p.ImagePath != "":
		ext := strings.ToLower(filepath.Ext(p.ImagePath))
		if !imgExts[ext] {
			return "", nil, fmt.Errorf("仅支持 png/jpg/webp")
		}
		st, e := os.Stat(p.ImagePath)
		if e != nil || st.IsDir() {
			return "", nil, fmt.Errorf("拖放的源图不可读: %s", p.ImagePath)
		}
		if st.Size() == 0 {
			return "", nil, fmt.Errorf("源图为空文件: %s", p.ImagePath)
		}
		if st.Size() > 64<<20 {
			return "", nil, fmt.Errorf("图片超过 64MB,请先压缩")
		}
		return p.ImagePath, nil, nil
	}
	return "", nil, fmt.Errorf("缺少图片内容(image_b64/image_path)")
}

// serveThumb 本机图片预览:拖放入上传区的文件在 webview 里被 file:// 安全
// 策略拦截,选中后的缩略图改走后端转发(仅图片扩展名,≤64MB,不缓存)。
func (s *Server) serveThumb(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	var ct string
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png":
		ct = "image/png"
	case ".jpg", ".jpeg":
		ct = "image/jpeg"
	case ".webp":
		ct = "image/webp"
	default:
		writeErr(w, 400, fmt.Errorf("仅支持 png/jpg/webp"))
		return
	}
	st, err := os.Stat(p)
	if err != nil || st.IsDir() || st.Size() == 0 || st.Size() > 64<<20 {
		writeErr(w, 404, fmt.Errorf("图片不可读"))
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		writeErr(w, 404, fmt.Errorf("图片不可读"))
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}
