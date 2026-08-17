# tools/gits — 多仓库 Git 批量操作 CLI

## 模块概览

`gits` 是独立 Go CLI 工具：对多个 Git 仓库执行统一的批量操作（status/pull/push/commit/...），支持仓库分组与按 ID 选择。

- **技术栈**：Go 1.22 / github.com/spf13/cobra v1.8.1；模块名 `github.com/wamdy/gits`
- **构建**：`go build -o gits .`（Windows 产物 `gits.exe`，仓库内已存编译产物）
- **配置文件**：当前工作目录 `.gits/config.json`（JSON，含 version/language/repositories/groups/defaults）

## 目录结构与职责

| 路径 | 职责 |
|------|------|
| `main.go` | 入口，调用 `cmd.Execute()` |
| `cmd/root.go` | 根命令 + 全局选项：`-g/--group`、`-r/--repo`（ID/区间/名称通配/名称）、`-p/--parallel`、`-y/--yes`、`--color=auto\|always\|never`、`--no-color`；i18n 初始化；启动时校验 `--color` 并调用 `ui.InitColorMode`；git 代理参数提取（`--` 分隔符） |
| `cmd/git.go` | Git 命令代理：仓库选择（`repo.ResolveSelection` 解析 `-r` / 分组 / 全部）、review 确认、串行/并行执行、汇总 |
| `cmd/config.go` | config 子命令：add/remove/show/rename/reid/scan/language/validate/group（add/assign/unassign/show） |
| `internal/config/loader.go` | `Config`/`Repository` 结构体、配置加载/保存/原始 JSON |
| `internal/config/manager.go` | 配置操作：仓库增删改查、分组管理、repo-ref 解析（ID/名称/路径）、ID 重排、校验清理 |
| `internal/git/executor.go` | git 命令执行封装（`exec.Command("git", ...)`）、`ExecutionResult`、branch/status/is-git 快捷方法 |
| `internal/i18n/i18n.go` | 中/英双语字典，`i18n.T(key, args...)` 取文案 |
| `internal/repo/discover.go` | 仓库发现：`Discover()` 目录遍历找 `.git`（跳过 9 种噪音目录）+ 8-worker pool 并发采集 branch/status；`GetRepositoryInfo` |
| `internal/repo/manager.go` | 仓库管理门面（转发到 config 包）+ `ResolveSelection`/`FindReposByWildcard`（ID/区间/通配/名称选择） |
| `internal/ui/printer.go` | 输出格式化：表格、分组、review 提示、**ANSI 颜色**（`ColorMode` 变量，`InitColorMode` 按终端能力置 ANSI 或空串） |
| `internal/ui/isvt_windows.go` / `isvt_other.go` | 平台差异的 `supportsVT()`：Windows 查 `ENABLE_VIRTUAL_TERMINAL_PROCESSING`；其余平台恒 true |

## 命名与约定

- **仓库引用（repo-ref）**：ID（纯数字）> 名称 > 路径，按优先级匹配
- **`-r/--repo`**：逗号分段选择，支持 ID 列表 `1,3,5` / ID 区间 `1-5` / 名称通配 `repo*`、`repo-???end` / 名称精确，混用按 ID 去重（`repo.ResolveSelection`，`path.Match` 实现通配）
- **i18n**：所有用户可见字符串必须经 `i18n.T()` 取文案，zh/en 双语成对维护
- **颜色**：集中在 `internal/ui/printer.go` 的 `Color*` 包级变量；`cmd/root.go` 启动时按 `--color`/`--no-color`/终端能力经 `InitColorMode` 统一置 ANSI 码或空串（56 处调用点引用变量，零逐点改动）；auto 模式尊重 `NO_COLOR` 与非 TTY 检测
- **命令代理**：git 命令透传，`--` 分隔 gits 参数与 git 参数；执行前默认 review 确认（`-y` 跳过）
- **输出风格**：成功 `[OK]`、失败 `[FAIL]`、结尾 `Summary: N failed, N succeeded`

## 测试

- 内置 `test-repos/`（repo1/repo2/repo3，均为 git 仓库）用于手工验证多仓库操作
- Go 单测：`internal/repo`（`ResolveSelection`/`FindReposByWildcard`/`isNoiseDir`）、`internal/ui`（`InitColorMode`）；运行 `go test ./...`
