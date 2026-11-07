package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type ExecutionResult struct {
	RepoPath string
	Success  bool
	Output   string
	Error    error
}

func Execute(repoPath string, args ...string) ExecutionResult {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath

	output, err := cmd.CombinedOutput()
	result := ExecutionResult{
		RepoPath: repoPath,
		Output:   strings.TrimSpace(string(output)),
	}

	if err != nil {
		result.Success = false
		result.Error = err
	} else {
		result.Success = true
	}

	return result
}

func IsGitRepository(path string) bool {
	result := Execute(path, "rev-parse", "--is-inside-work-tree")
	return result.Success
}

func GetBranchName(path string) string {
	result := Execute(path, "rev-parse", "--abbrev-ref", "HEAD")
	if result.Success {
		return result.Output
	}
	return ""
}

// GetCommitIDShort 返回当前 HEAD 的短 commit id（git rev-parse --short HEAD）。
// 空仓库（无任何提交）或执行失败时返回空串；用于无分支（空/游离 HEAD）时替代分支显示。
func GetCommitIDShort(path string) string {
	result := Execute(path, "rev-parse", "--short", "HEAD")
	if result.Success {
		return result.Output
	}
	return ""
}

func GetStatus(path string) string {
	result := Execute(path, "status", "--porcelain")
	return result.Output
}

// BranchExists 检查 repoPath 仓库是否存在匹配 pattern 的分支（本地或远程任一）。
// pattern 支持 glob（* / ?），由 git for-each-ref 的 refname 模式实现。
func BranchExists(repoPath string, pattern string) (bool, error) {
	result := Execute(repoPath, "for-each-ref", "--count=1",
		"refs/heads/"+pattern, "refs/remotes/*/"+pattern)
	if !result.Success {
		return false, fmt.Errorf("git for-each-ref failed in %s: %w", repoPath, result.Error)
	}
	return result.Output != "", nil
}

// CurrentBranch 返回 repoPath 当前检出的本地分支名（git symbolic-ref -q --short HEAD）。
// 游离 HEAD（detached）与非 git 目录返回空串——调用方语义：不可能「检出」任何分支。
// #1067：`checkedout:` 选择段用它按「实际检出」过滤，与 BranchExists 的「ref 存在」语义区分
// （同一仓库的所有 linked worktree 共享 refs，ref 存在判定会把主检出与各 worktree 无差别全命中）。
func CurrentBranch(repoPath string) string {
	result := Execute(repoPath, "symbolic-ref", "-q", "--short", "HEAD")
	if !result.Success {
		return ""
	}
	return strings.TrimSpace(result.Output)
}

// GitEntryKind 表示一个目录相对 git 的身份（判定底座三态 + 非 git）。
type GitEntryKind int

const (
	KindNotGit         GitEntryKind = iota // 不是 git 仓库/工作树
	KindRepo                               // 普通仓库（`.git` 为目录）
	KindLinkedWorktree                     // git linked worktree（`.git` 为文件，git-dir ≠ common-dir）
	KindSubmodule                          // submodule（`.git` 为文件，git-dir == common-dir）
)

// GitDirs 返回 path 的 git 目录与 common 目录（都归一化为绝对路径）。
// 判据全部来自 git 自身的 rev-parse 输出，不靠路径字符串里是否出现 worktrees/modules 猜测：
//   - 普通仓库：两者同指 <repo>/.git；
//   - linked worktree：git-dir 落在 <main>/.git/worktrees/<name>，common-dir 是主仓 <main>/.git；
//   - submodule：两者同指 <parent>/.git/modules/<name>。
//
// --absolute-git-dir 恒为绝对路径；--git-common-dir 在部分 git 版本/运行位置返回相对路径
// （相对 cmd.Dir，即 path），故按 path 解析为绝对路径。
func GitDirs(path string) (gitDir, commonDir string, ok bool) {
	gitRes := Execute(path, "rev-parse", "--absolute-git-dir")
	if !gitRes.Success || gitRes.Output == "" {
		return "", "", false
	}
	commonRes := Execute(path, "rev-parse", "--git-common-dir")
	if !commonRes.Success || commonRes.Output == "" {
		return "", "", false
	}
	return absFrom(path, gitRes.Output), absFrom(path, commonRes.Output), true
}

// Classify 判定 path 的 git 身份（三态互斥）：
// `.git` 目录 → KindRepo；`.git` 文件且 git-dir ≠ common-dir → KindLinkedWorktree；
// `.git` 文件且两者相等 → KindSubmodule；rev-parse 失败 → KindNotGit。
// 无 `.git` 入口的目录（裸仓库、主仓内的子目录）由 rev-parse 兜底：两者相等视为 KindRepo。
// 注意 linked worktree **内部的子目录**两值仍不等，同样落 KindLinkedWorktree
// （归属仍是它的主仓，MainRepoPath/MainRepoName 给出正确主仓名，语义无害）——评审 S-03。
func Classify(path string) GitEntryKind {
	entry, err := os.Stat(filepath.Join(path, ".git"))
	if err == nil && entry.IsDir() {
		return KindRepo // 普通仓库：无需调用 git
	}
	hasGitFile := err == nil // .git 存在且不是目录 → gitfile 形态

	gitDir, commonDir, ok := GitDirs(path)
	if !ok {
		return KindNotGit
	}
	if !samePath(gitDir, commonDir) {
		return KindLinkedWorktree
	}
	if hasGitFile {
		return KindSubmodule
	}
	return KindRepo
}

// MainRepoPath 返回 linked worktree 所属主仓路径（common-dir 的父目录）。
// 仅 KindLinkedWorktree 有意义：普通仓库 / submodule / 非 git 目录一律返回空串。
func MainRepoPath(path string) string {
	gitDir, commonDir, ok := GitDirs(path)
	if !ok || samePath(gitDir, commonDir) {
		return ""
	}
	return filepath.Dir(commonDir)
}

// WorktreeAdd 在 repoPath 仓库为已存在的分支 branch 创建一个 linked worktree 到 worktreePath。
// 只建 worktree、不建分支：分支存在性由调用方在预检阶段负责（AC-02.2/02.3），
// 因此这里不传 -b，也永不删除分支。
func WorktreeAdd(repoPath, worktreePath, branch string) ExecutionResult {
	return Execute(repoPath, "worktree", "add", worktreePath, branch)
}

// WorktreeRemove 拆除 worktreePath 对应的 linked worktree。
// force 为真时把 --force 透传给 git，用于越过 git 自身的脏检查（AC-03.3）；
// 本函数不删分支、不主动删磁盘目录（目录由 git 自身回收），也不调用 git worktree prune。
// 命令在主仓目录执行：linked worktree 的登记信息归主仓所有。
func WorktreeRemove(worktreePath string, force bool) ExecutionResult {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, worktreePath)

	dir := MainRepoPath(worktreePath)
	if dir == "" {
		dir = worktreePath // 主仓已不可解析时退回自身，交给 git 报错
	}
	return Execute(dir, args...)
}

// WorktreeCheckedOutBranches 只读枚举该仓所有 worktree **当前已检出**的本地分支，
// 返回 分支名 → worktree 目录。判据取 `git worktree list --porcelain` 的 branch 行
// （游离 HEAD 输出 detached、无 branch 行；远程跟踪头 refs/remotes/* 不计入，
// 本工具只为本地分支建 worktree）。预检用它把「同一分支重复检出」这种
// git 必定在阶段 B 拒绝的组合提前到只读阶段点名（评审 Q-03），不产生任何副作用。
func WorktreeCheckedOutBranches(repoPath string) map[string]string {
	out := map[string]string{}
	res := Execute(repoPath, "worktree", "list", "--porcelain")
	if !res.Success {
		return out
	}
	current := ""
	for _, line := range strings.Split(res.Output, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "worktree "):
			current = absFrom(repoPath, strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch ") && current != "":
			ref := strings.TrimPrefix(line, "branch ")
			if strings.HasPrefix(ref, "refs/heads/") {
				out[strings.TrimPrefix(ref, "refs/heads/")] = current
			}
			current = "" // 一个 worktree 至多一条 branch 行，取完即失效
		default:
			if strings.HasPrefix(line, "detached") {
				current = ""
			}
		}
	}
	return out
}

// absFrom 把 git 输出（可能为相对路径、且统一使用 / 分隔符）归一化为绝对路径。
// base 为执行 git 的目录（与 Execute 的 cmd.Dir 同源），相对路径按它解析。
func absFrom(base, p string) string {
	candidate := filepath.FromSlash(strings.TrimSpace(p))
	if filepath.IsAbs(candidate) {
		return filepath.Clean(candidate)
	}
	if abs, err := filepath.Abs(filepath.Join(base, candidate)); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(filepath.Join(base, candidate))
}

// samePath 判断两个路径是否指向同一目录（分隔符归一；Windows 路径大小写不敏感）。
func samePath(a, b string) bool {
	a = filepath.Clean(filepath.FromSlash(a))
	b = filepath.Clean(filepath.FromSlash(b))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
