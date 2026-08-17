package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// gitInit 在指定路径初始化一个空 Git 仓库。
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := exec.Command("git", "init", "-q", dir).Run(); err != nil {
		t.Fatalf("git init %s: %v", dir, err)
	}
}

func TestIsNoiseDir(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"node_modules", true},
		{"target", true},
		{"dist", true},
		{"build", true},
		{".next", true},
		{".venv", true},
		{"__pycache__", true},
		{"vendor", true},
		{".idea", true},
		// non-noise
		{"src", false},
		{"lib", false},
		{"my-project", false},
		{"Node_Modules", false}, // 大小写敏感验证
		{"Target", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNoiseDir(tt.name); got != tt.want {
				t.Errorf("isNoiseDir(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// TestDiscoverConcurrency 验证 Discover 的并发发现结果：
// 噪音目录被跳过、.git 正确发现、扫描根自身也是 Git 仓库时被纳入。
func TestDiscoverConcurrency(t *testing.T) {
	root := t.TempDir()

	// 创建测试仓库结构：
	// root/
	//   repo-backend/ (.git)  → 发现 ✓
	//   repo-frontend/ (.git) → 发现 ✓
	//   node_modules/         → 噪音目录，跳过子树
	//     ignored/ (.git)     → 被噪音跳过，不发现
	//   repo-tools/ (.git)    → 发现 ✓
	gitInit(t, filepath.Join(root, "repo-backend"))
	gitInit(t, filepath.Join(root, "repo-frontend"))
	gitInit(t, filepath.Join(root, "repo-tools"))
	// 噪音目录下的 .git 不应被发现
	noiseRepo := filepath.Join(root, "node_modules", "ignored")
	if err := os.MkdirAll(noiseRepo, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, noiseRepo)

	// 扫描根目录
	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover(%s) = _, %v", root, err)
	}

	// 断言：应发现 3 个仓库，且 node_modules/ignored 被跳过
	names := make([]string, 0, len(got))
	for _, r := range got {
		names = append(names, r.Name)
	}
	sort.Strings(names)

	want := []string{"repo-backend", "repo-frontend", "repo-tools"}
	if len(names) != len(want) {
		t.Fatalf("discovered repos = %v, want %v", names, want)
	}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("repo[%d] = %s, want %s", i, n, want[i])
		}
	}
}

// TestDiscoverRootIsGitRepo 验证扫描根自身是 Git 仓库时也被纳入发现结果。
func TestDiscoverRootIsGitRepo(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)

	// 根目录下再放一个子仓库
	gitInit(t, filepath.Join(root, "sub"))

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover(%s) = _, %v", root, err)
	}

	names := make([]string, 0, len(got))
	for _, r := range got {
		names = append(names, r.Name)
	}
	sort.Strings(names)

	// 根目录的 basename（即临时目录名）和子仓库都应被发现
	rootName := filepath.Base(root)
	if len(names) < 2 {
		t.Fatalf("discovered = %v, want at least 2 (root %s + sub)", names, rootName)
	}
	foundRoot := false
	foundSub := false
	for _, n := range names {
		if n == rootName {
			foundRoot = true
		}
		if n == "sub" {
			foundSub = true
		}
	}
	if !foundRoot {
		t.Errorf("root repo %q not discovered; got %v", rootName, names)
	}
	if !foundSub {
		t.Errorf("sub repo not discovered; got %v", names)
	}
}