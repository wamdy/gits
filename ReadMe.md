## 背景与需求分析

### 问题描述
Git 原生仅支持单一仓库操作，在微服务架构下，一个项目往往包含多个独立的代码仓库，导致：
- 代码管理分散，难以统一操作
- 批量执行 Git 命令繁琐（如批量拉取、推送、状态检查）
- 仓库依赖关系难以追踪
- 团队协作效率低下

### 需求目标
- 提供统一的命令行接口管理多个 Git 仓库
- 保持与 Git 原生命令的兼容性
- 支持仓库分组管理
- 提供批量操作能力
- 支持配置持久化
- **执行前 review 确认（可跳过）**

---

## 目录

- [核心功能设计](#核心功能设计)
  - [命令架构](#命令架构)
  - [全局选项](#全局选项)
  - [命令参数分支树](#命令参数分支树)
  - [子命令分类](#子命令分类)
- [仓库引用方式](#仓库引用方式)
- [Review 确认功能](#review-确认功能)
- [参数分隔符](#参数分隔符)
- [仓库管理详解](#仓库管理详解)
- [分组管理详解](#分组管理详解)
- [配置文件设计](#配置文件设计)
- [实现架构](#实现架构)
- [使用场景](#使用场景)
- [与 Git 的兼容性](#与-git-的兼容性)
- [错误处理](#错误处理)
- [性能考虑](#性能考虑)
- [部署方案](#部署方案)

---

## 核心功能设计

### 命令架构

```
gits [全局选项] <子命令> [子命令选项] [参数]
```

### 全局选项
| 选项 | 说明 |
|------|------|
| `-v, --version` | 显示版本信息 |
| `-h, --help` | 显示帮助信息 |
| `-g, --group <name>` | 对指定分组执行命令 |
| `-p, --parallel` | 并行执行模式 |
| `-y, --yes` | **跳过执行前确认** |
| `--` | **参数分隔符**，其后的参数将直接传递给 Git |

### 命令参数分支树

```
gits [全局选项] <命令> [参数]

├── 全局选项 (Persistent Flags)
│   ├── -v, --version          显示版本信息
│   ├── -h, --help             显示帮助信息
│   ├── -g, --group <name>    对指定分组执行命令
│   ├── -p, --parallel         并行执行模式
│   └── -y, --yes              跳过执行前确认
│
├── 子命令
│   │
│   ├── config                  配置管理
│   │   ├── add <path>          添加仓库到管理列表
│   │   │   ├── -n, --name     指定自定义名称
│   │   │   └── -i, --id       指定自定义 ID
│   │   ├── remove <path-or-id> 从管理列表移除仓库
│   │   ├── show                显示当前管理的仓库列表
│   │   ├── rename <repo-ref> <new-name>       修改仓库名称
│   │   ├── reid [<old-id> <new-id>]           修改仓库 ID（同步更新分组引用）
│   │   │   └── -a, --all                     重排所有 ID 从 1 开始
│   │   ├── scan [path]         扫描目录发现仓库，交互选择添加
│   │   ├── validate            校验并清理配置（移除无效分组引用）
│   │   └── group               分组管理
│   │       ├── add <name>      创建仓库分组
│   │       ├── remove <name>   删除仓库分组
│   │       ├── assign <group> <repo-ref>    将仓库分配到分组（自动创建不存在的分组）
│   │       ├── unassign <group> <repo-ref>  从分组移除仓库
│   │       └── show [<group>]  显示分组及成员列表
│   │
│   └── <git-command>           Git 命令代理（批量执行所有 git 命令）
│       ├── status              批量查看状态
│       ├── pull [remote] [branch]   批量拉取
│       ├── push [remote] [branch]   批量推送
│       ├── fetch               批量获取远程更新
│       ├── checkout <branch>   批量切换分支
│       ├── branch              批量查看分支
│       ├── commit -m "<msg>"   批量提交
│       ├── merge <branch>      批量合并
│       ├── stash               批量暂存
│       ├── stash pop           批量恢复暂存
│       ├── clean [options]     批量清理未跟踪文件
│       ├── diff [options]      批量查看变更差异
│       ├── log [options]       批量查看提交日志
│       └── ... (其他所有 git 命令)
│
└── 参数分隔符
    └── --                      其后的参数直接传递给 Git
```

### 子命令分类

#### 1. 仓库管理
| 命令 | 说明 | 示例 |
|------|------|------|
| `gits config scan [path]` | 扫描目录发现仓库，交互选择添加 | `gits config scan ./services` |
| `gits config add <path>` | 手动添加仓库（名称取自目录名，ID 自动分配） | `gits config add ./services/user-service` |
| `gits config add <path> --name <name>` | 手动添加仓库并指定自定义名称 | `gits config add ./services/user-service --name user-svc` |
| `gits config add <path> --id <id>` | 手动添加仓库并指定自定义 ID | `gits config add ./services/user-service --id 100` |
| `gits config add <path> --name <name> --id <id>` | 同时指定名称和 ID | `gits config add ./services/user-service --name user-svc --id 100` |
| `gits config remove <repo-ref>` | 从管理列表移除仓库（支持 ID、名称或路径） | `gits config remove 1` |
| `gits config validate` | 校验并清理配置（检查仓库路径、移除无效引用和空分组） | `gits config validate` |
| `gits config show` | 显示当前管理的仓库列表（含 ID、名称、路径、分组） | `gits config show` |
| `gits config show --raw` | 以 JSON 格式显示原始配置文件内容 | `gits config show -r` |
| `gits config rename <repo-ref> <new-name>` | 修改仓库名称（支持 ID、名称或路径引用） | `gits config rename 1 user-svc` |
| `gits config reid <old-id> <new-id>` | 修改仓库 ID（同步更新所有分组引用） | `gits config reid 2 5` |
| `gits config reid --all` | 重排所有仓库 ID，从 1 开始连续分配 | `gits config reid -a` |

#### 2. 分组管理
| 命令 | 说明 | 示例 |
|------|------|------|
| `gits config group add <name>` | 创建仓库分组 | `gits config group add backend` |
| `gits config group remove <name>` | 删除仓库分组 | `gits config group remove backend` |
| `gits config group assign <group> <repo-ref>` | 将仓库分配到分组（分组不存在时自动创建并确认） | `gits config group assign backend 1` |
| `gits config group unassign <group> <repo-ref>` | 从分组移除仓库 | `gits config group unassign backend 1` |
| `gits config group show [<group>]` | 显示分组及成员列表 | `gits config group show backend` |

#### 3. Git 命令代理（批量执行）
所有 Git 原生命令均可通过 gits 批量执行

| 命令 | 说明 | 示例 |
|------|------|------|
| `gits status` | 批量查看所有仓库状态 | `gits status` |
| `gits pull [remote] [branch]` | 批量拉取更新 | `gits pull origin main` |
| `gits push [remote] [branch]` | 批量推送代码 | `gits push origin main` |
| `gits fetch` | 批量获取远程更新 | `gits fetch` |
| `gits checkout <branch>` | 批量切换分支 | `gits checkout feature/login` |
| `gits branch` | 批量查看分支 | `gits branch -a` |
| `gits commit -m "<msg>"` | 批量提交（需所有仓库有变更） | `gits commit -m "Update dependencies"` |
| `gits merge <branch>` | 批量合并分支 | `gits merge feature/login` |
| `gits stash` | 批量暂存 | `gits stash` |
| `gits stash pop` | 批量恢复暂存 | `gits stash pop` |

#### 4. 分组操作
| 命令 | 说明 | 示例 |
|------|------|------|
| `gits -g <group> -- <command>` | 对指定分组执行命令 | `gits -g backend -- pull` |

---

## 仓库引用方式

`<repo-ref>` 参数支持三种引用方式，按优先级依次匹配：

| 优先级 | 方式 | 格式 | 示例 |
|--------|------|------|------|
| 1 | ID | 纯数字 | `1`, `2` |
| 2 | 名称 | 仓库自定义名称 | `user-svc`, `my-repo1` |
| 3 | 路径 | 仓库文件路径 | `./services/user-service` |

```bash
# 三种方式等价（假设 ID=1, name=user-svc, path=./services/user-service）
gits config group assign backend 1
gits config group assign backend user-svc
gits config group assign backend ./services/user-service

# remove 同样支持
gits config remove 1
gits config remove user-svc
gits config remove ./services/user-service
```

### 按分支存在过滤（`branch:` 段）

`-r` 支持 `branch:<pattern>` 段，选中所有"存在指定分支（本地或远程任一）"的仓库，典型用于按功能分支批量操作：

```bash
# 只对有 feature/X 分支的仓库执行合并
gits -r branch:feature/X -- merge feature/X

# 切到环境分支（uat/sit/dev）后推送
gits -r branch:feature/X -- checkout uat
gits -r branch:feature/X -- push origin uat

# glob：所有有 feature/* 分支的仓库
gits -r branch:feature/* -- status

# 与 ID 段并集（与逗号规则一致）
gits -r 1-3,branch:feature/X -- pull
```

说明：`branch:feature/X` 等价于仓库同时命中 `refs/heads/feature/X` 或 `refs/remotes/*/feature/X` 中任一分支；`pattern` 支持 `*` 与 `?` 通配。

---

## Review 确认功能

### 功能说明
为确保操作安全，gits 在执行可能影响仓库状态的命令前会显示 review 确认提示，包含：
- 即将执行的命令
- 命令参数
- 受影响的仓库列表

用户需要确认后才会执行操作。

### 使用示例

```bash
# 默认会显示确认提示
gits pull origin main

# 输出示例：
# Review before execution:
# Command: pull [origin main]
# Repositories (3):
#   - repo1
#   - repo2
#   - repo3
#
# Do you want to proceed? [y/N]:
```

```bash
# 使用 -y/--yes 跳过确认
gits -y -- pull origin main
gits -g backend -y -- pull
gits -y -- clean -fd
```

### 支持 review 的命令
所有通过 Git 命令代理执行的命令在执行前都会显示 review 确认提示：
- `gits <git-command>` — 所有 Git 命令代理

---

## 参数分隔符

### 功能说明
使用 `--` 作为参数分隔符，可以明确区分 gits 参数和要传递给底层 Git 命令的参数。这在 Git 命令参数与 gits 参数冲突时非常有用。

### 使用示例

```bash
# 组合使用多个 gits 参数
gits -g backend -p -y -- pull origin main

# 执行 git status -v（verbose 模式）
gits -- status -v
```

### 参数解析规则

```
gits [gits-flags] -- [git-command] [git-flags]
       ↑              ↑
    gits 参数     传递给 git 的参数
```

> **建议**：当 Git 命令参数与 gits 参数同名时，使用 `--` 分隔符确保正确传递。

---

## 仓库管理详解

### 仓库管理工作流

#### 1. 扫描并添加仓库
```bash
# 扫描当前目录
gits config scan

# 扫描指定目录
gits config scan ./services

# 交互式选择（支持 1,3,5 / 1-5 / all）
# Found 3 Git repositories:
#   [1] user-service (./services/user-service) [new]
#   [2] order-service (./services/order-service) [registered]
#   [3] app (./web/app) [new]
#
# Select repositories to add (e.g., 1,3,5 or 1-5 or all): 1,3
# Success: Added: user-service
# Success: Added: app
# Summary: 2 added, 0 skipped
```

#### 2. 手动添加仓库
```bash
# 使用目录名作为名称（自动处理重名）
gits config add ./services/user-service

# 指定自定义名称（重复时报错）
gits config add ./services/user-service --name user-svc
```

#### 3. 查看仓库列表
```bash
gits config show
# 输出：
# ID  NAME        PATH                          GROUPS
# --  ----        ----                          ------
# 1   user-svc    C:\project\services\user-svc  [backend]
# 2   order-svc   C:\project\services\order-svc [backend]
# 3   app         C:\project\web\app            [frontend]
```

#### 4. 修改仓库名称
```bash
# 通过 ID 改名
gits config rename 1 user-svc

# 通过旧名称改名
gits config rename repo1 user-svc

# 通过路径改名
gits config rename ./services/user-service user-svc
```

#### 5. 修改仓库 ID
```bash
# 将 ID 2 改为 5（分组引用自动同步）
gits config reid 2 5

# 重排所有 ID 从 1 开始连续分配
gits config reid --all
```

#### 6. 移除仓库
```bash
# 通过 ID 移除
gits config remove 1

# 通过名称移除
gits config remove user-svc

# 通过路径移除
gits config remove ./services/user-service
```

#### 7. 校验配置
```bash
# 检查并清理无效的分组引用和空分组
gits config validate
# Success: Configuration is valid, no issues found
# 或
# Success: Configuration cleaned: 1 empty groups removed, 2 invalid references removed
```

---

## 分组管理详解

### 分组设计理念

gits 采用**双向索引**设计来维护分组与仓库的关系：
- **仓库维度**：每个仓库记录自己属于哪些分组
- **分组维度**：每个分组记录包含哪些仓库的 **ID**

使用 ID 引用而非名称，确保仓库改名不影响分组关系。

### 分组管理工作流

#### 1. 创建分组
```bash
gits config group add backend
gits config group add frontend
gits config group add shared
```

#### 2. 分配仓库到分组
```bash
# 分配仓库到分组（支持 ID、名称或路径）
gits config group assign backend 1
gits config group assign backend order-svc
gits config group assign frontend ./web/app

# 分组不存在时自动创建（需确认）
gits config group assign newgroup user-svc
# Group 'newgroup' does not exist. Create it? [y/N]: y
# Success: Repository assigned to group successfully
```

#### 3. 查看分组信息
```bash
# 查看所有分组（含仓库数量和详情）
gits config group show
# 输出：
# backend (2):
#   - 1 user-svc (C:\project\services\user-svc)
#   - 2 order-svc (C:\project\services\order-svc)
#
# frontend (1):
#   - 3 app (C:\project\web\app)

# 查看特定分组的成员
gits config group show backend
# 输出：
# backend (2):
#   - 1 user-svc (C:\project\services\user-svc)
#   - 2 order-svc (C:\project\services\order-svc)
```

#### 4. 管理分组成员
```bash
# 从分组中移除仓库（仓库仍保留在管理列表中）
gits config group unassign backend 1
gits config group unassign backend user-svc

# 删除分组（仓库不受影响）
gits config group remove shared
```

### 分组操作示例

#### 对分组执行批量 Git 命令
```bash
# 只更新后端服务
gits -g backend -- pull

# 只查看前端项目状态
gits -g frontend -- status

# 查看共享模块状态
gits -g shared -- status
```

### 分组最佳实践

| 分组策略 | 示例 | 适用场景 |
|----------|------|----------|
| **按技术栈分组** | java-services, node-services, go-services | 针对特定语言/框架执行操作 |
| **按功能模块分组** | auth, payment, notification | 针对业务模块执行操作 |
| **按环境分组** | dev-tools, monitoring, ci-cd | 针对工具链执行操作 |
| **按团队分组** | team-a, team-b, team-c | 针对团队负责的仓库执行操作 |

---

## 配置文件设计

### 配置文件路径

配置文件保存在当前工作目录下：
```
<current-directory>/.gits/config.json
```

### 使用示例

```bash
# 查看当前目录的配置
gits config show

# 添加仓库到当前目录配置
gits config add ./services/user-service
```

### 配置结构
```json
{
  "version": "1.0.0",
  "repositories": [
    {
      "id": 1,
      "path": "/path/to/repo1",
      "name": "repo1",
      "groups": ["backend", "core"]
    },
    {
      "id": 2,
      "path": "/path/to/repo2",
      "name": "repo2",
      "groups": ["frontend"]
    }
  ],
  "groups": {
    "backend": [1],
    "frontend": [2],
    "core": [1]
  },
  "defaults": {
    "remote": "origin",
    "branch": "main"
  }
}
```

---

## 实现架构

### 技术栈
- **语言**: Go（跨平台、编译型、高性能）
- **依赖**: github.com/spf13/cobra（命令行框架）

### 模块结构
```
gits/
├── cmd/                    # 命令行入口
│   ├── root.go             # 根命令（全局选项）
│   ├── config.go           # config 子命令（add/remove/show/scan/group）
│   ├── git.go              # Git 命令代理（串行/并行执行）
│   └── mock.go             # mock 测试命令
├── internal/
│   ├── config/             # 配置管理
│   │   ├── loader.go       # 配置加载/保存、Repository/Config 结构体
│   │   └── manager.go      # 配置操作（增删改查、分组管理）
│   ├── git/                # Git 操作封装
│   │   └── executor.go     # Git 命令执行
│   ├── repo/               # 仓库管理
│   │   ├── discover.go     # 仓库扫描发现
│   │   └── manager.go      # 仓库管理（对外接口层）
│   └── ui/                 # 输出格式化
│       └── printer.go      # 结果输出（表格、分组、确认提示）
├── go.mod
├── go.sum
└── main.go
```

---

## 使用场景

### 场景一：日常开发流程
```bash
# 早上上班，同步所有仓库
gits -- pull

# 开发完成，检查所有仓库状态
gits -- status

# 提交代码
gits -- commit -m "Implement feature X"
gits -- push
```

### 场景二：分支管理
```bash
# 从 main 分支创建特性分支
gits -- checkout -b feature/new-feature

# 开发完成，合并到主分支
gits -- checkout main
gits -- pull
gits -- merge feature/new-feature
gits -- push
```

### 场景三：分组操作
```bash
# 只更新后端服务
gits -g backend -- pull

# 只查看前端项目状态
gits -g frontend -- status
```

### 场景四：仓库维护
```bash
# 扫描并选择添加新仓库
gits config scan ./services

# 查看所有仓库状态
gits -- status

# 清理所有仓库的未跟踪文件（跳过确认）
gits -y -- clean -fd
```

---

## 与 Git 的兼容性

### 命令映射规则
- gits 支持所有 Git 原生命令参数
- 命令执行时直接透传给底层 git 命令
- 保持相同的退出码和错误输出

### 兼容性示例
```bash
# 原生 Git
git status
git pull origin main
git log --oneline -n 10

# gits 等价命令
gits -- status
gits -- pull origin main
gits -- log --oneline -n 10
```

---

## 错误处理

### 错误类型
| 错误类型 | 处理方式 |
|----------|----------|
| 仓库不存在 | 跳过并记录警告 |
| Git 命令失败 | 显示错误信息，继续执行其他仓库 |
| 配置文件损坏 | 提示用户并提供修复选项 |
| 权限不足 | 显示权限错误，跳过该仓库 |

### 错误输出格式
```
[FAIL] /path/to/repo: git pull failed
       Error: fatal: could not read Password for 'https://github.com': terminal prompts disabled

[OK]   /path/to/repo2: Already up to date.

Summary: 1 failed, 3 succeeded
```

---

## 性能考虑

### 优化策略
1. **并行执行**: 多个仓库的 Git 命令并行执行，提升批量操作效率
2. **缓存机制**: 缓存仓库路径解析结果，减少重复扫描
3. **增量更新**: 仅在配置变更时重新加载配置文件

### 执行模式
- **串行模式**: 默认模式，按顺序执行，便于查看输出
- **并行模式**: 通过 `-p` 或 `--parallel` 启用，适合大量仓库

---

## 部署方案

### 安装方式
- **源码编译**: `go build -o gits main.go`
- **包管理器**: 支持 Homebrew、Chocolatey、apt 等
- **手动下载**: 从 GitHub Releases 下载预编译二进制

### 跨平台支持
- Windows (x86_64, arm64)
- macOS (x86_64, arm64)
- Linux (x86_64, arm64, ppc64le)
