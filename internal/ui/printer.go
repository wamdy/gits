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
	// 无登记列表可反查时，主仓展示名按路径 base 名兜底（同一标记模板，口径不变）。
	mainName := repo.MainRepoName(info.WorktreeOf, nil)
	if info.ID > 0 {
		fmt.Printf("\n%s[%d]%s %s%s%s%s\n", ColorCyan, info.ID, ColorReset,
			ColorBlue, info.Name, ColorReset, repo.WorktreeMark(mainName))
	} else {
		fmt.Printf("\n%s%s%s%s\n", ColorBlue, info.Name, ColorReset, repo.WorktreeMark(mainName))
	}
	fmt.Printf("  %s: %s\n", i18n.T("path"), info.Path)
	fmt.Printf("  %s: %s%s%s\n", i18n.T("branch"), ColorPurple, info.Branch, ColorReset)
	if info.WorktreeOf != "" {
		fmt.Printf("  %s: %s\n", i18n.T("worktree_of"), info.WorktreeOf)
	}
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
		// worktree 归属标记与 group show / scan / 结果行同一口径（repo.WorktreeMark）。
		mark := repo.WorktreeMark(repo.WorktreeMainName(r.Path, repos))
		fmt.Fprintf(w, "%s%d%s\t%s%s%s%s\t%s%s%s\t%s\t%s\n",
			ColorCyan, r.ID, ColorReset,
			ColorBlue, r.Name, ColorReset, mark,
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
				if r, ok := repoMap[id]; ok {
					fmt.Printf("  - %s%d%s %s%s%s%s (%s) %s%s%s\n",
						ColorCyan, id, ColorReset,
						ColorBlue, r.Name, ColorReset,
						repo.WorktreeMark(repo.WorktreeMainName(r.Path, allRepos)),
						r.Path,
						ColorPurple, r.Branch, ColorReset)
				} else {
					fmt.Printf("  - %s%d%s %s%s%s\n",
						ColorCyan, id, ColorReset,
						ColorRed, i18n.T("not_found"), ColorReset)
				}
			}
		}
	}
}

// PrintGroupDetails 输出单个分组的成员（config group show <组>）。
// allRepos 传**全量登记表**而非组内成员：worktree 归属要反查主仓条目，主仓常不在该组里，
// 只在组内反查会退化成目录 Base 名，与 config show 的标注不一致（AC-03.7，评审 Q-01）。
func PrintGroupDetails(name string, repos []config.Repository, allRepos []config.Repository) {
	fmt.Printf("\n%s%s%s (%s%d%s):\n", ColorCyan, name, ColorReset, ColorCyan, len(repos), ColorReset)
	if len(repos) == 0 {
		fmt.Println("  " + i18n.T("empty"))
	} else {
		for _, r := range repos {
			fmt.Printf("  - %s%d%s %s%s%s%s (%s) %s%s%s\n",
				ColorCyan, r.ID, ColorReset,
				ColorBlue, r.Name, ColorReset,
				repo.WorktreeMark(repo.WorktreeMainName(r.Path, allRepos)),
				r.Path,
				ColorPurple, r.Branch, ColorReset)
		}
	}
}

// PrintScanCandidate 输出 config scan 的一条候选行。
// 名称段取自 repo 列出家族（DisplayBranch + DisplayLabelWithWorktree → `名称 (分支) ← 主仓`），
// 与 config show / group show / review 提示、逐仓结果行同一口径，cmd 层不再各拼一套字符串。
// registeredID > 0 表示该路径已登记（附配置 ID 与「已注册」状态）；主仓展示名在 allRepos 中反查。
func PrintScanCandidate(index int, info repo.RepositoryInfo, registeredID int, allRepos []config.Repository) {
	status := i18n.T("new")
	statusColor := ColorReset
	nameColor := ColorBlue
	idPrefix := ""
	if registeredID > 0 {
		status = i18n.T("registered")
		statusColor = ColorGreen
		nameColor = ColorReset
		idPrefix = fmt.Sprintf("%s[%d]%s ", ColorCyan, registeredID, ColorReset)
	}
	// 名称段走列出家族 DisplayLabelWithWorktree：`名称 (分支) ← 主仓`，与 show/group/结果行同函数。
	label := repo.DisplayLabelWithWorktree(0, info.Name,
		repo.DisplayBranch(info.Branch, info.Path),
		repo.MainRepoName(info.WorktreeOf, allRepos))
	fmt.Printf("  %s#%d%s %s%s%s%s (%s) %s[%s]%s\n",
		ColorYellow, index+1, ColorReset,
		idPrefix,
		nameColor, label, ColorReset,
		info.Path,
		statusColor, status, ColorReset)
}

// PrintWorktreeSets 输出 config show 的「worktree 集合」块（紧跟配置项块之后）。
// 无集合时整块不输出，保持既有界面不噪。
func PrintWorktreeSets(sets []config.WorktreeSet, allRepos []config.Repository) {
	if len(sets) == 0 {
		return
	}
	repoByID := repo.IndexByID(allRepos)

	fmt.Printf("\n%s%s%s:\n", ColorCyan, i18n.T("worktree_sets"), ColorReset)
	for _, s := range sets {
		fmt.Printf("  %s%s%s (%s: %s%s%s, %s):\n",
			ColorBlue, s.Name, ColorReset,
			i18n.T("branch"), ColorPurple, s.Branch, ColorReset,
			i18n.T("worktree_set_members", len(s.Members)))
		if len(s.Members) == 0 {
			fmt.Println("    " + i18n.T("empty"))
			continue
		}
		for _, m := range s.Members {
			// 成员标签沿用 DisplayLabel 家族（`[ID] 名称`），路径跟在标签之后。
			fmt.Printf("    - %s%s%s  %s\n", ColorBlue, repo.DisplayLabel(m.RepoID, repo.MemberLabel(m, repoByID), ""), ColorReset, m.Path)
		}
	}
}

// PrintWorktreeSetList 输出 `worktree list` 的集合明细：
// 每行 `[ID] 名称 (检出分支) 路径`，分支取成员 worktree 的实时检出（repo.DisplayBranch 口径）；
// 成员目录已不存在时分支位显示 missing 标记（AC-03.2），名称仍优先取登记仓库名。
func PrintWorktreeSetList(sets []config.WorktreeSet, allRepos []config.Repository) {
	if len(sets) == 0 {
		fmt.Println(i18n.T("worktree_no_sets"))
		return
	}
	repoByID := repo.IndexByID(allRepos)

	for _, s := range sets {
		fmt.Printf("\n%s%s%s (%s: %s%s%s, %s):\n",
			ColorCyan, s.Name, ColorReset,
			i18n.T("branch"), ColorPurple, s.Branch, ColorReset,
			i18n.T("worktree_set_members", len(s.Members)))
		if len(s.Members) == 0 {
			fmt.Println("  " + i18n.T("empty"))
			continue
		}
		for _, m := range s.Members {
			branch := i18n.T("worktree_missing")
			color := ColorYellow
			if info, err := os.Stat(m.Path); err == nil && info.IsDir() {
				branch = repo.DisplayBranch(git.GetBranchName(m.Path), m.Path)
				color = ColorBlue
			}
			fmt.Printf("  - %s%s%s  %s\n", color, repo.DisplayLabel(m.RepoID, repo.MemberLabel(m, repoByID), branch), ColorReset, m.Path)
		}
	}
}

// printWorktreeReview 输出 worktree 命令族执行前的 review 确认（成员标签 + 目标路径），
// 取向与 PrintReviewPrompt 一致：列出受影响成员后读一行 y/N。颜色只用本文件的 Color* 变量。
func printWorktreeReview(command string, meta [][2]string, labels, paths []string) bool {
	fmt.Printf("\n%s%s:%s\n", ColorYellow, i18n.T("review_before_exec"), ColorReset)
	fmt.Printf("%s: %s%s%s\n", i18n.T("command"), ColorCyan, command, ColorReset)
	for _, kv := range meta {
		if kv[1] != "" {
			fmt.Printf("%s: %s\n", kv[0], kv[1])
		}
	}
	fmt.Printf("%s (%d):\n", i18n.T("repositories"), len(labels))
	for i, label := range labels {
		target := ""
		if i < len(paths) {
			target = "  " + paths[i]
		}
		fmt.Printf("  - %s%s%s%s\n", ColorBlue, label, ColorReset, target)
	}
	// proceed_prompt 文案自带问号，此处不再补 "?"（避免出现 `是否继续？? [y/N]`）。
	fmt.Printf("\n%s%s [y/N]:%s ", ColorYellow, i18n.T("proceed_prompt"), ColorReset)

	var response string
	fmt.Scanln(&response)
	return response == "y" || response == "Y" || response == "yes" || response == "Yes"
}

// PrintWorktreeAddReview 创建 worktree 集合前的 review：列出集合名、分支、根目录与各成员目标路径。
func PrintWorktreeAddReview(setName, branch, rootDir string, labels, paths []string) bool {
	return printWorktreeReview(fmt.Sprintf("worktree add %s", setName), [][2]string{
		{i18n.T("branch"), branch},
		{i18n.T("worktree_root_dir"), rootDir},
	}, labels, paths)
}

// PrintWorktreeRemoveReview 拆除 worktree 集合前的 review：列出成员与其路径（force 时附警告）。
func PrintWorktreeRemoveReview(setName string, labels, paths []string, force bool) bool {
	meta := [][2]string{}
	if force {
		meta = append(meta, [2]string{i18n.T("warning"), i18n.T("worktree_remove_force_warn")})
	}
	return printWorktreeReview(fmt.Sprintf("worktree remove %s", setName), meta, labels, paths)
}

// PrintGitResult 打印单个仓库的命令执行结果。
// name 为 `[ID] 名称 (分支) ← 主仓` 回显标签（repo.DisplayLabelWithWorktree 口径），前置于仓库路径；
// name 为空（调用方未提供标签）时仅显示路径。
func PrintGitResult(result git.ExecutionResult, name string) {
	label := ""
	if name != "" {
		label = fmt.Sprintf("%s%s%s ", ColorBlue, name, ColorReset)
	}
	if result.Success {
		fmt.Printf("[%s%s%s]   %s%s\n", ColorGreen, i18n.T("ok"), ColorReset, label, result.RepoPath)
		if result.Output != "" {
			fmt.Printf("%s\n", result.Output)
		}
	} else {
		fmt.Printf("[%s%s%s] %s%s: %s\n", ColorRed, i18n.T("fail"), ColorReset,
			label, result.RepoPath, i18n.T("git_cmd_failed"))
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

// PrintConfigSettings 打印配置项与默认值（version/language/defaults），
// 让用户不查看 raw JSON 也能知道 config.json 有哪些全局配置项。
func PrintConfigSettings(cfg *config.Config) {
	language := cfg.Language
	if language == "" {
		language = i18n.T("not_set")
	}
	fmt.Printf("\n%s%s%s:\n", ColorCyan, i18n.T("config_settings"), ColorReset)
	fmt.Printf("  %s: %s\n", i18n.T("version"), cfg.Version)
	fmt.Printf("  %s: %s\n", i18n.T("language"), language)
	fmt.Printf("  %s: %s\n", i18n.T("default_remote"), cfg.Defaults.Remote)
	fmt.Printf("  %s: %s\n", i18n.T("default_branch"), cfg.Defaults.Branch)
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
