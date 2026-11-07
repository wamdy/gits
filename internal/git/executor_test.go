package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireGit 本包单测依赖真实 git 命令（造主仓 / linked worktree / submodule 对象）；缺失则跳过。
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不在 PATH 上，跳过 git 判定底座单测: %v", err)
	}
}

// gitIn 在 dir 内执行 git 命令并断言成功（固定提交者身份，避免依赖全局 git 配置）。
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := tryGitIn(dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// tryGitIn 尝试执行 git 命令，返回合并输出与错误（不中断测试，用于可降级场景）。
func tryGitIn(dir string, args ...string) (string, error) {
	full := append([]string{
		"-c", "user.email=gits-test@example.com",
		"-c", "user.name=gits-test",
		"-c", "init.defaultBranch=main",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// newRepoWithCommit 初始化一个带一次提交的普通仓库。
func newRepoWithCommit(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	gitIn(t, dir, "init", "-q", ".")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	gitIn(t, dir, "add", "README.md")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// newLinkedWorktree 从主仓拉出一个 linked worktree（`.git` 落为文件）。
func newLinkedWorktree(t *testing.T, mainRepo, worktreeDir, branch string) string {
	t.Helper()
	gitIn(t, mainRepo, "worktree", "add", "-b", branch, worktreeDir)
	return worktreeDir
}

// newSubmoduleCheckout 造一个 submodule 检出目录，返回该目录路径。
// 首选真实 `git submodule add`（本地路径 + protocol.file.allow=always，离线可用）；
// 该命令在部分环境被禁用时，降级为 `git init --separate-git-dir` 构造等价的
// `.git` 文件形态（git-dir == common-dir，与 submodule 同一判据分支）。
func newSubmoduleCheckout(t *testing.T, root string) (string, string) {
	t.Helper()
	holder := newRepoWithCommit(t, filepath.Join(root, "holder"))
	newRepoWithCommit(t, filepath.Join(root, "subsrc")) // submodule 源仓（../subsrc）
	subDir := filepath.Join(holder, "sub")

	// 本地相对路径 clone（离线可用，无需网络）。
	out, err := tryGitIn(holder, "-c", "protocol.file.allow=always", "submodule", "add", "../subsrc", "sub")
	if err == nil {
		if _, statErr := os.Stat(filepath.Join(subDir, ".git")); statErr == nil {
			return subDir, "git submodule add"
		}
	}

	// 降级：gitfile 指向独立 git 目录，git-dir 与 common-dir 同指该目录（submodule 形态）。
	modDir := filepath.Join(root, "modules", "loose")
	if err := os.MkdirAll(filepath.Dir(modDir), 0o755); err != nil {
		t.Fatalf("mkdir modules: %v", err)
	}
	loose := filepath.Join(root, "loose-sub")
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatalf("mkdir loose-sub: %v", err)
	}
	gitIn(t, loose, "init", "-q", "--separate-git-dir="+modDir, ".")
	return loose, "git init --separate-git-dir 等价形态（真实 submodule add 不可用: " + strings.TrimSpace(out) + "）"
}

func isGitFile(t *testing.T, dir string) bool {
	t.Helper()
	info, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// TestClassifyThreeStates 验证三态判定底座（AC-01.2）：
// `.git` 目录 → KindRepo；`.git` 文件且 git-dir ≠ common-dir → KindLinkedWorktree；
// `.git` 文件且 git-dir == common-dir → KindSubmodule；非 git 目录 → KindNotGit。
func TestClassifyThreeStates(t *testing.T) {
	requireGit(t)
	root := t.TempDir()

	main := newRepoWithCommit(t, filepath.Join(root, "main"))
	wt := newLinkedWorktree(t, main, filepath.Join(root, "wt"), "feature/x")
	sub, how := newSubmoduleCheckout(t, root)
	if strings.HasPrefix(how, "git init") {
		t.Logf("真实 submodule add 不可用，采用等价 .git 文件形态覆盖 KindSubmodule：%s", how)
	}
	notGit := filepath.Join(root, "plain-dir")
	if err := os.MkdirAll(notGit, 0o755); err != nil {
		t.Fatalf("mkdir plain-dir: %v", err)
	}

	cases := []struct {
		name string
		path string
		want GitEntryKind
	}{
		{"普通仓库(.git 目录)", main, KindRepo},
		{"linked worktree(.git 文件, git-dir!=common-dir)", wt, KindLinkedWorktree},
		{"submodule(.git 文件, git-dir==common-dir)", sub, KindSubmodule},
		{"非 git 目录", notGit, KindNotGit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.path); got != c.want {
				t.Errorf("Classify(%q) = %d, want %d", c.path, got, c.want)
			}
		})
	}

	// 主仓归属：只有 linked worktree 能反查主仓路径。
	if got := MainRepoPath(wt); !samePath(got, main) {
		t.Errorf("MainRepoPath(worktree) = %q, want %q", got, main)
	}
	for _, p := range []string{main, sub, notGit} {
		if got := MainRepoPath(p); got != "" {
			t.Errorf("MainRepoPath(%q) = %q, want empty", p, got)
		}
	}
}

// TestClassifyTwoGitFileFormsContrast 覆盖 AC-01.4 的对照用例：
// 两个仓库路径的 `.git` 都是文件，但 git-dir 与 common-dir 的关系不同，
// 判定结果必须分别落在 KindLinkedWorktree / KindSubmodule，互不误判。
func TestClassifyTwoGitFileFormsContrast(t *testing.T) {
	requireGit(t)
	root := t.TempDir()

	main := newRepoWithCommit(t, filepath.Join(root, "main"))
	wt := newLinkedWorktree(t, main, filepath.Join(root, "wt"), "feature/y")
	sub, _ := newSubmoduleCheckout(t, root)

	if !isGitFile(t, wt) || !isGitFile(t, sub) {
		t.Fatalf("对照用例前提不成立：wt 与 sub 的 .git 都应是文件")
	}

	wtGitDir, wtCommon, ok := GitDirs(wt)
	if !ok {
		t.Fatalf("GitDirs(worktree) 失败")
	}
	if samePath(wtGitDir, wtCommon) {
		t.Errorf("worktree 的 git-dir 与 common-dir 不应相同：%q == %q", wtGitDir, wtCommon)
	}
	if Classify(wt) != KindLinkedWorktree {
		t.Errorf("Classify(worktree) = %d, want KindLinkedWorktree", Classify(wt))
	}

	subGitDir, subCommon, ok := GitDirs(sub)
	if !ok {
		t.Fatalf("GitDirs(submodule) 失败")
	}
	if !samePath(subGitDir, subCommon) {
		t.Errorf("submodule 的 git-dir 与 common-dir 应相同：%q != %q", subGitDir, subCommon)
	}
	if Classify(sub) != KindSubmodule {
		t.Errorf("Classify(submodule) = %d, want KindSubmodule", Classify(sub))
	}
}

// TestGitDirsPlainRepoResolvesRelativeCommonDir 验证普通仓场景：
// git 可能把 --git-common-dir 输出成相对路径（如 `.git`），必须按仓库目录解析为绝对路径，
// 才能与 --absolute-git-dir 同口径比较。
func TestGitDirsPlainRepoResolvesRelativeCommonDir(t *testing.T) {
	requireGit(t)
	main := newRepoWithCommit(t, filepath.Join(t.TempDir(), "main"))

	gitDir, commonDir, ok := GitDirs(main)
	if !ok {
		t.Fatalf("GitDirs(%s) 应成功", main)
	}
	for _, p := range []string{gitDir, commonDir} {
		if !filepath.IsAbs(p) {
			t.Errorf("GitDirs 返回值应为绝对路径，得到 %q", p)
		}
	}
	if !samePath(gitDir, commonDir) {
		t.Errorf("普通仓 git-dir 与 common-dir 应相同：%q != %q", gitDir, commonDir)
	}
	if Classify(main) != KindRepo {
		t.Errorf("Classify(plain) = %d, want KindRepo", Classify(main))
	}
}

// TestClassifyInsideRepo 验证判定底座的兜底口径：无 `.git` 入口但仍在主仓工作树内的子目录、
// 以及裸仓库，都归为 KindRepo（两者 git-dir == common-dir），不当成 worktree/submodule。
func TestClassifyInsideRepo(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	main := newRepoWithCommit(t, filepath.Join(root, "main"))

	subDir := filepath.Join(main, "src", "pkg")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if got := Classify(subDir); got != KindRepo {
		t.Errorf("Classify(仓库内子目录) = %d, want KindRepo", got)
	}

	bare := filepath.Join(root, "bare.git")
	gitIn(t, root, "init", "-q", "--bare", bare)
	if got := Classify(bare); got != KindRepo {
		t.Errorf("Classify(裸仓库) = %d, want KindRepo", got)
	}
}

// TestWorktreeAddRemoveRoundTrip 直接钉生产函数 git.WorktreeAdd / git.WorktreeRemove
// （评审 S-08：此前测试里的 worktree 全是各文件自备的裸 exec.Command，生产函数零触达）。
// 覆盖三个取向：只建 worktree 不建分支、拆除在主仓目录执行、--force 的映射，
// 以及红线「永不删分支」在 git 层的行为证据。
func TestWorktreeAddRemoveRoundTrip(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	main := newRepoWithCommit(t, filepath.Join(root, "m"))
	gitIn(t, main, "branch", "feat/rt")
	wt := filepath.Join(root, ".worktrees", "rt", "m")

	// 分支不存在时 WorktreeAdd 必须失败（本函数不传 -b，不代建分支）。
	if res := WorktreeAdd(main, filepath.Join(root, "nope"), "feat/missing"); res.Success {
		t.Error("分支不存在时 WorktreeAdd 竟成功（不该代建分支）")
	}

	if res := WorktreeAdd(main, wt, "feat/rt"); !res.Success {
		t.Fatalf("WorktreeAdd 失败: %s", res.Output)
	}
	if got := Classify(wt); got != KindLinkedWorktree {
		t.Errorf("新建目录 Classify = %d, want KindLinkedWorktree", got)
	}
	if got := MainRepoPath(wt); filepath.Clean(got) != filepath.Clean(main) {
		t.Errorf("MainRepoPath(worktree) = %q, want %q", got, main)
	}

	// 同一分支再建第二个：git 自身拒绝 —— 这条正是预检 Q-03 判据的行为依据。
	if res := WorktreeAdd(main, wt+"-2", "feat/rt"); res.Success {
		t.Error("同一分支竟能建出第二个 worktree")
	}

	// 脏 worktree 不带 --force 应被 git 拒绝；带 --force 才拆掉（AC-03.3 的 git 层证据）。
	if err := os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write dirty: %v", err)
	}
	if res := WorktreeRemove(wt, false); res.Success {
		t.Error("脏 worktree 未加 force 竟拆除成功")
	}
	if res := WorktreeRemove(wt, true); !res.Success {
		t.Fatalf("WorktreeRemove(force) 失败: %s", res.Output)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Error("force 拆除后目录应被 git 回收")
	}
	if out := gitIn(t, main, "branch", "--list", "feat/rt"); !strings.Contains(out, "feat/rt") {
		t.Errorf("WorktreeRemove 不得删分支，实得 branch --list = %q", out)
	}
}

// TestWorktreeCheckedOutBranches 钉住预检新增的只读判据（评审 Q-03）：
// 枚举「已被检出」的本地分支，主仓的 main 与 worktree 的 feat 都要命中，
// 未被任何 worktree 检出的分支不得出现；detached HEAD 不产出 branch 行。
func TestWorktreeCheckedOutBranches(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	main := newRepoWithCommit(t, filepath.Join(root, "m"))
	gitIn(t, main, "branch", "feat/x")
	gitIn(t, main, "branch", "feat/idle")
	wt := filepath.Join(root, ".worktrees", "cb", "m")
	if res := WorktreeAdd(main, wt, "feat/x"); !res.Success {
		t.Fatalf("WorktreeAdd 失败: %s", res.Output)
	}

	got := WorktreeCheckedOutBranches(main)
	if at, ok := got["main"]; !ok || filepath.Clean(at) != filepath.Clean(main) {
		t.Errorf("main 应检出在主仓，实得 %q ok=%v", at, ok)
	}
	if at, ok := got["feat/x"]; !ok || filepath.Clean(at) != filepath.Clean(wt) {
		t.Errorf("feat/x 应检出在 worktree，实得 %q ok=%v", at, ok)
	}
	if _, ok := got["feat/idle"]; ok {
		t.Errorf("feat/idle 未被任何 worktree 检出，不应出现在结果里：%v", got)
	}
	// 从 worktree 子目录调用同样可解析（同一 common-dir 视图）。
	sub := filepath.Join(wt, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, ok := WorktreeCheckedOutBranches(sub)["feat/x"]; !ok {
		t.Error("worktree 子目录视角应同样看到 feat/x 已检出")
	}
}

// TestClassifyInsideWorktreeSubdir 钉住评审 S-03 的边界：linked worktree 的**子目录**
// 没有 `.git` 入口，由 rev-parse 兜底；其 git-dir 指向 .git/worktrees/<name>、
// common-dir 指向主仓 .git，两值仍不等 ⇒ 落 KindLinkedWorktree（不是 KindRepo），
// 归属仍正确指向主仓。与主仓子目录（相等 → KindRepo）形成对照。
func TestClassifyInsideWorktreeSubdir(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	main := newRepoWithCommit(t, filepath.Join(root, "m"))
	gitIn(t, main, "branch", "feat/sub")
	wt := filepath.Join(root, ".worktrees", "sub", "m")
	if res := WorktreeAdd(main, wt, "feat/sub"); !res.Success {
		t.Fatalf("WorktreeAdd 失败: %s", res.Output)
	}
	inside := filepath.Join(wt, "src", "pkg")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if got := Classify(inside); got != KindLinkedWorktree {
		t.Errorf("Classify(worktree 子目录) = %d, want KindLinkedWorktree", got)
	}
	if got := MainRepoPath(inside); filepath.Clean(got) != filepath.Clean(main) {
		t.Errorf("MainRepoPath(worktree 子目录) = %q, want %q", got, main)
	}
	// 对照：主仓的子目录仍视为 KindRepo。
	mainSub := filepath.Join(main, "src")
	if err := os.MkdirAll(mainSub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := Classify(mainSub); got != KindRepo {
		t.Errorf("Classify(主仓子目录) = %d, want KindRepo", got)
	}
}

// TestCurrentBranch 验证 #1067 的检出分支读取：正常检出返回分支名，游离 HEAD 与非 git 目录返回空串。
func TestCurrentBranch(t *testing.T) {
	requireGit(t)
	base := t.TempDir()
	main := filepath.Join(base, "m")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "init")
	if err := os.WriteFile(filepath.Join(main, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "add", "f.txt")
	gitIn(t, main, "commit", "-m", "init")
	if got := CurrentBranch(main); got != "main" {
		t.Fatalf("CurrentBranch(正常检出) = %q, want %q", got, "main")
	}

	gitIn(t, main, "checkout", "-b", "feature/x")
	if got := CurrentBranch(main); got != "feature/x" {
		t.Fatalf("CurrentBranch(切分支后) = %q, want %q", got, "feature/x")
	}

	gitIn(t, main, "checkout", "--detach")
	if got := CurrentBranch(main); got != "" {
		t.Fatalf("CurrentBranch(游离 HEAD) = %q, want 空串", got)
	}

	if got := CurrentBranch(filepath.Join(base, "not-a-repo")); got != "" {
		t.Fatalf("CurrentBranch(非 git 目录) = %q, want 空串", got)
	}
}
