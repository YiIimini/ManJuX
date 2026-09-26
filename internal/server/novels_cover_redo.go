package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/YiIimini/ManJuX/internal/pipeline"
)

// novels_cover_redo.go 小说库的「封面重做」。
//
// 用户 2026-09-26:「小说库中的封面菜单需要新增一个"封面重做"的功能」。
//
// 为什么需要:封面是作品的招牌,而它现在是**技能端一次性生成**的产物
// (`<书>/封面/封面.png`,896×1344,由 `gen_cover.py` 按 `pos-qwen21.txt` 出图)。
// 想换个构图/换一版脸,原先只能回技能端跑 python —— 平台里明明有同一条 Qwen-Image-2.1
// 出图链路(`comfyengine.GenerateImage`,与定妆照同一个 family),没理由让用户绕出去。
//
// 口径:
//   - 正向词取 `<书>/封面/pos-qwen21.txt`(**技能端留的正向词就是权威**,平台不自己编);
//   - 尺寸沿用现有封面的实际尺寸(它决定了书卡与项目卡的构图),读不到才用 896×1344;
//   - **旧封面先备份**(`封面.旧-<时间戳>.png`)再覆盖 —— 重做是创作行为,用户随时可能反悔;
//   - 重做后**同步刷新这本书的集项目封面**(项目里的 cover.* 是导入时拷的副本,不刷新
//     用户会看到"封面重做了但视频卡还是旧的")。

// coverRedoPromptFile 技能端留的正向词文件名(权威来源)。
const coverRedoPromptFile = "pos-qwen21.txt"

// handleNovelCoverRedo POST /api/novels/cover-redo {path}
//
// path = 书目录(小说库里的书卡路径),不是封面文件本身。
func (s *Server) handleNovelCoverRedo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
		Seed int64  `json:"seed"`
	}
	if err := readBody(r, &in); err != nil || strings.TrimSpace(in.Path) == "" {
		writeErr(w, 400, fmt.Errorf("需要 path(小说库里的书目录)"))
		return
	}
	bookDir := strings.TrimSpace(in.Path)
	if !s.inNovelRoot(bookDir) {
		writeErr(w, 403, fmt.Errorf("路径超出小说库范围"))
		return
	}
	fi, err := os.Stat(bookDir)
	if err != nil || !fi.IsDir() {
		writeErr(w, 400, fmt.Errorf("书目录不存在:%s", bookDir))
		return
	}
	coverDir := filepath.Join(bookDir, "封面")
	promptPath := filepath.Join(coverDir, coverRedoPromptFile)
	raw, err := os.ReadFile(promptPath)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		writeErr(w, 400, fmt.Errorf(
			"这本书没有封面正向词(%s):先让技能端生成一次封面(gen_cover.py),或在封面目录放一份正向词再重做",
			promptPath))
		return
	}
	prompt := strings.TrimSpace(string(raw))

	// 尺寸沿用现有封面(它决定书卡/项目卡的构图);读不到再退回技能端的默认 896×1344。
	w0, h0 := 896, 1344
	coverPath := ""
	if p := coverInDir(bookDir); p != "" {
		coverPath = p
		if cw, ch, ok := s.imageSize(p); ok && cw > 0 && ch > 0 {
			w0, h0 = cw, ch
		}
	}
	seed := in.Seed
	if seed == 0 {
		seed = pipeline.RandomSeed()
	}

	eng, err := s.reg.Get("comfy")
	if err != nil {
		writeErr(w, 503, fmt.Errorf("本地 ComfyUI 引擎不可用:%w", err))
		return
	}
	// 出图可能数分钟(模型换入换出),用后台超时而不是请求超时。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	tmp := filepath.Join(coverDir, fmt.Sprintf(".redo-%d.png", seed))
	if err := os.MkdirAll(coverDir, 0o755); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.opLog("🎨 封面重做:《%s》seed=%d %d×%d(正向词 %s)",
		filepath.Base(bookDir), seed, w0, h0, coverRedoPromptFile)
	if _, err := eng.GenerateImage(ctx, prompt, "", seed, w0, h0, tmp); err != nil {
		_ = os.Remove(tmp)
		writeErr(w, 500, fmt.Errorf("封面重做失败:%w", err))
		return
	}
	// 旧封面先备份再覆盖:重做是创作行为,随时可能反悔。
	backup := ""
	if coverPath != "" {
		ext := strings.ToLower(filepath.Ext(coverPath))
		backup = filepath.Join(coverDir, "封面.旧-"+time.Now().Format("20060102-150405")+ext)
		if b, e := os.ReadFile(coverPath); e == nil {
			if e2 := os.WriteFile(backup, b, 0o644); e2 != nil {
				backup = ""
			}
		}
	}
	dst := filepath.Join(coverDir, "封面"+strings.ToLower(filepath.Ext(tmp)))
	if err := os.Rename(tmp, dst); err != nil {
		// 跨设备/占位等情况下退化为复制
		b, e := os.ReadFile(tmp)
		if e != nil {
			writeErr(w, 500, e)
			return
		}
		if e := os.WriteFile(dst, b, 0o644); e != nil {
			writeErr(w, 500, e)
			return
		}
		_ = os.Remove(tmp)
	}

	// 集项目里的 cover.* 是导入时拷的副本:不刷新,用户会看到"封面重做了但视频卡还是旧的"。
	refreshed := s.refreshProjectsCover(filepath.Base(bookDir), dst, backup)

	s.bus.Publish(pipeline.Event{Phase: "novel", Level: "success",
		Message: fmt.Sprintf("🎨 《%s》封面已重做(seed=%d)%s", filepath.Base(bookDir), seed,
			func() string {
				if refreshed > 0 {
					return fmt.Sprintf(",并刷新了 %d 个集项目封面", refreshed)
				}
				return ""
			}())})
	writeJSON(w, 200, map[string]any{
		"ok": true, "cover": filepath.ToSlash(filepath.Join("封面", filepath.Base(dst))),
		"seed": seed, "width": w0, "height": h0, "backup": backup, "projects": refreshed,
	})
}

// refreshProjectsCover 把新封面写进这本书名下的所有集项目。
//
// oldBackup = 旧封面的备份路径(重做前那一版)。集项目里的 `cover.*` 是导入时从**旧封面**
// 拷的副本,所以判据是**逐字节比对**:与旧封面相同的 = 我们拷的副本,可以覆盖;
// 不相同的 = 用户自己换过的封面,一律不碰("我明明换过封面,它又变回去了"是最难查的一类)。
func (s *Server) refreshProjectsCover(book, src, oldBackup string) int {
	ext := strings.ToLower(filepath.Ext(src))
	b, err := os.ReadFile(src)
	if err != nil {
		return 0
	}
	var old []byte
	if oldBackup != "" {
		old, _ = os.ReadFile(oldBackup)
	}
	n := 0
	for _, p := range s.store.List() {
		if !strings.HasPrefix(p.ID, book+"·第") {
			continue
		}
		dir := filepath.Join(s.store.Dir(p.ID), "assets")
		cur := coverFileIn(dir)
		if cur != "" {
			if len(old) == 0 {
				continue // 有封面但不知道旧封面长什么样:宁可不覆盖
			}
			cb, err := os.ReadFile(cur)
			if err != nil || len(cb) != len(old) || string(cb) != string(old) {
				continue // 用户自己换过的封面
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, "cover"+ext), b, 0o644); err != nil {
			continue
		}
		// 换了后缀(旧 jpg → 新 png)时清掉旧文件,免得 projectCover 挑到旧的。
		if cur != "" && !strings.EqualFold(cur, filepath.Join(dir, "cover"+ext)) {
			_ = os.Remove(cur)
		}
		n++
	}
	return n
}

// coverFileIn 项目 assets 目录里的封面文件(没有则空)。
func coverFileIn(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n := strings.ToLower(e.Name())
		if strings.HasPrefix(n, "cover.") && coverImageExts[filepath.Ext(n)] {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// imageSize 读图片像素尺寸(用 ffprobe,平台已有这条依赖;读不到返回 ok=false)。
func (s *Server) imageSize(path string) (int, int, bool) {
	if s.ff == nil {
		return 0, 0, false
	}
	info, err := s.ff.Probe(path)
	if err != nil || info == nil || info.Width <= 0 || info.Height <= 0 {
		return 0, 0, false
	}
	return info.Width, info.Height, true
}
