package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Repository struct {
	ID     int      `json:"id"`
	Path   string   `json:"path"`
	Name   string   `json:"name"`
	Groups []string `json:"groups"`
	Branch string   `json:"-"` // 运行时展示用当前分支/commit，不持久化到 config.json
}

type Defaults struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
}

// WorktreeMember 集合成员：关联已登记仓库 ID 与其 worktree 目录。
// Path 与 Repository.Path 同一口径：落盘为相对 ConfigBaseDir 的相对路径（跨盘退化为绝对路径），
// 读取时解析为绝对路径（读写同源不变量，整目录搬迁不断链）。
type WorktreeMember struct {
	RepoID int    `json:"repo_id"`
	Path   string `json:"path"`
}

// WorktreeSet 多仓同名分支 worktree 的具名集合（供 `-r set:<name>` 选择与 worktree 命令族管理）。
type WorktreeSet struct {
	Name    string           `json:"name"`
	Branch  string           `json:"branch"`
	Members []WorktreeMember `json:"members"`
}

type Config struct {
	Version      string           `json:"version"`
	Language     string           `json:"language"`
	Repositories []Repository     `json:"repositories"`
	Groups       map[string][]int `json:"groups"`
	Defaults     Defaults         `json:"defaults"`
	// WorktreeSets 为可选键：老配置无该键时保持 nil，行为与引入前一致；
	// version 不因此 bump（仅追加可选键，无读取 version 的校验分支）。
	WorktreeSets []WorktreeSet `json:"worktree_sets,omitempty"`
}

var (
	configPath string
)

func init() {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	configPath = locateConfig(cwd)
}

// locateConfig 从 startDir 开始逐级向上查找 .gits/config.json，
// 找到即返回该路径；直到文件系统根目录仍未找到时，回退到 startDir 下的默认路径
// （便于首次运行在当前位置创建配置）。
func locateConfig(startDir string) string {
	return locateConfigUntil(startDir, "")
}

// locateConfigUntil 实现向上查找；stopDir 非空时只查到该目录（含）为止，
// 空值表示一直查到文件系统根目录。
func locateConfigUntil(startDir, stopDir string) string {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		dir = startDir
	}
	stop := stopDir
	if stop != "" {
		if abs, err := filepath.Abs(stop); err == nil {
			stop = abs
		}
	}

	for {
		candidate := filepath.Join(dir, ".gits", "config.json")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}

		if stop != "" && dir == stop {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return filepath.Join(startDir, ".gits", "config.json")
}

// ConfigBaseDir 返回存放 .gits 目录的父目录。
// config.json 中的仓库路径以该目录为基准存为相对路径，使整个项目目录可搬迁。
// 基准锚定 configPath（由 locateConfig 就近定位到的那一份配置）而非启动时的 cwd：
// 配置在上级目录被命中时，相对路径也随之改挂到真正持有 .gits 的那一层，读写同源不变。
func ConfigBaseDir() string {
	base := filepath.Dir(filepath.Dir(configPath))
	if abs, err := filepath.Abs(base); err == nil {
		return abs
	}
	return base
}

// ResolvePath 把落盘路径解析为绝对路径。
// 绝对路径（存量配置）保持原样；相对路径基于 ConfigBaseDir 解析。
func ResolvePath(p string) string {
	return resolvePath(ConfigBaseDir(), p)
}

func resolvePath(baseDir, p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(p)))
}

// StoredPath 把绝对路径转换为落盘形态：优先相对 ConfigBaseDir，无法相对化时保留绝对路径。
// 落盘统一使用 / 分隔符，读取时由 ResolvePath 兼容 / 与平台原生分隔符。
func StoredPath(p string) string {
	return storedPath(ConfigBaseDir(), p)
}

func storedPath(baseDir, p string) string {
	if p == "" {
		return ""
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = resolvePath(baseDir, abs)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(baseDir, abs)
	if err != nil {
		// 跨卷/跨 UNC 共享等无法相对化：保留绝对路径，但仍统一分隔符。
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

func Load() (*Config, error) {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return NewDefaultConfig(), nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if cfg.Groups == nil {
		cfg.Groups = make(map[string][]int)
	}
	if cfg.Repositories == nil {
		cfg.Repositories = make([]Repository, 0)
	}

	// 落盘可为相对路径（相对 .gits 所在目录）或存量绝对路径；
	// 内存中统一解析为绝对路径，下游执行与比较逻辑无需感知存储形态。
	for i := range cfg.Repositories {
		cfg.Repositories[i].Path = ResolvePath(cfg.Repositories[i].Path)
	}

	// worktree 集合成员路径与仓库路径同一口径：读取时一律解析为绝对路径。
	for i := range cfg.WorktreeSets {
		for j := range cfg.WorktreeSets[i].Members {
			cfg.WorktreeSets[i].Members[j].Path = ResolvePath(cfg.WorktreeSets[i].Members[j].Path)
		}
	}

	return &cfg, nil
}

func (c *Config) Save() error {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// 落盘时相对化，但不修改调用方内存对象（保存后仍可继续使用绝对路径）。
	stored := *c
	stored.Repositories = make([]Repository, len(c.Repositories))
	for i, repo := range c.Repositories {
		repo.Path = StoredPath(repo.Path)
		stored.Repositories[i] = repo
	}

	// worktree 集合同样走 StoredPath/ResolvePath；nil 时由 omitempty 整键不写。
	if c.WorktreeSets != nil {
		stored.WorktreeSets = make([]WorktreeSet, len(c.WorktreeSets))
		for i, set := range c.WorktreeSets {
			set.Members = make([]WorktreeMember, len(c.WorktreeSets[i].Members))
			for j, member := range c.WorktreeSets[i].Members {
				member.Path = StoredPath(member.Path)
				set.Members[j] = member
			}
			stored.WorktreeSets[i] = set
		}
	}

	data, err := json.MarshalIndent(&stored, "", "  ")
	if err != nil {
		return err
	}

	// 原子写：先落同目录临时文件再 rename 覆盖（评审 S-02）。`worktree add` 阶段 C 把
	// 「仓库条目 + 集合登记」写进同一次 Save，直接 WriteFile 半写会同时毁掉两者，
	// 临时文件 + rename 让读方只看到「旧配置」或「新配置」两种完整状态。
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, configPath); err != nil {
		_ = os.Remove(tmp) // rename 失败时不留半写残留，原配置未被动过
		return err
	}
	return nil
}

func ConfigExists() bool {
	_, err := os.Stat(configPath)
	return !os.IsNotExist(err)
}

func NewDefaultConfig() *Config {
	return &Config{
		Version:      "1.0.0",
		Repositories: make([]Repository, 0),
		Groups:       make(map[string][]int),
		Defaults: Defaults{
			Remote: "origin",
			Branch: "main",
		},
	}
}

func GetConfigPath() string {
	return configPath
}

func GetRawConfig() ([]byte, error) {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		cfg := NewDefaultConfig()
		return json.MarshalIndent(cfg, "", "  ")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return json.MarshalIndent(cfg, "", "  ")
}

// NormalizePaths 显式把 config.json 中的仓库路径规范化为相对形态。
// 返回落盘值发生变化的仓库数量；配置文件不存在时不创建文件。
func NormalizePaths() (int, error) {
	if !ConfigExists() {
		return 0, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return 0, err
	}

	var raw Config
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, err
	}

	changed := 0
	for _, repo := range raw.Repositories {
		if normalized := StoredPath(ResolvePath(repo.Path)); normalized != repo.Path {
			changed++
		}
	}
	if changed == 0 {
		return 0, nil
	}

	cfg, err := Load()
	if err != nil {
		return 0, err
	}
	if err := cfg.Save(); err != nil {
		return 0, err
	}

	return changed, nil
}
