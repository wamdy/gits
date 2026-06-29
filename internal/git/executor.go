package git

import (
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

func GetStatus(path string) string {
	result := Execute(path, "status", "--porcelain")
	return result.Output
}
