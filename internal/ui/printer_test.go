package ui

import (
	"testing"
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