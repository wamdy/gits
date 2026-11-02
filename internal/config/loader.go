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
	Version      string       `json:"version"`
	Language     string       `json:"language"`
	Repositories []Repository `json:"repositories"`
	Groups       map[string][]int `json:"groups"`
	Defaults     Defaults     `json:"defaults"`
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

	return &cfg, nil
}

func (c *Config) Save() error {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
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
