package git

import (
	"fmt"
	"os/exec"
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
