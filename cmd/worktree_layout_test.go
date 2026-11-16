package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/i18n"
	"github.com/wamdy/gits/internal/repo"
)

// ---------------------------------------------------------------- #1434 同构布局：成员路径计算与执行顺序
//
// 这一组全是纯函数（零副作用、不碰 git），所以断言打在"算出来的形状"上：
// 镜像的意义就是 `<集合根>/<相对基准路径>` 与磁盘 1:1，任何"顺手压平/改名"的写法都会让它退化回 flat。

// withZhLang 固定中文口径，使点名串断言不随系统语言漂移（沿 worktree 既有写法）。
func withZhLang(t *testing.T) {
	t.Helper()
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	t.Cleanup(func() { i18n.SetLang(prev) })
}

func TestMirrorMemberPathsKeepsDiskNesting(t *testing.T) {
	withZhLang(t)
	base := filepath.Join("D:", "ws")
	setRoot := filepath.Join("D:", "wt", "s1")
	repos := []repo.RepositoryInfo{
		info(1, "appollo", filepath.Join(base, "appollo")),
		info(9, "gits", filepath.Join(base, "appollo", "code", "tools", "gits")),
	}

	paths, issues := mirrorMemberPaths(setRoot, base, repos)
	if len(issues) > 0 {
		t.Fatalf("不该有点名: %v", issues)
	}
	want := []string{
		filepath.Join(setRoot, "appollo"),
		filepath.Join(setRoot, "appollo", "code", "tools", "gits"),
	}
	if len(paths) != len(want) {
		t.Fatalf("paths 数量 = %d, want %d", len(paths), len(want))
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

// TestMirrorMemberPathsRejectsOutsideBase 越界（`..` 前缀）必须整组点名、且不给路径——
// 静默降级成扁平是本票明令禁止的行为（与 #1064「不猜各仓 HEAD」同一条纪律）。
func TestMirrorMemberPathsRejectsOutsideBase(t *testing.T) {
	withZhLang(t)
	base := filepath.Join("D:", "ws")
	paths, issues := mirrorMemberPaths(filepath.Join("D:", "wt", "s1"), base,
		[]repo.RepositoryInfo{info(7, "elsewhere", filepath.Join("D:", "other", "repo"))})
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want 1 条越界点名", issues)
	}
	if paths[0] != "" {
		t.Errorf("被拒成员不该给出路径，得到 %q", paths[0])
	}
	if !strings.Contains(issues[0], "elsewhere") || !strings.Contains(issues[0], base) {
		t.Errorf("点名串须含仓标签与基准，实得: %s", issues[0])
	}
}

// TestMirrorMemberPathsRejectsSelfBase rel == "."（仓正好等于基准）会撞在集合根本身上，
// 点名串必须给出可用的改法，否则用户只知道失败、不知道下一步。
func TestMirrorMemberPathsRejectsSelfBase(t *testing.T) {
	withZhLang(t)
	base := filepath.Join("D:", "ws", "appollo")
	paths, issues := mirrorMemberPaths(filepath.Join("D:", "wt", "s1"), base,
		[]repo.RepositoryInfo{info(1, "appollo", base)})
	if len(issues) != 1 {
		t.Fatalf("rel==\".\" 必须点名，实得 %v", issues)
	}
	if paths[0] != "" {
		t.Errorf("被拒成员不该给出路径，得到 %q", paths[0])
	}
	if !strings.Contains(issues[0], "--mirror-root") {
		t.Errorf("点名串缺可用基准写法提示: %s", issues[0])
	}
}

// TestMirrorMemberPathsCollectsAllIssues 两个坏仓 → 两条点名（对齐预检的全量收集纪律）。
func TestMirrorMemberPathsCollectsAllIssues(t *testing.T) {
	withZhLang(t)
	base := filepath.Join("D:", "ws")
	_, issues := mirrorMemberPaths(filepath.Join("D:", "wt", "s1"), base, []repo.RepositoryInfo{
		info(1, "a", filepath.Join("D:", "out1")),
		info(2, "b", filepath.Join("D:", "out2")),
	})
	if len(issues) != 2 {
		t.Errorf("issues = %d, want 2（不许首错即返）", len(issues))
	}
}

// TestMirrorMemberPathsSameNameUnderDifferentParents 同构布局的独有价值：
// 同 basename 的两个仓在不同父目录下不相撞（flat 分支只能靠追加 -<ID> 兜，见 safeMemberDirName）。
func TestMirrorMemberPathsSameNameUnderDifferentParents(t *testing.T) {
	withZhLang(t)
	base := filepath.Join("D:", "ws")
	paths, issues := mirrorMemberPaths(filepath.Join("D:", "wt", "s1"), base, []repo.RepositoryInfo{
		info(1, "lib", filepath.Join(base, "x", "lib")),
		info(2, "lib", filepath.Join(base, "y", "lib")),
	})
	if len(issues) > 0 {
		t.Fatalf("不该有点名: %v", issues)
	}
	if paths[0] == paths[1] {
		t.Errorf("两条成员路径相撞: %v", paths)
	}
}

func TestMirrorMemberPathsCrossVolumeRejected(t *testing.T) {
	withZhLang(t)
	if filepath.VolumeName(`D:\`) != "D:" {
		t.Skip("跨卷分支只能在 Windows 上构造")
	}
	_, issues := mirrorMemberPaths(`D:\wt\s1`, `C:\ws`, []repo.RepositoryInfo{info(1, "r", `E:\x`)})
	if len(issues) != 1 {
		t.Fatalf("跨卷须点名，实得 %v", issues)
	}
}

// TestResolveMirrorRoot --mirror-root 覆盖缺省；缺省＝配置基目录的**父目录**（不是它自己，
// 否则现网 appollo 自己就 rel=="." 撞在集合根上，见 design §二）。
func TestResolveMirrorRoot(t *testing.T) {
	got := resolveMirrorRoot(`D:\explicit\root`)
	if want := filepath.Clean(`D:\explicit\root`); got != want {
		t.Errorf("显式基准 = %q, want %q", got, want)
	}

	base := config.ConfigBaseDir()
	def := resolveMirrorRoot("")
	if want := filepath.Clean(filepath.Dir(base)); def != want {
		t.Errorf("缺省基准 = %q, want 配置基目录的父目录 %q", def, want)
	}
	if def == filepath.Clean(base) {
		t.Errorf("缺省基准不该等于配置基目录本身（会让该目录下的仓 rel==\".\"）: %q", def)
	}
}

// TestWorktreeBuildOrderAncestorsFirst 顺序是契约不是实现细节：
// 反序时 git 对祖先直接报 `already exists` 退 128（实测 E2），会留下半个集合。
func TestWorktreeBuildOrderAncestorsFirst(t *testing.T) {
	setRoot := filepath.Join("D:", "wt", "s1")
	repos := []repo.RepositoryInfo{
		info(3, "leaf", filepath.Join(setRoot, "appollo", "code", "tools", "gits", "deep", "leaf")),
		info(1, "appollo", filepath.Join(setRoot, "appollo")),
		info(2, "gits", filepath.Join(setRoot, "appollo", "code", "tools", "gits")),
	}
	paths := []string{repos[0].Path, repos[1].Path, repos[2].Path}

	order := worktreeBuildOrder(repos, paths, true)
	want := []int{1, 2, 0} // appollo(2 段) → gits(5 段) → leaf(7 段)
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v（祖先必须在前）", order, want)
		}
	}
}

// TestWorktreeBuildOrderSameDepthByID 同深度按 ID 升序，保证多次运行可 diff。
func TestWorktreeBuildOrderSameDepthByID(t *testing.T) {
	setRoot := filepath.Join("D:", "wt", "s1")
	repos := []repo.RepositoryInfo{
		info(7, "b", filepath.Join(setRoot, "ws", "b")),
		info(2, "a", filepath.Join(setRoot, "ws", "a")),
	}
	order := worktreeBuildOrder(repos, []string{repos[0].Path, repos[1].Path}, true)
	if order[0] != 1 || order[1] != 0 {
		t.Errorf("同深度未按 ID 升序: %v", order)
	}
}

// TestFlatBuildOrderIsIdentity flat 下 order 必须恒等——
// 否则"为镜像加的重排"会连带改掉既有扁平集合的执行与输出顺序。
func TestFlatBuildOrderIsIdentity(t *testing.T) {
	repos := []repo.RepositoryInfo{
		info(3, "c", filepath.Join("R", "set", "c")),
		info(1, "a", filepath.Join("R", "set", "a")),
		info(2, "b", filepath.Join("R", "set", "b")),
	}
	order := worktreeBuildOrder(repos, []string{repos[0].Path, repos[1].Path, repos[2].Path}, false)
	for i, idx := range order {
		if idx != i {
			t.Fatalf("flat order = %v, want 恒等 [0 1 2]", order)
		}
	}
}
