// Package library 跨项目资产库:角色定妆照/场景图一次入库全局复用。
// 存储:data/assets_library/{characters|scenes}/<safe_name>/{asset.json, image}
// 匹配策略:名称精确匹配(TrimSpace);生成/上传成功的资产自动入库。
package library

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry 一条库资产。
type Entry struct {
	Kind          string `json:"kind"`           // characters / scenes
	Name          string `json:"name"`
	Description   string `json:"description"`
	ImagePrompt   string `json:"image_prompt"`
	Image         string `json:"image"`            // 相对库根路径(characters/x/portrait.png)
	Fingerprint   string `json:"fingerprint"`      // 内容指纹
	SourceProject string `json:"source_project"`   // 首次入库项目
	Source        string `json:"source"`           // krea2 / upload
	CreatedAt     time.Time `json:"created_at"`
	UsedCount     int    `json:"used_count"`       // 复用计数
}

// Library 资产库(线程安全)。
type Library struct {
	mu   sync.RWMutex
	root string
}

// New 打开/创建资产库。
func New(root string) (*Library, error) {
	for _, sub := range []string{"characters", "scenes"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &Library{root: root}, nil
}

// Root 库根目录。
func (l *Library) Root() string { return l.root }

func safeName(name string) string {
	var sb strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r >= 0x4e00 && r <= 0x9fff:
			sb.WriteRune(r)
		case r == '_', r == '-':
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func (l *Library) entryDir(kind, name string) (string, error) {
	sn := safeName(name)
	if sn == "" {
		return "", fmt.Errorf("资产名不可用")
	}
	switch kind {
	case "characters", "scenes":
	default:
		return "", fmt.Errorf("kind 必须是 characters/scenes")
	}
	return filepath.Join(l.root, kind, sn), nil
}

// Match 名称精确匹配;命中返回条目与图片绝对路径。
func (l *Library) Match(kind, name string) (*Entry, string, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	dir, err := l.entryDir(kind, name)
	if err != nil {
		return nil, "", false
	}
	raw, err := os.ReadFile(filepath.Join(dir, "asset.json"))
	if err != nil {
		return nil, "", false
	}
	var e Entry
	if json.Unmarshal(raw, &e) != nil {
		return nil, "", false
	}
	// e.Image 相对库根(如 characters/林凡/portrait.png),须从 root 拼,不能再拼在条目目录下
	img := filepath.Join(l.root, filepath.FromSlash(e.Image))
	if _, err := os.Stat(img); err != nil {
		return nil, "", false
	}
	return &e, img, true
}

// Put 入库/更新:拷贝图片 + 落 asset.json(同名覆盖,更新指纹与提示词)。
func (l *Library) Put(kind, name, description, imagePrompt, imgPath, sourceProject, source string) (*Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	dir, err := l.entryDir(kind, name)
	if err != nil {
		return nil, err
	}
	raw, _ := os.ReadFile(imgPath)
	if len(raw) == 0 {
		return nil, fmt.Errorf("源图不存在: %s", imgPath)
	}
	ext := strings.ToLower(filepath.Ext(imgPath))
	if ext == "" {
		ext = ".png"
	}
	imgName := "portrait" + ext
	if kind == "scenes" {
		imgName = "image" + ext
	}
	_ = os.MkdirAll(dir, 0o755)
	// 清掉旧扩展图(换格式时避免双图)
	if old, err := os.ReadDir(dir); err == nil {
		for _, f := range old {
			if strings.HasPrefix(f.Name(), "portrait.") || strings.HasPrefix(f.Name(), "image.") {
				if f.Name() != imgName {
					_ = os.Remove(filepath.Join(dir, f.Name()))
				}
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, imgName), raw, 0o644); err != nil {
		return nil, err
	}
	// 保留已有 UsedCount
	used := 0
	if prev, err := os.ReadFile(filepath.Join(dir, "asset.json")); err == nil {
		var p Entry
		if json.Unmarshal(prev, &p) == nil {
			used = p.UsedCount
		}
	}
	e := Entry{
		Kind: kind, Name: strings.TrimSpace(name),
		Description: description, ImagePrompt: imagePrompt,
		Image: filepath.ToSlash(filepath.Join(kind, safeName(name), imgName)),
		SourceProject: sourceProject, Source: source,
		CreatedAt: time.Now(), UsedCount: used,
	}
	// 指纹
	sum := md5hex(raw)
	e.Fingerprint = sum
	out, _ := json.MarshalIndent(&e, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "asset.json"), append(out, '\n'), 0o644); err != nil {
		return nil, err
	}
	return &e, nil
}

// BumpUsed 复用计数 +1。
func (l *Library) BumpUsed(kind, name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	dir, err := l.entryDir(kind, name)
	if err != nil {
		return
	}
	raw, err := os.ReadFile(filepath.Join(dir, "asset.json"))
	if err != nil {
		return
	}
	var e Entry
	if json.Unmarshal(raw, &e) != nil {
		return
	}
	e.UsedCount++
	out, _ := json.MarshalIndent(&e, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "asset.json"), append(out, '\n'), 0o644)
}

// List 全部条目(按类型分组)。
func (l *Library) List() map[string][]Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := map[string][]Entry{"characters": {}, "scenes": {}}
	for _, kind := range []string{"characters", "scenes"} {
		base := filepath.Join(l.root, kind)
		entries, _ := os.ReadDir(base)
		for _, de := range entries {
			if !de.IsDir() {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(base, de.Name(), "asset.json"))
			if err != nil {
				continue
			}
			var e Entry
			if json.Unmarshal(raw, &e) == nil {
				img := filepath.Join(l.root, filepath.FromSlash(e.Image))
				if _, err := os.Stat(img); err == nil {
					out[kind] = append(out[kind], e)
				}
			}
		}
		sort.Slice(out[kind], func(i, j int) bool { return out[kind][i].UsedCount > out[kind][j].UsedCount })
	}
	return out
}

// Delete 删除条目(整目录)。
func (l *Library) Delete(kind, name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	dir, err := l.entryDir(kind, name)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// ImagePath 库内图片绝对路径(供文件服务校验)。
func (l *Library) ImagePath(kind, name string) (string, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	e, img, ok := l.Match(kind, name)
	_ = e
	if !ok {
		return "", fmt.Errorf("库资产不存在")
	}
	return img, nil
}

func md5hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])[:16]
}
