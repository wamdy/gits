package repo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/git"
	"github.com/wamdy/gits/internal/i18n"
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

// TestDisplayLabel 覆盖仓库回显标签口径：ID>0 带 `[ID] ` 前缀，
// 分支为空省略括号，两者皆缺时退回纯名称。
func TestDisplayLabel(t *testing.T) {
	cases := []struct {
		id     int
		name   string
		branch string
		want   string
	}{
		{3, "repo-backend", "mluat", "[3] repo-backend (mluat)"},
		{3, "repo-backend", "", "[3] repo-backend"},
		{0, "newrepo", "main", "newrepo (main)"},
		{0, "newrepo", "", "newrepo"},
	}
	for _, c := range cases {
		if got := DisplayLabel(c.id, c.name, c.branch); got != c.want {
			t.Errorf("DisplayLabel(%d, %q, %q) = %q, want %q", c.id, c.name, c.branch, got, c.want)
		}
	}
}

// TestDisplayLabelWithWorktree 覆盖列出口径的 worktree 归属标记：
// 主仓名为空（普通仓库）时与 DisplayLabel 完全一致；非空时追加 ` ← 主仓名`。
func TestDisplayLabelWithWorktree(t *testing.T) {
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	if got := DisplayLabelWithWorktree(2, "wt-a", "feature/x", ""); got != "[2] wt-a (feature/x)" {
		t.Errorf("非 worktree 标签 = %q, want 不带归属标记", got)
	}
	if got := DisplayLabelWithWorktree(2, "wt-a", "feature/x", "主仓"); got != "[2] wt-a (feature/x) ← 主仓" {
		t.Errorf("worktree 标签 = %q, want %q", got, "[2] wt-a (feature/x) ← 主仓")
	}
	if got := WorktreeMark(""); got != "" {
		t.Errorf("WorktreeMark(\"\") = %q, want empty", got)
	}
}

// TestMainRepoName 覆盖主仓展示名反查：登记命中取 Name，查不到取路径 base 名，空路径返回空串。
func TestMainRepoName(t *testing.T) {
	repos := []config.Repository{
		{ID: 1, Path: filepath.Clean("/data/main"), Name: "主仓名"},
		{ID: 2, Path: filepath.Clean("/data/other"), Name: "other"},
	}
	if got := MainRepoName(filepath.Clean("/data/main"), repos); got != "主仓名" {
		t.Errorf("MainRepoName(登记命中) = %q, want 主仓名", got)
	}
	if got := MainRepoName(filepath.Clean("/data/unregistered"), repos); got != "unregistered" {
		t.Errorf("MainRepoName(未登记) = %q, want 路径 base 名 unregistered", got)
	}
	if got := MainRepoName("", repos); got != "" {
		t.Errorf("MainRepoName(空) = %q, want empty", got)
	}
	if got := MainRepoName(filepath.Clean("/data/main"), nil); got != "main" {
		t.Errorf("MainRepoName(无登记列表) = %q, want base 名 main", got)
	}
}

// TestRepoLabels 覆盖 review 提示/结果行共用标签：worktree 带归属标记，普通仓不带。
func TestRepoLabels(t *testing.T) {
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	all := []config.Repository{
		{ID: 1, Path: filepath.Clean("/data/main"), Name: "主仓名"},
		{ID: 2, Path: filepath.Clean("/data/.worktrees/demo/main"), Name: "wt-main"},
	}
	infos := []RepositoryInfo{
		{ID: 2, Path: filepath.Clean("/data/.worktrees/demo/main"), Name: "wt-main", Branch: "feature/x", WorktreeOf: filepath.Clean("/data/main")},
		{ID: 1, Path: filepath.Clean("/data/main"), Name: "主仓名", Branch: "main"},
	}
	got := RepoLabels(infos, all)
	if got[0] != "[2] wt-main (feature/x) ← 主仓名" {
		t.Errorf("RepoLabels(worktree) = %q, want %q", got[0], "[2] wt-main (feature/x) ← 主仓名")
	}
	if got[1] != "[1] 主仓名 (main)" {
		t.Errorf("RepoLabels(main) = %q, want %q", got[1], "[1] 主仓名 (main)")
	}
}

// TestWorktreeMainNameRealWorktree 用真实 linked worktree 验证运行时归属判定 + 反查主仓名。
func TestWorktreeMainNameRealWorktree(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "repo-main")
	gitInit(t, main)
	gitCommitIn(t, main)
	wt := filepath.Join(root, ".worktrees", "demo", "wt-main")
	if err := os.MkdirAll(filepath.Dir(wt), 0755); err != nil {
		t.Fatal(err)
	}
	gitWorktreeAdd(t, main, wt, "feature/label")

	all := []config.Repository{{ID: 1, Path: main, Name: "登记名"}}
	if got := WorktreeMainName(wt, all); got != "登记名" {
		t.Errorf("WorktreeMainName(worktree) = %q, want 登记名", got)
	}
	if got := WorktreeMainName(main, all); got != "" {
		t.Errorf("WorktreeMainName(主仓) = %q, want empty", got)
	}
	// 主仓未登记时按主仓路径 base 名兜底。
	if got := WorktreeMainName(wt, nil); got != "repo-main" {
		t.Errorf("WorktreeMainName(worktree, 无登记) = %q, want repo-main", got)
	}
	if got := DetectWorktreeOf(filepath.Join(root, "not-a-repo")); got != "" {
		t.Errorf("DetectWorktreeOf(非 git) = %q, want empty", got)
	}
}

// TestResolveSelectionWithSets 覆盖 -r 的 set: 段：命中集合、与 ID 混用去重、
// 集合缺失报错（不静默返回空）、空集合名报错、sets 为 nil（老配置）时报错、成员仓已删则跳过。
func TestResolveSelectionWithSets(t *testing.T) {
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	repos := []config.Repository{
		{ID: 1, Name: "repo-backend"},
		{ID: 2, Name: "repo-frontend"},
		{ID: 3, Name: "tools"},
		{ID: 5, Name: "wiki"},
	}
	sets := []config.WorktreeSet{
		{Name: "demo", Branch: "harness/demo", Members: []config.WorktreeMember{
			{RepoID: 1, Path: `/data/.worktrees/demo/backend`},
			{RepoID: 5, Path: `/data/.worktrees/demo/wiki`},
			{RepoID: 99, Path: `/data/.worktrees/demo/gone`}, // 成员仓已被 config remove，选择时跳过
		}},
		{Name: "other", Branch: "harness/other", Members: []config.WorktreeMember{{RepoID: 2}}},
	}

	tests := []struct {
		name    string
		raw     string
		sets    []config.WorktreeSet
		want    []int
		wantErr string
	}{
		{"集合命中", "set:demo", sets, []int{1, 5}, ""},
		{"另一集合命中", "set:other", sets, []int{2}, ""},
		{"与 ID 混用去重", "1,set:demo", sets, []int{1, 5}, ""},
		{"与区间混用去重", "1-2,set:demo", sets, []int{1, 2, 5}, ""},
		{"集合不存在", "set:missing", sets, nil, "未找到 worktree 集合：missing"},
		{"空集合名", "set:", sets, nil, "set: 后的集合名不能为空"},
		{"老配置无集合（sets 为 nil）", "set:demo", nil, nil, "未找到 worktree 集合：demo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveSelectionWithSets(tt.raw, repos, tt.sets, nil)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ResolveSelectionWithSets(%q) 期望错误 %q，实得 nil", tt.raw, tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("ResolveSelectionWithSets(%q) 错误 = %q, want %q", tt.raw, err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveSelectionWithSets(%q) 意外错误: %v", tt.raw, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ResolveSelectionWithSets(%q) = %d 个仓库，want %d（%v）", tt.raw, len(got), len(tt.want), tt.want)
			}
			for i, r := range got {
				if r.ID != tt.want[i] {
					t.Errorf("ResolveSelectionWithSets(%q)[%d] = ID %d, want %d", tt.raw, i, r.ID, tt.want[i])
				}
			}
		})
	}
}

// TestResolveSelectionThinShell 验证旧签名保持可用（manager_test/cmd 既有调用不破）：
// 传 nil sets 时既有段类型行为不变，set: 段则报错。
func TestResolveSelectionThinShell(t *testing.T) {
	repos := []config.Repository{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}
	got, err := ResolveSelection("1-2", repos, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("ResolveSelection(1-2) = %v, %v; want 2 repos, nil", got, err)
	}
	if _, err := ResolveSelection("set:any", repos, nil); err == nil {
		t.Error("ResolveSelection 对 set: 段应报错（无集合可查）")
	}
}

// TestMemberLabelSingleSource 钉住评审 S-10：成员展示名只有一处口径——
// 命中登记表取仓库名，未登记或名称为空退回成员路径 Base（三处列出共用本函数）。
func TestMemberLabelSingleSource(t *testing.T) {
	byID := IndexByID([]config.Repository{
		{ID: 3, Name: "repoA@demo", Path: filepath.Join("root", ".worktrees", "demo", "repoA")},
		{ID: 4, Name: "", Path: filepath.Join("root", ".worktrees", "demo", "repoB")},
	})
	if got := MemberLabel(config.WorktreeMember{RepoID: 3, Path: filepath.Join("root", "x", "y")}, byID); got != "repoA@demo" {
		t.Errorf("命中登记表应取登记名，实得 %q", got)
	}
	if got := MemberLabel(config.WorktreeMember{RepoID: 4, Path: filepath.Join("root", "wt", "repoB")}, byID); got != "repoB" {
		t.Errorf("登记名为空应退回路径 Base，实得 %q", got)
	}
	if got := MemberLabel(config.WorktreeMember{RepoID: 99, Path: filepath.Join("root", "wt", "gone")}, byID); got != "gone" {
		t.Errorf("未登记成员应退回路径 Base，实得 %q", got)
	}
	if len(IndexByID(nil)) != 0 {
		t.Error("IndexByID(nil) 应为空索引")
	}
}
