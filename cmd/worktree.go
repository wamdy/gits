package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	wtBranch     string
	wtDir        string
	wtForce      bool
	wtLayout     string
	wtMirrorRoot string
)

var worktreeAddCmd = &cobra.Command{
	Use:   "add [set-name] -r <selection> [--branch <branch>] [--dir <root>] [--layout flat|mirror] [--mirror-root <base>]",
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

// ---------------------------------------------------------------- 同构布局（#1434）

// 两种落位口径。flat 是缺省且**永远**是缺省：不给 --layout 时行为与 #1064 终态逐字一致。
const (
	layoutFlat   = "flat"
	layoutMirror = "mirror"
)

// normalizeLayout 将命令行取值收敛为内部口径：
// 空串与 flat 都归一成 flat（**不落 layout 键**，保持配置 diff 零噪音），
// mirror 原样保留；其余取值报错——布局口径不允许悄悄变（与 #1064「不猜各仓 HEAD」同一条纪律）。
func normalizeLayout(flagValue string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(flagValue))
	switch v {
	case "", layoutFlat:
		return layoutFlat, nil
	case layoutMirror:
		return layoutMirror, nil
	default:
		return "", errors.New(i18n.T("worktree_layout_unknown", strings.TrimSpace(flagValue)))
	}
}

// resolveWorktreeLayout 收敛 `--layout` 与 `--mirror-root` 的**组合**语义
// （normalizeLayout 只管单旗标取值，这里管两个旗标撞在一起时谁说了算）：
//
//   - 两个都不给 → flat，与 #1064 终态逐字一致；
//   - 只给 --mirror-root → 判为 mirror。给了基准还压平，等于把用户明确表达的意图
//     静默丢掉——旗标被忽略比报错更难查（本项目登记过的「门全绿而功能死」同型缺陷）；
//   - 显式 --layout flat + --mirror-root → 点名矛盾。这两个取值互相否定，
//     flat 下基准没有任何消费点，不存在「定个语义」的余地，只能让人改；
//   - --layout 取值非法 → 先报非法（不因顺带给了基准而绕过校验）。
func resolveWorktreeLayout(layoutFlag, mirrorRootFlag string) (string, error) {
	explicitRoot := strings.TrimSpace(mirrorRootFlag) != ""
	if strings.TrimSpace(layoutFlag) == "" && explicitRoot {
		return layoutMirror, nil
	}
	layout, err := normalizeLayout(layoutFlag)
	if err != nil {
		return "", err
	}
	if layout == layoutFlat && explicitRoot {
		return "", errors.New(i18n.T("worktree_layout_conflict"))
	}
	return layout, nil
}

// defaultMirrorRoot 缺省镜像基准＝配置基目录的**父目录**（不是它自己）。
// 原因在现网就能看到：`.gits` 位于 appollo 根，取它当基准会让 appollo 自己 rel=="."，
// 撞上集合根本身；取父目录后 appollo → `<集合根>/appollo`、
// gits → `<集合根>/appollo/code/tools/gits`，与磁盘 1:1 且不需要任何特例分支。
func defaultMirrorRoot() string {
	return filepath.Dir(config.ConfigBaseDir())
}

// resolveMirrorRoot --mirror-root 覆盖缺省基准；空值走缺省。
func resolveMirrorRoot(flagValue string) string {
	v := strings.TrimSpace(flagValue)
	if v == "" {
		return filepath.Clean(defaultMirrorRoot())
	}
	if abs, err := filepath.Abs(filepath.FromSlash(v)); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(filepath.FromSlash(v))
}

// mirrorMemberPaths 按磁盘布局算成员目录：`<集合根>/Rel(基准, 仓绝对路径)`。
//
// 与 flat 一样是**纯计算、零副作用**；越界不静默降级，而是逐仓点名进 issues
// （全量收集，不首错即返——对齐 precheckWorktreeAdd 的点名纪律）：
//   - 跨卷/无法相对化（filepath.Rel 报错）；
//   - 仓不在基准之下（rel 以 ".." 开头）；
//   - 仓正好等于基准（rel == "."，会撞在集合根本身上）。
//
// 被拒成员的 paths[i] 留空串：调用方靠 issues 判成败，空路径也让它不可能被误用。
func mirrorMemberPaths(setRoot, mirrorRoot string, repos []repo.RepositoryInfo) (paths []string, issues []string) {
	paths = make([]string, len(repos))
	for i, r := range repos {
		label := repo.DisplayLabel(r.ID, r.Name, "")
		rel, err := filepath.Rel(mirrorRoot, filepath.Clean(filepath.FromSlash(r.Path)))
		switch {
		case err != nil:
			issues = append(issues, i18n.T("worktree_mirror_cross_volume", label, mirrorRoot))
			continue
		case rel == ".":
			issues = append(issues, i18n.T("worktree_mirror_root_is_repo", label, mirrorRoot, filepath.Dir(mirrorRoot)))
			continue
		case rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)):
			issues = append(issues, i18n.T("worktree_mirror_out_of_root", label, mirrorRoot))
			continue
		}
		paths[i] = filepath.Join(setRoot, filepath.FromSlash(rel))
	}
	return paths, issues
}

// worktreeBuildOrder 返回执行顺序（repos/paths 的索引切片）。
//
// mirror 布局下**必须先建祖先再建后代**：实测反序时 git 对祖先直接
// `fatal: '<path>' already exists` 退 128，会留下半个集合并触发回滚链。
// 深度用「目标路径的分隔符个数」度量：同一集合的成员共享同一根目录前缀，
// 因此绝对路径的分隔符数与相对深度只差一个常量，排序结果等价，
// 无需把 setRoot 再传进来（多一个参数就多一个传错的机会）。
// 同深度按仓 ID 升序，保证同一命令多次运行的顺序可 diff（沿 #1276 的选择索引归位口径）。
// flat 布局返回恒等切片——重排只服务镜像，不许连带改掉扁平集合的执行与输出顺序。
func worktreeBuildOrder(repos []repo.RepositoryInfo, paths []string, mirror bool) []int {
	order := make([]int, len(paths))
	for i := range order {
		order[i] = i
	}
	if !mirror {
		return order
	}
	depth := func(p string) int {
		return strings.Count(filepath.ToSlash(p), "/")
	}
	sort.SliceStable(order, func(a, b int) bool {
		da, db := depth(paths[order[a]]), depth(paths[order[b]])
		if da != db {
			return da < db
		}
		return repos[order[a]].ID < repos[order[b]].ID
	})
	return order
}

// ---------------------------------------------------------------- 阶段 A 预检

// planWorktreeMembers 是 runWorktreeAdd 唯一的落位入口：按布局口径算成员路径，
// 并把**布局专属**问题（越界、撞基准、跨卷）与阶段 A 预检问题合并成同一份点名清单，
// 走同一「整组不建」出口（AC-02.2 的失败语义对两类问题是同一条）。
//
// 返回的 mirrorRoot 供集合登记；flat 恒返回空串，调用方不得据此猜基准。
// 抽成函数而不是就地 if：镜像的三条拒绝必须与 flat 走同一条出口，
// 复制一份退出逻辑就是复制一份会漂移的口径。
func planWorktreeMembers(repos []repo.RepositoryInfo, rootDir, layout, mirrorRootFlag string) (paths []string, issues []string, mirrorRoot string) {
	if layout != layoutMirror {
		return worktreeMemberPaths(rootDir, repos), nil, ""
	}
	mirrorRoot = resolveMirrorRoot(mirrorRootFlag)
	paths, issues = mirrorMemberPaths(rootDir, mirrorRoot, repos)
	return paths, issues, mirrorRoot
}

// worktreeSetLayoutKeys 决定集合登记里 layout/mirror_root 两键写什么。
// 归一责任在 cmd 层：配置层照单序列化（TestWorktreeSetFlatLayoutOmitted 已钉），
// 这里若把 flat 写成显式 "flat"，每个存量用户下次 add 都会在 config.json 多出两行噪音，
// 而 omitempty 的意义正是「缺省口径不落盘」。
func worktreeSetLayoutKeys(layout, mirrorRoot string) (string, string) {
	if layout != layoutMirror {
		return "", ""
	}
	return layoutMirror, mirrorRoot
}

// precheckWorktreeAdd 只读预检，零副作用：绝不创建目录、绝不调用 git worktree add。
// 逐项检查非 git 目录（含未 init 的 submodule，E8）、分支存在性（AC-02.3）与
// 「分支已被本仓 worktree 检出」（评审 Q-03）、目标路径占用、成员重复；
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

		// #1434 R1-a：submodule 源放行（原 #1064 Q5 的"本期不支持"由本预检 + config scan
		// 静默跳过两条闸共同构成，本票同批撤除）。实测依据：对真 submodule 源执行
		// `git worktree add` 成功且可用（E4），建出的 worktree 落 KindLinkedWorktree（E7），
		// 归属由 MainRepoPath 的 core.worktree 判据解析回源 submodule 工作树（E12/E13）。
		// **未 init** 的 submodule 两次 rev-parse 失败落 KindNotGit（E8），仍被下面一支点名拒绝。
		if kind := git.Classify(r.Path); kind == git.KindNotGit {
			issues = append(issues, i18n.T("worktree_not_git", label))
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

// createWorktrees 按 order 指定的**执行顺序**逐个 `git worktree add`，
// 结果行却按**选择索引**顺序打印——两条顺序故意分开：
//   - 执行序服务 git 的硬约束：镜像布局必须先建祖先，实测反序时 git 对祖先直接
//     `fatal: '<path>' already exists` 退 128（E2），留下半个集合并触发回滚链；
//   - 行序服务人：沿 #1276「结尾清单按选择索引归位」的口径，flat 下 order 恒等，
//     输出与改造前逐字相同。
//
// created 记录的是**执行序**——rollbackWorktrees 按它倒序回滚，最深的成员先回。
// 失败即停；已尝试过的成员（含失败那条）都会打出结果行，未尝试的不打。
// 失败详情（含 git 输出）由结果行承担，本函数不重复输出。
func createWorktrees(repos []repo.RepositoryInfo, paths, labels []string, branch string, order []int) (created []string, failedLabel string) {
	type attempt struct {
		result git.ExecutionResult
		label  string
		done   bool
	}
	attempts := make([]attempt, len(repos))
	for _, idx := range order {
		if idx < 0 || idx >= len(repos) {
			continue // 越界索引不参与（宁可少跑，不可 panic 在半途留下半个集合）
		}
		result := git.WorktreeAdd(repos[idx].Path, paths[idx], branch)
		// 结果行回显 worktree 目标路径：git 的执行目录是主仓，展示主仓路径对建组没有信息量。
		result.RepoPath = paths[idx]
		attempts[idx] = attempt{result: result, label: labels[idx], done: true}
		if !result.Success {
			break
		}
		created = append(created, paths[idx])
	}
	for _, a := range attempts {
		if !a.done {
			continue
		}
		ui.PrintGitResult(a.result, a.label)
		if !a.result.Success {
			failedLabel = a.label
		}
	}
	return created, failedLabel
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

// worktreeRemovalOrder 返回拆除执行顺序（paths 的索引切片）：**深度降序**（后代先），
// 同深度按登记序，所以 flat（成员互为兄弟）恒等——既有 remove 的行序与行为逐字不变。
//
// 为什么不能就用 add 侧的 `worktreeBuildOrder` 倒序：那是**本次调用的执行序**的倒影，
// 而 remove 拿到的是集合登记序，登记序与深度无关（用户当时手输 `-r 2,1` 就反了）。
// 实测的形状是：先拆祖先会把整棵后代目录连带删掉，后代随即 `chdir … cannot find the path`，
// 失败计数 >0 ⇒ 集合登记原地保留、`.git/worktrees` 里挂 prunable 残项（评审 B1）。
func worktreeRemovalOrder(paths []string) []int {
	order := make([]int, len(paths))
	for i := range order {
		order[i] = i
	}
	depth := func(p string) int { return strings.Count(filepath.ToSlash(p), "/") }
	sort.SliceStable(order, func(a, b int) bool {
		return depth(paths[order[a]]) > depth(paths[order[b]])
	})
	return order
}

// worktreeNestedMembers 算出每个成员**内部**还包着哪些同集合成员，返回 git status 口径的
// 相对路径（`/` 分隔、无尾斜杠），索引与 paths 对齐。兄弟目录、集合外的路径都不会进清单。
//
// 这份清单只服务一件事：脏判据的豁免（见 findDirtyWorktreeMembers）。
// flat 集合下恒为空 ⇒ 拆除侧对既有形状零影响。
func worktreeNestedMembers(paths []string) map[int][]string {
	nested := make(map[int][]string, len(paths))
	for i, p := range paths {
		base := filepath.Clean(p)
		for j, q := range paths {
			if i == j {
				continue
			}
			rel, err := filepath.Rel(base, filepath.Clean(q))
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue // 不含（跨卷/在外面/自身）：不豁免任何东西
			}
			nested[i] = append(nested[i], filepath.ToSlash(rel))
		}
	}
	return nested
}

// findDirtyWorktreeMembers 逐成员看未提交改动，返回脏成员的标签清单（调用方据此整组中止）。
// 路径已不存在的成员不算脏（git 自身会拒绝拆除，由 remove 的结果行点名）。
//
// nested（worktreeNestedMembers 的结果）是**镜像集合的结构性豁免**：后代成员目录就长在祖先
// 工作树里，祖先的 status 必然把它记成未跟踪内容——那不是「用户有改动没提交」，
// 而是集合自己的落位形状。不豁免它，嵌套镜像集合在脏判据这一关就被整组中止，根本走不到拆除。
// 豁免口径严格限定为「未跟踪(`??`) 且路径逐条精确等于某后代成员」；
// 其余任何状态（含同目录里的真未跟踪文件、已跟踪文件的改动、暂存内容）照旧判脏。
// 有豁免项的成员才走 `status --porcelain -uall -z`（逐文件枚举，成本更高），
// 没有豁免项的成员继续走默认口径——flat 集合的判据与成本都不变。
func findDirtyWorktreeMembers(labels, paths []string, nested map[int][]string) []string {
	var dirty []string
	for i, p := range paths {
		if len(nested[i]) == 0 {
			if git.GetStatus(p) != "" {
				dirty = append(dirty, labels[i])
			}
			continue
		}
		if statusIgnoringMembers(p, nested[i]) {
			dirty = append(dirty, labels[i])
		}
	}
	return dirty
}

// statusIgnoringMembers 判断该工作树是否有「不算集合结构」的改动。
// 解析 `--porcelain -uall -z`：NUL 分隔，每条形如 `?? tools/inner/` 或 ` M path`。
// 只跳过 `?? ` 且去掉尾斜杠后精确命中豁免清单的条目；解析不动的任何其它条目一律算脏（宁可多判，
// 不可把用户改动当结构拆掉）。
func statusIgnoringMembers(dir string, exempt []string) bool {
	skip := make(map[string]bool, len(exempt))
	for _, rel := range exempt {
		skip[strings.TrimSuffix(filepath.ToSlash(rel), "/")] = true
	}
	out := git.StatusUntrackedAll(dir)
	if out == "" {
		return false // 命令失败时 Output 为空：交给后续 git worktree remove 自己报错，不在这里替它判定
	}
	for _, rec := range strings.Split(out, "\x00") {
		rec = strings.TrimRight(rec, "\r\n")
		if rec == "" {
			continue
		}
		if rel, ok := strings.CutPrefix(rec, "?? "); ok {
			if skip[strings.TrimSuffix(filepath.ToSlash(rel), "/")] {
				continue
			}
		}
		return true
	}
	return false
}

// teardownWorktreeSet 拆除集合全部成员：按 worktreeRemovalOrder 后代先拆，逐个
// `git worktree remove` 并打结果行，返回失败个数。**永不删分支**（AC-03.4），
// `force` 只透传给 git；不碰配置、不落盘——登记清理与 Save 由调用方负责
// （评审 BLOCK-2 的条目回收口径也留在调用方，避免这里出现第二份生命周期判断）。
// 脏判据同样由调用方在 review 之前执行（人类门控必须排在任何写盘动作前面）。
func teardownWorktreeSet(labels, paths []string, force bool) int {
	failed := 0
	for _, idx := range worktreeRemovalOrder(paths) {
		result := git.WorktreeRemove(paths[idx], force)
		result.RepoPath = paths[idx] // 结果行回显被拆除的 worktree 路径（git 在归属源树目录执行）
		ui.PrintGitResult(result, labels[idx])
		if !result.Success {
			failed++
		}
	}
	return failed
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

	// #1434：布局口径在选择解析**之前**收敛。打错 --layout 若放到后面才发现，
	// 用户已经白等一轮仓库扫描；布局与基准两个旗标的组合语义见 resolveWorktreeLayout。
	layout, err := resolveWorktreeLayout(wtLayout, wtMirrorRoot)
	if err != nil {
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
	// 布局问题（越界/撞基准/跨卷）与逐仓问题合并成同一份点名清单、走同一出口：
	// 镜像的拒绝口径是「整组不建 + 逐仓点名」，不是静默降级成 flat。
	paths, issues, mirrorRoot := planWorktreeMembers(repos, rootDir, layout, wtMirrorRoot)
	issues = append(issues, precheckWorktreeAdd(repos, paths, branch)...)
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
	// 执行顺序按布局口径（镜像＝祖先优先，E2），结果行仍按选择索引归位（#1276 口径）。
	order := worktreeBuildOrder(repos, paths, layout == layoutMirror)
	created, failedLabel := createWorktrees(repos, paths, labels, branch, order)
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
	// 布局口径随集合一起登记：`worktree list` / `config show` 要靠它区分同构与压平，
	// 而 flat 两键全空（omitempty 零污染，存量配置文件一行不多）。
	setLayout, setMirrorRoot := worktreeSetLayoutKeys(layout, mirrorRoot)
	set := config.WorktreeSet{Name: setName, Branch: branch, Layout: setLayout,
		MirrorRoot: setMirrorRoot, Members: members}
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
	// 豁免清单（同集合后代目录）在这里传进去：镜像集合的祖先必然把后代记成未跟踪内容，
	// 不豁免就是「结构冒充改动」，整组永远拆不掉（评审 B1）。
	if !wtForce {
		if dirty := findDirtyWorktreeMembers(labels, paths, worktreeNestedMembers(paths)); len(dirty) > 0 {
			exitOnError(fmt.Errorf("%s", i18n.T("worktree_dirty_abort", strings.Join(dirty, ", "))))
		}
	}

	if !GetYes() {
		if !ui.PrintWorktreeRemoveReview(name, labels, paths, wtForce) {
			ui.PrintMessage(i18n.T("operation_cancelled"))
			return
		}
	}

	// 逐个拆除（后代先，见 worktreeRemovalOrder）；永不删分支（AC-03.4：不调 git branch -D、不传任何删分支参数）。
	failed := teardownWorktreeSet(labels, paths, wtForce)
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
	if f := worktreeAddCmd.Flags().Lookup("layout"); f != nil {
		f.Usage = i18n.T("flag_worktree_layout")
	}
	if f := worktreeAddCmd.Flags().Lookup("mirror-root"); f != nil {
		f.Usage = i18n.T("flag_worktree_mirror_root")
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
	worktreeAddCmd.Flags().StringVar(&wtLayout, "layout", "", i18n.T("flag_worktree_layout"))
	worktreeAddCmd.Flags().StringVar(&wtMirrorRoot, "mirror-root", "", i18n.T("flag_worktree_mirror_root"))
	worktreeRemoveCmd.Flags().BoolVar(&wtForce, "force", false, i18n.T("flag_worktree_force"))
	initWorktreeCmdI18n()
}
