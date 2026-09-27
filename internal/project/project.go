// Package project 定义 ManJuX 的项目域模型与磁盘目录规范。
//
// 单个项目目录结构(data/projects/<name>/):
//
//	project.json        项目元数据与配置
//	source.txt          输入源文本
//	plan/plan.json      LLM 直出的分镜方案(角色/场景/镜头+完整 H3 提示词)
//	assets/characters/  角色定妆照 + asset_map.json
//	assets/scenes/      场景图
//	assets/props/       道具图
//	clips/NNN.mp4       每镜渲染产物(自带 H3 原生音轨)+ manifest.json
//	output/final.mp4    合成成片
//	logs/run.log        管线运行日志
package project

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/YiIimini/ManJuX/internal/fsutil"
)

// ShotStatus 镜头渲染状态。
type ShotStatus string

// 实际会被写入的状态只有 Rendering / Done / Failed 三种,按「用不到的枚举
// 不保留」清理掉了此前的 pending / stale 两个空定义:
//   - 没有 pending 阶段:镜头条目在首次渲染前根本不存在;
//   - 没有 stale 阶段:输入指纹变化是靠 manifest 的 InputFP 比对直接判定重渲,
//     不需要单独的状态标记(见 pipeline.renderFingerprint)。
const (
	ShotRendering ShotStatus = "rendering"
	ShotDone      ShotStatus = "done"
	ShotFailed    ShotStatus = "failed"
)

// Line 一句台词。
type Line struct {
	Speaker string `json:"speaker"` // 角色名,或画外音标签(旁白/系统/画外音/内心)
	Text    string `json:"text"`
	// OS 画外音(v2,2026-09-26):这句话**不是画面里的人张嘴说的**。
	//
	// 内心独白、系统提示音、无人称旁白都属于这一类 —— 画面里的角色嘴唇必须闭合。
	// 为什么必须进契约而不是靠提示词措辞:技能端产出的 889 条台词里有 543 条是旁白,
	// 而 v1 把它们一律写成 <d>[Chinese]…</d> 塞在镜头描述里,模型只看得见"这里有句话",
	// 看不见"谁不许开口",于是找画面里的人张嘴 —— 用户报的「系统旁白,主角动嘴」。
	// 有了这一位,平台就能:①入口拦住"画外音标签却没标 os"的包;②后处理补上
	// "画面里的人嘴唇完全闭合"子句;③字幕按画外音排版。
	OS bool `json:"os,omitempty"`
}

// Shot 单个分镜。
type Shot struct {
	ID         int      `json:"id"`
	Scene      string   `json:"scene"`      // 场景 id
	Characters []string `json:"characters"` // 出场角色 id
	ShotSize   string   `json:"shot_size"`  // 景别:远景/全景/中景/近景/特写
	Camera     string   `json:"camera"`     // 运镜描述
	Action     string   `json:"action"`     // 画面动作概述(中文)
	Dialogue   []Line   `json:"dialogue"`   // 台词(逐字保留进 H3 提示词)
	Narration  string   `json:"narration"`  // 旁白(画外音)
	Duration   int      `json:"duration"`   // 秒(4-10)
	Mode       string   `json:"mode"`       // ref2va(有参考图) / t2va(纯文生)
	H3Prompt   string   `json:"h3_prompt"`  // 完整六段式/三段式提示词(LLM 直出)

	// View 本镜的**主视角/机位角度**(英文,逐字来自官方 9 视角词表,见 view_vocab.go)。
	//
	// 为什么与 Camera 分开(2026-09-27 技能侧整改单 §1.1):Camera 是**运镜**(推/拉/摇),
	// View 是**机位**(往哪看、从多高看)。原先只有 Camera,于是镜头语言退化成
	// "景别推两个运镜值"—— 最长 21 镜同景别、零机位角度,画面像用同一台机器拍完整集。
	// 空值合法(存量包):契约按 warn 提示,不拒收。
	View string `json:"view,omitempty"`

	// EndState 本镜**尾帧状态**:这一镜最后一帧的画面上有什么(人物位置/姿态/表情/
	// 视线方向/环境状态),供下一镜接着写。
	//
	// 为什么要有(整改单 §1.2):平台只有 seam 开关(把上一镜尾帧喂给下一镜),
	// 却没有任何"尾帧长什么样"的写法约束 —— 于是"接上一镜"这件事全靠模型自己猜。
	// **禁写听觉/氛围/情绪词**(寂静/压抑…)——下游画不出来,只会稀释视觉信息,
	// 判据见 view_vocab.go 的 EndStateBannedIn。跨场景切换处不要求动作连贯
	// (改用渐隐渐显 + 转场字幕),所以空值对"每段场景的首镜"是允许的。
	EndState string `json:"end_state,omitempty"`

	// HookType 本镜的**钩子类型**（受控词表，见 shot_table.go）。	//
	// 来自官方「标准镜头信息表」六列之一（`skills/3d-animation-short-generator` STEP 5），
	// 用途是**每集钩子分布自检**（STEP 5.5 检查 1）：每镜一个，每连续三镜至少一个
	// reveal/reversal/callback，开篇与收尾必须是强钩子。没有它，"节奏"就只能靠感觉说，
	// 有它之后"这一集是不是平"是可机检的。空值合法（存量包）：契约按 warn 提示。
	HookType string `json:"hook_type,omitempty"`

	// Unbound / UnboundReason 本镜的画面里有人，但**刻意不绑卡、也不给脸**。
	//
	// 为什么要有这一位（2026-09-27 技能侧回复 §6/§9）：`特写/近景却没有出场角色` 是一条门
	// （脸上特写没参考图 = 每次换一张脸），但有一类镜是**故意**这样的 —— 群演、一次性角色、
	// 只给背影/局部/人群的人。把"刻意不露脸"与"忘了绑卡"分开，门才不会永远挂着假告警。
	//
	// 取值（技能侧的 `unbound_reason`）：`no_card`（画面里的人不在本集角色表）/
	// `faceless_by_design`（有意不给脸）。**旧写法**是他们早先在提示词里写的
	// `kept off-face and unbound`（prose），平台仍认它作兜底（见 PlanUnboundFaceMarker）。
	Unbound       bool   `json:"unbound,omitempty"`
	UnboundReason string `json:"unbound_reason,omitempty"`

	// KeyframePrompt 本镜**首帧 T2I 静帧**提示词(技能侧 manju_plan.py 的
	// build_keyframe_prompt 产出)。
	//
	// 为什么平台要收下来(2026-09-26):① 之前 Plan 里没有这个字段,技能侧写进 plan.json 的
	// 首帧提示词被平台**静默丢弃** —— 导入后平台再重写一次 plan.json,它就永久没了
	// (与 props 同一类缺陷);② 它是图生图模型的输入,而图生图模型画中文几乎必然成乱码
	// 字形(实测该字段汉字 1362 → 0 是技能侧 v2.3 的清洗成果),所以契约里对它单独设门:
	// 出现任何汉字都 warn(见 plan_contract.go)—— 这是"这一镜的第一帧就带字"的可判形态。
	KeyframePrompt string `json:"keyframe_prompt,omitempty"`
}

// Character 角色卡。
//
// Kind 是**实体类型**,决定走哪一套渲染方法(定妆照取景/多视角方式/镜头身份锁都不同):
// human 人 / object 物 / spirit_pet 灵宠 / beast 妖兽 / incorporeal 无实体 / chibi Q版。
// 为什么它必须是数据而不是"渲染时猜":一只机关铜雀与一个少年,在提示词层需要的是
// 完全不同的取景与身份约束 —— 用同一套"半身正面、照抄脸"去渲染铜雀,模型只会画出
// 一个人(2026-09-24 用户实证)。**空值合法**:存量方案没有这个字段,由代码按名称与
// 描述推断(见 pipeline.ResolveEntityKind),UI 里可人工纠正。
type Character struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind,omitempty"` // human / object / spirit_pet / beast / incorporeal / chibi
	Description string `json:"description"`    // 外貌/服装/气质(供一致性)
	ImagePrompt string `json:"image_prompt"`   // 定妆照文生图提示词(英文)
	Voice       string `json:"voice"`          // 声线描述(英文,进 H3 提示词)
}

// Scene 场景卡。
type Scene struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ImagePrompt string `json:"image_prompt"`

	// Landmarks 本场景的**连续性地标**:同场景跨镜必须保持屏幕位置的固定物
	// (门框/中岛/系船桩/树…)。跨镜回到同一地点时,观众靠这些不动的东西认出"还是这儿";
	// 地标在镜头之间换位置,空间就散了。
	Landmarks string `json:"landmarks,omitempty"`

	// LightState 本场景的**光态**:日/夜 + 主光方向(如 "黄昏,主光从西侧门洞斜射")。
	// H3 每镜独立渲染,同一地点相邻两镜一个中午一个黄昏就是硬伤;写成字段后
	// 分镜与提示词都照着它写,不靠模型从上下文猜。
	LightState string `json:"light_state,omitempty"`
}

// Prop 道具卡。
//
// 技能侧原来只给 {id, name, en}(英文外观短语),口径是"道具只走文字描述、不给参考图";
// 用户 2026-09-26 定调改成**提示词也由技能侧直出**,所以这里预留 description /
// image_prompt 两个字段 —— 技能侧还没吐出来的存量包照旧能读(en 是老的 look 短语)。
//
// ⚠ 之前 Plan 里**根本没有 props 字段**:技能侧写进 plan.json 的道具被平台静默丢弃,
// 导入后若平台再重写这份 plan.json,道具信息就永久没了。字段收下来才算真读到了。
type Prop struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	EN          string `json:"en,omitempty"`           // 英文外观短语(技能侧早期的 look)
	Description string `json:"description,omitempty"`  // 中文外观/材质/配色描述
	ImagePrompt string `json:"image_prompt,omitempty"` // 道具文生图提示词(英文)
}

// Plan LLM 直出的完整分镜方案。
type Plan struct {
	Title      string      `json:"title"`
	Logline    string      `json:"logline"`
	Style      string      `json:"style"` // 视觉风格(英文,注入每镜提示词)
	Characters []Character `json:"characters"`
	Scenes     []Scene     `json:"scenes"`
	Props      []Prop      `json:"props,omitempty"`
	Shots      []Shot      `json:"shots"`

	// StrippedDirectives 本次读取时从 h3_prompt 里剔掉的"生成器指令行"条数（见 h3_clean.go）。
	// **不进 JSON**：它是读取期的诊断量，不是方案的一部分（写回磁盘会把上游文件"改脏"）。
	StrippedDirectives int `json:"-"`
}

// Asset 参考图资产。
type Asset struct {
	ID          string `json:"id"`   // 形如 char:linfan / scene:yuelai_inn
	Kind        string `json:"kind"` // characters / scenes / props
	Name        string `json:"name"`
	File        string `json:"file"` // 相对项目目录路径
	ImagePrompt string `json:"image_prompt"`
	Source      string `json:"source"`      // upload / comfy / llm-prompt
	Fingerprint string `json:"fingerprint"` // 内容指纹(变更检测)
}

// ClipManifest 镜头产物清单(断点续跑与时效管理)。
type ClipManifest struct {
	ShotID  int        `json:"shot_id"`
	Status  ShotStatus `json:"status"`
	Engine  string     `json:"engine"`   // comfy / api
	File    string     `json:"file"`     // 相对项目目录
	InputFP string     `json:"input_fp"` // 渲染输入指纹(提示词+资产+参数)
	// Length 本镜产物**实际渲出的帧数**(= duration × 24)。
	//
	// 为什么不并进 InputFP(2026-09-24 审计 P0-8):指纹串是老项目断点续跑的判据,
	// 往里加字段会让**存量项目全部重渲**。而"改了时长却没重渲"是真实故障:用户把
	// 4 秒改成 6 秒(为了装下被截断的台词),指纹不变 → 走跳过分支 → 旧片原样留着,
	// 界面还报成功。所以单独一个字段,跳过条件追加 `m.Length == shot.Duration*24`;
	// 老 manifest 里没有这个键 → Length=0 → 与任何非零时长都不等 → 正确地重渲一次
	// (改过时长的镜头本来就该重渲),此后就稳定了。
	Length int `json:"length,omitempty"`
	// Model 本镜产物的**渲染模型身份**(底模 + 加速 LoRA / 云端模型名)。
	//
	// 为什么要单独记(2026-09-25 用户问「Minimax-h3_Singularity 用起来没有?」时发现的缺口):
	// 渲染指纹里**不含模型名** —— 换底模(如 2026-09-24 换到 Singularity)之后,
	// 已渲镜头不会被判失效,于是同一集里会混着"旧模型的老镜头"和"新模型的新镜头",
	// 画风/质感不一致而没人发现。与 P0-8 同一类缺陷,处理方式也一致:
	// **不往指纹里加字段**(那会让存量项目全部重渲),而是单独记一列并纳入跳过判据;
	// 老清单没有这个键(Model=="")时不推翻指纹结论,只提示用户"这些镜头是换模型之前渲的"。
	Model string `json:"model,omitempty"`
	// SeamFrom 接缝链的**上游依赖**:渲这一镜时,上一镜的 InputFP 是什么。
	//
	// 为什么要单独记(审计:接缝链的上游依赖不在下游镜头的判据里):镜间连续是跨镜
	// 硬依赖 —— 上一镜渲完会把它的 AV latent 写进固定槽 clip_N,下一镜开头 22 帧钉的
	// 就是那份 latent。用户只重渲镜 N(改提示词/时长/档位)之后,镜 N+1 的指纹没变,
	// 于是被判「产物有效,跳过」,它钉的仍是**旧**镜 N 的尾部:接缝处画面跳变、色调
	// 突变,而成片照常合成、阶段照常报成功。
	//
	// 处理方式与 Length/Model 一致:**不往指纹里加字段**(那会让存量项目全部重渲),
	// 而是单独记一列。老清单没有这个键(空串)时不推翻指纹结论,只对"接缝确实生效且
	// 上游指纹变了"的新清单判失效。
	SeamFrom  string    `json:"seam_from,omitempty"`
	TaskID    string    `json:"task_id"` // 云端任务 id(如有)
	Error     string    `json:"error"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProjectConfig 项目级配置(创建时从全局默认拷贝,之后独立调优)。
type ProjectConfig struct {
	Params StageParams `json:"params"`
}

// Project 项目元数据。
type Project struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Config    ProjectConfig `json:"config"`

	// Chapters 本集覆盖的章号(平台在拆集时按章切片写入的真源)。
	// 为什么落盘:覆盖核对与"自动补缺"此前依赖 LLM 写在 chapters 字段里的字符串或
	// 【本集原文】是否附着成功 —— 两者都不可靠,曾据此误判缺章、删掉正确分集
	// (2026-09-12 事故:64 集被删成 63 集)。章号由平台自己记,判定才有依据。
	Chapters []int `json:"chapters,omitempty"`

	// 运行时状态(不落盘字段由 Store 维护)
	Running bool   `json:"running"`
	Phase   string `json:"phase"` // script/assets/render/qc/assemble/idle
}

// Store 项目仓库(线程安全的内存索引 + 磁盘读写)。
type Store struct {
	mu       sync.Mutex
	root     string // data/projects
	projects map[string]*Project
}

// NewStore 创建项目仓库并扫描磁盘恢复项目列表。
// NewStore 打开项目仓库:先做旧版平铺分集的一次性迁移,再扫描目录装载项目。
// 目录规范(两级):projects/普通项目/ 与 projects/书名/分集/;
// 以目录内是否存在 project.json 判定项目,书名目录无 meta 不会被误判。
func NewStore(root string) (*Store, error) {
	s := &Store{root: root, projects: map[string]*Project{}}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	s.migrateSeriesDirs()

	loadDir := func(dir string) { // 目录下所有含 project.json 的子目录装为项目
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := &Project{ID: e.Name(), Name: e.Name()}
			if err := s.loadMeta(p); err == nil {
				s.projects[p.ID] = p
			}
		}
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(full, "project.json")); err == nil {
			p := &Project{ID: e.Name(), Name: e.Name()}
			if err := s.loadMeta(p); err == nil {
				s.projects[p.ID] = p
			}
			continue
		}
		loadDir(full) // 书名目录:装载其下分集
	}
	return s, nil
}

// Root 返回项目根目录。
func (s *Store) Root() string { return s.root }

// Rescan 重扫项目根目录,把**外部新建**的项目装进内存索引,返回新增的 id 列表与总数。
//
// 为什么必须有这一个(2026-09-26 · 双侧共组):
// 技能端(ManJuNovel)现在直接产出项目包并写进 `projects/<书名>/<集名>/` —— 契约见
// `docs/渲染脚本契约.md`。而 Store 只在**进程启动时**扫一次盘,不重扫的后果是
// "技能端出好脚本了,平台端得重启才看得见" —— 链路断在这一步,双边分工就白做。
//
// 语义边界:只**新增**与**刷新元信息**,不删任何东西(盘上目录被外部删掉时,列表里
// 那条留到下次启动;这样比"运行中悄悄消失一个项目"更可预期)。已装载的项目保留
// 内存里的运行态(参数调优不会被盘上旧值顶掉)。
func (s *Store) Rescan() (added []string, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	added = []string{}
	loadIn := func(dir string) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() || s.projects[e.Name()] != nil {
				continue
			}
			p := &Project{ID: e.Name(), Name: e.Name()}
			if err := s.loadMeta(p); err == nil {
				s.projects[p.ID] = p
				added = append(added, p.ID)
			}
		}
	}
	entries, _ := os.ReadDir(s.root)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(s.root, e.Name())
		if _, err := os.Stat(filepath.Join(full, "project.json")); err == nil {
			if s.projects[e.Name()] == nil {
				p := &Project{ID: e.Name(), Name: e.Name()}
				if err := s.loadMeta(p); err == nil {
					s.projects[p.ID] = p
					added = append(added, p.ID)
				}
			}
			continue
		}
		loadIn(full) // 书名目录:装载其下分集
	}
	return added, len(s.projects)
}

// Dir 返回项目目录。
// seriesRe 「书名·第N集·标题」系列分集命名。
var seriesRe = regexp.MustCompile(`^(.+)·第\d+集`)

// Dir 项目磁盘目录:系列分集归入「书名」子目录管理
// (projects/书名/书名·第01集·标题),普通项目仍在根下。
func (s *Store) Dir(id string) string {
	if m := seriesRe.FindStringSubmatch(id); m != nil {
		return filepath.Join(s.root, m[1], id)
	}
	return filepath.Join(s.root, id)
}

func (s *Store) metaPath(id string) string {
	return filepath.Join(s.Dir(id), "project.json")
}

func (s *Store) loadMeta(p *Project) error {
	raw, err := os.ReadFile(s.metaPath(p.ID))
	if err != nil {
		return err
	}
	if err := jsonUnmarshal(string(raw), p); err != nil {
		return err
	}
	p.ensureDefaultParams()
	return nil
}

// Create 新建项目:name 仅允许中文/字母/数字/下划线/连字符。
// migrateSeriesDirs 一次性迁移:旧版平铺在 projects/ 根的分集目录
// 移入「书名」子目录(目标已存在则跳过,可安全重试)。
func (s *Store) migrateSeriesDirs() {
	ents, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		m := seriesRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		src := filepath.Join(s.root, e.Name())
		dst := filepath.Join(s.root, m[1], e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		if err := os.Rename(src, dst); err == nil {
			log.Printf("[store] 迁移分集目录:%s → %s/", e.Name(), m[1])
		}
	}
}

func (s *Store) Create(name string, cfg ProjectConfig) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("项目名不能为空")
	}
	if !validName(name) {
		return nil, fmt.Errorf("项目名仅允许中文、字母、数字、下划线、连字符")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := name
	if _, exists := s.projects[id]; exists {
		return nil, fmt.Errorf("项目 %s 已存在", name)
	}
	now := time.Now()
	p := &Project{ID: id, Name: name, CreatedAt: now, UpdatedAt: now, Config: cfg}
	for _, sub := range []string{"plan", "assets/characters", "assets/scenes", "assets/props", "clips", "output", "logs"} {
		if err := os.MkdirAll(filepath.Join(s.Dir(id), sub), 0o755); err != nil {
			return nil, err
		}
	}
	if err := s.saveMetaLocked(p); err != nil {
		return nil, err
	}
	s.projects[id] = p
	return p, nil
}

// List 返回按更新时间倒序的项目列表。
func (s *Store) List() []*Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Project, 0, len(s.projects))
	for _, p := range s.projects {
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// Get 查询项目。
func (s *Store) Get(id string) (*Project, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return nil, false
	}
	cp := *p
	return &cp, true
}

// ensureDefaultParams 读盘兜底:参数的空白补平台默认。
//
// 【为什么必须有这一处】2026-09-26 实测事故:技能端导入的 `project.json` 里
// `config.params` 是空的 `{}`(技能端只写 id/name/config,不写平台的分阶段参数)。
// 后果是一条链:
//
//	assets.source 为空 → 资产阶段**连引擎都不去要**(comfyGen 保持 nil)
//	→ 资产 0/3 却报 ✔ 就绪 → 镜头降级 t2va 纯文生 → 到渲染阶段才因"无可用引擎"失败,
//	批量产线第 1 集就断。
//
// 契约承诺"好包导入后与平台自产包**等价**",而等价的前提是**参数有默认值** ——
// 所以兜底放在**读盘**这一处(不是导入那一处):已经导进来的项目、以及任何来源的
// 老项目,都在同一处被补齐,不必逐个入口去补。
//
// 判据取两个关键字段(Assets.Source 与 Script.Styles 同时为空 = 从没配过),
// 而不是"整个结构等于零值" —— 后者要逐字段比较,参数表每加一个字段就得改一次。
func (p *Project) ensureDefaultParams() {
	if p == nil {
		return
	}
	if strings.TrimSpace(p.Config.Params.Assets.Source) != "" || len(p.Config.Params.Script.Styles) > 0 {
		return
	}
	p.Config.Params = DefaultStageParams()
}

// SaveMeta 更新项目元数据。
func (s *Store) SaveMeta(p *Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.projects[p.ID]
	if !ok {
		return fmt.Errorf("项目不存在: %s", p.ID)
	}
	p.UpdatedAt = time.Now()
	if err := s.saveMetaLocked(p); err != nil {
		return err
	}
	*cur = *p
	return nil
}

func (s *Store) saveMetaLocked(p *Project) error {
	raw, _ := jsonMarshal(p)
	return atomicWrite(s.metaPath(p.ID), raw)
}

// Delete 删除项目(整目录)。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[id]; !ok {
		return fmt.Errorf("项目不存在: %s", id)
	}
	if err := os.RemoveAll(s.Dir(id)); err != nil {
		return err
	}
	delete(s.projects, id)
	return nil
}

// ---------- 方案 / 资产 / 清单读写 ----------

// PlanPath 方案文件路径。
func (s *Store) PlanPath(id string) string {
	return filepath.Join(s.Dir(id), "plan", "plan.json")
}

// SourcePath 源文本路径。
func (s *Store) SourcePath(id string) string {
	return filepath.Join(s.Dir(id), "source.txt")
}

// SaveSource 保存源文本。
func (s *Store) SaveSource(id, text string) error {
	return atomicWrite(s.SourcePath(id), []byte(text))
}

// LoadSource 读取源文本。
func (s *Store) LoadSource(id string) (string, error) {
	raw, err := os.ReadFile(s.SourcePath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(raw), nil
}

// SavePlan 保存分镜方案。
func (s *Store) SavePlan(id string, plan *Plan) error {
	raw, _ := jsonMarshal(plan)
	return atomicWrite(s.PlanPath(id), raw)
}

// LoadPlan 读取分镜方案。
//
// 顺带做一次 mode 空值推断(见 shot_mode.go):契约把"空值"的推断交给平台,而所有
// 消费者(渲染阶段、资产范围、2K 升级)都从这里拿 plan —— 在读取边界推断一次,
// 四处同时正确。不写回磁盘,只影响本次读取的语义。
//
// 2026-09-27 晚再加一次**指令行剔除**(见 h3_clean.go):技能侧重出的包里,全书 2462 镜的
// `h3_prompt` 都混着"写给生成器的话"(REFERENCE ORDER / PICTURE MANIFEST …),而平台是把
// `shot.H3Prompt` 原样拼给模型的。同样放在这个边界上:一处清理,渲染/2K/质检四条消费路径
// 同时干净;剔除条数记在 `StrippedDirectives` 上,由协调器播报一次(不静默改数据)。
func (s *Store) LoadPlan(id string) (*Plan, error) {
	raw, err := os.ReadFile(s.PlanPath(id))
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := jsonUnmarshal(string(raw), &p); err != nil {
		return nil, err
	}
	p.NormalizeShotModes()
	for i := range p.Shots {
		cleaned, n := StripH3WriterDirectives(p.Shots[i].H3Prompt)
		if n > 0 {
			p.Shots[i].H3Prompt = cleaned
			p.StrippedDirectives += n
		}
	}
	return &p, nil
}

// ManifestPath 镜头清单路径。
func (s *Store) ManifestPath(id string) string {
	return filepath.Join(s.Dir(id), "clips", "manifest.json")
}

// LoadManifest 读取镜头清单(不存在返回空表)。
func (s *Store) LoadManifest(id string) (map[string]ClipManifest, error) {
	raw, err := os.ReadFile(s.ManifestPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]ClipManifest{}, nil
		}
		return nil, err
	}
	var m map[string]ClipManifest
	if err := jsonUnmarshal(string(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// SaveManifest 保存镜头清单。
func (s *Store) SaveManifest(id string, m map[string]ClipManifest) error {
	raw, _ := jsonMarshal(m)
	return atomicWrite(s.ManifestPath(id), raw)
}

// AssetsPath 资产映射路径。
func (s *Store) AssetsPath(id string) string {
	return filepath.Join(s.Dir(id), "assets", "asset_map.json")
}

// LoadAssets 读取资产映射(不存在返回空表)。
func (s *Store) LoadAssets(id string) (map[string]Asset, error) {
	raw, err := os.ReadFile(s.AssetsPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Asset{}, nil
		}
		return nil, err
	}
	var m map[string]Asset
	if err := jsonUnmarshal(string(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// SaveAssets 保存资产映射。
func (s *Store) SaveAssets(id string, m map[string]Asset) error {
	raw, _ := jsonMarshal(m)
	return atomicWrite(s.AssetsPath(id), raw)
}

// ---------- 工具 ----------

func validName(name string) bool {
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r >= 0x4e00 && r <= 0x9fff: // CJK 统一表意
		case r == '_', r == '-', r == '·': // 间隔号·(剧集命名「主名·第N集·标题」用)
		default:
			return false
		}
	}
	return true
}

// FingerprintFile 计算文件内容指纹(md5 前 16 位),用于资产变更检测。
func FingerprintFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := md5.Sum(raw)
	return hex.EncodeToString(sum[:])[:16]
}

// FingerprintText 计算文本指纹。
func FingerprintText(parts ...string) string {
	h := md5.Sum([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(h[:])[:16]
}

// atomicWrite 原子写。实现收敛到 fsutil,包内 5 个调用点保持原样。
// 跨项目资产库(library)也需要同一条保证,故做成公共工具而非各包各写一份。
func atomicWrite(path string, data []byte) error {
	return fsutil.WriteFileAtomic(path, data)
}
