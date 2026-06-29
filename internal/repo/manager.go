package repo

import (
	"github.com/wamdy/gits/internal/config"
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
