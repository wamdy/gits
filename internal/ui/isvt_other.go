//go:build !windows

package ui

// supportsVT 非 Windows 平台：Unix 终端天然支持 ANSI 序列。
func supportsVT() bool {
	return true
}
