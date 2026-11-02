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

type Config struct {
	Version      string           `json:"version"`
	Language     string           `json:"language"`
	Repositories []Repository     `json:"repositories"`
	Groups       map[string][]int `json:"groups"`
	Defaults     Defaults         `json:"defaults"`
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

	data, err := json.MarshalIndent(&stored, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
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
