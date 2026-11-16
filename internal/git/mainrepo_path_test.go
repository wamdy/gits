package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------- #1434 归属源树解析
//
// 背景缺陷：MainRepoPath 用 filepath.Dir(commonDir) 反查归属，对 submodule 派生的
// linked worktree 算出 `<super>/.git/modules/<rel 的父目录>`——一个不存在的路径。
// 而它同时是 `← 主仓` 归属列与 WorktreeRemove 执行目录的来源，于是"放开 submodule 成员"
// 会直接长出拆不掉的集合。修法是把判据换成 git 自己写在 common 库里的 core.worktree，
// 并做源树双向校验（ADR-150：禁止用路径子串猜身份）。

// newSubmoduleDerivedWorktree 造「holder 主仓 + 真 submodule + 该 submodule 的 linked worktree」，
// worktree 落在镜像布局同形的嵌套位置（derived/holder/sub），返回 (worktree, submodule 源树)。
func newSubmoduleDerivedWorktree(t *testing.T, root string) (wt string, subSrc string) {
	t.Helper()
	subSrc, how := newSubmoduleCheckout(t, root)
	if strings.HasPrefix(how, "git init") {
		t.Skipf("真实 submodule add 不可用，等价形态不足以覆盖 core.worktree 判据：%s", how)
	}
	branch := "harness/derived"
	gitIn(t, subSrc, "branch", branch)

	wt = filepath.Join(root, "derived", "holder", "sub")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatalf("mkdir derived: %v", err)
	}
	gitIn(t, subSrc, "worktree", "add", wt, branch)
	return wt, subSrc
}

// TestMainRepoPathSubmoduleDerived 正向钉：归属必须解析到 submodule 源工作树，
// 且该目录确实能管 worktree（实测 E12/E13：在那里 worktree list 列出源 + 派生两条）。
func TestMainRepoPathSubmoduleDerived(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	wt, subSrc := newSubmoduleDerivedWorktree(t, root)

	if got := Classify(wt); got != KindLinkedWorktree {
		t.Fatalf("Classify(派生 worktree) = %d, want KindLinkedWorktree（无需第四态）", got)
	}
	owner := MainRepoPath(wt)
	if !samePath(owner, subSrc) {
		t.Errorf("MainRepoPath = %q, want submodule 源树 %q", owner, subSrc)
	}
	res := Execute(owner, "worktree", "list")
	if !res.Success {
		t.Fatalf("在归属目录执行 worktree list 失败：%s", res.Output)
	}
	if !strings.Contains(res.Output, filepath.ToSlash(wt)) {
		t.Errorf("归属目录的 worktree list 未列出派生 worktree:\n%s", res.Output)
	}
	// 派生 worktree 自己不能是自己的归属（否则 `← 主仓` 打出自引用）。
	if samePath(owner, wt) {
		t.Errorf("归属不该等于 worktree 自身：%q", owner)
	}
}

// TestMainRepoPathNormalWorktreeUnchanged 对照钉（E14）：普通仓的 linked worktree
// 所在 common 库没有 core.worktree 键，必须仍走 dir(commonDir)，行为逐字不变。
func TestMainRepoPathNormalWorktreeUnchanged(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	main := newRepoWithCommit(t, filepath.Join(root, "main"))
	wt := newLinkedWorktree(t, main, filepath.Join(root, "wt"), "feature/x")

	if _, commonDir, ok := GitDirs(wt); !ok {
		t.Fatal("GitDirs(worktree) 失败")
	} else if v := commonConfigValue(wt, commonDir, "core.worktree"); v != "" {
		t.Fatalf("普通仓的 common 库不该有 core.worktree，实得 %q（新判据会改这里的行为）", v)
	}
	if got := MainRepoPath(wt); !samePath(got, main) {
		t.Errorf("MainRepoPath = %q, want 主仓 %q", got, main)
	}
	// 源树本身（非 worktree）仍返回空串——既有断言方向不许变。
	if got := MainRepoPath(main); got != "" {
		t.Errorf("MainRepoPath(主仓) = %q, want 空串", got)
	}
}

// TestMainRepoPathFallsBackWhenSrcInvalid 因果钉：把 core.worktree 写成无效相对路径后，
// 源树校验必须不过 → 退回 dir(commonDir)（假路径）→ WorktreeRemove 必须**失败**。
// 这条同时证明"归属解析"与"能否拆除"之间真有因果关系，而不是各自碰巧对。
func TestMainRepoPathFallsBackWhenSrcInvalid(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	wt, subSrc := newSubmoduleDerivedWorktree(t, root)

	_, commonDir, ok := GitDirs(wt)
	if !ok {
		t.Fatal("GitDirs(派生 worktree) 失败")
	}
	// 先把正确解析钉住，再破坏它：否则"退回假路径"可能只是没生效而已。
	if got := MainRepoPath(wt); !samePath(got, subSrc) {
		t.Fatalf("前置条件不成立：MainRepoPath = %q, want %q", got, subSrc)
	}
	// 破坏必须走 `--file` 且以该 worktree 为 cwd：在库目录或源树目录里跑，git 会先去 chdir
	// 这个坏值并在解析阶段 fatal，键本身都读不回来——那样测试执行的是"没有键"分支，
	// 源树校验分支一次都跑不到（实测踩过：变异掉校验后测试仍然绿，就是这种假绿）。
	write := Execute(wt, "config", "--file", filepath.Join(commonDir, "config"), "core.worktree", "../../../nope")
	if !write.Success {
		t.Fatalf("破坏 core.worktree 失败：%s", write.Output)
	}
	if v := commonConfigValue(wt, commonDir, "core.worktree"); v != "../../../nope" {
		t.Fatalf("前置条件不成立：写坏后应读回无效值，实得 %q（源树校验分支不会被执行）", v)
	}

	if got := MainRepoPath(wt); !samePath(got, filepath.Dir(commonDir)) {
		t.Errorf("校验不过应退回 dir(commonDir)，实得 %q", got)
	}
	res := WorktreeRemove(wt, false)
	if res.Success {
		t.Errorf("归属为假路径时 WorktreeRemove 必须失败（E6+E11 的因果链），实得成功")
	}
}
