package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/git"
	"github.com/wamdy/gits/internal/repo"
)

// ---------------------------------------------------------------- #1434 扩范围：集合成员级增删
//
// 动手前必须先回答一个 #1064 留下的缺口：**集合根目录没有登记**。当初 `--dir` 可以指
// 任意位置，配置里只存了每条成员的最终路径。所以成员增删不能"重新算根"，否则：
//   - flat 还好——成员互为兄弟，父目录就是根，可精确派生；
//   - mirror 不行——成员深度不同，「最浅成员的父目录」只有在恰好有成员直接挂在根下时才等于根，
//     猜错的后果是把新成员放到另一棵树上并登记成功，之后 `-r set:` 全跑在错地方。
// 裁决（D11）：本票起 `worktree add` 把根登记进集合（`root`，第三个 omitempty 键，
// 与 layout/mirror_root 同一套相对化口径）；存量集合按上面两条规则处理，mirror 且无根 ⇒ 拒绝。

// memberFixture 造一个 mirror 集合（appollo + appollo/code/tools/gits 两层）与对应登记表。
func memberFixture() (config.WorktreeSet, []config.Repository) {
	root := filepath.Join("D:", "ws")
	setRoot := filepath.Join(root, ".worktrees", "demo")
	a := filepath.Join(setRoot, "appollo")
	b := filepath.Join(setRoot, "appollo", "code", "tools", "gits")
	set := config.WorktreeSet{
		Name: "demo", Branch: "harness/demo", Layout: layoutMirror, MirrorRoot: root, Root: setRoot,
		Members: []config.WorktreeMember{{RepoID: 3, Path: a}, {RepoID: 4, Path: b}},
	}
	all := []config.Repository{
		{ID: 1, Name: "appollo", Path: filepath.Join(root, "appollo")},
		{ID: 2, Name: "gits", Path: filepath.Join(root, "appollo", "code", "tools", "gits")},
		{ID: 3, Name: "appollo@demo", Path: a},
		{ID: 4, Name: "gits@demo", Path: b},
	}
	return set, all
}

// TestWorktreeSetRootRegistered 钉主路径：登记过 `root` 就直接用它——零 git 调用、零猜测。
func TestWorktreeSetRootRegistered(t *testing.T) {
	withZhLang(t)
	set, _ := memberFixture()
	want := filepath.Join("D:", "ws", ".worktrees", "demo")
	got, err := worktreeSetRoot(set)
	if err != nil {
		t.Fatalf("已登记根的集合不该失败: %v", err)
	}
	if got != want {
		t.Errorf("集合根 = %q, want %q", got, want)
	}
}

// TestWorktreeSetRootDerivedForFlatLegacy 钉存量 flat 集合（无 root 键）的精确派生：
// 成员互为兄弟 → 父目录即根；但**全部成员必须同父**，不同父说明被手工改过。
func TestWorktreeSetRootDerivedForFlatLegacy(t *testing.T) {
	withZhLang(t)
	setRoot := filepath.Join("D:", "wt", "demo")
	set := config.WorktreeSet{Name: "demo", Branch: "harness/demo",
		Members: []config.WorktreeMember{
			{RepoID: 3, Path: filepath.Join(setRoot, "a")},
			{RepoID: 4, Path: filepath.Join(setRoot, "b")},
		}}
	got, err := worktreeSetRoot(set)
	if err != nil {
		t.Fatalf("flat 存量集合应可精确派生根: %v", err)
	}
	if got != setRoot {
		t.Errorf("集合根 = %q, want %q", got, setRoot)
	}

	amb := config.WorktreeSet{Name: "demo", Branch: "b",
		Members: []config.WorktreeMember{
			{RepoID: 3, Path: filepath.Join("D:", "wt", "s1", "a")},
			{RepoID: 4, Path: filepath.Join("D:", "wt", "s2", "b")},
		}}
	if _, err := worktreeSetRoot(amb); err == nil {
		t.Error("成员不同父必须拒绝，不能挑一个当根")
	}
}

// TestWorktreeSetRootRefusesUnregisteredMirror 是本组命令最重要的一条拒绝：
// mirror 集合没有登记根时**不许**用「最浅成员的父目录」凑——那个值只在恰好有成员
// 直接挂在根下时才等于根，否则新成员会被放到别的树上并登记成功（后续 `-r set:` 静默跑错地方）。
func TestWorktreeSetRootRefusesUnregisteredMirror(t *testing.T) {
	withZhLang(t)
	set, _ := memberFixture()
	set.Root = "" // 模拟手工编辑/异常配置：mirror 但无根
	if _, err := worktreeSetRoot(set); err == nil {
		t.Fatal("mirror 集合缺登记根必须拒绝")
	} else if !strings.Contains(err.Error(), "root") {
		t.Errorf("拒绝文案必须给出可执行修法（补 root 键或重建集合），实得 %q", err.Error())
	}

	// 空集合同样拒绝——空集合本就由 prune 删除，不留可加成员的空壳。
	if _, err := worktreeSetRoot(config.WorktreeSet{Name: "demo", Branch: "b"}); err == nil {
		t.Error("空集合无法确定根，应拒绝")
	}
}

// TestWorktreeMemberAddPlanUsesSetLayout 钉「布局随集合」：
// 加进 mirror 集合按同构落位、加进 flat 集合按压平落位；命令面**不接受**布局旗标覆盖
// ——中途改口径会让同一个集合里出现两种结构，而 `list` 只报一种。
func TestWorktreeMemberAddPlanUsesSetLayout(t *testing.T) {
	withZhLang(t)
	root := filepath.Join("D:", "ws")
	setRoot := filepath.Join(root, ".worktrees", "demo")
	set, all := memberFixture()

	newer := []repo.RepositoryInfo{info(5, "wiki", filepath.Join(root, "wiki"))}
	paths, issues, err := worktreeMemberAddPlan(set, all, newer)
	if err != nil {
		t.Fatalf("加成员计划失败: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("不该有问题，实得 %q", issues)
	}
	if want := filepath.Join(setRoot, "wiki"); paths[0] != want {
		t.Errorf("mirror 集合新成员 = %q, want %q", paths[0], want)
	}

	// 嵌套新成员必须保留父目录段（同构的意义本身）。
	nested := []repo.RepositoryInfo{info(6, "deep", filepath.Join(root, "vendor", "pkg", "deep"))}
	paths, _, err = worktreeMemberAddPlan(set, all, nested)
	if err != nil {
		t.Fatalf("嵌套新成员计划失败: %v", err)
	}
	if want := filepath.Join(setRoot, "vendor", "pkg", "deep"); paths[0] != want {
		t.Errorf("嵌套新成员 = %q, want %q（父目录段不许丢）", paths[0], want)
	}

	flatSet, flatAll := memberFixture()
	flatSet.Layout, flatSet.MirrorRoot = "", ""
	flatSet.Root = setRoot
	flatSet.Members = []config.WorktreeMember{{RepoID: 3, Path: filepath.Join(setRoot, "appollo")}}
	paths, _, err = worktreeMemberAddPlan(flatSet, flatAll, newer)
	if err != nil {
		t.Fatalf("flat 加成员计划失败: %v", err)
	}
	if want := filepath.Join(setRoot, "wiki"); paths[0] != want {
		t.Errorf("flat 集合新成员 = %q, want %q（目录名取仓名）", paths[0], want)
	}
}

// TestWorktreeMemberAddPlanRejectsExisting 钉「已在集合里就点名拒绝」，不静默跳过。
// 判据是**源仓路径**（同一源仓的第二条登记条目不算新成员；只比成员条目 ID 会漏）。
func TestWorktreeMemberAddPlanRejectsExisting(t *testing.T) {
	withZhLang(t)
	root := filepath.Join("D:", "ws")
	set, all := memberFixture()

	dup := []repo.RepositoryInfo{info(1, "appollo", filepath.Join(root, "appollo"))}
	_, issues, err := worktreeMemberAddPlan(set, all, dup)
	if err != nil {
		t.Fatalf("不该整体失败: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0], "appollo") {
		t.Fatalf("重复成员应点名拒绝，实得 %q", issues)
	}

	// 一条重复 + 一条全新：只点名重复的那条，全新那条仍要算出路径（全量收集口径，不首错即返）。
	mixed := []repo.RepositoryInfo{
		info(1, "appollo", filepath.Join(root, "appollo")),
		info(5, "wiki", filepath.Join(root, "wiki")),
	}
	paths, issues, err := worktreeMemberAddPlan(set, all, mixed)
	if err != nil {
		t.Fatalf("混合计划失败: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("应只点名 1 条重复，实得 %q", issues)
	}
	if paths[0] != "" || paths[1] == "" {
		t.Errorf("被拒成员路径应留空、可用成员路径应算出，实得 %q", paths)
	}

	// 基准之外的新成员：走 mirrorMemberPaths 的同一套拒绝（不降级成 flat）。
	outside := []repo.RepositoryInfo{info(7, "elsewhere", filepath.Join("E:", "other"))}
	_, issues, _ = worktreeMemberAddPlan(set, all, outside)
	if len(issues) == 0 {
		t.Error("不在镜像基准之下的新成员必须被拒绝")
	}
}

// TestFindWorktreeMember 钉成员定位口径：ID 与登记条目名都能指到，
// 指不到必须报错——静默返回「没找到所以什么都不做」会报成功。
func TestFindWorktreeMember(t *testing.T) {
	set, all := memberFixture()

	if idx, err := findWorktreeMember(set, all, "4"); err != nil || idx != 1 {
		t.Errorf("按 ID 定位 = %d, %v, want 1", idx, err)
	}
	if idx, err := findWorktreeMember(set, all, "appollo@demo"); err != nil || idx != 0 {
		t.Errorf("按条目名定位 = %d, %v, want 0", idx, err)
	}
	if idx, err := findWorktreeMember(set, all, "  gits@demo  "); err != nil || idx != 1 {
		t.Errorf("带空白的条目名应被收敛，= %d, %v, want 1", idx, err)
	}
	for _, bad := range []string{"", "   ", "99", "nope", "demo"} {
		if _, err := findWorktreeMember(set, all, bad); err == nil {
			t.Errorf("成员引用 %q 不该被指到", bad)
		}
	}
}

// TestWorktreeMemberChildMembers 是本组命令最硬的一条保护：
// **被移除成员内部还包着同集合其它成员**。这正是评审 B1 的单成员版——
// 先拆祖先会把整棵后代目录连带删掉，后代随即拆不到、集合登记留在原地。
// 顺序责任交回给人（先拆后代），工具不做静默连带删除。
func TestWorktreeMemberChildMembers(t *testing.T) {
	set, _ := memberFixture()

	if nested := worktreeMemberChildMembers(set, 0); len(nested) != 1 || nested[0] != 1 {
		t.Errorf("成员 0（appollo）内部应含成员 1，实得 %v", nested)
	}
	if nested := worktreeMemberChildMembers(set, 1); len(nested) != 0 {
		t.Errorf("最深成员内部不该有其它成员，实得 %v", nested)
	}
	// 兄弟成员互不包含。
	siblings := config.WorktreeSet{Name: "s", Branch: "b", Root: filepath.Join("D:", "wt", "s"),
		Members: []config.WorktreeMember{
			{RepoID: 3, Path: filepath.Join("D:", "wt", "s", "a")},
			{RepoID: 4, Path: filepath.Join("D:", "wt", "s", "b")},
		}}
	for i := range siblings.Members {
		if nested := worktreeMemberChildMembers(siblings, i); len(nested) != 0 {
			t.Errorf("兄弟成员 %d 不该互含，实得 %v", i, nested)
		}
	}
}

// TestCheckWorktreeMemberRemovable 把「祖先保护」落到错误文案上：必须点名后代成员，
// 让人知道下一步该移谁，而不是只说「不能删」。
func TestCheckWorktreeMemberRemovable(t *testing.T) {
	withZhLang(t)
	set, _ := memberFixture()

	if _, err := checkWorktreeMemberRemovable(set, 0); err == nil {
		t.Fatal("移除内部还有其它成员的祖先必须被拦")
	} else if !strings.Contains(err.Error(), "gits") {
		t.Errorf("拦截文案应点名挡住它的后代成员，实得 %q", err.Error())
	}
	if _, err := checkWorktreeMemberRemovable(set, 1); err != nil {
		t.Errorf("最深成员不该被拦: %v", err)
	}
	if _, err := checkWorktreeMemberRemovable(set, 9); err == nil {
		t.Error("成员索引越界必须报错，不许静默放过")
	}
}

// TestRemoveWorktreeMember 钉登记侧口径：移出后集合还在就留、清空则整组删除
// （与 prune / config remove 既有口径一致，不留空集合），
// 且**不得顺手改动集合名/分支/布局/基准/根**——口径随集合，增删成员不改口径。
func TestRemoveWorktreeMember(t *testing.T) {
	set, _ := memberFixture()
	kept, empty := removeWorktreeMember(set, 0)
	if empty {
		t.Error("还剩 1 个成员，不该判成空集合")
	}
	if len(kept.Members) != 1 || kept.Members[0].RepoID != 4 {
		t.Errorf("移出后成员 = %v, want 仅 RepoID 4", kept.Members)
	}
	if kept.Name != "demo" || kept.Branch != set.Branch || kept.Layout != layoutMirror ||
		kept.MirrorRoot != set.MirrorRoot || kept.Root != set.Root {
		t.Error("移出成员不得改动集合名/分支/布局/基准/根")
	}

	one := config.WorktreeSet{Name: "solo", Branch: "b", Members: []config.WorktreeMember{{RepoID: 7, Path: "p"}}}
	if _, empty := removeWorktreeMember(one, 0); !empty {
		t.Error("最后一个成员移出后应判空集合（由调用方整组删除）")
	}
}

// TestMemberSubcommandsRegistered 钉命令面真接上了（"契约钉形状不钉用法"的同型坑：
// 纯函数全绿而 `gits worktree member add` 根本不存在，等于功能没交付）。
func TestMemberSubcommandsRegistered(t *testing.T) {
	member := findSubcommand(worktreeCmd, "member")
	if member == nil {
		t.Fatal("worktree 未注册 member 子命令")
	}
	for _, name := range []string{"add", "remove"} {
		sub := findSubcommand(member, name)
		if sub == nil {
			t.Fatalf("worktree member 缺子命令 %q", name)
		}
		if sub.Run == nil {
			t.Errorf("worktree member %s 未接 Run", name)
		}
	}
	if member.Run == nil {
		t.Error("裸 `worktree member` 必须有出口（未知子命令报错退非 0，沿 Q-05 口径）")
	}
	// add 不接受布局旗标（口径随集合登记，中途改会让同一集合出现两种结构）。
	if add := findSubcommand(member, "add"); add != nil && add.Flags().Lookup("layout") != nil {
		t.Error("member add 不该出现 --layout")
	}
	// remove 只接受 --force（脏判据透传口径与整组 remove 一致）。
	if rm := findSubcommand(member, "remove"); rm != nil && rm.Flags().Lookup("force") == nil {
		t.Error("member remove 必须有 --force")
	}
}

func findSubcommand(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

// TestMemberAddRemoveRealGitChain 用真 git 跑一遍成员级增删（本票扩范围部分的端到端）：
// ① 初始 mirror 集合两层建成；
// ② 加一个成员 → 路径按同构落位、条目登记、`rev-parse --show-toplevel` 落在新 worktree 自身；
// ③ 移除内部还包着别的成员的祖先 → 拒绝并点名后代；
// ④ 移除最深的单个成员 → worktree 消失、**分支仍在**、自动登记条目被回收、集合仍留着；
// ⑤ 加过成员的集合仍可整组拆除（证明 member add 没把集合变成拆不掉）。
func TestMemberAddRemoveRealGitChain(t *testing.T) {
	requireGit(t)
	withZhLang(t)

	root := t.TempDir()
	outer := newMainRepo(t, filepath.Join(root, "outer"), "harness/demo")
	innerSrc := newMainRepo(t, filepath.Join(root, "outer", "tools", "inner"), "harness/demo")
	extraSrc := newMainRepo(t, filepath.Join(root, "extra"), "harness/demo")

	setRoot := filepath.Join(root, "wt", "demo")
	cfg := &config.Config{}
	for _, r := range []repo.RepositoryInfo{info(1, "outer", outer), info(2, "inner", innerSrc), info(5, "extra", extraSrc)} {
		if _, _, err := config.EnsureRepositoryEntry(cfg, r.Path, r.Name); err != nil {
			t.Fatalf("登记源仓 %s: %v", r.Path, err)
		}
	}

	// ① 建集合：祖先 + 后代两个成员，mirror 口径。
	repos := []repo.RepositoryInfo{info(1, "outer", outer), info(2, "inner", innerSrc)}
	paths, issues, mirrorRoot := planWorktreeMembers(repos, setRoot, layoutMirror, root)
	if len(issues) != 0 {
		t.Fatalf("初始建组不该有问题: %q", issues)
	}
	labels := []string{"[1] outer", "[2] inner"}
	created, failed := createWorktrees(repos, paths, labels, "harness/demo", worktreeBuildOrder(repos, paths, true))
	if failed != "" || len(created) != 2 {
		t.Fatalf("初始建组失败: %q created=%v", failed, created)
	}
	members, _, err := buildWorktreeSetMembers(cfg, repos, paths, "demo")
	if err != nil {
		t.Fatalf("登记成员条目: %v", err)
	}
	lk, mr := worktreeSetLayoutKeys(layoutMirror, mirrorRoot)
	set := config.WorktreeSet{Name: "demo", Branch: "harness/demo", Layout: lk, MirrorRoot: mr,
		Root: setRoot, Members: members}

	// ② 加成员：extra 落 `<集合根>/extra`。
	newer := []repo.RepositoryInfo{info(5, "extra", extraSrc)}
	addPaths, addIssues, err := worktreeMemberAddPlan(set, cfg.Repositories, newer)
	if err != nil {
		t.Fatalf("加成员计划失败: %v", err)
	}
	if len(addIssues) != 0 {
		t.Fatalf("加成员不该有问题: %q", addIssues)
	}
	wantExtra := filepath.Join(setRoot, "extra")
	if addPaths[0] != wantExtra {
		t.Fatalf("新成员路径 = %q, want %q", addPaths[0], wantExtra)
	}
	if _, f := createWorktrees(newer, addPaths, []string{"[5] extra"}, set.Branch,
		worktreeBuildOrder(newer, addPaths, true)); f != "" {
		t.Fatalf("新成员 worktree 创建失败: %q", f)
	}
	newMembers, _, err := buildWorktreeSetMembers(cfg, newer, addPaths, "demo")
	if err != nil || len(newMembers) != 1 {
		t.Fatalf("登记新成员条目: len=%d err=%v", len(newMembers), err)
	}
	set.Members = append(set.Members, newMembers...)
	if got := gitTopLevel(t, wantExtra); !samePathForTest(got, wantExtra) {
		t.Errorf("新成员上 rev-parse --show-toplevel = %q, want %q", got, wantExtra)
	}

	// ③ 祖先保护。
	if kids := worktreeMemberChildMembers(set, 0); len(kids) == 0 {
		t.Fatal("夹具失效：outer 内部应含 inner，祖先保护测不到")
	}
	if _, err := checkWorktreeMemberRemovable(set, 0); err == nil {
		t.Error("移除内部还有其它成员的祖先必须拒绝并点名后代")
	}

	// ④ 移除最深的新成员 extra。
	idx, err := findWorktreeMember(set, cfg.Repositories, "extra@demo")
	if err != nil {
		t.Fatalf("按条目名定位新成员失败: %v", err)
	}
	if _, err := checkWorktreeMemberRemovable(set, idx); err != nil {
		t.Fatalf("最深成员不该被拦: %v", err)
	}
	// addPaths 是"本次新增"的切片（长度 1），idx 是集合成员索引，两者不同轴，直接用目标路径。
	res := git.WorktreeRemove(wantExtra, false)
	if !res.Success {
		t.Fatalf("移除新成员失败: %v %s", res.Error, res.Output)
	}
	if _, err := os.Stat(wantExtra); err == nil {
		t.Error("新成员目录仍在磁盘（未真拆除）")
	}
	if out := gitIn(t, extraSrc, "branch", "--list", "harness/demo"); !strings.Contains(out, "harness/demo") {
		t.Error("member remove 之后分支消失了——违反永不删分支红线")
	}
	kept, empty := removeWorktreeMember(set, idx)
	if empty || len(kept.Members) != 2 {
		t.Errorf("移出 1 个后应剩 2 个成员且集合仍留着，实得 empty=%v members=%v", empty, kept.Members)
	}
	if dropped := config.DropWorktreeMemberRepos(cfg, []config.WorktreeMember{set.Members[idx]}); dropped != 1 {
		t.Errorf("应回收新成员的自动登记条目 1 条，实得 %d", dropped)
	}

	// ⑤ 剩余成员仍可整组拆除。
	if failed := teardownWorktreeSet(labels, paths, false); failed != 0 {
		t.Errorf("加过成员的集合整组拆除应全成功，实得失败 %d", failed)
	}
}
