package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

func removeRepoByIndex(cfg *Config, index int) error {
	repo := cfg.Repositories[index]
	for _, group := range repo.Groups {
		if ids, ok := cfg.Groups[group]; ok {
			cfg.Groups[group] = removeInt(ids, repo.ID)
			if len(cfg.Groups[group]) == 0 {
				delete(cfg.Groups, group)
			}
		}
	}
	cfg.Repositories = append(cfg.Repositories[:index], cfg.Repositories[index+1:]...)
	return cfg.Save()
}

func RemoveRepository(path string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	for i, repo := range cfg.Repositories {
		if repo.Path == absPath {
			return removeRepoByIndex(cfg, i)
		}
	}

	return errors.New("repository not found")
}

func RemoveRepositoryByID(id int) error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	for i, repo := range cfg.Repositories {
		if repo.ID == id {
			return removeRepoByIndex(cfg, i)
		}
	}

	return errors.New("repository not found")
}

func RemoveRepositoryByName(name string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	for i, repo := range cfg.Repositories {
		if repo.Name == name {
			return removeRepoByIndex(cfg, i)
		}
	}

	return errors.New("repository not found")
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

	return cfg.Save()
}

func ValidateAndClean() (int, int, int, error) {
	cfg, err := Load()
	if err != nil {
		return 0, 0, 0, err
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
			removeRepoByIndex(cfg, i)
			missingRepos++
		}
	}

	if cleanedRefs > 0 || cleanedGroups > 0 || missingRepos > 0 {
		return cleanedGroups, cleanedRefs, missingRepos, cfg.Save()
	}

	return 0, 0, 0, nil
}
