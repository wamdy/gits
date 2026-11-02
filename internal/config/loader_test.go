package config

import (
	"os"
	"path/filepath"
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
