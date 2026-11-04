package cmd

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/i18n"
	"github.com/wamdy/gits/internal/repo"
)

// TestRepoDisplayNames 覆盖仓库回显名称生成（review 提示与逐仓结果行共用）：
// 已注册仓库 → "[ID] name (branch)"；未注册（ID=0）→ 省略 ID 前缀；
// 不存在路径（展示值空串）→ 不追加空括号。
func TestRepoDisplayNames(t *testing.T) {
	repos := []repo.RepositoryInfo{
		{ID: 7, Path: "../test-repos/repo1", Name: "normal"},
		{Path: "../test-repos/repo2", Name: "unregistered"},
		{ID: 8, Path: "/nonexistent/path/for/test", Name: "empty"},
	}
	got := repoDisplayNames(repos, nil)
	// 正常仓库：应带 ID 前缀与非空分支后缀。
	if !strings.HasPrefix(got[0], "[7] normal (") || !strings.HasSuffix(got[0], ")") {
		t.Errorf("repoDisplayNames(id=7) = %q, want '[7] normal (branch)' form", got[0])
	}
	// ID=0（scan 待添加仓库）：不应出现空的 [0] 前缀。
	if strings.HasPrefix(got[1], "[0]") {
		t.Errorf("repoDisplayNames(id=0) = %q, want no '[0]' prefix", got[1])
	}
	// 不存在路径（DisplayBranch 兜底空串）：仅 ID + 仓库名，不追加空括号。
	if got[2] != "[8] empty" {
		t.Errorf("repoDisplayNames(empty display) = %q, want '[8] empty'", got[2])
	}
}

func testConfig() *config.Config {
	return &config.Config{
		Repositories: []config.Repository{
			{ID: 1, Path: "/tmp/repo-backend", Name: "repo-backend"},
			{ID: 2, Path: "/tmp/repo-frontend", Name: "repo-frontend"},
			{ID: 3, Path: "/tmp/repo-tools", Name: "repo-tools"},
		},
	}
}

func assertSameSet(t *testing.T, got []repo.RepositoryInfo, want []string) {
	t.Helper()
	g := make(map[string]bool, len(got))
	for _, r := range got {
		g[r.Name] = true
	}
	if len(g) != len(want) {
		t.Fatalf("repo set = %v, want %v", g, want)
	}
	for _, n := range want {
		if !g[n] {
			t.Errorf("missing repo %q in %v", n, g)
		}
	}
}

// TestSelectReposByRepoRef 通过构造 config 注入，覆盖 -r 的
// ID / ID 区间 / 名称精确 / 名称通配 / 混合去重 / 无匹配 / 非法区间 路由。
func TestSelectReposByRepoRef(t *testing.T) {
	cfg := testConfig()

	tests := []struct {
		name   string
		raw    string
		want   []string
		errMsg string // 非空表示期望返回包含该子串的错误
	}{
		{"ID list", "1,3", []string{"repo-backend", "repo-tools"}, ""},
		{"ID range", "1-2", []string{"repo-backend", "repo-frontend"}, ""},
		{"exact name", "repo-frontend", []string{"repo-frontend"}, ""},
		{"wildcard", "repo-*", []string{"repo-backend", "repo-frontend", "repo-tools"}, ""},
		{"mixed ID and wildcard dedup", "2,repo-*", []string{"repo-backend", "repo-frontend", "repo-tools"}, ""},
		{"wildcard no match", "nomatch-*", nil, ""},
		{"ID no match", "99", nil, ""},
		{"invalid range", "1-5-8", nil, "invalid ID range"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectRepos(tt.raw, "", cfg)
			if tt.errMsg != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tt.errMsg)
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Fatalf("error = %q, want containing %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertSameSet(t, got, tt.want)
		})
	}
}

// TestSelectReposCarriesID 验证选择结果带上配置 ID（供回显 `[ID] 名称 (分支)` 使用）。
func TestSelectReposCarriesID(t *testing.T) {
	got, err := selectRepos("1,3", "", testConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []int{1, 3}
	if len(got) != len(want) {
		t.Fatalf("got %d repos, want %d", len(got), len(want))
	}
	for i, r := range got {
		if r.ID != want[i] {
			t.Errorf("repos[%d].ID = %d, want %d", i, r.ID, want[i])
		}
	}
}

// TestSelectReposConflict 验证 --group 与 --repo 互斥。
func TestSelectReposConflict(t *testing.T) {
	_, err := selectRepos("1", "group1", testConfig())
	if err == nil {
		t.Fatal("want conflict error, got nil")
	}
}

// TestSelectReposNoRepositories 验证配置无仓库时返回 errNoRepositories 哨兵。
func TestSelectReposNoRepositories(t *testing.T) {
	_, err := selectRepos("", "", &config.Config{})
	if !errors.Is(err, errNoRepositories) {
		t.Fatalf("want errNoRepositories, got %v", err)
	}
}

// cfgWithSet 在 testConfig 基础上登记一个 worktree 集合（成员指向 ID 1、3）。
func cfgWithSet() *config.Config {
	cfg := testConfig()
	cfg.WorktreeSets = []config.WorktreeSet{{
		Name:   "demo",
		Branch: "harness/demo",
		Members: []config.WorktreeMember{
			{RepoID: 1, Path: `/tmp/.worktrees/demo/repo-backend`},
			{RepoID: 3, Path: `/tmp/.worktrees/demo/repo-tools`},
		},
	}}
	return cfg
}

// TestSelectReposWorktreeSet 验证 -r 接入 ResolveSelectionWithSets：
// set: 命中集合成员、与 ID 混用按 ID 去重、集合缺失返回错误（不静默为空）。
func TestSelectReposWorktreeSet(t *testing.T) {
	cfg := cfgWithSet()

	got, err := selectRepos("set:demo", "", cfg)
	if err != nil {
		t.Fatalf("selectRepos(set:demo) 意外错误: %v", err)
	}
	assertSameSet(t, got, []string{"repo-backend", "repo-tools"})

	got, err = selectRepos("2,set:demo", "", cfg)
	if err != nil {
		t.Fatalf("selectRepos(2,set:demo) 意外错误: %v", err)
	}
	assertSameSet(t, got, []string{"repo-backend", "repo-frontend", "repo-tools"})

	if _, err := selectRepos("set:missing", "", cfg); err == nil {
		t.Error("selectRepos(set:missing) 期望报错，实得 nil")
	} else if !strings.Contains(err.Error(), "missing") {
		t.Errorf("selectRepos(set:missing) 错误应点名集合名，实得 %q", err.Error())
	}

	// 老配置（无集合）+ set: 段 → 同样报「集合不存在」，兼容矩阵第一行。
	if _, err := selectRepos("set:demo", "", testConfig()); err == nil {
		t.Error("老配置下 set: 段期望报错，实得 nil")
	}
}

// TestRepoDisplayNamesWorktreeMark 验证 review 提示/结果行沿用 worktree 归属标记口径，
// 主仓展示名从登记列表反查，查不到时按主仓路径 base 名兜底。
func TestRepoDisplayNamesWorktreeMark(t *testing.T) {
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	all := []config.Repository{
		{ID: 1, Path: filepath.Clean("/data/main"), Name: "登记主仓"},
		{ID: 2, Path: filepath.Clean("/data/.worktrees/demo/main"), Name: "wt-main"},
	}
	infos := []repo.RepositoryInfo{
		{ID: 2, Path: filepath.Clean("/data/.worktrees/demo/main"), Name: "wt-main", Branch: "feature/x", WorktreeOf: filepath.Clean("/data/main")},
		{ID: 2, Path: filepath.Clean("/data/.worktrees/demo/main"), Name: "wt-main", Branch: "feature/x", WorktreeOf: filepath.Clean("/data/unregistered-main")},
		{ID: 1, Path: filepath.Clean("/data/main"), Name: "登记主仓", Branch: "main"},
	}
	got := repoDisplayNames(infos, all)

	if got[0] != "[2] wt-main (feature/x) ← 登记主仓" {
		t.Errorf("worktree 回显 = %q, want %q", got[0], "[2] wt-main (feature/x) ← 登记主仓")
	}
	if got[1] != "[2] wt-main (feature/x) ← unregistered-main" {
		t.Errorf("主仓未登记时回显 = %q, want base 名兜底", got[1])
	}
	if strings.Contains(got[2], "←") {
		t.Errorf("普通仓库不应带归属标记: %q", got[2])
	}
}
