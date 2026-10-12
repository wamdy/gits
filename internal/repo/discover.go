package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/wamdy/gits/internal/git"
)

type RepositoryInfo struct {
	ID     int // 配置中的仓库 ID；0 表示尚未注册（如 scan 新发现的仓库）
	Path   string
	Name   string
	Branch string
	Status string
}

// skipDirs 为遍历时需要跳过的噪音目录（构建产物/依赖/缓存目录）。
var skipDirs = map[string]bool{
	"node_modules": true,
	"target":       true,
	"dist":         true,
	"build":        true,
	".next":        true,
	".venv":        true,
	"__pycache__":  true,
	"vendor":       true,
	".idea":        true,
}

// isNoiseDir 判断目录名是否为需跳过的噪音目录（精确匹配，大小写敏感）。
func isNoiseDir(name string) bool {
	return skipDirs[name]
}

// Discover 遍历目录发现 Git 仓库：
// 噪音目录直接跳过子树；.git 发现后由有界 worker pool 并发采集 branch/status。
// 扫描根本身若是 Git 仓库，同样纳入发现结果（与扫描其下子仓库一视同仁）。
func Discover(path string) ([]RepositoryInfo, error) {
	found := make(chan string, 64)    // 发现的仓库路径
	results := make(chan RepositoryInfo, 64)
	var wg sync.WaitGroup
	const workers = 8
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range found {
				branch := git.GetBranchName(p)
				status := git.GetStatus(p)
				results <- RepositoryInfo{
					Path:   p,
					Name:   filepath.Base(p),
					Branch: branch,
					Status: status,
				}
			}
		}()
	}
	go func() { // 结果收集中协程：全部 worker 结束后关闭 results
		wg.Wait()
		close(results)
	}()

	walkErr := filepath.Walk(path, func(currentPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if isNoiseDir(info.Name()) {
				return filepath.SkipDir
			}
			if info.Name() == ".git" {
				repoPath := filepath.Dir(currentPath)
				absRepoPath, err := filepath.Abs(repoPath)
				if err != nil {
					return err
				}
				found <- absRepoPath
				return filepath.SkipDir
			}
		}
		return nil
	})
	close(found)

	var repos []RepositoryInfo
	for r := range results {
		repos = append(repos, r)
	}

	if walkErr != nil {
		return nil, walkErr
	}
	return repos, nil
}

func ListRepositories() ([]RepositoryInfo, error) {
	return Discover(".")
}

func GetRepositoryInfo(path string) (RepositoryInfo, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return RepositoryInfo{}, err
	}

	if !git.IsGitRepository(absPath) {
		return RepositoryInfo{}, fmt.Errorf("not a git repository: %s", absPath)
	}

	return RepositoryInfo{
		Path:   absPath,
		Name:   filepath.Base(absPath),
		Branch: git.GetBranchName(absPath),
		Status: git.GetStatus(absPath),
	}, nil
}
