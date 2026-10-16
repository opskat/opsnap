package executor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
)

// alive 判断进程是否还存在（僵尸进程被回收前也算存在，所以调用方需要轮询）
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// waitGone 在超时前轮询，直到进程消失
func waitGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return !alive(pid)
}

// readPid 等待子进程把自己的 pid 写进文件
func readPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path) //nolint:gosec // 测试临时目录里的文件
		if err == nil && strings.HasSuffix(string(b), "\n") {
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			require.NoError(t, err)
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("没有读到子进程 pid")
	return 0
}

func TestExecRun(t *testing.T) {
	convey.Convey("执行子进程", t, func() {
		r := New()
		ctx := context.Background()

		convey.Convey("合并输出 stdout 与 stderr，返回退出码", func() {
			var out bytes.Buffer
			code, err := r.Run(ctx, cmdbuild.Cmd{Name: "sh", Args: []string{"-c", "echo out; echo err 1>&2; exit 3"}}, &out)
			require.NoError(t, err)
			assert.Equal(t, 3, code)
			assert.Contains(t, out.String(), "out\n")
			assert.Contains(t, out.String(), "err\n")
		})
		convey.Convey("成功时退出码为 0", func() {
			code, err := r.Run(ctx, cmdbuild.Cmd{Name: "true"}, nil)
			require.NoError(t, err)
			assert.Equal(t, 0, code)
		})
		convey.Convey("Env 和 Stdin 传给子进程", func() {
			var out bytes.Buffer
			code, err := r.Run(ctx, cmdbuild.Cmd{
				Name:  "sh",
				Args:  []string{"-c", `printf '%s|' "$OPSNAP_T"; cat`},
				Env:   []string{"OPSNAP_T=env-value"},
				Stdin: []byte("stdin-value"),
			}, &out)
			require.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.Equal(t, "env-value|stdin-value", out.String())
		})
		convey.Convey("不继承本进程的主密钥等环境变量，但保留 PATH", func() {
			t.Setenv("OPSNAP_SECRET_KEY", "master-should-not-leak")
			var out bytes.Buffer
			code, err := r.Run(ctx, cmdbuild.Cmd{Name: "sh", Args: []string{"-c", `printf '%s|%s' "$OPSNAP_SECRET_KEY" "$PATH"`}}, &out)
			require.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.NotContains(t, out.String(), "master-should-not-leak")
			assert.True(t, strings.HasPrefix(out.String(), "|"))
			assert.Contains(t, out.String(), os.Getenv("PATH"))
		})
		convey.Convey("程序不存在时返回错误", func() {
			code, err := r.Run(ctx, cmdbuild.Cmd{Name: "opsnap-no-such-binary"}, nil)
			assert.Error(t, err)
			assert.Equal(t, -1, code)
		})
	})
}

func TestExecKillsProcessGroup(t *testing.T) {
	convey.Convey("context 结束时终止整个进程组", t, func() {
		r := New()
		r.KillGrace = 300 * time.Millisecond
		dir := t.TempDir()
		pidFile := filepath.Join(dir, "child.pid")

		convey.Convey("超时：shell 和它启动的后台子进程都被终止", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			// 先写临时文件再改名，避免读到半截 pid
			script := `sleep 30 & printf '%s\n' $! > "$1.tmp"; mv "$1.tmp" "$1"; wait`
			start := time.Now()
			code, err := r.Run(ctx, cmdbuild.Cmd{Name: "sh", Args: []string{"-c", script, "sh", pidFile}}, nil)
			assert.Equal(t, -1, code)
			assert.True(t, errors.Is(err, context.DeadlineExceeded), "err=%v", err)
			assert.Less(t, time.Since(start), 10*time.Second)
			child := readPid(t, pidFile)
			assert.True(t, waitGone(child, 3*time.Second), "子进程 %d 仍然存活", child)
		})
		convey.Convey("手动停止：返回 context 的 cause", func() {
			ctx, cancel := context.WithCancelCause(context.Background())
			stop := errors.New("手动停止")
			go func() {
				readPid(t, pidFile)
				cancel(stop)
			}()
			script := `sleep 30 & printf '%s\n' $! > "$1.tmp"; mv "$1.tmp" "$1"; wait`
			code, err := r.Run(ctx, cmdbuild.Cmd{Name: "sh", Args: []string{"-c", script, "sh", pidFile}}, nil)
			assert.Equal(t, -1, code)
			assert.ErrorIs(t, err, stop)
			child := readPid(t, pidFile)
			assert.True(t, waitGone(child, 3*time.Second), "子进程 %d 仍然存活", child)
		})
		convey.Convey("忽略 SIGTERM 的子进程在宽限期后被 SIGKILL", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			script := `trap '' TERM; sleep 30 & printf '%s\n' $! > "$1.tmp"; mv "$1.tmp" "$1"; wait`
			code, err := r.Run(ctx, cmdbuild.Cmd{Name: "sh", Args: []string{"-c", script, "sh", pidFile}}, nil)
			assert.Equal(t, -1, code)
			assert.True(t, errors.Is(err, context.DeadlineExceeded), "err=%v", err)
			child := readPid(t, pidFile)
			assert.True(t, waitGone(child, 3*time.Second), "子进程 %d 仍然存活", child)
		})
		convey.Convey("已经结束的 context 不启动进程", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			code, err := r.Run(ctx, cmdbuild.Cmd{Name: "sh", Args: []string{"-c", "touch \"$1\"", "sh", pidFile}}, nil)
			assert.Equal(t, -1, code)
			assert.ErrorIs(t, err, context.Canceled)
			_, statErr := os.Stat(pidFile)
			assert.True(t, os.IsNotExist(statErr))
		})
	})
}

func TestFake(t *testing.T) {
	convey.Convey("替身执行器", t, func() {
		f := NewFake()
		ctx := context.Background()
		backup := cmdbuild.Cmd{Name: "restic", Args: []string{"backup", "--json"}, Env: []string{"RESTIC_PASSWORD=x"}}
		rsync := cmdbuild.Cmd{Name: "rsync", Args: []string{"--archive"}}

		convey.Convey("按匹配规则返回脚本化的退出码和输出，并记录调用", func() {
			f.On(Match("restic", "backup"), Step{ExitCode: 0, Output: `{"snapshot_id":"abc"}`})
			f.On(Match("rsync"), Step{ExitCode: 24, Output: "file has vanished"})
			var out bytes.Buffer
			code, err := f.Run(ctx, rsync, &out)
			require.NoError(t, err)
			assert.Equal(t, 24, code)
			assert.Equal(t, "file has vanished", out.String())
			out.Reset()
			code, err = f.Run(ctx, backup, &out)
			require.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.Equal(t, `{"snapshot_id":"abc"}`, out.String())
			calls := f.Calls()
			require.Len(t, calls, 2)
			assert.Equal(t, rsync, calls[0])
			assert.Equal(t, backup, calls[1])
		})
		convey.Convey("同一规则的多个步骤依次消耗，最后一步重复使用", func() {
			f.On(Match("restic"), Step{ExitCode: 1}, Step{ExitCode: 0})
			codes := []int{}
			for i := 0; i < 3; i++ {
				code, err := f.Run(ctx, backup, nil)
				require.NoError(t, err)
				codes = append(codes, code)
			}
			assert.Equal(t, []int{1, 0, 0}, codes)
		})
		convey.Convey("先注册的规则优先", func() {
			f.On(Match("restic", "backup"), Step{ExitCode: 3})
			f.On(Match("restic"), Step{ExitCode: 5})
			code, _ := f.Run(ctx, backup, nil)
			assert.Equal(t, 3, code)
			code, _ = f.Run(ctx, cmdbuild.Cmd{Name: "restic", Args: []string{"snapshots"}}, nil)
			assert.Equal(t, 5, code)
		})
		convey.Convey("没有匹配的规则时返回 Default", func() {
			f.Default = Step{ExitCode: 9}
			code, err := f.Run(ctx, rsync, nil)
			require.NoError(t, err)
			assert.Equal(t, 9, code)
		})
		convey.Convey("脚本化的启动错误", func() {
			boom := errors.New("exec: not found")
			f.On(Match("rsync"), Step{Err: boom})
			code, err := f.Run(ctx, rsync, nil)
			assert.Equal(t, -1, code)
			assert.ErrorIs(t, err, boom)
		})
		convey.Convey("阻塞步骤一直等到 context 结束，模拟超时和手动停止", func() {
			f.On(Match("restic"), Step{Block: true, Output: "partial"})
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			var out bytes.Buffer
			code, err := f.Run(ctx, backup, &out)
			assert.Equal(t, -1, code)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Equal(t, "partial", out.String())
		})
		convey.Convey("Match 只匹配参数的前缀子命令", func() {
			assert.True(t, Match("restic")(backup))
			assert.True(t, Match("restic", "backup")(backup))
			assert.False(t, Match("restic", "forget")(backup))
			assert.False(t, Match("rsync")(backup))
			assert.False(t, Match("restic", "backup", "--json", "x")(backup))
		})
		convey.Convey("满足 Runner 接口", func() {
			var _ Runner = f
			var _ Runner = New()
		})
	})
}
