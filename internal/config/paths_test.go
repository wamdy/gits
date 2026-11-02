package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func withConfigPath(t *testing.T, path string) {
	t.Helper()
	old := configPath
	configPath = path
	t.Cleanup(func() { configPath = old })
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func readStoredRepositories(t *testing.T, path string) []Repository {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var raw Config
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return raw.Repositories
}

func TestStoredPathAndResolvePath(t *testing.T) {
	base := t.TempDir()
	abs := filepath.Join(base, "repos", "r1")

	if got, want := storedPath(base, abs), "repos/r1"; got != want {
		t.Fatalf("storedPath() = %q, want %q", got, want)
	}
	if got, want := resolvePath(base, "repos/r1"), filepath.Clean(abs); got != want {
		t.Fatalf("resolvePath() = %q, want %q", got, want)
	}

	// 已落盘的绝对路径（存量配置）原样保留。
	if got := resolvePath(base, abs); got != filepath.Clean(abs) {
		t.Fatalf("resolvePath(absolute) = %q, want %q", got, filepath.Clean(abs))
	}

	// 允许 .. 逃逸到基目录之外，仍可往返。
	outside := filepath.Join(filepath.Dir(base), "shared", "r2")
	rel, err := filepath.Rel(base, outside)
	if err != nil {
		t.Skipf("base and outside are not relatable on this platform: %v", err)
	}
	stored := storedPath(base, outside)
	if want := filepath.ToSlash(rel); stored != want {
		t.Fatalf("storedPath(outside) = %q, want %q", stored, want)
	}
	if got := resolvePath(base, stored); got != filepath.Clean(outside) {
		t.Fatalf("resolvePath(stored outside) = %q, want %q", got, filepath.Clean(outside))
	}
}

func TestStoredPathCrossVolumeFallback(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cross-volume fallback is Windows-specific")
	}
	base := `C:\tmp\gits-base`
	target := `D:\repos\r1`
	if got, want := storedPath(base, target), "D:/repos/r1"; got != want {
		t.Fatalf("storedPath(cross-volume) = %q, want absolute %q", got, want)
	}
}

func TestSaveLoadRoundTripAndRelocation(t *testing.T) {
	root := t.TempDir()
	base1 := filepath.Join(root, "project-a")
	repo := filepath.Join(base1, "repos", "r1")
	mustMkdirAll(t, repo)

	cfgPath1 := filepath.Join(base1, ".gits", "config.json")
	withConfigPath(t, cfgPath1)

	cfg := NewDefaultConfig()
	cfg.Repositories = append(cfg.Repositories, Repository{
		ID: 1, Path: repo, Name: "r1", Groups: []string{},
	})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	stored := readStoredRepositories(t, cfgPath1)
	if len(stored) != 1 {
		t.Fatalf("stored repositories = %d, want 1", len(stored))
	}
	if stored[0].Path != "repos/r1" {
		t.Fatalf("stored path = %q, want %q", stored[0].Path, "repos/r1")
	}

	// 保存不应改写调用方内存中的绝对路径。
	if cfg.Repositories[0].Path != filepath.Clean(repo) {
		t.Fatalf("in-memory path mutated to %q", cfg.Repositories[0].Path)
	}

	// 整体移动项目目录（含 .gits 与 repos）后重新加载。
	base2 := filepath.Join(root, "project-b")
	if err := os.Rename(base1, base2); err != nil {
		t.Fatalf("rename project dir: %v", err)
	}
	withConfigPath(t, filepath.Join(base2, ".gits", "config.json"))

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after relocation: %v", err)
	}
	want := filepath.Join(base2, "repos", "r1")
	if got := loaded.Repositories[0].Path; got != want {
		t.Fatalf("resolved path after relocation = %q, want %q", got, want)
	}
}

func TestLegacyAbsoluteConfigIsResolvedAndNormalized(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repos", "r1")
	mustMkdirAll(t, repo)

	cfgPath := filepath.Join(base, ".gits", "config.json")
	withConfigPath(t, cfgPath)
	mustMkdirAll(t, filepath.Dir(cfgPath))

	legacy := NewDefaultConfig()
	legacy.Repositories = append(legacy.Repositories, Repository{
		ID: 1, Path: repo, Name: "r1", Groups: []string{},
	})
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy config: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() legacy: %v", err)
	}
	if got := loaded.Repositories[0].Path; got != filepath.Clean(repo) {
		t.Fatalf("legacy resolved path = %q, want %q", got, filepath.Clean(repo))
	}

	loaded.Repositories[0].Name = "renamed"
	if err := loaded.Save(); err != nil {
		t.Fatalf("Save() normalized: %v", err)
	}
	stored := readStoredRepositories(t, cfgPath)
	if stored[0].Path != "repos/r1" {
		t.Fatalf("normalized stored path = %q, want %q", stored[0].Path, "repos/r1")
	}
}

func TestNormalizePaths(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, ".gits", "config.json")
	withConfigPath(t, cfgPath)

	// 无配置文件：返回 0 且不创建文件。
	changed, err := NormalizePaths()
	if err != nil {
		t.Fatalf("NormalizePaths() without config: %v", err)
	}
	if changed != 0 {
		t.Fatalf("changed = %d, want 0", changed)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("config file should not be created, stat err = %v", err)
	}

	// 存量绝对路径：改写 1 条并落盘为相对形态。
	repo := filepath.Join(base, "repos", "r1")
	mustMkdirAll(t, repo)
	mustMkdirAll(t, filepath.Dir(cfgPath))
	legacy := NewDefaultConfig()
	legacy.Repositories = append(legacy.Repositories, Repository{
		ID: 1, Path: repo, Name: "r1", Groups: []string{},
	})
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy config: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	changed, err = NormalizePaths()
	if err != nil {
		t.Fatalf("NormalizePaths(): %v", err)
	}
	if changed != 1 {
		t.Fatalf("changed = %d, want 1", changed)
	}
	stored := readStoredRepositories(t, cfgPath)
	if stored[0].Path != "repos/r1" {
		t.Fatalf("normalized stored path = %q, want %q", stored[0].Path, "repos/r1")
	}

	// 已是相对形态：再次规范化应为 0 改写。
	changed, err = NormalizePaths()
	if err != nil {
		t.Fatalf("NormalizePaths() idempotent: %v", err)
	}
	if changed != 0 {
		t.Fatalf("second normalize changed = %d, want 0", changed)
	}
}

func TestRenameRepositoryByRelativePath(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repos", "r1")
	mustMkdirAll(t, repo)

	withConfigPath(t, filepath.Join(base, ".gits", "config.json"))
	cfg := NewDefaultConfig()
	cfg.Repositories = append(cfg.Repositories, Repository{
		ID: 1, Path: repo, Name: "r1", Groups: []string{},
	})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	// 本包目前没有 t.Parallel()；Chdir 会改变进程级 cwd，新增并行用例前须移除本测试的并发假设。
	if err := os.Chdir(base); err != nil {
		t.Fatalf("Chdir(): %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	if err := RenameRepository(filepath.Join("repos", "r1"), "renamed"); err != nil {
		t.Fatalf("RenameRepository() by relative path: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if loaded.Repositories[0].Name != "renamed" {
		t.Fatalf("repository name = %q, want renamed", loaded.Repositories[0].Name)
	}
}

func TestGetRawConfigKeepsStoredForm(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repos", "r1")
	mustMkdirAll(t, repo)

	cfgPath := filepath.Join(base, ".gits", "config.json")
	withConfigPath(t, cfgPath)
	cfg := NewDefaultConfig()
	cfg.Repositories = append(cfg.Repositories, Repository{
		ID: 1, Path: repo, Name: "r1", Groups: []string{},
	})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	data, err := GetRawConfig()
	if err != nil {
		t.Fatalf("GetRawConfig(): %v", err)
	}
	if !strings.Contains(string(data), `"path": "repos/r1"`) {
		t.Fatalf("raw config does not keep relative path: %s", string(data))
	}
}
