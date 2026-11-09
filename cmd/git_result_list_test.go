package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/wamdy/gits/internal/git"
	"github.com/wamdy/gits/internal/i18n"
)

// captureCmdStdout 捕获当前进程 stdout（cmd 包内无既有辅助函数，
// 与 internal/ui 的 captureStdout 同口径；结尾清单是纯打印逻辑，断言靠回读文本）。
func captureCmdStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(): %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return buf.String()
}

func TestSummarizeGitFailurePrefersFirstOutputLine(t *testing.T) {
	// git 的错误事实在 stderr 首行；exit status 128 信息量低，不得抢占
	result := git.ExecutionResult{
		Success: false,
		Output:  "\nerror: pathspec 'nope' did not match any file(s) known to git\nfatal: something else\n",
		Error:   errors.New("exit status 128"),
	}
	got := summarizeGitFailure(result)
	want := "error: pathspec 'nope' did not match any file(s) known to git"
	if got != want {
		t.Fatalf("summarizeGitFailure() = %q, want %q", got, want)
	}
}

func TestSummarizeGitFailureFallsBackToErrorThenGeneric(t *testing.T) {
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(i18n.Zh)

	if got, want := summarizeGitFailure(git.ExecutionResult{Error: errors.New("exit status 1")}), "exit status 1"; got != want {
		t.Fatalf("no output: got %q, want %q", got, want)
	}
	if got, want := summarizeGitFailure(git.ExecutionResult{Output: "  \n\t\n"}), i18n.T("git_cmd_failed"); got != want {
		t.Fatalf("empty everything: got %q, want %q", got, want)
	}
}

func TestSummarizeGitFailureCollapsesWhitespaceAndTruncatesRunes(t *testing.T) {
	long := strings.Repeat("仓", maxFailureSummaryRunes+40)
	got := summarizeGitFailure(git.ExecutionResult{Output: "  对齐    填充\t\t文本  "})
	if got != "对齐 填充 文本" {
		t.Fatalf("whitespace not collapsed: %q", got)
	}

	got = summarizeGitFailure(git.ExecutionResult{Output: long})
	if want := strings.Repeat("仓", maxFailureSummaryRunes) + "…"; got != want {
		t.Fatalf("truncation mismatch: len(runes)=%d", len([]rune(got)))
	}
	if !strings.HasPrefix(got, "仓") {
		t.Fatalf("truncation broke rune boundary: %q", got[:4])
	}
}

func TestItemsInSelectionOrderRestoresSelectionIndex(t *testing.T) {
	// 并行按完成顺序收集，这里模拟"完成顺序 ≠ 选择顺序"，验证归位且不改动入参
	items := []gitResultItem{
		{index: 2, label: "[3] repoC (main)", path: "/c", success: true},
		{index: 0, label: "[1] repoA (main)", path: "/a", success: false, errText: "boom"},
		{index: 1, label: "[2] repoB (main)", path: "/b", success: true},
	}
	ordered := itemsInSelectionOrder(items)
	if len(ordered) != 3 {
		t.Fatalf("len = %d, want 3", len(ordered))
	}
	for i, want := range []string{"/a", "/b", "/c"} {
		if ordered[i].path != want {
			t.Fatalf("ordered[%d].path = %q, want %q", i, ordered[i].path, want)
		}
	}
	if items[0].index != 2 {
		t.Fatalf("input slice mutated: items[0].index = %d, want 2", items[0].index)
	}
}

func TestCountGitResultsAndFailCountSync(t *testing.T) {
	items := []gitResultItem{
		{index: 0, success: true},
		{index: 1, success: false},
		{index: 2, success: false},
	}
	success, fail := countGitResults(items)
	if success != 1 || fail != 2 {
		t.Fatalf("countGitResults() = (%d,%d), want (1,2)", success, fail)
	}

	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(i18n.Zh)
	oldFail := failCount
	defer func() { failCount = oldFail }()

	captureCmdStdout(t, func() { printGitCommandSummary(items) })
	if failCount != 2 {
		t.Fatalf("failCount = %d, want 2（退出码 1 的依据）", failCount)
	}
}

func TestPrintGitCommandSummaryListsBothSectionsInSelectionOrder(t *testing.T) {
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(i18n.Zh)
	oldFail := failCount
	defer func() { failCount = oldFail }()

	// 完成顺序 repoC → repoA → repoB；清单必须按选择索引 A(1) B(2) C(3) 输出
	items := []gitResultItem{
		{index: 2, label: "[3] repoC (main)", path: "/tmp/c", success: true},
		{index: 0, label: "[1] repoA (main)", path: "/tmp/a", success: false, errText: "pathspec did not match"},
		{index: 1, label: "[2] repoB (main)", path: "/tmp/b", success: true},
	}
	out := captureCmdStdout(t, func() { printGitCommandSummary(items) })

	if !strings.Contains(out, "汇总") {
		t.Fatalf("missing count line: %q", out)
	}
	failedAt := strings.Index(out, "失败仓库")
	succeededAt := strings.Index(out, "成功仓库")
	if failedAt < 0 || succeededAt < 0 {
		t.Fatalf("missing section title(s): %q", out)
	}
	if failedAt > succeededAt {
		t.Fatalf("failed section must precede succeeded section: %q", out)
	}

	failedBlock := out[failedAt:succeededAt]
	if !strings.Contains(failedBlock, "[1] repoA (main)") || !strings.Contains(failedBlock, "pathspec did not match") {
		t.Fatalf("failed block missing label or reason: %q", failedBlock)
	}
	if strings.Contains(failedBlock, "repoB") || strings.Contains(failedBlock, "repoC") {
		t.Fatalf("success entries leaked into failed block: %q", failedBlock)
	}

	succeededBlock := out[succeededAt:]
	bPos := strings.Index(succeededBlock, "[2] repoB (main)")
	cPos := strings.Index(succeededBlock, "[3] repoC (main)")
	if bPos < 0 || cPos < 0 {
		t.Fatalf("succeeded block missing entries: %q", succeededBlock)
	}
	if bPos > cPos {
		t.Fatalf("succeeded block not in selection order: %q", succeededBlock)
	}
}

func TestPrintGitCommandSummaryOmitsEmptySection(t *testing.T) {
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(i18n.Zh)
	oldFail := failCount
	defer func() { failCount = oldFail }()

	allOK := []gitResultItem{
		{index: 0, label: "[1] repoA (main)", path: "/tmp/a", success: true},
	}
	out := captureCmdStdout(t, func() { printGitCommandSummary(allOK) })
	if strings.Contains(out, "失败仓库") {
		t.Fatalf("pure success must not print failed section: %q", out)
	}
	if !strings.Contains(out, "成功仓库") || !strings.Contains(out, "[1] repoA (main)") {
		t.Fatalf("pure success must list succeeded repositories: %q", out)
	}
	if failCount != 0 {
		t.Fatalf("failCount = %d, want 0", failCount)
	}

	allFail := []gitResultItem{
		{index: 0, label: "[1] repoA (main)", path: "/tmp/a", success: false, errText: "boom"},
	}
	out = captureCmdStdout(t, func() { printGitCommandSummary(allFail) })
	if strings.Contains(out, "成功仓库") {
		t.Fatalf("pure failure must not print succeeded section: %q", out)
	}
	if !strings.Contains(out, "失败仓库") || !strings.Contains(out, "boom") {
		t.Fatalf("pure failure must list failed repositories with reason: %q", out)
	}
	if failCount != 1 {
		t.Fatalf("failCount = %d, want 1", failCount)
	}
}

func TestNewGitResultItemOnlyCarriesReasonOnFailure(t *testing.T) {
	ok := newGitResultItem(0, "[1] repoA (main)", "/tmp/a", git.ExecutionResult{Success: true, Output: "Already up to date."})
	if ok.errText != "" {
		t.Fatalf("success item must not carry reason: %q", ok.errText)
	}
	bad := newGitResultItem(1, "[2] repoB (main)", "/tmp/b", git.ExecutionResult{Output: "fatal: not a git repository"})
	if bad.errText != "fatal: not a git repository" {
		t.Fatalf("failure item reason = %q", bad.errText)
	}
	if bad.index != 1 || bad.path != "/tmp/b" || bad.success {
		t.Fatalf("failure item fields wrong: %+v", bad)
	}
}
