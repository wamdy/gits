# gits 配置仓库路径相对化设计

- 日期：2026-10-26
- 票据：#1056 / `ISSUE-20261026-gits-relative-repo-paths`
- 基线：`28d909f`

## 目标

`.gits/config.json` 的 `repositories[].path` 以 `.gits` 所在目录为基准存相对路径；运行时解析为绝对路径；存量绝对路径继续可用。

## 语义

```
<project>/
├── .gits/config.json      repositories[].path = "repos/r1"
└── repos/r1/
```

- 配置基目录 `ConfigBaseDir()` = `filepath.Dir(filepath.Dir(configPath))`，返回绝对路径。
- `ResolvePath`：绝对路径 Clean 后保留；相对路径 `Join(base, FromSlash(p))` + Clean。
- `StoredPath`：`filepath.Rel(base, abs)` 成功则 `ToSlash`；失败（如 Windows 跨盘符）保留绝对路径并 `ToSlash`。

## 转换点

- `Load()`：逐仓 `Path = ResolvePath(Path)`，保证内存中恒为绝对路径，下游（git 执行、scan 比较、UI、validate）零改动。
- `Save()`：复制 repositories，逐仓 `Path = StoredPath(Path)` 后序列化；不修改调用方内存对象。
- `NormalizePaths()`：显式迁移；无配置文件不创建；返回落盘值变化条数。

## 兼容

| 场景 | 行为 |
|------|------|
| 存量绝对路径 | Load 正常解析；下一次 Save 自动相对化 |
| 跨盘符 | 保留绝对路径（分隔符统一 `/`） |
| 落盘分隔符 | 统一 `/`；读取兼容 `/` 与 `\` |
| schema | 不变，version 仍 `1.0.0` |
| rename 按路径 | 先把 repo-ref 路径解析为绝对路径再比较 |

## 测试

`internal/config/paths_test.go`：round-trip、整目录 rename 后 Load、存量绝对路径、跨卷回退、normalize 幂等、无配置不建文件、raw 保留落盘形态、rename 相对路径命中。
