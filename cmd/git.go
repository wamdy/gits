package cmd

import (
	"errors"
	"fmt"
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

// repoReviewNames 生成命令执行前确认提示用的仓库名列表。
// 每项追加当前分支/短 commit id（复用 DisplayBranch，口径与 config show / group show / scan 一致）；
// 空仓库（无 commit，展示值为空串）则不追加后缀，仅显示仓库名。
func repoReviewNames(repos []repo.RepositoryInfo) []string {
	names := make([]string, len(repos))
	for i, r := range repos {
		branch := repo.DisplayBranch(git.GetBranchName(r.Path), r.Path)
		if branch == "" {
			names[i] = r.Name
			continue
		}
		names[i] = fmt.Sprintf("%s (%s)", r.Name, branch)
	}
	return names
}

// selectRepos 根据 -r / -g / 默认 三种模式选择目标仓库。
// -r 分支基于注入的 cfg 解析（可在测试中构造 config 注入）；
// -g / 默认 分支沿用既有仓库层查询（内部自行加载配置），行为与之前一致。
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
		// 按 ID / ID 区间 / 名称通配 / 名称精确 / branch: 选择仓库（ResolveSelection 内部去重）
		selected, err := repo.ResolveSelection(repoIDStr, cfg.Repositories, repo.DefaultBranchChecker)
		if err != nil {
			return nil, err
		}
		for _, r := range selected {
			repos = append(repos, repo.RepositoryInfo{
				Path: r.Path,
				Name: r.Name,
			})
		}
	case groupName != "":
		configRepos, err := repo.GetRepositoriesByGroup(groupName)
		if err != nil {
			return nil, err
		}
		for _, r := range configRepos {
			repos = append(repos, repo.RepositoryInfo{
				Path: r.Path,
				Name: r.Name,
			})
		}
	default:
		// 使用配置中的全部仓库
		registered, err := repo.GetRegisteredRepositories()
		if err != nil {
			return nil, err
		}
		for _, r := range registered {
			repos = append(repos, repo.RepositoryInfo{
				Path: r.Path,
				Name: r.Name,
			})
		}
	}
	return repos, nil
}

func runGitCommand(args []string) {
	groupName := GetGroup()
	repoIDStr := GetRepoIDs()

	cfg, err := config.Load()
	if err != nil {
		ui.PrintError(err)
		return
	}

	repos, err := selectRepos(repoIDStr, groupName, cfg)
	if errors.Is(err, errNoRepositories) {
		ui.PrintNoConfigPrompt()
		return
	}
	if err != nil {
		ui.PrintError(err)
		return
	}

	if len(repos) == 0 {
		// 含通配符 → 通配符无匹配文案；纯 ID / 名称精确 → 通用"未找到仓库"文案
		if repoIDStr != "" && strings.ContainsAny(repoIDStr, "*?") {
			ui.PrintMessage(i18n.T("wildcard_no_match"))
		} else {
			ui.PrintMessage(i18n.T("no_repositories"))
		}
		return
	}

	if !GetYes() {
		repoNames := repoReviewNames(repos)

		if !ui.PrintReviewPrompt(repoNames, args[0], args[1:]) {
			ui.PrintMessage(i18n.T("operation_cancelled"))
			return
		}
	}

	if GetParallel() {
		runGitCommandParallel(repos, args)
	} else {
		runGitCommandSequential(repos, args)
	}
}

func runGitCommandSequential(repos []repo.RepositoryInfo, args []string) {
	successCount := 0
	failCount := 0

	for _, r := range repos {
		result := git.Execute(r.Path, args...)
		ui.PrintGitResult(result)
		if result.Success {
			successCount++
		} else {
			failCount++
		}
	}

	ui.PrintSummary(successCount, failCount)
}

func runGitCommandParallel(repos []repo.RepositoryInfo, args []string) {
	var wg sync.WaitGroup
	resultChan := make(chan git.ExecutionResult, len(repos))

	for _, r := range repos {
		wg.Add(1)
		go func(repoPath string) {
			defer wg.Done()
			result := git.Execute(repoPath, args...)
			resultChan <- result
		}(r.Path)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	successCount := 0
	failCount := 0

	for result := range resultChan {
		ui.PrintGitResult(result)
		if result.Success {
			successCount++
		} else {
			failCount++
		}
	}

	ui.PrintSummary(successCount, failCount)
}
