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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ShotStatus 镜头渲染状态。
type ShotStatus string

const (
	ShotPending   ShotStatus = "pending"
	ShotRendering ShotStatus = "rendering"
	ShotDone      ShotStatus = "done"
	ShotFailed    ShotStatus = "failed"
	ShotStale     ShotStatus = "stale" // 输入指纹变化,产物过期
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
type Character struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"` // 外貌/服装/气质(供一致性)
	ImagePrompt string `json:"image_prompt"` // 定妆照文生图提示词(英文)
	Voice       string `json:"voice"`        // 声线描述(英文,进 H3 提示词)
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
	Title      string       `json:"title"`
	Logline    string       `json:"logline"`
	Style      string       `json:"style"` // 视觉风格(英文,注入每镜提示词)
	Characters []Character  `json:"characters"`
	Scenes     []Scene      `json:"scenes"`
	Shots      []Shot       `json:"shots"`
}

// Asset 参考图资产。
type Asset struct {
	ID          string `json:"id"`          // 形如 char:linfan / scene:yuelai_inn
	Kind        string `json:"kind"`        // characters / scenes / props
	Name        string `json:"name"`
	File        string `json:"file"`        // 相对项目目录路径
	ImagePrompt string `json:"image_prompt"`
	Source      string `json:"source"`      // upload / comfy / llm-prompt
	Fingerprint string `json:"fingerprint"` // 内容指纹(变更检测)
}

// ClipManifest 镜头产物清单(断点续跑与时效管理)。
type ClipManifest struct {
	ShotID    int       `json:"shot_id"`
	Status    ShotStatus `json:"status"`
	Engine    string    `json:"engine"` // comfy / api
	File      string    `json:"file"`   // 相对项目目录
	InputFP   string    `json:"input_fp"`  // 渲染输入指纹(提示词+资产+参数)
	TaskID    string    `json:"task_id"`   // 云端任务 id(如有)
	Error     string    `json:"error"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProjectConfig 项目级配置(创建时从全局默认拷贝,之后独立调优)。
type ProjectConfig struct {
	Params StageParams `json:"params"`
}

// Project 项目元数据。
type Project struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	Config    ProjectConfig `json:"config"`

	// 运行时状态(不落盘字段由 Store 维护)
	Running bool   `json:"running"`
	Phase   string `json:"phase"` // script/assets/render/qc/assemble/idle
}

// Store 项目仓库(线程安全的内存索引 + 磁盘读写)。
type Store struct {
	mu        sync.Mutex
	root      string // data/projects
	projects  map[string]*Project
}

// NewStore 创建项目仓库并扫描磁盘恢复项目列表。
func NewStore(root string) (*Store, error) {
	s := &Store{root: root, projects: map[string]*Project{}}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := &Project{ID: e.Name(), Name: e.Name()}
		if err := s.loadMeta(p); err == nil {
			s.projects[p.ID] = p
		}
	}
	return s, nil
}

// Root 返回项目根目录。
func (s *Store) Root() string { return s.root }

// Dir 返回项目目录。
func (s *Store) Dir(id string) string { return filepath.Join(s.root, id) }

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
		case r == '_', r == '-':
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

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
