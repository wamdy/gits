package ui

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/i18n"
	"github.com/wamdy/gits/internal/repo"
)

func TestInitColorMode(t *testing.T) {
	// 测试前重置颜色变量
	resetColors()

	tests := []struct {
		name    string
		mode    string
		noColor bool
	}{
		{"never", "never", false},
		{"always", "always", false},
		{"no-color flag", "auto", true},
		{"auto with no-color flag", "auto", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InitColorMode(tt.mode, tt.noColor)
			// always/never 分支可预测
			if tt.mode == "never" || tt.noColor {
				if ColorReset != "" {
					t.Errorf("InitColorMode(%q, %v): ColorReset = %q, want empty", tt.mode, tt.noColor, ColorReset)
				}
			} else if tt.mode == "always" {
				if ColorReset == "" {
					t.Errorf("InitColorMode(%q, %v): ColorReset is empty, want ANSI code", tt.mode, tt.noColor)
				}
			}
		})
	}
}

func TestInitColorModeDefaults(t *testing.T) {
	// 默认模式（auto）不应 panic
	InitColorMode("auto", false)
	// 非法模式不应 panic
	InitColorMode("", false)
}

// resetColors 重置颜色变量为初始状态。
func resetColors() {
	ColorReset = ""
	ColorRed = ""
	ColorGreen = ""
	ColorYellow = ""
	ColorBlue = ""
	ColorPurple = ""
	ColorCyan = ""
}

func captureStdout(t *testing.T, fn func()) string {
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

func TestPrintConfigSettings(t *testing.T) {
	resetColors()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(i18n.Zh)

	cfg := &config.Config{
		Version:      "1.0.0",
		Language:     "",
		Repositories: []config.Repository{},
		Groups:       map[string][]int{},
		Defaults:     config.Defaults{Remote: "origin", Branch: "main"},
	}

	out := captureStdout(t, func() { PrintConfigSettings(cfg) })

	for _, want := range []string{"配置项", "版本: 1.0.0", "语言: （未设置）", "默认远程: origin", "默认分支: main"} {
		if !strings.Contains(out, want) {
			t.Errorf("PrintConfigSettings output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintConfigSettingsEnglish(t *testing.T) {
	resetColors()
	i18n.SetLang(i18n.En)
	defer i18n.SetLang(i18n.Zh)

	cfg := &config.Config{
		Version:  "1.0.0",
		Language: "en",
		Defaults: config.Defaults{Remote: "upstream", Branch: "develop"},
	}

	out := captureStdout(t, func() { PrintConfigSettings(cfg) })

	for _, want := range []string{"Configuration", "Language: en", "Default remote: upstream", "Default branch: develop"} {
		if !strings.Contains(out, want) {
			t.Errorf("PrintConfigSettings output missing %q:\n%s", want, out)
		}
	}
}

// TestPrintWorktreeSetsBlock 验证 config show 的 worktree 集合块：
// 有集合时输出标题 + 集合名/分支/成员数 + 成员标签；无集合时整块不输出（界面不噪）。
func TestPrintWorktreeSetsBlock(t *testing.T) {
	resetColors()
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	all := []config.Repository{
		{ID: 1, Path: filepath.Clean("/data/main"), Name: "base"},
		{ID: 4, Path: filepath.Clean("/data/wiki"), Name: "wiki"},
	}
	sets := []config.WorktreeSet{{
		Name:   "20261104-demo",
		Branch: "harness/20261104-demo",
		Members: []config.WorktreeMember{
			{RepoID: 1, Path: filepath.Clean("/data/.worktrees/20261104-demo/base")},
			{RepoID: 4, Path: filepath.Clean("/data/.worktrees/20261104-demo/wiki")},
			{RepoID: 99, Path: filepath.Clean("/data/.worktrees/20261104-demo/gone")},
		},
	}}

	out := captureStdout(t, func() { PrintWorktreeSets(sets, all) })
	for _, want := range []string{
		"worktree 集合:",
		"20261104-demo",
		"分支: harness/20261104-demo",
		"3 个成员",
		"[1] base",
		"[4] wiki",
		filepath.Clean("/data/.worktrees/20261104-demo/base"),
		"[99] gone", // 成员仓已不在登记表：仍按路径 base 名展示，不吞掉
	} {
		if !strings.Contains(out, want) {
			t.Errorf("PrintWorktreeSets 输出缺少 %q:\n%s", want, out)
		}
	}

	empty := captureStdout(t, func() { PrintWorktreeSets(nil, all) })
	if strings.TrimSpace(empty) != "" {
		t.Errorf("无集合时不应输出任何内容，实得:\n%s", empty)
	}
}

// TestPrintScanCandidateMark 验证 scan 候选行经共用口径带上 worktree 归属标记。
func TestPrintScanCandidateMark(t *testing.T) {
	resetColors()
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	all := []config.Repository{{ID: 3, Path: filepath.Clean("/data/main"), Name: "主仓名"}}
	wt := repo.RepositoryInfo{
		Path:       filepath.Clean("/data/.worktrees/demo/main"),
		Name:       "main",
		Branch:     "feature/x",
		WorktreeOf: filepath.Clean("/data/main"),
	}

	out := captureStdout(t, func() { PrintScanCandidate(1, wt, 0, all) })
	if !strings.Contains(out, "#2") {
		t.Errorf("scan 行应带待选择序号 #2，实得:\n%s", out)
	}
	if !strings.Contains(out, "main (feature/x) ← 主仓名") {
		t.Errorf("scan 行应带 worktree 归属标记，实得:\n%s", out)
	}

	normal := repo.RepositoryInfo{Path: filepath.Clean("/data/main"), Name: "主仓名", Branch: "main"}
	out = captureStdout(t, func() { PrintScanCandidate(0, normal, 3, all) })
	if strings.Contains(out, "←") {
		t.Errorf("普通仓库不应带归属标记，实得:\n%s", out)
	}
	if !strings.Contains(out, "[3] 主仓名") || !strings.Contains(out, "已注册") {
		t.Errorf("已注册仓库应带配置 ID 与状态，实得:\n%s", out)
	}
}

// TestPrintRegisteredRepositoriesWorktreeMark 用真实 linked worktree 验证 config show 列表口径：
// worktree 行名称后带 ` ← 主仓名`，主仓行不带。
func TestPrintRegisteredRepositoriesWorktreeMark(t *testing.T) {
	resetColors()
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	root := t.TempDir()
	main := filepath.Join(root, "repo-main")
	if err := exec.Command("git", "init", "-q", main).Run(); err != nil {
		t.Skipf("git init 不可用，跳过真实 worktree 列出验证: %v", err)
	}
	commit := exec.Command("git", "-c", "user.email=t@example.com", "-c", "user.name=t",
		"commit", "-q", "--allow-empty", "-m", "init")
	commit.Dir = main
	if out, err := commit.CombinedOutput(); err != nil {
		t.Skipf("git commit 不可用，跳过: %v\n%s", err, out)
	}
	wt := filepath.Join(root, ".worktrees", "demo", "wt-main")
	if err := os.MkdirAll(filepath.Dir(wt), 0755); err != nil {
		t.Fatal(err)
	}
	add := exec.Command("git", "-c", "user.email=t@example.com", "-c", "user.name=t",
		"worktree", "add", "-b", "feature/show", wt)
	add.Dir = main
	if out, err := add.CombinedOutput(); err != nil {
		t.Skipf("git worktree add 不可用，跳过: %v\n%s", err, out)
	}

	repos := repo.GetRepositoryBranches([]config.Repository{
		{ID: 1, Path: main, Name: "repo-main", Groups: []string{}},
		{ID: 2, Path: wt, Name: "wt-main", Groups: []string{}},
	})
	out := captureStdout(t, func() { PrintRegisteredRepositories(repos) })
	if !strings.Contains(out, "wt-main") || !strings.Contains(out, " ← repo-main") {
		t.Errorf("config show 的 worktree 行应带归属标记，实得:\n%s", out)
	}
	if strings.Count(out, "←") != 1 {
		t.Errorf("归属标记只应出现在 worktree 行，实得:\n%s", out)
	}
}

// TestPrintWorktreeSetList 覆盖 worktree list 的渲染（评审 BLOCK-3 指出的零触达单元）：
// 无集合走「暂无」文案；成员目录不存在时必须标 missing，存在的走仓库条目名。
// 不依赖真实 git——用「目录存在与否」这条分支即可钉住输出形状。
func TestPrintWorktreeSetList(t *testing.T) {
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	out := captureStdout(t, func() { PrintWorktreeSetList(nil, nil) })
	if strings.TrimSpace(out) != i18n.T("worktree_no_sets") {
		t.Errorf("空集合输出 = %q, want %q", strings.TrimSpace(out), i18n.T("worktree_no_sets"))
	}

	alive := t.TempDir()
	gone := filepath.Join(t.TempDir(), "not-created")
	sets := []config.WorktreeSet{{
		Name: "demo", Branch: "harness/demo",
		Members: []config.WorktreeMember{{RepoID: 3, Path: alive}, {RepoID: 4, Path: gone}},
	}}
	allRepos := []config.Repository{{ID: 3, Name: "repoA@demo", Path: alive}, {ID: 4, Name: "repoB@demo", Path: gone}}

	out = captureStdout(t, func() { PrintWorktreeSetList(sets, allRepos) })
	for _, want := range []string{"demo", "harness/demo", "repoA@demo", "repoB@demo", i18n.T("worktree_missing")} {
		if !strings.Contains(out, want) {
			t.Errorf("list 输出缺少 %q，实得:\n%s", want, out)
		}
	}
	// 存在的成员不应带 missing 标记（两条成员行只应有一条 missing）。
	if got := strings.Count(out, i18n.T("worktree_missing")); got != 1 {
		t.Errorf("missing 标记出现 %d 次, want 1（只有消失的那个成员）：\n%s", got, out)
	}
}

// TestPrintGroupDetailsMarkUsesFullRegistry 钉住评审 Q-01（AC-03.7 的「三处列出同一口径」）：
// 单组视图把**全量登记表**传给归属反查。组里只有 worktree 条目、主仓不在组内时，
// 标记仍取主仓的登记名（main-name），而不是退回目录 Base 名（repo-main）。
func TestPrintGroupDetailsMarkUsesFullRegistry(t *testing.T) {
	resetColors()
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	root := t.TempDir()
	main := filepath.Join(root, "repo-main")
	if err := exec.Command("git", "init", "-q", main).Run(); err != nil {
		t.Skipf("git init 不可用，跳过真实 worktree 反查验证: %v", err)
	}
	commit := exec.Command("git", "-c", "user.email=t@example.com", "-c", "user.name=t",
		"commit", "-q", "--allow-empty", "-m", "init")
	commit.Dir = main
	if out, err := commit.CombinedOutput(); err != nil {
		t.Skipf("git commit 不可用，跳过: %v\n%s", err, out)
	}
	wt := filepath.Join(root, ".worktrees", "gq", "repo-main")
	if err := os.MkdirAll(filepath.Dir(wt), 0755); err != nil {
		t.Fatal(err)
	}
	add := exec.Command("git", "-c", "user.email=t@example.com", "-c", "user.name=t",
		"worktree", "add", "-b", "feature/group", wt)
	add.Dir = main
	if out, err := add.CombinedOutput(); err != nil {
		t.Skipf("git worktree add 不可用，跳过: %v\n%s", err, out)
	}

	mainEntry := config.Repository{ID: 1, Path: main, Name: "main-name", Groups: []string{"g"}}
	wtEntry := config.Repository{ID: 2, Path: wt, Name: "repo-main@gq", Groups: []string{"g"}}
	all := repo.GetRepositoryBranches([]config.Repository{mainEntry, wtEntry})

	// 组内只有 worktree 条目（主仓不在组里）——这是 Q-01 的触发形态。
	out := captureStdout(t, func() { PrintGroupDetails("g", []config.Repository{all[1]}, all) })
	if !strings.Contains(out, "← main-name") {
		t.Errorf("单组视图应按全量登记表反查主仓登记名，实得:\n%s", out)
	}
	if strings.Contains(out, "← repo-main") {
		t.Errorf("不得退回目录 Base 名（与 config show 口径发散），实得:\n%s", out)
	}
}
