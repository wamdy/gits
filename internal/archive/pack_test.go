package archive

import (
	"archive/zip"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------- 测试脚手架

// makeRepo 在 t.TempDir() 下造一个"仓库目录"：可选 .git 目录形态或 .git 文件形态，
// 并写入 files 列出的相对路径（`a/b.txt:内容`）。不依赖真实 git —— 本包不调 git。
func makeRepo(t *testing.T, name string, gitDir bool, files ...string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if gitDir {
		if err := os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o755); err != nil {
			t.Fatalf("mkdir .git: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[core]\n"), 0o644); err != nil {
			t.Fatalf("write .git/config: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, ".git", "objects", "pack.idx"), []byte("idx"), 0o644); err != nil {
			t.Fatalf("write .git/objects: %v", err)
		}
	} else {
		// linked worktree 形态：.git 是 gitdir 指针文本文件（E5 实测 84 字节）
		if err := os.WriteFile(filepath.Join(root, ".git"),
			[]byte("gitdir: /main/.git/worktrees/"+name+"\n"), 0o644); err != nil {
			t.Fatalf("write gitfile: %v", err)
		}
	}
	for _, f := range files {
		p, content, _ := strings.Cut(f, ":")
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return root
}

func specFor(root string) RepoSpec {
	return RepoSpec{
		ID: 7, Name: "cfgname", Path: root,
		TopDir: TopDir(root), Branch: "main", GitKind: "repo",
	}
}

// packTo 把若干仓写入临时 zip 并返回 entry 名列表（`/` 分隔）与 zip 路径。
func packTo(t *testing.T, opts Options, roots ...string) (string, []string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.zip")
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	w := zip.NewWriter(f)
	for _, r := range roots {
		if _, err := Write(w, specFor(r), opts); err != nil {
			t.Fatalf("Write(%s): %v", r, err)
		}
	}
	if err := NewManifest(opts.Mode, out, opts.Excludes.Patterns(), "test").WriteTo(w); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	return out, readNames(t, out)
}

func readNames(t *testing.T, zipPath string) []string {
	t.Helper()
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer r.Close()
	names := make([]string, 0, len(r.File))
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return names
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- 命名与冲突

func TestTopDirUsesBasenameNotConfigName(t *testing.T) {
	cases := []struct{ in, want string }{
		{filepath.FromSlash("/srv/code/appollo"), "appollo"},
		{filepath.FromSlash("/srv/code/appollo/"), "appollo"},
		{filepath.FromSlash("/srv/code/appollo//"), "appollo"},
		{"/", "root"},
		{"gits@demo", "gits@demo"},
		{"fea*ture", "fea-ture"}, // 非法字符必须清掉（AC-04.3）
		{"", "root"},
	}
	for _, c := range cases {
		if got := TopDir(c.in); got != c.want {
			t.Errorf("TopDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeNameBranchForms(t *testing.T) {
	cases := []struct{ in, want string }{
		{"feature/20261106-gits-pack", "feature-20261106-gits-pack"},
		{"release:1.0", "release-1.0"},
		{"a<b>c|d?e*f", "a-b-c-d-e-f"},
		{"trailing...", "trailing"},
		{"trail-space ", "trail-space"},
		{"***", "---"},
		{"", "-"},
		{"normal_branch", "normal_branch"},
	}
	for _, c := range cases {
		if got := SanitizeName(c.in); got != c.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCaseFoldDuplicatesDetectsCaseOnlyDifference(t *testing.T) {
	// TQ-04：`Repo` 与 `repo` 在 zip 里是两个合法 entry，解出来却并成同一目录。
	dup := CaseFoldDuplicates([]string{"Repo", "other", "repo"})
	if len(dup) != 1 || !strings.EqualFold(dup[0], "repo") {
		t.Fatalf("CaseFoldDuplicates = %v, want exactly one repo-form", dup)
	}
	if got := CaseFoldDuplicates([]string{"a", "b", "c"}); len(got) != 0 {
		t.Fatalf("distinct names reported as duplicates: %v", got)
	}
	// 大小写敏感文件系统（Linux）上这不是缺陷，但判据按目标平台最保守取值：
	// 三个同名不同写法只报两组重复，证明每组只报一次。
	if got := CaseFoldDuplicates([]string{"x", "X", "x"}); len(got) != 2 {
		t.Fatalf("CaseFoldDuplicates triple = %v, want 2 entries", got)
	}
}

// ---------------------------------------------------------------- 排除模式

func TestCompileExcludesClassifiesAndSplitsCommas(t *testing.T) {
	m, err := CompileExcludes([]string{"node_modules,dist", "src/*.min.js", ".next"})
	if err != nil {
		t.Fatalf("CompileExcludes: %v", err)
	}
	if len(m.segments) != 3 {
		t.Errorf("segments = %v, want node_modules/dist/.next", m.segments)
	}
	if len(m.paths) != 1 || m.paths[0] != "src/*.min.js" {
		t.Errorf("paths = %v, want [src/*.min.js]", m.paths)
	}
	if got := len(m.Patterns()); got != 4 {
		t.Errorf("Patterns() len = %d, want 4", got)
	}
	if _, err := CompileExcludes([]string{"[bad"}); err == nil {
		t.Error("invalid glob must error, not be silently dropped")
	}
}

func TestExcludeMatcherSemantics(t *testing.T) {
	m, err := CompileExcludes([]string{"node_modules", "a*"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, rel := range []string{"node_modules/x.js", "web/node_modules/deep/pkg/index.js", "assets/logo.png", "app.go"} {
		if !m.MatchSegments(rel) {
			t.Errorf("segment pattern must match %q at any depth (AC-05.1)", rel)
		}
	}
	if m.MatchSegments("src/main.go") {
		t.Error("src/main.go must not match")
	}

	pm, err := CompileExcludes([]string{"src/*.log"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !pm.MatchRelPath("src/a.log") {
		t.Error("src/a.log must match path pattern")
	}
	if pm.MatchRelPath("src/deep/a.log") {
		t.Error("* must not cross separator (path.Match semantics, same as -r glob)")
	}
	if pm.MatchRelPath("SRC/a.log") {
		t.Error("D-01: patterns are case-sensitive")
	}
}

// ---------------------------------------------------------------- 同构与形态

func TestWriteIsomorphicSingleRepo(t *testing.T) {
	root := makeRepo(t, "repoA", true, "src/main.go:pkg main", "README.md:hi")
	_, names := packTo(t, Options{Mode: ModeFull}, root)

	want := []string{"repoA/", "repoA/src/main.go", "repoA/README.md", "repoA/.git/config", "repoA/.git/objects/pack.idx"}
	for _, w := range want {
		if !has(names, w) {
			t.Errorf("missing entry %q in %v", w, names)
		}
	}
	if !has(names, ManifestName) {
		t.Errorf("manifest entry missing in %v", names)
	}
	// 同构不变量：包内除顶层目录与 manifest 外不能有别的根级 entry
	for _, n := range names {
		first := strings.SplitN(n, "/", 2)[0]
		if first != "repoA" && first != ManifestName {
			t.Errorf("unexpected root entry segment %q (同构不变量 1)", first)
		}
	}
}

func TestWriteTwoReposSideBySide(t *testing.T) {
	// AC-01.3 / Q10：一次调用一个包，包内 N 个并列顶层目录
	a := makeRepo(t, "repoA", true, "a.txt:A")
	b := makeRepo(t, "repoB", false, "b.txt:B")
	_, names := packTo(t, Options{Mode: ModeFull}, a, b)

	if !has(names, "repoA/a.txt") || !has(names, "repoB/b.txt") {
		t.Fatalf("both repos must appear as sibling top dirs, got %v", names)
	}
	// worktree 的 .git 是文件，原样压（AC-03.5 / Q4）
	if !has(names, "repoB/.git") {
		t.Errorf("linked worktree gitfile must be packed as-is, got %v", names)
	}
}

func TestModeGitOnlyAndNoGit(t *testing.T) {
	root := makeRepo(t, "repoA", true, "src/main.go:x")

	_, names := packTo(t, Options{Mode: ModeGitOnly}, root)
	for _, n := range names {
		if n == ManifestName || strings.HasPrefix(n, "repoA/.git") || n == "repoA/" {
			continue
		}
		t.Errorf("git-only must contain only .git content, found %q", n)
	}
	if !has(names, "repoA/.git/config") {
		t.Error("git-only must contain .git/config")
	}
	if has(names, "repoA/src/main.go") {
		t.Error("git-only must not contain working tree files (AC-03.1)")
	}

	_, names2 := packTo(t, Options{Mode: ModeNoGit}, root)
	for _, n := range names2 {
		if strings.HasPrefix(n, "repoA/.git") {
			t.Errorf("no-git must not contain .git entries (AC-03.2): %q", n)
		}
	}
	if !has(names2, "repoA/src/main.go") {
		t.Error("no-git must contain working tree files")
	}
}

func TestModeFilterOnlyAppliesToRootGitDir(t *testing.T) {
	// design D10：嵌套仓（submodule / 仓中仓）的 .git 属于外层工作区内容，
	// --no-git 不得把它剥掉——否则备份出来的嵌套仓是坏的。
	root := makeRepo(t, "outer", true,
		"code/tools/inner/.git:gitdir: /x\n",
		"code/tools/inner/main.go:package inner",
		"src/app.go:package app")
	_, names := packTo(t, Options{Mode: ModeNoGit}, root)

	if !has(names, "outer/code/tools/inner/.git") {
		t.Errorf("nested repo .git must survive --no-git (D10), got %v", names)
	}
	if !has(names, "outer/code/tools/inner/main.go") {
		t.Errorf("nested repo content must survive --no-git, got %v", names)
	}
	if has(names, "outer/.git/config") {
		t.Errorf("root .git must be excluded, got %v", names)
	}
}

func TestExcludePrunesWholeSubtree(t *testing.T) {
	root := makeRepo(t, "repoA", true,
		"node_modules/pkg/deep/deeper/index.js:x",
		"web/node_modules/other/index.js:y",
		"src/keep.go:z",
		"dist/app.min.js:q")
	m, err := CompileExcludes([]string{"node_modules", "dist"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, names := packTo(t, Options{Mode: ModeFull, Excludes: m}, root)

	for _, n := range names {
		if strings.Contains(n, "node_modules") || strings.Contains(n, "dist/") {
			t.Errorf("excluded path leaked into archive: %q", n)
		}
	}
	if !has(names, "repoA/src/keep.go") {
		t.Errorf("kept file missing: %v", names)
	}
}

func TestSelfExclusionTargetInsideRepo(t *testing.T) {
	// TQ-05 / AC-06.4：目标包（与 .tmp）落在被压仓内时必须排除自身
	root := makeRepo(t, "repoA", true, "bk/repoA__main__x.zip.tmp:半成品", "a.txt:A")
	opts := Options{Mode: ModeFull, SelfPaths: []string{
		filepath.Join(root, "bk", "repoA__main__x.zip"),
		filepath.Join(root, "bk", "repoA__main__x.zip.tmp"),
	}}
	_, names := packTo(t, opts, root)
	for _, n := range names {
		if strings.Contains(n, ".zip") {
			t.Errorf("archive must not contain its own target file: %q", n)
		}
	}
	if !has(names, "repoA/a.txt") {
		t.Errorf("normal file must still be packed: %v", names)
	}
	// 排除动作必须可见（进 manifest 计数）
	st, err := Count(specFor(root), opts)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if st.ExcludedSelf != 1 {
		t.Errorf("ExcludedSelf = %d, want 1 (tmp file counted)", st.ExcludedSelf)
	}
}

func TestSelfExclusionIsCaseInsensitive(t *testing.T) {
	root := makeRepo(t, "repoA", true, "BK/RepoA.ZIP:dummy")
	opts := Options{Mode: ModeFull, SelfPaths: []string{filepath.Join(root, "bk", "repoa.zip")}}
	st, err := Count(specFor(root), opts)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if st.ExcludedSelf != 1 {
		t.Errorf("ExcludedSelf = %d, want 1 — Windows 上大小写不同仍是同一文件", st.ExcludedSelf)
	}
}

func TestNonRegularFilesSkipped(t *testing.T) {
	root := makeRepo(t, "repoA", true, "a.txt:A")
	link := filepath.Join(root, "link-to-a")
	if err := os.Symlink(filepath.Join(root, "a.txt"), link); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}
	_, names := packTo(t, Options{Mode: ModeFull}, root)
	if has(names, "repoA/link-to-a") {
		t.Errorf("symlink must not be dereferenced into the archive (TQ-06): %v", names)
	}
	st, err := Count(specFor(root), Options{Mode: ModeFull})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if st.LinkSkipped == 0 {
		t.Errorf("symlink must be counted in LinkSkipped, got %+v", st)
	}
}

func TestOutOfRangeMtimeDoesNotBreakArchive(t *testing.T) {
	// safeModTime 的反向验证目标：1970 的时间戳若直送 archive/zip 会拒绝建 entry，
	// 整包失败；夹到 1980 才能保证"宁可时间不准，不要内容缺失"。
	root := makeRepo(t, "repoA", true, "old.txt:ancient")
	old := filepath.Join(root, "old.txt")
	if err := os.Chtimes(old, time.Unix(0, 0), time.Unix(0, 0)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	_, names := packTo(t, Options{Mode: ModeFull}, root)
	if !has(names, "repoA/old.txt") {
		t.Fatalf("file with 1970 mtime must still be archived: %v", names)
	}
}

func TestEmptyRepoStillHasTopDir(t *testing.T) {
	// AC-02.5：只有 .git 的仓（工作区为空）也要有顶层目录 entry
	root := makeRepo(t, "emptyRepo", true)
	_, names := packTo(t, Options{Mode: ModeNoGit}, root)
	if !has(names, "emptyRepo/") {
		t.Errorf("empty repo must still contribute its top dir entry: %v", names)
	}
}

func TestUnicodeAndDeepPathRoundTrip(t *testing.T) {
	// TQ-09 的静态那一半：非 ASCII 名称与超长相对路径必须写入并可读回。
	// 实机解压比对（系统解压工具解出并核对）在 06_test 的端到端用例里承载。
	deep := "项目/子目录"
	for i := 0; i < 12; i++ {
		deep += "/层级目录-中文-" + strings.Repeat("x", 12)
	}
	if len(deep) < 200 {
		t.Fatalf("test fixture not deep enough: len=%d", len(deep))
	}
	root := makeRepo(t, "repo-中文", true, deep+"/文件.txt:内容")
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(deep))); err != nil {
		t.Skipf("文件系统不支持该深度路径，静态比对改由实机用例覆盖: %v", err)
	}
	out, names := packTo(t, Options{Mode: ModeFull}, root)

	var found string
	for _, n := range names {
		if strings.HasSuffix(n, "文件.txt") {
			found = n
		}
	}
	if found == "" {
		t.Fatalf("non-ASCII deep entry missing from archive: %v", names)
	}
	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name != found {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry: %v", err)
		}
		buf := make([]byte, 16)
		n, _ := rc.Read(buf)
		if string(buf[:n]) != "内容" {
			t.Errorf("entry content mismatch: %q", string(buf[:n]))
		}
		rc.Close()
	}
}

func TestCountMatchesWrite(t *testing.T) {
	// dry-run 的数必须与真实包一致：两者共用同一遍历实现，这条是防"两套代码分叉"的钉。
	root := makeRepo(t, "repoA", true, "a.txt:AAA", "src/b.txt:BBBBB", "node_modules/x/y.js:zzzzzz")
	m, err := CompileExcludes([]string{"node_modules"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	opts := Options{Mode: ModeFull, Excludes: m}
	cnt, err := Count(specFor(root), opts)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	out, _ := packTo(t, opts, root)
	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r.Close()
	files := 0
	var bytes int64
	for _, f := range r.File {
		if strings.HasSuffix(f.Name, "/") || f.Name == ManifestName {
			continue
		}
		files++
		bytes += int64(f.UncompressedSize64)
	}
	if files != cnt.Files || bytes != cnt.Bytes {
		t.Errorf("dry-run counts (%d files, %d bytes) != archive contents (%d files, %d bytes)",
			cnt.Files, cnt.Bytes, files, bytes)
	}
	if cnt.Files != 4 {
		t.Errorf("Count files = %d, want 4（工作区 2 个 + .git 内 2 个；node_modules 已剪）", cnt.Files)
	}
}

func TestWriteFailsOnMissingRoot(t *testing.T) {
	// AC-06.1：仓整体不可用必须升级为 error，由 cmd 层拦在写盘之前
	fresh := t.TempDir()
	spec := specFor(filepath.Join(fresh, "no-such-dir"))
	f, err := os.Create(filepath.Join(fresh, "z.zip"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if _, err := Write(zip.NewWriter(f), spec, Options{Mode: ModeFull}); err == nil {
		t.Error("Write on missing root must return error")
	}
	// 根路径是普通文件而非目录
	fileRoot := filepath.Join(fresh, "plain.txt")
	if err := os.WriteFile(fileRoot, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Write(zip.NewWriter(f), specFor(fileRoot), Options{Mode: ModeFull}); err == nil {
		t.Error("Write on a non-directory root must return error")
	}
}

func TestManifestRoundTrip(t *testing.T) {
	// AC-05.4：包内清单可读回且字段齐全，多仓单包不打开包也能知道里面是什么
	a := makeRepo(t, "repoA", true, "a.txt:AAA")
	b := makeRepo(t, "repoB", false, "b.txt:B")
	m, err := CompileExcludes([]string{"node_modules"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out.zip")
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w := zip.NewWriter(f)
	mani := NewManifest(ModeNoGit, out, m.Patterns(), "1.2.3")
	for _, root := range []string{a, b} {
		st, err := Write(w, specFor(root), Options{Mode: ModeNoGit, Excludes: m})
		if err != nil {
			t.Fatalf("write: %v", err)
		}
		spec := specFor(root)
		mani.AddRecord(RepoRecord{ID: spec.ID, Name: spec.Name, SourcePath: spec.Path,
			TopDir: spec.TopDir, Branch: spec.Branch, GitKind: spec.GitKind,
			Files: st.Files, Bytes: st.Bytes, OK: true})
	}
	if err := mani.WriteTo(w); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	f.Close()

	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r.Close()
	var raw []byte
	for _, file := range r.File {
		if file.Name != ManifestName {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open manifest: %v", err)
		}
		buf := make([]byte, file.UncompressedSize64)
		if _, err := io.ReadFull(rc, buf); err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		rc.Close()
		raw = buf
	}
	if raw == nil {
		t.Fatal("manifest entry not found")
	}
	var got Manifest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if got.Mode != "no-git" || got.Version != "1.2.3" || got.Tool != "gits pack" {
		t.Errorf("manifest header wrong: %+v", got)
	}
	if got.Summary.Repos != 2 || got.Summary.Succeeded != 2 || got.Summary.Failed != 0 {
		t.Errorf("manifest summary wrong: %+v", got.Summary)
	}
	if len(got.Repos) != 2 || got.Repos[0].TopDir != "repoA" || got.Repos[1].TopDir != "repoB" {
		t.Errorf("manifest per-repo records wrong: %+v", got.Repos)
	}
	if got.Repos[0].SourcePath == "" || got.Repos[0].Branch != "main" {
		t.Errorf("manifest record missing identity: %+v", got.Repos[0])
	}
	if len(got.Excludes) != 1 || got.Excludes[0] != "node_modules" {
		t.Errorf("manifest excludes wrong: %v", got.Excludes)
	}
}

func TestDirEntriesOnlyAtTopLevel(t *testing.T) {
	// 已知取舍的可见化：中间层目录不单独成 entry（由解压工具按名还原），空子目录不入包。
	root := makeRepo(t, "repoA", true, "a/b/c.txt:x")
	if err := os.MkdirAll(filepath.Join(root, "empty-dir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, names := packTo(t, Options{Mode: ModeFull}, root)
	dirs := 0
	for _, n := range names {
		if strings.HasSuffix(n, "/") {
			dirs++
			if n != "repoA/" {
				t.Errorf("only the top dir may be an explicit directory entry, got %q", n)
			}
		}
	}
	if dirs != 1 {
		t.Errorf("dir entry count = %d, want 1 (top dir only)", dirs)
	}
	if !has(names, "repoA/a/b/c.txt") {
		t.Errorf("nested file must be present via its name: %v", names)
	}
}

func TestWriteModePermPreserved(t *testing.T) {
	// 非常规文件的排除已在别处覆盖；这里确认写入的 entry 带上了可读的权限位，
	// 避免 Linux 侧解出来全变 0000 这种"能解但不可用"的产物。
	root := makeRepo(t, "repoA", true, "a.txt:AAA")
	out, _ := packTo(t, Options{Mode: ModeFull}, root)
	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name == "repoA/a.txt" {
			var mode fs.FileMode = f.Mode()
			if mode.Perm()&0o400 == 0 {
				t.Errorf("entry mode = %v, want readable perms", mode)
			}
		}
	}
}
