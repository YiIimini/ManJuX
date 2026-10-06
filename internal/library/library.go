// Package library 跨项目资产库:角色定妆照/场景图/道具图一次入库全局复用。
// 存储:data/assets_library/{characters|scenes|props}/<safe_name>/{asset.json, image}
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
	"sync/atomic"
	"time"

	"github.com/YiIimini/ManJuX/internal/fsutil"
	"github.com/YiIimini/ManJuX/internal/project"
)

// Entry 一条库资产。
type Entry struct {
	Kind string `json:"kind"` // characters / scenes
	Name string `json:"name"`
	// ID **卡片 ID**（如 `char_bingsheng`）—— 关联的**真源**。
	//
	// 【2026-09-28 用户定调】「资产以元数据保存下来，然后直接以数据ID关联，程序里都以
	// 数据ID调用，很简单的逻辑啊，这样就不会有什么名称问题」。
	//
	// 在这之前，库条目是**按名字**建目录、按名字命中、同名覆盖：名字里多一个括号注解
	// （卡片叫「碑生（丙十七）」而提示词里写「丙十七」）、换个称呼、繁简/标点差异，全都会
	// miss 或串到别人身上；两个项目各有一个「林昭」时，后生成的会把前者的条目**改成自己的脸**。
	// 现在条目目录与命中都以 ID 为准（见 entryKey / FindForCard），Name 只是**显示名**，
	// 以及无 ID 条目（手动上传、存量）的兜底键。
	//
	// 空 = 这条没有 ID，仍走原来的名字路径（不迁移老库，也不强迫上传路径先造 ID）。
	ID string `json:"id,omitempty"`
	// Dir 这条条目**自己的目录名**（`<库根>/<kind>/<Dir>/`）。
	//
	// 为什么必须记下来（2026-09-28 审计实测）：目录名一直是从 Name 推出来的，而同名不同 ID 时
	// PutCard 会**故意**另起 `名字__ID` —— 于是每一个"按名字重推目录"的入口（重抽、删除、
	// 清角度图、别名回写、详情取图）都会落到**别人那条**上：给 B 换脸写进 A 的目录、点删 B
	// 删掉 A。记下来，所有入口一律用它，谁都不许再自己推。
	// 老条目没有这个字段 → 退回按显示名推（与过去一致，不做迁移）。
	Dir         string `json:"dir,omitempty"`
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
	// OverwroteProjects 这个条目在**跨项目**入库时覆盖掉过的项目名（去重、按时间追加）。
	//
	// 为什么必须记（2026-09-28 全面审计）：库是按 kind+name 精确匹配的，而 Put 是**同名覆盖** ——
	// 两个项目各有一个叫「林昭」的角色（网文里撞名很常见）时，B 项目一生成就把 A 的条目**改成
	// B 的脸**，A 此后的重渲会拿到 B 的脸，而两边都看不到任何异常。留下这份"被覆盖过"的名单，
	// 至少让后来的人/后来的重渲能查出来"这张脸换过主人"。
	OverwroteProjects []string `json:"overwrote_projects,omitempty"`
	// OverwriteNote 最近一次跨项目覆盖的可读说明（"这条资产原本属于《X》，已被《Y》覆盖"）。
	// 给调用方播报用，避免为了一句话改 Put 的签名。
	OverwriteNote string `json:"overwrite_note,omitempty"`
	Source        string `json:"source"` // krea2 / upload / ...
	// EntityKind **生效实体类型**(human/object/spirit_pet/beast/incorporeal/chibi)。
	// 上传图由视觉模型**看图**判定后记在这里 —— 不能拿名字猜:用户上传一张铜雀,
	// 名字叫「阿凿」,按文本推断会当成"人"去做原地转身,而器物该走转台自转。
	// 为空时调用方退回按名称/描述推断(存量条目)。
	EntityKind string    `json:"entity_kind,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UsedCount  int       `json:"used_count"` // 复用计数

	// Family 同一个角色的**造型家族基名**(「翻篇·归零」→「翻篇」;无档名时就是本名)。
	//
	// 技能侧把"造型随剧情变"落成**一角色多张卡**(主卡 + `翻篇·归零` / `糖球·雨夜` …,
	// 见 project/char_family.go)。库这边于是会同时躺着同一个人的好几档定妆照,
	// 而库里凡按"角色"聚合的地方(管理页列表、复用统计)都得能看出它们是一家人。
	//
	// **不落盘**:字段只在读路径(Match/Find/List)由条目名现算填充,写盘时恒为空串
	// (omitempty)—— 名字改了基名就跟着变,存下来只会与名字不同步。
	Family string `json:"family,omitempty"`
}

// fillFamily 按条目名现算造型家族基名(只影响返回值,不写盘)。
func (e *Entry) fillFamily() {
	if e != nil {
		e.Family = project.CharFamilyBaseName(e.Name)
	}
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

// sameVariantCard 两条条目的名字是不是**同一张卡**(同一个人、同一个造型档)。
//
// Find 里那几条"同一角色的另一种叫法"的模糊判据(名字互相包含 + 描述相似度)对
// **不同造型档**必须失效:变体卡共享同一份形象卡描述,按描述相似度命中会直接把
// 另一档的像素复制过来当这一档的定妆照 —— 造型档就永远不会出现在画面里。
// 判据用造型档名(「翻篇·归零」的「归零」):两边都叫不出档名 = 都不是变体卡,
// 按老规矩照常合并(同一角色换个称呼那种情形一点没变)。
func sameVariantCard(a, b string) bool {
	return project.CharVariantLabel(a) == project.CharVariantLabel(b)
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

// relatedGlobs 关联图的文件名模式 —— **三个后缀的唯一真源**：
//
//	*_view_*.png     多视角原图（渲染脚本契约 §2.1 的三视图/四分之三视图）
//	*_turnaround.png 转台图（pipeline/turnaround.go，H3 转身视频抽帧拼条）
//	*_board.png      三视图细节板（pipeline/board.go，官方 production reference sheet）
//
// 为什么集中成一处（2026-09-27 加板子时顺手还的债）：入库收集、换图清理、库→项目改名
// 三处原本各写一份字面量列表，加一个新后缀就要记得改三处 —— 漏一处的后果分别是
// "生成了却看不见"与"换了脸还留着旧图"，两种都不会报错。现在只有这一处。
var relatedGlobs = []string{"*_view_*.png", "*_turnaround.png", "*_board.png"}

// relatedImages 找出主图同目录的关联图:多视角原图(<base>_view_*.png)、
// 转台图(<base>_turnaround.png)与三视图细节板(<base>_board.png)。资产阶段生成时它们与定妆照
// **同目录同主名**(见 pipeline.genCharacterViews / pipeline.genCharacterBoard)。
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
	for _, suffix := range []string{"_turnaround", "_board"} {
		p := filepath.Join(dir, base+suffix+".png")
		if _, err := os.Stat(p); err == nil {
			outs = append(outs, p)
		}
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
	for _, pat := range relatedGlobs {
		if m, err := filepath.Glob(filepath.Join(dir, pat)); err == nil {
			outs = append(outs, m...)
		}
	}
	sort.Strings(outs)
	return outs
}

// Kinds 资产库的分类。**单一真源**:建目录(New)、分类校验(entryDir)、列表分组(List)
// 都从这里取 —— 此前这三处各自硬编码 "characters"/"scenes",新增一类资产要满仓找字符串,
// 漏一处就表现为"存进去了,但列表里看不见"。
//
//	characters 角色:定妆照 + 多视角原图/转台图
//	scenes     场景:一张场景图
//	props      道具:器物/机关/法宝这类既不是角色、也不是场景的东西
//	           (用户 2026-09-26:「资产库 新增一个 道具 管理页面」)
//
// Kinds 库里的分类。**music 是 2026-09-28 新增**（用户要求：生成的背景音乐也进资产库管理，
// 后续遇到相同的配乐需求直接复用）。
//
// 为什么音乐要跟角色/场景放同一个库：它们的**生命周期一样** —— 都是"一次产出、跨集跨项目反复用"，
// 而平台既有的库已经解决了复用计数、条目损坏排查、跨项目覆盖留痕这些事。
// 单独再写一套音乐库等于把这些事重做一遍，还会让"资产"这个词在界面上分裂成两处。
var Kinds = []string{"characters", "scenes", "props", "music"}

// validKind 是否为已知分类。
func validKind(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// ValidKind 分类是否合法(供 server 在解析请求时先挡一道)。
func ValidKind(kind string) bool { return validKind(kind) }

// KindLabel 分类的中文名(错误文案与提示语用;前端另有一份显示用的映射)。
func KindLabel(kind string) string {
	switch kind {
	case "characters":
		return "角色"
	case "scenes":
		return "场景"
	case "props":
		return "道具"
	case "music":
		return "配乐"
	}
	return "资产"
}

// Library 资产库(线程安全)。
type Library struct {
	mu   sync.RWMutex
	root string

	// —— 进程内条目索引（2026-09-29 性能审计 #1）——
	//
	// ## 为什么需要它
	//
	// 库里的查找此前是**每次调用都 `os.ReadDir` + 读遍该 kind 下所有 `asset.json`**
	// （`matchByNameLocked` / `matchByID` / `findWith` / `findInFamily` 四份独立扫描）。
	// 而一张卡在资产阶段要查 1~2 次，复用命中时 `BumpUsed` 与 `RelatedFiles` 还会各扫一遍
	// ⇒ **每卡 3~5 次全库扫描**。
	//
	// 实测（审计给的数据）：库 600 条（≈40 集×15 卡）时 `Match` 17.4 ms / `Find` 35.2 ms
	// ⇒ **每集白烧 1.6~2.6 秒**；3000 条（≈200 集）⇒ 每集 8~13 秒，且继续线性涨。
	// 这是全平台唯一"随总产量线性变慢"的地方。
	//
	// ## 怎么保证不读到脏数据
	//
	// 用**版本号 + 懒重建**而不是"手工维护增量索引"：
	//
	//	· 每次写（Put/SetMeta/Delete/BumpUsed/…）都 `bumpLocked()` 把 gen 加一；
	//	· 读时发现 `idxGen != gen` 就整份重建（一次 ReadDir + 读齐 asset.json）。
	//
	// 这样**即使漏标一个写点，最坏也只是多读到一次旧快照**——不，比那更安全：
	// 漏标只会让索引**迟一点失效**，而所有写方都在同一把 `mu` 下，所以
	// "写完之后立刻读"能拿到新值（写方自己会 bump，同一个进程内顺序一致）。
	// 反过来"手工增量更新"漏一处就是永久脏数据 —— 这里选了保守的那一侧。
	//
	// 【2026-09-29 补：索引自己一把锁】`idx` / `idxGen` 的**重建会发生在读路径上**
	// （第一次读、以及任何一次写之后……），而读路径只持有 `mu` 的**读锁** ——
	// 两个并发的读（界面在列资产、管线在查库）同时重建就是"并发写 map"。
	// 原来靠"读方也走写锁"是巧合而不是约定（`List` / `matchByID` / `findInFamily`
	// 都挂着 RLock），`-race` 又恰好不跑这个包 —— 属于**随时会 panic** 的那种潜伏问题。
	// 现在：索引单独一把 `idxMu`，版本号 `gen` 用原子量，读路径照常并发。
	idxMu  sync.Mutex
	idx    map[string][]libEntry
	idxGen uint64
	gen    atomic.Uint64
}

// libEntry 索引里的一条：目录名 + 已解析的条目（省掉重复 unmarshal）。
type libEntry struct {
	Dir string
	E   Entry
}

// bumpLocked 标记"库变了"（调用方须持写锁）。整份失效——只在拿不准该不该逐条更新时用。
func (l *Library) bumpLocked() { l.gen.Add(1) }

// reindexLocked 把**一个条目**的最新状态同步进索引（调用方须持写锁）。
//
// 【为什么不是"整份失效"】(2026-09-29 真机实测)
// 库的读与写在**资产阶段是交替的**：每张卡先 `FindForCard` 查库，再 `PutCard` 入库
// 或 `BumpUsed` 记一次复用。一次写就让整张索引作废的话，下一次查要重读全库 ——
// 实测 600 条、一集 15 张卡：「整份失效」648 ms/集，而**从来不加索引、每次全扫**
// 也才 195 ms/集 —— 加了索引反而更慢（这正是"索引没覆盖热路径"之外的第二种坑）。
// 只重读**这一个** asset.json（几十微秒）就既不整份重建、也不必背"永久脏数据"的风险。
//
// 与"手工增量维护"的区别：不是"记得在 N 个写点上标记一下"，而是每个写操作
// **写完盘就照着刚写下去的那份文件重建这一条** —— 一致性由这次自查保证，
// 不靠调用者的记性。删条目（盘上已无 asset.json）时它会把这一条从索引里摘掉。
func (l *Library) reindexLocked(kind, dir string) {
	l.idxMu.Lock()
	defer l.idxMu.Unlock()
	if l.idx == nil || l.idxGen != l.gen.Load() {
		return // 索引本来就不新鲜了：下一次读会整份重建，这里不必忙
	}
	base := filepath.Base(dir)
	list := l.idx[kind]
	out := make([]libEntry, 0, len(list)+1)
	for _, it := range list {
		if it.Dir != base {
			out = append(out, it)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "asset.json")); err == nil {
		var e Entry
		if json.Unmarshal(raw, &e) == nil {
			out = append(out, libEntry{Dir: base, E: e})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	l.idx[kind] = out
}

// readDirFn 目录枚举的**唯一入口**。
// 收成一个变量只有一个理由：让"读路径到底扫了几次盘"变成可机检的事实
// （见 index_scan_guard_test.go）—— 「读走索引」这件事的失败方式是**静默**的
// （代码照跑、结果也对，只是每次调用都白读一遍全库），2026-09-29 真机实测就是这么
// 漏掉 matchByIDLocked 的：那次改动把三个读入口改走索引，唯独最热的那个没改，
// 而没有任何测试会因此变红。
var readDirFn = os.ReadDir

// entriesLocked 取某个 kind 的条目列表（必要时重建索引）。
//
// **自己管 `idxMu`，只要求调用方持有 `mu`（读锁或写锁都行）**：重建是写索引，
// 而读路径只持读锁 —— 靠"读方恰好也拿着写锁"是巧合，两个并发读同时重建就是并发写 map。
func (l *Library) entriesLocked(kind string) []libEntry {
	if !validKind(kind) {
		return nil
	}
	gen := l.gen.Load()
	l.idxMu.Lock()
	defer l.idxMu.Unlock()
	if l.idx != nil && l.idxGen == gen {
		return l.idx[kind]
	}
	// 重建（整库一次；读齐所有 asset.json 比"每次查都扫"便宜得多）
	l.idx = map[string][]libEntry{}
	for _, k := range Kinds {
		dirs, err := readDirFn(filepath.Join(l.root, k))
		if err != nil {
			continue
		}
		list := make([]libEntry, 0, len(dirs))
		for _, de := range dirs {
			if !de.IsDir() {
				continue
			}
			raw, rerr := os.ReadFile(filepath.Join(l.root, k, de.Name(), "asset.json"))
			if rerr != nil {
				continue
			}
			var e Entry
			if json.Unmarshal(raw, &e) != nil {
				continue
			}
			list = append(list, libEntry{Dir: de.Name(), E: e})
		}
		l.idx[k] = list
	}
	l.idxGen = gen
	return l.idx[kind]
}

// New 打开/创建资产库。
func New(root string) (*Library, error) {
	for _, sub := range Kinds {
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

// entryKey 条目目录名：**ID 优先**。
//
// ID（卡片 ID，如 `char_bingsheng`）在同书跨集、跨项目都是稳定的，而名字会随
// 分集分镜的措辞漂移（「碑生（丙十七）」/「丙十七」/「碑生」）—— 用 ID 做目录名，
// 命中就与名字怎么写无关，同名不同 ID 的两个角色也各占一个目录、不再互相覆盖。
// 没有 ID 的条目（手动上传、老库）退回名字。
func entryKey(cardID, name string) string {
	if k := safeName(cardID); k != "" {
		return k
	}
	return safeName(name)
}

// entryDir 条目目录。key 由调用方给：有卡片 ID 时是 ID，否则是名字（见 entryKey）。
func (l *Library) entryDir(kind, key string) (string, error) {
	sn := safeName(key)
	if sn == "" {
		return "", fmt.Errorf("资产名不可用")
	}
	if !validKind(kind) {
		return "", fmt.Errorf("kind 必须是 %s", strings.Join(Kinds, "/"))
	}
	return filepath.Join(l.root, kind, sn), nil
}

// entryDirOfLocked 条目**自己**的目录（调用方须持锁）。优先用它记的 Dir；
// 老条目（没有 Dir 字段）才按显示名推 —— 与过去行为一致。
func (l *Library) entryDirOfLocked(e *Entry) (string, error) {
	key := strings.TrimSpace(e.Dir)
	if key == "" {
		key = safeName(e.Name)
	}
	return l.entryDir(e.Kind, key)
}

// looksLikeCardID 这个字符串看着像**平台自己发的卡片 id** 吗（审计 B13）。
//
// 只认平台发的几个前缀 —— 显示名（中文或人给的名字）绝不会长成这样，
// 所以"名字段里混进 id"这件事能被安全地区分出来。
//
// 【2026-09-29 补 chibi_】Q 版条目（`chibi_char_qiqi`，资产名「祁七·Q版」）此前不在这张表里，
// 于是界面按卡片 id 取图时它不被当成 id ⇒ 退回按名字找 ⇒ 名字是 id 不是名 ⇒ **404**：
// 用户看到的就是「资产库 Q版形象封面显示异常」（真机实测：`/image` 用 id **404**、用名字 **200**）。
func looksLikeCardID(s string) bool {
	s = strings.TrimSpace(s)
	for _, p := range []string{
		"char_", "char:", "prop_", "prop:", "scene_", "scene:",
		"chibi_", "chibi:", // Q 版派生条目：id 形如 chibi_char_qiqi
	} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// matchByNameLocked 按**显示名或别名**找条目（调用方须持锁）。扫目录，不走目录名。
//
// 走**进程内索引**（2026-09-29 性能审计 #1）：原来每次调用都 ReadDir + 读遍该 kind 的
// 所有 asset.json，而一张卡在资产阶段要查 1~2 次、复用命中时还会各扫一遍。
func (l *Library) matchByNameLocked(kind, name string) (*Entry, bool) {
	name = strings.TrimSpace(name)
	if name == "" || !validKind(kind) {
		return nil, false
	}
	for _, it := range l.entriesLocked(kind) {
		e := it.E
		hit := strings.TrimSpace(e.Name) == name
		if !hit {
			for _, al := range e.Aliases {
				if strings.TrimSpace(al) == name {
					hit = true
					break
				}
			}
		}
		if !hit {
			continue
		}
		e.Dir = it.Dir // 目录以**盘上事实**为准（条目里记的可能是旧值）
		e.fillFamily()
		return &e, true
	}
	return nil, false
}

// locateByNameLocked 按**名字**（界面/接口给的）定位条目目录（调用方须持锁）。
//
// 顺序：① 目录名恰好就是它 → 直接用（老库、以及"名字即目录"的条目，零成本）；
// ② 否则扫条目按显示名/别名认，用条目**自己记的 Dir** 定位。
//
// 为什么必须这样（2026-09-28 审计实测）：同名不同 ID 时 PutCard 会另起 `名字__ID` 目录，
// 而"按名字重推目录"的每一个入口（重抽、删除、清角度图、别名回写、详情取图）都会落到
// **别人那条**上 —— 给 B 换脸写进 A 的目录、点删 B 删掉 A。界面按名字访问天然有歧义，
// 但绝不能因此写到别人家里。
func (l *Library) locateByNameLocked(kind, name string) (string, error) {
	if dir, err := l.entryDir(kind, safeName(name)); err == nil {
		if _, serr := os.Stat(filepath.Join(dir, "asset.json")); serr == nil {
			return dir, nil
		}
	}
	if e, ok := l.matchByNameLocked(kind, name); ok && e != nil {
		if dir, derr := l.entryDirOfLocked(e); derr == nil {
			log.Printf("[library] 按名字「%s」定位到条目目录 %s（同名不同卡时目录会带 ID 后缀）",
				name, filepath.Base(dir))
			return dir, nil
		}
	}
	return "", fmt.Errorf("库资产不存在: %s", name)
}

// locateEntryLocked 解析出条目目录（调用方须持锁）：**id 优先**（与 Match 同一判据），
// 对不上再退回按名字/别名定位。
//
// 【2026-09-29 真机实测】`ImagePathByName` 的 `?file=` 那一路原来只按**名字**定位，
// 而界面传的是**卡片 id**（`libKey(e)` = `e.id`）—— 于是同一个角色的主图能显示、
// 详情弹窗里的第二张图（三视图板 / 多视角）**全部 404**。实测：
//
//	/api/library/characters/char_qiqi/image                              → 200
//	/api/library/characters/char_qiqi/image?file=<三视图板文件>            → 404
//
// 判据与 `Match` 完全一致（`looksLikeCardID` + 索引按 id 查），一处收口所有"按 id 取图"的路。
func (l *Library) locateEntryLocked(kind, name string) (string, error) {
	if looksLikeCardID(name) {
		if e, _, ok := l.matchByIDLocked(kind, name); ok && e != nil {
			if d := strings.TrimSpace(e.Dir); d != "" {
				if dir, err := l.entryDir(kind, d); err == nil {
					return dir, nil
				}
			}
		}
	}
	return l.locateByNameLocked(kind, name)
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
	dir, err := l.locateEntryLocked(kind, name)
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
	// **按条目自己的目录**（不是重推）：同名不同卡时目录可能叫 `名字__ID`，重推会把别人的
	// 角度图清掉（2026-09-28 审计实测）；而界面传进来的可能是**卡片 id**，只按名字定位
	// 会一个都找不到（2026-09-29 实测：抽卡时旧角度图没被清掉，详情弹窗继续展示上一张脸
	// 的角度图）—— 定位一律走 locateEntryLocked（id 优先，与 Match 同一判据）。
	dir, err := l.locateEntryLocked(kind, name)
	if err != nil {
		return
	}
	for _, pat := range relatedGlobs {
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
	// **id 优先**（2026-09-28 审计 B13）：界面那条路的 URL 段叫 `{name}`，
	// 但传进来的可能是**卡片 id** —— 而"按名字找"在**同名不同人**时只能到第一条：
	// 第二条条目在界面上访问不到，而且点删除/重抽会落到**第一条**身上。
	// 这里加一层"看着像卡片 id 就先按 id 查"，一处修、全部走这条路的路由都受益
	// （`GET/PUT/DELETE /api/library/{kind}/{name}` 与 regen/views/describe 共七条）。
	//
	// 判据 `looksLikeCardID` 只认 `char_` / `char:` / `prop_` / `scene_` 这些**平台自己发的**
	// 前缀 —— 中文名与任意显示名绝不会长成这样，所以对存量调用零影响。
	if looksLikeCardID(name) {
		if e, img, ok := l.matchByID(kind, name); ok {
			return e, img, true
		}
	}
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
	e.fillFamily()
	return &e, img, true
}

// PutInto 按**已存在条目自己的目录**写入（主图替换 / 抽卡 / 重抽走这条路）。
//
// 【为什么必须有它，2026-09-29 真机实测】`/api/library/{kind}/{name}/regen`（界面上的
// 「抽卡」）先按 id 或名字 `Match` 到条目，却拿**URL 里那一段**去做 `Put` —— 而界面传的是
// **卡片 id**（`libKey(e)` = `e.id`）。于是抽一次卡就在库里**新建**一条以 id 命名的重复条目：
// 实测 7 个角色抽一轮变 **14 条**（原条目带描述/提示词/三视图板，新条目只有一张主图、连
// `id` 都没有），此后 `Find` / 复用取到哪一条取决于目录名排序 —— 本仓审计里记过同一形态
// （「同一角色在库里变成两条，表现为随机的一致性差异」）。
//
// 与 `PutCard` 的分工：那条是**按卡片 id 认条目**（自动化链路，可能要新建）；
// 这条是**已经知道要改哪一条**（界面按 id 点进来的运营操作），一律写进那条自己的目录
// —— 目录以 `e.Dir` 为准，谁都不许按名字重推。
func (l *Library) PutInto(kind, name, description, imagePrompt, imgPath, sourceProject, source string) (*Entry, error) {
	l.mu.RLock()
	dir, err := l.locateEntryLocked(kind, name)
	cardID, display := "", ""
	if err == nil {
		if raw, rerr := os.ReadFile(filepath.Join(dir, "asset.json")); rerr == nil {
			var e Entry
			if json.Unmarshal(raw, &e) == nil {
				cardID, display = e.ID, e.Name
			}
		}
	}
	l.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(display) == "" {
		display = strings.TrimSpace(name)
	}
	return l.put(kind, filepath.Base(dir), cardID, display, description, imagePrompt, imgPath, sourceProject, source)
}

// Put 入库/更新（**无 ID 路径**：手动上传、界面操作、没有卡片的存量条目）。
//
// 自动化链路请用 PutCard —— 那一条按卡片 ID 认条目，名字怎么写都不影响命中与复用。
// **替换已有条目的图请用 PutInto** —— 它按条目自己的目录写，不会因为传了 id 而新建重复条目。
func (l *Library) Put(kind, name, description, imagePrompt, imgPath, sourceProject, source string) (*Entry, error) {
	return l.put(kind, safeName(name), "", name, description, imagePrompt, imgPath, sourceProject, source)
}

// PutCard 入库/更新（**按卡片 ID**）。这是"资产以元数据保存下来、程序里都以数据 ID 调用"
// 的落地入口（用户 2026-09-28 定调）。
//
// **同一张卡永远落在同一条条目上**：先按 ID 找回已有条目、沿用它的目录名 —— 这一集把它叫
// 「丙十七」、下一集叫「碑生（丙十七）」，既不会新建第二条，也不会互相覆盖。
// 目录仍按**显示名**建（界面照旧按名字浏览/操作），ID 只做关联；条目元数据里同时留 Name 与 ID。
func (l *Library) PutCard(kind, cardID, name, description, imagePrompt, imgPath, sourceProject, source string) (*Entry, error) {
	key := ""
	if existing, _, ok := l.matchByID(kind, cardID); ok && existing != nil {
		key = safeName(existing.Name) // 同一张卡：沿用它原有的目录（换了叫法不新建条目）
	}
	if key == "" {
		key = safeName(name)
		// 这个名字的目录被**别的卡片**占着吗（同名不同 ID）⇒ 另起一个带 ID 的目录。
		// 绝不覆盖：库里一个目录就是一张脸，覆盖一次就等于把别人的角色永久换脸
		// （2026-09-28 全面审计记过的"林昭 A 的脸被 B 覆盖"就是这个）。
		if other, _, ok := l.Match(kind, key); ok && other != nil {
			oid := strings.TrimSpace(other.ID)
			if oid != "" && oid != strings.TrimSpace(cardID) {
				key = key + "__" + safeName(cardID)
			}
		}
	}
	return l.put(kind, key, cardID, name, description, imagePrompt, imgPath, sourceProject, source)
}

func (l *Library) put(kind, key, cardID, name, description, imagePrompt, imgPath, sourceProject, source string) (*Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("资产名不可用")
	}
	dir, err := l.entryDir(kind, key)
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
	// 主图文件名按分类区分(翻库时一眼能认出这是什么):角色是定妆照 portrait,
	// 场景是 image,道具是 prop。
	imgName := "image" + ext
	switch kind {
	case "characters":
		imgName = "portrait" + ext
	case "props":
		imgName = "prop" + ext
	case "music":
		// 音乐条目的主文件叫 track.*（翻库时一眼能认出这是曲子，不是图）
		imgName = "track" + ext
	}
	_ = os.MkdirAll(dir, 0o755)
	// 清掉旧扩展图(换格式时避免双图)
	if old, err := os.ReadDir(dir); err == nil {
		for _, f := range old {
			if strings.HasPrefix(f.Name(), "portrait.") || strings.HasPrefix(f.Name(), "image.") ||
				strings.HasPrefix(f.Name(), "prop.") {
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
	prevID := ""
	var overwrote []string
	overwriteNote := ""
	if prev, err := os.ReadFile(filepath.Join(dir, "asset.json")); err == nil {
		var p Entry
		if json.Unmarshal(prev, &p) == nil {
			used = p.UsedCount
			prevFP = p.Fingerprint
			prevID = strings.TrimSpace(p.ID)
			overwrote = append(overwrote, p.OverwroteProjects...)
			// **跨项目覆盖必须留痕**（2026-09-28 全面审计）：库按 kind+name 精确匹配、Put 同名覆盖，
			// 于是"B 项目生成同名角色「林昭」"会把 A 项目的条目改成 B 的脸，A 此后重渲就拿到 B 的脸，
			// 而两边界面都看不到异常。这里记下被覆盖的项目名 + 一句可读说明，由调用方播报。
			if old := strings.TrimSpace(p.SourceProject); old != "" && old != sourceProject {
				overwriteNote = fmt.Sprintf("这条资产原本属于《%s》，已被《%s》覆盖（同名不同项目）", old, sourceProject)
				dup := false
				for _, s := range overwrote {
					if s == old {
						dup = true
						break
					}
				}
				if !dup {
					overwrote = append(overwrote, old)
				}
			}
		}
	}
	// **ID 不许被"无 ID 路径"抹掉**（2026-09-28 审计实测）：库详情的「重抽」、渲染前补齐写回、
	// 上传替换主图走的都是 Put（不带 ID）—— 照 cardID 直接写，这些日常操作会把条目的 ID
	// 清空，于是 FindForCard 的 ID 分支永久空转，名字一漂就又重出一张脸。
	// 规则：显式给了 ID 以它为准；没给就**沿用条目原有的 ID**。
	if id := strings.TrimSpace(cardID); id != "" {
		prevID = id
	}
	e := Entry{
		Kind: kind, Name: strings.TrimSpace(name), ID: prevID, Dir: key,
		Description: description, ImagePrompt: imagePrompt,
		Image:         filepath.ToSlash(filepath.Join(kind, safeName(key), imgName)),
		SourceProject: sourceProject, Source: source,
		OverwroteProjects: overwrote, OverwriteNote: overwriteNote,
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
		rel := filepath.ToSlash(filepath.Join(kind, safeName(key), bname))
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
	l.reindexLocked(kind, dir) // 只更新这一条（整份失效会让交替的读写退化成"每次全扫"）
	return &e, nil
}

// clearRelatedFiles 删除条目目录内的角度图(调用方须持有锁)。
func clearRelatedFiles(dir string) {
	for _, f := range entryRelatedImages(dir) {
		_ = os.Remove(f)
	}
}

// EntryDir 条目目录绝对路径(供调用方在库内落地新生成的角度图/转台图)。
// 定位 id 优先（界面按卡片 id 调过来的场合，只按名字会找不到）。
func (l *Library) EntryDir(kind, name string) (string, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.locateEntryLocked(kind, name)
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
	// **定位走 locateEntryLocked（id 优先）**（2026-09-30 深度审计）：界面传的是卡片 id
	// （`libKey(e)` = `e.id`），而只按名字定位在同名不同人的库里只到第一条 ——
	// 症状是"保存描述/提示词"报「库资产不存在: char_qiqi」，而同屏的抽卡/AI 生成描述能用
	// （它们走 Match，本来就是 id 优先）⇒ 看着像随机失败。判据与 Match / SetEntityKind 同一份。
	dir, err := l.locateEntryLocked(kind, name)
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
	l.reindexLocked(kind, dir)
	return &e, nil
}

// SetEntityKind 记下**看图判定的生效实体类型**(空值忽略,不覆盖已有判断)。
//
// 为什么要落库而不是每次现判:类型决定整套渲染方法(定妆照取景/多视角方式/
// 镜头身份锁),而判定要走一次视觉模型。判一次记下来,后续"补全多视角""重抽"
// 直接用,既省钱也稳定(同一张图每次判出不同类型才是灾难)。
//
// 【2026-09-29 两处修】① 定位走 `locateEntryLocked`（**id 优先**）—— 调用方之一是
// 界面（传卡片 id），只按名字定位会"库资产不存在"；② 空值分支原来调 `Match`，
// 而本函数**已持有写锁**、`Match` 又要拿读锁 —— 同一 goroutine 重入 RWMutex 是**死锁**
// （一旦有调用方传空串，请求永不返回，且整库的写锁被占着 ⇒ 资产阶段整条卡住）。
// 现在空值分支只做一次不带锁的快查。
func (l *Library) SetEntityKind(kind, name, ek string) (*Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if strings.TrimSpace(ek) == "" {
		// 空值忽略:不覆盖已有判断(调用方判不出来时不该把已知类型抹掉)
		dir, derr := l.locateEntryLocked(kind, name)
		if derr != nil {
			return nil, fmt.Errorf("库资产不存在")
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, "asset.json"))
		if rerr != nil {
			return nil, fmt.Errorf("库资产不存在")
		}
		var e Entry
		if json.Unmarshal(raw, &e) != nil {
			return nil, fmt.Errorf("库资产不存在")
		}
		return &e, nil
	}
	dir, err := l.locateEntryLocked(kind, name)
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
	l.reindexLocked(kind, dir)
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
//
// ⚠ **同一角色的不同造型档之间没有这几条模糊判据**(§4.2):变体卡共享同一份形象卡
// 描述(`翻篇·收容所` 与 `翻篇·归零` 只差尾部一行「造型档」,实测 Jaccard ≈ 0.9),
// 照旧按描述相似度命中就会把**别的造型档的像素**当成这一档的定妆照 —— 变体的服装
// 永远不会出现在画面里。要拿同族主卡当**派生起点**请走 FindInFamily(图生图),
// 那条路不复制像素。
// matchByID 按**卡片 ID** 在库里找条目（扫元数据，不依赖目录名）。
//
// 目录是按显示名建的（界面照旧按名字浏览/操作），所以 ID → 条目只能扫一遍；条目量级是
// 几十~几百，且只在资产阶段每家角色调用一次，成本可以忽略。
func (l *Library) matchByID(kind, cardID string) (*Entry, string, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.matchByIDLocked(kind, cardID)
}

func (l *Library) matchByIDLocked(kind, cardID string) (*Entry, string, bool) {
	cardID = strings.TrimSpace(cardID)
	if cardID == "" || !validKind(kind) {
		return nil, "", false
	}
	// 【2026-09-29 真机实测修】这里原来自己 `os.ReadDir` + 读遍该 kind 的所有 asset.json
	// —— 而它正是**资产阶段最热的那个调用**：`FindForCard(kind, cardID, …)`
	// （stages_assets.go 每张卡第一步就问库）先落到 matchByID，于是"库索引"
	// 那次改动漏掉的恰好是收益最大的那一路。实测（600 条真实卡片、按卡 ID 查）：
	// 单次 25~30 ms、一集 15 张卡 ≈ 210 ms 的纯白烧 —— 与审计推算的 1.6~2.6 秒/集同量级，
	// 只是发生在"按 ID 查"而不是"按名字查"上（按名字查同名目录直接命中，本来就快）。
	// 现在一律走 entriesLocked（进程内索引，见那里的版本号 + 懒重建说明）。
	for _, it := range l.entriesLocked(kind) {
		e := it.E
		if strings.TrimSpace(e.ID) != cardID {
			continue
		}
		img := filepath.Join(l.root, filepath.FromSlash(e.Image))
		if _, serr := os.Stat(img); serr != nil {
			continue // 条目没有图 = 不可复用，当作没命中（与 Match 同一口径）
		}
		e.Dir = it.Dir // 目录以**盘上事实**为准（调用方要靠它写回/清图，不许按名字重推）
		e.fillFamily()
		return &e, img, true
	}
	return nil, "", false
}

// FindForCard 自动化链路专用（资产阶段"先查库、再生成"这一步）。
//
// **先按卡片 ID 命中**，没有再退回按名字/描述匹配（手动上传的资产、没有卡片的存量条目、
// 技能侧没给 ID 的情况）。
//
// 为什么必须先在 ID 上命中（用户 2026-09-28 定调「程序里都以数据ID调用」）：名字会漂 ——
// 卡片叫「碑生（丙十七）」，某一集的分镜把它写成「丙十七」、另一集又写成「碑生」；只按名字
// 匹配的后果是同一个角色被反复当成新角色生成定妆照（2026-09-12 用户报「相同角色无中生有
// 多产生出定妆照」），跨项目重名时还会串脸。ID 不漂。
func (l *Library) FindForCard(kind, cardID, name, description string) (*Entry, string, bool) {
	if e, img, ok := l.matchByID(kind, cardID); ok {
		return e, img, true
	}
	// 【2026-09-28 审计 B11】落到模糊匹配时**必须把"id 明确不同的条目"排除掉**。
	//
	// 不排的后果：本集新角色认领库里**另一个人**的条目 —— 典型形态是「山贼甲」撞上库里的
	// 「山贼」：两者 `CharVariantLabel` 都是空（`sameVariantCard` 恒真）、名字互为子串、
	// 描述若又都是"粗布短打的中年汉子"（`descSimilar` ≥0.6）就会并上，
	// 于是这个角色被锚到别人的脸上，还会把"山贼甲"写进那条目的别名表。
	// **有 id 的场合这条根本不该发生：id 不同就是两张卡。**
	return l.findWith(kind, name, description, strings.TrimSpace(cardID))
}

// MatchCard 按卡片 ID 精确取条目（ID 优先，没有 ID / 还没入库时退回名字精确匹配）。
// 与 PutCard 配对使用：入库按 ID、回查也按 ID，中间不经过名字。
func (l *Library) MatchCard(kind, cardID, name string) (*Entry, string, bool) {
	if e, img, ok := l.matchByID(kind, cardID); ok {
		return e, img, true
	}
	return l.Match(kind, name)
}

// sameCardIDOrUnknown 两张卡的 id 是否"算同一张"。
//
// 只在这一种情形下返回 true：**对方没有 id**（老条目/手工建的）—— 那时只能按名字判。
// 两边都有 id 就必须相等；不等 = 两张卡，模糊匹配再像也不认领（2026-09-28 审计 B11）。
func sameCardIDOrUnknown(candID, selfID string) bool {
	c, s := strings.TrimSpace(candID), strings.TrimSpace(selfID)
	if s == "" {
		return true // 自己没 id：没有判据，退回老行为
	}
	if c == "" {
		return true // 对方没 id（存量条目）：同上
	}
	return c == s
}

func (l *Library) Find(kind, name, description string) (*Entry, string, bool) {
	return l.findWith(kind, name, description, "")
}

// findWith 模糊匹配的**唯一实现**；selfID 非空时跳过"id 明确不同"的条目（见 FindForCard）。
func (l *Library) findWith(kind, name, description, selfID string) (*Entry, string, bool) {
	if selfID == "" {
		if e, img, ok := l.Match(kind, name); ok {
			return e, img, true
		}
	} else if e, img, ok := l.Match(kind, name); ok && sameCardIDOrUnknown(e.ID, selfID) {
		// 精确名命中也要过 id 这一关：**同名不同人**时"名字一样"恰恰是最强的假信号。
		return e, img, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	nn := normName(name)
	type cand struct {
		entry *Entry
		img   string
		score float64
	}
	var best *cand
	// 走**进程内索引**（2026-09-29 性能审计 #1），不再每次 ReadDir + 读遍 asset.json。
	for _, it := range l.entriesLocked(kind) {
		e := it.E
		// id 明确不同 ⇒ 这不是要找的那张卡，模糊匹配再像也不认领（见 FindForCard）。
		if selfID != "" && !sameCardIDOrUnknown(e.ID, selfID) {
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
		if !hit && sameVariantCard(name, e.Name) {
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
	best.entry.fillFamily()
	// 记下这次的新叫法(下次按名即可命中),并写回条目
	if nn != normName(best.entry.Name) && strings.TrimSpace(name) != "" {
		best.entry.Aliases = append(best.entry.Aliases, strings.TrimSpace(name))
		out, _ := json.MarshalIndent(best.entry, "", "  ")
		// 写回**条目自己的目录**，不是按名字重推（2026-09-28 审计实测）：同名不同卡时目录会带
		// ID 后缀，名字推出来的那个目录可能是**别人**的 —— 覆盖过去等于把对方的条目整份换掉
		// （连它的 ID 与图片路径一起），两边都不报错。
		// 定位顺序：条目记的 Dir → 按 ID 再扫一遍（ID 是唯一不漂的键）→ 最后才退回按名字推。
		dirKey := strings.TrimSpace(best.entry.Dir)
		if dirKey == "" {
			if id := strings.TrimSpace(best.entry.ID); id != "" {
				if e2, _, ok2 := l.matchByIDLocked(kind, id); ok2 && e2 != nil && strings.TrimSpace(e2.Dir) != "" {
					dirKey = e2.Dir
				}
			}
		}
		if dirKey == "" {
			dirKey = safeName(best.entry.Name)
		}
		_ = fsutil.WriteFileAtomic(filepath.Join(l.root, kind, dirKey, "asset.json"), append(out, '\n'))
		l.reindexLocked(kind, filepath.Join(l.root, kind, dirKey))
		log.Printf("[library] 角色「%s」判定为已有条目「%s」的同一人(相似度 %.2f),已登记别名", name, best.entry.Name, best.score)
	}
	return best.entry, best.img, true
}

// FindInFamily 在同一角色的条目里找"可以当**派生起点**的那一张"(§4.1/§4.2)。
//
// 返回同族里**最靠前**的条目:主卡(名字不带档名的那条)优先,其余按名字序稳定挑选;
// 找不到同族条目时 ok=false。
//
// 为什么只能到"主卡优先"这一步:库条目**只有名字**(`char_01v4` 那种带档位号的 id
// 只活在方案里),所以「归零」与「进城」之间排不出先后 —— 名字序至少是确定性的,
// 而且它们本来就是同一个人、同一张脸,差别只在服装,拿哪一张当起点都不至于换脸。
// 项目侧那条路(见 pipeline.resolveSheetRef)有 id,能精确挑"档位号最小"的那张,优先走它。
//
// 与 Find 的分工(两条判据不能混,混了就出错):
//
//	Find         找"可以**直接复用像素**的同一张卡":只认同一档(见 sameVariantCard);
//	FindInFamily 找"可以当**图生图起点**的同族卡":同族即可,主卡优先。
//
// 它的像素**绝不允许**直接当变体卡的定妆照 —— 那是同一个人**另一套服装/状态**。
// 用途只有一处:变体卡的定妆照必须以主卡为 --init-image 派生,而不是各自文生图
// (每张卡各自文生图 → 同一角色几张脸会漂,见 pipeline/sheetderive.go)。
func (l *Library) FindInFamily(kind, name string) (*Entry, string, bool) {
	return l.findInFamily(kind, "", name)
}

// FindInFamilyForCard 带卡片 id 的家族查找（2026-09-28 审计 B12）。
//
// 与 `FindInFamily` 的差别只有一处、但很关键：**库里若有 id 相同的那一条，直接用它**。
//
// 为什么需要：`FindInFamily` 按**名字的族基名**匹配（`沈砚·少年` → `沈砚`），
// 于是两个恰好同名的不同角色会互相命中 —— 派生参考图取到**另一个人**的主卡，
// 与 B9/B11 是同一个病根（名字当键）。有 id 时这条根本不该发生。
func (l *Library) FindInFamilyForCard(kind, cardID, name string) (*Entry, string, bool) {
	if e, img, ok := l.matchByID(kind, cardID); ok {
		return e, img, true
	}
	return l.findInFamily(kind, strings.TrimSpace(cardID), name)
}

// findInFamily 家族查找的实现；selfID 非空时跳过"id 明确不同"的条目。
func (l *Library) findInFamily(kind, selfID, name string) (*Entry, string, bool) {
	fam := project.CharFamilyBaseName(name)
	if strings.TrimSpace(fam) == "" || !validKind(kind) {
		return nil, "", false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	// 走进程内索引（同 matchByIDLocked 的说明）：家族查找也在资产阶段的每张卡上跑，
	// 每次全库读一遍 asset.json 是同一笔白烧。
	var best *Entry
	var bestImg string
	for _, it := range l.entriesLocked(kind) {
		e := it.E
		if project.CharFamilyBaseName(e.Name) != fam {
			continue
		}
		// **id 明确不同 ⇒ 不是这张卡**（审计 B12）：同名不同人是两个人，
		// 派生参考图取到别人的主卡，就是"这个角色被锚到另一张脸"。
		if selfID != "" && !sameCardIDOrUnknown(e.ID, selfID) {
			continue
		}
		img := filepath.Join(l.root, filepath.FromSlash(e.Image))
		if _, serr := os.Stat(img); serr != nil {
			continue
		}
		if best == nil || familyRank(e.Name) < familyRank(best.Name) ||
			(familyRank(e.Name) == familyRank(best.Name) && e.Name < best.Name) {
			cp := e
			best, bestImg = &cp, img
		}
	}
	if best == nil {
		return nil, "", false
	}
	best.fillFamily()
	return best, bestImg, true
}

// familyRank 造型档的排序权重:**主卡最前**(0),其次档号(1,2,…)。
//
// 之所以用"名字能不能叫出档名"而不是别的:库条目只有名字(没有 char_NNvK 那种 id),
// 所以"主卡"在这里的定义就是"名字不带档名的那一条"。
func familyRank(name string) int {
	if project.CharVariantLabel(name) == "" {
		return 0
	}
	return 1
}

// BumpUsed 复用计数 +1。
func (l *Library) BumpUsed(kind, name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	dir, err := l.locateByNameLocked(kind, name)
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
	} else {
		l.reindexLocked(kind, dir) // UsedCount 变了：只更新这一条
	}
}

// List 全部条目(按分类分组)。
//
// 走**进程内索引**（2026-09-29 性能审计 #1）—— 它原来也是"每调一次读遍整库"，
// 而这个接口被资产库页面与多处装配逻辑调用。
func (l *Library) List() map[string][]Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := map[string][]Entry{}
	for _, kind := range Kinds {
		out[kind] = []Entry{}
		for _, it := range l.entriesLocked(kind) {
			e := it.E
			img := filepath.Join(l.root, filepath.FromSlash(e.Image))
			if _, err := os.Stat(img); err != nil {
				continue
			}
			e.fillFamily()
			out[kind] = append(out[kind], e)
		}
		sort.Slice(out[kind], func(i, j int) bool { return out[kind][i].UsedCount > out[kind][j].UsedCount })
	}
	return out
}

// Delete 删除条目(整目录)。
func (l *Library) Delete(kind, name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	// **定位走 locateEntryLocked（id 优先）**（2026-09-30 深度审计）：
	// 界面的垃圾桶与批量删除传的是卡片 id（`libKey(e)`），只按名字定位时
	// **同名不同人的库会删错那一条**（名字匹配只到第一条）—— 用户点第二个「老张」的垃圾桶，
	// 判据与 Match / SetEntityKind 同一份（id 优先，名字兜底）。
	dir, err := l.locateEntryLocked(kind, name)
	if err != nil {
		return err
	}
	// **索引更新要自己管**（不能用 defer）：`os.RemoveAll` 成功才该同步，
	// 而"先 defer 再 return"的写法让它变成不可达代码 —— 这次改动的第一版就写错了，
	// 是 `go build` 之后逐处核对落点才发现的。
	// reindexLocked 发现盘上已无 asset.json 时会把这一条从索引里摘掉。
	rmErr := os.RemoveAll(dir)
	if rmErr == nil {
		l.reindexLocked(kind, dir)
	}
	return rmErr
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

// MusicEntryName 把"配乐需求"归一成一个库条目名（音乐条目按**需求**存，名字就是需求本身）。
//
// 为什么用需求而不是"曲名"（2026-09-28 用户要求：生成的背景音乐进资产库管理、后续遇到
// 相同的配乐需求直接复用）：配乐没有天然的"名字" —— 决定它能不能复用的是**这段需求**
// （风格/情绪/乐器/节奏 + 项目画风）。同一份需求再出现时，直接命中同一条目、免重生成。
//
// 归一规则：去首尾空白、把连续空白与常见分隔符并成一个 "·"、截断到 60 字符。
// 归一必须**稳定**：同一句需求在不同集、不同项目里要算出同一个名字，否则复用永远不命中。
func MusicEntryName(brief string) string {
	s := strings.TrimSpace(brief)
	if s == "" {
		return ""
	}
	// **先把所有分隔性标点换成空格，再按"词"合并**——不能把标点直接换成 "·"：
	// 那样 "，x" 会产出两个分隔符（"··x"），导致"低沉竹笛与弦乐，压抑"与
	// "低沉竹笛与弦乐, 压抑"归一成两个不同的名字 ⇒ **复用永远不命中**
	// （测试 TestMusicEntryNameIsStable 就是为这条写的，第一版真被它咬住了）。
	// 用循环而不是 NewReplacer：(k,v) 成对写容易数错，第一版就是这么 panic 的
	// （strings.NewReplacer: odd argument count）。
	for _, sep := range []string{
		"，", "。", "、", "；", "：", "！", "？",
		",", ".", ";", ":", "!", "?",
		"\r", "\n", "\t",
	} {
		s = strings.ReplaceAll(s, sep, " ")
	}
	s = strings.Join(strings.Fields(s), "·")
	r := []rune(s)
	if len(r) > 60 {
		s = string(r[:60])
	}
	return strings.Trim(s, "·")
}
