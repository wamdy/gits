package repo

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
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
	_, err := UnregisterRepositoryByRefAndDetachSets(ref)
	return err
}

// UnregisterRepositoryByRefAndDetachSets 同 UnregisterRepositoryByRef（repo-ref 优先级不变），
// 额外返回被连带移出成员的 worktree 集合名，供 `config remove` 点名输出（TQ-01 / AC-03.6）。
func UnregisterRepositoryByRefAndDetachSets(ref string) ([]string, error) {
	id, path, _ := config.ParseRepoRef(ref)
	if id > 0 {
		return config.RemoveRepositoryByIDAndDetachSets(id)
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if foundID, ok := config.FindRepoByName(cfg, ref); ok {
		return config.RemoveRepositoryByIDAndDetachSets(foundID)
	}
	return config.RemoveRepositoryAndDetachSets(path)
}

func GetRegisteredRepositories() ([]config.Repository, error) {
	return config.GetAllRepositories()
}

// DisplayBranch 统一「列表展示用分支值」口径（三处列出场景共用，避免口径发散）：
// 正常分支 → 原样返回；无分支（空仓库）或游离 HEAD（GetBranchName 得 "HEAD"）→ 取短 commit id；
// 空仓库无任何 commit（commit id 也为空）→ 返回空串（列留白）。
func DisplayBranch(branch, path string) string {
	if branch == "" || branch == "HEAD" {
		if id := git.GetCommitIDShort(path); id != "" {
			return id
		}
		return ""
	}
	return branch
}

// DisplayLabel 统一「仓库回显」口径：`[ID] 名称 (分支)`。
// ID <= 0（尚未注册的待添加仓库）省略 ID 前缀；分支为空（空仓库无任何 commit）省略括号。
// 与 DisplayBranch 配套使用，保证 review 提示与逐仓结果行口径一致。
func DisplayLabel(id int, name, branch string) string {
	label := name
	if id > 0 {
		label = fmt.Sprintf("[%d] %s", id, name)
	}
	if branch != "" {
		label += fmt.Sprintf(" (%s)", branch)
	}
	return label
}

// WorktreeMark 渲染 worktree 归属标记（` ← 主仓名`）；mainName 为空（普通仓库）返回空串不加噪。
// 三处列出（config show / config group show / config scan）与 review 提示、结果行共用这一处拼接，
// 不在 cmd 层各自拼字符串（AC-03.7）。
func WorktreeMark(mainName string) string {
	if mainName == "" {
		return ""
	}
	return i18n.T("worktree_of_mark", mainName)
}

// IndexByID 把仓库登记表按 ID 索引，供成员展示名反查复用（避免三处各建一次 map）。
func IndexByID(repos []config.Repository) map[int]config.Repository {
	byID := make(map[int]config.Repository, len(repos))
	for _, r := range repos {
		byID[r.ID] = r
	}
	return byID
}

// MemberLabel 给出 worktree 集合成员的展示名：优先取登记仓库名，未登记或名称为空时
// 退回成员路径的 Base。三处列出（worktree list / config show 的集合节 / remove·prune
// 的成员预览）共用本函数（评审 S-10），延续 AC-03.7「不得各自实现一套口径」的初衷。
func MemberLabel(m config.WorktreeMember, byID map[int]config.Repository) string {
	if r, ok := byID[m.RepoID]; ok && r.Name != "" {
		return r.Name
	}
	return filepath.Base(m.Path)
}

// DisplayLabelWithWorktree 完整列出口径：`[ID] 名称 (分支) ← 主仓名`。
// mainName 为空时退化为 DisplayLabel（普通仓库形态不变）。
func DisplayLabelWithWorktree(id int, name, branch, mainName string) string {
	return DisplayLabel(id, name, branch) + WorktreeMark(mainName)
}

// DetectWorktreeOf 运行时判定该路径是否为 linked worktree，是则返回所属主仓路径，
// 否则返回空串（普通仓库 / submodule / 非 git）。worktree 身份不落配置，始终取自 git 事实。
func DetectWorktreeOf(path string) string {
	if path == "" || git.Classify(path) != git.KindLinkedWorktree {
		return ""
	}
	return git.MainRepoPath(path)
}

// MainRepoName 反查主仓展示名：在已登记仓库中按路径命中取其 Name，
// 查不到（主仓未登记/分组列表不完整）则退回主仓路径的 base 名；mainPath 为空返回空串。
func MainRepoName(mainPath string, repos []config.Repository) string {
	if mainPath == "" {
		return ""
	}
	target := filepath.Clean(filepath.FromSlash(mainPath))
	for _, r := range repos {
		if r.Path == "" || r.Name == "" {
			continue
		}
		if sameDir(r.Path, target) {
			return r.Name
		}
	}
	return filepath.Base(target)
}

// WorktreeMainName 组合入口：运行时判定 + 反查主仓展示名（供只有路径的列出场景使用）。
func WorktreeMainName(path string, repos []config.Repository) string {
	return MainRepoName(DetectWorktreeOf(path), repos)
}

// RepoLabels 生成一组仓库的回显标签（review 提示 / 逐仓结果行 / scan 候选共用口径）：
// `[ID] 名称 (分支) ← 主仓名`；分支为空时现取，主仓名在 allRepos 中反查（可为 nil）。
func RepoLabels(infos []RepositoryInfo, allRepos []config.Repository) []string {
	labels := make([]string, len(infos))
	for i, r := range infos {
		branch := r.Branch
		if branch == "" {
			branch = git.GetBranchName(r.Path)
		}
		labels[i] = DisplayLabelWithWorktree(r.ID, r.Name, DisplayBranch(branch, r.Path),
			MainRepoName(r.WorktreeOf, allRepos))
	}
	return labels
}

// sameDir 比较两个目录路径是否同一目录（分隔符归一；Windows 路径大小写不敏感）。
func sameDir(a, b string) bool {
	a = filepath.Clean(filepath.FromSlash(a))
	b = filepath.Clean(filepath.FromSlash(b))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// GetRepositoryBranches 串行为每个仓库填充运行时 Branch 字段（供 config show / config group show 打印）。
func GetRepositoryBranches(repos []config.Repository) []config.Repository {
	for i := range repos {
		repos[i].Branch = DisplayBranch(git.GetBranchName(repos[i].Path), repos[i].Path)
	}
	return repos
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
// 保留为 ResolveSelectionWithSets 的薄壳（不破坏既有调用方与单测签名）。
func ResolveSelection(raw string, repos []config.Repository, branchChecker BranchChecker) ([]config.Repository, error) {
	return ResolveSelectionWithSets(raw, repos, nil, branchChecker)
}

// ResolveSelectionWithSets 在 ResolveSelection 的段类型之上追加 `set:<集合名>` 段：
// 命中 worktree 集合 → 按 Members[].RepoID 收集（仓库已不存在则跳过该成员），沿用 seen 按 ID 去重；
// 集合名为空或不存在 → 返回 error（不静默返回空，AC-03.1）。
// sets 为 nil（老配置/旧调用方）时，任何 set: 段都按「集合不存在」报错。
func ResolveSelectionWithSets(raw string, repos []config.Repository, sets []config.WorktreeSet, branchChecker BranchChecker) ([]config.Repository, error) {
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
		case strings.HasPrefix(seg, "set:"): // 按 worktree 集合成员选择
			name := strings.TrimPrefix(seg, "set:")
			if name == "" {
				return nil, errors.New(i18n.T("set_name_empty"))
			}
			set, ok := findSetByName(sets, name)
			if !ok {
				return nil, errors.New(i18n.T("set_not_found", name))
			}
			for _, m := range set.Members {
				if r, ok := findByID(repos, m.RepoID); ok && !seen[r.ID] {
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

// findSetByName 按集合名精确命中 worktree 集合（集合名不做通配/大小写变体）。
func findSetByName(sets []config.WorktreeSet, name string) (config.WorktreeSet, bool) {
	for _, s := range sets {
		if s.Name == name {
			return s, true
		}
	}
	return config.WorktreeSet{}, false
}
