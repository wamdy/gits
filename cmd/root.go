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

// rootLongText 组装 root 的 Long：一句话说明 + worktree 子命令的兼容口径（TQ-04）。
// `worktree` 注册为子命令后 `gits worktree <sub>` 不再透传给 git，
// 裸 git worktree 透传形态改为 `gits -r <sel> -- worktree <sub>`；
// 文案集中在 i18n（zh/en 成对），init 与 Execute 两处都走本函数，切语言即刻生效。
func rootLongText() string {
	return "gits - " + i18n.T("git_cmd_desc") + "\n\n" + i18n.T("worktree_passthrough_note") +
		"\n" + i18n.T("pack_passthrough_note")
}

func init() {
	initI18n()

	rootCmd.Short = i18n.T("manage_multi_repos")
	rootCmd.Long = rootLongText()
	rootCmd.AddCommand(worktreeCmd)
	rootCmd.AddCommand(packCmd)

	rootCmd.PersistentFlags().StringVarP(&group, "group", "g", "", i18n.T("execute_on_group"))
	rootCmd.PersistentFlags().StringVarP(&repoIDs, "repo", "r", "", i18n.T("execute_on_repos"))
	rootCmd.PersistentFlags().BoolVarP(&parallel, "parallel", "p", false, i18n.T("execute_parallel"))
	rootCmd.PersistentFlags().BoolVarP(&yesFlag, "yes", "y", false, i18n.T("skip_confirm"))
	rootCmd.PersistentFlags().StringVar(&color, "color", "auto", i18n.T("color_desc"))
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, i18n.T("no_color_desc"))
}

func initI18n() {
	cfg, err := config.Load()
	if err == nil && cfg != nil && cfg.Language != "" {
		i18n.SetLang(i18n.Lang(cfg.Language))
	} else {
		// 配置缺失或解析失败时回退系统语言；损坏配置的报错与退出码
		// 由命令执行路径（runGitCommand 的 config.Load 分支）统一处理，
		// 不在 init 阶段崩溃。
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
	rootCmd.Long = rootLongText()
	rootCmd.PersistentFlags().Lookup("group").Usage = i18n.T("execute_on_group")
	rootCmd.PersistentFlags().Lookup("repo").Usage = i18n.T("repo_selection_note")
	rootCmd.PersistentFlags().Lookup("parallel").Usage = i18n.T("execute_parallel")
	rootCmd.PersistentFlags().Lookup("yes").Usage = i18n.T("skip_confirm")
	rootCmd.PersistentFlags().Lookup("color").Usage = i18n.T("color_desc")
	rootCmd.PersistentFlags().Lookup("no-color").Usage = i18n.T("no_color_desc")

	initConfigCmdI18n()
	initWorktreeCmdI18n()
	initPackCmdI18n()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// git 透传路径的退出码在 runGitCommand 内按语义设置（0/1/2）：
	// 选择/配置失败=2、执行阶段有失败=1、全部成功=0。worktree/config
	// 子命令各自 exitOnError 已直接 os.Exit(1)，不会走到这里。
	if exitCode != exitOK {
		os.Exit(exitCode)
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
