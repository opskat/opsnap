package executor

import (
	"context"
	"io"
	"sync"

	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
)

// Step 替身执行器的一次脚本化结果
type Step struct {
	// ExitCode 返回的退出码
	ExitCode int
	// Output 写入输出的内容
	Output string
	// Stderr 标准错误的内容；RunSplit 写入 stderr，Run 在 Output 之后写入合并输出
	Stderr string
	// Err 非空时模拟无法启动：返回 -1 和该错误
	Err error
	// Block 写完 Output 后阻塞到 ctx 结束，返回 -1 和 context.Cause(ctx)，用于模拟超时和手动停止
	Block bool
}

// rule 一条匹配规则及其待消耗的步骤
type rule struct {
	match func(cmdbuild.Cmd) bool
	steps []Step
	next  int
}

// Fake 替身执行器：按注册顺序匹配规则返回脚本化结果，并记录所有调用，供运行引擎测试使用
type Fake struct {
	// Default 没有规则匹配时返回的结果
	Default Step

	mu    sync.Mutex
	rules []*rule
	calls []cmdbuild.Cmd
}

// NewFake 创建替身执行器；未匹配的命令默认以退出码 0 成功
func NewFake() *Fake {
	return &Fake{}
}

// On 注册规则：匹配到的命令依次使用 steps，用完后重复最后一个。先注册的规则优先
func (f *Fake) On(match func(cmdbuild.Cmd) bool, steps ...Step) *Fake {
	if len(steps) == 0 {
		steps = []Step{{}}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, &rule{match: match, steps: steps})
	return f
}

// Calls 返回按顺序记录的所有调用
func (f *Fake) Calls() []cmdbuild.Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cmdbuild.Cmd(nil), f.calls...)
}

// Run 见 Runner
func (f *Fake) Run(ctx context.Context, cmd cmdbuild.Cmd, out io.Writer) (int, error) {
	return f.RunSplit(ctx, cmd, out, out)
}

// RunSplit 见 Streamer
func (f *Fake) RunSplit(ctx context.Context, cmd cmdbuild.Cmd, stdout, stderr io.Writer) (int, error) {
	step := f.pick(cmd)
	if step.Err != nil {
		return -1, step.Err
	}
	if stdout != nil && step.Output != "" {
		if _, err := io.WriteString(stdout, step.Output); err != nil {
			return -1, err
		}
	}
	if stderr != nil && step.Stderr != "" {
		if _, err := io.WriteString(stderr, step.Stderr); err != nil {
			return -1, err
		}
	}
	if step.Block {
		<-ctx.Done()
		return -1, context.Cause(ctx)
	}
	return step.ExitCode, nil
}

// pick 记录调用并取出对应的步骤
func (f *Fake) pick(cmd cmdbuild.Cmd) Step {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, cmd)
	for _, r := range f.rules {
		if !r.match(cmd) {
			continue
		}
		s := r.steps[r.next]
		if r.next < len(r.steps)-1 {
			r.next++
		}
		return s
	}
	return f.Default
}

// Match 匹配程序名，并要求参数以 argsPrefix 开头（例如 Match("restic", "backup")）
func Match(name string, argsPrefix ...string) func(cmdbuild.Cmd) bool {
	return func(c cmdbuild.Cmd) bool {
		if c.Name != name || len(c.Args) < len(argsPrefix) {
			return false
		}
		for i, a := range argsPrefix {
			if c.Args[i] != a {
				return false
			}
		}
		return true
	}
}
