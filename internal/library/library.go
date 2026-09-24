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
	Images []string `json:"images,omitempty"`
	// Aliases 同一角色的其它叫法。分集分镜由 LLM 独立撰写,同一角色会换个称呼
	// (机关人 / 阿枢(一三七四一号) / 机关人(一三七四一)),按名字精确匹配就会为
	// "同一个角色"反复生成定妆照(2026-09-12 用户报「相同角色无中生有多产生出定妆照」,
	// 名称一致与不一致都有)。首次命中后把新叫法记在这里,之后按名秒级复用。
	Aliases       []string `json:"aliases,omitempty"`
	Fingerprint   string   `json:"fingerprint"`    // 内容指纹
	SourceProject string   `json:"source_project"` // 首次入库项目
	Source        string   `json:"source"`         // krea2 / upload / ...
	// EntityKind **生效实体类型**(human/object/spirit_pet/beast/incorporeal/chibi)。
	// 上传图由视觉模型**看图**判定后记在这里 —— 不能拿名字猜:用户上传一张铜雀,
	// 名字叫「阿凿」,按文本推断会当成"人"去做原地转身,而器物该走转台自转。
	// 为空时调用方退回按名称/描述推断(存量条目)。
	EntityKind string    `json:"entity_kind,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UsedCount  int       `json:"used_count"` // 复用计数
}

// normName 名字归一化:去掉空格/常见标点/「的」,并把「一三七四一号」这类序号尾
// 的「号」也去掉 —— 同一个角色的两种写法归一后应当相等。
func normName(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r == ' ' || r == '\u3000' || r == '·' || r == '・' || r == '-' || r == '_':
			continue
		case r == '（' || r == '(': // 括号内容整体丢弃(序号等)
			return strings.TrimSuffix(sb.String(), "的")
		case r == '「' || r == '」' || r == '“' || r == '”' || r == '、' || r == ',':
			continue
		case r == '的':
			continue
		default:
			sb.WriteRune(r)
		}
	}
	out := strings.TrimSuffix(sb.String(), "号")
	return out
}

// bigrams 中文文本切双字组,用于描述相似度(对"多一字少一字"的改写不敏感)。
func bigrams(s string) map[string]bool {
	r := []rune(s)
	out := map[string]bool{}
	for i := 0; i+1 < len(r); i++ {
		out[string(r[i:i+2])] = true
	}
	return out
}

// descSimilar 两段角色描述是否指向同一个人(双字组 Jaccard)。
// 阈值 0.6:同一角色跨集复述几乎逐字相同(实测 Jaccard ≈ 0.95),而不同角色
// (哪怕同类型:都是老差役)通常低于 0.4 —— 中间留足余量。
func descSimilar(a, b string) float64 {
	na, nb := normName(a), normName(b)
	if len([]rune(na)) < 12 || len([]rune(nb)) < 12 {
		return 0 // 描述太短不足以判断,宁可新生成也不误并
	}
	ba, bb := bigrams(na), bigrams(nb)
	if len(ba) == 0 || len(bb) == 0 {
		return 0
	}
	inter := 0
	for k := range ba {
		if bb[k] {
			inter++
		}
	}
	union := len(ba) + len(bb) - inter
	return float64(inter) / float64(union)
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

// entryRelatedImages 条目目录内**已存在**的多视角图与转台图(不限主名)。//
// 与 relatedImages 的区别:那个按「主图主名」拼文件名(用于从项目侧收集),
// 这个只按目录内文件名模式匹配 —— 库里的关联图主名来自入库时的项目侧命名
// (如 characters_char_张三_view_side.png),与主图 portrait.png 的主名并不相同。
// 重新入库(Put)时必须靠它把既有角度图保住,否则 Images 会退化成只剩主图。
func entryRelatedImages(dir string) []string {
	outs := []string{}
	for _, pat := range []string{"*_view_*.png", "*_turnaround.png"} {
		if m, err := filepath.Glob(filepath.Join(dir, pat)); err == nil {
			outs = append(outs, m...)
		}
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
	// 保留已有 UsedCount 与指纹
	used := 0
	prevFP := ""
	if prev, err := os.ReadFile(filepath.Join(dir, "asset.json")); err == nil {
		var p Entry
		if json.Unmarshal(prev, &p) == nil {
			used = p.UsedCount
			prevFP = p.Fingerprint
		}
	}
	e := Entry{
		Kind: kind, Name: strings.TrimSpace(name),
		Description: description, ImagePrompt: imagePrompt,
		Image:         filepath.ToSlash(filepath.Join(kind, safeName(name), imgName)),
		SourceProject: sourceProject, Source: source,
		CreatedAt: time.Now(), UsedCount: used,
	}
	// 关联图入库:多视角原图与转台图(由资产阶段生成,与定妆照同目录同主名)。
	// 一并收进来,资产库才是一个角色的**完整**参考集合,而不只是正面一张。
	srcRelated := relatedImages(imgPath)
	// 角度图必须与当前主图对得上,否则详情弹窗与渲染都会拿"上一张脸"的侧面:
	//   ① 源图自带角度图 → 它们是为主图新生成的,目录里**同名之外**的(上一张脸的、
	//      别的项目命名规则留下的重复视角)让位清掉。注意不能整目录清空再拷 ——
	//      资产库自己生成多视角时(源图与条目目录就是同一个目录),清空会把刚生成
	//      的三帧连同转台图一起删掉,再拷贝时源文件已不存在,条目最终只剩一张定妆照
	//      (2026-09-12 真机实证,本测试即为此而加)。
	//   ② 主图换了(指纹变化)而源图没带角度图 → 目录里的旧角度图已过期,清掉。
	keep := map[string]bool{}
	for _, r := range srcRelated {
		keep[filepath.Base(r)] = true
	}
	if len(srcRelated) > 0 {
		for _, f := range entryRelatedImages(dir) {
			if !keep[filepath.Base(f)] {
				_ = os.Remove(f)
			}
		}
	} else if prevFP != "" && prevFP != md5hex(raw) {
		clearRelatedFiles(dir)
	}
	rest := []string{}
	seen := map[string]bool{e.Image: true}
	addRel := func(bname string) {
		rel := filepath.ToSlash(filepath.Join(kind, safeName(name), bname))
		if !seen[rel] {
			rest = append(rest, rel)
			seen[rel] = true
		}
	}
	for _, r := range srcRelated {
		bname := filepath.Base(r)
		if raw, err := os.ReadFile(r); err == nil {
			if fsutil.WriteFileAtomic(filepath.Join(dir, bname), raw) == nil {
				addRel(bname)
			}
		}
	}
	// 条目目录里已存在的角度图同样保留:本次入库的源图可能只是单张定妆照
	// (手动上传/重抽/从库复用回来的项目),若只按源图收集,Images 会退化成
	// 只剩主图 —— 详情弹窗的多视角与渲染的第二张参考都会凭空消失。
	for _, r := range entryRelatedImages(dir) {
		addRel(filepath.Base(r))
	}
	sort.Strings(rest)
	imgs := append([]string{e.Image}, rest...)
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

// clearRelatedFiles 删除条目目录内的角度图(调用方须持有锁)。
func clearRelatedFiles(dir string) {
	for _, f := range entryRelatedImages(dir) {
		_ = os.Remove(f)
	}
}

// EntryDir 条目目录绝对路径(供调用方在库内落地新生成的角度图/转台图)。
func (l *Library) EntryDir(kind, name string) (string, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.entryDir(kind, name)
}

// RelatedFiles 条目目录内已存在的多视角图与转台图绝对路径(按主图在前之外的
// 文件名排序)。库复用时靠它把整套角度图一并带进项目 —— 只带主图的话,项目里
// 的角色就只剩一张正面照,渲染拿不到第二张参考(2026-09-12 用户实证)。
func (l *Library) RelatedFiles(kind, name string) []string {
	dir, err := l.EntryDir(kind, name)
	if err != nil {
		return nil
	}
	return entryRelatedImages(dir)
}

// SetMeta 只更新中文描述与英文提示词(图片、来源、复用计数原样保留)。
// 详情弹窗里手动补全/编辑描述与提示词走这里 —— 用 Put 覆盖会把主图重新写一遍,
// 也可能因为源图路径变化丢掉角度图。
func (l *Library) SetMeta(kind, name, description, imagePrompt string) (*Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	dir, err := l.entryDir(kind, name)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "asset.json"))
	if err != nil {
		return nil, fmt.Errorf("库资产不存在")
	}
	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	if strings.TrimSpace(description) != "" {
		e.Description = strings.TrimSpace(description)
	}
	if strings.TrimSpace(imagePrompt) != "" {
		e.ImagePrompt = strings.TrimSpace(imagePrompt)
	}
	out, _ := json.MarshalIndent(&e, "", "  ")
	if err := fsutil.WriteFileAtomic(filepath.Join(dir, "asset.json"), append(out, '\n')); err != nil {
		return nil, err
	}
	return &e, nil
}

// SetEntityKind 记下**看图判定的生效实体类型**(空值忽略,不覆盖已有判断)。
//
// 为什么要落库而不是每次现判:类型决定整套渲染方法(定妆照取景/多视角方式/
// 镜头身份锁),而判定要走一次视觉模型。判一次记下来,后续"补全多视角""重抽"
// 直接用,既省钱也稳定(同一张图每次判出不同类型才是灾难)。
func (l *Library) SetEntityKind(kind, name, ek string) (*Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if strings.TrimSpace(ek) == "" {
		// 空值忽略:不覆盖已有判断(调用方判不出来时不该把已知类型抹掉)
		if e, _, ok := l.Match(kind, name); ok {
			return e, nil
		}
		return nil, fmt.Errorf("库资产不存在")
	}
	dir, err := l.entryDir(kind, name)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "asset.json"))
	if err != nil {
		return nil, fmt.Errorf("库资产不存在")
	}
	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	e.EntityKind = strings.TrimSpace(ek)
	out, _ := json.MarshalIndent(&e, "", "  ")
	if err := fsutil.WriteFileAtomic(filepath.Join(dir, "asset.json"), append(out, '\n')); err != nil {
		return nil, err
	}
	return &e, nil
}

// Find 按名字找到库条目,找不到时尝试"同一角色的另一种叫法"。
//
// 匹配顺序(命中即记录别名并返回):
//  1. 名字精确匹配(原有行为);
//  2. 名字归一化后相等(去空格/标点/「的」/「号」);
//  3. 别名表命中(之前已登记过的叫法);
//  4. 名字互为前后缀 **且** 描述相似度 ≥ 0.6(肩上小人 / 阿枢肩上小人 / 机关人的肩上小人);
//  5. 描述相似度 ≥ 0.8(名字完全不同但形象描述逐字相同:机关人 / 阿枢(一三七四一号))。
//
// 为什么必须做这一层:分集分镜由 LLM 独立撰写,同一角色换个称呼很常见,而"按名
// 精确匹配"会把同一个角色当成新角色,再生成一张定妆照 —— 用户看到的就是"相同角色
// 无中生有又多出一张定妆照"(2026-09-12 报障,名称一致与不一致都有)。
// 置信度不够(描述太短/相似度不足)时一律不合并:宁可多生成一张,也不能把两个
// 不同角色并成一个(那会让两个人共用一张脸)。
func (l *Library) Find(kind, name, description string) (*Entry, string, bool) {
	if e, img, ok := l.Match(kind, name); ok {
		return e, img, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	base := filepath.Join(l.root, kind)
	dirs, err := os.ReadDir(base)
	if err != nil {
		return nil, "", false
	}
	nn := normName(name)
	type cand struct {
		entry *Entry
		img   string
		score float64
	}
	var best *cand
	for _, de := range dirs {
		if !de.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(base, de.Name(), "asset.json"))
		if err != nil {
			continue
		}
		var e Entry
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		img := filepath.Join(l.root, filepath.FromSlash(e.Image))
		if _, err := os.Stat(img); err != nil {
			continue
		}
		sim := 0.0
		hit := false
		switch {
		case normName(e.Name) == nn && nn != "":
			hit, sim = true, 1
		default:
			for _, al := range e.Aliases {
				if normName(al) == nn && nn != "" {
					hit, sim = true, 1
					break
				}
			}
		}
		if !hit {
			ne := normName(e.Name)
			contain := len([]rune(nn)) >= 3 && ne != "" &&
				(strings.HasSuffix(ne, nn) || strings.HasSuffix(nn, ne) ||
					strings.Contains(ne, nn) || strings.Contains(nn, ne))
			d := descSimilar(e.Description, description)
			if contain && d >= 0.6 {
				hit, sim = true, 0.6+d
			} else if d >= 0.8 {
				hit, sim = true, d
			}
		}
		if hit && (best == nil || sim > best.score) {
			cp := e
			best = &cand{entry: &cp, img: img, score: sim}
		}
	}
	if best == nil {
		return nil, "", false
	}
	// 记下这次的新叫法(下次按名即可命中),并写回条目
	if nn != normName(best.entry.Name) && strings.TrimSpace(name) != "" {
		best.entry.Aliases = append(best.entry.Aliases, strings.TrimSpace(name))
		out, _ := json.MarshalIndent(best.entry, "", "  ")
		_ = fsutil.WriteFileAtomic(filepath.Join(base, safeName(best.entry.Name), "asset.json"), append(out, '\n'))
		log.Printf("[library] 角色「%s」判定为已有条目「%s」的同一人(相似度 %.2f),已登记别名", name, best.entry.Name, best.score)
	}
	return best.entry, best.img, true
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
