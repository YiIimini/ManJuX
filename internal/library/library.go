// Package library 跨项目资产库:角色定妆照/场景图一次入库全局复用。
// 存储:data/assets_library/{characters|scenes}/<safe_name>/{asset.json, image}
// 匹配策略:名称精确匹配(TrimSpace);生成/上传成功的资产自动入库。
package library

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/fsutil"
)

// Entry 一条库资产。
type Entry struct {
	Kind        string `json:"kind"` // characters / scenes
	Name        string `json:"name"`
	Description string `json:"description"`
	ImagePrompt string `json:"image_prompt"`
	Image       string `json:"image"` // 主图相对库根路径(characters/x/portrait.png)
	// Images 全部图片(主图在前,随后是多视角原图与转台图)。定妆照只是角色的
	// 一个视角,视频渲染靠的是整套参考图 —— 库作为跨项目真源必须收全。
	Images        []string  `json:"images,omitempty"`
	Fingerprint   string    `json:"fingerprint"`    // 内容指纹
	SourceProject string    `json:"source_project"` // 首次入库项目
	Source        string    `json:"source"`         // krea2 / upload
	CreatedAt     time.Time `json:"created_at"`
	UsedCount     int       `json:"used_count"` // 复用计数
}

// relatedImages 找出主图同目录的关联图:多视角原图(<base>_view_*.png)与
// 转台图(<base>_turnaround.png)。资产阶段生成时它们与定妆照**同目录同主名**
// (见 pipeline.genCharacterViews,抽帧刻意不落在 _views/ 子目录里)。
//
// 注意这里的 glob **不递归**:资产阶段若把抽帧写进子目录,本函数与 ClearRelated
// 都会静默失手 —— 库收不进多视角原图,换了定妆照也清不掉旧角度图。改动产物路径
// 时必须同步改这里(TestPutCollectsRelatedImagesAndClearRelated 是这条契约的守门测试)。
func relatedImages(masterPath string) []string {
	dir := filepath.Dir(masterPath)
	base := strings.TrimSuffix(filepath.Base(masterPath), filepath.Ext(masterPath))
	outs := []string{}
	if m, err := filepath.Glob(filepath.Join(dir, base+"_view_*.png")); err == nil {
		outs = append(outs, m...)
	}
	turn := filepath.Join(dir, base+"_turnaround.png")
	if _, err := os.Stat(turn); err == nil {
		outs = append(outs, turn)
	}
	sort.Strings(outs)
	return outs
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

// ImagePathByName 取库内某张图片的绝对路径;file 为空时返回主图。
//
// file 只接受**基名**并限定图片扩展名:挡住 ../ 目录穿越,也避免误取 asset.json。
// 用于详情弹窗按名字取多视角原图/转台图。
func (l *Library) ImagePathByName(kind, name, file string) (string, error) {
	if strings.TrimSpace(file) == "" {
		return l.ImagePath(kind, name)
	}
	if filepath.Base(file) != file {
		return "", fmt.Errorf("非法文件名")
	}
	switch strings.ToLower(filepath.Ext(file)) {
	case ".png", ".jpg", ".jpeg", ".webp":
	default:
		return "", fmt.Errorf("不支持的图片格式")
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	dir, err := l.entryDir(kind, name)
	if err != nil {
		return "", err
	}
	full := filepath.Join(dir, file)
	if _, err := os.Stat(full); err != nil {
		return "", err
	}
	return full, nil
}

// ClearRelated 删除条目目录内的多视角原图与转台图。
// 重抽/替换主图后它们已与新定妆照不一致,必须一并清掉,否则详情弹窗会展示一批
// 对应上一张脸的过期角度图。与 relatedImages 同一套 glob(非递归),见那里的注释。
func (l *Library) ClearRelated(kind, name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	dir, err := l.entryDir(kind, name)
	if err != nil {
		return
	}
	for _, pat := range []string{"*_view_*.png", "*_turnaround.png"} {
		matches, gerr := filepath.Glob(filepath.Join(dir, pat))
		if gerr != nil {
			continue
		}
		for _, f := range matches {
			_ = os.Remove(f)
		}
	}
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
		return nil, "", false // 条目不存在属正常未命中,不打日志
	}
	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		// 条目存在但内容损坏:此前静默按「未命中」处理,界面上完全看不出这条坏
		// 数据,只表现为「这个角色怎么不反复用了」—— 至少要留下可排查的线索。
		log.Printf("[library] 资产条目损坏(%s/%s):%v", kind, name, err)
		return nil, "", false
	}
	// e.Image 相对库根(如 characters/林凡/portrait.png),须从 root 拼,不能再拼在条目目录下
	img := filepath.Join(l.root, filepath.FromSlash(e.Image))
	if _, err := os.Stat(img); err != nil {
		log.Printf("[library] 资产条目缺少图片文件(%s/%s → %s)", kind, name, img)
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
	if err := fsutil.WriteFileAtomic(filepath.Join(dir, imgName), raw); err != nil {
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
	// 关联图入库:多视角原图与转台图(由资产阶段生成,与定妆照同目录同主名)。
	// 一并收进来,资产库才是一个角色的**完整**参考集合,而不只是正面一张。
	imgs := []string{e.Image}
	for _, r := range relatedImages(imgPath) {
		bname := filepath.Base(r)
		if raw, err := os.ReadFile(r); err == nil {
			if fsutil.WriteFileAtomic(filepath.Join(dir, bname), raw) == nil {
				imgs = append(imgs, filepath.ToSlash(filepath.Join(kind, safeName(name), bname)))
			}
		}
	}
	e.Images = imgs
	// 指纹
	sum := md5hex(raw)
	e.Fingerprint = sum
	out, _ := json.MarshalIndent(&e, "", "  ")
	if err := fsutil.WriteFileAtomic(filepath.Join(dir, "asset.json"), append(out, '\n')); err != nil {
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
	// 复用计数不是关键数据,但写入失败也不该完全静默 —— 否则「这个角色怎么
	// 不再被复用了」会完全没有线索。原子写同时避免把 asset.json 写成半截。
	if err := fsutil.WriteFileAtomic(filepath.Join(dir, "asset.json"), append(out, '\n')); err != nil {
		log.Printf("[library] 复用计数写入失败(%s/%s):%v", kind, name, err)
	}
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
