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
	// WorktreeOf 为 linked worktree 的所属主仓路径；普通仓库 / 非 worktree 为空串。
	// 运行时由 git 判定，不落配置（与 Repository.Branch 的 json:"-" 同口径，避免与磁盘漂移）。
	WorktreeOf string
}

// discoveryTarget 遍历阶段发现的候选：path 为仓库目录，gitIsFile 表示其 `.git` 入口是文件
// （linked worktree 或 submodule，需交给 worker 用 git 判定底座细分）。
type discoveryTarget struct {
	path      string
	gitIsFile bool
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
// 噪音目录直接跳过子树；`.git` 目录或 `.git` 文件（linked worktree / submodule）都算命中；
// 命中后由有界 worker pool 并发采集 branch/status，并对 gitfile 形态做三态判定
// （submodule 按 Q5 跳过、不进候选也不报错；linked worktree 记录所属主仓路径）。
// 扫描根本身若是 Git 仓库，同样纳入发现结果（与扫描其下子仓库一视同仁）。
func Discover(path string) ([]RepositoryInfo, error) {
	found := make(chan discoveryTarget, 64) // 发现的候选仓库
	results := make(chan RepositoryInfo, 64)
	var wg sync.WaitGroup
	const workers = 8
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range found {
				info := RepositoryInfo{
					Path: item.path,
					Name: filepath.Base(item.path),
				}
				if item.gitIsFile {
					// `.git` 为文件：linked worktree 与 submodule 只靠 git 自身的
					// git-dir / common-dir 关系区分，不猜路径字符串。
					switch git.Classify(item.path) {
					case git.KindSubmodule:
						continue // Q5：submodule 排除在本票外，静默跳过
					case git.KindLinkedWorktree:
						info.WorktreeOf = git.MainRepoPath(item.path)
					}
				}
				info.Branch = git.GetBranchName(item.path)
				info.Status = git.GetStatus(item.path)
				results <- info
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
		if info.Name() != ".git" {
			if info.IsDir() && isNoiseDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		// `.git` 命中：目录 → 普通仓；文件 → worktree / submodule（由 worker 细分）。
		repoPath := filepath.Dir(currentPath)
		absRepoPath, err := filepath.Abs(repoPath)
		if err != nil {
			return err
		}
		found <- discoveryTarget{path: absRepoPath, gitIsFile: !info.IsDir()}
		// 命中即跳过该仓库目录的剩余条目（含 .git 文件场景下的兄弟子目录），
		// 与修复前对 `.git` 目录 SkipDir 的语义保持一致。
		return filepath.SkipDir
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

	info := RepositoryInfo{
		Path:   absPath,
		Name:   filepath.Base(absPath),
		Branch: git.GetBranchName(absPath),
		Status: git.GetStatus(absPath),
	}
	// linked worktree 带主仓归属（运行时判定，不落配置）。
	if git.Classify(absPath) == git.KindLinkedWorktree {
		info.WorktreeOf = git.MainRepoPath(absPath)
	}
	return info, nil
}
