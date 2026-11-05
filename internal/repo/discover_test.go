package repo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// gitInit 在指定路径初始化一个空 Git 仓库。
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := exec.Command("git", "init", "-q", dir).Run(); err != nil {
		t.Fatalf("git init %s: %v", dir, err)
	}
}

// gitCommitIn 在 dir 内提交一次空提交：worktree add 需要一个有效分支点。
func gitCommitIn(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git",
		"-c", "user.email=gits-test@example.com", "-c", "user.name=gits-test",
		"-c", "commit.gpgsign=false",
		"commit", "-q", "--allow-empty", "-m", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit in %s: %v\n%s", dir, err, out)
	}
}

// gitWorktreeAdd 从主仓拉一个 linked worktree（`.git` 落为文件）。
func gitWorktreeAdd(t *testing.T, mainRepo, worktreeDir, branch string) {
	t.Helper()
	cmd := exec.Command("git", "-c", "user.email=gits-test@example.com", "-c", "user.name=gits-test",
		"worktree", "add", "-b", branch, worktreeDir)
	cmd.Dir = mainRepo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add %s (from %s): %v\n%s", worktreeDir, mainRepo, err, out)
	}
}

// gitSeparateGitDirCheckout 构造与 submodule 同形态的 `.git` 文件目录：
// git-dir 与 common-dir 同指独立目录（判定底座的 KindSubmodule 分支）。
func gitSeparateGitDirCheckout(t *testing.T, workDir, gitDir string) {
	t.Helper()
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("mkdir git dir %s: %v", gitDir, err)
	}
	cmd := exec.Command("git", "init", "-q", "--separate-git-dir="+gitDir, workDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --separate-git-dir %s: %v\n%s", workDir, err, out)
	}
}

// sameDir（路径比较，Windows 大小写不敏感）沿用 manager.go 的生产实现，测试不再自带副本。

// byBaseName 按目录名索引发现结果，便于断言。
func byBaseName(repos []RepositoryInfo) map[string]RepositoryInfo {
	m := make(map[string]RepositoryInfo, len(repos))
	for _, r := range repos {
		m[filepath.Base(filepath.FromSlash(r.Path))] = r
	}
	return m
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

// TestDiscoverLinkedWorktreeIsFound 验证 AC-01.1 的根因修复：
// `.git` 是文件（linked worktree）不再被 IsDir 门吞掉，主仓与 worktree 都进候选，
// 且 worktree 带 WorktreeOf（主仓路径）供列出口径反查主仓名。
func TestDiscoverLinkedWorktreeIsFound(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "repo-main")
	gitInit(t, main)
	gitCommitIn(t, main)

	wt := filepath.Join(root, ".worktrees", "demo", "wt-main")
	if err := os.MkdirAll(filepath.Dir(wt), 0755); err != nil {
		t.Fatal(err)
	}
	gitWorktreeAdd(t, main, wt, "feature/x")

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover(%s) = _, %v", root, err)
	}

	byName := byBaseName(got)
	if len(got) != 2 {
		t.Fatalf("discovered = %d 条 %v, want 2（主仓 + linked worktree）", len(got), got)
	}
	mainInfo, ok := byName["repo-main"]
	if !ok {
		t.Fatalf("主仓未被发现: %v", got)
	}
	if mainInfo.WorktreeOf != "" {
		t.Errorf("主仓 WorktreeOf = %q, want empty", mainInfo.WorktreeOf)
	}

	wtInfo, ok := byName["wt-main"]
	if !ok {
		t.Fatalf("linked worktree %s 未被发现: %v", wt, got)
	}
	if !sameDir(wtInfo.WorktreeOf, main) {
		t.Errorf("worktree WorktreeOf = %q, want 主仓 %q", wtInfo.WorktreeOf, main)
	}
	if wtInfo.Branch != "feature/x" {
		t.Errorf("worktree Branch = %q, want feature/x", wtInfo.Branch)
	}
}

// TestDiscoverSkipsSubmodule 验证 AC-01.2 的 Q5 口径：submodule（`.git` 文件且
// git-dir == common-dir）不进候选、不报错。
func TestDiscoverSkipsSubmodule(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "repo-main")
	gitInit(t, main)
	gitCommitIn(t, main)

	sub := filepath.Join(root, "loose-sub")
	subGit := filepath.Join(root, "modules", "loose-sub")
	if err := os.MkdirAll(filepath.Dir(subGit), 0755); err != nil {
		t.Fatal(err)
	}
	gitSeparateGitDirCheckout(t, sub, subGit)

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover(%s) = _, %v（submodule 不应导致报错）", root, err)
	}
	if len(got) != 1 {
		t.Fatalf("discovered = %d 条 %v, want 1（只有主仓）", len(got), got)
	}
	if !sameDir(got[0].Path, main) {
		t.Errorf("discovered[0].Path = %q, want 主仓 %q", got[0].Path, main)
	}

	// 直接扫描 submodule 检出目录：0 候选，仍不报错。
	only, err := Discover(sub)
	if err != nil {
		t.Fatalf("Discover(submodule) = _, %v", err)
	}
	if len(only) != 0 {
		t.Errorf("Discover(submodule) = %v, want 空", only)
	}
}

// TestDiscoverWorktreeAndSubmoduleMixed 三态同现于一次扫描：主仓 + worktree 进候选、
// submodule 被跳过（AC-01.1 / AC-01.2 组合口径）。
func TestDiscoverWorktreeAndSubmoduleMixed(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "repo-main")
	gitInit(t, main)
	gitCommitIn(t, main)

	wt := filepath.Join(root, ".worktrees", "demo", "wt-a")
	if err := os.MkdirAll(filepath.Dir(wt), 0755); err != nil {
		t.Fatal(err)
	}
	gitWorktreeAdd(t, main, wt, "feature/y")

	sub := filepath.Join(root, ".worktrees", "demo", "loose-sub")
	gitSeparateGitDirCheckout(t, sub, filepath.Join(root, "modules", "loose-sub"))

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover(%s) = _, %v", root, err)
	}

	names := make([]string, 0, len(got))
	for _, r := range got {
		names = append(names, r.Name)
	}
	sort.Strings(names)

	want := []string{"repo-main", "wt-a"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("discovered = %v, want %v（submodule 应被跳过）", names, want)
	}
	for _, r := range got {
		switch r.Name {
		case "wt-a":
			if !sameDir(r.WorktreeOf, main) {
				t.Errorf("wt-a WorktreeOf = %q, want %q", r.WorktreeOf, main)
			}
		case "repo-main":
			if r.WorktreeOf != "" {
				t.Errorf("repo-main WorktreeOf = %q, want empty", r.WorktreeOf)
			}
		}
	}
}

// TestDiscoverManyCandidatesNoDeadlock 回归 #1070：候选数越过两个通道容量之和
// （found 64 + results 64 + 8 个在途 worker ≈ 136）时，遍历与结果消费必须并行推进。
// 旧实现把遍历放在调用协程、结果只在遍历结束后才收，此处会以
// `fatal error: all goroutines are asleep - deadlock` 崩溃（exit 2、零业务输出）。
func TestDiscoverManyCandidatesNoDeadlock(t *testing.T) {
	const candidates = 200 // 明确高于 136 的触发水位
	root := t.TempDir()
	// 只造 `.git` 目录占位、不跑 git init：本用例验证的是协程编排，不是采集结果，
	// 采集函数对非法仓库返回空串，不影响候选计数与判据。
	for i := 0; i < candidates; i++ {
		gitDir := filepath.Join(root, fmt.Sprintf("r%03d", i), ".git")
		if err := os.MkdirAll(gitDir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", gitDir, err)
		}
	}

	type outcome struct {
		repos []RepositoryInfo
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		repos, err := Discover(root)
		done <- outcome{repos: repos, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Discover(%s) = _, %v", root, got.err)
		}
		if len(got.repos) != candidates {
			t.Fatalf("discovered = %d 条, want %d 条（候选不得丢失）", len(got.repos), candidates)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("Discover 在 %d 个候选上超时未返回：遍历与结果消费互堵（#1070 回归）", candidates)
	}
}

// TestDiscoverPropagatesWalkError 验证并发化之后遍历错误仍然上抛（#1070 AC-4）：
// walkErr 经 walkDone 单值通道回传，不存在「协程把错误吞掉、调用方拿到空结果当成功」的形态。
func TestDiscoverPropagatesWalkError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	repos, err := Discover(missing)
	if err == nil {
		t.Fatalf("Discover(%s) = %d 条, nil；遍历错误未上抛", missing, len(repos))
	}
	if repos != nil {
		t.Errorf("Discover(%s) 出错时 repos = %v, want nil", missing, repos)
	}
}

// TestGetRepositoryInfoWorktreeOf 验证单仓信息采集同样带出 worktree 归属。
func TestGetRepositoryInfoWorktreeOf(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "repo-main")
	gitInit(t, main)
	gitCommitIn(t, main)
	wt := filepath.Join(root, "wt-main")
	gitWorktreeAdd(t, main, wt, "feature/z")

	info, err := GetRepositoryInfo(wt)
	if err != nil {
		t.Fatalf("GetRepositoryInfo(worktree) = _, %v", err)
	}
	if !sameDir(info.WorktreeOf, main) {
		t.Errorf("GetRepositoryInfo(worktree).WorktreeOf = %q, want %q", info.WorktreeOf, main)
	}

	mainInfo, err := GetRepositoryInfo(main)
	if err != nil {
		t.Fatalf("GetRepositoryInfo(main) = _, %v", err)
	}
	if mainInfo.WorktreeOf != "" {
		t.Errorf("GetRepositoryInfo(main).WorktreeOf = %q, want empty", mainInfo.WorktreeOf)
	}
}