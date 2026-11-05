# tools/gits — 多仓库 Git 批量操作 CLI

## 模块概览

`gits` 是独立 Go CLI 工具：对多个 Git 仓库执行统一的批量操作（status/pull/push/commit/...），支持仓库分组与按 ID 选择。

- **技术栈**：Go 1.22 / github.com/spf13/cobra v1.8.1；模块名 `github.com/wamdy/gits`
- **构建**：`go build -o gits .`（Windows 产物 `gits.exe`，仓库内已存编译产物）
- **配置文件**：从当前工作目录开始逐级向上查找 `.gits/config.json`，使用最近的配置（写入也落在该文件）；直到根目录仍未找到时，回退到当前工作目录下的 `.gits/config.json`（首次保存时创建）。JSON，含 version/language/repositories/groups/defaults，以及可选键 `worktree_sets`（`omitempty`，version 仍为 1.0.0）；`repositories[].path` 与 `worktree_sets[].members[].path` 都以**实际命中的** `.gits` 所在目录为基准存相对路径（读取时解析为绝对路径，存量绝对路径兼容）

## 目录结构与职责

| 路径 | 职责 |
|------|------|
| `main.go` | 入口，调用 `cmd.Execute()` |
| `cmd/root.go` | 根命令 + 全局选项：`-g/--group`、`-r/--repo`（ID/区间/名称通配/名称/`branch:`/`set:`）、`-p/--parallel`、`-y/--yes`、`--color=auto\|always\|never`、`--no-color`；i18n 初始化；启动时校验 `--color` 并调用 `ui.InitColorMode`；git 代理参数提取（`--` 分隔符）；注册 `worktree` 子命令族并在 `Long` 里写明裸透传口径 |
| `cmd/git.go` | Git 命令代理：仓库选择（`repo.ResolveSelectionWithSets` 解析 `-r` / 分组 / 全部）、review 确认、串行/并行执行、汇总 |
| `cmd/worktree.go` | worktree 命令族：`add`（阶段 A 只读预检零副作用 → 阶段 B 逐个创建 + 仅回滚本次调用新建的 → 阶段 C 登记集合）、`list`（成员检出分支 + 路径有效性）、`remove`（任一脏即整组中止，`--force` 才透传给 git，永不删分支）、`prune`（只清登记，不删磁盘、不调 `git worktree prune`） |
| `cmd/config.go` | config 子命令：add/remove/show/rename/reid/scan/language/validate/normalize/group（add/assign/unassign/show）；remove 连带清 worktree 集合并点名受影响集合，validate 兜底清悬空 RepoID 成员 |
| `internal/config/loader.go` | `Config`/`Repository`/`WorktreeMember`/`WorktreeSet` 结构体、配置加载/保存/原始 JSON；路径相对化（`ConfigBaseDir`/`ResolvePath`/`StoredPath`/`NormalizePaths`，仓库路径与集合成员路径同一口径） |
| `internal/config/manager.go` | 配置操作：仓库增删改查、分组管理、repo-ref 解析（ID/名称/路径）、ID 重排、校验清理；worktree 集合连带清理（`detachRepoFromWorktreeSets`/`detachDanglingWorktreeMembers`） |
| `internal/git/executor.go` | git 命令执行封装（`exec.Command("git", ...)`）、`ExecutionResult`、branch/status/is-git 快捷方法、`BranchExists`（分支存在检查）、**worktree 三态判定底座**（`GitDirs`/`Classify`/`MainRepoPath` + `GitEntryKind`）、`WorktreeAdd`/`WorktreeRemove` |
| `internal/i18n/i18n.go` | 中/英双语字典，`i18n.T(key, args...)` 取文案 |
| `internal/repo/discover.go` | 仓库发现：`Discover()` 目录遍历找 `.git`（跳过 9 种噪音目录）+ 8-worker pool 并发采集 branch/status；**遍历协程与调用协程的结果消费循环必须并行推进**——`found`/`results` 各带 64 缓冲，若遍历在调用协程内同步跑完才收结果，worker 写满 `results` 即不再读 `found`、遍历写满 `found` 即无人读，候选数越过 64+64+8≈136 触发 `all goroutines are asleep - deadlock`（#1070，回归钉 `TestDiscoverManyCandidatesNoDeadlock`）；`GetRepositoryInfo` |
| `internal/repo/manager.go` | 仓库管理门面（转发到 config 包）+ `ResolveSelectionWithSets`（ID/区间/通配/名称/`branch:`/`set:` 选择，`ResolveSelection` 为薄壳）+ 列出口径 `DisplayBranch`/`DisplayLabel`/`DisplayLabelWithWorktree`/`RepoLabels`/`WorktreeMark`/`WorktreeMainName` |
| `internal/ui/printer.go` | 输出格式化：表格、分组、review 提示、config show 配置项/默认值块与 worktree 集合块、`worktree list` 明细、worktree add/remove 确认、**ANSI 颜色**（`ColorMode` 变量，`InitColorMode` 按终端能力置 ANSI 或空串） |
| `internal/ui/isvt_windows.go` / `isvt_other.go` | 平台差异的 `supportsVT()`：Windows 查 `ENABLE_VIRTUAL_TERMINAL_PROCESSING`；其余平台恒 true |

## 命名与约定

- **仓库引用（repo-ref）**：ID（纯数字）> 名称 > 路径，按优先级匹配
- **`-r/--repo`**：逗号分段选择，支持 ID 列表 `1,3,5` / ID 区间 `1-5` / 名称通配 `repo*`、`repo-???end` / 名称精确 / `branch:<pattern>`（按分支存在过滤，本地或远程任一，pattern 支持 `*`/`?`）/ `set:<集合名>`（按 worktree 集合成员选择，集合名不存在即报错不静默为空），混用按 ID 去重（`repo.ResolveSelectionWithSets`，`path.Match` 实现通配，`git.BranchExists` 实现分支存在检查；`ResolveSelection` 保留为薄壳）
- **worktree 三态判定与 submodule 排除**：目录身份一律由 `git.Classify` 判定，判据取 git 自身输出（`rev-parse --absolute-git-dir` 与 `--git-common-dir` 是否相同），**禁止**靠路径里出现 `worktrees`/`modules` 猜测：`.git` 目录 → `KindRepo`；`.git` 文件且两目录不同 → `KindLinkedWorktree`（主仓由 `MainRepoPath` 反查）；`.git` 文件且两者相同 → `KindSubmodule`；无 `.git` 入口时由 rev-parse 兜底——主仓子目录与裸仓库相等 → `KindRepo`，**linked worktree 的子目录两值仍不等 → `KindLinkedWorktree`**（归属仍指主仓，回归钉 `TestClassifyInsideWorktreeSubdir`）。submodule 本期不支持（`worktree add` 预检直接点名拒绝，`config scan` 静默跳过）。worktree 身份运行时算、不落配置（与 `Repository.Branch` 的 `json:"-"` 同口径），列出口径统一为 `[ID] 名称 (分支) ← 主仓`（`repo.DisplayLabelWithWorktree` 家族，cmd 层不拼 ANSI 与归属字符串）
- **worktree 集合成员指向**：`WorktreeMember.RepoID` 指向 **worktree 目录自己的仓库条目**，不是父仓条目。`worktree add` 创建 worktree 后为每个目录自动登记一条仓库条目（名称 `<父仓名>@<集合名>`，同路径复用，`config.EnsureRepositoryEntry`），条目与集合在同一次 `Save()` 落盘。`-r set:<集合>` 正是靠这一点把命令投递到 worktree 目录；把成员写成父仓 ID 会让命令跑在主工作区（检出分支都不是集合分支），是 #1064 复核抓到的缺陷形态，回归钉：`cmd/worktree_test.go` 的 `TestWorktreeAddMembersTargetWorktreeNotParent`。**ID 生命周期联动**：`config reid` / `config reid --all` 必须同步回写成员 `repo_id`（`remapWorktreeMemberIDs`）——不回写会让 `set:` 静默漏选、且 `config validate` 把成员当悬空删掉（评审 BLOCK-1，回归钉 `TestReassignIDsRemapWorktreeMembers`）
- **worktree 集合红线**：`add` **必须显式给 `-r`/`-g`**（缺省选择集＝全部登记仓库，一次手滑就给每个仓建目录，评审 Q-02）；预检除分支存在性外还要查「**分支已被本仓某 worktree 检出**」（`git.WorktreeCheckedOutBranches`，只读；最常见形态是缺省落到 `defaults.branch=main` 而 main 正检出在主仓，评审 Q-03）；`remove` 任一成员脏即整组中止并点名、`--force` 才透传给 git；两个命令都**永不调用 `git branch -D`、不删分支**；`prune` 只清登记，**不调 `git worktree prune`、不删磁盘目录**；`remove`/`prune` **必须一并回收 `add` 自动登记的仓库条目**（`config.DropWorktreeMemberRepos`，带路径守卫：成员指向父仓条目时一条都不删）——否则每次默认全量批量都多两行 chdir `[FAIL]`、Summary 恒不为零（评审 BLOCK-2，回归钉 `TestDropWorktreeMemberRepos`）；`add` 的回滚只作用于本次调用内存记账的新建路径，绝不遍历磁盘、绝不动调用前既有集合（回归钉 `TestCreateWorktreesRollbackOnlyThisCall`）；`gits worktree <未知子命令>` 报错退出非 0，不静默打 help（评审 Q-05）
- **配置落盘**：`config.Save()` 用「写 `config.json.tmp` → `os.Rename` 覆盖」原子替换（评审 S-02）——`worktree add` 阶段 C 把「仓库条目 + 集合登记」写进同一次 Save，直接 `WriteFile` 半写会同时毁掉两者；自动登记的条目 `groups` 初始化为 `[]`（与 `config add` 同形，落盘不出现 `null`）
- **i18n**：所有用户可见字符串必须经 `i18n.T()` 取文案，zh/en 双语成对维护
- **颜色**：集中在 `internal/ui/printer.go` 的 `Color*` 包级变量；`cmd/root.go` 启动时按 `--color`/`--no-color`/终端能力经 `InitColorMode` 统一置 ANSI 码或空串（56 处调用点引用变量，零逐点改动）；auto 模式尊重 `NO_COLOR` 与非 TTY 检测
- **命令代理**：git 命令透传，`--` 分隔 gits 参数与 git 参数；执行前默认 review 确认（`-y` 跳过）。`worktree` 注册为子命令后 `gits worktree <sub>` **不再透传**，裸 git worktree 用 `gits -r <选择> -- worktree <sub>`（root 与 worktree 的 `Long` 均写明；文案 key `worktree_passthrough_note`，zh/en 成对）
- **输出风格**：成功 `[OK]`、失败 `[FAIL]`、结尾 `Summary: N failed, N succeeded`

## 测试

- 内置 `test-repos/`（repo1/repo2/repo3，均为 git 仓库）用于手工验证多仓库操作
- Go 单测：`cmd`（`selectRepos` 的 `set:` 选择、worktree 预检零副作用与「分支已被检出」点名、重名拒绝、显式范围要求、脏点名、回滚只回本次调用、prune 只清登记、`WorktreeRemove` 不删分支）、`internal/repo`（`ResolveSelection`/`ResolveSelectionWithSets`/`FindReposByWildcard`/`isNoiseDir`/`MemberLabel` 口径）、`internal/ui`（`InitColorMode`、`PrintWorktreeSets`/`PrintWorktreeSetList` 的 missing 标记、`PrintGroupDetails` 按全量登记表反查主仓名）、`internal/config`（路径相对化/解析/搬迁/normalize、worktree 集合连带清理、reid 回写成员、死条目回收）、`internal/git`（`Classify` 三态与两个 `.git` 文件形态对照、worktree 子目录、`WorktreeAdd`/`WorktreeRemove` 生产函数直呼、`WorktreeCheckedOutBranches`）；运行 `go test -count=1 ./...`（实测 5 个 `ok` 包 + 2 个 no-test 包）
- **真实 git 依赖的两种处理，别混为一谈**：`cmd`/`internal/git`/`internal/ui` 的真 git 用例走 `requireGit` 或 `t.Skipf`（无 git 时**跳过**、包仍报 ok）；`internal/repo` 的用例直接 `t.Fatalf`（无 git 时**失败**）。所以「无 git 环境」不会让整扇门悄悄变绿——挡门的是 `internal/repo` 那条硬依赖，改造成 skip 会一并卸掉这层保护
- 实机端到端（关票门禁）：ASCII 临时目录建 2 个真仓 → `worktree add` 全绿（确认成员条目指向 worktree 目录、`-r set:` 在 worktree 内执行）→ 缺分支预检零副作用（`git worktree list` 佐证）→ `--branch main` 被预检按「已被检出」点名 → 脏成员整组中止 → `--force` 拆除后分支仍在 → `remove`/`prune` 后默认全量批量 0 失败（条目已回收）→ `config reid 3 10` 与 `config reid --all` 后 `set:` 仍全选、`config validate` 不删成员 → `gits worktree bogus` 退出码 1 → `config language en` 双语复跑
