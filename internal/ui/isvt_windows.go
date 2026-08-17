//go:build windows

package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// supportsVT 检测 Windows 控制台是否支持 ANSI/VT 序列。
// 传统 cmd.exe（未启用 ENABLE_VIRTUAL_TERMINAL_PROCESSING）返回 false，避免输出颜色乱码。
func supportsVT() bool {
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	return mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0
}
