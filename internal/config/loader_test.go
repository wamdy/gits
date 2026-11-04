package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir string) string {
	t.Helper()
	configDir := filepath.Join(dir, ".gits")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	path := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version":"1.0.0"}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLocateConfigInCurrentDir(t *testing.T) {
	root := t.TempDir()
	want := writeConfig(t, root)

	got := locateConfig(root)
	if got != want {
		t.Errorf("locateConfig(%q) = %q, want %q", root, got, want)
	}
}

func TestLocateConfigInParentDir(t *testing.T) {
	root := t.TempDir()
	want := writeConfig(t, root)
	child := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	got := locateConfig(child)
	if got != want {
		t.Errorf("locateConfig(%q) = %q, want %q", child, got, want)
	}
}

func TestLocateConfigPrefersNearest(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root)
	child := filepath.Join(root, "a")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	want := writeConfig(t, child)
	grandchild := filepath.Join(child, "b")
	if err := os.MkdirAll(grandchild, 0755); err != nil {
		t.Fatalf("mkdir grandchild: %v", err)
	}

	got := locateConfig(grandchild)
	if got != want {
		t.Errorf("locateConfig(%q) = %q, want nearest %q", grandchild, got, want)
	}
}

func TestLocateConfigNotFoundFallsBackToStartDir(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "x", "y")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	// 限定查找上界，避免测试环境更上层目录的真实配置干扰断言
	want := filepath.Join(child, ".gits", "config.json")
	got := locateConfigUntil(child, root)
	if got != want {
		t.Errorf("locateConfigUntil(%q, %q) = %q, want %q", child, root, got, want)
	}
}

// readStoredWorktreeSets 直接读落盘 JSON 中的 worktree_sets（验证相对化与 omitempty）。
func readStoredWorktreeSets(t *testing.T, path string) []WorktreeSet {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var raw Config
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return raw.WorktreeSets
}

// TestLegacyConfigWithoutWorktreeSetsIsReadable 兼容矩阵第一行：
// 老配置（无 worktree_sets 键）在新 gits 下可读，WorktreeSets 为 nil，version 不变。
func TestLegacyConfigWithoutWorktreeSetsIsReadable(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, ".gits", "config.json")
	withConfigPath(t, cfgPath)
	mustMkdirAll(t, filepath.Dir(cfgPath))

	legacy := `{"version":"1.0.0","language":"zh","repositories":[{"id":1,"path":"main","name":"main","groups":[]}],"groups":{},"defaults":{"remote":"origin","branch":"main"}}`
	if err := os.WriteFile(cfgPath, []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() legacy: %v", err)
	}
	if loaded.WorktreeSets != nil {
		t.Errorf("WorktreeSets = %#v, want nil for legacy config", loaded.WorktreeSets)
	}
	if loaded.Version != "1.0.0" {
		t.Errorf("Version = %q, want 1.0.0（TQ-03 不 bump）", loaded.Version)
	}
	if len(loaded.Repositories) != 1 || loaded.Repositories[0].Path != filepath.Clean(filepath.Join(base, "main")) {
		t.Errorf("repositories 解析异常: %#v", loaded.Repositories)
	}
}

// TestWorktreeSetsRoundTripKeepsStoredRelativeForm 往返：成员 path 落盘相对 ConfigBaseDir
// （与 Repository.Path 同口径，TQ-02），读取解析为绝对，且 Save 不改写调用方内存对象。
func TestWorktreeSetsRoundTripKeepsStoredRelativeForm(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, ".gits", "config.json")
	withConfigPath(t, cfgPath)

	main := filepath.Join(base, "main")
	mustMkdirAll(t, main)
	wtAbs := filepath.Join(base, ".worktrees", "demo", "main")
	mustMkdirAll(t, wtAbs)

	cfg := NewDefaultConfig()
	cfg.Repositories = append(cfg.Repositories, Repository{ID: 1, Path: main, Name: "main", Groups: []string{}})
	cfg.WorktreeSets = []WorktreeSet{{
		Name:   "demo",
		Branch: "harness/demo",
		Members: []WorktreeMember{
			{RepoID: 1, Path: wtAbs},
		},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	// 内存对象不被落盘改写。
	if got := cfg.WorktreeSets[0].Members[0].Path; got != filepath.Clean(wtAbs) {
		t.Errorf("in-memory member path mutated to %q, want %q", got, filepath.Clean(wtAbs))
	}

	stored := readStoredWorktreeSets(t, cfgPath)
	if len(stored) != 1 || len(stored[0].Members) != 1 {
		t.Fatalf("stored sets = %#v, want 1 set / 1 member", stored)
	}
	if want := ".worktrees/demo/main"; stored[0].Members[0].Path != want {
		t.Errorf("stored member path = %q, want %q", stored[0].Members[0].Path, want)
	}
	if stored[0].Members[0].RepoID != 1 || stored[0].Name != "demo" || stored[0].Branch != "harness/demo" {
		t.Errorf("stored set core fields = %#v, want name=demo branch=harness/demo repo_id=1", stored[0])
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if got := loaded.WorktreeSets[0].Members[0].Path; got != filepath.Clean(wtAbs) {
		t.Errorf("resolved member path = %q, want %q", got, filepath.Clean(wtAbs))
	}
}

// TestWorktreeSetsSurviveRelocation 整目录搬迁后成员路径仍不断链（读写同源不变量）。
func TestWorktreeSetsSurviveRelocation(t *testing.T) {
	root := t.TempDir()
	base1 := filepath.Join(root, "project-a")
	main := filepath.Join(base1, "main")
	wt := filepath.Join(base1, ".worktrees", "demo", "main")
	mustMkdirAll(t, main)
	mustMkdirAll(t, wt)

	cfgPath1 := filepath.Join(base1, ".gits", "config.json")
	withConfigPath(t, cfgPath1)

	cfg := NewDefaultConfig()
	cfg.Repositories = append(cfg.Repositories, Repository{ID: 1, Path: main, Name: "main", Groups: []string{}})
	cfg.WorktreeSets = []WorktreeSet{{
		Name:    "demo",
		Branch:  "harness/demo",
		Members: []WorktreeMember{{RepoID: 1, Path: wt}},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	base2 := filepath.Join(root, "project-b")
	if err := os.Rename(base1, base2); err != nil {
		t.Fatalf("rename project dir: %v", err)
	}
	withConfigPath(t, filepath.Join(base2, ".gits", "config.json"))

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after relocation: %v", err)
	}
	want := filepath.Join(base2, ".worktrees", "demo", "main")
	if got := loaded.WorktreeSets[0].Members[0].Path; got != want {
		t.Fatalf("relocated member path = %q, want %q", got, want)
	}
}

// TestWorktreeSetsOmittedWhenNil omitempty：无集合时整键不落盘（老配置形态不噪）。
func TestWorktreeSetsOmittedWhenNil(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, ".gits", "config.json")
	withConfigPath(t, cfgPath)

	cfg := NewDefaultConfig()
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "worktree_sets") {
		t.Errorf("无集合时不应写出 worktree_sets 键:\n%s", data)
	}
}

// TestWorktreeSetCrossVolumeMemberFallsBackToAbsolute 跨盘成员路径退化为绝对路径落盘
// （#1056 既有 StoredPath 行为，不新增分支）。
func TestWorktreeSetCrossVolumeMemberFallsBackToAbsolute(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("跨卷退化是 Windows 专有场景（与 paths_test 同口径）")
	}
	base := t.TempDir()
	cfgPath := filepath.Join(base, ".gits", "config.json")
	withConfigPath(t, cfgPath)

	wt := `D:\other-volume\demo\main`
	cfg := NewDefaultConfig()
	cfg.WorktreeSets = []WorktreeSet{{
		Name:    "demo",
		Branch:  "harness/demo",
		Members: []WorktreeMember{{RepoID: 1, Path: wt}},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	stored := readStoredWorktreeSets(t, cfgPath)
	if len(stored) != 1 || len(stored[0].Members) != 1 {
		t.Fatalf("stored sets = %#v", stored)
	}
	if want := "D:/other-volume/demo/main"; stored[0].Members[0].Path != want {
		t.Errorf("cross-volume stored path = %q, want absolute %q", stored[0].Members[0].Path, want)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if got := loaded.WorktreeSets[0].Members[0].Path; got != filepath.Clean(wt) {
		t.Errorf("resolved cross-volume path = %q, want %q", got, filepath.Clean(wt))
	}
}
