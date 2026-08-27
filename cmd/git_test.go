package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/repo"
)

// TestRepoReviewNames 覆盖命令前确认提示名称生成：
// 正常仓库 → "name (branch)"；不存在路径（展示值空串）→ 仅 "name"，不追加空后缀。
func TestRepoReviewNames(t *testing.T) {
	repos := []repo.RepositoryInfo{
		{Path: "../test-repos/repo1", Name: "normal"},
		{Path: "/nonexistent/path/for/test", Name: "empty"},
	}
	got := repoReviewNames(repos)
	// 正常仓库：应追加非空分支后缀。
	if !strings.Contains(got[0], "normal (") || !strings.HasSuffix(got[0], ")") {
		t.Errorf("repoReviewNames(normal) = %q, want a 'name (branch)' form", got[0])
	}
	// 不存在路径（DisplayBranch 兜底空串）：仅仓库名，不追加空括号。
	if got[1] != "empty" {
		t.Errorf("repoReviewNames(empty display) = %q, want just 'empty'", got[1])
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
