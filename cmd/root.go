package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/i18n"
)

var (
	version  = "1.0.0"
	group    string
	repoIDs  string
	parallel bool
	yesFlag  bool
)

var rootCmd = &cobra.Command{
	Use:     "gits",
	Version: version,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Help()
			return
		}

		gitArgs := extractGitArgs(args)
		if len(gitArgs) == 0 {
			cmd.Help()
			return
		}

		runGitCommand(gitArgs)
	},
	Args: cobra.ArbitraryArgs,
}

func extractGitArgs(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}
	return args
}

func init() {
	initI18n()

	rootCmd.Short = i18n.T("manage_multi_repos")
	rootCmd.Long = `gits - ` + i18n.T("git_cmd_desc")

	rootCmd.PersistentFlags().StringVarP(&group, "group", "g", "", i18n.T("execute_on_group"))
	rootCmd.PersistentFlags().StringVarP(&repoIDs, "repo", "r", "", i18n.T("execute_on_repos"))
	rootCmd.PersistentFlags().BoolVarP(&parallel, "parallel", "p", false, i18n.T("execute_parallel"))
	rootCmd.PersistentFlags().BoolVarP(&yesFlag, "yes", "y", false, i18n.T("skip_confirm"))
}

func initI18n() {
	cfg, _ := config.Load()
	if cfg.Language != "" {
		i18n.SetLang(i18n.Lang(cfg.Language))
	} else {
		i18n.SetLang(i18n.DetectSystemLang())
	}
}

func Execute() {
	initI18n()

	// 动态设置命令描述（语言可能在上一次运行中被修改）
	rootCmd.Short = i18n.T("manage_multi_repos")
	rootCmd.Long = `gits - ` + i18n.T("git_cmd_desc")
	rootCmd.PersistentFlags().Lookup("group").Usage = i18n.T("execute_on_group")
	rootCmd.PersistentFlags().Lookup("repo").Usage = i18n.T("execute_on_repos")
	rootCmd.PersistentFlags().Lookup("parallel").Usage = i18n.T("execute_parallel")
	rootCmd.PersistentFlags().Lookup("yes").Usage = i18n.T("skip_confirm")

	initConfigCmdI18n()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func GetGroup() string {
	return group
}

func GetRepoIDs() string {
	return repoIDs
}

func GetParallel() bool {
	return parallel
}

func GetYes() bool {
	return yesFlag
}
