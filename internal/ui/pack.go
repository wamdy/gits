package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wamdy/gits/internal/archive"
	"github.com/wamdy/gits/internal/i18n"
	"golang.org/x/term"
)

// StdinIsTerminal 报告标准输入是否为交互终端。
//
// 判据必须是 Stdin：既有颜色逻辑判的是 Stdout（那是"输出能不能渲染 ANSI"，语义不同），
// 而会把整批命令挂住的是 fmt.Scanln 读不到输入这一件事（TQ-02）。
// 管道、CI、`echo | gits pack` 三种形态下本函数返回 false。
func StdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// HumanBytes 把字节数收敛成可读体积（dry-run 与结果行的共用口径）。
// 阈值固定 1024 进制：压缩场景关心的是"这个包会不会占满盘"，不是 SI 口径的 marketing 数字。
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// PrintPackReview 打印 pack 的执行前确认块（列目标包路径、模式、排除与选中仓库）。
// 返回 false 表示用户取消，cmd 层必须零写盘退出。
func PrintPackReview(labels []string, mode archive.Mode, target string, excludes []string) bool {
	fmt.Printf("\n%s%s:%s\n", ColorYellow, i18n.T("review_before_exec"), ColorReset)
	fmt.Printf("%s: %sgits pack [%s]%s\n", i18n.T("command"), ColorCyan, mode.String(), ColorReset)
	// 标签用专门的文案键：`flag_pack_out` 是 --help 里的选项说明（一整句），
	// 当行首标签打出来是「输出目录或具体 zip 文件路径（缺省为…）: C:\...zip」这种畸形行。
	fmt.Printf("%s: %s\n", i18n.T("pack_target_label"), target)
	if len(excludes) > 0 {
		fmt.Printf("%s: %s\n", "--exclude", strings.Join(excludes, ", "))
	}
	fmt.Printf("%s (%d):\n", i18n.T("repositories"), len(labels))
	for _, name := range labels {
		fmt.Printf("  - %s%s%s\n", ColorBlue, name, ColorReset)
	}
	// proceed_prompt 文案自带问号，这里不能再补一个 `?`（printer.go 的 printWorktreeReview
	// 已修过一次，复制 printReviewPrompt 旧变体时又踩了：实机输出「是否继续？? [y/N]」）。
	fmt.Printf("\n%s%s [y/N]:%s ", ColorYellow, i18n.T("proceed_prompt"), ColorReset)

	var response string
	fmt.Scanln(&response)
	return yesAnswer(response)
}

// AskPackOverwrite 就"目标包已存在"征求覆盖决定（AC-04.4）。
// 必须把现有文件的体积与修改时间一起显示——只报一个路径就问"覆盖吗"，
// 等于让用户闭着眼睛签掉一份可能更大的备份。
func AskPackOverwrite(target string, size int64, mtime time.Time) bool {
	fmt.Printf("\n%s%s%s\n", ColorYellow,
		i18n.T("pack_cover_prompt", target, HumanBytes(size), mtime.Format("2006-01-02 15:04:05")), ColorReset)
	fmt.Printf("%s [y/N]: ", i18n.T("proceed_prompt"))

	var response string
	fmt.Scanln(&response)
	return yesAnswer(response)
}

func yesAnswer(response string) bool {
	r := strings.ToLower(strings.TrimSpace(response))
	return r == "y" || r == "yes"
}

// PrintPackResultRow 打印单仓结果行：`[OK] label：N 个文件，X MiB（细节计数）`。
// 细节行的口径：**四项全为 0 时整行不打印**（正常路径不刷屏）；任一项非零则四项一并列出，
// 不做逐项裁剪——同一批仓的各行列宽一致、可直接横向对比（AC-05.2 只要求可见披露，未要求省略零项）。
func PrintPackResultRow(label string, st archive.Stats, ok bool, errText string) {
	if !ok {
		fmt.Printf("[%s%s%s] %s%s%s: %s\n", ColorRed, i18n.T("fail"), ColorReset,
			ColorBlue, label, ColorReset, errText)
		return
	}
	fmt.Printf("[%s%s%s]   %s%s%s — %s\n", ColorGreen, i18n.T("ok"), ColorReset,
		ColorBlue, label, ColorReset,
		i18n.T("pack_result_row", st.Files, HumanBytes(st.Bytes)))
	if detail := packDetailCounts(st); detail != "" {
		fmt.Printf("       %s\n", detail)
	}
}

// PrintPackDryRunRow 打印 dry-run 的单仓预估行（AC-05.3）。
func PrintPackDryRunRow(label string, st archive.Stats) {
	fmt.Printf("  %s\n", i18n.T("pack_dry_run_row", label, st.Files, HumanBytes(st.Bytes),
		st.LinkSkipped, st.Failed, st.ExcludedSelf, st.PrunedDirs))
}

func packDetailCounts(st archive.Stats) string {
	if st.LinkSkipped == 0 && st.Failed == 0 && st.ExcludedSelf == 0 && st.PrunedDirs == 0 {
		return ""
	}
	return i18n.T("pack_row_detail", st.LinkSkipped, st.Failed, st.ExcludedSelf, st.PrunedDirs)
}

// PrintPackSummary 打印 pack 的收尾汇总。既有 PrintSummary 只有两个计数，
// pack 还需要"用户主动跳过"这一态（它不是失败），故新增本函数而不改既有签名（AC-06.6）。
func PrintPackSummary(successCount, failCount, skippedCount int) {
	fmt.Printf("\n%s%s:%s %s%d%s %s, %s%d%s %s",
		ColorCyan, i18n.T("summary"), ColorReset,
		ColorRed, failCount, ColorReset, i18n.T("failed"),
		ColorGreen, successCount, ColorReset, i18n.T("succeeded"))
	if skippedCount > 0 {
		fmt.Printf(", %s%d%s %s", ColorPurple, skippedCount, ColorReset, i18n.T("pack_skipped"))
	}
	fmt.Println()
}
