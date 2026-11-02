package ui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/wamdy/gits/internal/config"
	"github.com/wamdy/gits/internal/i18n"
)

func TestInitColorMode(t *testing.T) {
	// 测试前重置颜色变量
	resetColors()

	tests := []struct {
		name    string
		mode    string
		noColor bool
	}{
		{"never", "never", false},
		{"always", "always", false},
		{"no-color flag", "auto", true},
		{"auto with no-color flag", "auto", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InitColorMode(tt.mode, tt.noColor)
			// always/never 分支可预测
			if tt.mode == "never" || tt.noColor {
				if ColorReset != "" {
					t.Errorf("InitColorMode(%q, %v): ColorReset = %q, want empty", tt.mode, tt.noColor, ColorReset)
				}
			} else if tt.mode == "always" {
				if ColorReset == "" {
					t.Errorf("InitColorMode(%q, %v): ColorReset is empty, want ANSI code", tt.mode, tt.noColor)
				}
			}
		})
	}
}

func TestInitColorModeDefaults(t *testing.T) {
	// 默认模式（auto）不应 panic
	InitColorMode("auto", false)
	// 非法模式不应 panic
	InitColorMode("", false)
}

// resetColors 重置颜色变量为初始状态。
func resetColors() {
	ColorReset = ""
	ColorRed = ""
	ColorGreen = ""
	ColorYellow = ""
	ColorBlue = ""
	ColorPurple = ""
	ColorCyan = ""
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(): %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return buf.String()
}

func TestPrintConfigSettings(t *testing.T) {
	resetColors()
	i18n.SetLang(i18n.Zh)
	defer i18n.SetLang(i18n.Zh)

	cfg := &config.Config{
		Version:      "1.0.0",
		Language:     "",
		Repositories: []config.Repository{},
		Groups:       map[string][]int{},
		Defaults:     config.Defaults{Remote: "origin", Branch: "main"},
	}

	out := captureStdout(t, func() { PrintConfigSettings(cfg) })

	for _, want := range []string{"配置项", "版本: 1.0.0", "语言: （未设置）", "默认远程: origin", "默认分支: main"} {
		if !strings.Contains(out, want) {
			t.Errorf("PrintConfigSettings output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintConfigSettingsEnglish(t *testing.T) {
	resetColors()
	i18n.SetLang(i18n.En)
	defer i18n.SetLang(i18n.Zh)

	cfg := &config.Config{
		Version:  "1.0.0",
		Language: "en",
		Defaults: config.Defaults{Remote: "upstream", Branch: "develop"},
	}

	out := captureStdout(t, func() { PrintConfigSettings(cfg) })

	for _, want := range []string{"Configuration", "Language: en", "Default remote: upstream", "Default branch: develop"} {
		if !strings.Contains(out, want) {
			t.Errorf("PrintConfigSettings output missing %q:\n%s", want, out)
		}
	}
}
