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
	Speaker string `json:"speaker"` // 角色名或 "旁白"
	Text    string `json:"text"`
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
}

// Plan LLM 直出的完整分镜方案。
type Plan struct {
	Title      string      `json:"title"`
	Logline    string      `json:"logline"`
	Style      string      `json:"style"` // 视觉风格(英文,注入每镜提示词)
	Characters []Character `json:"characters"`
	Scenes     []Scene     `json:"scenes"`
	Shots      []Shot      `json:"shots"`
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
	Length    int       `json:"length,omitempty"`
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
	return jsonUnmarshal(string(raw), p)
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
func (s *Store) LoadPlan(id string) (*Plan, error) {
	raw, err := os.ReadFile(s.PlanPath(id))
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := jsonUnmarshal(string(raw), &p); err != nil {
		return nil, err
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
