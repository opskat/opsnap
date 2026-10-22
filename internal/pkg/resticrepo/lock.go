// Package resticrepo restic 仓库状态的判定，供运行引擎与快照读取共用。
package resticrepo

import "strings"

// LockedExitCode restic 报告「无法获取仓库锁」的退出码
const LockedExitCode = 11

// Locked restic 是否因为仓库被别的进程锁住而结束。只看退出码与输出里「already locked」的说明：
// 同一存储位置上的并发备份持有非独占锁，而 forget、prune 这类需要独占锁的命令会立即以它结束。
func Locked(exitCode int, output string) bool {
	return exitCode == LockedExitCode || strings.Contains(strings.ToLower(output), "already locked")
}
