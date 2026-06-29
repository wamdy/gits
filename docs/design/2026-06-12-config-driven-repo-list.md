# gits 配置驱动仓库列表设计文档

## 1. 背景与问题

当前 `gits` 执行 Git 命令时，无论是否存在配置文件，都会扫描当前目录下的所有 Git 仓库（通过 `repo.ListRepositories()` → `Discover(".")`）。这导致：

- 有配置文件时，仍然扫描目录，行为不符合用户预期
- 无配置文件时，默默扫描并执行，用户可能不知道自己操作的是哪些仓库
- 配置文件的仓库列表和实际扫描结果可能不一致，造成混乱

## 2. 需求目标

1. **无配置时提示**：没有配置文件或配置中无仓库时，提示用户执行 `scan` 或 `add` 操作
2. **有配置时直接使用**：有配置文件且仓库列表非空时，直接使用配置信息，不再扫描目录
3. **不自动 validate**：不在执行命令前自动执行 validate（用户已确认去掉此功能）

## 3. 方案设计

### 3.1 核心改动点

#### 3.1.1 `cmd/git.go` - `runGitCommand` 函数

在函数开头增加配置检查和仓库来源判断逻辑：

```go
func runGitCommand(args []string) {
    var repos []repo.RepositoryInfo
    var err error

    groupName := GetGroup()
    
    // 检查配置是否存在
    cfg, err := config.Load()
    if err != nil {
        ui.PrintError(err)
        return
    }
    
    // 无配置或仓库列表为空时提示用户
    if len(cfg.Repositories) == 0 {
        ui.PrintNoConfigPrompt()
        return
    }
    
    if groupName != "" {
        // 使用分组配置
        configRepos, err := repo.GetRepositoriesByGroup(groupName)
        if err != nil {
            ui.PrintError(err)
            return
        }
        for _, r := range configRepos {
            info, err := repo.GetRepositoryInfo(r.Path)
            if err != nil {
                ui.PrintError(err)
                continue
            }
            repos = append(repos, info)
        }
    } else {
        // 使用配置中的全部仓库（不再扫描目录）
        registered, err := repo.GetRegisteredRepositories()
        if err != nil {
            ui.PrintError(err)
            return
        }
        for _, r := range registered {
            info, err := repo.GetRepositoryInfo(r.Path)
            if err != nil {
                ui.PrintError(err)
                continue
            }
            repos = append(repos, info)
        }
    }

    if len(repos) == 0 {
        ui.PrintMessage("No repositories found.")
        return
    }

    // ... 后续逻辑不变
}
```

#### 3.1.2 `internal/config/loader.go` - 新增 `ConfigExists` 函数

```go
func ConfigExists() bool {
    _, err := os.Stat(configPath)
    return !os.IsNotExist(err)
}
```

#### 3.1.3 `internal/ui/printer.go` - 新增提示函数

```go
func PrintNoConfigPrompt() {
    fmt.Printf("%sNo configuration found.%s\n", ColorYellow, ColorReset)
    fmt.Println("Please run one of the following commands to set up repositories:")
    fmt.Printf("  %sgits config scan [path]%s     - Scan directory for Git repositories\n", ColorCyan, ColorReset)
    fmt.Printf("  %sgits config add <path>%s      - Add a specific repository\n", ColorCyan, ColorReset)
}
```

### 3.2 执行流程图

```
用户执行 gits <command>
        |
        v
+-------------------+
|  加载配置文件      |
+-------------------+
        |
        v
+-------------------+
| 仓库列表为空？     |
+-------------------+
    |           |
   是          否
    |           |
    v           v
+--------+   +-------------------+
| 提示用户 |   | 使用配置中的仓库   |
| scan/add |   | （不再扫描目录）   |
+--------+   +-------------------+
                    |
                    v
            +-------------------+
            | 按原有逻辑执行命令  |
            +-------------------+
```

## 4. 影响范围

| 文件 | 改动类型 | 说明 |
|------|----------|------|
| `cmd/git.go` | 修改 | `runGitCommand` 增加配置检查和仓库来源判断 |
| `internal/config/loader.go` | 新增 | 增加 `ConfigExists()` 辅助函数 |
| `internal/ui/printer.go` | 新增 | 增加 `PrintNoConfigPrompt()` 提示函数 |

## 5. 兼容性说明

- **向后兼容**：有配置文件的用户无感知，只是不再扫描目录
- **行为变更**：无配置文件时从"自动扫描"变为"提示用户配置"
- **分组操作**：`--group` 参数仍然正常工作，从配置中读取分组仓库

## 6. 测试建议

1. **无配置场景**：删除 `.gits/config.json`，执行 `gits status`，应看到提示信息
2. **空配置场景**：保留配置文件但清空 `repositories` 数组，执行 `gits status`，应看到提示信息
3. **有配置场景**：正常配置文件，执行 `gits status`，应直接使用配置中的仓库
4. **分组场景**：使用 `--group` 参数，应从配置中读取对应分组的仓库
