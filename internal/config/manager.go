package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func AddRepository(path string) error {
	return AddRepositoryWithName(path, "")
}

func AddRepositoryWithName(path string, customName string) error {
	return AddRepositoryWithOptions(path, customName, 0)
}

func AddRepositoryWithOptions(path string, customName string, customID int) error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	for _, repo := range cfg.Repositories {
		if repo.Path == absPath {
			return errors.New("repository already exists")
		}
	}

	var name string
	if customName != "" {
		if nameExists(cfg, customName) {
			return fmt.Errorf("repository name '%s' already exists", customName)
		}
		name = customName
	} else {
		name = filepath.Base(absPath)
		name = uniqueRepoName(cfg, name)
	}

	var id int
	if customID > 0 {
		for _, repo := range cfg.Repositories {
			if repo.ID == customID {
				return fmt.Errorf("repository ID %d already exists", customID)
			}
		}
		id = customID
	} else {
		id = nextRepoID(cfg)
	}

	cfg.Repositories = append(cfg.Repositories, Repository{
		ID:     id,
		Path:   absPath,
		Name:   name,
		Groups: make([]string, 0),
	})

	return cfg.Save()
}

// detachRepoFromWorktreeSets 把该仓库 ID 的成员从所有 worktree 集合连带移出（TQ-01 / AC-03.6），
// 成员被清空的集合整组删除，不留悬空 RepoID；返回受影响的集合名（按配置顺序，供命令层点名）。
// 未命中任何集合时不改动 cfg.WorktreeSets（保持 nil 与既有落盘形态不变）。
func detachRepoFromWorktreeSets(cfg *Config, repoID int) []string {
	var affected []string
	kept := make([]WorktreeSet, 0, len(cfg.WorktreeSets))
	for _, set := range cfg.WorktreeSets {
		members := make([]WorktreeMember, 0, len(set.Members))
		hit := false
		for _, m := range set.Members {
			if m.RepoID == repoID {
				hit = true
				continue
			}
			members = append(members, m)
		}
		if !hit {
			kept = append(kept, set)
			continue
		}
		affected = append(affected, set.Name)
		if len(members) > 0 {
			set.Members = members
			kept = append(kept, set)
		}
	}
	if len(affected) > 0 {
		cfg.WorktreeSets = kept
	}
	return affected
}

// removeRepoByIndex 删除仓库并连带清理 worktree 集合成员；返回受影响的集合名清单。
func removeRepoByIndex(cfg *Config, index int) ([]string, error) {
	repo := cfg.Repositories[index]
	affected := detachRepoFromWorktreeSets(cfg, repo.ID)
	for _, group := range repo.Groups {
		if ids, ok := cfg.Groups[group]; ok {
			cfg.Groups[group] = removeInt(ids, repo.ID)
			if len(cfg.Groups[group]) == 0 {
				delete(cfg.Groups, group)
			}
		}
	}
	cfg.Repositories = append(cfg.Repositories[:index], cfg.Repositories[index+1:]...)
	return affected, cfg.Save()
}

func RemoveRepository(path string) error {
	_, err := RemoveRepositoryAndDetachSets(path)
	return err
}

// RemoveRepositoryAndDetachSets 同 RemoveRepository，额外返回被连带清理的集合名（TQ-01 点名用）。
func RemoveRepositoryAndDetachSets(path string) ([]string, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	for i, repo := range cfg.Repositories {
		if repo.Path == absPath {
			return removeRepoByIndex(cfg, i)
		}
	}

	return nil, errors.New("repository not found")
}

func RemoveRepositoryByID(id int) error {
	_, err := RemoveRepositoryByIDAndDetachSets(id)
	return err
}

// RemoveRepositoryByIDAndDetachSets 同 RemoveRepositoryByID，额外返回被连带清理的集合名。
func RemoveRepositoryByIDAndDetachSets(id int) ([]string, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}

	for i, repo := range cfg.Repositories {
		if repo.ID == id {
			return removeRepoByIndex(cfg, i)
		}
	}

	return nil, errors.New("repository not found")
}

func RemoveRepositoryByName(name string) error {
	_, err := RemoveRepositoryByNameAndDetachSets(name)
	return err
}

// RemoveRepositoryByNameAndDetachSets 同 RemoveRepositoryByName，额外返回被连带清理的集合名。
func RemoveRepositoryByNameAndDetachSets(name string) ([]string, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}

	for i, repo := range cfg.Repositories {
		if repo.Name == name {
			return removeRepoByIndex(cfg, i)
		}
	}

	return nil, errors.New("repository not found")
}

func AddGroup(name string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	if _, ok := cfg.Groups[name]; ok {
		return errors.New("group already exists")
	}

	cfg.Groups[name] = make([]int, 0)
	return cfg.Save()
}

func RemoveGroup(name string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	if _, ok := cfg.Groups[name]; !ok {
		return errors.New("group not found")
	}

	for i := range cfg.Repositories {
		cfg.Repositories[i].Groups = removeString(cfg.Repositories[i].Groups, name)
	}

	delete(cfg.Groups, name)
	return cfg.Save()
}

func AssignToGroup(groupName, repoPath string, autoCreate bool) error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	autoCreated := false
	if _, ok := cfg.Groups[groupName]; !ok {
		if !autoCreate {
			return errors.New("group not found")
		}
		cfg.Groups[groupName] = make([]int, 0)
		autoCreated = true
	}

	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return err
	}

	var repoID int
	var repoIndex int
	found := false
	for i, repo := range cfg.Repositories {
		if repo.Path == absPath {
			repoID = repo.ID
			repoIndex = i
			found = true
			break
		}
	}

	if !found {
		if autoCreated {
			delete(cfg.Groups, groupName)
		}
		return errors.New("repository not found")
	}

	if containsInt(cfg.Groups[groupName], repoID) {
		return errors.New("repository already in group")
	}

	cfg.Groups[groupName] = append(cfg.Groups[groupName], repoID)
	cfg.Repositories[repoIndex].Groups = append(cfg.Repositories[repoIndex].Groups, groupName)

	return cfg.Save()
}

func AssignToGroupByID(groupName string, repoID int, autoCreate bool) error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	autoCreated := false
	if _, ok := cfg.Groups[groupName]; !ok {
		if !autoCreate {
			return errors.New("group not found")
		}
		cfg.Groups[groupName] = make([]int, 0)
		autoCreated = true
	}

	var repoIndex int
	found := false
	for i, repo := range cfg.Repositories {
		if repo.ID == repoID {
			repoIndex = i
			found = true
			break
		}
	}

	if !found {
		if autoCreated {
			delete(cfg.Groups, groupName)
		}
		return errors.New("repository not found")
	}

	if containsInt(cfg.Groups[groupName], repoID) {
		return errors.New("repository already in group")
	}

	cfg.Groups[groupName] = append(cfg.Groups[groupName], repoID)
	cfg.Repositories[repoIndex].Groups = append(cfg.Repositories[repoIndex].Groups, groupName)

	return cfg.Save()
}

func UnassignFromGroup(groupName, repoPath string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	if _, ok := cfg.Groups[groupName]; !ok {
		return errors.New("group not found")
	}

	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return err
	}

	var repoID int
	var repoIndex int
	found := false
	for i, repo := range cfg.Repositories {
		if repo.Path == absPath {
			repoID = repo.ID
			repoIndex = i
			found = true
			break
		}
	}

	if !found {
		return errors.New("repository not found")
	}

	cfg.Groups[groupName] = removeInt(cfg.Groups[groupName], repoID)
	if len(cfg.Groups[groupName]) == 0 {
		delete(cfg.Groups, groupName)
	}

	cfg.Repositories[repoIndex].Groups = removeString(cfg.Repositories[repoIndex].Groups, groupName)

	return cfg.Save()
}

func UnassignFromGroupByID(groupName string, repoID int) error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	if _, ok := cfg.Groups[groupName]; !ok {
		return errors.New("group not found")
	}

	var repoIndex int
	found := false
	for i, repo := range cfg.Repositories {
		if repo.ID == repoID {
			repoIndex = i
			found = true
			break
		}
	}

	if !found {
		return errors.New("repository not found")
	}

	cfg.Groups[groupName] = removeInt(cfg.Groups[groupName], repoID)
	if len(cfg.Groups[groupName]) == 0 {
		delete(cfg.Groups, groupName)
	}

	cfg.Repositories[repoIndex].Groups = removeString(cfg.Repositories[repoIndex].Groups, groupName)

	return cfg.Save()
}

func GetRepositoriesByGroup(groupName string) ([]Repository, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}

	repoIDs, ok := cfg.Groups[groupName]
	if !ok {
		return nil, errors.New("group not found")
	}

	var repos []Repository
	for _, id := range repoIDs {
		for _, repo := range cfg.Repositories {
			if repo.ID == id {
				repos = append(repos, repo)
				break
			}
		}
	}

	return repos, nil
}

func GetAllRepositories() ([]Repository, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}
	return cfg.Repositories, nil
}

func GetAllGroups() (map[string][]int, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}
	return cfg.Groups, nil
}

func removeString(slice []string, target string) []string {
	result := make([]string, 0)
	for _, s := range slice {
		if s != target {
			result = append(result, s)
		}
	}
	return result
}

func removeInt(slice []int, target int) []int {
	result := make([]int, 0)
	for _, v := range slice {
		if v != target {
			result = append(result, v)
		}
	}
	return result
}

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}

func containsInt(slice []int, target int) bool {
	for _, v := range slice {
		if v == target {
			return true
		}
	}
	return false
}

func nameExists(cfg *Config, name string) bool {
	for _, repo := range cfg.Repositories {
		if repo.Name == name {
			return true
		}
	}
	return false
}

// EnsureRepositoryEntry 确保 path 在**内存配置**里有对应仓库条目：已存在（按解析后的绝对
// 路径比对，兼容存量绝对路径与待解析相对路径）则复用并返回「未新建」；否则以 preferredName
// 追加一条新条目（名称冲突沿用 uniqueRepoName 兜底，ID 沿用 nextRepoID 续号）并返回「已新建」。
//
// 只改内存、不落盘：调用方在同一次 Save() 里把仓库条目与 worktree 集合一起写出，避免
// 「条目已存盘而集合未写」的中间态（区别于 AddRepositoryWithOptions：它自带 Load/Save）。
// 内存中 Repository.Path 恒为绝对路径（#1056 的读写同源不变量），相对化由 Save 负责。
func EnsureRepositoryEntry(cfg *Config, path, preferredName string) (Repository, bool, error) {
	if cfg == nil {
		return Repository{}, false, errors.New("nil config")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return Repository{}, false, err
	}
	absPath = filepath.Clean(absPath)
	for _, r := range cfg.Repositories {
		if sameResolvedPath(r.Path, absPath) {
			return r, false, nil
		}
	}
	name := preferredName
	if name == "" {
		name = filepath.Base(absPath)
	}
	// Groups 初始化与 config add 同形（评审 S-01）：留 nil 会落盘 "groups": null，
	// 与手工添加条目的 [] 不一致，ReadMe 示例形状也是 []。
	entry := Repository{
		ID:     nextRepoID(cfg),
		Path:   absPath,
		Name:   uniqueRepoName(cfg, name),
		Groups: make([]string, 0),
	}
	cfg.Repositories = append(cfg.Repositories, entry)
	return entry, true, nil
}

// sameResolvedPath 判断配置中已存路径（绝对或相对）与目标绝对路径是否指向同一目录。
// 相对形态按 ConfigBaseDir 解析后再比；Windows 路径大小写不敏感。
func sameResolvedPath(have, want string) bool {
	a := filepath.Clean(ResolvePath(have))
	b := filepath.Clean(want)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func uniqueRepoName(cfg *Config, name string) string {
	usedNames := make(map[string]bool)
	for _, repo := range cfg.Repositories {
		usedNames[repo.Name] = true
	}
	if !usedNames[name] {
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d", name, i)
		if !usedNames[candidate] {
			return candidate
		}
	}
}

func nextRepoID(cfg *Config) int {
	maxID := 0
	for _, repo := range cfg.Repositories {
		if repo.ID > maxID {
			maxID = repo.ID
		}
	}
	return maxID + 1
}

func ParseRepoRef(ref string) (int, string, error) {
	id, err := strconv.Atoi(ref)
	if err == nil {
		return id, "", nil
	}
	return 0, ref, nil
}

func FindRepoByName(cfg *Config, name string) (int, bool) {
	for _, repo := range cfg.Repositories {
		if repo.Name == name {
			return repo.ID, true
		}
	}
	return 0, false
}

func RenameRepository(repoRef string, newName string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	if nameExists(cfg, newName) {
		return fmt.Errorf("repository name '%s' already exists", newName)
	}

	id, path, _ := ParseRepoRef(repoRef)
	if path != "" {
		// 配置中的 Path 已解析为绝对路径；用户传入的路径引用可能是相对路径。
		if abs, err := filepath.Abs(path); err == nil {
			path = filepath.Clean(abs)
		}
	}
	found := false
	for i := range cfg.Repositories {
		if id > 0 && cfg.Repositories[i].ID == id {
			cfg.Repositories[i].Name = newName
			found = true
			break
		}
		if path != "" && cfg.Repositories[i].Path == path {
			cfg.Repositories[i].Name = newName
			found = true
			break
		}
		if cfg.Repositories[i].Name == repoRef {
			cfg.Repositories[i].Name = newName
			found = true
			break
		}
	}

	if !found {
		return errors.New("repository not found")
	}

	return cfg.Save()
}

func ReassignRepositoryID(oldID int, newID int) error {
	if oldID <= 0 || newID <= 0 {
		return errors.New("ID must be positive")
	}
	if oldID == newID {
		return errors.New("old ID and new ID are the same")
	}

	cfg, err := Load()
	if err != nil {
		return err
	}

	for _, repo := range cfg.Repositories {
		if repo.ID == newID {
			return fmt.Errorf("repository ID %d already exists", newID)
		}
	}

	var repoIndex int
	found := false
	for i := range cfg.Repositories {
		if cfg.Repositories[i].ID == oldID {
			repoIndex = i
			found = true
			break
		}
	}

	if !found {
		return errors.New("repository not found")
	}

	cfg.Repositories[repoIndex].ID = newID

	for groupName, ids := range cfg.Groups {
		for j, id := range ids {
			if id == oldID {
				cfg.Groups[groupName][j] = newID
				break
			}
		}
	}

	// worktree 集合成员同样按 oldID→newID 回写（评审 BLOCK-1）：成员 RepoID 指向仓库条目，
	// 若只改 repositories 与 groups，reid 之后 `-r set:` 会静默指向别的条目或查不到，
	// 再被 config validate 当悬空成员永久删除。
	remapWorktreeMemberIDs(cfg, map[int]int{oldID: newID})

	return cfg.Save()
}

func ReassignAllIDs() error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	if len(cfg.Repositories) == 0 {
		return nil
	}

	idMapping := make(map[int]int)
	for i := range cfg.Repositories {
		oldID := cfg.Repositories[i].ID
		newID := i + 1
		if oldID != newID {
			idMapping[oldID] = newID
			cfg.Repositories[i].ID = newID
		}
	}

	for groupName, ids := range cfg.Groups {
		for j, id := range ids {
			if newID, ok := idMapping[id]; ok {
				cfg.Groups[groupName][j] = newID
			}
		}
	}

	// 集合成员一并续号（评审 BLOCK-1）：与 Groups 同一张 idMapping，未变更的 ID 不在表里即原样保留。
	remapWorktreeMemberIDs(cfg, idMapping)

	return cfg.Save()
}

// remapWorktreeMemberIDs 按 oldID→newID 映射回写 worktree 集合成员的 RepoID。
// 映射表里没有的 ID 保持原值；无集合时零改动（幂等，不产生写盘副作用以外的变化）。
func remapWorktreeMemberIDs(cfg *Config, idMapping map[int]int) {
	if len(cfg.WorktreeSets) == 0 || len(idMapping) == 0 {
		return
	}
	for i := range cfg.WorktreeSets {
		for j := range cfg.WorktreeSets[i].Members {
			if newID, ok := idMapping[cfg.WorktreeSets[i].Members[j].RepoID]; ok {
				cfg.WorktreeSets[i].Members[j].RepoID = newID
			}
		}
	}
}

// DropWorktreeMemberRepos 移除「ID 命中给定成员、且解析后路径等于该成员路径」的仓库条目，
// 返回被移除的条目数（评审 BLOCK-2）。
//
// 为什么需要：`worktree add` 会为每个 worktree 目录自动登记仓库条目；若 `remove`/`prune`
// 只删集合而回收不了条目，那些条目就成了指向已消失目录的死引用——默认全量批量命令每次都要
// 为它们吐一行 `chdir … cannot find the file specified`，只能靠 config validate 兜。
// 为什么必须同时比路径：早期/手工编辑的配置里成员可能指向父仓条目，只按 ID 删会把父仓整条抹掉。
func DropWorktreeMemberRepos(cfg *Config, members []WorktreeMember) int {
	if cfg == nil || len(members) == 0 || len(cfg.Repositories) == 0 {
		return 0
	}
	want := make(map[int]string, len(members)) // repoID → member 路径
	for _, m := range members {
		want[m.RepoID] = m.Path
	}
	kept := make([]Repository, 0, len(cfg.Repositories))
	dropped := 0
	for _, r := range cfg.Repositories {
		if mp, ok := want[r.ID]; ok && sameResolvedPath(r.Path, mp) {
			dropped++
			continue
		}
		kept = append(kept, r)
	}
	cfg.Repositories = kept
	return dropped
}

// detachDanglingWorktreeMembers 兜底清理：把 worktree 集合里指向「配置中已不存在的仓库 ID」的
// 悬空成员移出（手工编辑过配置、或历史遗留），集合清空则整组删除；返回被移出的成员数（TQ-01）。
func detachDanglingWorktreeMembers(cfg *Config) int {
	if len(cfg.WorktreeSets) == 0 {
		return 0
	}
	existing := make(map[int]bool, len(cfg.Repositories))
	for _, repo := range cfg.Repositories {
		existing[repo.ID] = true
	}

	removed := 0
	kept := make([]WorktreeSet, 0, len(cfg.WorktreeSets))
	for _, set := range cfg.WorktreeSets {
		members := make([]WorktreeMember, 0, len(set.Members))
		for _, m := range set.Members {
			if existing[m.RepoID] {
				members = append(members, m)
				continue
			}
			removed++
		}
		if len(members) == 0 {
			continue
		}
		set.Members = members
		kept = append(kept, set)
	}
	if removed > 0 {
		cfg.WorktreeSets = kept
	}
	return removed
}

// ValidateAndClean 校验并清理配置；返回值依次为：移除的空分组数、移除的无效分组引用数、
// 移除的缺失仓库数、移除的 worktree 悬空成员数（TQ-01 兜底口径），以及错误。
func ValidateAndClean() (int, int, int, int, error) {
	cfg, err := Load()
	if err != nil {
		return 0, 0, 0, 0, err
	}

	repoMap := make(map[int]bool)
	for _, repo := range cfg.Repositories {
		repoMap[repo.ID] = true
	}

	cleanedGroups := 0
	cleanedRefs := 0
	missingRepos := 0

	for groupName, ids := range cfg.Groups {
		validIDs := make([]int, 0)
		for _, id := range ids {
			if repoMap[id] {
				validIDs = append(validIDs, id)
			} else {
				cleanedRefs++
			}
		}
		if len(validIDs) == 0 {
			delete(cfg.Groups, groupName)
			cleanedGroups++
		} else {
			cfg.Groups[groupName] = validIDs
		}
	}

	for i := range cfg.Repositories {
		validGroups := make([]string, 0)
		for _, group := range cfg.Repositories[i].Groups {
			if _, ok := cfg.Groups[group]; ok {
				validGroups = append(validGroups, group)
			}
		}
		cfg.Repositories[i].Groups = validGroups
	}

	for i := len(cfg.Repositories) - 1; i >= 0; i-- {
		if _, err := os.Stat(cfg.Repositories[i].Path); os.IsNotExist(err) {
			if _, err := removeRepoByIndex(cfg, i); err != nil {
				return cleanedGroups, cleanedRefs, missingRepos, 0, err
			}
			missingRepos++
		}
	}

	// 兜底：清理 worktree 集合里的悬空 RepoID 成员（removeRepoByIndex 已处理的路径不重复计数）。
	cleanedSetMembers := detachDanglingWorktreeMembers(cfg)

	if cleanedRefs > 0 || cleanedGroups > 0 || missingRepos > 0 || cleanedSetMembers > 0 {
		return cleanedGroups, cleanedRefs, missingRepos, cleanedSetMembers, cfg.Save()
	}

	return 0, 0, 0, 0, nil
}
