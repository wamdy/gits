package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/git"
	"github.com/wamdy/gits/internal/i18n"
	"github.com/wamdy/gits/internal/repo"
	"github.com/wamdy/gits/internal/ui"
)

func parseRepoIDs(input string, maxID int) []int {
	var result []int
	seen := make(map[int]bool)
	parts := strings.Split(input, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			rangeParts := strings.SplitN(part, "-", 2)
			start, err1 := strconv.Atoi(rangeParts[0])
			end, err2 := strconv.Atoi(rangeParts[1])
			if err1 == nil && err2 == nil && start >= 1 && end <= maxID && start <= end {
				for i := start; i <= end; i++ {
					if !seen[i] {
						seen[i] = true
						result = append(result, i)
					}
				}
			}
		} else {
			id, err := strconv.Atoi(part)
			if err == nil && id >= 1 && id <= maxID && !seen[id] {
				seen[id] = true
				result = append(result, id)
			}
		}
	}
	return result
}

func runGitCommand(args []string) {
	var repos []repo.RepositoryInfo

	groupName := GetGroup()
	repoIDStr := GetRepoIDs()

	// 检查 --group 和 --repo 是否同时使用
	if groupName != "" && repoIDStr != "" {
		ui.PrintError(fmt.Errorf(i18n.T("repo_and_group_conflict")))
		return
	}

	// 检查配置是否有仓库
	cfg, err := config.Load()
	if err != nil {
		ui.PrintError(err)
		return
	}

	if len(cfg.Repositories) == 0 {
		ui.PrintNoConfigPrompt()
		return
	}

	// 构建 ID -> Repository 映射
	repoMap := make(map[int]config.Repository)
	maxID := 0
	for _, r := range cfg.Repositories {
		repoMap[r.ID] = r
		if r.ID > maxID {
			maxID = r.ID
		}
	}

	if repoIDStr != "" {
		// 按仓库 ID 选择
		ids := parseRepoIDs(repoIDStr, maxID)
		for _, id := range ids {
			if r, ok := repoMap[id]; ok {
				repos = append(repos, repo.RepositoryInfo{
					Path: r.Path,
					Name: r.Name,
				})
			}
		}
	} else if groupName != "" {
		configRepos, err := repo.GetRepositoriesByGroup(groupName)
		if err != nil {
			ui.PrintError(err)
			return
		}
		for _, r := range configRepos {
			repos = append(repos, repo.RepositoryInfo{
				Path: r.Path,
				Name: r.Name,
			})
		}
	} else {
		// 使用配置中的全部仓库，不再扫描目录
		registered, err := repo.GetRegisteredRepositories()
		if err != nil {
			ui.PrintError(err)
			return
		}
		for _, r := range registered {
			repos = append(repos, repo.RepositoryInfo{
				Path: r.Path,
				Name: r.Name,
			})
		}
	}

	if len(repos) == 0 {
		ui.PrintMessage(i18n.T("no_repositories"))
		return
	}

	if !GetYes() {
		repoNames := make([]string, len(repos))
		for i, r := range repos {
			repoNames[i] = r.Name
		}

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
