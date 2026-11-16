package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/git"
	"github.com/wamdy/gits/internal/i18n"
	"github.com/wamdy/gits/internal/repo"
	"github.com/wamdy/gits/internal/ui"
)

// ---------------------------------------------------------------- #1434 集合成员级增删
//
// 集合是「一次建、整体拆」的编成单位，但现实里成员会变：加一个仓进同一编成、
// 拆掉其中一个而不动其它。本文件提供 `worktree member add|remove`。
//
// 三条与整组命令**共享**的口径（不许在这里另立一套）：
//   - 布局随集合登记：member add 不接受 --layout/--mirror-root，中途改口径会让同一集合
//     出现两种结构，而 `list` 只报一种；
//   - 脏判据 / 永不删分支 / `--force` 只透传给 git：与 `worktree remove` 同一条红线；
//   - 自动登记条目双向回收：add 建条目、remove 收条目（ADR-151 的生命周期表）。

var wtMemberForce bool

var worktreeMemberCmd = &cobra.Command{
	Use:   "member",
	Short: "Add or remove members of an existing worktree set",
	Args:  cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) > 0 {
			exitOnError(errors.New(i18n.T("worktree_unknown_subcommand", "member "+strings.Join(args, " "))))
		}
		cmd.Help()
	},
}

var worktreeMemberAddCmd = &cobra.Command{
	Use:   "add <set-name> -r <selection>",
	Short: "Create worktrees for additional repositories and append them to a set",
	Args:  cobra.ExactArgs(1),
	Run:   runWorktreeMemberAdd,
}

var worktreeMemberRemoveCmd = &cobra.Command{
	Use:   "remove <set-name> <member> [--force]",
	Short: "Remove one member from a set (its worktree is torn down; the branch is kept)",
	Args:  cobra.ExactArgs(2),
	Run:   runWorktreeMemberRemove,
}

// worktreeSetRoot 确定集合根目录（成员增删的落位锚点）。
//
// `root` 是本票新增的登记键；只有**本票之前建的存量集合**才会缺它：
//   - flat 存量集合：成员互为兄弟，父目录就是根，可**精确**派生（仍要求全部同父）；
//   - mirror 存量集合：成员深度不一，任何"取最浅成员父目录"的做法都只是碰巧成立——
//     猜错就是把新成员放到另一棵树并登记成功，之后 `-r set:` 全跑在错地方。
//     故直接拒绝并给出修法（补 root 键或重建集合），绝不猜。
func worktreeSetRoot(set config.WorktreeSet) (string, error) {
	if strings.TrimSpace(set.Root) != "" {
		return filepath.Clean(set.Root), nil
	}
	if len(set.Members) == 0 {
		return "", fmt.Errorf("%s", i18n.T("worktree_set_root_unavailable", set.Name, i18n.T("worktree_set_root_empty")))
	}
	if set.Layout == layoutMirror {
		return "", fmt.Errorf("%s", i18n.T("worktree_set_root_unavailable", set.Name,
			i18n.T("worktree_set_root_no_mirror_key")))
	}
	root := filepath.Dir(set.Members[0].Path)
	for _, m := range set.Members[1:] {
		if parent := filepath.Dir(m.Path); parent != root {
			return "", fmt.Errorf("%s", i18n.T("worktree_set_root_unavailable", set.Name,
				i18n.T("worktree_set_root_ambiguous", root, parent)))
		}
	}
	return filepath.Clean(root), nil
}

// worktreeMemberAddPlan 算出本次要加的成员目标路径与问题清单（只读、零副作用）。
//
// 布局**完全随集合登记**：mirror 集合走同构落位（含越界/撞基准/跨卷三条拒绝），
// flat 集合按压平口径。已在本集合的仓逐条点名拒绝（判据：算出的目标路径与既有成员相同，
// 或选中的源仓路径本身就是某成员路径）——不静默跳过，因为"以为加了其实没加"最难查。
func worktreeMemberAddPlan(set config.WorktreeSet, allRepos []config.Repository,
	newRepos []repo.RepositoryInfo) (paths []string, issues []string, err error) {
	root, err := worktreeSetRoot(set)
	if err != nil {
		return nil, nil, err
	}
	if set.Layout == layoutMirror {
		paths, issues = mirrorMemberPaths(root, set.MirrorRoot, newRepos)
	} else {
		paths = worktreeMemberPaths(root, newRepos)
	}
	for i, r := range newRepos {
		if paths[i] == "" {
			continue // 已因布局口径被拒，不重复点名
		}
		for _, m := range set.Members {
			if sameDirLoose(paths[i], m.Path) || sameDirLoose(r.Path, m.Path) {
				issues = append(issues, i18n.T("worktree_member_exists",
					repo.DisplayLabel(r.ID, r.Name, ""), set.Name))
				paths[i] = ""
				break
			}
		}
	}
	return paths, issues, nil
}

// sameDirLoose 比较两个目录路径是否指向同一处（大小写折叠，Windows 盘符与路径大小写
// 不总是可靠地归一；这里只用于「是不是同一个成员」的判等，不参与任何执行目录决定）。
func sameDirLoose(a, b string) bool {
	return strings.EqualFold(filepath.Clean(filepath.FromSlash(a)), filepath.Clean(filepath.FromSlash(b)))
}

// findWorktreeMember 定位成员：接受成员条目的 RepoID 或登记条目名（`worktree list` 里显示的那个）。
// 指不到即报错——静默返回"没找到"会让调用方报成功而什么都没做。
func findWorktreeMember(set config.WorktreeSet, allRepos []config.Repository, ref string) (int, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return -1, fmt.Errorf("%s", i18n.T("worktree_member_not_found", ref, set.Name))
	}
	byID := repo.IndexByID(allRepos)
	if id, err := strconv.Atoi(ref); err == nil {
		for i, m := range set.Members {
			if m.RepoID == id {
				return i, nil
			}
		}
	}
	for i, m := range set.Members {
		if entry, ok := byID[m.RepoID]; ok && entry.Name == ref {
			return i, nil
		}
		if filepath.Clean(m.Path) == filepath.Clean(ref) {
			return i, nil
		}
	}
	return -1, fmt.Errorf("%s", i18n.T("worktree_member_not_found", ref, set.Name))
}

// worktreeMemberChildMembers 返回落在 idx 成员目录**内部**的其它成员索引。
// 移除成员前必须查这个：先拆祖先会把整棵后代目录连带删掉，后代随即拆不到、
// 集合登记留在原地（评审 B1 的单成员版）。工具不做静默连带删除——顺序交回给人。
func worktreeMemberChildMembers(set config.WorktreeSet, idx int) []int {
	if idx < 0 || idx >= len(set.Members) {
		return nil
	}
	base := filepath.Clean(set.Members[idx].Path)
	var kids []int
	for j, m := range set.Members {
		if j == idx {
			continue
		}
		rel, err := filepath.Rel(base, filepath.Clean(m.Path))
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		kids = append(kids, j)
	}
	return kids
}

// checkWorktreeMemberRemovable 前置校验：索引越界报错；内部还有其它成员则点名拒绝。
func checkWorktreeMemberRemovable(set config.WorktreeSet, idx int) ([]int, error) {
	if idx < 0 || idx >= len(set.Members) {
		return nil, fmt.Errorf("%s", i18n.T("worktree_member_index_out_of_range", idx, len(set.Members)))
	}
	kids := worktreeMemberChildMembers(set, idx)
	if len(kids) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(kids))
	for _, k := range kids {
		names = append(names, filepath.Base(filepath.Clean(set.Members[k].Path)))
	}
	// 用目录名而非 `[ID]`：这里只有集合成员路径可用，成员条目的名字要另外查登记表，
	// 而目录名在本集合内是唯一且用户在 `worktree list` 里能直接看到的那个。
	label := filepath.Base(filepath.Clean(set.Members[idx].Path))
	return kids, fmt.Errorf("%s", i18n.T("worktree_member_has_nested", label, strings.Join(names, ", ")))
}

// removeWorktreeMember 从集合里移出 idx 成员，返回新集合与「是否已空」。
// 已空由调用方整组删除（与 prune / config remove 的既有口径一致，不留空集合）；
// 集合名/分支/布局/基准/根一律不动——增删成员不改口径。
func removeWorktreeMember(set config.WorktreeSet, idx int) (config.WorktreeSet, bool) {
	members := make([]config.WorktreeMember, 0, len(set.Members))
	for i, m := range set.Members {
		if i == idx {
			continue
		}
		members = append(members, m)
	}
	set.Members = members
	return set, len(members) == 0
}

// replaceWorktreeSet 用同名新集合替换登记（顺序不变，避免配置 diff 噪音）。
func replaceWorktreeSet(sets []config.WorktreeSet, set config.WorktreeSet) []config.WorktreeSet {
	for i := range sets {
		if sets[i].Name == set.Name {
			sets[i] = set
			return sets
		}
	}
	return append(sets, set)
}

// ---------------------------------------------------------------- 子命令实现

func runWorktreeMemberAdd(cmd *cobra.Command, args []string) {
	cfg, err := config.Load()
	if err != nil {
		exitOnError(err)
	}
	if err := checkWorktreeAddScope(GetRepoIDs(), GetGroup()); err != nil {
		exitOnError(err)
	}
	setName := strings.TrimSpace(args[0])
	set, ok := findWorktreeSet(cfg.WorktreeSets, setName)
	if !ok {
		exitOnError(fmt.Errorf("%s", i18n.T("set_not_found", setName)))
	}

	repos, err := selectRepos(GetRepoIDs(), GetGroup(), cfg)
	if errors.Is(err, errNoRepositories) {
		ui.PrintNoConfigPrompt()
		return
	}
	if err != nil {
		exitOnError(err)
	}
	if len(repos) == 0 {
		exitOnError(errors.New(i18n.T("worktree_no_members")))
	}

	paths, issues, err := worktreeMemberAddPlan(set, cfg.Repositories, repos)
	if err != nil {
		exitOnError(err)
	}
	// 复用整组 add 的逐仓预检：分支存在、同分支已被检出、目标占用、非 git 目录。
	// 分支取集合登记的 branch——集合的定义就是「同名分支」，不接受在这条路径上换分支。
	issues = append(issues, precheckWorktreeAdd(repos, paths, set.Branch)...)
	if len(issues) > 0 {
		for _, line := range issues {
			ui.PrintMessage(line)
		}
		exitOnError(fmt.Errorf("%s", i18n.T("worktree_precheck_failed", len(issues))))
	}

	labels := repo.RepoLabels(repos, cfg.Repositories)
	root, err := worktreeSetRoot(set)
	if err != nil {
		exitOnError(err)
	}
	if !GetYes() {
		if !ui.PrintWorktreeAddReview(set.Name, set.Branch, root, labels, paths) {
			ui.PrintMessage(i18n.T("operation_cancelled"))
			return
		}
	}

	order := worktreeBuildOrder(repos, paths, set.Layout == layoutMirror)
	created, failedLabel := createWorktrees(repos, paths, labels, set.Branch, order)
	if failedLabel != "" {
		rolled := rollbackWorktrees(created)
		ui.PrintMessage(i18n.T("worktree_rolled_back", rolled))
		exitOnError(fmt.Errorf("%s", i18n.T("worktree_add_failed", failedLabel)))
	}

	newMembers, registered, err := buildWorktreeSetMembers(cfg, repos, paths, set.Name)
	if err != nil {
		exitOnError(err)
	}
	set.Members = append(set.Members, newMembers...)
	cfg.WorktreeSets = replaceWorktreeSet(cfg.WorktreeSets, set)
	if err := cfg.Save(); err != nil {
		exitOnError(err)
	}
	ui.PrintSuccess(i18n.T("worktree_member_added", len(newMembers), set.Name))
	if registered > 0 {
		ui.PrintMessage(i18n.T("worktree_repos_registered", registered, set.Name))
	}
}

func runWorktreeMemberRemove(cmd *cobra.Command, args []string) {
	cfg, err := config.Load()
	if err != nil {
		exitOnError(err)
	}
	setName := strings.TrimSpace(args[0])
	set, ok := findWorktreeSet(cfg.WorktreeSets, setName)
	if !ok {
		exitOnError(fmt.Errorf("%s", i18n.T("set_not_found", setName)))
	}

	idx, err := findWorktreeMember(set, cfg.Repositories, args[1])
	if err != nil {
		exitOnError(err)
	}
	// 祖先保护：内部还有其它成员就拒绝（先拆后代，不做静默连带删除）。
	if _, err := checkWorktreeMemberRemovable(set, idx); err != nil {
		exitOnError(err)
	}

	labels, paths := worktreeMemberViews(set, cfg.Repositories)
	memberLabels := []string{labels[idx]}
	memberPaths := []string{paths[idx]}

	// 脏判据与整组 remove 同一口径：单成员脏即中止，`--force` 才透传给 git。
	// 不传嵌套豁免——内部已确认没有其它成员，祖先那个"因后代而显示的未跟踪"在这里不存在。
	if !wtMemberForce {
		if dirty := findDirtyWorktreeMembers(memberLabels, memberPaths, nil); len(dirty) > 0 {
			exitOnError(fmt.Errorf("%s", i18n.T("worktree_dirty_abort", strings.Join(dirty, ", "))))
		}
	}
	if !GetYes() {
		if !ui.PrintWorktreeRemoveReview(set.Name, memberLabels, memberPaths, wtMemberForce) {
			ui.PrintMessage(i18n.T("operation_cancelled"))
			return
		}
	}

	result := git.WorktreeRemove(paths[idx], wtMemberForce)
	result.RepoPath = paths[idx]
	ui.PrintGitResult(result, labels[idx])
	if !result.Success {
		exitOnError(fmt.Errorf("%s", i18n.T("worktree_remove_failed", set.Name, 1)))
	}

	removed := set.Members[idx]
	kept, empty := removeWorktreeMember(set, idx)
	// 条目回收与整组 remove 同一条生命周期口径（ADR-151），只回收路径也匹配的条目。
	droppedRepos := config.DropWorktreeMemberRepos(cfg, []config.WorktreeMember{removed})
	if empty {
		cfg.WorktreeSets = removeWorktreeSetByName(cfg.WorktreeSets, set.Name)
	} else {
		cfg.WorktreeSets = replaceWorktreeSet(cfg.WorktreeSets, kept)
	}
	if err := cfg.Save(); err != nil {
		exitOnError(err)
	}
	ui.PrintSuccess(i18n.T("worktree_member_removed", labels[idx], set.Name))
	if empty {
		ui.PrintMessage(i18n.T("worktree_member_set_dropped", set.Name))
	}
	if droppedRepos > 0 {
		ui.PrintMessage(i18n.T("worktree_entries_dropped", droppedRepos))
	}
}

// ---------------------------------------------------------------- 注册与 i18n

func initWorktreeMemberCmdI18n() {
	worktreeMemberCmd.Short = i18n.T("cmd_worktree_member")
	worktreeMemberAddCmd.Short = i18n.T("cmd_worktree_member_add")
	worktreeMemberRemoveCmd.Short = i18n.T("cmd_worktree_member_remove")
	if f := worktreeMemberRemoveCmd.Flags().Lookup("force"); f != nil {
		f.Usage = i18n.T("flag_worktree_force")
	}
}

func init() {
	worktreeCmd.AddCommand(worktreeMemberCmd)
	worktreeMemberCmd.AddCommand(worktreeMemberAddCmd)
	worktreeMemberCmd.AddCommand(worktreeMemberRemoveCmd)

	worktreeMemberRemoveCmd.Flags().BoolVar(&wtMemberForce, "force", false, i18n.T("flag_worktree_force"))
	initWorktreeMemberCmdI18n()
}
