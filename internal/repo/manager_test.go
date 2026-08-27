package repo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/git"
)

func TestFindReposByWildcard(t *testing.T) {
	repos := []config.Repository{
		{ID: 1, Name: "repo-backend"},
		{ID: 2, Name: "repo-frontend"},
		{ID: 3, Name: "genealogy-web"},
		{ID: 4, Name: "genealogy-api"},
	}

	tests := []struct {
		name    string
		pattern string
		want    []int // IDs
	}{
		{"prefix star", "repo*", []int{1, 2}},
		{"suffix star", "*api", []int{4}},
		{"question mark", "repo-????end", []int{1}},
		{"exact", "genealogy-web", []int{3}},
		{"no match", "xxxx*", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindReposByWildcard(tt.pattern, repos)
			if len(got) != len(tt.want) {
				t.Errorf("FindReposByWildcard(%q) = %d repos, want %d", tt.pattern, len(got), len(tt.want))
				return
			}
			for i, r := range got {
				if r.ID != tt.want[i] {
					t.Errorf("FindReposByWildcard(%q)[%d] = ID %d, want %d", tt.pattern, i, r.ID, tt.want[i])
				}
			}
		})
	}
}

func TestResolveSelection(t *testing.T) {
	repos := []config.Repository{
		{ID: 1, Name: "repo-backend"},
		{ID: 2, Name: "repo-frontend"},
		{ID: 3, Name: "genealogy-web"},
		{ID: 4, Name: "genealogy-api"},
		{ID: 5, Name: "tools"},
	}

	tests := []struct {
		name    string
		input   string
		want    []int // IDs
		wantErr bool
	}{
		{"pure IDs", "1,3,5", []int{1, 3, 5}, false},
		{"ID range", "2-4", []int{2, 3, 4}, false},
		{"wildcard", "repo*", []int{1, 2}, false},
		{"name exact", "tools", []int{5}, false},
		{"mixed", "1,repo*", []int{1, 2}, false},
		{"mixed with range", "1-2,genealogy*", []int{1, 2, 3, 4}, false},
		{"no match", "xxxx*", nil, false},
		{"dedup", "1,1,1-2", []int{1, 2}, false},
		{"invalid range", "5-3", nil, true},
		{"invalid range format", "1-5-8", nil, true},
		{"empty segment ignored", "1,,2", []int{1, 2}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveSelection(tt.input, repos, nil)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ResolveSelection(%q) expected error", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("ResolveSelection(%q) unexpected error: %v", tt.input, err)
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("ResolveSelection(%q) = %d repos, want %d", tt.input, len(got), len(tt.want))
				return
			}
			for i, r := range got {
				if r.ID != tt.want[i] {
					t.Errorf("ResolveSelection(%q)[%d] = ID %d, want %d", tt.input, i, r.ID, tt.want[i])
				}
			}
		})
	}
}

func TestResolveSelectionByBranch(t *testing.T) {
	repos := []config.Repository{
		{ID: 1, Path: "/a/backend", Name: "backend"},
		{ID: 2, Path: "/b/frontend", Name: "frontend"},
		{ID: 3, Path: "/c/web", Name: "web"},
	}
	branchOwners := map[string]bool{
		"/a/backend": true,
		"/c/web":     true,
	}
	checker := func(repoPath, pattern string) (bool, error) {
		return branchOwners[repoPath], nil
	}

	tests := []struct {
		name    string
		input   string
		want    []int
		wantErr bool
	}{
		{"single branch", "branch:feature/X", []int{1, 3}, false},
		{"mixed with ID", "2,branch:feature/X", []int{2, 1, 3}, false},
		{"dedup by ID", "1,branch:feature/X", []int{1, 3}, false},
		{"empty pattern", "branch:", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveSelection(tt.input, repos, checker)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ResolveSelection(%q) expected error", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("ResolveSelection(%q) unexpected error: %v", tt.input, err)
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("ResolveSelection(%q) = %d repos, want %d", tt.input, len(got), len(tt.want))
				return
			}
			for i, r := range got {
				if r.ID != tt.want[i] {
					t.Errorf("ResolveSelection(%q)[%d] = ID %d, want %d", tt.input, i, r.ID, tt.want[i])
				}
			}
		})
	}
}

func TestResolveSelectionByBranchErrors(t *testing.T) {
	repos := []config.Repository{
		{ID: 1, Path: "/a/backend", Name: "backend"},
	}
	// 传 nil checker + branch: 段 → 报错
	if _, err := ResolveSelection("branch:feature/X", repos, nil); err == nil {
		t.Error("ResolveSelection with branch: and nil checker expected error")
	}
	// checker 自身报错 → 透传
	errChecker := func(repoPath, pattern string) (bool, error) {
		return false, fmt.Errorf("boom")
	}
	if _, err := ResolveSelection("branch:feature/X", repos, errChecker); err == nil {
		t.Error("ResolveSelection with failing checker expected error")
	}
}

func TestLooksLikeIDRange(t *testing.T) {
	tests := []struct {
		seg  string
		want bool
	}{
		{"1", true},
		{"1-5", true},
		{"1-5-8", true},
		{"repo-backend", false},
		{"repo*", false},
		{"abc", false},
		{"-1", false},
	}
	for _, tt := range tests {
		t.Run(tt.seg, func(t *testing.T) {
			if got := looksLikeIDRange(tt.seg); got != tt.want {
				t.Errorf("looksLikeIDRange(%q) = %v, want %v", tt.seg, got, tt.want)
			}
		})
	}
}

func TestDisplayBranchOnRealRepo(t *testing.T) {
	// 使用 test-repos 下真实 git 仓库：正常分支应原样返回（非空即可，不假设分支名）。
	nonEmpty := DisplayBranch(git.GetBranchName("../../test-repos/repo1"), "../../test-repos/repo1")
	if nonEmpty == "" {
		t.Error("DisplayBranch(normal repo) should return a non-empty branch")
	}
}

func TestDisplayBranchDetachedHead(t *testing.T) {
	// 游离 HEAD（GetBranchName 返回 "HEAD"）应回退到短 commit id。
	// 此处用纯逻辑分支验证：branch=="HEAD" 且目标仓库有 commit → 返回非空 commit id。
	got := DisplayBranch("HEAD", "../../test-repos/repo1")
	if got == "" {
		t.Error("DisplayBranch('HEAD', realRepo) should fall back to a short commit id, got empty")
	}
	if got == "HEAD" {
		t.Error("DisplayBranch('HEAD', realRepo) should not return literal 'HEAD'")
	}
}

func TestDisplayBranchEdge(t *testing.T) {
	// 空仓库/不存在路径 → GetBranchName 与 commit id 均空 → 空串，不报错。
	if got := DisplayBranch("", "/nonexistent/path/for/test"); got != "" {
		t.Errorf("DisplayBranch('') = %q, want empty", got)
	}
}

func TestGetRepositoryBranches(t *testing.T) {
	// 正常仓库 → Branch 填充为当前分支（非空）；不存在路径 → Branch 兜底空串。
	repos := []config.Repository{
		{ID: 1, Path: "../../test-repos/repo1", Name: "repo1"},
		{ID: 2, Path: "/nonexistent/path/for/test", Name: "fake"},
	}
	got := GetRepositoryBranches(repos)
	if got[0].Branch == "" {
		t.Error("GetRepositoryBranches should fill a non-empty branch for a real repo")
	}
	if got[1].Branch != "" {
		t.Errorf("GetRepositoryBranches(nonexistent path) Branch = %q, want empty", got[1].Branch)
	}
}

func TestRepositoryBranchNotPersisted(t *testing.T) {
	// Repository.Branch 带 json:"-"，Marshal 后不得出现 branch 键（AC7 不持久化）。
	data, err := json.Marshal(config.Repository{ID: 1, Path: "/a", Name: "a", Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("branch")) {
		t.Errorf("Repository.Branch must not be serialized to JSON, got %s", data)
	}
}
