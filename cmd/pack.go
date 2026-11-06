package cmd

import (
	"archive/zip"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wamdy/gits/internal/archive"
	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/git"
	"github.com/wamdy/gits/internal/i18n"
	"github.com/wamdy/gits/internal/repo"
	"github.com/wamdy/gits/internal/ui"
)

var (
	packGitOnly   bool
	packNoGit     bool
	packOut       string
	packExcludes  []string
	packOverwrite bool
	packDryRun    bool
)

// packCmd 把选中仓库压缩为**一个** zip（Q10：一次调用恒为一个包）。
//
// Args 用 cobra.NoArgs：`gits pack` 注册为真实子命令后不再走 git 透传，
// 多余位置参数（打错的词）必须报错退出非 0——既不能当成 git 参数发出去，
// 也不能静默打 help 退 0（AC-01.7，与 #1064 评审 Q-05 同一取向）。
var packCmd = &cobra.Command{
	Use:   "pack [--git-only | --no-git] [--out <dir | file.zip>] [--exclude <glob>] [--overwrite] [--dry-run]",
	Short: "Pack repositories into a single zip archive",
	Long:  "Pack the selected repositories into one zip archive.",
	Args:  cobra.NoArgs,
	Run:   runPack,
}

// packEntry 是一条"待压仓库"的完整描述：spec 交给 archive 包，label 交给输出，
// pointerGit 记录"这仓的 .git 是 gitdir 指针文件"这一事实（判据取自 git.Classify，
// 不靠路径里出现 worktrees 猜测——ADR-150 红线）。
type packEntry struct {
	spec       archive.RepoSpec
	label      string
	pointerGit bool
}

// packPlan 是阶段 A 预检的产物：一个"已判定可以写"的写入计划。
type packPlan struct {
	entries  []packEntry
	labels   []string
	target   string
	mode     archive.Mode
	matcher  *archive.ExcludeMatcher
	dryRun   bool
	parallel bool
}

// ------------------------------------------------------------------ 纯函数层

// packModeOf 解析两个互斥排除开关（AC-03.3）：同时给出即报错，不定义优先级、不猜意图。
func packModeOf(gitOnly, noGit bool) (archive.Mode, error) {
	if gitOnly && noGit {
		return archive.ModeFull, errors.New(i18n.T("pack_mode_conflict"))
	}
	switch {
	case gitOnly:
		return archive.ModeGitOnly, nil
	case noGit:
		return archive.ModeNoGit, nil
	default:
		return archive.ModeFull, nil
	}
}

// outIsDirForm 判定 --out 走"目录形态"（AC-04.2）。规则唯一，不留给实现现场判断：
// 空值（缺省 cwd）、以路径分隔符结尾、或磁盘上已存在的目录 → 目录形态；否则按文件路径。
// existsAsDir 由调用方注入，使本函数可表驱动单测（不依赖真实文件系统）。
func outIsDirForm(out string, existsAsDir bool) bool {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return true
	}
	if strings.HasSuffix(trimmed, "/") || strings.HasSuffix(trimmed, `\`) {
		return true
	}
	return existsAsDir
}

// resolvePackTarget 计算目标包绝对路径。目录形态用缺省包名，文件形态完全按用户给的名字
// （后缀原样保留、不补 .zip——用户写了什么路径就该出现什么路径）。
func resolvePackTarget(out, cwd, fileName string) string {
	trimmed := strings.TrimSpace(out)
	if !outIsDirForm(trimmed, dirExists(trimmed)) {
		return absPathOrSelf(trimmed)
	}
	dir := trimmed
	if dir == "" {
		dir = cwd
	}
	return absPathOrSelf(filepath.Join(dir, fileName))
}

func absPathOrSelf(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// packFileName 目录形态的缺省包名（AC-04.3）：单仓带仓名与分支值，多仓统一 gits-pack-<时间戳>。
func packFileName(entries []packEntry, ts time.Time) string {
	stamp := ts.Format("20060102-150405")
	if len(entries) == 1 {
		return fmt.Sprintf("%s__%s__%s.zip",
			archive.SanitizeName(entries[0].spec.TopDir),
			archive.SanitizeName(entries[0].spec.Branch), stamp)
	}
	return "gits-pack-" + stamp + ".zip"
}

// gitKindName 把 git.Classify 的三态结果收敛成进 manifest 的稳定字符串。
func gitKindName(kind git.GitEntryKind) string {
	switch kind {
	case git.KindRepo:
		return "repo"
	case git.KindLinkedWorktree:
		return "linked-worktree"
	case git.KindSubmodule:
		return "submodule"
	default:
		return "not-git"
	}
}

// packBranchToken 给出进包名与 manifest 的分支值（TQ-03 三态）。
// 复用 repo.DisplayBranch 的既有口径（detached HEAD 与空仓一律退回短 commit），不另立一套判定，
// 避免同一份"当前在哪个提交"在两个命令里说法不同；连短 commit 都取不到（空仓）时用 nobranch。
func packBranchToken(path string) string {
	if v := repo.DisplayBranch(git.GetBranchName(path), path); v != "" {
		return v
	}
	return "nobranch"
}

// packNameConflicts 返回包内顶层目录名冲突项（Q11 + TQ-04）。
// 判定大小写不敏感，并额外把 manifest 保留名一起纳入——某仓目录恰好叫
// gits-pack-manifest.json 时，包根会出现两个同名 entry，解出来只剩一个。
func packNameConflicts(entries []packEntry) []string {
	names := make([]string, 0, len(entries)+1)
	for _, e := range entries {
		names = append(names, e.spec.TopDir)
	}
	return archive.CaseFoldDuplicates(append(names, archive.ManifestName))
}

// packConflictReport 逐条给冲突文案（保留名冲突与普通重名的处置方式不同）。
func packConflictReport(dups []string) string {
	lines := make([]string, 0, len(dups))
	for _, d := range dups {
		if strings.EqualFold(d, archive.ManifestName) {
			lines = append(lines, i18n.T("pack_manifest_name_conflict", d))
			continue
		}
		lines = append(lines, i18n.T("pack_topdir_conflict", d))
	}
	return strings.Join(lines, "\n")
}

func labelsOf(entries []packEntry) []string {
	labels := make([]string, 0, len(entries))
	for _, e := range entries {
		labels = append(labels, e.label)
	}
	return labels
}

// ------------------------------------------------------------------ 阶段 A：只读预检

// buildPackEntries 逐仓采集 spec 并收集预检问题；全程只读，不产生任何文件（AC-06.1）。
// 仓级失败全部在这里判掉，写入期只剩"单条 entry 读不到"与"写流失败"两种情况（AC-06.3）。
func buildPackEntries(repos []repo.RepositoryInfo, labels []string) ([]packEntry, []string) {
	entries := make([]packEntry, 0, len(repos))
	issues := make([]string, 0)
	for i, r := range repos {
		label := r.Name
		if i < len(labels) {
			label = labels[i]
		}
		if strings.TrimSpace(r.Path) == "" {
			issues = append(issues, i18n.T("pack_issue_path_missing", label))
			continue
		}
		info, err := os.Stat(r.Path)
		if err != nil {
			issues = append(issues, i18n.T("pack_issue_path_missing", label))
			continue
		}
		if !info.IsDir() {
			issues = append(issues, i18n.T("pack_issue_not_dir", label))
			continue
		}
		kind := git.Classify(r.Path)
		if kind == git.KindNotGit {
			issues = append(issues, i18n.T("pack_issue_not_repo", label))
			continue
		}
		entries = append(entries, packEntry{
			spec: archive.RepoSpec{
				ID: r.ID, Name: r.Name, Path: r.Path,
				TopDir: archive.TopDir(r.Path), Branch: packBranchToken(r.Path),
				GitKind: gitKindName(kind),
			},
			label:      label,
			pointerGit: kind == git.KindLinkedWorktree,
		})
	}
	return entries, issues
}

// preparePack 跑完阶段 A 的全部只读判定，产出写入计划或错误。
// 拆成函数而不是埋在 runPack 里，是为了让"预检失败零产物"能被单测直接钉住
// （runPack 走 os.Exit，测试拿不到中间态）。
func preparePack(cfg *config.Config) (packPlan, error) {
	var plan packPlan

	mode, err := packModeOf(packGitOnly, packNoGit)
	if err != nil {
		return plan, err
	}
	matcher, err := archive.CompileExcludes(packExcludes)
	if err != nil {
		return plan, err
	}

	repos, err := selectRepos(GetRepoIDs(), GetGroup(), cfg)
	if errors.Is(err, errNoRepositories) {
		ui.PrintNoConfigPrompt()
		return plan, errors.New(i18n.T("no_repositories"))
	}
	if err != nil {
		// AC-01.2：选择失败必须非零退出。批量 git 命令的"选择失败仍退 0"是存量缺陷（#1068），
		// 本票新命令无历史用户，直接按正确语义实现，不继承该缺陷。
		return plan, err
	}
	if len(repos) == 0 {
		return plan, errors.New(i18n.T("pack_no_repositories_selected"))
	}

	entries, issues := buildPackEntries(repos, repo.RepoLabels(repos, cfg.Repositories))
	if len(issues) > 0 {
		for _, line := range issues {
			ui.PrintMessage(line)
		}
		return plan, fmt.Errorf("%s", i18n.T("pack_precheck_failed", len(issues)))
	}
	if dups := packNameConflicts(entries); len(dups) > 0 {
		ui.PrintMessage(packConflictReport(dups))
		return plan, fmt.Errorf("%s", i18n.T("pack_precheck_failed", len(dups)))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return plan, err
	}
	plan.entries = entries
	plan.labels = labelsOf(entries)
	plan.target = resolvePackTarget(packOut, cwd, packFileName(entries, time.Now()))
	plan.mode = mode
	plan.matcher = matcher
	plan.dryRun = packDryRun
	plan.parallel = GetParallel()
	return plan, nil
}

// coverAllowed 处理"目标包已存在"的三态（AC-04.4/04.5/04.6）。
//
// stdio 与提问函数都由调用方注入：非 TTY 分支绝不能真的去读 stdin——提问会让管道与 CI
// 挂死，静默覆盖又会抹掉既有备份，两者都不可接受，所以直接返回错误让上层非零退出（TQ-02）。
// 拆成纯逻辑而非埋在 runPack 里，四个状态才测得到。
func coverAllowed(target string, overwrite, yes, stdinTTY bool, ask func(string, int64, time.Time) bool) (allowed, skipped bool, err error) {
	info, statErr := os.Stat(target)
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return true, false, nil // 不存在 → 直接写
		}
		// 取不到信息（权限、被占用）不当作"不存在"放行：交给写入期报错处理，
		// 避免"预检以为不存在 → Create 撞上去"的假绿路径。
		return true, false, nil
	}
	if overwrite || yes {
		return true, false, nil
	}
	if stdinTTY {
		if ask(target, info.Size(), info.ModTime()) {
			return true, false, nil
		}
		return false, true, nil // 用户主动跳过：不是失败
	}
	return false, false, errors.New(i18n.T("pack_cover_non_tty", target))
}

// ------------------------------------------------------------------ 主流程

func runPack(cmd *cobra.Command, args []string) {
	cfg, err := config.Load()
	if err != nil {
		exitOnError(err)
	}

	// TQ-07：单包只有一条写流，-p 无语义可给；显式告知忽略而不是静默丢弃。
	if GetParallel() {
		ui.PrintMessage(i18n.T("pack_parallel_ignored"))
	}

	plan, err := preparePack(cfg)
	if err != nil {
		exitOnError(err)
	}

	if plan.dryRun {
		exitOnError(runPackDryRun(plan))
	}

	if !GetYes() && !ui.PrintPackReview(plan.labels, plan.mode, plan.target, plan.matcher.Patterns()) {
		ui.PrintMessage(i18n.T("operation_cancelled"))
		return
	}

	// coverAllowed 的三个返回值互斥：err != nil 即"非 TTY 且已存在"（已在此退出），
	// skipped 即"用户答不"，其余情况一定允许写入。
	_, skipped, err := coverAllowed(plan.target, packOverwrite, GetYes(), ui.StdinIsTerminal(), ui.AskPackOverwrite)
	if err != nil {
		exitOnError(err)
	}
	if skipped {
		ui.PrintMessage(i18n.T("pack_cover_skipped", plan.target))
		ui.PrintPackSummary(0, 0, 1)
		return // 用户主动跳过不是失败（AC-06.7）
	}

	succeeded, failed, fatal := writePackArchive(plan)
	if fatal != "" {
		exitOnError(errors.New(fatal))
	}
	size := int64(0)
	if info, err := os.Stat(plan.target); err == nil {
		size = info.Size()
	}
	ui.PrintMessage(i18n.T("pack_archive_line", plan.target, ui.HumanBytes(size), len(plan.entries)))
	ui.PrintPackSummary(succeeded, failed, 0)
	if failed > 0 {
		os.Exit(1)
	}
}

// runPackDryRun 只读预估（AC-05.3 / TQ-01）：不建目录、不提问、不写盘。
// 返回非 nil error 表示有仓在预检之后仍读不动，由调用方以非零码退出。
func runPackDryRun(plan packPlan) error {
	exists := i18n.T("pack_exists_absent")
	if _, err := os.Stat(plan.target); err == nil {
		exists = i18n.T("pack_exists_present")
	}
	ui.PrintMessage(i18n.T("pack_dry_run_header", plan.target, exists))

	opts := archive.Options{Mode: plan.mode, Excludes: plan.matcher,
		SelfPaths: []string{plan.target, plan.target + ".tmp"}}
	totalFiles, totalBytes, failed := 0, int64(0), 0
	for _, e := range plan.entries {
		st, err := archive.Count(e.spec, opts)
		if err != nil {
			ui.PrintPackResultRow(e.label, st, false, err.Error())
			failed++
			continue
		}
		ui.PrintPackDryRunRow(e.label, st)
		totalFiles += st.Files
		totalBytes += st.Bytes
	}
	ui.PrintMessage(i18n.T("pack_dry_run_total", len(plan.entries), totalFiles, ui.HumanBytes(totalBytes)))
	if failed > 0 {
		return fmt.Errorf("%s", i18n.T("pack_precheck_failed", failed))
	}
	return nil
}

// writePackArchive 串行写单个 zip：临时文件写满再 rename（AC-04.7，沿 config.Save 的原子写口径）。
// 返回 (成功仓数, 失败仓数, 致命错误文案)；致命错误非空时磁盘上不留下任何半成品包。
func writePackArchive(plan packPlan) (int, int, string) {
	tmp := plan.target + ".tmp"
	if err := os.MkdirAll(filepath.Dir(plan.target), 0o755); err != nil {
		return 0, 0, i18n.T("pack_create_failed", filepath.Dir(plan.target), err)
	}
	f, err := os.Create(tmp)
	if err != nil {
		return 0, 0, i18n.T("pack_create_failed", tmp, err)
	}
	w := zip.NewWriter(f)
	mani := archive.NewManifest(plan.mode, plan.target, plan.matcher.Patterns(), version)
	opts := archive.Options{Mode: plan.mode, Excludes: plan.matcher, SelfPaths: []string{plan.target, tmp}}

	// abort 关流并删临时文件；Windows 的 rename 才具备替换语义，所以覆盖路径不靠"先删再赌"。
	abort := func(msg string) (int, int, string) {
		_ = w.Close()
		_ = f.Close()
		_ = os.Remove(tmp)
		return 0, 0, msg
	}

	succeeded, failed := 0, 0
	for _, e := range plan.entries {
		rec := archive.RepoRecord{
			ID: e.spec.ID, Name: e.spec.Name, SourcePath: e.spec.Path,
			TopDir: e.spec.TopDir, Branch: e.spec.Branch, GitKind: e.spec.GitKind,
		}
		st, err := archive.Write(w, e.spec, opts)
		rec.Files, rec.Bytes = st.Files, st.Bytes
		rec.SkippedFiles, rec.FailedFiles = st.LinkSkipped, st.Failed
		rec.ExcludedSelf, rec.PrunedDirs = st.ExcludedSelf, st.PrunedDirs
		if err != nil {
			// 写流半途失败或整仓不可遍历：整包作废（AC-06.2）。
			// 半途截断的 entry 留在"备份"里比整包失败更坏——它看起来是成功的那一个。
			rec.Error = err.Error()
			mani.AddRecord(rec)
			ui.PrintPackResultRow(e.label, st, false, err.Error())
			return abort(i18n.T("pack_write_failed", err.Error()))
		}
		rec.OK = true
		mani.AddRecord(rec)
		ui.PrintPackResultRow(e.label, st, true, "")
		if e.pointerGit && plan.mode == archive.ModeGitOnly {
			// AC-03.6：原样压 worktree 的限制必须可见，但不阻断（Q4 裁决）。
			ui.PrintMessage(i18n.T("pack_worktree_pointer_note", e.label))
		}
		succeeded++
	}

	if err := mani.WriteTo(w); err != nil {
		return abort(i18n.T("pack_manifest_failed", err.Error()))
	}
	if err := w.Close(); err != nil {
		return abort(i18n.T("pack_write_failed", err.Error()))
	}
	if err := f.Close(); err != nil {
		return abort(i18n.T("pack_write_failed", err.Error()))
	}
	if err := os.Rename(tmp, plan.target); err != nil {
		return abort(i18n.T("pack_write_failed", err.Error()))
	}
	return succeeded, failed, ""
}

// ------------------------------------------------------------------ 注册与 i18n

func initPackCmdI18n() {
	packCmd.Short = i18n.T("pack_cmd")
	packCmd.Long = i18n.T("pack_cmd_long") + "\n\n" +
		i18n.T("pack_passthrough_note") + "\n" + i18n.T("pack_yes_overwrite_warning")

	for name, key := range map[string]string{
		"git-only":  "flag_pack_git_only",
		"no-git":    "flag_pack_no_git",
		"out":       "flag_pack_out",
		"exclude":   "flag_pack_exclude",
		"overwrite": "flag_pack_overwrite",
		"dry-run":   "flag_pack_dry_run",
	} {
		if f := packCmd.Flags().Lookup(name); f != nil {
			f.Usage = i18n.T(key)
		}
	}
}

func init() {
	packCmd.Flags().BoolVar(&packGitOnly, "git-only", false, i18n.T("flag_pack_git_only"))
	packCmd.Flags().BoolVar(&packNoGit, "no-git", false, i18n.T("flag_pack_no_git"))
	packCmd.Flags().StringVar(&packOut, "out", "", i18n.T("flag_pack_out"))
	// StringArray（非 StringSlice）：`--exclude a --exclude b` 与 `--exclude a,b` 两种写法都吃，
	// 逗号展开统一在 CompileExcludes 里做，避免 cobra 先把含逗号的合法 glob 拆坏。
	packCmd.Flags().StringArrayVar(&packExcludes, "exclude", nil, i18n.T("flag_pack_exclude"))
	packCmd.Flags().BoolVar(&packOverwrite, "overwrite", false, i18n.T("flag_pack_overwrite"))
	packCmd.Flags().BoolVar(&packDryRun, "dry-run", false, i18n.T("flag_pack_dry_run"))
	initPackCmdI18n()
}
