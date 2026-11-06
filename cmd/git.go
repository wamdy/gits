package cmd

import (
	"errors"
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

func runGitCommandSequential(repos []repo.RepositoryInfo, names []string, args []string) {
	successCount := 0
	failCount = 0

	for i, r := range repos {
		result := git.Execute(r.Path, args...)
		ui.PrintGitResult(result, names[i])
		if result.Success {
			successCount++
		} else {
			failCount++
		}
	}

	ui.PrintSummary(successCount, failCount)
}

func runGitCommandParallel(repos []repo.RepositoryInfo, names []string, args []string) {
	type repoResult struct {
		name   string
		result git.ExecutionResult
	}

	var wg sync.WaitGroup
	resultChan := make(chan repoResult, len(repos))

	for i, r := range repos {
		wg.Add(1)
		go func(name, repoPath string) {
			defer wg.Done()
			resultChan <- repoResult{name: name, result: git.Execute(repoPath, args...)}
		}(names[i], r.Path)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	successCount := 0
	failCount = 0

	for rr := range resultChan {
		ui.PrintGitResult(rr.result, rr.name)
		if rr.result.Success {
			successCount++
		} else {
			failCount++
		}
	}

	ui.PrintSummary(successCount, failCount)
}
