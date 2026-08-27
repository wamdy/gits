package ui

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/git"
	"github.com/wamdy/gits/internal/i18n"
	"github.com/wamdy/gits/internal/repo"
	"golang.org/x/term"
)

// 颜色码改为包级变量：由 InitColorMode 按终端能力统一置 ANSI 码或空串，
// 全部调用点（56 处）引用包级变量，模式初始化后自动生效，无需逐点改动。
var (
	ColorReset  string
	ColorRed    string
	ColorGreen  string
	ColorYellow string
	ColorBlue   string
	ColorPurple string
	ColorCyan   string
)

// ColorMode 表示颜色输出模式。
type ColorMode int

const (
	ColorAuto   ColorMode = iota // 自动检测终端能力
	ColorAlways                  // 强制着色
	ColorNever                   // 强制纯文本
)

var colorMode = ColorAuto

// InitColorMode 根据 --color / --no-color 选项与终端能力初始化颜色变量。
// 必须在任何输出发生前调用一次。
func InitColorMode(mode string, noColorFlag bool) {
	switch {
	case noColorFlag || mode == "never":
		colorMode = ColorNever
	case mode == "always":
		colorMode = ColorAlways
	default:
		colorMode = ColorAuto
	}
	applyColorMode()
}

func applyColorMode() {
	if shouldColor() {
		ColorReset = "\033[0m"
		ColorRed = "\033[31m"
		ColorGreen = "\033[32m"
		ColorYellow = "\033[33m"
		ColorBlue = "\033[34m"
		ColorPurple = "\033[35m"
		ColorCyan = "\033[36m"
		return
	}
	ColorReset, ColorRed, ColorGreen, ColorYellow = "", "", "", ""
	ColorBlue, ColorPurple, ColorCyan = "", "", ""
}

// shouldColor 判断当前模式与终端能力下是否应输出颜色。
func shouldColor() bool {
	switch colorMode {
	case ColorAlways:
		return true
	case ColorNever:
		return false
	}
	// auto：尊重 NO_COLOR 约定
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	// 非 TTY（输出重定向到文件/管道）不输出颜色
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return false
	}
	return supportsVT()
}


func PrintRepoInfo(info repo.RepositoryInfo) {
	fmt.Printf("\n%s%s%s\n", ColorBlue, info.Name, ColorReset)
	fmt.Printf("  %s: %s\n", i18n.T("path"), info.Path)
	fmt.Printf("  %s: %s%s%s\n", i18n.T("branch"), ColorPurple, info.Branch, ColorReset)
	if info.Status != "" {
		fmt.Printf("  %s: %s%s%s\n", i18n.T("status"), ColorYellow, info.Status, ColorReset)
	} else {
		fmt.Printf("  %s: %s%s%s\n", i18n.T("status"), ColorGreen, i18n.T("clean"), ColorReset)
	}
}

func PrintRepositories(repos []repo.RepositoryInfo) {
	if len(repos) == 0 {
		fmt.Println(i18n.T("no_git_repos_found"))
		return
	}

	fmt.Print(i18n.T("found_repos", len(repos)) + ":\n")
	for _, r := range repos {
		PrintRepoInfo(r)
	}
}

func PrintRegisteredRepositories(repos []config.Repository) {
	if len(repos) == 0 {
		fmt.Println(i18n.T("no_registered_repos"))
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "%s%s%s\t%s%s%s\t%s%s%s\t%s%s%s\t%s%s%s\n",
		ColorCyan, i18n.T("id"), ColorReset,
		ColorCyan, i18n.T("name"), ColorReset,
		ColorCyan, i18n.T("branch"), ColorReset,
		ColorCyan, i18n.T("path"), ColorReset,
		ColorCyan, i18n.T("groups"), ColorReset)
	fmt.Fprintln(w, "--\t----\t------\t----\t------")
	for _, r := range repos {
		groups := i18n.T("none")
		if len(r.Groups) > 0 {
			groups = fmt.Sprintf("%v", r.Groups)
		}
		fmt.Fprintf(w, "%s%d%s\t%s%s%s\t%s%s%s\t%s\t%s\n",
			ColorCyan, r.ID, ColorReset,
			ColorBlue, r.Name, ColorReset,
			ColorPurple, r.Branch, ColorReset,
			r.Path, groups)
	}
	w.Flush()
}

func PrintGroups(groups map[string][]int, allRepos []config.Repository) {
	if len(groups) == 0 {
		fmt.Println(i18n.T("no_groups"))
		return
	}

	repoMap := make(map[int]config.Repository)
	for _, r := range allRepos {
		repoMap[r.ID] = r
	}

	for name, ids := range groups {
		fmt.Printf("\n%s%s%s (%s%d%s):\n", ColorCyan, name, ColorReset, ColorCyan, len(ids), ColorReset)
		if len(ids) == 0 {
			fmt.Println("  " + i18n.T("empty"))
		} else {
			for _, id := range ids {
				if repo, ok := repoMap[id]; ok {
					fmt.Printf("  - %s%d%s %s%s%s (%s) %s%s%s\n",
						ColorCyan, id, ColorReset,
						ColorBlue, repo.Name, ColorReset,
						repo.Path,
						ColorPurple, repo.Branch, ColorReset)
				} else {
					fmt.Printf("  - %s%d%s %s%s%s\n",
						ColorCyan, id, ColorReset,
						ColorRed, i18n.T("not_found"), ColorReset)
				}
			}
		}
	}
}

func PrintGroupDetails(name string, repos []config.Repository) {
	fmt.Printf("\n%s%s%s (%s%d%s):\n", ColorCyan, name, ColorReset, ColorCyan, len(repos), ColorReset)
	if len(repos) == 0 {
		fmt.Println("  " + i18n.T("empty"))
	} else {
		for _, r := range repos {
			fmt.Printf("  - %s%d%s %s%s%s (%s) %s%s%s\n",
				ColorCyan, r.ID, ColorReset,
				ColorBlue, r.Name, ColorReset,
				r.Path,
				ColorPurple, r.Branch, ColorReset)
		}
	}
}

func PrintGitResult(result git.ExecutionResult) {
	if result.Success {
		fmt.Printf("[%s%s%s]   %s\n", ColorGreen, i18n.T("ok"), ColorReset, result.RepoPath)
		if result.Output != "" {
			fmt.Printf("%s\n", result.Output)
		}
	} else {
		fmt.Printf("[%s%s%s] %s: %s\n", ColorRed, i18n.T("fail"), ColorReset, result.RepoPath, i18n.T("git_cmd_failed"))
		if result.Error != nil {
			fmt.Printf("       %s: %s%v%s\n", i18n.T("error"), ColorRed, result.Error, ColorReset)
		}
		if result.Output != "" {
			fmt.Printf("       %s: %s\n", i18n.T("output"), result.Output)
		}
	}
}

func PrintSummary(successCount, failCount int) {
	fmt.Printf("\n%s%s:%s %s%d%s %s, %s%d%s %s\n",
		ColorCyan, i18n.T("summary"), ColorReset,
		ColorRed, failCount, ColorReset, i18n.T("failed"),
		ColorGreen, successCount, ColorReset, i18n.T("succeeded"))
}

func PrintError(err error) {
	fmt.Printf("%s%s:%s %v\n", ColorRed, i18n.T("error_prefix"), ColorReset, err)
}

func PrintSuccess(message string) {
	fmt.Printf("%s%s:%s %s\n", ColorGreen, i18n.T("success_prefix"), ColorReset, message)
}

func PrintMessage(message string) {
	fmt.Println(message)
}

func PrintNoConfigPrompt() {
	fmt.Printf("%s%s.%s\n", ColorYellow, i18n.T("no_config_found"), ColorReset)
	fmt.Println(i18n.T("please_run_setup"))
	fmt.Printf("  %sgits config scan [path]%s     - %s\n", ColorCyan, ColorReset, i18n.T("scan_desc"))
	fmt.Printf("  %sgits config add <path>%s      - %s\n", ColorCyan, ColorReset, i18n.T("add_desc"))
}

func PrintConfigPath(path string) {
	fmt.Printf("%s%s:%s %s\n", ColorCyan, i18n.T("config_file"), ColorReset, path)
}

func PrintHelp(w io.Writer) {
	fmt.Fprintln(w, `gits - `+i18n.T("config_cmd")+`

`+i18n.T("usage")+`:
  gits [`+i18n.T("global_options")+`] <command> [arguments...]

`+i18n.T("global_options")+`:
  -v, --version      `+i18n.T("version")+`
  -h, --help         `+i18n.T("help")+`
  -G, --group       `+i18n.T("execute_on_group")+`
  -r, --repo        `+i18n.T("execute_on_repos")+`
  -p, --parallel     `+i18n.T("execute_parallel")+`
  -y, --yes          `+i18n.T("skip_confirm")+`

`+i18n.T("commands")+`:
  config             `+i18n.T("config_cmd")+`
  config scan        `+i18n.T("config_scan_cmd")+`
  <git-command>      `+i18n.T("git_cmd_desc")+`

`+i18n.T("use_help")+`:`)
}

func PrintReviewPrompt(repoNames []string, command string, args []string) bool {
	fmt.Printf("\n%s%s:%s\n", ColorYellow, i18n.T("review_before_exec"), ColorReset)
	fmt.Printf("%s: %s%s %s%s\n", i18n.T("command"), ColorCyan, command, ColorReset, args)
	fmt.Printf("%s (%d):\n", i18n.T("repositories"), len(repoNames))
	for _, name := range repoNames {
		fmt.Printf("  - %s%s%s\n", ColorBlue, name, ColorReset)
	}
	fmt.Printf("\n%s%s? [y/N]:%s ", ColorYellow, i18n.T("proceed_prompt"), ColorReset)

	var response string
	fmt.Scanln(&response)

	return response == "y" || response == "Y" || response == "yes" || response == "Yes"
}
