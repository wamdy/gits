package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/git"
	"github.com/wamdy/gits/internal/i18n"
	"github.com/wamdy/gits/internal/repo"
)

// requireGit 本文件单测用真实 git 对象（主仓 / linked worktree / 脏工作树）；缺失则跳过。
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不在 PATH 上，跳过 worktree 命令族单测: %v", err)
	}
}

// gitIn 在 dir 内执行 git 命令（固定提交者身份，不依赖全局配置），失败即终止测试。
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.email=gits-test@example.com",
		"-c", "user.name=gits-test",
		"-c", "init.defaultBranch=main",
		"-c", "core.autocrlf=false",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newMainRepo 造一个带一次提交的普通仓库，返回仓库路径。
func newMainRepo(t *testing.T, dir, branch string) string {
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
	if branch != "" && branch != "main" {
		gitIn(t, dir, "branch", branch)
	}
	return dir
}

// worktreeListCount 返回 `git worktree list` 的条目数（用于证明零副作用 / 整组未拆）。
func worktreeListCount(t *testing.T, mainRepo string) int {
	t.Helper()
	out := gitIn(t, mainRepo, "worktree", "list", "--porcelain")
	if out == "" {
		return 0
	}
	return strings.Count(out, "worktree ")
}

func info(id int, name, path string) repo.RepositoryInfo {
	return repo.RepositoryInfo{ID: id, Name: name, Path: path}
}

// TestPrecheckWorktreeAddIsZeroSideEffect 覆盖 AC-02.2「整组失败语义」的预检部分：
// 选中 2 个仓，其中 1 个没有目标分支 → 预检点名该仓（含其当前分支），
// 且磁盘上零副作用：集合根目录与各成员路径都不存在、`git worktree list` 仍只有主仓一条。
func TestPrecheckWorktreeAddIsZeroSideEffect(t *testing.T) {
	requireGit(t)
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	root := t.TempDir()
	good := newMainRepo(t, filepath.Join(root, "good"), "harness/demo")
	bad := newMainRepo(t, filepath.Join(root, "bad"), "") // 只有 main，没有 harness/demo

	repos := []repo.RepositoryInfo{info(1, "good", good), info(2, "bad", bad)}
	rootDir := filepath.Join(root, ".worktrees", "demo")
	paths := worktreeMemberPaths(rootDir, repos)

	issues := precheckWorktreeAdd(repos, paths, "harness/demo")
	if len(issues) == 0 {
		t.Fatal("缺分支仓应被预检点名，实得零问题")
	}
	joined := strings.Join(issues, "\n")
	if !strings.Contains(joined, "bad") {
		t.Errorf("预检清单应点名缺分支仓 bad，实得:\n%s", joined)
	}
	if !strings.Contains(joined, "harness/demo") {
		t.Errorf("预检清单应包含缺失分支名，实得:\n%s", joined)
	}

	// 零副作用：预检不得创建任何目录，也不得在任何仓里登记 worktree。
	if _, err := os.Stat(rootDir); err == nil {
		t.Errorf("预检创建了集合根目录 %s（应零副作用）", rootDir)
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("预检创建了成员目录 %s（应零副作用）", p)
		}
	}
	for _, r := range []string{good, bad} {
		if n := worktreeListCount(t, r); n != 1 {
			t.Errorf("%s 的 git worktree list 应为 1 条（仅主仓），实得 %d", r, n)
		}
	}
}

// TestPrecheckWorktreeAddRejectsSubmodule 覆盖 Q5：判定为 submodule 的成员直接拒绝并说明不支持。
func TestPrecheckWorktreeAddRejectsSubmodule(t *testing.T) {
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	// 用 --separate-git-dir 造 `.git` 文件且 git-dir == common-dir 的形态（与 submodule 同一判据分支）。
	root := t.TempDir()
	modDir := filepath.Join(root, "modules", "loose")
	loose := filepath.Join(root, "loose-sub")
	if err := os.MkdirAll(filepath.Dir(modDir), 0o755); err != nil {
		t.Fatalf("mkdir modules: %v", err)
	}
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatalf("mkdir loose: %v", err)
	}
	hand := exec.Command("git", "-c", "init.defaultBranch=main", "init", "-q", "--separate-git-dir="+modDir, ".")
	hand.Dir = loose
	if out, err := hand.CombinedOutput(); err != nil {
		t.Skipf("本机 git 不支持 --separate-git-dir 形态，跳过: %v\n%s", err, out)
	}

	repos := []repo.RepositoryInfo{info(9, "loose-sub", loose)}
	paths := worktreeMemberPaths(filepath.Join(root, ".worktrees", "s"), repos)
	issues := precheckWorktreeAdd(repos, paths, "main")
	if len(issues) != 1 || !strings.Contains(issues[0], "loose-sub") {
		t.Fatalf("submodule 成员应被点名拒绝，实得 %q", issues)
	}
}

// TestCheckWorktreeSetNameRejectsDuplicate 覆盖 AC-02.5：同名集合已存在即拒绝，不覆盖。
func TestCheckWorktreeSetNameRejectsDuplicate(t *testing.T) {
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	sets := []config.WorktreeSet{{Name: "demo", Branch: "harness/demo"}}
	err := checkWorktreeSetName("demo", sets)
	if err == nil {
		t.Fatal("同名集合应被拒绝")
	}
	if !strings.Contains(err.Error(), "demo") {
		t.Errorf("拒绝提示应点名集合名，实得 %q", err.Error())
	}
	if err := checkWorktreeSetName("other", sets); err != nil {
		t.Errorf("新集合名应放行，实得 %v", err)
	}
	if err := checkWorktreeSetName("", sets); err == nil {
		t.Error("空集合名应被拒绝")
	}
	if err := checkWorktreeSetName("../escape", sets); err == nil {
		t.Error("含路径成分的集合名应被拒绝（默认根目录会拼接该段）")
	}
}

// TestWorktreeSetSlugDerivation 覆盖 AC-02.5 的集合名缺省规则：分支名去 harness/ 前缀后的 slug。
func TestWorktreeSetSlugDerivation(t *testing.T) {
	cases := map[string]string{
		"harness/20261104-demo": "20261104-demo",
		"20261104-demo":         "20261104-demo",
		"feature/a-b":           "feature-a-b",
		"harness/":              "",
	}
	for branch, want := range cases {
		if got := worktreeSetSlug(branch); got != want {
			t.Errorf("worktreeSetSlug(%q) = %q, want %q", branch, got, want)
		}
	}
}

// TestWorktreeBranchOfNeverGuessesHead 覆盖 AC-02.3：分支来源 --branch > defaults.branch，两者皆缺为空串（由调用方报错）。
func TestWorktreeBranchOfNeverGuessesHead(t *testing.T) {
	if got := worktreeBranchOf("feature/x", "main"); got != "feature/x" {
		t.Errorf("--branch 应优先，实得 %q", got)
	}
	if got := worktreeBranchOf("", "main"); got != "main" {
		t.Errorf("应回退 defaults.branch，实得 %q", got)
	}
	if got := worktreeBranchOf("  ", ""); got != "" {
		t.Errorf("两者皆缺应为空串（绝不猜 HEAD），实得 %q", got)
	}
}

// TestWorktreeMemberPathsDedup 成员目录名取仓库 Name，重名时追加 -<ID>；
// 名称里的路径成分被收敛为单层目录名，避免拼接跳出集合根。
func TestWorktreeMemberPathsDedup(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "base", "root")
	repos := []repo.RepositoryInfo{
		info(1, "svc", filepath.Join("a", "svc")),
		info(2, "svc", filepath.Join("b", "svc")),
		info(3, "../../escape", filepath.Join("c", "esc")),
		info(4, "", filepath.Join("d", "blank")),
	}
	got := worktreeMemberPaths(root, repos)
	want := []string{
		filepath.Join(root, "svc"),
		filepath.Join(root, "svc-2"),
		filepath.Join(root, "escape"),
		filepath.Join(root, "repo-4"),
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("member path [%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestFindDirtyWorktreeMembers 覆盖 AC-03.3 的脏判据：
// 只点名有未提交改动的成员，干净成员不在清单里（调用方据此整组中止）。
// 两个成员分属两个主仓——同一主仓的同一分支不能被两个 worktree 同时检出。
func TestFindDirtyWorktreeMembers(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	mainA := newMainRepo(t, filepath.Join(root, "repo-a"), "harness/demo")
	mainB := newMainRepo(t, filepath.Join(root, "repo-b"), "harness/demo")

	wtA := filepath.Join(root, ".worktrees", "demo", "repo-a")
	wtB := filepath.Join(root, ".worktrees", "demo", "repo-b")
	gitIn(t, mainA, "worktree", "add", wtA, "harness/demo")
	gitIn(t, mainB, "worktree", "add", wtB, "harness/demo")

	if err := os.WriteFile(filepath.Join(wtB, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	labels := []string{"[1] repo-a", "[2] repo-b"}
	paths := []string{wtA, wtB}
	dirty := findDirtyWorktreeMembers(labels, paths)
	if len(dirty) != 1 || dirty[0] != "[2] repo-b" {
		t.Fatalf("脏成员清单应只点名 [2] repo-b，实得 %q", dirty)
	}

	// 整组中止的语义验证：脏判据非空即中止，两个 worktree 都必须仍在每个主仓的登记里。
	for _, m := range []string{mainA, mainB} {
		if n := worktreeListCount(t, m); n != 2 {
			t.Errorf("%s 的 worktree 应为 2 条（主仓 + 1 成员），实得 %d", m, n)
		}
	}
}

// TestPruneWorktreeSetsIsRegistrationOnly 覆盖 AC-03.5：prune 只清登记。
// 被判定为「路径不存在」的成员移出集合、空集合删除；
// 同时断言磁盘目录一个都没被删（用真实目录 + 假 exists 判据）。
func TestPruneWorktreeSetsIsRegistrationOnly(t *testing.T) {
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	root := t.TempDir()
	aliveA := filepath.Join(root, ".worktrees", "demo", "repo-a")
	goneB := filepath.Join(root, ".worktrees", "demo", "repo-b")
	aliveC := filepath.Join(root, ".worktrees", "solo", "repo-c")
	diskGoneButExists := filepath.Join(root, ".worktrees", "demo", "repo-d")
	for _, d := range []string{aliveA, aliveC, diskGoneButExists} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	allRepos := []config.Repository{
		{ID: 1, Name: "repo-a", Path: filepath.Join(root, "repo-a")},
		{ID: 2, Name: "repo-b", Path: filepath.Join(root, "repo-b")},
		{ID: 3, Name: "repo-c", Path: filepath.Join(root, "repo-c")},
		{ID: 4, Name: "repo-d", Path: filepath.Join(root, "repo-d")},
	}
	sets := []config.WorktreeSet{
		{Name: "demo", Branch: "harness/demo", Members: []config.WorktreeMember{
			{RepoID: 1, Path: aliveA},
			{RepoID: 2, Path: goneB},
			{RepoID: 4, Path: diskGoneButExists},
		}},
		{Name: "solo", Branch: "harness/solo", Members: []config.WorktreeMember{
			{RepoID: 3, Path: aliveC},
		}},
		{Name: "empty-set", Branch: "harness/empty", Members: []config.WorktreeMember{
			{RepoID: 2, Path: goneB},
		}},
	}

	// exists 判据：goneB 视为消失，其余视为存在（磁盘上 goneB 从未创建，不影响断言）。
	exists := func(p string) bool { return p != goneB }
	removed, droppedSets, kept, droppedMembers := pruneWorktreeSets(sets, allRepos, exists)

	if len(removed) != 2 || removed[0] != "demo/repo-b" || removed[1] != "empty-set/repo-b" {
		t.Errorf("被移出的成员 = %q, want [demo/repo-b empty-set/repo-b]", removed)
	}
	// 评审 BLOCK-2：消失的成员要一并交给条目回收，否则死登记会让默认全量批量命令天天报 chdir 失败。
	if len(droppedMembers) != 2 {
		t.Errorf("待回收条目数 = %d, want 2（两处集合里的 repo-b 成员）", len(droppedMembers))
	}
	for _, m := range droppedMembers {
		if m.RepoID != 2 || m.Path != goneB {
			t.Errorf("待回收成员 = %+v, want {RepoID:2 Path:%s}", m, goneB)
		}
	}
	if len(droppedSets) != 1 || droppedSets[0] != "empty-set" {
		t.Errorf("空集合应被删除，实得 %q", droppedSets)
	}
	if len(kept) != 2 {
		t.Fatalf("应保留 2 个集合，实得 %d", len(kept))
	}
	if len(kept[0].Members) != 2 {
		t.Errorf("demo 集合应剩 2 个成员（repo-a、repo-d），实得 %d", len(kept[0].Members))
	}
	for _, m := range kept[0].Members {
		if m.RepoID == 2 {
			t.Error("悬空成员 repo-b 仍留在 demo 集合里")
		}
	}

	// 只清登记：磁盘目录必须原样存在（包括被移出登记的那个）。
	for _, d := range []string{aliveA, aliveC, diskGoneButExists} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("prune 删除了磁盘目录 %s: %v", d, err)
		}
	}
}

// TestRemoveWorktreeSetByName 验证拆除成功后集合登记被删除、其余集合保留。
func TestRemoveWorktreeSetByName(t *testing.T) {
	sets := []config.WorktreeSet{{Name: "a"}, {Name: "b"}}
	kept := removeWorktreeSetByName(sets, "a")
	if len(kept) != 1 || kept[0].Name != "b" {
		t.Errorf("removeWorktreeSetByName = %v, want 只剩 b", kept)
	}
}

// TestWorktreeMemberViews 成员视图：名称优先取登记仓库名，未登记时按路径 base 名兜底。
func TestWorktreeMemberViews(t *testing.T) {
	set := config.WorktreeSet{Name: "demo", Members: []config.WorktreeMember{
		{RepoID: 1, Path: filepath.Join("root", ".worktrees", "demo", "on-disk-name")},
		{RepoID: 9, Path: filepath.Join("root", ".worktrees", "demo", "wiki")},
	}}
	allRepos := []config.Repository{{ID: 1, Name: "登记名", Path: "root/base"}}
	labels, paths := worktreeMemberViews(set, allRepos)
	if labels[0] != "[1] 登记名" {
		t.Errorf("成员标签 = %q, want '[1] 登记名'", labels[0])
	}
	if labels[1] != "[9] wiki" {
		t.Errorf("未登记成员的标签 = %q, want '[9] wiki'", labels[1])
	}
	if paths[0] != set.Members[0].Path {
		t.Errorf("成员路径应原样透传，实得 %q", paths[0])
	}
}

// TestWorktreeAddMembersTargetWorktreeNotParent 是 #1064 复核阶段抓到的「三门全绿而功能死」的
// 回归钉：add 阶段 C 必须把集合成员指向 **worktree 目录自己的仓库条目**。若指向父仓条目，
// `-r set:<集合>` 会经 RepoID 反查出父仓 Repository，批量命令跑在主工作区（检出的分支都不是
// 集合那个分支），整组执行能力名存实亡。本测试把「条目登记 → 集合成员 → 选择执行目录」整条链
// 在无 git 依赖下钉死。
func TestWorktreeAddMembersTargetWorktreeNotParent(t *testing.T) {
	root := t.TempDir()
	parentPath := filepath.Join(root, "repoA")
	wtPath := filepath.Join(root, ".worktrees", "demo", "repoA")

	cfg := &config.Config{Repositories: []config.Repository{{ID: 1, Name: "repoA", Path: parentPath}}}
	repos := []repo.RepositoryInfo{{ID: 1, Name: "repoA", Path: parentPath}}

	members, registered, err := buildWorktreeSetMembers(cfg, repos, []string{wtPath}, "demo")
	if err != nil {
		t.Fatalf("buildWorktreeSetMembers 意外报错: %v", err)
	}
	if registered != 1 {
		t.Errorf("应新登记 1 个 worktree 仓库条目，实得 %d", registered)
	}
	if len(members) != 1 {
		t.Fatalf("成员数 = %d, want 1", len(members))
	}
	if members[0].RepoID == 1 {
		t.Error("成员指向父仓条目（ID 1）——正是 #1064 复核抓到的缺陷形态")
	}
	cfg.WorktreeSets = []config.WorktreeSet{{Name: "demo", Branch: "harness/x", Members: members}}

	got, err := selectRepos("set:demo", "", cfg)
	if err != nil {
		t.Fatalf("selectRepos(set:demo) 意外报错: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("set: 命中 %d 条, want 1", len(got))
	}
	if filepath.Clean(got[0].Path) != filepath.Clean(wtPath) {
		t.Errorf("set: 选中的执行目录 = %q, want worktree 目录 %q", got[0].Path, filepath.Clean(wtPath))
	}
	if got[0].Name != "repoA@demo" {
		t.Errorf("选中条目名 = %q, want repoA@demo", got[0].Name)
	}

	// 幂等：同一 worktree 路径重复登记不涨条目、成员 ID 不变（add 重跑 / 用户已手工 config add）。
	before := len(cfg.Repositories)
	again, registered2, err := buildWorktreeSetMembers(cfg, repos, []string{wtPath}, "demo")
	if err != nil || registered2 != 0 || len(again) != 1 || again[0].RepoID != members[0].RepoID ||
		len(cfg.Repositories) != before {
		t.Errorf("重复登记应复用条目：registered=%d id=%d/%d 条目数=%d/%d err=%v",
			registered2, again[0].RepoID, members[0].RepoID, len(cfg.Repositories), before, err)
	}

	// 父仓条目不受影响，仍可单独按 ID 选中。
	if p, err := selectRepos("1", "", cfg); err != nil || len(p) != 1 ||
		filepath.Clean(p[0].Path) != filepath.Clean(parentPath) {
		t.Errorf("按 ID 选中父仓 = %+v err=%v, want 主仓路径 %q", p, err, filepath.Clean(parentPath))
	}
}

// TestCreateWorktreesRollbackOnlyThisCall 补 AC-02.7 要求的回滚单测（评审 BLOCK-3：
// rollbackWorktrees / WorktreeAdd 此前零测试触达）。
// 场景：本次调用有 2 个成员，第 1 个能建、第 2 个必失败；另有一个**调用前就存在**的 worktree
// 作对照。断言三件事：① 新建记账只含本次成功的第 1 个；② 回滚把它撤掉；
// ③ 调用前那个 worktree 原样在位（回滚绝不遍历磁盘、绝不动别人的工作区）。
func TestCreateWorktreesRollbackOnlyThisCall(t *testing.T) {
	requireGit(t)
	root := t.TempDir()

	good := newMainRepo(t, filepath.Join(root, "good"), "feat/x")
	gitIn(t, good, "branch", "feat/pre")
	pre := filepath.Join(root, ".worktrees", "pre", "good")
	gitIn(t, good, "worktree", "add", "-q", pre, "feat/pre") // 调用前既有，作为"不许被动"的对照

	bad := newMainRepo(t, filepath.Join(root, "bad"), "") // 没有 feat/x 分支 → 创建必失败

	repos := []repo.RepositoryInfo{info(1, "good", good), info(2, "bad", bad)}
	paths := []string{
		filepath.Join(root, ".worktrees", "s", "good"),
		filepath.Join(root, ".worktrees", "s", "bad"),
	}
	labels := []string{"[1] good (feat/x)", "[2] bad (main)"}

	created, failedLabel := createWorktrees(repos, paths, labels, "feat/x")
	if failedLabel == "" {
		t.Fatal("第 2 个成员缺分支，createWorktrees 应报失败")
	}
	if len(created) != 1 || created[0] != paths[0] {
		t.Errorf("本次新建记账 = %v, want 仅 [%s]", created, paths[0])
	}

	rolled := rollbackWorktrees(created)
	if rolled != 1 {
		t.Errorf("应回滚本次新建的 1 个，实得 %d", rolled)
	}
	if _, err := os.Stat(paths[0]); err == nil {
		t.Error("回滚后被回滚的 worktree 目录仍存在")
	}
	if _, err := os.Stat(pre); err != nil {
		t.Errorf("回滚越界动了调用前的 worktree：%v", err)
	}
	if got := worktreeListCount(t, good); got != 2 {
		t.Errorf("good 仓的 worktree 数 = %d, want 2（主仓 + 调用前那个 pre）", got)
	}
}

// TestWorktreeRemoveNeverDeletesBranch 钉 AC-03.4 红线与 WorktreeRemove 的边界（评审 BLOCK-3：
// git.WorktreeRemove 此前零测试触达）：脏 worktree 在不给 --force 时必须被拒、目录不被回收；
// 给 --force 才拆除；**两种情况下分支都必须活着**。
func TestWorktreeRemoveNeverDeletesBranch(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	main := newMainRepo(t, filepath.Join(root, "m"), "feat/keep")
	wt := filepath.Join(root, ".worktrees", "s", "m")
	gitIn(t, main, "worktree", "add", "-q", wt, "feat/keep")

	if err := os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	if res := git.WorktreeRemove(wt, false); res.Success {
		t.Error("脏 worktree 未加 --force 竟拆除成功（红线失守）")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("被拒后 worktree 目录应仍在位：%v", err)
	}

	if res := git.WorktreeRemove(wt, true); !res.Success {
		t.Fatalf("--force 应拆除成功，实得：%s", res.Output)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Error("--force 后 worktree 目录应已被 git 回收")
	}

	if out := gitIn(t, main, "branch", "--list", "feat/keep"); !strings.Contains(out, "feat/keep") {
		t.Errorf("分支 feat/keep 被删了（AC-03.4 违反）：branch --list = %q", out)
	}
	if out := gitIn(t, main, "rev-parse", "--verify", "refs/heads/feat/keep"); out == "" {
		t.Error("refs/heads/feat/keep 应仍可解析")
	}
}

// TestCheckWorktreeAddScopeRequiresExplicitSelection 钉住评审 Q-02：
// worktree add 不给 -r/-g 时选择集会退化成「全部登记仓库」，必须拒绝而不是静默全建。
func TestCheckWorktreeAddScopeRequiresExplicitSelection(t *testing.T) {
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	if err := checkWorktreeAddScope("", ""); err == nil {
		t.Error("-r 与 -g 都缺省时应拒绝执行")
	}
	if err := checkWorktreeAddScope("  ", ""); err == nil {
		t.Error("空白 -r 等同缺省，应拒绝")
	}
	for _, tc := range [][2]string{{"1,2", ""}, {"", "grp"}, {"set:demo", ""}} {
		if err := checkWorktreeAddScope(tc[0], tc[1]); err != nil {
			t.Errorf("显式范围 (%q,%q) 不该被拒：%v", tc[0], tc[1], err)
		}
	}
}

// TestPrecheckFlagsBranchAlreadyCheckedOut 钉住评审 Q-03：目标分支已被本仓某个 worktree
// 检出时，预检就在只读阶段点名（而不是等阶段 B 被 git 拒绝再回滚）。
// 主仓默认检出 main，因此 --branch 缺省落到 defaults.branch=main 的组合最常见。
func TestPrecheckFlagsBranchAlreadyCheckedOut(t *testing.T) {
	requireGit(t)
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	root := t.TempDir()
	main := newMainRepo(t, filepath.Join(root, "m"), "") // 检出分支 = main
	repos := []repo.RepositoryInfo{info(1, "m", main)}
	paths := worktreeMemberPaths(filepath.Join(root, ".worktrees", "s"), repos)

	issues := precheckWorktreeAdd(repos, paths, "main")
	if len(issues) == 0 {
		t.Fatal("main 已被主仓检出，预检应点名而不是放行到阶段 B")
	}
	joined := strings.Join(issues, "\n")
	if !strings.Contains(joined, "已被本仓 worktree 检出") {
		t.Errorf("预检应给「分支已被检出」而非「分支不存在」，实得:\n%s", joined)
	}
	if strings.Contains(joined, "不存在分支") {
		t.Errorf("main 分支确实存在，不该报缺分支:\n%s", joined)
	}
	// 零副作用不变：预检不得创建任何目录。
	if _, err := os.Stat(filepath.Join(root, ".worktrees")); err == nil {
		t.Error("预检阶段不得创建集合根目录")
	}
	if got := worktreeListCount(t, main); got != 1 {
		t.Errorf("预检后 worktree 数 = %d, want 1（只有主仓）", got)
	}
}
