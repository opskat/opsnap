// Package executor 执行 cmdbuild 构建的命令：子进程放在独立进程组里，
// context 结束（超时或手动停止）时终止整个进程组，并把合并后的输出写给调用方。
package executor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/cago-frame/cago/pkg/gogo"

	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
)

// DefaultKillGrace 发出 SIGTERM 后等待进程组自行退出的时间，超时再发 SIGKILL
const DefaultKillGrace = 10 * time.Second

// Runner 执行一条命令
type Runner interface {
	// Run 执行命令并把 stdout/stderr 合并写入 out（可为 nil）。
	// 进程正常结束时返回它的退出码和 nil（非零退出码不算 error）；
	// 无法启动时返回 -1 和启动错误；
	// ctx 结束时终止整个进程组，返回 -1 和 context.Cause(ctx)（超时为 context.DeadlineExceeded）。
	Run(ctx context.Context, cmd cmdbuild.Cmd, out io.Writer) (exitCode int, err error)
}

// Streamer 能把标准输出和标准错误分开的执行器。下载数据流时标准输出就是正文，
// 不能混入标准错误，因此单独提供 RunSplit。
type Streamer interface {
	Runner
	// RunSplit 同 Run，但标准输出写入 stdout，标准错误写入 stderr（均可为 nil）
	RunSplit(ctx context.Context, cmd cmdbuild.Cmd, stdout, stderr io.Writer) (exitCode int, err error)
}

// Exec 真实的进程执行器
type Exec struct {
	// KillGrace SIGTERM 与 SIGKILL 之间的宽限期；让 restic 有机会释放仓库锁
	KillGrace time.Duration
}

// New 创建执行器
func New() *Exec {
	return &Exec{KillGrace: DefaultKillGrace}
}

// Run 见 Runner。子进程只继承 PATH、HOME 等白名单环境变量，再加上 cmd.Env
func (e *Exec) Run(ctx context.Context, cmd cmdbuild.Cmd, out io.Writer) (int, error) {
	if out == nil {
		out = io.Discard
	}
	return e.run(ctx, cmd, out, out)
}

// RunSplit 见 Streamer
func (e *Exec) RunSplit(ctx context.Context, cmd cmdbuild.Cmd, stdout, stderr io.Writer) (int, error) {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return e.run(ctx, cmd, stdout, stderr)
}

func (e *Exec) run(ctx context.Context, cmd cmdbuild.Cmd, stdout, stderr io.Writer) (int, error) {
	if ctx.Err() != nil {
		return -1, context.Cause(ctx)
	}
	// 执行器的职责就是运行 cmdbuild 构建的命令，程序名和参数来自受控的构建函数
	c := exec.Command(cmd.Name, cmd.Args...) //nolint:gosec // 见上
	c.Env = append(baseEnv(), cmd.Env...)
	// Run 把同一个 writer 同时作为 stdout 和 stderr，exec 只用一个管道，写入不会交错
	c.Stdout, c.Stderr = stdout, stderr
	if cmd.Stdin != nil {
		c.Stdin = bytes.NewReader(cmd.Stdin)
	}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	grace := e.KillGrace
	if grace <= 0 {
		grace = DefaultKillGrace
	}
	// 主进程退出后，若有残留子进程仍占着输出管道，最多再等一个宽限期就关闭管道
	c.WaitDelay = grace
	if err := c.Start(); err != nil {
		return -1, err
	}
	pgid := c.Process.Pid
	done := make(chan struct{})
	// mu 保护 exited/killed：只有在进程被回收前发出了信号，才算被终止
	var mu sync.Mutex
	exited, killed := false, false
	gogo.Go(func() error {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
		}
		mu.Lock()
		if exited {
			mu.Unlock()
			return nil
		}
		killed = true
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		mu.Unlock()
		select {
		case <-done:
		case <-time.After(grace):
		}
		// 无论主进程是否已退出，都确保组内残留进程被清理
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return nil
	})
	err := c.Wait()
	mu.Lock()
	exited = true
	wasKilled := killed
	mu.Unlock()
	close(done)
	if wasKilled {
		return -1, context.Cause(ctx)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
