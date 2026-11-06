package cmd

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/wamdy/gits/internal/config"
)

// withTestFlags 将全局 flag 置为测试值（-y 跳过 review、清空 -r/-g、关闭并行），
// 返回恢复函数。
func withTestFlags(t *testing.T) {
	t.Helper()
	prev := struct {
		repoIDs string
		group   string
		yes     bool
		par     bool
	}{repoIDs, group, yesFlag, parallel}
	repoIDs = ""
	group = ""
	yesFlag = true
	parallel = false
	t.Cleanup(func() {
		repoIDs = prev.repoIDs
		group = prev.group
		yesFlag = prev.yes
		parallel = prev.par
	})
}

// TestExitCodeSelectionFailed 覆盖选择/配置阶段四类失败的退出码语义（均应 exit 2）。
func TestExitCodeSelectionFailed(t *testing.T) {
	withTestFlags(t)

	cases := []struct {
		name    string
		repoIDs string
		cfg     *config.Config
	}{
		{"no_repositories", "", &config.Config{}},
		{"invalid_range", "1-5-8", testConfig()},
		{"set_not_found", "set:missing", testConfig()},
		{"wildcard_no_match", "nomatch-*", testConfig()},
		{"id_not_found", "99", testConfig()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exitCode = exitOK
			repoIDs = tc.repoIDs
			runGitCommandWithConfig(tc.cfg, []string{"status"})
			if exitCode != exitSelectionFailed {
				t.Errorf("exitCode = %d, want %d (selection failed)", exitCode, exitSelectionFailed)
			}
		})
	}
}

// TestExitCodeExecutionFailed 覆盖执行阶段有仓库失败时的退出码（应 exit 1）。
// 仓库指向不存在的目录，git 命令必然失败。
func TestExitCodeExecutionFailed(t *testing.T) {
	withTestFlags(t)

	exitCode = exitOK
	cfg := &config.Config{
		Repositories: []config.Repository{
			{ID: 1, Path: filepath.Join(t.TempDir(), "missing-repo"), Name: "missing"},
		},
	}
	runGitCommandWithConfig(cfg, []string{"status"})
	if exitCode != exitExecutionFailed {
		t.Errorf("exitCode = %d, want %d (execution failed)", exitCode, exitExecutionFailed)
	}
}

// TestExitCodeConfigScanWalkError 覆盖 config scan 对不存在路径的 walk 错误
// （同一「用法/输入错误 ⇒ exit 2」口径，#1070 测试棒补充证据要求纳入）。
func TestExitCodeConfigScanWalkError(t *testing.T) {
	exitCode = exitOK
	configScanCmd.Run(configScanCmd, []string{filepath.Join(t.TempDir(), "no-such-dir")})
	if exitCode != exitSelectionFailed {
		t.Errorf("exitCode = %d, want %d (scan walk error)", exitCode, exitSelectionFailed)
	}
}

// TestExitCodeExecutionSuccess 覆盖全部成功时的退出码（应 exit 0）。
func TestExitCodeExecutionSuccess(t *testing.T) {
	withTestFlags(t)

	dir := t.TempDir()
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	exitCode = exitOK
	cfg := &config.Config{
		Repositories: []config.Repository{
			{ID: 1, Path: dir, Name: "ok"},
		},
	}
	runGitCommandWithConfig(cfg, []string{"status"})
	if exitCode != exitOK {
		t.Errorf("exitCode = %d, want %d (all success)", exitCode, exitOK)
	}
}
