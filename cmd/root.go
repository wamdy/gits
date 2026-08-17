package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/i18n"
	"github.com/wamdy/gits/internal/ui"
)

var (
	version  = "1.0.0"
	group    string
	repoIDs  string
	parallel bool
	yesFlag  bool
	color    string
	noColor  bool
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
	rootCmd.PersistentFlags().StringVar(&color, "color", "auto", i18n.T("color_desc"))
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, i18n.T("no_color_desc"))
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

	// 在产生任何输出前解析颜色选项并初始化颜色模式
	// （cobra 初始化后、命令执行前；--color=invalid 在此报错退出非 0）
	if err := rootCmd.ParseFlags(os.Args[1:]); err == nil {
		switch color {
		case "auto", "always", "never":
		default:
			fmt.Fprintf(os.Stderr, "Error: invalid value for --color: %s (must be auto, always, or never)\n", color)
			os.Exit(1)
		}
	}
	ui.InitColorMode(color, noColor)

	// 动态设置命令描述（语言可能在上一次运行中被修改）
	rootCmd.Short = i18n.T("manage_multi_repos")
	rootCmd.Long = `gits - ` + i18n.T("git_cmd_desc")
	rootCmd.PersistentFlags().Lookup("group").Usage = i18n.T("execute_on_group")
	rootCmd.PersistentFlags().Lookup("repo").Usage = i18n.T("repo_selection_note")
	rootCmd.PersistentFlags().Lookup("parallel").Usage = i18n.T("execute_parallel")
	rootCmd.PersistentFlags().Lookup("yes").Usage = i18n.T("skip_confirm")
	rootCmd.PersistentFlags().Lookup("color").Usage = i18n.T("color_desc")
	rootCmd.PersistentFlags().Lookup("no-color").Usage = i18n.T("no_color_desc")

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
