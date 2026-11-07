package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/git"
	"github.com/wamdy/gits/internal/i18n"
	"github.com/wamdy/gits/internal/repo"
	"github.com/wamdy/gits/internal/ui"
)

// worktreeCmd 是多仓 worktree 集合的命令族入口。
// 注册为子命令后，`gits worktree <sub>` 不再走 git 透传；裸透传形态改为
// `gits -r <选择> -- worktree <sub>`（TQ-04，root 的 Long 与本命令 Long 均显式写明）。
var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Manage multi-repo worktree sets",
	Long:  "Manage multi-repo worktree sets.",
	Args:  cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		// 打错子命令不得「打 help + 退出码 0」（评审 Q-05）：worktree 曾是 git 透传入口，
		// 静默成功会让人以为跑的是 git。裸 `gits worktree` 仍按 cobra 惯例打 help。
		if len(args) > 0 {
			exitOnError(errors.New(i18n.T("worktree_unknown_subcommand", strings.Join(args, " "))))
		}
		cmd.Help()
	},
}

var (
	wtBranch string
	wtDir    string
	wtForce  bool
)

var worktreeAddCmd = &cobra.Command{
	Use:   "add [set-name] -r <selection> [--branch <branch>] [--dir <root>]",
	Short: "Create one worktree per selected repository and register a set",
	Args:  cobra.MaximumNArgs(1),
	Run:   runWorktreeAdd,
}

var worktreeListCmd = &cobra.Command{
	Use:   "list [set-name]",
	Short: "List worktree sets and members",
	Args:  cobra.MaximumNArgs(1),
	Run:   runWorktreeList,
}

var worktreeRemoveCmd = &cobra.Command{
	Use:   "remove <set-name> [--force]",
	Short: "Remove every member of a worktree set",
	Args:  cobra.ExactArgs(1),
	Run:   runWorktreeRemove,
}

var worktreePruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Drop registered worktree members that no longer exist",
	Args:  cobra.NoArgs,
	Run:   runWorktreePrune,
}

// exitOnError 统一命令层失败出口：走 ui 的错误口径并以非零码退出（AC-02.2 要求预检失败非零退出）。
//
// err 为 nil 时**什么都不做**：本函数原先无条件打印 + 退出，于是 `exitOnError(someErr())`
// 这种「把返回值直接喂进来」的写法会把成功路径也判成失败（实机验证抓到 pack 的 dry-run
// 正是这样每次都报「错误: <nil>」并退 1）。加 nil 守卫后这类写法不再致命。
func exitOnError(err error) {
	if err == nil {
		return
	}
	ui.PrintError(err)
	os.Exit(1)
}

// ---------------------------------------------------------------- 参数推导

// worktreeBranchOf 分支来源优先级：--branch > 配置 defaults.branch；两者皆缺返回空串，
// 由调用方报错——绝不猜各仓 HEAD（AC-02.3）。
func worktreeBranchOf(flagValue, defaultBranch string) string {
	if strings.TrimSpace(flagValue) != "" {
		return strings.TrimSpace(flagValue)
	}
	return strings.TrimSpace(defaultBranch)
}

// worktreeSetSlug 集合名缺省规则：分支名去掉 `harness/` 前缀后的 slug
// （剩余 `/` 换 `-`，可安全作为目录名）；结果为空返回空串（AC-02.5）。
func worktreeSetSlug(branch string) string {
	slug := strings.TrimPrefix(strings.TrimSpace(branch), "harness/")
	slug = strings.ReplaceAll(slug, "/", "-")
	return strings.TrimSpace(slug)
}

// checkWorktreeSetName 同名集合已存在即拒绝，不做覆盖（AC-02.5）；
// 集合名要作为默认根目录的单层目录名拼接，故必须是不含路径成分的普通段名。
func checkWorktreeSetName(name string, sets []config.WorktreeSet) error {
	if strings.TrimSpace(name) == "" {
		return errors.New(i18n.T("worktree_set_name_required"))
	}
	if name != filepath.Base(name) || name == "." || name == ".." {
		return fmt.Errorf("%s", i18n.T("worktree_set_name_invalid", name))
	}
	for _, s := range sets {
		if s.Name == name {
			return fmt.Errorf("%s", i18n.T("worktree_set_exists", name))
		}
	}
	return nil
}

// checkWorktreeAddScope 强制 `worktree add` 显式给出仓库范围（评审 Q-02）：
// -r 与 -g 都为空时选择集会退化成「全部登记仓库」，对建仓这种写盘+建目录动作太危险。
// 纯函数，入参为两个旗标原值。
func checkWorktreeAddScope(repoIDs, group string) error {
	if strings.TrimSpace(repoIDs) == "" && strings.TrimSpace(group) == "" {
		return errors.New(i18n.T("worktree_add_requires_selection"))
	}
	return nil
}

// safeMemberDirName 把仓库名收敛成可安全拼接的单层目录名：取 Base 去掉任何路径成分，
// 空值 / `.` / `..` 一律退回 `repo-<ID>`，保证拼接进集合根目录后不会跳出根。
func safeMemberDirName(name string, id int) string {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "" || base == "." || base == ".." {
		return fmt.Sprintf("repo-%d", id)
	}
	return base
}

// worktreeMemberPaths 计算各成员的目标目录：默认根目录成员名取仓库 Name，
// 重名冲突时追加 `-<ID>` 兜底，避免两个同名仓互相覆盖。
func worktreeMemberPaths(rootDir string, repos []repo.RepositoryInfo) []string {
	used := map[string]bool{}
	paths := make([]string, len(repos))
	for i, r := range repos {
		dirName := safeMemberDirName(r.Name, r.ID)
		if used[dirName] {
			dirName = fmt.Sprintf("%s-%d", dirName, r.ID)
		}
		used[dirName] = true
		paths[i] = filepath.Join(rootDir, dirName)
	}
	return paths
}

// ---------------------------------------------------------------- 阶段 A 预检

// precheckWorktreeAdd 只读预检，零副作用：绝不创建目录、绝不调用 git worktree add。
// 逐项检查 submodule 排除（Q5）、非 git 目录、分支存在性（AC-02.3）与「分支已被本仓 worktree
// 检出」（评审 Q-03）、目标路径占用、成员重复；
// 返回逐仓点名清单（含缺分支仓的名字与其当前分支），非空即调用方整组不建（AC-02.2）。
func precheckWorktreeAdd(repos []repo.RepositoryInfo, paths []string, branch string) []string {
	var issues []string
	seen := map[int]bool{}
	for i, r := range repos {
		label := repo.DisplayLabel(r.ID, r.Name, "")
		if seen[r.ID] {
			issues = append(issues, i18n.T("worktree_duplicate_member", label))
			continue
		}
		seen[r.ID] = true

		switch git.Classify(r.Path) {
		case git.KindNotGit:
			issues = append(issues, i18n.T("worktree_not_git", label))
			continue
		case git.KindSubmodule:
			issues = append(issues, i18n.T("worktree_submodule_unsupported", label))
			continue
		}

		exists, err := git.BranchExists(r.Path, branch)
		if err != nil {
			issues = append(issues, i18n.T("worktree_branch_check_failed", label, err.Error()))
			continue
		}
		if !exists {
			current := repo.DisplayBranch(git.GetBranchName(r.Path), r.Path)
			issues = append(issues, i18n.T("worktree_branch_missing", label, branch, current))
		} else if at, busy := git.WorktreeCheckedOutBranches(r.Path)[branch]; busy {
			// 同一分支已被本仓某个 worktree 检出，git 会拒绝再建一个（评审 Q-03）：
			// 最常见的触发形态是 --branch 缺省落到 defaults.branch=main，而 main 正检出在主仓。
			issues = append(issues, i18n.T("worktree_branch_checked_out", label, branch, at))
		}

		if _, err := os.Stat(paths[i]); err == nil {
			issues = append(issues, i18n.T("worktree_path_occupied", label, paths[i]))
		}
	}
	return issues
}

// ---------------------------------------------------------------- 阶段 B 创建

// createWorktrees 逐个执行 `git worktree add <path> <branch>`。
// 返回本次调用已成功创建的路径（内存记账，回滚只认这份清单）与失败成员的标签；
// 失败详情（含 git 输出）由结果行打印，本函数不重复输出。
func createWorktrees(repos []repo.RepositoryInfo, paths, labels []string, branch string) (created []string, failedLabel string) {
	for i, r := range repos {
		result := git.WorktreeAdd(r.Path, paths[i], branch)
		// 结果行回显 worktree 目标路径：git 的执行目录是主仓，展示主仓路径对建组没有信息量。
		result.RepoPath = paths[i]
		ui.PrintGitResult(result, labels[i])
		if !result.Success {
			return created, labels[i]
		}
		created = append(created, paths[i])
	}
	return created, ""
}

// rollbackWorktrees 只回滚本次调用新建的 worktree（AC-02.4）：
// 绝不遍历磁盘、绝不动调用前既有集合。先常规拆除，被 git 拒绝时再带 --force 重试
// （目标目录由本次调用刚创建，留下即半个集合）；两种形态都不删分支。
func rollbackWorktrees(created []string) int {
	rolled := 0
	for i := len(created) - 1; i >= 0; i-- {
		result := git.WorktreeRemove(created[i], false)
		if !result.Success {
			result = git.WorktreeRemove(created[i], true)
		}
		if result.Success {
			rolled++
			continue
		}
		ui.PrintMessage(i18n.T("worktree_rollback_failed", created[i]))
	}
	return rolled
}

// ---------------------------------------------------------------- 集合工具

func findWorktreeSet(sets []config.WorktreeSet, name string) (config.WorktreeSet, bool) {
	for _, s := range sets {
		if s.Name == name {
			return s, true
		}
	}
	return config.WorktreeSet{}, false
}

func removeWorktreeSetByName(sets []config.WorktreeSet, name string) []config.WorktreeSet {
	kept := make([]config.WorktreeSet, 0, len(sets))
	for _, s := range sets {
		if s.Name != name {
			kept = append(kept, s)
		}
	}
	return kept
}

// worktreeMemberViews 生成成员的展示标签与路径（review 提示、脏检查点名、结果行共用）。
// 标签为 `[ID] 名称`（路径单独成列，不在此拼接）。
func worktreeMemberViews(set config.WorktreeSet, allRepos []config.Repository) ([]string, []string) {
	byID := repo.IndexByID(allRepos)
	labels := make([]string, len(set.Members))
	paths := make([]string, len(set.Members))
	for i, m := range set.Members {
		labels[i] = repo.DisplayLabel(m.RepoID, repo.MemberLabel(m, byID), "")
		paths[i] = m.Path
	}
	return labels, paths
}

// findDirtyWorktreeMembers 逐成员看 `git status --porcelain`，返回脏成员的标签清单。
// 路径已不存在的成员不算脏（git 自身会拒绝拆除，由 remove 的结果行点名）。
func findDirtyWorktreeMembers(labels, paths []string) []string {
	var dirty []string
	for i, p := range paths {
		if git.GetStatus(p) != "" {
			dirty = append(dirty, labels[i])
		}
	}
	return dirty
}

// pruneWorktreeSets 只清登记（AC-03.5）：路径不存在的成员移出集合，成员清空的集合整组删除。
// 不调用 git worktree prune、不删除任何磁盘目录——exists 由调用方注入，本函数只做纯计算。
func pruneWorktreeSets(sets []config.WorktreeSet, allRepos []config.Repository,
	exists func(string) bool) (removed []string, droppedSets []string, kept []config.WorktreeSet, droppedMembers []config.WorktreeMember) {
	byID := repo.IndexByID(allRepos)
	kept = make([]config.WorktreeSet, 0, len(sets))
	for _, s := range sets {
		members := make([]config.WorktreeMember, 0, len(s.Members))
		for _, m := range s.Members {
			if exists(m.Path) {
				members = append(members, m)
				continue
			}
			name := filepath.Base(m.Path)
			if r, ok := byID[m.RepoID]; ok && r.Name != "" {
				name = r.Name
			}
			removed = append(removed, fmt.Sprintf("%s/%s", s.Name, name))
			// 目录已不存在的成员，其自动登记的仓库条目一并回收（评审 BLOCK-2）。
			droppedMembers = append(droppedMembers, m)
		}
		if len(members) == 0 {
			if len(s.Members) > 0 {
				droppedSets = append(droppedSets, s.Name)
			}
			continue
		}
		s.Members = members
		kept = append(kept, s)
	}
	return removed, droppedSets, kept, droppedMembers
}

// dirExists 判断目录存在（prune 的默认判据）。
func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// ---------------------------------------------------------------- 子命令实现

func runWorktreeAdd(cmd *cobra.Command, args []string) {
	cfg, err := config.Load()
	if err != nil {
		exitOnError(err)
	}

	// Q-02：创建集合是写盘+建目录的动作，缺省选择集是「全部登记仓库」，一次手滑就给每个仓
	// （含既有 worktree 条目）建目录。这里强制显式 -r/-g，与票面命令签名一致。
	if err := checkWorktreeAddScope(GetRepoIDs(), GetGroup()); err != nil {
		exitOnError(err)
	}

	repos, err := selectRepos(GetRepoIDs(), GetGroup(), cfg)
	if errors.Is(err, errNoRepositories) {
		ui.PrintNoConfigPrompt()
		os.Exit(1)
	}
	if err != nil {
		exitOnError(err)
	}
	if len(repos) == 0 {
		exitOnError(errors.New(i18n.T("worktree_no_members")))
	}

	branch := worktreeBranchOf(wtBranch, cfg.Defaults.Branch)
	if branch == "" {
		exitOnError(errors.New(i18n.T("worktree_branch_required")))
	}

	setName := ""
	if len(args) > 0 {
		setName = strings.TrimSpace(args[0])
	}
	if setName == "" {
		setName = worktreeSetSlug(branch)
	}
	if err := checkWorktreeSetName(setName, cfg.WorktreeSets); err != nil {
		exitOnError(err)
	}

	rootDir := wtDir
	if strings.TrimSpace(rootDir) == "" {
		rootDir = filepath.Join(config.ConfigBaseDir(), ".worktrees", setName)
	}
	if abs, err := filepath.Abs(rootDir); err == nil {
		rootDir = abs
	}

	// 阶段 A：预检（只读、零副作用）——任一失败即一个 worktree 都不建（AC-02.2）。
	paths := worktreeMemberPaths(rootDir, repos)
	issues := precheckWorktreeAdd(repos, paths, branch)
	if len(issues) > 0 {
		for _, line := range issues {
			ui.PrintMessage(line)
		}
		exitOnError(fmt.Errorf("%s", i18n.T("worktree_precheck_failed", len(issues))))
	}

	labels := repo.RepoLabels(repos, cfg.Repositories)

	// AC-02.6：默认执行前 review 确认（列成员与目标路径），-y 跳过。
	if !GetYes() {
		if !ui.PrintWorktreeAddReview(setName, branch, rootDir, labels, paths) {
			ui.PrintMessage(i18n.T("operation_cancelled"))
			return
		}
	}

	// 阶段 B：逐个创建；中途失败只回滚本次调用新建的（AC-02.4）。
	created, failedLabel := createWorktrees(repos, paths, labels, branch)
	if failedLabel != "" {
		// 失败详情（含 git 输出）已由 createWorktrees 里的结果行打印，这里只补回滚与结论。
		rolled := rollbackWorktrees(created)
		ui.PrintMessage(i18n.T("worktree_rolled_back", rolled))
		exitOnError(fmt.Errorf("%s", i18n.T("worktree_add_failed", failedLabel)))
	}

	// 阶段 C：登记集合。**成员必须指向 worktree 目录自己的仓库条目**，不能指向父仓条目：
	// 若指向父仓，`-r set:<集合名>` 会经 RepoID 反查出父仓 Repository，批量命令就跑在父仓主工作区
	// （分支也不是集合的那个分支），整组执行能力名存实亡——票据 #1064 复核实录：三门全绿、
	// add 报「已创建并登记」，而 `-r set:demo -- rev-parse --show-toplevel` 返回的是主仓路径。
	// 因此先把每个新建 worktree 目录 Ensure 成仓库条目（名称 `<父仓名>@<集合名>`，同路径已存在则复用），
	// 再用该条目 ID 记成员；条目与集合在同一次 Save() 落盘，不留「条目已存、集合未写」的中间态。
	members, registered, err := buildWorktreeSetMembers(cfg, repos, paths, setName)
	if err != nil {
		exitOnError(err)
	}
	set := config.WorktreeSet{Name: setName, Branch: branch, Members: members}
	cfg.WorktreeSets = append(cfg.WorktreeSets, set)
	if err := cfg.Save(); err != nil {
		exitOnError(err)
	}
	ui.PrintSuccess(i18n.T("worktree_set_created", set.Name, len(set.Members)))
	if registered > 0 {
		ui.PrintMessage(i18n.T("worktree_repos_registered", registered, set.Name))
	}
}

// buildWorktreeSetMembers 为本次新建的每个 worktree 目录确保一个仓库条目，返回指向这些条目的
// 成员列表（顺序与 repos/paths 一致）以及「本次新增条目数」（复用既有条目不计入）。
// repos 与 paths 由阶段 A/B 保证一一对应。
func buildWorktreeSetMembers(cfg *config.Config, repos []repo.RepositoryInfo, paths []string, setName string) ([]config.WorktreeMember, int, error) {
	var members []config.WorktreeMember
	registered := 0
	for i, r := range repos {
		preferred := fmt.Sprintf("%s@%s", r.Name, setName)
		entry, added, err := config.EnsureRepositoryEntry(cfg, paths[i], preferred)
		if err != nil {
			return nil, registered, err
		}
		if added {
			registered++
		}
		members = append(members, config.WorktreeMember{RepoID: entry.ID, Path: paths[i]})
	}
	return members, registered, nil
}

func runWorktreeList(cmd *cobra.Command, args []string) {
	cfg, err := config.Load()
	if err != nil {
		exitOnError(err)
	}
	sets := cfg.WorktreeSets
	if len(args) > 0 {
		set, ok := findWorktreeSet(sets, strings.TrimSpace(args[0]))
		if !ok {
			exitOnError(fmt.Errorf("%s", i18n.T("set_not_found", args[0])))
		}
		sets = []config.WorktreeSet{set}
	}
	ui.PrintWorktreeSetList(sets, cfg.Repositories)
}

func runWorktreeRemove(cmd *cobra.Command, args []string) {
	cfg, err := config.Load()
	if err != nil {
		exitOnError(err)
	}
	name := strings.TrimSpace(args[0])
	set, ok := findWorktreeSet(cfg.WorktreeSets, name)
	if !ok {
		exitOnError(fmt.Errorf("%s", i18n.T("set_not_found", name)))
	}

	labels, paths := worktreeMemberViews(set, cfg.Repositories)

	// AC-03.3：任一成员脏 → 整组中止（一个都不拆）并点名脏成员；只有显式 --force 才透传给 git。
	if !wtForce {
		if dirty := findDirtyWorktreeMembers(labels, paths); len(dirty) > 0 {
			exitOnError(fmt.Errorf("%s", i18n.T("worktree_dirty_abort", strings.Join(dirty, ", "))))
		}
	}

	if !GetYes() {
		if !ui.PrintWorktreeRemoveReview(name, labels, paths, wtForce) {
			ui.PrintMessage(i18n.T("operation_cancelled"))
			return
		}
	}

	// 逐个拆除；永不删分支（AC-03.4：不调 git branch -D、不传任何删分支参数）。
	failed := 0
	for i := range paths {
		result := git.WorktreeRemove(paths[i], wtForce)
		result.RepoPath = paths[i] // 结果行回显被拆除的 worktree 路径（git 在主仓目录执行）
		ui.PrintGitResult(result, labels[i])
		if !result.Success {
			failed++
		}
	}
	if failed > 0 {
		// 只走 exitOnError 一条出口（评审 S-06）：先前额外 PrintMessage 同一句话会打两遍。
		exitOnError(fmt.Errorf("%s", i18n.T("worktree_remove_failed", name, failed)))
	}

	cfg.WorktreeSets = removeWorktreeSetByName(cfg.WorktreeSets, name)
	// 回收 add 阶段 C 自动登记的条目（评审 BLOCK-2）：只删集合不回收条目，
	// 之后每次默认全量批量命令都会为这些死条目报 chdir 失败，只能靠 config validate 兜。
	// 路径比对由 DropWorktreeMemberRepos 负责，成员若指向父仓条目（早期/手工配置）不会被误删。
	droppedRepos := config.DropWorktreeMemberRepos(cfg, set.Members)
	if err := cfg.Save(); err != nil {
		exitOnError(err)
	}
	ui.PrintSuccess(i18n.T("worktree_removed", name, len(paths)))
	if droppedRepos > 0 {
		ui.PrintMessage(i18n.T("worktree_entries_dropped", droppedRepos))
	}
}

func runWorktreePrune(cmd *cobra.Command, args []string) {
	cfg, err := config.Load()
	if err != nil {
		exitOnError(err)
	}
	// AC-03.5：只清登记——不删磁盘目录、不调用 git worktree prune。
	removed, droppedSets, kept, droppedMembers := pruneWorktreeSets(cfg.WorktreeSets, cfg.Repositories, dirExists)
	if len(removed) == 0 && len(droppedSets) == 0 {
		ui.PrintMessage(i18n.T("worktree_prune_none"))
		return
	}
	cfg.WorktreeSets = kept
	// 死登记一并回收（评审 BLOCK-2）：目录都没了还留着条目，默认全量批量命令每次都吐 chdir 失败行。
	droppedRepos := config.DropWorktreeMemberRepos(cfg, droppedMembers)
	if err := cfg.Save(); err != nil {
		exitOnError(err)
	}
	if len(removed) > 0 {
		ui.PrintMessage(i18n.T("worktree_prune_members", len(removed), strings.Join(removed, ", ")))
	}
	if len(droppedSets) > 0 {
		ui.PrintMessage(i18n.T("worktree_prune_sets", len(droppedSets), strings.Join(droppedSets, ", ")))
	}
	if droppedRepos > 0 {
		ui.PrintMessage(i18n.T("worktree_entries_dropped", droppedRepos))
	}
	ui.PrintMessage(i18n.T("worktree_prune_note"))
}

// ---------------------------------------------------------------- 注册与 i18n

func initWorktreeCmdI18n() {
	worktreeCmd.Short = i18n.T("worktree_cmd")
	worktreeCmd.Long = i18n.T("worktree_cmd_long") + "\n\n" + i18n.T("worktree_passthrough_note")
	worktreeAddCmd.Short = i18n.T("cmd_worktree_add")
	worktreeListCmd.Short = i18n.T("cmd_worktree_list")
	worktreeRemoveCmd.Short = i18n.T("cmd_worktree_remove")
	worktreePruneCmd.Short = i18n.T("cmd_worktree_prune")

	if f := worktreeAddCmd.Flags().Lookup("branch"); f != nil {
		f.Usage = i18n.T("flag_worktree_branch")
	}
	if f := worktreeAddCmd.Flags().Lookup("dir"); f != nil {
		f.Usage = i18n.T("flag_worktree_dir")
	}
	if f := worktreeRemoveCmd.Flags().Lookup("force"); f != nil {
		f.Usage = i18n.T("flag_worktree_force")
	}
}

func init() {
	worktreeCmd.AddCommand(worktreeAddCmd)
	worktreeCmd.AddCommand(worktreeListCmd)
	worktreeCmd.AddCommand(worktreeRemoveCmd)
	worktreeCmd.AddCommand(worktreePruneCmd)

	worktreeAddCmd.Flags().StringVar(&wtBranch, "branch", "", i18n.T("flag_worktree_branch"))
	worktreeAddCmd.Flags().StringVar(&wtDir, "dir", "", i18n.T("flag_worktree_dir"))
	worktreeRemoveCmd.Flags().BoolVar(&wtForce, "force", false, i18n.T("flag_worktree_force"))
	initWorktreeCmdI18n()
}
