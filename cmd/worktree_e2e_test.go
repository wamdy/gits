package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/git"
	"github.com/wamdy/gits/internal/repo"
)

// ---------------------------------------------------------------- #1434 端到端（真 git 全链）
//
// 单测把每个纯函数钉在**形状**上；这条链把它们串起来跑真 git，回答单测回答不了的问题：
// 「按磁盘同构落位之后，这个集合还能不能整组执行、能不能整组拆掉、归属还指不指得对」。
//
// 为什么必须有这一层：#1064 复核实录是「三门全绿、add 报已创建并登记，而
// `-r set:demo -- rev-parse --show-toplevel` 返回主仓路径」。所以本文件的硬断言
// 全部打在**磁盘上真实存在的目录**和 **git 自己的回答**上，不打在被调函数的返回值上。
//
// 夹具刻意含三层：主仓 proj（浅）＋ proj/tools/inner（嵌套仓）＋ proj/tools/sub
// （真 submodule）——只有嵌套存在，「祖先优先」这条红线才真的被执行；只有 submodule
// 存在，R1-a 与 core.worktree 归属判据才真的被走到。

// newRealSubmodule 在超级项目 superDir 内加一个**真** submodule（本地相对源，离线可用）。
//
// 这里**不许**降级成 `git init --separate-git-dir`：那条形态与真 submodule 只在
// 「`.git` 是文件且 git-dir == common-dir」这一条判据上等价，而本用例要断言的是
// **归属**——实机实测（#1434 Task 6）表明 `--separate-git-dir` 建的库里没有
// `core.worktree` 自述，MainRepoPath 只能退回 `filepath.Dir(commonDir)` 假路径，
// remove 随即失败。用那种夹具测 R1-a 的归属等于自证个假命题，所以宁可直接失败。
func newRealSubmodule(t *testing.T, superDir, rel string) string {
	t.Helper()
	src := newMainRepo(t, filepath.Join(filepath.Dir(superDir), "subsrc"), "main")
	// 源仓先有目标分支：submodule 检出后建同名本地分支才有落点（worktree add 需要分支存在）。
	gitIn(t, src, "branch", "harness/demo")

	subDir := filepath.Join(superDir, rel)
	// 本地 URL 按 git 口径必须写成 `./x` 或 `../x`（相对**超级项目根**，裸目录名被明确拒绝），
	// 并放行 file 协议——两者都是离线本机可用的前提。
	url, err := filepath.Rel(superDir, src)
	if err != nil {
		t.Fatalf("rel %s→%s: %v", superDir, src, err)
	}
	out, err := tryGitIn(superDir, "-c", "protocol.file.allow=always", "submodule", "add", "--",
		filepath.ToSlash(url), filepath.ToSlash(rel))
	if err != nil {
		t.Fatalf("本机 git 建不出真 submodule（本用例的归属断言依赖真夹具）: %v\n%s", err, out)
	}
	if !isGitFile(t, subDir) {
		t.Fatalf("submodule 检出后 %s/.git 应为文件（linked 形态）", subDir)
	}
	gitIn(t, subDir, "branch", "harness/demo")
	return subDir
}

// isGitFile `.git` 存在且是文件（linked worktree / submodule 的共同形态）。
func isGitFile(t *testing.T, dir string) bool {
	t.Helper()
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && !info.IsDir()
}

// gitTopLevel 回读某目录的工作树根（`-r set:` 是否真的落在镜像目录里，就看这个）。
func gitTopLevel(t *testing.T, dir string) string {
	t.Helper()
	return gitIn(t, dir, "rev-parse", "--show-toplevel")
}

// TestE2EMirrorSetFullChain 覆盖 AC-02/04/05/06/08 的同一条链：
// ① 成员落位与磁盘 1:1（且明确**不等于** flat 的压平结果）；
// ② 祖先优先顺序下三层全部建成；
// ③ `-r set:demo` 解析到三个 worktree 条目，逐条 `rev-parse --show-toplevel` 落在镜像目录内；
// ④ submodule 派生 worktree 的归属指回**源 submodule**（不是 `.git/modules/…` 假路径）；
// ⑤ 整体拆除后源仓分支仍在、worktree 登记回收、剩余条目批量 0 失败。
func TestE2EMirrorSetFullChain(t *testing.T) {
	requireGit(t)

	root := t.TempDir()
	proj := newMainRepo(t, filepath.Join(root, "proj"), "harness/demo")
	inner := newMainRepo(t, filepath.Join(root, "proj", "tools", "inner"), "harness/demo")
	sub := newRealSubmodule(t, proj, filepath.Join("tools", "sub"))
	if got := git.Classify(sub); got != git.KindSubmodule {
		t.Fatalf("前置条件：submodule 夹具应判为 KindSubmodule，实得 %d", got)
	}

	// 选择顺序刻意「深在前、浅在后」——镜像布局的执行顺序必须由 order 重排，
	// 若这里写成天然浅在前，② 就什么都没测。
	repos := []repo.RepositoryInfo{
		info(3, "sub", sub),
		info(2, "inner", inner),
		info(1, "proj", proj),
	}
	base := root
	setRoot := filepath.Join(root, "wt", "demo")

	paths, issues, mirrorRoot := planWorktreeMembers(repos, setRoot, layoutMirror, base)
	if len(issues) != 0 {
		t.Fatalf("镜像落位不该有问题，实得 %q", issues)
	}
	if mirrorRoot != filepath.Clean(base) {
		t.Errorf("mirrorRoot = %q, want %q", mirrorRoot, filepath.Clean(base))
	}

	// ① 与磁盘同构：`<集合根>/Rel(基准, 仓路径)`，父目录段一段都不能丢。
	want := []string{
		filepath.Join(setRoot, "proj", "tools", "sub"),
		filepath.Join(setRoot, "proj", "tools", "inner"),
		filepath.Join(setRoot, "proj"),
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
	// 同构 ≠ 压平：flat 对同一组仓只会给出单层目录名。这一条断言就是「镜像真生效」的判据。
	flat := worktreeMemberPaths(setRoot, repos)
	if flat[0] != filepath.Join(setRoot, "sub") {
		t.Fatalf("flat 对照 = %q, want %q（对照夹具失效）", flat[0], filepath.Join(setRoot, "sub"))
	}
	if paths[0] == flat[0] {
		t.Error("mirror 与 flat 算出同一路径——同构根本没生效")
	}

	// ② 祖先优先：期望顺序 [2 1 0]（浅→深），且真跑通三层。
	order := worktreeBuildOrder(repos, paths, true)
	if len(order) != 3 || order[0] != 2 || order[1] != 1 || order[2] != 0 {
		t.Fatalf("order = %v, want [2 1 0]（祖先优先）", order)
	}
	labels := []string{"[3] sub", "[2] inner", "[1] proj"}
	created, failedLabel := createWorktrees(repos, paths, labels, "harness/demo", order)
	if failedLabel != "" {
		t.Fatalf("祖先优先顺序下整组应建成，失败于 %q", failedLabel)
	}
	if len(created) != 3 {
		t.Fatalf("created = %v, want 3 个成员", created)
	}
	for i, p := range want {
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			t.Errorf("第 %d 层成员目录 %s 未真实落盘: %v", i+1, p, err)
		}
		if got := git.Classify(p); got != git.KindLinkedWorktree {
			t.Errorf("%s 判为 %d, want KindLinkedWorktree（镜像目录必须是可用 worktree，不是空壳目录）", p, got)
		}
		if br := gitIn(t, p, "rev-parse", "--abbrev-ref", "HEAD"); br != "harness/demo" {
			t.Errorf("%s 检出分支 = %q, want harness/demo", p, br)
		}
	}

	// ④ 归属：submodule 派生的 worktree 指回源 submodule —— 这是 R1-a 敢放开 add 的前提
	//（WorktreeRemove 的执行目录就由它决定，指错即整组拆不掉）。
	if got := git.MainRepoPath(paths[0]); !samePathForTest(got, sub) {
		t.Errorf("submodule worktree 归属 = %q, want 源 %q", got, sub)
	}
	if got := git.MainRepoPath(paths[1]); !samePathForTest(got, inner) {
		t.Errorf("嵌套仓 worktree 归属 = %q, want 源 %q", got, inner)
	}

	// ③ 集合登记 + `-r set:demo`：条目必须指向 worktree 目录本身（#1064 复核的同型缺陷就出在这里）。
	cfg := &config.Config{}
	for _, r := range repos {
		if _, _, err := config.EnsureRepositoryEntry(cfg, r.Path, r.Name); err != nil {
			t.Fatalf("登记源仓 %s: %v", r.Path, err)
		}
	}
	members, registered, err := buildWorktreeSetMembers(cfg, repos, paths, "demo")
	if err != nil {
		t.Fatalf("Ensure 成员条目: %v", err)
	}
	if registered != 3 {
		t.Errorf("本次应新登记 3 条 worktree 条目，实得 %d", registered)
	}
	l, mr := worktreeSetLayoutKeys(layoutMirror, mirrorRoot)
	sets := []config.WorktreeSet{{Name: "demo", Branch: "harness/demo", Layout: l, MirrorRoot: mr, Members: members}}

	sel, err := repo.ResolveSelectionWithSets("set:demo", cfg.Repositories, sets, repo.DefaultBranchChecker)
	if err != nil {
		t.Fatalf("-r set:demo 解析失败: %v", err)
	}
	if len(sel) != 3 {
		t.Fatalf("-r set:demo = %d 条 %v, want 3", len(sel), sel)
	}
	for _, r := range sel {
		if !isUnder(r.Path, setRoot) {
			t.Errorf("集合成员 %s (%s) 不在集合根 %s 之下", r.Name, r.Path, setRoot)
			continue
		}
		if top := gitTopLevel(t, r.Path); !samePathForTest(top, r.Path) {
			t.Errorf("`-r set:demo` 的 %s 上 rev-parse --show-toplevel = %q, want 镜像目录自身 %q"+
				"（指回主仓＝集合执行能力名存实亡）", r.Name, top, r.Path)
		}
	}

	// ⑤ 整体拆除：走**命令实际用的那套组合**（脏判据 + teardownWorktreeSet），
	// 不再由测试自己倒序循环——第一版就是这样把 B1 绕过去的：测试全绿，而 `worktree remove`
	// 对嵌套镜像集合常态拆不掉。组合必须与 runWorktreeRemove 一致，否则端到端等于测另一个实现。
	if dirty := findDirtyWorktreeMembers(labels, paths, worktreeNestedMembers(paths)); len(dirty) > 0 {
		t.Fatalf("脏判据把嵌套结构误判成未提交改动，整组会被中止：%q", dirty)
	}
	if failed := teardownWorktreeSet(labels, paths, false); failed != 0 {
		t.Errorf("拆除嵌套镜像集合应全部成功，实得失败 %d 个", failed)
	}
	for _, src := range []string{proj, inner, sub} {
		// 永不删分支是 AC-03.4 红线：拆完分支必须还在，否则整条流水线的提交就没了。
		if out := gitIn(t, src, "branch", "--list", "harness/demo"); !strings.Contains(out, "harness/demo") {
			t.Errorf("%s 的 harness/demo 分支在拆除后消失", src)
		}
		if n := worktreeListCount(t, src); n != 1 {
			t.Errorf("%s 拆除后 git worktree list = %d 条, want 1（登记未回收）", src, n)
		}
	}

	// 登记回收后，剩余条目跑默认全量批量必须 0 失败——死条目留着的话，每条批量命令都会吐 chdir 失败。
	dropped := config.DropWorktreeMemberRepos(cfg, members)
	if dropped != 3 {
		t.Errorf("回收 add 自动登记的条目 = %d, want 3", dropped)
	}
	if n := len(cfg.Repositories); n != 3 {
		t.Fatalf("回收后应只剩 3 条源仓条目，实得 %d: %v", n, cfg.Repositories)
	}
	for _, r := range cfg.Repositories {
		res := git.Execute(r.Path, "rev-parse", "--show-toplevel")
		if !res.Success {
			t.Errorf("批量默认口径在 %s 失败（死登记未回收）: %v %s", r.Path, res.Error, res.Output)
		}
	}
}

// TestE2EMirrorRejectsOutsideBaseWholeSet 钉 AC-04 的失败语义在真 git 夹具上的形状：
// 一个成员越界 → 点名它，且**磁盘上什么都没建**（不是建一半再回滚）。
func TestE2EMirrorRejectsOutsideBaseWholeSet(t *testing.T) {
	requireGit(t)

	inside := t.TempDir()
	outside := t.TempDir()
	proj := newMainRepo(t, filepath.Join(inside, "proj"), "harness/demo")
	alien := newMainRepo(t, filepath.Join(outside, "alien"), "harness/demo")
	setRoot := filepath.Join(inside, "wt", "demo")

	repos := []repo.RepositoryInfo{info(1, "proj", proj), info(2, "alien", alien)}
	paths, issues, _ := planWorktreeMembers(repos, setRoot, layoutMirror, inside)
	issues = append(issues, precheckWorktreeAdd(repos, paths, "harness/demo")...)
	if len(issues) == 0 {
		t.Fatal("越界成员必须被点名，实得零问题")
	}
	if !strings.Contains(strings.Join(issues, "\n"), "alien") {
		t.Errorf("问题清单应点名越界仓 alien，实得 %q", issues)
	}
	if _, err := os.Stat(setRoot); err == nil {
		t.Error("布局拒绝阶段就创建了集合根目录（应零副作用）")
	}
	for _, p := range paths {
		if p != "" {
			if _, err := os.Stat(p); err == nil {
				t.Errorf("被拒成员目录 %s 竟已存在", p)
			}
		}
	}
}

// tryGitIn 与 gitIn 同参，但**不** t.Fatal：夹具的可选形态（真 submodule add）要用它试探。
func tryGitIn(dir string, args ...string) (string, error) {
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
	return string(out), err
}

// isUnder 判断路径是否在根目录之下（集合根的相对段都用同一路径前缀，够用且不比 filepath.Rel 更容易误判）。
func isUnder(path, root string) bool {
	p := filepath.Clean(path)
	r := filepath.Clean(root)
	if len(p) <= len(r) {
		return false
	}
	return strings.EqualFold(p[:len(r)], r) && (p[len(r)] == filepath.Separator || p[len(r)] == '/' || p[len(r)] == '\\')
}

func samePathForTest(a, b string) bool {
	ai, aerr := filepath.Abs(filepath.Clean(a))
	bi, berr := filepath.Abs(filepath.Clean(b))
	if aerr != nil || berr != nil {
		return a == b
	}
	return strings.EqualFold(ai, bi) // Windows 大小写不敏感，真仓路径常见混合大小写
}
