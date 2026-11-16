package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---------------------------------------------------------------- #1434 同构布局：集合登记的两个新可选键
//
// 这三条钉的是同一件事：`layout` / `mirror_root` 是**登记性**字段，
// 一旦落盘就必须与成员路径走同一套 StoredPath/ResolvePath（读写同源），
// 否则整目录搬迁后 mirror_root 会指向老位置，而集合还能"看起来正常"地列出来。

// TestWorktreeSetLayoutRoundTripKeepsStoredForm mirror 集合的往返：
// 落盘相对（含多段成员路径原样保留），读取解析为绝对，内存对象不被落盘改写。
func TestWorktreeSetLayoutRoundTripKeepsStoredForm(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, ".gits", "config.json")
	withConfigPath(t, cfgPath)

	mirrorRoot := filepath.Dir(base) // 缺省基准＝配置基目录的父目录
	main := filepath.Join(base, "appollo")
	mustMkdirAll(t, main)
	member := filepath.Join(base, ".worktrees", "demo", "appollo", "code", "tools", "gits")
	mustMkdirAll(t, member)

	cfg := NewDefaultConfig()
	cfg.Repositories = append(cfg.Repositories, Repository{ID: 1, Path: main, Name: "appollo", Groups: []string{}})
	cfg.WorktreeSets = []WorktreeSet{{
		Name:       "demo",
		Branch:     "harness/demo",
		Layout:     "mirror",
		MirrorRoot: mirrorRoot,
		Members:    []WorktreeMember{{RepoID: 2, Path: member}},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	stored := readStoredWorktreeSets(t, cfgPath)
	if len(stored) != 1 {
		t.Fatalf("stored sets = %#v, want 1", stored)
	}
	if stored[0].Layout != "mirror" {
		t.Errorf("stored layout = %q, want mirror", stored[0].Layout)
	}
	if stored[0].MirrorRoot != ".." {
		t.Errorf("stored mirror_root = %q, want 相对配置基目录的 %q", stored[0].MirrorRoot, "..")
	}
	// 同构布局的成员路径本就是多段相对路径，落盘口径与 flat 完全一致（不得被压平或改写）。
	if want := ".worktrees/demo/appollo/code/tools/gits"; stored[0].Members[0].Path != want {
		t.Errorf("stored member path = %q, want %q", stored[0].Members[0].Path, want)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	gotRoot := loaded.WorktreeSets[0].MirrorRoot
	if !filepath.IsAbs(gotRoot) {
		t.Errorf("loaded mirror_root = %q, want 绝对路径", gotRoot)
	}
	if !sameDirForTest(gotRoot, mirrorRoot) {
		t.Errorf("loaded mirror_root = %q, want %q", gotRoot, mirrorRoot)
	}
	if got := loaded.WorktreeSets[0].Members[0].Path; !filepath.IsAbs(got) {
		t.Errorf("loaded member path = %q, want 绝对路径", got)
	}
	// 内存对象不被落盘改写（与既有成员路径同口径的不变量）。
	if got := cfg.WorktreeSets[0].MirrorRoot; !filepath.IsAbs(got) {
		t.Errorf("in-memory mirror_root mutated to %q", got)
	}
}

// TestWorktreeSetFlatLayoutOmitted 缺省（""）不落 layout/mirror_root 键，
// 保证存量配置与 flat 集合在 diff 上零噪音（omitempty 纪律）。
// 注意契约分工：本包**如实序列化**调用方给的字符串，"flat 不写键"由命令层保证
// （`worktree add` 在 flat 下传空串，见 cmd 侧 #1434 钉），这里不替命令层兜改写。
func TestWorktreeSetFlatLayoutOmitted(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, ".gits", "config.json")
	withConfigPath(t, cfgPath)

	cfg := NewDefaultConfig()
	cfg.WorktreeSets = []WorktreeSet{
		{Name: "a", Branch: "main", Members: []WorktreeMember{{RepoID: 1, Path: base}}},
		{Name: "b", Branch: "main", Layout: "mirror", MirrorRoot: filepath.Dir(base), Members: []WorktreeMember{{RepoID: 1, Path: base}}},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save(): %v", err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(data)
	if strings.Contains(s, `"layout": "flat"`) {
		t.Errorf("不该凭空写出 flat:\n%s", s)
	}
	// flat 集合那一组必须整组无新键，mirror 那一组才有 layout——两条一起钉才防得住"整篇补键"。
	if strings.Count(s, `"layout"`) != 1 {
		t.Errorf("只有 mirror 集合该有 layout 键，实际出现 %d 次:\n%s", strings.Count(s, `"layout"`), s)
	}
	if strings.Count(s, `"mirror_root"`) != 1 {
		t.Errorf("只有 mirror 集合该有 mirror_root 键，实际出现 %d 次", strings.Count(s, `"mirror_root"`))
	}
}

// TestLegacyWorktreeSetWithoutLayoutKeys 兼容矩阵第一行：
// 没有这两个键的老集合记录读出来必须是 ""（＝flat），不许报错、更不许默认成 mirror。
func TestLegacyWorktreeSetWithoutLayoutKeys(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, ".gits", "config.json")
	withConfigPath(t, cfgPath)
	mustMkdirAll(t, filepath.Join(base, ".gits"))
	mustMkdirAll(t, filepath.Join(base, "main"))

	raw := `{"version":"1.0.0","language":"",` +
		`"repositories":[{"id":1,"path":"main","name":"main","groups":[]}],` +
		`"groups":{},"defaults":{"remote":"origin","branch":"main"},` +
		`"worktree_sets":[{"name":"old","branch":"harness/old",` +
		`"members":[{"repo_id":1,"path":".worktrees/old/main"}]}]}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write raw: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("老集合读取失败: %v", err)
	}
	if len(cfg.WorktreeSets) != 1 {
		t.Fatalf("WorktreeSets = %#v, want 1 组", cfg.WorktreeSets)
	}
	if got := cfg.WorktreeSets[0].Layout; got != "" {
		t.Errorf("Layout = %q, want \"\"（缺键＝flat，不许猜成 mirror）", got)
	}
	if got := cfg.WorktreeSets[0].MirrorRoot; got != "" {
		t.Errorf("MirrorRoot = %q, want \"\"", got)
	}
	if got := cfg.WorktreeSets[0].Members[0].Path; !filepath.IsAbs(got) {
		t.Errorf("成员路径未解析为绝对: %q", got)
	}
}

// sameDirForTest 按平台口径比较两个目录（Windows 路径大小写不敏感）。
func sameDirForTest(a, b string) bool {
	a = filepath.Clean(filepath.FromSlash(a))
	b = filepath.Clean(filepath.FromSlash(b))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// TestNormalizePathsCoversSetMembersAndMirrorRoot 钉 `config normalize` 的覆盖面：
// 它必须把集合成员路径与 `mirror_root` 一起收敛回相对形态，并且**计数算进 changed**。
//
// 为什么这条属于本票：normalize 的计数口径停在「仓库路径」是 #1056 时代的形状，那时还没有
// 集合键。只数仓库路径会报「无需规范化」（changed==0 → 整段直接 return，不落盘），
// 而绝对 `mirror_root` 就永远留在配置里——`member add` 要复用集合登记的基准，
// 基准不随搬迁相对化就会把新成员算到旧树上。这是「门全绿而功能死」的同型缺口。
func TestNormalizePathsCoversSetMembersAndMirrorRoot(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, ".gits", "config.json")
	withConfigPath(t, cfgPath)
	mustMkdirAll(t, filepath.Dir(cfgPath))

	// 手工造一份「全部是绝对路径」的存量配置：1 条仓 + 1 个 mirror 集合（2 个成员 + 绝对基准）。
	repoPath := filepath.Join(base, "appollo")
	mustMkdirAll(t, repoPath)
	memberA := filepath.Join(base, ".worktrees", "demo", "appollo")
	memberB := filepath.Join(memberA, "code", "tools", "gits")
	cfg := NewDefaultConfig()
	cfg.Repositories = append(cfg.Repositories, Repository{ID: 1, Path: repoPath, Name: "appollo", Groups: []string{}})
	cfg.WorktreeSets = []WorktreeSet{{
		Name: "demo", Branch: "harness/demo", Layout: "mirror", MirrorRoot: filepath.Dir(base),
		Members: []WorktreeMember{{RepoID: 1, Path: memberA}, {RepoID: 2, Path: memberB}},
	}}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// 1 仓库 + 1 基准 + 2 成员 = 4 处；changed 若仍只数仓库路径就会是 1（口径回归即红）。
	changed, err := NormalizePaths()
	if err != nil {
		t.Fatalf("NormalizePaths(): %v", err)
	}
	if changed != 4 {
		t.Errorf("changed = %d, want 4（仓库路径 + 镜像基准 + 2 条成员路径）", changed)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var stored Config
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("unmarshal stored: %v", err)
	}
	set := stored.WorktreeSets[0]
	if filepath.IsAbs(set.MirrorRoot) {
		t.Errorf("mirror_root 仍为绝对路径 %q（规范化没覆盖它）", set.MirrorRoot)
	}
	if set.MirrorRoot != ".." {
		t.Errorf("mirror_root = %q, want %q", set.MirrorRoot, "..")
	}
	if want := filepath.ToSlash(filepath.Join(".worktrees", "demo", "appollo")); set.Members[0].Path != want {
		t.Errorf("成员 0 落盘 = %q, want %q", set.Members[0].Path, want)
	}
	if want := filepath.ToSlash(filepath.Join(".worktrees", "demo", "appollo", "code", "tools", "gits")); set.Members[1].Path != want {
		t.Errorf("成员 1 落盘 = %q, want %q（多段相对路径必须原样保留）", set.Members[1].Path, want)
	}

	// 幂等：第二次必须 0 处改写（并因此整段不落盘）。
	changed, err = NormalizePaths()
	if err != nil {
		t.Fatalf("NormalizePaths() idempotent: %v", err)
	}
	if changed != 0 {
		t.Errorf("第二次 changed = %d, want 0", changed)
	}

	// 规范化后必须仍能解析回原位置——这是「搬迁不断链」的前提。
	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load after normalize: %v", err)
	}
	if got := reloaded.WorktreeSets[0].MirrorRoot; !sameDirForTest(got, filepath.Dir(base)) {
		t.Errorf("重读基准 = %q, want %q", got, filepath.Dir(base))
	}
	if got := reloaded.WorktreeSets[0].Members[1].Path; !sameDirForTest(got, memberB) {
		t.Errorf("重读成员 1 = %q, want %q", got, memberB)
	}
}
