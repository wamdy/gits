package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestReassignIDsRemapWorktreeMembers 是评审 BLOCK-1 的回归钉：`config reid` 改仓库 ID 时
// 必须同步回写 worktree 集合成员的 RepoID。漏改的后果不是"报错"而是**静默错指向**：
// `-r set:` 会选中别的条目或干脆选不到，随后 `config validate` 把指不到的成员当悬空永久删除。
func TestReassignIDsRemapWorktreeMembers(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, ".gits")
	mustMkdirAll(t, dir)
	cfgPath := filepath.Join(dir, "config.json")
	withConfigPath(t, cfgPath)

	seed := &Config{
		Version:  "1.0.0",
		Language: "zh",
		Repositories: []Repository{
			{ID: 7, Name: "a", Path: filepath.Join(base, "a")},
			{ID: 9, Name: "b", Path: filepath.Join(base, "b")},
		},
		Groups:   map[string][]int{"g": {7, 9}},
		Defaults: Defaults{Remote: "origin", Branch: "main"},
		WorktreeSets: []WorktreeSet{{
			Name: "s", Branch: "feat/x",
			Members: []WorktreeMember{
				{RepoID: 7, Path: filepath.Join(base, ".worktrees", "s", "a")},
				{RepoID: 9, Path: filepath.Join(base, ".worktrees", "s", "b")},
			},
		}},
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	if err := os.WriteFile(cfgPath, raw, 0644); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	if err := ReassignRepositoryID(7, 1); err != nil {
		t.Fatalf("ReassignRepositoryID(7,1): %v", err)
	}
	sets := readStoredWorktreeSets(t, cfgPath)
	if len(sets) != 1 || len(sets[0].Members) != 2 {
		t.Fatalf("集合结构被破坏: %+v", sets)
	}
	if ids := []int{sets[0].Members[0].RepoID, sets[0].Members[1].RepoID}; ids[0] != 1 || ids[1] != 9 {
		t.Errorf("单点 reid 后成员 = %v, want [1 9]（7 已改名为 1）", ids)
	}

	if err := ReassignAllIDs(); err != nil {
		t.Fatalf("ReassignAllIDs: %v", err)
	}
	sets = readStoredWorktreeSets(t, cfgPath)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	repoIDs := map[int]bool{}
	for _, r := range cfg.Repositories {
		repoIDs[r.ID] = true
	}
	for _, m := range sets[0].Members {
		if !repoIDs[m.RepoID] {
			t.Errorf("续号后成员 RepoID %d 在 repositories 里不存在——validate 会把它当悬空成员删掉", m.RepoID)
		}
	}
	if len(sets[0].Members) != 2 {
		t.Errorf("成员数 = %d, want 2", len(sets[0].Members))
	}
}

// TestDropWorktreeMemberRepos 覆盖评审 BLOCK-2 的条目回收：`remove`/`prune` 之后，
// add 自动登记的仓库条目必须一并消失，否则每次默认全量批量命令都会为死目录报 chdir 失败。
// 同时钉住路径守卫：成员若指向父仓条目（早期/手工配置），**一条都不许删**。
func TestDropWorktreeMemberRepos(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "a")
	wt := filepath.Join(base, ".worktrees", "s", "a")
	cfg := &Config{
		Repositories: []Repository{
			{ID: 1, Name: "a", Path: parent},
			{ID: 2, Name: "a@s", Path: wt},
		},
	}

	// 正常形态：成员指向 worktree 条目 ⇒ 只删该条目，父仓保留。
	n := DropWorktreeMemberRepos(cfg, []WorktreeMember{{RepoID: 2, Path: wt}})
	if n != 1 {
		t.Errorf("应回收 1 个条目，实得 %d", n)
	}
	if len(cfg.Repositories) != 1 || cfg.Repositories[0].ID != 1 {
		t.Fatalf("父仓条目必须保留，实得 %+v", cfg.Repositories)
	}
	if again := DropWorktreeMemberRepos(cfg, []WorktreeMember{{RepoID: 2, Path: wt}}); again != 0 {
		t.Errorf("重复回收应幂等返回 0，实得 %d", again)
	}

	// 路径守卫：成员指向父仓 ID 但路径不等 ⇒ 一条都不能删（防把父仓整条抹掉）。
	guard := &Config{Repositories: []Repository{{ID: 1, Name: "a", Path: parent}}}
	if n := DropWorktreeMemberRepos(guard, []WorktreeMember{{RepoID: 1, Path: wt}}); n != 0 || len(guard.Repositories) != 1 {
		t.Errorf("路径不等时不得删条目：dropped=%d 剩余=%+v", n, guard.Repositories)
	}
	// 路径相等但形态是父仓（早期配置：成员 RepoID 指父仓、路径也写了父仓）——按定义就是它自己的条目，
	// 仍受"路径相等"这一条守卫保护，交由 remove/prune 的成员语义处理；此处只钉住不误删第三方条目。
	other := &Config{Repositories: []Repository{{ID: 1, Name: "a", Path: parent}, {ID: 2, Name: "b", Path: filepath.Join(base, "b")}}}
	if n := DropWorktreeMemberRepos(other, []WorktreeMember{{RepoID: 1, Path: parent}}); n != 1 || len(other.Repositories) != 1 || other.Repositories[0].ID != 2 {
		t.Errorf("只应删命中成员，实得 dropped=%d 剩余=%+v", n, other.Repositories)
	}
	if n := DropWorktreeMemberRepos(nil, []WorktreeMember{{RepoID: 1}}); n != 0 {
		t.Errorf("nil cfg 应返回 0，实得 %d", n)
	}
}

// TestEnsureRepositoryEntryRegistersWorktree 钉住 #1064 复核发现的缺陷根因：
// worktree 集合的成员要能指向「worktree 目录自己的仓库条目」，而不是只能指向父仓条目。
// 契约：按解析后的绝对路径复用既有条目（同路径不产生双条目）；缺失时以首选名追加新条目
// （名称冲突由 uniqueRepoName 兜底、ID 由 nextRepoID 续号）；**只改内存、不落盘**，
// 由调用方在同一次 Save() 里把条目与集合一起写出。
func TestEnsureRepositoryEntryRegistersWorktree(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "repoA")
	member := filepath.Join(root, ".worktrees", "demo", "repoA")
	cfg := &Config{Repositories: []Repository{{ID: 3, Name: "repoA", Path: parent}}}

	entry, added, err := EnsureRepositoryEntry(cfg, member, "repoA@demo")
	if err != nil {
		t.Fatalf("首次登记意外报错: %v", err)
	}
	if !added {
		t.Error("首次登记应判为新建，实得 added=false")
	}
	if entry.ID != 4 || entry.Name != "repoA@demo" || entry.Path != filepath.Clean(member) {
		t.Errorf("新条目 = %+v, want ID=4 Name=repoA@demo Path=%s", entry, filepath.Clean(member))
	}
	if len(cfg.Repositories) != 2 {
		t.Errorf("仓库条目数 = %d, want 2（父仓 + worktree）", len(cfg.Repositories))
	}

	// 同路径再登记：复用既有条目、不新增（add 重跑或用户已手工 config add 时不出双条目）。
	again, added2, err := EnsureRepositoryEntry(cfg, member, "repoA@demo")
	if err != nil || added2 || again.ID != entry.ID || len(cfg.Repositories) != 2 {
		t.Errorf("同路径复登记应复用：added=%v id=%d 条目数=%d err=%v", added2, again.ID, len(cfg.Repositories), err)
	}

	// 名称冲突走 uniqueRepoName 兜底（集合名不同 ⇒ 目录不同 ⇒ 首选名重复）。
	other, added3, err := EnsureRepositoryEntry(cfg, filepath.Join(root, ".worktrees", "demo2", "repoA"), "repoA@demo")
	if err != nil || !added3 {
		t.Fatalf("第二个集合成员应新建成功: added=%v err=%v", added3, err)
	}
	if other.Name != "repoA@demo_2" || other.ID != 5 {
		t.Errorf("名称冲突兜底 = %q(ID %d), want repoA@demo_2(ID 5)", other.Name, other.ID)
	}

	// 首选名为空时退回目录名；nil cfg 报错而不 panic。
	fallback, _, err := EnsureRepositoryEntry(cfg, filepath.Join(root, "loose"), "")
	if err != nil || fallback.Name != filepath.Base(filepath.Join(root, "loose")) {
		t.Errorf("空首选名应退回目录名: name=%q err=%v", fallback.Name, err)
	}
	if _, _, err := EnsureRepositoryEntry(nil, member, "x"); err == nil {
		t.Error("nil cfg 应返回错误而非 panic")
	}
}

// TestDetachRepoFromWorktreeSets 覆盖 TQ-01 / AC-03.6 的连带清理纯逻辑：
// 删仓时该 ID 的成员从所有集合移出并点名受影响集合；成员清空的集合整组删除，不留悬空 ID。
func TestDetachRepoFromWorktreeSets(t *testing.T) {
	cfg := &Config{
		Repositories: []Repository{{ID: 1, Name: "base"}, {ID: 2, Name: "wiki"}},
		WorktreeSets: []WorktreeSet{
			{Name: "a", Branch: "harness/a", Members: []WorktreeMember{{RepoID: 1, Path: "p1"}, {RepoID: 2, Path: "p2"}}},
			{Name: "b", Branch: "harness/b", Members: []WorktreeMember{{RepoID: 2, Path: "p3"}}},
			{Name: "c", Branch: "harness/c", Members: []WorktreeMember{{RepoID: 1, Path: "p4"}}},
		},
	}

	affected := detachRepoFromWorktreeSets(cfg, 2)
	if len(affected) != 2 || affected[0] != "a" || affected[1] != "b" {
		t.Errorf("受影响集合 = %q, want [a b]", affected)
	}
	if len(cfg.WorktreeSets) != 2 {
		t.Fatalf("应剩 2 个集合（b 被清空删除），实得 %d", len(cfg.WorktreeSets))
	}
	if cfg.WorktreeSets[0].Name != "a" || len(cfg.WorktreeSets[0].Members) != 1 ||
		cfg.WorktreeSets[0].Members[0].RepoID != 1 {
		t.Errorf("a 集合应只剩 repo 1，实得 %+v", cfg.WorktreeSets[0])
	}

	// 未命中任何集合时不改动集合表（含 nil 场景，落盘形态不变）。
	untouched := &Config{WorktreeSets: []WorktreeSet{{Name: "z", Members: []WorktreeMember{{RepoID: 7}}}}}
	if got := detachRepoFromWorktreeSets(untouched, 9); len(got) != 0 {
		t.Errorf("未命中时不应点名集合，实得 %q", got)
	}
	if len(untouched.WorktreeSets) != 1 {
		t.Errorf("未命中时不应改动集合，实得 %+v", untouched.WorktreeSets)
	}
}

// TestDetachDanglingWorktreeMembers 覆盖 config validate 的兜底清理：
// 集合里指向已不存在仓库 ID 的成员被移出并计数，成员清空的集合整组删除。
func TestDetachDanglingWorktreeMembers(t *testing.T) {
	cfg := &Config{
		Repositories: []Repository{{ID: 1, Name: "base"}},
		WorktreeSets: []WorktreeSet{
			{Name: "a", Members: []WorktreeMember{{RepoID: 1}, {RepoID: 99}}},
			{Name: "b", Members: []WorktreeMember{{RepoID: 99}}},
		},
	}
	if removed := detachDanglingWorktreeMembers(cfg); removed != 2 {
		t.Errorf("应移除 2 个悬空成员，实得 %d", removed)
	}
	if len(cfg.WorktreeSets) != 1 || cfg.WorktreeSets[0].Name != "a" ||
		len(cfg.WorktreeSets[0].Members) != 1 || cfg.WorktreeSets[0].Members[0].RepoID != 1 {
		t.Errorf("清理后集合 = %+v, want 只剩 a(repo 1)", cfg.WorktreeSets)
	}

	// 无集合时零改动。
	empty := &Config{}
	if removed := detachDanglingWorktreeMembers(empty); removed != 0 || empty.WorktreeSets != nil {
		t.Errorf("无集合时不应产生改动：%d %+v", removed, empty.WorktreeSets)
	}
}
