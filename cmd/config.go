package cmd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/i18n"
	"github.com/wamdy/gits/internal/repo"
	"github.com/wamdy/gits/internal/ui"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration",
	Long:  "Manage gits configuration including repositories and groups.",
}

var (
	addRepoName string
	addRepoID   int
	showRaw     bool
)

var configAddCmd = &cobra.Command{
	Use:   "add <path>",
	Short: "Add repository",
	Long:  "Add a repository to the management list. Use --name to specify a custom name, --id to specify a custom ID.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		err := repo.RegisterRepositoryWithOptions(args[0], addRepoName, addRepoID)
		if err != nil {
			ui.PrintError(err)
			return
		}
		ui.PrintSuccess("Repository added successfully")
	},
}

var configRemoveCmd = &cobra.Command{
	Use:   "remove <repo-ref>",
	Short: "Remove repository",
	Long:  "Remove a repository from the management list by ID, name, or path. Worktree set members pointing at the repository are detached as well.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// 删仓连带把该成员从所有 worktree 集合移出，集合清空则删除集合（TQ-01 / AC-03.6）。
		affected, err := repo.UnregisterRepositoryByRefAndDetachSets(args[0])
		if err != nil {
			ui.PrintError(err)
			return
		}
		ui.PrintSuccess("Repository removed successfully")
		if len(affected) > 0 {
			ui.PrintMessage(i18n.T("config_remove_sets_hit", len(affected), strings.Join(affected, ", ")))
		}
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show configuration",
	Long:  "Show current configuration including all registered repositories. Use --raw to display raw JSON.",
	Run: func(cmd *cobra.Command, args []string) {
		if showRaw {
			data, err := config.GetRawConfig()
			if err != nil {
				ui.PrintError(err)
				return
			}
			fmt.Println(string(data))
			return
		}
		cfg, err := config.Load()
		if err != nil {
			ui.PrintError(err)
			return
		}
		repos := repo.GetRepositoryBranches(cfg.Repositories)
		ui.PrintRegisteredRepositories(repos)
		ui.PrintConfigSettings(cfg)
		// worktree 集合块紧跟配置项之后；无集合时整块不输出。
		ui.PrintWorktreeSets(cfg.WorktreeSets, repos)
		ui.PrintConfigPath(config.GetConfigPath())
	},
}

var configRenameCmd = &cobra.Command{
	Use:   "rename <repo-ref> <new-name>",
	Short: "Rename repository",
	Long:  "Rename a repository by ID, name, or path.",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		err := repo.RenameRepository(args[0], args[1])
		if err != nil {
			ui.PrintError(err)
			return
		}
		ui.PrintSuccess("Repository renamed successfully")
	},
}

var reidAllFlag bool

var configReidCmd = &cobra.Command{
	Use:   "reid [<old-id> <new-id>]",
	Short: "Reassign repository ID",
	Long:  "Change a repository's ID and update all group references. Use --all to reassign all IDs sequentially starting from 1.",
	Args: func(cmd *cobra.Command, args []string) error {
		if reidAllFlag {
			if len(args) != 0 {
				return errors.New("no arguments allowed with --all")
			}
			return nil
		}
		if len(args) != 2 {
			return errors.New("requires exactly 2 arguments: <old-id> <new-id>")
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		if reidAllFlag {
			err := repo.ReassignAllIDs()
			if err != nil {
				ui.PrintError(err)
				return
			}
			ui.PrintSuccess("All repository IDs reassigned sequentially starting from 1")
			return
		}

		oldID, err1 := strconv.Atoi(args[0])
		newID, err2 := strconv.Atoi(args[1])
		if err1 != nil || err2 != nil || oldID <= 0 || newID <= 0 {
			ui.PrintError(errors.New("both IDs must be positive integers"))
			return
		}
		err := repo.ReassignRepositoryID(oldID, newID)
		if err != nil {
			ui.PrintError(err)
			return
		}
		ui.PrintSuccess("Repository ID reassigned successfully")
	},
}

var configGroupCmd = &cobra.Command{
	Use:   "group",
	Short: "Manage groups",
	Long:  "Manage repository groups.",
}

var configGroupAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add group",
	Long:  "Create a new repository group.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		err := repo.CreateGroup(args[0])
		if err != nil {
			ui.PrintError(err)
			return
		}
		ui.PrintSuccess("Group created successfully")
	},
}

var configGroupRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove group",
	Long:  "Delete a repository group.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		err := repo.DeleteGroup(args[0])
		if err != nil {
			ui.PrintError(err)
			return
		}
		ui.PrintSuccess("Group removed successfully")
	},
}

var configGroupAssignCmd = &cobra.Command{
	Use:   "assign <group> <repo-ref>",
	Short: "Assign repository to group",
	Long:  "Assign a repository to a group by name, path or ID. Auto-creates group with user confirmation if it doesn't exist.",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		groupName := args[0]
		repoRef := args[1]

		exists, err := repo.GroupExists(groupName)
		if err != nil {
			ui.PrintError(err)
			return
		}

		autoCreate := false
		if !exists {
			fmt.Printf("%sGroup '%s' does not exist. Create it? [y/N]:%s ",
				ui.ColorYellow, groupName, ui.ColorReset)
			var response string
			_, err := fmt.Scanln(&response)
			if err != nil {
				ui.PrintMessage("Operation cancelled.")
				return
			}
			if response == "y" || response == "Y" || response == "yes" || response == "Yes" {
				autoCreate = true
			} else {
				ui.PrintMessage("Operation cancelled.")
				return
			}
		}

		err = repo.AssignToGroup(groupName, repoRef, autoCreate)
		if err != nil {
			ui.PrintError(err)
			return
		}
		ui.PrintSuccess("Repository assigned to group successfully")
	},
}

var configGroupUnassignCmd = &cobra.Command{
	Use:   "unassign <group> <repo-ref>",
	Short: "Unassign repository from group",
	Long:  "Remove a repository from a group by name, path or ID.",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		err := repo.UnassignFromGroup(args[0], args[1])
		if err != nil {
			ui.PrintError(err)
			return
		}
		ui.PrintSuccess("Repository unassigned from group successfully")
	},
}

var configGroupShowCmd = &cobra.Command{
	Use:   "show [<group>]",
	Short: "Show groups",
	Long:  "Show all groups or a specific group's members.",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			groups, err := repo.GetAllGroups()
			if err != nil {
				ui.PrintError(err)
				return
			}
			allRepos, err := repo.GetRegisteredRepositories()
			if err != nil {
				ui.PrintError(err)
				return
			}
			allRepos = repo.GetRepositoryBranches(allRepos)
			ui.PrintGroups(groups, allRepos)
		} else {
			repos, err := repo.GetRepositoriesByGroup(args[0])
			if err != nil {
				ui.PrintError(err)
				return
			}
			repos = repo.GetRepositoryBranches(repos)
			// 全量登记表一并传入：worktree 归属反查主仓条目，主仓通常不在被看的组里（评审 Q-01）。
			allRepos, err := repo.GetRegisteredRepositories()
			if err != nil {
				ui.PrintError(err)
				return
			}
			ui.PrintGroupDetails(args[0], repos, allRepos)
		}
	},
}

var configScanCmd = &cobra.Command{
	Use:   "scan [path]",
	Short: "Scan and add repositories",
	Long:  "Scan directory for Git repositories and interactively select which ones to add.",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		scanPath := "."
		if len(args) > 0 {
			scanPath = args[0]
		}

		discovered, err := repo.Discover(scanPath)
		if err != nil {
			ui.PrintError(err)
			return
		}

		if len(discovered) == 0 {
			ui.PrintMessage("No Git repositories found.")
			return
		}

		registered, err := repo.GetRegisteredRepositories()
		if err != nil {
			ui.PrintError(err)
			return
		}

		registeredIDs := make(map[string]int)
		for _, r := range registered {
			registeredIDs[r.Path] = r.ID
		}

		var newRepos []repo.RepositoryInfo
		fmt.Print(i18n.T("found_repos", len(discovered)) + ":\n\n")
		for i, r := range discovered {
			// 列出交由 ui 统一口径：分支展示值与 worktree 归属标记（← 主仓）
			// 与 config show / group show / review 提示共用同一套函数，cmd 层不拼字符串。
			ui.PrintScanCandidate(i, r, registeredIDs[r.Path], registered)
			if _, ok := registeredIDs[r.Path]; !ok {
				newRepos = append(newRepos, r)
			}
		}

		if len(newRepos) == 0 {
			ui.PrintMessage("\n" + i18n.T("all_registered"))
			return
		}

		fmt.Printf("\n%sSelect repositories to add (e.g., 1,3,5 or 1-5 or all):%s ",
			ui.ColorYellow, ui.ColorReset)
		var input string
		_, scanErr := fmt.Scanln(&input)
		if scanErr != nil {
			ui.PrintMessage("No repositories selected.")
			return
		}

		input = strings.TrimSpace(input)
		if input == "" {
			ui.PrintMessage("No repositories selected.")
			return
		}

		selected := parseSelection(input, len(discovered))
		if len(selected) == 0 {
			ui.PrintMessage("Invalid selection.")
			return
		}

		addedCount := 0
		skippedCount := 0
		for _, idx := range selected {
			r := discovered[idx]
			if id, ok := registeredIDs[r.Path]; ok {
				ui.PrintMessage(fmt.Sprintf("Skipped: %s (already registered as ID %d)", r.Name, id))
				skippedCount++
				continue
			}
			err := repo.RegisterRepository(r.Path)
			if err != nil {
				ui.PrintMessage("Skipped: " + r.Name + " (" + err.Error() + ")")
				skippedCount++
			} else {
				ui.PrintSuccess("Added: " + r.Name)
				addedCount++
			}
		}

		fmt.Printf("\nSummary: %d added, %d skipped\n", addedCount, skippedCount)
	},
}

func parseSelection(input string, max int) []int {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "all" || input == "a" {
		var result []int
		for i := 0; i < max; i++ {
			result = append(result, i)
		}
		return result
	}

	var result []int
	parts := strings.Split(input, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			rangeParts := strings.SplitN(part, "-", 2)
			start, err1 := strconv.Atoi(rangeParts[0])
			end, err2 := strconv.Atoi(rangeParts[1])
			if err1 == nil && err2 == nil && start >= 1 && end <= max && start <= end {
				for i := start - 1; i < end; i++ {
					result = append(result, i)
				}
			}
		} else {
			num, err := strconv.Atoi(part)
			if err == nil && num >= 1 && num <= max {
				result = append(result, num-1)
			}
		}
	}
	return result
}

var configLanguageCmd = &cobra.Command{
	Use:   "language <zh|en>",
	Short: i18n.T("set_language"),
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		lang := args[0]
		if lang != "zh" && lang != "en" {
			ui.PrintError(fmt.Errorf("%s", i18n.T("invalid_language", lang)))
			return
		}
		cfg, err := config.Load()
		if err != nil {
			ui.PrintError(err)
			return
		}
		cfg.Language = lang
		if err := cfg.Save(); err != nil {
			ui.PrintError(err)
			return
		}
		i18n.SetLang(i18n.Lang(lang))
		ui.PrintSuccess(i18n.T("language_set") + " " + lang)
	},
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate and clean configuration",
	Long:  "Check configuration for invalid group references and clean them up.",
	Run: func(cmd *cobra.Command, args []string) {
		// 最后一项是被兜底清理的 worktree 悬空成员数（TQ-01 的补偿路径）。
		cleanedGroups, cleanedRefs, missingRepos, cleanedSetMembers, err := config.ValidateAndClean()
		if err != nil {
			ui.PrintError(err)
			return
		}
		if cleanedGroups == 0 && cleanedRefs == 0 && missingRepos == 0 && cleanedSetMembers == 0 {
			ui.PrintSuccess(i18n.T("config_valid"))
			return
		}
		ui.PrintSuccess(fmt.Sprintf("%s: %d %s, %d %s, %d %s",
			i18n.T("config_cleaned"),
			cleanedGroups, i18n.T("empty_groups_removed"),
			cleanedRefs, i18n.T("invalid_refs_removed"),
			missingRepos, i18n.T("missing_repos_removed")))
		if cleanedSetMembers > 0 {
			ui.PrintMessage(fmt.Sprintf("%d %s", cleanedSetMembers, i18n.T("set_members_removed")))
		}
	},
}

var configNormalizeCmd = &cobra.Command{
	Use:   "normalize",
	Short: "Normalize repository paths",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		changed, err := config.NormalizePaths()
		if err != nil {
			ui.PrintError(err)
			return
		}
		ui.PrintSuccess(i18n.T("config_normalized", changed))
	},
}

func initConfigCmdI18n() {
	configCmd.Short = i18n.T("config_cmd")
	configCmd.Long = i18n.T("config_cmd") + ": " + i18n.T("manage_config_detail")
	configAddCmd.Short = i18n.T("cmd_add")
	configRemoveCmd.Short = i18n.T("cmd_remove")
	configShowCmd.Short = i18n.T("config_show")
	configRenameCmd.Short = i18n.T("cmd_rename")
	configReidCmd.Short = i18n.T("cmd_reid")
	configScanCmd.Short = i18n.T("cmd_scan")
	configLanguageCmd.Short = i18n.T("cmd_language")
	configValidateCmd.Short = i18n.T("cmd_validate")
	configNormalizeCmd.Short = i18n.T("cmd_normalize")
	configNormalizeCmd.Long = i18n.T("cmd_normalize_long")
	configGroupCmd.Short = i18n.T("manage_groups")
	configGroupAddCmd.Short = i18n.T("cmd_group_add")
	configGroupRemoveCmd.Short = i18n.T("cmd_group_remove")
	configGroupAssignCmd.Short = i18n.T("cmd_group_assign")
	configGroupUnassignCmd.Short = i18n.T("cmd_group_unassign")
	configGroupShowCmd.Short = i18n.T("cmd_group_show")

	configAddCmd.Flags().Lookup("name").Usage = i18n.T("specify_custom_name")
	configAddCmd.Flags().Lookup("id").Usage = i18n.T("specify_custom_id")
	configShowCmd.Flags().Lookup("raw").Usage = i18n.T("display_raw_json")
	configReidCmd.Flags().Lookup("all").Usage = i18n.T("reassign_all_ids")
}

func init() {
	rootCmd.AddCommand(configCmd)

	configCmd.AddCommand(configAddCmd)
	configCmd.AddCommand(configRemoveCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configRenameCmd)
	configCmd.AddCommand(configReidCmd)
	configCmd.AddCommand(configScanCmd)
	configCmd.AddCommand(configLanguageCmd)
	configCmd.AddCommand(configValidateCmd)
	configCmd.AddCommand(configNormalizeCmd)
	configCmd.AddCommand(configGroupCmd)

	configGroupCmd.AddCommand(configGroupAddCmd)
	configGroupCmd.AddCommand(configGroupRemoveCmd)
	configGroupCmd.AddCommand(configGroupAssignCmd)
	configGroupCmd.AddCommand(configGroupUnassignCmd)
	configGroupCmd.AddCommand(configGroupShowCmd)

	configAddCmd.Flags().StringVarP(&addRepoName, "name", "n", "", i18n.T("specify_custom_name"))
	configAddCmd.Flags().IntVarP(&addRepoID, "id", "i", 0, i18n.T("specify_custom_id"))
	configShowCmd.Flags().BoolVarP(&showRaw, "raw", "R", false, i18n.T("display_raw_json"))
	configReidCmd.Flags().BoolVarP(&reidAllFlag, "all", "a", false, i18n.T("reassign_all_ids"))
}
