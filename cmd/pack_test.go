package cmd

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wamdy/gits/internal/archive"
	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/repo"
)

// ------------------------------------------------------------------ 脚手架

// resetPackFlags 把 pack 的包级 flag 变量复位，并在测试结束后恢复。
// pack 的纯逻辑函数按取值读这些变量（与 cobra 一致），测试必须自己管住它们，
// 否则同包用例之间会串状态——那种"单独跑绿、一起跑红"的假象最难查。
func resetPackFlags(t *testing.T) {
	t.Helper()
	oldGitOnly, oldNoGit, oldOut := packGitOnly, packNoGit, packOut
	oldExcludes, oldOverwrite, oldDryRun := packExcludes, packOverwrite, packDryRun
	oldRepoIDs, oldGroup, oldParallel := repoIDs, group, parallel
	t.Cleanup(func() {
		packGitOnly, packNoGit, packOut = oldGitOnly, oldNoGit, oldOut
		packExcludes, packOverwrite, packDryRun = oldExcludes, oldOverwrite, oldDryRun
		repoIDs, group, parallel = oldRepoIDs, oldGroup, oldParallel
	})
	packGitOnly, packNoGit, packOut = false, false, ""
	packExcludes, packOverwrite, packDryRun = nil, false, false
	repoIDs, group, parallel = "", "", false
}

// newGitRepo 在 parent 下建一个真 git 仓库并至少提交一次（requireGit 保护下调用）。
func newGitRepo(t *testing.T, parent, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "seed")
	return dir
}

// ------------------------------------------------------------------ 纯函数

func TestPackModeOfRejectsMutualExclusion(t *testing.T) {
	cases := []struct {
		gitOnly, noGit bool
		want           archive.Mode
		wantErr        bool
	}{
		{false, false, archive.ModeFull, false},
		{true, false, archive.ModeGitOnly, false},
		{false, true, archive.ModeNoGit, false},
		{true, true, archive.ModeFull, true}, // AC-03.3：互斥，不猜优先级
	}
	for _, c := range cases {
		got, err := packModeOf(c.gitOnly, c.noGit)
		if c.wantErr {
			if err == nil {
				t.Errorf("packModeOf(%v,%v) must error", c.gitOnly, c.noGit)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("packModeOf(%v,%v) = %v,%v want %v", c.gitOnly, c.noGit, got, err, c.want)
		}
	}
}

func TestOutIsDirFormSingleRule(t *testing.T) {
	cases := []struct {
		out     string
		exists  bool
		wantDir bool
		remark  string
	}{
		{"", false, true, "缺省即目录（cwd）"},
		{"bk", false, false, "不存在且不以分隔符结尾 → 文件形态（AC-04.2 的易踩点）"},
		{"bk", true, true, "已存在目录 → 目录形态"},
		{"bk/", false, true, "尾斜杠表达目录意图"},
		{`bk\`, false, true, "尾反斜杠同样算目录意图"},
		{"bk/a.zip", false, false, "具体文件路径"},
		{"  ", false, true, "纯空白等同未给"},
	}
	for _, c := range cases {
		if got := outIsDirForm(c.out, c.exists); got != c.wantDir {
			t.Errorf("outIsDirForm(%q,%v) = %v want %v（%s）", c.out, c.exists, got, c.wantDir, c.remark)
		}
	}
}

func TestResolvePackTargetForms(t *testing.T) {
	resetPackFlags(t)
	cwd := t.TempDir()
	existingDir := filepath.Join(cwd, "already")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// 目录形态：缺省落 cwd，包名自动
	got := resolvePackTarget("", cwd, "repoA__main__20261106-120000.zip")
	if filepath.Dir(got) != filepath.Clean(cwd) || filepath.Base(got) != "repoA__main__20261106-120000.zip" {
		t.Errorf("缺省目录形态结果 = %q（AC-04.1 要求落 cwd）", got)
	}
	// 目录形态：显式已存在目录
	got = resolvePackTarget(existingDir, cwd, "x.zip")
	if got != filepath.Join(existingDir, "x.zip") {
		t.Errorf("已存在目录应拼包名，得到 %q", got)
	}
	// 文件形态：后缀原样保留，不补 .zip
	got = resolvePackTarget(filepath.Join(cwd, "custom-archive"), cwd, "ignored.zip")
	if filepath.Base(got) != "custom-archive" {
		t.Errorf("文件形态必须完全按用户给的路径，得到 %q（Q8）", got)
	}
	// 目录形态（尾分隔符）在不存在的目录上也生效
	got = resolvePackTarget(filepath.Join(cwd, "newdir")+string(filepath.Separator), cwd, "y.zip")
	if filepath.Base(got) != "y.zip" {
		t.Errorf("尾分隔符形态应拼自动包名，得到 %q", got)
	}
}

func TestPackFileNameSingleVsMulti(t *testing.T) {
	ts := time.Date(2026, 11, 6, 12, 34, 56, 0, time.UTC)
	single := []packEntry{{spec: archive.RepoSpec{TopDir: "gits", Branch: "feature/x"}}}
	got := packFileName(single, ts)
	if !strings.HasPrefix(got, "gits__feature-x__2026") || !strings.HasSuffix(got, ".zip") {
		t.Errorf("单仓库名 = %q，要求 仓名__分支(清洗)__时间戳（AC-04.3/TQ-03）", got)
	}
	if strings.Contains(got, "feature/x") {
		t.Errorf("分支里的 / 必须清洗掉，否则路径会多出一层目录: %q", got)
	}
	multi := []packEntry{{spec: archive.RepoSpec{TopDir: "a"}}, {spec: archive.RepoSpec{TopDir: "b"}}}
	if got := packFileName(multi, ts); !strings.HasPrefix(got, "gits-pack-2026") {
		t.Errorf("多仓库名 = %q，要求 gits-pack-<时间戳>.zip（Q10）", got)
	}
	// 空仓（分支值 nobranch）不得产出连续分隔符或空段
	empty := []packEntry{{spec: archive.RepoSpec{TopDir: "e", Branch: "nobranch"}}}
	if got := packFileName(empty, ts); got != "e__nobranch__20261106-123456.zip" {
		t.Errorf("空仓包名 = %q", got)
	}
}

func TestPackNameConflictsIncludingManifestReservedName(t *testing.T) {
	// TQ-04：大小写不同也必须判冲突——zip 里两个 entry，解出来一个目录
	entries := []packEntry{
		{spec: archive.RepoSpec{TopDir: "Repo"}},
		{spec: archive.RepoSpec{TopDir: "other"}},
		{spec: archive.RepoSpec{TopDir: "repo"}},
	}
	dups := packNameConflicts(entries)
	if len(dups) != 1 || !strings.EqualFold(dups[0], "repo") {
		t.Fatalf("冲突集 = %v, want 一组 repo 形态（Q11）", dups)
	}
	if got := packConflictReport(dups); !strings.Contains(got, "Repo") && !strings.Contains(got, "repo") {
		t.Errorf("冲突文案未点名: %q", got)
	}

	// 保留名撞车：某仓目录恰好叫 gits-pack-manifest.json
	reserved := []packEntry{{spec: archive.RepoSpec{TopDir: archive.ManifestName}}}
	rp := packNameConflicts(reserved)
	if len(rp) != 1 || !strings.EqualFold(rp[0], archive.ManifestName) {
		t.Fatalf("保留名冲突未检出: %v", rp)
	}
	if !strings.Contains(packConflictReport(rp), "清单") && !strings.Contains(packConflictReport(rp), "manifest") {
		t.Errorf("保留名文案应区别于普通重名: %q", packConflictReport(rp))
	}

	clean := []packEntry{{spec: archive.RepoSpec{TopDir: "a"}}, {spec: archive.RepoSpec{TopDir: "b"}}}
	if got := packNameConflicts(clean); len(got) != 0 {
		t.Errorf("不冲突时误报: %v", got)
	}
}

// ------------------------------------------------------------------ 预检零副作用

func TestPreparePackConflictWritesNothing(t *testing.T) {
	resetPackFlags(t)
	requireGit(t)

	base := t.TempDir()
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 两个不同父目录、同名 repoA —— 包内顶层目录必然撞车
	a := newGitRepo(t, filepath.Join(base, "one"), "repoA", map[string]string{"a.txt": "A"})
	b := newGitRepo(t, filepath.Join(base, "two"), "repoA", map[string]string{"b.txt": "B"})

	cfg := &config.Config{Repositories: []config.Repository{
		{ID: 1, Name: "one-A", Path: a, Groups: []string{}},
		{ID: 2, Name: "two-A", Path: b, Groups: []string{}},
	}}
	repoIDs = "1,2"
	packOut = outDir + string(filepath.Separator)

	plan, err := preparePack(cfg)
	if err == nil {
		t.Fatalf("重名必须预检失败（Q11），却拿到计划 %+v", plan)
	}
	entries, _ := os.ReadDir(outDir)
	if len(entries) != 0 {
		t.Errorf("预检失败后输出目录必须为空（AC-06.1 零副作用），实得 %d 项", len(entries))
	}
}

func TestPreparePackRejectsNonGitPathWithoutWriting(t *testing.T) {
	resetPackFlags(t)
	requireGit(t)

	base := t.TempDir()
	outDir := filepath.Join(base, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	good := newGitRepo(t, base, "good", map[string]string{"g.txt": "G"})
	plain := filepath.Join(base, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatalf("mkdir plain: %v", err)
	}

	cfg := &config.Config{Repositories: []config.Repository{
		{ID: 1, Name: "good", Path: good, Groups: []string{}},
		{ID: 2, Name: "plain", Path: plain, Groups: []string{}},
	}}
	repoIDs = "1,2"
	packOut = outDir + string(filepath.Separator)

	if _, err := preparePack(cfg); err == nil {
		t.Fatal("非 git 仓必须预检失败（AC-06.1）")
	}
	if entries, _ := os.ReadDir(outDir); len(entries) != 0 {
		t.Errorf("预检失败后不得留下任何文件，实得 %d 项", len(entries))
	}
}

func TestBuildPackEntriesClassifiesWorktree(t *testing.T) {
	resetPackFlags(t)
	requireGit(t)

	base := t.TempDir()
	main := newGitRepo(t, base, "main-repo", map[string]string{"m.txt": "M"})
	wt := filepath.Join(base, ".worktrees", "demo", "main-repo")
	gitIn(t, main, "worktree", "add", "-q", wt, "-b", "feat/pack")

	infos := []repo.RepositoryInfo{
		{ID: 1, Name: "main-repo", Path: main},
		{ID: 2, Name: "main-repo@demo", Path: wt},
	}
	entries, issues := buildPackEntries(infos, []string{"[1] main-repo (main)", "[2] main-repo@demo (feat/pack)"})
	if len(issues) != 0 {
		t.Fatalf("两个都是合法 git 目录，不应有预检问题: %v", issues)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].pointerGit {
		t.Error("主仓不该判成 linked worktree")
	}
	if !entries[1].pointerGit {
		t.Error("linked worktree 必须被 git.Classify 判出（AC-03.6 的提示依据，不得靠路径猜）")
	}
	if entries[1].spec.GitKind != "linked-worktree" {
		t.Errorf("GitKind = %q, want linked-worktree", entries[1].spec.GitKind)
	}
	// 两个目录 basename 相同（.worktrees/demo/main-repo 与 main-repo 不同名，此处按实际路径断言）
	if entries[0].spec.TopDir == "main-repo" && entries[1].spec.TopDir == "main-repo" {
		t.Log("主仓与 worktree 的 basename 相同 —— 单包内会撞车，冲突预检必须拦住")
		if len(packNameConflicts(entries)) != 1 {
			t.Errorf("同 basename 的主仓+worktree 必须判冲突，实得 %v", packNameConflicts(entries))
		}
	}
	if entries[0].spec.Branch == "" || entries[1].spec.Branch == "" {
		t.Errorf("分支值必须解析为三态之一，实得 %q / %q", entries[0].spec.Branch, entries[1].spec.Branch)
	}
}

// ------------------------------------------------------------------ 覆盖三态

func TestCoverAllowedFourStates(t *testing.T) {
	resetPackFlags(t)
	dir := t.TempDir()
	absent := filepath.Join(dir, "absent.zip")
	present := filepath.Join(dir, "present.zip")
	if err := os.WriteFile(present, []byte("existing backup"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	asked := false
	askYes := func(string, int64, time.Time) bool { asked = true; return true }
	askNo := func(string, int64, time.Time) bool { asked = true; return false }

	// 1) 目标不存在 → 直接写
	if ok, skip, err := coverAllowed(absent, false, false, false, askYes); !ok || skip || err != nil {
		t.Errorf("absent target: (%v,%v,%v) want (true,false,nil)", ok, skip, err)
	}
	// 2) 存在 + --overwrite → 不问直接覆盖
	asked = false
	if ok, _, err := coverAllowed(present, true, false, true, askNo); !ok || err != nil || asked {
		t.Errorf("overwrite: (%v,%v) asked=%v want (true,nil) 且不提问", ok, err, asked)
	}
	// 3) 存在 + -y → 等同批准覆盖（AC-04.6 的副作用）
	asked = false
	if ok, _, err := coverAllowed(present, false, true, true, askNo); !ok || err != nil || asked {
		t.Errorf("-y: (%v,%v) asked=%v want (true,nil) 且不提问", ok, err, asked)
	}
	// 4) 存在 + TTY + 用户答 y → 覆盖；答 N → skipped（不是失败）
	asked = false
	if ok, skip, err := coverAllowed(present, false, false, true, askYes); !ok || skip || err != nil || !asked {
		t.Errorf("tty+yes: (%v,%v,%v) asked=%v", ok, skip, err, asked)
	}
	asked = false
	if ok, skip, err := coverAllowed(present, false, false, true, askNo); ok || !skip || err != nil || !asked {
		t.Errorf("tty+no: (%v,%v,%v) asked=%v want (false,true,nil)", ok, skip, err, asked)
	}
	// 5) 存在 + 非 TTY + 无 --overwrite → 报错，绝不提问（TQ-02）
	asked = false
	if ok, skip, err := coverAllowed(present, false, false, false, askYes); ok || skip || err == nil || asked {
		t.Errorf("non-tty: (%v,%v,%v) asked=%v want 报错且不提问", ok, skip, err, asked)
	}
	if err := os.Remove(present); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(present); !errorsIsNotExist(err) {
		t.Error("用例前提：present.zip 已删除")
	}
}

func errorsIsNotExist(err error) bool { return err != nil && os.IsNotExist(err) }

// ------------------------------------------------------------------ 端到端（单包）

func TestWritePackArchiveProducesIsomorphicSinglePackage(t *testing.T) {
	resetPackFlags(t)
	requireGit(t)

	base := t.TempDir()
	outDir := filepath.Join(base, "out")
	a := newGitRepo(t, base, "alpha", map[string]string{"README.md": "A", "src/a.go": "package a"})
	b := newGitRepo(t, base, "beta", map[string]string{"src/b.go": "package b"})

	cfg := &config.Config{Repositories: []config.Repository{
		{ID: 1, Name: "alpha", Path: a, Groups: []string{}},
		{ID: 2, Name: "beta", Path: b, Groups: []string{}},
	}}
	repoIDs = "1,2"
	packOut = outDir + string(filepath.Separator)

	plan, err := preparePack(cfg)
	if err != nil {
		t.Fatalf("preparePack: %v", err)
	}
	succeeded, failed, fatal := writePackArchive(plan)
	if fatal != "" {
		t.Fatalf("writePackArchive fatal: %s", fatal)
	}
	if succeeded != 2 || failed != 0 {
		t.Errorf("counts = (%d,%d) want (2,0)", succeeded, failed)
	}

	// 目录形态 + 多仓 → 唯一产物是 gits-pack-<ts>.zip（Q10）
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("目录形态多仓必须只产出 1 个包（Q10），实得 %v", names)
	}
	if !strings.HasPrefix(entries[0].Name(), "gits-pack-") {
		t.Errorf("包名 = %q, want gits-pack-<ts>.zip", entries[0].Name())
	}
	zipPath := filepath.Join(outDir, entries[0].Name())
	if _, err := os.Stat(zipPath + ".tmp"); !os.IsNotExist(err) {
		t.Error("临时文件必须在 rename 后消失（AC-04.7）")
	}

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer r.Close()

	names := map[string]bool{}
	for _, f := range r.File {
		names[f.Name] = true
	}
	// AC-01.3 / AC-02.1/02.2：一次调用一个包，包内两仓并列、相对路径保真
	for _, want := range []string{"alpha/README.md", "alpha/src/a.go", "alpha/.git/config",
		"beta/src/b.go", "beta/.git/config", archive.ManifestName} {
		if !names[want] {
			t.Errorf("包内缺 entry %q；实得 %d 条", want, len(names))
		}
	}

	// manifest 内容可读回且与实际包体一致（AC-05.4）
	for _, f := range r.File {
		if f.Name != archive.ManifestName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open manifest: %v", err)
		}
		var mani archive.Manifest
		if err := json.NewDecoder(rc).Decode(&mani); err != nil {
			t.Fatalf("decode manifest: %v", err)
		}
		rc.Close()
		if mani.Summary.Repos != 2 || mani.Summary.Succeeded != 2 {
			t.Errorf("manifest summary = %+v", mani.Summary)
		}
		if mani.Mode != "full" {
			t.Errorf("manifest mode = %q, want full", mani.Mode)
		}
		if mani.Out == "" || filepath.Base(mani.Out) != entries[0].Name() {
			t.Errorf("manifest out = %q, want 真实包路径", mani.Out)
		}
		for _, rec := range mani.Repos {
			if rec.SourcePath == "" || rec.TopDir == "" || rec.Branch == "" || rec.GitKind != "repo" {
				t.Errorf("manifest 记录字段不全: %+v", rec)
			}
		}
	}
}

func TestPreparePackGitOnlyOnWorktreeStillPlansEntry(t *testing.T) {
	resetPackFlags(t)
	requireGit(t)

	base := t.TempDir()
	main := newGitRepo(t, base, "main-repo", map[string]string{"m.txt": "M"})
	wtName := "wt-copy"
	wt := filepath.Join(base, ".worktrees", "demo", wtName)
	gitIn(t, main, "worktree", "add", "-q", wt, "-b", "feat/pack")

	// 用不同 basename 建两仓，避免撞车；断言 worktree 条目被识别为 linked-worktree
	cfg := &config.Config{Repositories: []config.Repository{
		{ID: 1, Name: "main-repo", Path: main, Groups: []string{}},
		{ID: 2, Name: "wt-copy", Path: wt, Groups: []string{}},
	}}
	repoIDs = "1,2"
	packGitOnly = true

	plan, err := preparePack(cfg)
	if err != nil {
		t.Fatalf("preparePack: %v", err)
	}
	if plan.mode != archive.ModeGitOnly {
		t.Errorf("mode = %v", plan.mode)
	}
	var pointer bool
	for _, e := range plan.entries {
		if e.pointerGit {
			pointer = true
		}
	}
	if !pointer {
		t.Error("选择集里的 linked worktree 必须被标出（AC-03.6 提示依据）")
	}
}

// ------------------------------------------------------------------ dry-run

func TestDryRunWritesNothing(t *testing.T) {
	resetPackFlags(t)
	requireGit(t)

	base := t.TempDir()
	outDir := filepath.Join(base, "out")
	a := newGitRepo(t, base, "alpha", map[string]string{"a.txt": "A"})
	cfg := &config.Config{Repositories: []config.Repository{
		{ID: 1, Name: "alpha", Path: a, Groups: []string{}},
	}}
	repoIDs = "1"
	packOut = outDir + string(filepath.Separator)
	packDryRun = true

	plan, err := preparePack(cfg)
	if err != nil {
		t.Fatalf("preparePack: %v", err)
	}
	if !plan.dryRun {
		t.Fatal("dry-run 开关未进计划")
	}
	if err := runPackDryRun(plan); err != nil {
		t.Fatalf("runPackDryRun: %v", err)
	}
	// TQ-01：连输出目录都不该被创建
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Errorf("dry-run 不得创建目录或写盘（AC-05.3），outDir 状态 err=%v", err)
	}
}

// TestShouldWarnPointerGitAcrossModes 钉住 worktree 指针提示的三态：
// 全量与 --git-only 都把 .git 装进了包 → 必须提示；--no-git 没装 → 不许提示。
// 原实现只在 --git-only 提示，默认（全量）形态解包后才发现包不可独立恢复。
func TestShouldWarnPointerGitAcrossModes(t *testing.T) {
	cases := []struct {
		mode       archive.Mode
		pointerGit bool
		want       bool
		remark     string
	}{
		{archive.ModeFull, true, true, "全量装了 .git"},
		{archive.ModeGitOnly, true, true, "只压 .git"},
		{archive.ModeNoGit, true, false, "没装 .git，提示是噪音"},
		{archive.ModeFull, false, false, "普通仓不提示"},
		{archive.ModeGitOnly, false, false, "普通仓不提示"},
	}
	for _, c := range cases {
		if got := shouldWarnPointerGit(c.mode, c.pointerGit); got != c.want {
			t.Errorf("shouldWarnPointerGit(%v,%v) = %v, want %v（%s）", c.mode, c.pointerGit, got, c.want, c.remark)
		}
	}
}

// ------------------------------------------------------------------ flag 契约

// TestExitOnErrorIgnoresNil 钉住 exitOnError 的 nil 守卫。
// 本用例的价值在于「它能跑完」：exitOnError(nil) 若仍按旧写法无条件 os.Exit(1)，
// 测试进程会当场以退出码 1 死掉（表现为整包 FAIL），而不是断言失败。
// 起因是实机验证抓到 `gits pack --dry-run` 每次都报「错误: <nil>」并退 1，
// 单测全绿而命令恒失败——dry-run 的返回值被直接喂给了当时无条件的 exitOnError。
func TestExitOnErrorIgnoresNil(t *testing.T) {
	exitOnError(nil)
}

func TestPackRejectsStrayArgs(t *testing.T) {
	// AC-01.7：`gits pack bogus` 不能被当成 git 参数透传，也不能静默打 help 退 0
	if packCmd.Args == nil {
		t.Fatal("packCmd.Args 未设置")
	}
	if err := packCmd.Args(packCmd, []string{"bogus"}); err == nil {
		t.Error("多余位置参数必须报错（cobra.NoArgs），否则会被误认为透传")
	}
	if err := packCmd.Args(packCmd, nil); err != nil {
		t.Errorf("无位置参数必须通过: %v", err)
	}
}

func TestPackFlagsRegistered(t *testing.T) {
	for _, name := range []string{"git-only", "no-git", "out", "exclude", "overwrite", "dry-run"} {
		if packCmd.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s 未注册", name)
		}
	}
	if packCmd.Flags().Lookup("exclude").Value.Type() != "stringArray" {
		t.Error("--exclude 必须是 stringArray，含逗号的合法 glob 才不会被 cobra 预先拆坏")
	}
}

func TestPackArchiveDirFormStaysInsideRequestedDir(t *testing.T) {
	// 路径拼接的边界：--out 给的目录里只能出现一个包，且不会跑到目录外
	resetPackFlags(t)
	requireGit(t)
	base := t.TempDir()
	outDir := filepath.Join(base, "out")
	a := newGitRepo(t, base, "alpha", map[string]string{"a.txt": "A"})
	cfg := &config.Config{Repositories: []config.Repository{{ID: 1, Name: "alpha", Path: a, Groups: []string{}}}}
	repoIDs = "1"
	// 目录已存在 → 目录形态（AC-04.2 第二判据）。不存在且不以分隔符结尾时按文件路径处理，
	// 那是票面 Q8 写明的易踩点，已由 TestOutIsDirForm 与 TestResolvePackTargetForms 分别钉住。
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir out: %v", err)
	}
	packOut = outDir

	plan, err := preparePack(cfg)
	if err != nil {
		t.Fatalf("preparePack: %v", err)
	}
	succeeded, failed, fatal := writePackArchive(plan)
	if fatal != "" {
		t.Fatalf("write: %s", fatal)
	}
	if succeeded != 1 || failed != 0 {
		t.Errorf("counts = (%d,%d) want (1,0)", succeeded, failed)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("outDir 应被创建（真实跑不是 dry-run）: %v", err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "alpha__") {
		t.Errorf("目录形态单仓应产出 alpha__<分支>__<ts>.zip，实得 %+v", entries)
	}
	// entry 名不得越出顶层目录
	r, err := zip.OpenReader(filepath.Join(outDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "/") || strings.Contains(f.Name, "..") {
			t.Errorf("entry 名越界: %q", f.Name)
		}
	}
}
