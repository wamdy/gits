package i18n

import (
	"fmt"
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
		"remove_repo":           "移除仓库",
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
		"set_language":          "设置语言",
		"invalid_language":      "无效的语言：%s（必须是 'zh' 或 'en'）",
		"repo_and_group_conflict": "--group 和 --repo 不能同时使用",
		"invalid_repo_id":       "无效的仓库 ID：%d",
		"new":                   "新",
		"registered":            "已注册",
		"no_git_repos_found":    "未找到 Git 仓库",
		"path":                  "路径",
		"branch":                "分支",
		"status":                "状态",
		"clean":                 "干净",
		"id":                    "ID",
		"name":                  "名称",
		"groups":                "分组",
		"none":                  "无",
		"ok":                    "OK",
		"fail":                  "失败",
		"git_cmd_failed":        "git 命令失败",
		"error":                 "错误",
		"output":                "输出",
		"review":                "确认",
		"cancelled":             "已取消",
		"no_registered_repos":   "没有已注册的仓库",
		"no_git_repos":          "未找到 Git 仓库",
		"all_repos_registered":  "所有仓库已注册",
		"select_repos_prompt":   "选择要添加的仓库（例如 1,3,5 或 1-5 或 all）",
		"no_selection":          "未选择仓库",
		"invalid_selection":     "无效的选择",
		"skipped_already":       "已跳过：%s（已注册）",
		"skipped_error":         "已跳过：%s（%s）",
		"added_repo":            "已添加：%s",
		"summary_added_skipped": "汇总：%d 已添加，%d 已跳过",
		"group_not_found":       "分组未找到",
		"repo_not_found":        "仓库未找到",
		"repo_already_exists":   "仓库已存在",
		"repo_name_exists":      "仓库名称 '%s' 已存在",
		"repo_id_exists":        "仓库 ID %d 已存在",
		"group_already_exists":  "分组已存在",
		"repo_already_in_group": "仓库已在分组中",
		"ids_must_positive":     "ID 必须为正数",
		"same_ids":              "旧 ID 和新 ID 相同",
		"all_ids_reassigned":    "所有仓库 ID 已重新分配",
		"repo_id_reassigned":    "仓库 ID 重新分配成功",
		"repo_renamed":          "仓库重命名成功",
		"config_path":           "配置文件路径",
		"create_group_prompt":   "分组 '%s' 不存在。是否创建？[y/N]：",
		"operation":             "操作",
		"yes_no":                "[y/N]",
		"version":               "版本",
		"help":                  "帮助",
		"usage":                 "用法",
		"global_options":        "全局选项",
		"commands":              "命令",
		"config_cmd":            "管理配置",
		"config_scan_cmd":       "扫描并添加仓库",
		"git_cmd_desc":          "在所有已注册仓库上执行 git 命令",
		"use_help":              "使用 \"gits <command> --help\" 获取更多信息",
		"repo_ref_help":         "通过 ID、名称或路径",
		"auto_create_group":     "自动创建分组",
		"display_raw_json":      "显示原始 JSON 配置",
		"specify_custom_name":   "指定自定义名称",
		"specify_custom_id":     "指定自定义 ID",
		"reassign_all_ids":      "重新分配所有 ID",
		"execute_on_group":      "在指定分组上执行命令",
		"execute_on_repos":      "在指定仓库上执行命令（例如 1,3,5 或 1-5）",
		"execute_parallel":      "并行执行命令",
		"skip_confirm":          "跳过执行前确认",
		"use_global_config":     "使用全局配置",
		"manage_multi_repos":    "管理多个 Git 仓库",
		"manage_config_detail":  "管理仓库和分组配置",
		"config_show":           "显示配置",
		"manage_groups":         "管理分组",
		"show_groups":           "显示分组",
		"cmd_reid":              "重新分配仓库 ID",
		"cmd_remove":            "移除仓库",
		"cmd_rename":            "重命名仓库",
		"cmd_validate":          "验证并清理配置",
		"cmd_add":               "添加仓库",
		"cmd_scan":              "扫描并添加仓库",
		"cmd_language":          "设置语言",
		"cmd_group_add":         "添加分组",
		"cmd_group_remove":      "移除分组",
		"cmd_group_assign":      "分配仓库到分组",
		"cmd_group_unassign":    "从分组移除仓库",
		"cmd_group_show":        "显示分组",
		"wildcard_no_match":     "通配符未匹配到任何仓库",
		"color_desc":            "颜色输出模式（auto|always|never）",
		"no_color_desc":         "禁用颜色输出（等价于 --color=never）",
		"repo_selection_note":   "在指定仓库上执行命令（支持 ID 列表、名称通配如 repo*、或混合使用）",
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
		"remove_repo":           "Remove repository",
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
		"set_language":          "Set language",
		"invalid_language":      "Invalid language: %s (must be 'zh' or 'en')",
		"repo_and_group_conflict": "--group and --repo cannot be used together",
		"invalid_repo_id":       "Invalid repository ID: %d",
		"new":                   "new",
		"registered":            "registered",
		"no_git_repos_found":    "No Git repositories found",
		"path":                  "Path",
		"branch":                "Branch",
		"status":                "Status",
		"clean":                 "clean",
		"id":                    "ID",
		"name":                  "NAME",
		"groups":                "GROUPS",
		"none":                  "none",
		"ok":                    "OK",
		"fail":                  "FAIL",
		"git_cmd_failed":        "git command failed",
		"error":                 "Error",
		"output":                "Output",
		"review":                "Review",
		"cancelled":             "cancelled",
		"no_registered_repos":   "No registered repositories",
		"no_git_repos":          "No Git repositories found",
		"all_repos_registered":  "All repositories are already registered",
		"select_repos_prompt":   "Select repositories to add (e.g. 1,3,5 or 1-5 or all)",
		"no_selection":          "No repositories selected",
		"invalid_selection":     "Invalid selection",
		"skipped_already":       "Skipped: %s (already registered)",
		"skipped_error":         "Skipped: %s (%s)",
		"added_repo":            "Added: %s",
		"summary_added_skipped": "Summary: %d added, %d skipped",
		"group_not_found":       "group not found",
		"repo_not_found":        "repository not found",
		"repo_already_exists":   "repository already exists",
		"repo_name_exists":      "repository name '%s' already exists",
		"repo_id_exists":        "repository ID %d already exists",
		"group_already_exists":  "group already exists",
		"repo_already_in_group": "repository already in group",
		"ids_must_positive":     "ID must be positive",
		"same_ids":              "old ID and new ID are the same",
		"all_ids_reassigned":    "All repository IDs reassigned sequentially starting from 1",
		"repo_id_reassigned":    "Repository ID reassigned successfully",
		"repo_renamed":          "Repository renamed successfully",
		"config_path":           "Config file",
		"create_group_prompt":   "Group '%s' does not exist. Create it? [y/N]: ",
		"operation":             "Operation",
		"yes_no":                "[y/N]",
		"version":               "Version",
		"help":                  "Help",
		"usage":                 "Usage",
		"global_options":        "Global Options",
		"commands":              "Commands",
		"config_cmd":            "Manage configuration",
		"config_scan_cmd":       "Scan and add repositories",
		"git_cmd_desc":          "Execute git command on all registered repositories",
		"use_help":              "Use \"gits <command> --help\" for more information",
		"repo_ref_help":         "by ID, name, or path",
		"auto_create_group":     "Auto-create group",
		"display_raw_json":      "Display raw JSON configuration",
		"specify_custom_name":   "Specify a custom name",
		"specify_custom_id":     "Specify a custom ID",
		"reassign_all_ids":      "Reassign all IDs sequentially starting from 1",
		"execute_on_group":      "Execute command on specified group",
		"execute_on_repos":      "Execute command on specified repositories by ID (e.g. 1,3,5 or 1-5)",
		"execute_parallel":      "Execute commands in parallel",
		"skip_confirm":          "Skip review confirmation before execution",
		"use_global_config":     "Use global configuration",
		"manage_multi_repos":    "Manage multiple Git repositories",
		"manage_config_detail":  "Manage repositories and groups configuration",
		"config_show":           "Show configuration",
		"manage_groups":         "Manage groups",
		"show_groups":           "Show groups",
		"cmd_reid":              "Reassign repository ID",
		"cmd_remove":            "Remove repository",
		"cmd_rename":            "Rename repository",
		"cmd_validate":          "Validate and clean configuration",
		"cmd_add":               "Add repository",
		"cmd_scan":              "Scan and add repositories",
		"cmd_language":          "Set language",
		"cmd_group_add":         "Add group",
		"cmd_group_remove":      "Remove group",
		"cmd_group_assign":      "Assign repository to group",
		"cmd_group_unassign":    "Unassign repository from group",
		"cmd_group_show":        "Show groups",
		"wildcard_no_match":     "No repositories matched by wildcard",
		"color_desc":            "Color output mode (auto|always|never)",
		"no_color_desc":         "Disable color output (equivalent to --color=never)",
		"repo_selection_note":   "Execute command on specified repositories (ID list, name wildcard like repo*, or mixed)",
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
	// 优先检查 LANG / LC_ALL 环境变量（Unix/Linux/macOS）
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = os.Getenv("LC_ALL")
	}
	if strings.Contains(strings.ToLower(lang), "zh") {
		return Zh
	}

	// Windows 系统语言检测
	if os.Getenv("OS") == "Windows_NT" {
		// 检查 Windows 区域设置环境变量
		for _, env := range []string{"USERPROFILE", "HOMEPATH"} {
			if val := os.Getenv(env); val != "" {
				// 中文 Windows 用户目录通常包含中文字符或特定路径
				if strings.Contains(val, "\u7528\u6237") || strings.Contains(val, "Users") {
					// 进一步检查系统代码页
					if cp := os.Getenv("CODEPAGE"); cp != "" && strings.HasPrefix(cp, "93") {
						return Zh
					}
				}
			}
		}
		// 默认中文（中国大陆用户为主）
		return Zh
	}

	return En
}

func CurrentLang() Lang {
	return currentLang
}
