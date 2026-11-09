package cmd

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/git"
	"github.com/wamdy/gits/internal/i18n"
	"github.com/wamdy/gits/internal/repo"
	"github.com/wamdy/gits/internal/ui"
)

// errNoRepositories 表示配置中尚无任何已注册仓库。
var errNoRepositories = errors.New("no repositories registered")

// 退出码语义（与 ReadMe「错误处理」节一致）：
// 0 = 全部成功（或用户主动取消）；
// 1 = 执行阶段至少一个仓库 git 命令失败（failCount > 0）；
// 2 = 选择/配置阶段失败（配置读取失败、-r/-g 解析出错、集合/分组不存在、0 匹配）。
const (
	exitOK              = 0
	exitExecutionFailed = 1
	exitSelectionFailed = 2
)

// exitCode 记录本进程最终退出码，由 Execute() 在命令执行完成后统一 os.Exit。
var exitCode = exitOK

// repoDisplayNames 生成仓库回显用的名称列表（review 确认提示与逐仓结果行共用）。
// 每项为 `[ID] 名称 (当前分支/短 commit id) ← 主仓`（复用 repo.RepoLabels，
// 口径与 config show / group show / scan 一致）；空仓库（展示值为空串）只省略分支后缀，
// 非 worktree 省略 ` ← 主仓` 归属标记。
func repoDisplayNames(repos []repo.RepositoryInfo, allRepos []config.Repository) []string {
	return repo.RepoLabels(repos, allRepos)
}

// toRepositoryInfos 把选中的已登记仓库转为执行/回显用信息，并运行时补出 worktree 归属
// （linked worktree 带主仓路径；普通仓库为空串——身份不落配置）。
func toRepositoryInfos(repos []config.Repository) []repo.RepositoryInfo {
	infos := make([]repo.RepositoryInfo, 0, len(repos))
	for _, r := range repos {
		infos = append(infos, repo.RepositoryInfo{
			ID:         r.ID,
			Path:       r.Path,
			Name:       r.Name,
			WorktreeOf: repo.DetectWorktreeOf(r.Path),
		})
	}
	return infos
}

// selectRepos 根据 -r / -g / 默认 三种模式选择目标仓库。
// 全部基于注入的 cfg 解析（可在测试中构造 config 注入），行为与磁盘查询一致。
func selectRepos(repoIDStr, groupName string, cfg *config.Config) ([]repo.RepositoryInfo, error) {
	// --group 和 --repo 不能同时使用
	if groupName != "" && repoIDStr != "" {
		return nil, errors.New(i18n.T("repo_and_group_conflict"))
	}

	// 检查配置是否有仓库
	if len(cfg.Repositories) == 0 {
		return nil, errNoRepositories
	}

	var repos []repo.RepositoryInfo
	switch {
	case repoIDStr != "":
		// 按 ID / ID 区间 / 名称通配 / 名称精确 / branch: / set: 选择仓库（内部按 ID 去重）
		selected, err := repo.ResolveSelectionWithSets(repoIDStr, cfg.Repositories, cfg.WorktreeSets, repo.DefaultBranchChecker)
		if err != nil {
			return nil, err
		}
		repos = toRepositoryInfos(selected)
	case groupName != "":
		configRepos, err := repo.GetRepositoriesByGroup(groupName)
		if err != nil {
			return nil, err
		}
		repos = toRepositoryInfos(configRepos)
	default:
		// 使用配置中的全部仓库
		repos = toRepositoryInfos(cfg.Repositories)
	}
	return repos, nil
}

func runGitCommand(args []string) {
	cfg, err := config.Load()
	if err != nil {
		ui.PrintError(err)
		exitCode = exitSelectionFailed
		return
	}
	runGitCommandWithConfig(cfg, args)
}

// runGitCommandWithConfig 执行 git 透传命令，接受注入的配置（便于测试）：
// 选择/配置阶段失败 → exitCode=2；执行阶段有仓库失败 → exitCode=1；
// 全部成功或用户取消 → exitCode=0。
func runGitCommandWithConfig(cfg *config.Config, args []string) {
	groupName := GetGroup()
	repoIDStr := GetRepoIDs()

	repos, err := selectRepos(repoIDStr, groupName, cfg)
	if errors.Is(err, errNoRepositories) {
		ui.PrintNoConfigPrompt()
		exitCode = exitSelectionFailed
		return
	}
	if err != nil {
		ui.PrintError(err)
		exitCode = exitSelectionFailed
		return
	}

	if len(repos) == 0 {
		// 含通配符 → 通配符无匹配文案；纯 ID / 名称精确 → 通用"未找到仓库"文案
		if repoIDStr != "" && strings.ContainsAny(repoIDStr, "*?") {
			ui.PrintMessage(i18n.T("wildcard_no_match"))
		} else {
			ui.PrintMessage(i18n.T("no_repositories"))
		}
		exitCode = exitSelectionFailed
		return
	}

	// 回显用名称（含 ID、当前分支与 worktree 归属）一次算好，review 提示与逐仓结果行共用
	names := repoDisplayNames(repos, cfg.Repositories)

	if !GetYes() {
		if !ui.PrintReviewPrompt(names, args[0], args[1:]) {
			ui.PrintMessage(i18n.T("operation_cancelled"))
			return
		}
	}

	if GetParallel() {
		runGitCommandParallel(repos, names, args)
	} else {
		runGitCommandSequential(repos, names, args)
	}

	// 执行阶段任一仓库失败 → 退出码 1（选择/配置失败已在此前以 2 退出）
	if failCount > 0 {
		exitCode = exitExecutionFailed
	}
}

// failCount 记录最近一次执行阶段的失败仓库数（由串行/并行执行函数累加）。
var failCount = 0

// gitResultItem 单仓执行明细（结尾清单用，#1276）。index 记该仓在选择集里的原始位置：
// 并行执行按完成顺序消费 channel，清单必须按 index 归位才能保证"同命令多次运行顺序一致"（Q9）。
type gitResultItem struct {
	index   int
	label   string
	path    string
	success bool
	errText string
}

// maxFailureSummaryRunes 限制单条失败原因的长度（按 rune 计，避免截断半个汉字）：
// 一条 git 报错动辄几十行，整段塞进结尾清单会把清单本身冲掉。
const maxFailureSummaryRunes = 160

// newGitResultItem 由执行结果构造清单条目；失败项即时算好单行原因摘要。
func newGitResultItem(index int, label, path string, result git.ExecutionResult) gitResultItem {
	item := gitResultItem{index: index, label: label, path: path, success: result.Success}
	if !result.Success {
		item.errText = summarizeGitFailure(result)
	}
	return item
}

// summarizeGitFailure 生成单行失败原因摘要：优先取 git 自身输出的首个非空行
// （Execute 用 CombinedOutput，git 的错误事实在 stderr 首行，如 `error: pathspec ... did not match`），
// 其次取执行错误（`exit status 128` 这类信息量低，故排在输出之后），都没有则回退通用文案。
func summarizeGitFailure(result git.ExecutionResult) string {
	reason := firstNonEmptyLine(result.Output)
	if reason == "" && result.Error != nil {
		reason = result.Error.Error()
	}
	if reason == "" {
		return i18n.T("git_cmd_failed")
	}
	return truncateRunes(collapseSpaces(reason), maxFailureSummaryRunes)
}

// firstNonEmptyLine 取首行非空白内容（已去首尾空白）。
func firstNonEmptyLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// collapseSpaces 把行内连续空白压成单空格：git 对多字节字符做对齐填充，
// 原样带进清单会出现大片空白（宽度按字节算时尤甚）。
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateRunes 按 rune 截断，超长补省略号（不产生非法 UTF-8 片段）。
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

// itemsInSelectionOrder 返回按选择索引升序的副本（不改动入参）。
func itemsInSelectionOrder(items []gitResultItem) []gitResultItem {
	ordered := make([]gitResultItem, len(items))
	copy(ordered, items)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].index < ordered[j].index })
	return ordered
}

// countGitResults 统计成功/失败仓库数（纯函数，便于单测；清单本身不改计数口径）。
func countGitResults(items []gitResultItem) (successCount, failTotal int) {
	for _, it := range items {
		if it.success {
			successCount++
			continue
		}
		failTotal++
	}
	return successCount, failTotal
}

// printGitCommandSummary 输出计数行 + 结尾两段清单，并同步包级 failCount（退出码依据）。
func printGitCommandSummary(items []gitResultItem) {
	successCount, failTotal := countGitResults(items)
	failCount = failTotal
	ui.PrintSummary(successCount, failTotal)

	ordered := itemsInSelectionOrder(items)
	list := make([]ui.ResultItem, 0, len(ordered))
	for _, it := range ordered {
		list = append(list, ui.ResultItem{Label: it.label, Path: it.path, Success: it.success, ErrText: it.errText})
	}
	ui.PrintResultList(list)
}

func runGitCommandSequential(repos []repo.RepositoryInfo, names []string, args []string) {
	items := make([]gitResultItem, 0, len(repos))

	for i, r := range repos {
		result := git.Execute(r.Path, args...)
		ui.PrintGitResult(result, names[i])
		items = append(items, newGitResultItem(i, names[i], r.Path, result))
	}

	printGitCommandSummary(items)
}

func runGitCommandParallel(repos []repo.RepositoryInfo, names []string, args []string) {
	type repoResult struct {
		index  int
		name   string
		path   string
		result git.ExecutionResult
	}

	var wg sync.WaitGroup
	resultChan := make(chan repoResult, len(repos))

	for i, r := range repos {
		wg.Add(1)
		go func(index int, name, repoPath string) {
			defer wg.Done()
			resultChan <- repoResult{index: index, name: name, path: repoPath, result: git.Execute(repoPath, args...)}
		}(i, names[i], r.Path)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 逐仓行仍按完成顺序即时打印（保留并行的实时反馈），结尾清单按 index 归位（Q9）
	items := make([]gitResultItem, 0, len(repos))
	for rr := range resultChan {
		ui.PrintGitResult(rr.result, rr.name)
		items = append(items, newGitResultItem(rr.index, rr.name, rr.path, rr.result))
	}

	printGitCommandSummary(items)
}
