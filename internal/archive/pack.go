// Package archive 实现 `gits pack` 的内容装配逻辑：把一个仓库的磁盘内容，按
// 「与仓库同构」的规则写入调用方已经打开的 zip 流（REQ-02 / REQ-03 / REQ-05 / REQ-06）。
//
// 分层边界（#1094 design.md §一）：本包不理解 cobra 与 flag、不打印、不判 TTY、
// 不调 os.Exit，也不调用 git —— 仓库的 git 身份（普通仓 / linked worktree / submodule）
// 由 cmd 层用 git.Classify 判定后以字符串传入（ADR-150 红线：判据取 git 自身输出，
// 本包绝不靠路径里是否出现 worktrees/modules 猜测）。
//
// 三个同构不变量（design.md §三）：
//  1. 一仓一个顶层目录，entry 第一段恒等于 RepoSpec.TopDir；
//  2. 顶层目录内的相对路径与仓库内相对路径逐段相等（含 .git 内部路径）；
//  3. 包根唯一的非目录附加物是 ManifestName。
package archive

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ManifestName 是包根上唯一允许的非仓库 entry 名（同构不变量 3）。
// 顶层目录名与之大小写不敏感撞车时必须整批报错（AC-02.3 延伸）。
const ManifestName = "gits-pack-manifest.json"

// gitEntryName 是仓库自身 git 数据入口的名字（目录形态与 gitfile 形态同名）。
const gitEntryName = ".git"

// Mode 三态。互斥校验在 cmd 层完成（AC-03.3），本包只消费结果。
type Mode int

const (
	ModeFull    Mode = iota // AC-03.4：工作区 + .git，磁盘原样快照
	ModeGitOnly             // AC-03.1：只收 .git 入口内容，排除工作区文件
	ModeNoGit               // AC-03.2：只收工作区，排除仓库自身的 .git 入口
)

func (m Mode) String() string {
	switch m {
	case ModeGitOnly:
		return "git-only"
	case ModeNoGit:
		return "no-git"
	default:
		return "full"
	}
}

// RepoSpec 描述一个待压仓库。全部字段由 cmd 层解析后传入，本包不再访问 git。
type RepoSpec struct {
	ID      int    // 配置 ID，仅进 manifest 供审计回溯（不构成持久化引用，design D7）
	Name    string // 配置名（worktree 自动条目形如 gits@demo）；只进 manifest，不作顶层目录名
	Path    string // 仓库绝对路径
	TopDir  string // 包内顶层目录名 = TopDir(Path) 清洗结果（AC-02.1）
	Branch  string // 展示用分支值（detached/空仓三态已在 cmd 层解析，TQ-03）
	GitKind string // "repo" / "linked-worktree" / "submodule" / "not-git"
}

// Options 是 pack 的内容选项。
type Options struct {
	Mode Mode
	// Excludes 为 nil 表示不排除（AC-05.1）。
	Excludes *ExcludeMatcher
	// SelfPaths 是目标包及其 .tmp 的绝对路径；落在被压仓内时必须显式排除（TQ-05 / AC-06.4）。
	SelfPaths []string
}

// Stats 是单仓的内容装配计数，同时供结果行与 manifest（AC-05.2/05.4）。
type Stats struct {
	Files        int   // 实际写入的常规文件 entry 数
	Bytes        int64 // 这些 entry 的未压缩字节合计
	LinkSkipped  int   // 非常规文件（link / socket / fifo / device）跳过数（AC-06.5）
	Failed       int   // 打开/取信息失败的 entry 数（AC-06.3）
	ExcludedSelf int   // 因 SelfPaths 排除掉的条数（AC-06.4）
	PrunedDirs   int   // 被 --exclude 剪掉的目录子树数（诊断用）
}

// RepoRecord 是 manifest 里的一条仓库记录。
type RepoRecord struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	SourcePath   string `json:"source_path"`
	TopDir       string `json:"top_dir"`
	Branch       string `json:"branch"`
	GitKind      string `json:"git_entry_kind"`
	Files        int    `json:"files"`
	SkippedFiles int    `json:"skipped_nonregular_files"`
	FailedFiles  int    `json:"failed_files"`
	ExcludedSelf int    `json:"excluded_self_paths"`
	PrunedDirs   int    `json:"pruned_dirs"`
	Bytes        int64  `json:"bytes"`
	OK           bool   `json:"ok"`
	Error        string `json:"error,omitempty"`
}

// Manifest 是包内 `gits-pack-manifest.json` 的内容（AC-05.4）。
type Manifest struct {
	CreatedAt string       `json:"created_at"`
	Tool      string       `json:"tool"`
	Version   string       `json:"tool_version"`
	Mode      string       `json:"mode"`
	Out       string       `json:"out"`
	Excludes  []string     `json:"excludes"`
	Repos     []RepoRecord `json:"repos"`
	Summary   Summary      `json:"summary"`
}

// Summary 是 manifest 的整体计数——读包即知内容与成败，不必解包。
type Summary struct {
	Repos     int   `json:"repos"`
	Succeeded int   `json:"succeeded"`
	Failed    int   `json:"failed"`
	Files     int   `json:"files"`
	Bytes     int64 `json:"bytes"`
}

// NewManifest 建一份 manifest；excludes 为生效模式列表（nil 落盘为 []）。
func NewManifest(mode Mode, out string, excludes []string, version string) *Manifest {
	if excludes == nil {
		excludes = []string{}
	}
	return &Manifest{
		CreatedAt: time.Now().Format(time.RFC3339),
		Tool:      "gits pack",
		Version:   version,
		Mode:      mode.String(),
		Out:       filepath.ToSlash(out),
		Excludes:  excludes,
		Repos:     []RepoRecord{},
	}
}

// AddRecord 登记一条仓库记录并累加汇总。
func (m *Manifest) AddRecord(r RepoRecord) {
	m.Repos = append(m.Repos, r)
	m.Summary.Repos++
	if r.OK {
		m.Summary.Succeeded++
		m.Summary.Files += r.Files
		m.Summary.Bytes += r.Bytes
	} else {
		m.Summary.Failed++
	}
}

// WriteTo 把 manifest 作为包根 entry 写出（design D4：写在全部仓库之后）。
// 失败由调用方升级为整包失败（AC-05.5）——不产出"看起来成功却无清单"的包。
func (m *Manifest) WriteTo(w *zip.Writer) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	hdr := &zip.FileHeader{Name: ManifestName, Method: zip.Deflate, Modified: safeModTime(time.Now())}
	hdr.SetMode(0o644)
	zw, err := w.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = zw.Write(data)
	return err
}

// ---------------------------------------------------------------- 命名

var illegalNameChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// SanitizeName 把任意字符串收敛成可安全使用的文件名单段（AC-04.3）：
// 非法字符与控制字符替换为 `-`，剥掉结尾的点与空格（Windows 会静默丢弃，留着会让
// "写的名字"与"存在的名字"不一致），全非法或空串退回 `-`。
func SanitizeName(s string) string {
	out := illegalNameChars.ReplaceAllString(s, "-")
	out = strings.TrimRight(out, ". ")
	if strings.TrimSpace(out) == "" {
		return "-"
	}
	return out
}

// TopDir 给出仓库在包内的顶层目录名：仓库路径 basename 清洗后的结果（AC-02.1）。
// 刻意不用配置 Name——worktree 自动登记条目名形如 `gits@demo`，磁盘上并不存在那个目录，
// 用它会让"解压出来的目录名 ≠ 磁盘上的目录名"，违背 Q1 裁决的同构定义（design D1）。
// 根目录形态（`C:\` 或 `/`）取卷名兜底，避免产生空顶层目录。
func TopDir(repoPath string) string {
	clean := filepath.Clean(strings.TrimSpace(repoPath))
	base := filepath.Base(clean)
	if base == "." || base == ".." || base == string(filepath.Separator) || base == "" || base == "/" {
		if vol := filepath.VolumeName(clean); vol != "" {
			base = strings.TrimRight(vol, ":")
		} else {
			base = "root"
		}
	}
	return SanitizeName(base)
}

// CaseFoldDuplicates 返回大小写不敏感意义下重复的名称（每组只报一次，保留首次写法）。
// 目标文件系统（Windows 与 macOS 默认卷）大小写不敏感：zip 里 `Repo` 与 `repo` 是两个
// 合法 entry，解出来却并成同一目录互相覆盖——冲突判定必须不区分大小写（TQ-04）。
func CaseFoldDuplicates(names []string) []string {
	seen := make(map[string]bool, len(names))
	dup := make([]string, 0)
	for _, n := range names {
		key := strings.ToLower(n)
		if seen[key] {
			dup = append(dup, n)
			continue
		}
		seen[key] = true
	}
	return dup
}

// safeModTime 把 mtime 夹进 DOS 时间可表示区间。Go 的 archive/zip 对 year<1980 或 >2107
// 的 Modified 直接拒绝建 entry，一个 1970 年的时间戳会让整包失败；夹到 1980-01-01
// 而不是静默跳过该文件（宁可时间不准，不要内容缺失）。
func safeModTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if y := t.Year(); y < 1980 || y > 2107 {
		return time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return t
}

// ---------------------------------------------------------------- 排除模式

// ExcludeMatcher 承载 `--exclude` 语义（AC-05.1）。模式按"是否含路径分隔符"分两类：
//   - 段名模式（不含分隔符）：对仓库相对路径的**任意一段**做 path.Match，命中即剪整棵子树
//     （`--exclude node_modules` 一次剪干净，不再下探）；
//   - 路径模式（含 `/` 或 `\`）：对统一 `/` 分隔的相对路径做整段 path.Match
//     （`*` 不跨分隔符，与 `-r` 名称通配同口径）。
//
// 大小写敏感——与既有 isNoiseDir（discover.go:44 注释明写"精确匹配，大小写敏感"）
// 及 `-r` 通配同一取向（design D5 / requirements D-01）。
type ExcludeMatcher struct {
	raw      []string
	segments []string
	paths    []string
}

// CompileExcludes 编译模式列表；逗号分隔的一次多值在此展开，非法 glob 直接报错（不静默丢弃）。
func CompileExcludes(patterns []string) (*ExcludeMatcher, error) {
	m := &ExcludeMatcher{raw: []string{}, segments: []string{}, paths: []string{}}
	for _, item := range patterns {
		for _, part := range strings.Split(item, ",") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			if _, err := path.Match(p, "x"); err != nil {
				return nil, fmt.Errorf("invalid --exclude pattern %q: %w", p, err)
			}
			slash := filepath.ToSlash(p)
			m.raw = append(m.raw, slash)
			if strings.Contains(slash, "/") {
				m.paths = append(m.paths, slash)
			} else {
				m.segments = append(m.segments, slash)
			}
		}
	}
	return m, nil
}

// Patterns 返回生效模式列表（进 manifest 与结果披露，AC-05.2）。
func (m *ExcludeMatcher) Patterns() []string {
	if m == nil {
		return []string{}
	}
	return m.raw
}

// MatchSegments 判断相对路径的任一段是否命中段名模式。
func (m *ExcludeMatcher) MatchSegments(relSlash string) bool {
	if m == nil || len(m.segments) == 0 {
		return false
	}
	for _, seg := range strings.Split(relSlash, "/") {
		for _, pat := range m.segments {
			if ok, err := path.Match(pat, seg); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// MatchRelPath 判断仓库相对路径是否命中路径模式。
func (m *ExcludeMatcher) MatchRelPath(relSlash string) bool {
	if m == nil || len(m.paths) == 0 {
		return false
	}
	for _, pat := range m.paths {
		if ok, err := path.Match(pat, relSlash); err == nil && ok {
			return true
		}
	}
	return false
}

// Excluded 综合两类模式；调用方对目录命中即 SkipDir，对文件命中即跳过。
func (m *ExcludeMatcher) Excluded(relSlash string) bool {
	if m == nil {
		return false
	}
	return m.MatchSegments(relSlash) || m.MatchRelPath(relSlash)
}

// ---------------------------------------------------------------- 核心遍历

// emit 消费一条"即将成为 entry"的内容。diskPath 为磁盘绝对路径，entryName 为包内路径
// （目录 entry 以 `/` 结尾）。返回 errOpenSentinel 会被 walk 降级为计数并继续；
// 其他 error 一律中止整包（半途截断的 entry 留在备份包里比整包失败更坏）。
type emit func(entryName, diskPath string, size int64, mode fs.FileMode) error

// walk 是同构装配的唯一遍历实现：Write 与 Count 共用同一套过滤，保证
// "dry-run 报的数"与"真实包里的数"不因两套代码而分叉。
func walk(spec RepoSpec, opts Options, out emit) (Stats, error) {
	var st Stats

	root := filepath.Clean(spec.Path)
	rootInfo, err := os.Stat(root)
	if err != nil {
		return st, err
	}
	if !rootInfo.IsDir() {
		return st, fmt.Errorf("repository path is not a directory: %s", root)
	}
	top := spec.TopDir
	if top == "" {
		top = TopDir(root)
	}
	self := cleanedPathSet(opts.SelfPaths)

	// 同构不变量 1：顶层目录 entry 恒存在（空仓也看得见这个目录，AC-02.5）。
	// 中间层目录不单独写 entry，由解压工具按 entry 名隐式还原；空子目录因此不入包（已知取舍）。
	if out != nil {
		if err := out(top+"/", root, 0, rootInfo.Mode()|fs.ModeDir); err != nil {
			return st, err
		}
	}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// 遍历途中取不到信息：计数并继续（AC-06.3 的"读不到"分支）。
			st.Failed++
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == root {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			st.Failed++
			return nil
		}
		relSlash := filepath.ToSlash(rel)

		// 1) Mode 过滤。**只作用于根级 .git**：嵌套仓（submodule / 仓中仓）的 .git 属于
		//    外层仓的工作区内容，悄悄剥掉会让备份出来的嵌套仓坏掉（design D10）。
		if relSlash == gitEntryName && opts.Mode == ModeNoGit {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if relSlash != gitEntryName && !strings.HasPrefix(relSlash, gitEntryName+"/") && opts.Mode == ModeGitOnly {
			// git-only 形态下，`.git` 之外的一切都属于工作区内容（AC-03.1）。
			// 判据必须覆盖 `.git` 的**子路径**：只比 `.git` 本身会让 .git/config 被当成工作区文件漏掉。
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		// 2) 目标包自身与它的 .tmp（TQ-05 / AC-06.4）：不排除会读到自己在增长的文件，
		//    或把半成品算进条目数——两者都会让"产物看起来对、其实不对"。
		if !d.IsDir() && self[cleanKey(p)] {
			st.ExcludedSelf++
			return nil
		}

		// 3) --exclude（AC-05.1）：目录命中即剪整棵子树，不再下探。
		if opts.Excludes.Excluded(relSlash) {
			if d.IsDir() {
				st.PrunedDirs++
				return fs.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			st.Failed++
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// 4) 只收常规文件（TQ-06 / AC-06.5）：link / socket / fifo / device 跳过并计数。
		//    不跟随、不解引用——跟随会翻倍体积并可能成环（node_modules 式链接、worktree 互指）。
		if !isPackedFileMode(info.Mode()) {
			st.LinkSkipped++
			return nil
		}

		if out == nil {
			st.Files++
			st.Bytes += info.Size()
			return nil
		}
		// 同构不变量 2：entry 相对路径与仓库内相对路径逐段相等（AC-02.2）。
		if err := out(top+"/"+relSlash, p, info.Size(), info.Mode()); err != nil {
			var oe errOpenFailed
			if errors.As(err, &oe) {
				st.Failed++ // 单条 entry 打不开：降级为计数，包继续（AC-06.3）
				return nil
			}
			return err // 写流失败：中止整包
		}
		st.Files++
		st.Bytes += info.Size()
		return nil
	})
	if err != nil {
		return st, err
	}
	return st, nil
}

// Write 把 spec 描述的仓库内容写入已打开的 zip 流（Create/Close/Rename 由 cmd 层负责）。
//
// 返回非 nil error 有两种含义：该仓整体不可用（路径不存在/不是目录/不可遍历），
// 或写流中途失败。cmd 层两者都必须废弃临时文件、不产出半包（AC-06.1/06.2）。
// 单条 entry 的"打不开"不升级为 error，只计入 Stats.Failed（AC-06.3）。
func Write(w *zip.Writer, spec RepoSpec, opts Options) (Stats, error) {
	return walk(spec, opts, func(entryName, diskPath string, size int64, mode fs.FileMode) error {
		if strings.HasSuffix(entryName, "/") {
			return writeDirEntry(w, entryName, mode)
		}
		return writeFileEntry(w, entryName, diskPath, mode)
	})
}

// Count 只做只读遍历统计，不产生任何 zip 结构（--dry-run，AC-05.3 / TQ-01）。
func Count(spec RepoSpec, opts Options) (Stats, error) {
	return walk(spec, opts, nil)
}

// errOpenFailed 标记"打开失败"这一类可容忍错误：walk 将其降级为计数并继续。
// 写流失败不用它包装，原样上抛以触发整包作废。
type errOpenFailed struct{ err error }

func (e errOpenFailed) Error() string { return e.err.Error() }
func (e errOpenFailed) Unwrap() error { return e.err }

func writeDirEntry(w *zip.Writer, name string, mode fs.FileMode) error {
	hdr := &zip.FileHeader{Name: name, Method: zip.Store, Modified: safeModTime(time.Now())}
	hdr.SetMode(fs.ModeDir | 0o755 | mode.Perm())
	_, err := w.CreateHeader(hdr)
	return err
}

func writeFileEntry(w *zip.Writer, entryName, diskPath string, mode fs.FileMode) error {
	f, err := os.Open(diskPath)
	if err != nil {
		return errOpenFailed{err}
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return errOpenFailed{err}
	}
	hdr := &zip.FileHeader{Name: entryName, Method: zip.Deflate, Modified: safeModTime(info.ModTime())}
	hdr.SetMode(info.Mode().Perm())
	zw, err := w.CreateHeader(hdr)
	if err != nil {
		return fmt.Errorf("create entry %s: %w", entryName, err)
	}
	if _, err := io.Copy(zw, f); err != nil {
		// entry 已建立却内容不完整：必须整包作废，不能留半条 entry 冒充备份。
		return fmt.Errorf("copy entry %s: %w", entryName, err)
	}
	return nil
}

// isPackedFileMode 是"这条内容是否进包"的唯一判据：只有常规文件进包。
// 抽成函数不是为了复用，而是让 symlink/socket/fifo/device 的排除语义在 Windows
// 无法建符号链接的环境里仍然可被单测钉住（真实 link 用例在那种环境只能 skip）。
func isPackedFileMode(mode fs.FileMode) bool {
	return mode.IsRegular()
}

func cleanedPathSet(paths []string) map[string]bool {
	set := make(map[string]bool, len(paths)*2)
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		set[cleanKey(p)] = true
	}
	return set
}

// cleanKey 归一化到可比较键：绝对化 + Clean + 分隔符统一 + 大小写折叠。
// 折叠是刻意的：Windows 上 `Foo.zip` 与 `foo.zip` 是同一个文件，不折叠会让
// 自吞排除漏掉一半形态（与 TQ-04 同一判据用在同一处）。
func cleanKey(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	return strings.ToLower(filepath.ToSlash(filepath.Clean(abs)))
}
