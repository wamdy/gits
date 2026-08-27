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
	configPath = filepath.Join(cwd, ".gits", "config.json")
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
