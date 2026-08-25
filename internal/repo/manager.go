package repo

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/git"
	"github.com/wamdy/gits/internal/i18n"
)

func RegisterRepository(path string) error {
	return config.AddRepository(path)
}

func RegisterRepositoryWithName(path string, name string) error {
	return config.AddRepositoryWithName(path, name)
}

func RegisterRepositoryWithOptions(path string, name string, id int) error {
	return config.AddRepositoryWithOptions(path, name, id)
}

func UnregisterRepository(path string) error {
	return config.RemoveRepository(path)
}

func UnregisterRepositoryByID(id int) error {
	return config.RemoveRepositoryByID(id)
}

func UnregisterRepositoryByRef(ref string) error {
	id, path, _ := config.ParseRepoRef(ref)
	if id > 0 {
		return config.RemoveRepositoryByID(id)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if foundID, ok := config.FindRepoByName(cfg, ref); ok {
		return config.RemoveRepositoryByID(foundID)
	}
	return config.RemoveRepository(path)
}

func GetRegisteredRepositories() ([]config.Repository, error) {
	return config.GetAllRepositories()
}

func GetRepositoriesByGroup(groupName string) ([]config.Repository, error) {
	return config.GetRepositoriesByGroup(groupName)
}

func CreateGroup(name string) error {
	return config.AddGroup(name)
}

func DeleteGroup(name string) error {
	return config.RemoveGroup(name)
}

func AssignToGroup(groupName, repoRef string, autoCreate bool) error {
	id, path, _ := config.ParseRepoRef(repoRef)
	if id > 0 {
		return config.AssignToGroupByID(groupName, id, autoCreate)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if foundID, ok := config.FindRepoByName(cfg, repoRef); ok {
		return config.AssignToGroupByID(groupName, foundID, autoCreate)
	}
	return config.AssignToGroup(groupName, path, autoCreate)
}

func UnassignFromGroup(groupName, repoRef string) error {
	id, path, _ := config.ParseRepoRef(repoRef)
	if id > 0 {
		return config.UnassignFromGroupByID(groupName, id)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if foundID, ok := config.FindRepoByName(cfg, repoRef); ok {
		return config.UnassignFromGroupByID(groupName, foundID)
	}
	return config.UnassignFromGroup(groupName, path)
}

func GetAllGroups() (map[string][]int, error) {
	return config.GetAllGroups()
}

func ParseRepoRef(ref string) (int, string) {
	id, path, _ := config.ParseRepoRef(ref)
	return id, path
}

func GroupExists(name string) (bool, error) {
	cfg, err := config.Load()
	if err != nil {
		return false, err
	}
	_, ok := cfg.Groups[name]
	return ok, nil
}

func RenameRepository(repoRef, newName string) error {
	return config.RenameRepository(repoRef, newName)
}

func ReassignRepositoryID(oldID, newID int) error {
	return config.ReassignRepositoryID(oldID, newID)
}

func ReassignAllIDs() error {
	return config.ReassignAllIDs()
}

// FindReposByWildcard 按仓库名称 glob 匹配（path.Match，支持 * 和 ?），返回匹配仓库。
func FindReposByWildcard(pattern string, repos []config.Repository) []config.Repository {
	var result []config.Repository
	for _, r := range repos {
		if ok, _ := path.Match(pattern, r.Name); ok {
			result = append(result, r)
		}
	}
	return result
}

// BranchChecker 检查某仓库路径是否存在匹配 pattern 的分支。
type BranchChecker func(repoPath string, pattern string) (bool, error)

// DefaultBranchChecker 基于 git 层实现的分支存在检查（本地或远程任一）。
func DefaultBranchChecker(repoPath string, pattern string) (bool, error) {
	return git.BranchExists(repoPath, pattern)
}

// FindReposByBranch 遍历 repos，返回所有存在匹配 pattern 分支的仓库。
func FindReposByBranch(pattern string, repos []config.Repository, checker BranchChecker) ([]config.Repository, error) {
	var result []config.Repository
	for _, r := range repos {
		exists, err := checker(r.Path, pattern)
		if err != nil {
			return nil, err
		}
		if exists {
			result = append(result, r)
		}
	}
	return result, nil
}

// ResolveSelection 解析 -r 选择值（逗号分段），支持 ID / ID 区间 / 名称通配 / 名称精确 /
// branch: 段混合，结果按仓库 ID 去重。0 匹配时返回空切片与 nil 错误。
func ResolveSelection(raw string, repos []config.Repository, branchChecker BranchChecker) ([]config.Repository, error) {
	seen := map[int]bool{}
	var result []config.Repository
	for _, seg := range strings.Split(raw, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		switch {
		case looksLikeIDRange(seg): // 纯 ID / ID 区间（含非法区间，如 1-5-8）
			ids, err := parseIDRange(seg)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				if r, ok := findByID(repos, id); ok && !seen[r.ID] {
					seen[r.ID] = true
					result = append(result, r)
				}
			}
		case strings.HasPrefix(seg, "branch:"): // 按分支存在过滤
			pattern := strings.TrimPrefix(seg, "branch:")
			if pattern == "" {
				return nil, errors.New(i18n.T("branch_filter_invalid_empty"))
			}
			if branchChecker == nil {
				return nil, errors.New(i18n.T("branch_filter_no_checker"))
			}
			matched, err := FindReposByBranch(pattern, repos, branchChecker)
			if err != nil {
				return nil, err
			}
			for _, r := range matched {
				if !seen[r.ID] {
					seen[r.ID] = true
					result = append(result, r)
				}
			}
		case strings.ContainsAny(seg, "*?"): // 名称通配
			for _, r := range FindReposByWildcard(seg, repos) {
				if !seen[r.ID] {
					seen[r.ID] = true
					result = append(result, r)
				}
			}
		default: // 普通名称精确匹配
			if r, ok := findByName(repos, seg); ok && !seen[r.ID] {
				seen[r.ID] = true
				result = append(result, r)
			}
		}
	}
	return result, nil
}

// looksLikeIDRange 判断段是否意图按 ID/区间选择：
// 纯数字（"1"）、数字-数字（"1-5"）、数字-非数字-…（"1-5-8" 非法区间）均为 true；
// 非数字开头（如 "repo-backend"）为 false（归入名称匹配）。
func looksLikeIDRange(seg string) bool {
	parts := strings.SplitN(seg, "-", 2)
	if _, err := strconv.Atoi(parts[0]); err != nil {
		return false
	}
	return true
}

// parseIDRange 解析单个 ID 或 ID 区间（如 "1"、"1-5"）为 ID 列表；非法区间返回错误。
func parseIDRange(seg string) ([]int, error) {
	if parts := strings.SplitN(seg, "-", 2); len(parts) == 2 {
		start, err1 := strconv.Atoi(parts[0])
		end, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || start < 1 || end < start {
			return nil, fmt.Errorf("invalid ID range: %s", seg)
		}
		var ids []int
		for i := start; i <= end; i++ {
			ids = append(ids, i)
		}
		return ids, nil
	}
	id, err := strconv.Atoi(seg)
	if err != nil || id < 1 {
		return nil, fmt.Errorf("invalid ID: %s", seg)
	}
	return []int{id}, nil
}

func findByID(repos []config.Repository, id int) (config.Repository, bool) {
	for _, r := range repos {
		if r.ID == id {
			return r, true
		}
	}
	return config.Repository{}, false
}

func findByName(repos []config.Repository, name string) (config.Repository, bool) {
	for _, r := range repos {
		if r.Name == name {
			return r, true
		}
	}
	return config.Repository{}, false
}
