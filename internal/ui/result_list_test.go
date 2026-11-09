package ui

import (
	"strings"
	"testing"

	"github.com/wamdy/gits/internal/i18n"
)

func TestPrintResultListEmptyNoOutput(t *testing.T) {
	resetColors()
	out := captureStdout(t, func() { PrintResultList(nil) })
	if out != "" {
		t.Fatalf("empty list must print nothing, got %q", out)
	}
}

func TestPrintResultListSectionsAndFallbacks(t *testing.T) {
	resetColors()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(i18n.Zh)

	out := captureStdout(t, func() {
		PrintResultList([]ResultItem{
			{Label: "[1] repoA (main)", Path: "/tmp/a", Success: false, ErrText: "pathspec did not match"},
			{Label: "[2] repoB (main)", Path: "/tmp/b", Success: true},
			{Path: "/tmp/c", Success: false, ErrText: ""},
		})
	})

	failedAt := strings.Index(out, "失败仓库")
	succeededAt := strings.Index(out, "成功仓库")
	if failedAt < 0 || succeededAt < 0 || failedAt > succeededAt {
		t.Fatalf("sections missing or misordered: %q", out)
	}
	if !strings.Contains(out[:succeededAt], "原因：pathspec did not match") {
		t.Fatalf("failure reason missing: %q", out)
	}
	// Label 为空退化为只显示路径；ErrText 为空时不打空原因行
	if !strings.Contains(out[:succeededAt], "/tmp/c") || strings.Contains(out[:succeededAt], "原因：\n") {
		t.Fatalf("empty label / empty reason handling broken: %q", out)
	}
	if !strings.Contains(out[succeededAt:], "[2] repoB (main)") || strings.Contains(out[succeededAt:], "原因：") {
		t.Fatalf("succeeded section wrong: %q", out)
	}
}

func TestPrintResultListEnglishTitles(t *testing.T) {
	resetColors()
	i18n.SetLang(i18n.En)
	defer i18n.SetLang(i18n.Zh)

	out := captureStdout(t, func() {
		PrintResultList([]ResultItem{{Label: "[1] repoA (main)", Path: "/tmp/a", Success: false, ErrText: "boom"}})
	})
	if !strings.Contains(out, "Failed repositories") || !strings.Contains(out, "reason: boom") {
		t.Fatalf("english copy missing: %q", out)
	}
	if strings.Contains(out, "成功仓库") {
		t.Fatalf("zh copy leaked into en output: %q", out)
	}
}
