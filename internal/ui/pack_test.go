package ui

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wamdy/gits/internal/archive"
	"github.com/wamdy/gits/internal/i18n"
)

// 本文件覆盖 internal/ui/pack.go 里**不读 stdin** 的那一半。
// PrintPackReview / AskPackOverwrite 要 fmt.Scanln，单测里要么挂住要么依赖
// "测试进程的 stdin 恰好是 EOF"这种环境巧合——它们由实机端到端覆盖（含非 TTY 形态），
// 这里不假装测过。

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"}, // 1024 以下不进位（否则小文件被打成 0.0 KiB）
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{285 * 1024 * 1024, "285.0 MiB"},
		{3 * 1024 * 1024 * 1024, "3.0 GiB"},
	}
	for _, c := range cases {
		if got := HumanBytes(c.in); got != c.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestYesAnswer(t *testing.T) {
	yes := []string{"y", "Y", " yes ", "YES"}
	for _, s := range yes {
		if !yesAnswer(s) {
			t.Errorf("yesAnswer(%q) = false, want true", s)
		}
	}
	// 默认拒绝是红线：空回车、任意其他词、以及"看起来像 yes 的串"都不能算批准。
	for _, s := range []string{"", "n", "N", "no", "yeah", "yolo", "1", "y y"} {
		if yesAnswer(s) {
			t.Errorf("yesAnswer(%q) = true, want false（覆盖确认必须默认拒绝）", s)
		}
	}
}

func TestPackDetailCountsHiddenWhenAllZero(t *testing.T) {
	resetColors()
	if got := packDetailCounts(archive.Stats{}); got != "" {
		t.Errorf("四项全 0 应整行不打印，实际 %q", got)
	}
	got := packDetailCounts(archive.Stats{LinkSkipped: 2, Failed: 1, ExcludedSelf: 3, PrunedDirs: 4})
	for _, want := range []string{"2", "1", "3", "4"} {
		if !strings.Contains(got, want) {
			t.Errorf("packDetailCounts 缺计数 %q：%q", want, got)
		}
	}
}

func TestPrintPackSummarySkippedSegment(t *testing.T) {
	resetColors()
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	// skipped 段只在 >0 时出现：正常备份路径的汇总行保持既有两计数口径。
	out := captureStdout(t, func() { PrintPackSummary(2, 0, 0) })
	if strings.Contains(out, "已跳过") {
		t.Errorf("skipped=0 不该出现跳过段：\n%s", out)
	}
	out = captureStdout(t, func() { PrintPackSummary(0, 0, 1) })
	for _, want := range []string{"1 已跳过", "0 失败", "0 成功"} {
		if !strings.Contains(out, want) {
			t.Errorf("汇总缺 %q：\n%s", want, out)
		}
	}
}

func TestPrintPackResultRowAndDryRunRow(t *testing.T) {
	resetColors()
	prev := i18n.CurrentLang()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(prev)

	st := archive.Stats{Files: 31, Bytes: 28160}
	out := captureStdout(t, func() { PrintPackResultRow("[1] repoA (main)", st, true, "") })
	for _, want := range []string{"[OK]", "[1] repoA (main)", "31 个文件"} {
		if !strings.Contains(out, want) {
			t.Errorf("结果行缺 %q：\n%s", want, out)
		}
	}
	if strings.Contains(out, "非常规跳过") {
		t.Errorf("全 0 细节不该刷屏：\n%s", out)
	}

	// 失败行必须把错误文案打出来，不能只剩一个 [FAIL]。
	out = captureStdout(t, func() {
		PrintPackResultRow("[1] repoA (main)", st, false, "copy entry x: truncated")
	})
	if !strings.Contains(out, "truncated") {
		t.Errorf("失败行缺错误文案：\n%s", out)
	}

	out = captureStdout(t, func() { PrintPackDryRunRow("[1] repoA (main)", st) })
	if !strings.Contains(out, "31 个文件") {
		t.Errorf("dry-run 行缺文件数：\n%s", out)
	}
}

// TestProceedPromptHasSingleQuestion 只钉文案本身（proceed_prompt 以问号结尾）。
// 它测不到调用点——printer.go 的 PrintReviewPrompt 曾在此前提下又补了一个 `?`，
// 实机打出「是否继续？? [y/N]」而本测试照旧全绿（#1253）。真正的守卫是同文件的
// TestProceedPromptCallSitesAppendNoQuestion。
func TestProceedPromptHasSingleQuestion(t *testing.T) {
	prev := i18n.CurrentLang()
	defer i18n.SetLang(prev)
	for _, lang := range []i18n.Lang{i18n.Zh, i18n.En} {
		i18n.SetLang(lang)
		prompt := i18n.T("proceed_prompt")
		runes := []rune(strings.TrimRight(prompt, " "))
		last := runes[len(runes)-1]
		if last != '?' && last != '？' {
			t.Errorf("%s 的 proceed_prompt 不以问号结尾（本测试的前提变了）：%q", lang, prompt)
		}
	}
}

// TestProceedPromptCallSitesAppendNoQuestion 是「问号重复」的真守卫（#1253）：
// 逐行扫描模块内所有 Go 源码里的 `i18n.T("proceed_prompt")` 调用点，要求调用表达式以外
// 不再出现 `?`/`？`——文案自带问号，调用点再补一个就成 `是否继续？? [y/N]`。
// 扫到的调用点数为 0 即 FAIL：字面量失效（改名、改成变量拼接）时不许退化成空跑假绿。
func TestProceedPromptCallSitesAppendNoQuestion(t *testing.T) {
	const call = `i18n.T("proceed_prompt")`
	root := filepath.Join("..", "..") // internal/ui → 模块根
	sites := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(data), "\n") {
			idx := strings.Index(line, call)
			if idx < 0 {
				continue
			}
			sites++
			rest := line[:idx] + line[idx+len(call):]
			if strings.ContainsAny(rest, "?？") {
				t.Errorf("%s:%d 调用点自行补了问号（文案已自带）：%s", path, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 %s 失败：%v", root, err)
	}
	if sites < 3 {
		t.Fatalf("只扫到 %d 个 proceed_prompt 调用点，扫描口径已失效（期望 >= 3：printer.go 两处 + pack.go 两处）", sites)
	}
}
