# gits 按"分支存在"筛选仓库设计文档（`branch:` 段类型）

## 1. 目标与范围

### 1.1 目标

扩展 `gits -r` 语法，新增 `branch:<pattern>` 段类型，让命令能"选中所有存在指定分支（本地或远程任一）的仓库"，
覆盖典型场景：把 `feature/X` 合并到 `uat/sit/dev` 时，只动真正有 `feature/X` 分支的仓库，减少无效的仓库切换。

### 1.2 范围

- 仅扩展 `-r` 选项值语法
- 仅"分支存在"过滤，不做"领先提交"差异分析
- v1 不区分本地/远程；`branch:X` = 本地 `refs/heads/X` 或远程 `refs/remotes/*/X` 任一存在即命中
- glob 支持 `*` 和 `?`（与现有仓库名通配一致）

### 1.3 非目标（YAGNI）

- 不做 `tag:` / `commit:` 等其他过滤类型（虽然 `branch:` 关键词为未来预留统一前缀格式）
- 不引入 `localbranch:` / `remotebranch:` 显式区分（后续可用 `localbranch:` 扩展，不破坏现有 `branch:` 语义）

## 2. 语法

```
gits -r branch:<pattern> [-- <git-command>]
gits -r <其他段>,branch:<pattern>[,<其他段>] [-- <git-command>]
```

端到端示例（主场景）：

```bash
gits -r branch:feature/X -- checkout uat
gits -r branch:feature/X -- merge feature/X
gits -r branch:feature/X -- push origin uat

# glob：所有有 feature/* 分支的仓库
gits -r branch:feature/* -- status

# 与 ID 段并集（与现有逗号规则一致）
gits -r 1-3,branch:feature/X -- pull
```

**本地/远程语义**：`branch:feature/X` → 等价 `git for-each-ref refs/heads/feature/X refs/remotes/*/feature/X` 任一非空即命中。

**边界**：
- 模式为空（`branch:`）→ 报错，不执行
- 模式含 `:` 不合法（git refname 本身禁止 `:`），故首个 `:` 即分隔符，无歧义

## 3. 架构与组件

### 3.1 `internal/git/executor.go` 新增 `BranchExists`

```go
// BranchExists 检查 repoPath 仓库是否存在匹配 pattern 的分支（本地或远程任一）。
// pattern 支持 glob（* / ?），由 git for-each-ref 的 refname 模式实现。
func BranchExists(repoPath string, pattern string) (bool, error) {
	result := Execute(repoPath, "for-each-ref", "--count=1",
		"refs/heads/"+pattern, "refs/remotes/*/"+pattern)
	if !result.Success {
		return false, fmt.Errorf("git for-each-ref failed in %s: %w", repoPath, result.Error)
	}
	return result.Output != "", nil
}
```

### 3.2 `internal/repo/manager.go` 新增

```go
type BranchChecker func(repoPath string, pattern string) (bool, error)

func DefaultBranchChecker(repoPath string, pattern string) (bool, error) {
	return git.BranchExists(repoPath, pattern)
}

func FindReposByBranch(pattern string, repos []config.Repository, checker BranchChecker) ([]config.Repository, error)
```

### 3.3 `ResolveSelection` 签名扩展

集中解析，避免逻辑分散到 cmd 层。

```go
// 改前
func ResolveSelection(raw string, repos []config.Repository) ([]config.Repository, error)
// 改后
func ResolveSelection(raw string, repos []config.Repository, branchChecker BranchChecker) ([]config.Repository, error)
```

switch 内新增 case：

```go
case strings.HasPrefix(seg, "branch:"):
	pattern := strings.TrimPrefix(seg, "branch:")
	if pattern == "" {
		return nil, errors.New(i18n.T("branch_filter_invalid_empty"))
	}
	if branchChecker == nil {
		return nil, errors.New(i18n.T("branch_filter_no_checker"))
	}
	matched, err := FindReposByBranch(pattern, repos, branchChecker)
	if err != nil {
		return nil, err
	}
	for _, r := range matched {
		if !seen[r.ID] {
			seen[r.ID] = true
			result = append(result, r)
		}
	}
```

`branchChecker == nil` 遇到 `branch:` 段直接报错——保证纯测试场景行为可预测。

### 3.4 `cmd/git.go` `selectRepos` 调用更新

```go
selected, err := repo.ResolveSelection(repoIDStr, cfg.Repositories, repo.DefaultBranchChecker)
```

## 4. 数据流

```
gits -r branch:feature/X -- merge feature/X
  → cmd/root.go 解析 -r
  → cmd/git.go selectRepos
  → repo.ResolveSelection("branch:feature/X", cfg.Repositories, DefaultBranchChecker)
  → 命中 branch: 段 → FindReposByBranch("feature/X", repos, git.BranchExists)
  → 逐仓库 git.BranchExists → git for-each-ref --count=1 refs/heads/feature/X refs/remotes/*/feature/X
  → 输出非空即命中 → 去重后返回
  → ui.PrintReviewPrompt → 执行 git merge feature/X
```

## 5. 错误处理

| 场景 | 行为 |
|------|------|
| `branch:` 模式为空 | 报错 `branch_filter_invalid_empty`，不执行 |
| `branch:` 段 + checker==nil（纯测试误用） | 报错 `branch_filter_no_checker` |
| `git for-each-ref` 失败（仓库损坏） | 报错并中止，沿用现有 git execute 错误风格 |
| `branch:` 段 0 命中 | 不报错；走现有"通配无匹配"路径，输出 `wildcard_no_match`（含 `*?`）或 `no_repositories` |

## 6. i18n

新增 key（zh/en 成对）：

- `branch_filter_invalid_empty`
- `branch_filter_no_checker`

并更新 `repo_selection_note` 帮助文案，追加 `branch:<pattern>` 说明。

> 注：`git for-each-ref` 执行失败属低层原始错误（仓库损坏等），沿用现有 `ResolveSelection` 原始错误包装风格（`fmt.Errorf`），不纳入 i18n——与既有 "invalid ID range: %s" 等解析错误的处理一致。

## 7. 影响范围

| 文件 | 改动 |
|------|------|
| `internal/git/executor.go` | 新增 `BranchExists` |
| `internal/repo/manager.go` | 新增 `BranchChecker` / `DefaultBranchChecker` / `FindReposByBranch`；`ResolveSelection` 加参 + `branch:` case |
| `cmd/git.go` | `selectRepos` 传 `repo.DefaultBranchChecker` |
| `internal/repo/manager_test.go` | 现有测试补 `nil` 第三参；新增 `branch:` 段专项用例（fake checker） |
| `internal/i18n/i18n.go` | 新增 3 条 i18n key |
| `tools/gits/ReadMe.md` | `-r` 章节补 `branch:` 段与示例 |
| `tools/gits/AGENTS.md` | 仓库引用/`-r` 约定小节补 `branch:` 段 |

## 8. 设计取舍说明

1. **`branch:` 关键词而非 `@` sigil**：为未来 `tag:` / `commit:` 预留统一前缀格式。
2. **v1 不区分本地/远程**：用户已确认"本地或远程任一"满足主场景；显式区分可后续用 `localbranch:` 扩展，不破坏现有 `branch:` 语义。
3. **并集而非交集**：与现有逗号段并集规则一致；主场景单段使用，无歧义；混用场景交集会破坏既有心智。
4. **`branchChecker` 注入 `ResolveSelection`**：解析逻辑集中一处，注入接口保留纯测试能力。
