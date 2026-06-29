package repo

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/wamdy/gits/internal/git"
)

type RepositoryInfo struct {
	Path   string
	Name   string
	Branch string
	Status string
}

func Discover(path string) ([]RepositoryInfo, error) {
	var repos []RepositoryInfo

	absRoot, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	err = filepath.Walk(path, func(currentPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() && info.Name() == ".git" {
			repoPath := filepath.Dir(currentPath)
			absRepoPath, err := filepath.Abs(repoPath)
			if err != nil {
				return err
			}

			if absRepoPath == absRoot {
				return filepath.SkipDir
			}

			repoName := filepath.Base(absRepoPath)
			branch := git.GetBranchName(absRepoPath)
			status := git.GetStatus(absRepoPath)

			repos = append(repos, RepositoryInfo{
				Path:   absRepoPath,
				Name:   repoName,
				Branch: branch,
				Status: status,
			})

			return filepath.SkipDir
		}

		return nil
	})

	if err != nil {
		return nil, err
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
