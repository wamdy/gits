# gits 多语言、scan 标识优化、仓库 ID 选择设计文档

## 1. 多语言支持 (i18n)

### 1.1 需求
- 支持中文和英文
- 语言优先级：config 配置 > 系统语言检测 > 默认中文
- 通过 `gits config language <zh|en>` 设置语言

### 1.2 设计方案

#### 新增 `internal/i18n/i18n.go`

```go
package i18n

import (
	"os"
	"strings"
)

type Lang string

const (
	Zh Lang = "zh"
	En Lang = "en"
)

var currentLang = Zh

var dict = map[Lang]map[string]string{
	Zh: {
		"no_config_found":       "未找到配置",
		"please_run_setup":      "请运行以下命令来设置仓库：",
		"scan_desc":             "扫描目录中的 Git 仓库",
		"add_desc":              "添加指定仓库",
		"review_before_exec":    "执行前确认",
		"command":               "命令",
		"repositories":          "仓库",
		"proceed_prompt":        "是否继续？",
		"operation_cancelled":   "操作已取消",
		"no_repositories":       "未找到仓库",
		"summary":               "汇总",
		"failed":                "失败",
		"succeeded":             "成功",
		"error_prefix":          "错误",
		"success_prefix":        "成功",
		"config_file":           "配置文件",
		"repo_added":            "仓库添加成功",
		"repo_removed":          "仓库移除成功",
		"group_created":         "分组创建成功",
		"group_removed":         "分组移除成功",
		"assigned_success":      "仓库分配到分组成功",
		"unassigned_success":    "仓库从分组移除成功",
		"config_valid":          "配置有效，未发现问题",
		"config_cleaned":        "配置已清理",
		"empty_groups_removed":  "个空分组已移除",
		"invalid_refs_removed":  "个无效引用已移除",
		"missing_repos_removed": "个缺失仓库已移除",
		"found_repos":           "发现 %d 个 Git 仓库",
		"select_repos":          "选择要添加的仓库（例如 1,3,5 或 1-5 或 all）",
		"all_registered":        "所有仓库已注册",
		"added":                 "已添加",
		"skipped":               "已跳过",
		"already_registered":    "已注册",
		"no_groups":             "未定义分组",
		"empty":                 "（空）",
		"not_found":             "未找到",
		"language_set":          "语言已设置为",
	},
	En: {
		"no_config_found":       "No configuration found",
		"please_run_setup":      "Please run one of the following commands to set up repositories:",
		"scan_desc":             "Scan directory for Git repositories",
		"add_desc":              "Add a specific repository",
		"review_before_exec":    "Review before execution",
		"command":               "Command",
		"repositories":          "Repositories",
		"proceed_prompt":        "Do you want to proceed?",
		"operation_cancelled":   "Operation cancelled",
		"no_repositories":       "No repositories found",
		"summary":               "Summary",
		"failed":                "failed",
		"succeeded":             "succeeded",
		"error_prefix":          "Error",
		"success_prefix":        "Success",
		"config_file":           "Config file",
		"repo_added":            "Repository added successfully",
		"repo_removed":          "Repository removed successfully",
		"group_created":         "Group created successfully",
		"group_removed":         "Group removed successfully",
		"assigned_success":      "Repository assigned to group successfully",
		"unassigned_success":    "Repository unassigned from group successfully",
		"config_valid":          "Configuration is valid, no issues found",
		"config_cleaned":        "Configuration cleaned",
		"empty_groups_removed":  "empty groups removed",
		"invalid_refs_removed":  "invalid references removed",
		"missing_repos_removed": "missing repositories removed",
		"found_repos":           "Found %d Git repositories",
		"select_repos":          "Select repositories to add (e.g. 1,3,5 or 1-5 or all)",
		"all_registered":        "All repositories are already registered",
		"added":                 "Added",
		"skipped":               "Skipped",
		"already_registered":    "already registered",
		"no_groups":             "No groups defined",
		"empty":                 "(empty)",
		"not_found":             "not found",
		"language_set":          "Language set to",
	},
}

func SetLang(lang Lang) {
	currentLang = lang
}

func T(key string, args ...interface{}) string {
	msg, ok := dict[currentLang][key]
	if !ok {
		msg = dict[En][key]
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

func DetectSystemLang() Lang {
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = os.Getenv("LC_ALL")
	}
	if strings.Contains(strings.ToLower(lang), "zh") {
		return Zh
	}
	return En
}

func CurrentLang() Lang {
	return currentLang
}
```

#### 配置结构更新

```go
type Config struct {
	Version      string           `json:"version"`
	Language     string           `json:"language"`  // 新增
	Repositories []Repository     `json:"repositories"`
	Groups       map[string][]int `json:"groups"`
	Defaults     Defaults         `json:"defaults"`
}
```

#### 初始化逻辑

在 `cmd/root.go` 或 `main.go` 中：

```go
func initI18n() {
	cfg, _ := config.Load()
	if cfg.Language != "" {
		i18n.SetLang(i18n.Lang(cfg.Language))
	} else {
		i18n.SetLang(i18n.DetectSystemLang())
	}
}
```

### 1.3 新增 `config language` 子命令

```go
var configLanguageCmd = &cobra.Command{
	Use:   "language <zh|en>",
	Short: i18n.T("set_language"),
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		lang := args[0]
		if lang != "zh" && lang != "en" {
			ui.PrintError(fmt.Errorf("invalid language: %s (must be 'zh' or 'en')", lang))
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
```

---

## 2. config scan 标识已添加仓库

### 2.1 当前行为
已显示 `[new]` / `[registered]`，但颜色未区分。

### 2.2 改进方案

- `[registered]` 用 **绿色** 高亮
- `[new]` 保持默认或灰色
- 已注册仓库名称也用灰色显示，突出新仓库

```go
for i, r := range discovered {
    status := "new"
    statusColor := ui.ColorReset
    nameColor := ui.ColorBlue
    if registeredPaths[r.Path] {
        status = "registered"
        statusColor = ui.ColorGreen
        nameColor = ui.ColorReset  // 灰色
    }
    fmt.Printf("  %s[%d]%s %s%s%s (%s) %s[%s]%s\n",
        ui.ColorCyan, i+1, ui.ColorReset,
        nameColor, r.Name, ui.ColorReset,
        r.Path, statusColor, status, ui.ColorReset)
}
```

---

## 3. 按仓库 ID 选择多个仓库

### 3.1 新增全局参数

```go
var repoIDs string

rootCmd.PersistentFlags().StringVarP(&repoIDs, "repo", "r", "", "Execute command on specified repositories by ID (e.g. 1,3,5 or 1-5)")
```

### 3.2 与 `--group` 互斥

```go
func runGitCommand(args []string) {
    if groupName != "" && repoIDs != "" {
        ui.PrintError(errors.New("--group and --repo cannot be used together"))
        return
    }
    // ...
}
```

### 3.3 解析 ID 列表

```go
func parseRepoIDs(input string, maxID int) []int {
    var result []int
    parts := strings.Split(input, ",")
    for _, part := range parts {
        part = strings.TrimSpace(part)
        if strings.Contains(part, "-") {
            rangeParts := strings.SplitN(part, "-", 2)
            start, _ := strconv.Atoi(rangeParts[0])
            end, _ := strconv.Atoi(rangeParts[1])
            for i := start; i <= end && i <= maxID; i++ {
                result = append(result, i)
            }
        } else {
            id, _ := strconv.Atoi(part)
            if id > 0 && id <= maxID {
                result = append(result, id)
            }
        }
    }
    return result
}
```

### 3.4 在 `runGitCommand` 中处理

```go
if repoIDs != "" {
    // 解析 ID 列表
    ids := parseRepoIDs(repoIDs, maxRepoID)
    for _, id := range ids {
        for _, r := range cfg.Repositories {
            if r.ID == id {
                info, _ := repo.GetRepositoryInfo(r.Path)
                repos = append(repos, info)
                break
            }
        }
    }
}
```

---

## 4. 影响范围

| 文件 | 改动 |
|------|------|
| `internal/i18n/i18n.go` | 新增 i18n 模块 |
| `internal/config/loader.go` | Config 结构体新增 Language 字段 |
| `cmd/root.go` | 新增 `--repo` 参数，初始化 i18n |
| `cmd/git.go` | 处理 `--repo` 参数，与 `--group` 互斥检查 |
| `cmd/config.go` | 新增 `language` 子命令，scan 标识颜色优化 |
| `internal/ui/printer.go` | 所有输出字符串改为 `i18n.T()` 调用 |

## 5. 使用示例

```bash
# 设置语言
gits config language zh
gits config language en

# 按仓库 ID 选择
gits -r 1,3,5 -- status
gits -r 1-5 -- pull
gits -r 2 -- push

# scan 时标识已注册仓库（绿色 [registered]）
gits config scan
```
