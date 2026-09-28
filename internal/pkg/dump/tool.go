package dump

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	// headSize、tailSize 完整性检查保留的输出开头与末尾字节数
	headSize = 16
	tailSize = 512
	// stderrSize 保留的错误输出末尾字节数
	stderrSize = 8 << 10
	// waitDelay 进程被终止后等待其输出管道关闭的上限
	waitDelay = 5 * time.Second
)

// toolRun 一次导出工具的执行：第一次读取时启动，标准输出直接作为导出文件的内容
type toolRun struct {
	s     *Session
	tool  *toolInfo
	args  []string
	env   []string
	check func(head, tail []byte) error

	// 以下只在读取方的 goroutine 中访问
	head, tail []byte
	err        error

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdout  *os.File
	stderr  *tailBuffer
	once    sync.Once
	waitErr error
}

// addTool 登记一个由导出工具产生的文件；wrap 不为 nil 时用它包装工具输出
func (s *Session) addTool(name string, t *toolInfo, args, env []string, check func(head, tail []byte) error, wrap func(io.Reader) io.Reader) {
	r := &toolRun{s: s, tool: t, args: args, env: env, check: check}
	s.runs = append(s.runs, r)
	var src io.Reader = r
	if wrap != nil {
		src = wrap(r)
	}
	s.addFile(name, src)
}

func (r *toolRun) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.stdout == nil {
		if err := r.start(); err != nil {
			r.err = err
			return 0, err
		}
	}
	n, err := r.stdout.Read(p)
	r.record(p[:n])
	if err != nil {
		r.err = r.finish(err)
		return n, r.err
	}
	return n, nil
}

func (r *toolRun) start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.s.closed.Load() {
		return errClosed
	}
	if err := r.s.ctx.Err(); err != nil {
		return err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(r.s.ctx, r.tool.path, r.args...) //nolint:gosec // 工具路径来自 PATH / tools.dir 查找，参数不含秘密
	cmd.Env = r.env
	cmd.Dir = r.s.dir
	cmd.Stdout = pw
	r.stderr = &tailBuffer{max: stderrSize}
	cmd.Stderr = r.stderr
	cmd.WaitDelay = waitDelay
	err = cmd.Start()
	_ = pw.Close()
	if err != nil {
		_ = pr.Close()
		return fmt.Errorf("启动 %s: %w", r.tool.name, err)
	}
	r.cmd, r.stdout = cmd, pr
	return nil
}

func (r *toolRun) record(b []byte) {
	if len(r.head) < headSize {
		r.head = append(r.head, b[:min(len(b), headSize-len(r.head))]...)
	}
	r.tail = append(r.tail, b...)
	if len(r.tail) > tailSize {
		r.tail = append(r.tail[:0], r.tail[len(r.tail)-tailSize:]...)
	}
}

// finish 输出结束（或读取出错）后等待进程退出并判定这份导出：只有正常退出且通过完整性检查才返回 io.EOF
func (r *toolRun) finish(readErr error) error {
	eof := errors.Is(readErr, io.EOF)
	if !eof {
		r.kill()
	}
	werr := r.wait()
	if err := r.s.ctx.Err(); err != nil {
		return err
	}
	if r.s.closed.Load() {
		return errClosed
	}
	if !eof {
		return fmt.Errorf("读取 %s 的输出: %w", r.tool.name, readErr)
	}
	if werr != nil {
		return r.failure(werr)
	}
	if err := r.check(r.head, r.tail); err != nil {
		return err
	}
	if msg := strings.TrimSpace(r.s.scrub(r.stderr.String())); msg != "" {
		for _, line := range strings.Split(msg, "\n") {
			r.s.logf("%s: %s", r.tool.name, line)
		}
	}
	return io.EOF
}

// failure 把工具的失败退出转换为 *ToolError；缺少权限时同时包装 ErrPrivilege
func (r *toolRun) failure(werr error) error {
	te := &ToolError{Tool: r.tool.name, ExitCode: -1, Stderr: strings.TrimSpace(r.s.scrub(r.stderr.String())), Err: r.s.fwd.lastErr()}
	var ee *exec.ExitError
	if errors.As(werr, &ee) {
		te.ExitCode = ee.ExitCode()
	}
	switch {
	case strings.Contains(te.Stderr, "permission denied for table pg_authid"):
		return fmt.Errorf("%w：导出全局对象需要超级用户权限（pg_dumpall 读取 pg_authid）: %w", ErrPrivilege, te)
	case isPrivilegeMessage(te.Stderr):
		return fmt.Errorf("%w：%w", ErrPrivilege, te)
	}
	return te
}

// isPrivilegeMessage 工具错误输出是否表示缺少权限（原文中已写明需要的权限或对象）
func isPrivilegeMessage(msg string) bool {
	for _, s := range []string{"Access denied; you need", "command denied to user", "' to database '", "permission denied for"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func (r *toolRun) kill() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
}

// wait 只等待一次进程退出，读取方与 Close 都可能调用
func (r *toolRun) wait() error {
	r.mu.Lock()
	cmd := r.cmd
	r.mu.Unlock()
	if cmd == nil {
		return nil
	}
	r.once.Do(func() { r.waitErr = cmd.Wait() })
	return r.waitErr
}

// stop 由 Close 调用：终止进程、等待退出并关闭输出管道，使阻塞中的读取返回错误
func (r *toolRun) stop() {
	r.kill()
	_ = r.wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stdout != nil {
		_ = r.stdout.Close()
	}
}

// checkMySQLDump mysqldump 输出的最后一个非空行必须是完成标记
func checkMySQLDump(_, tail []byte) error {
	lines := strings.Split(strings.TrimRight(string(tail), "\r\n\t "), "\n")
	if !strings.HasPrefix(lines[len(lines)-1], "-- Dump completed") {
		return fmt.Errorf("%w：mysqldump 的输出没有以完成标记（-- Dump completed）结尾", ErrIncomplete)
	}
	return nil
}

// checkPGArchive pg_dump custom 格式归档头：PGDMP、版本 3 字节、int 与 offset 大小、格式（1 = custom）
func checkPGArchive(head, _ []byte) error {
	if len(head) < 11 || !bytes.HasPrefix(head, []byte("PGDMP")) || head[10] != 1 {
		return fmt.Errorf("%w：pg_dump 的输出不是完整的 custom 格式归档头", ErrIncomplete)
	}
	return nil
}

// checkPGGlobals pg_dumpall 的输出以集群导出完成标记结尾
func checkPGGlobals(_, tail []byte) error {
	if !bytes.Contains(tail, []byte("PostgreSQL database cluster dump complete")) {
		return fmt.Errorf("%w：pg_dumpall 的输出没有完成标记（PostgreSQL database cluster dump complete）", ErrIncomplete)
	}
	return nil
}

// tailBuffer 只保留最后 max 字节的并发安全缓冲；String 在超出时注明省略了前面多少字节，
// 并从第一个完整的行开始（避免半行，也避免被截断的秘密逃过去秘密）
type tailBuffer struct {
	mu      sync.Mutex
	b       []byte
	max     int
	dropped int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if over := len(t.b) - t.max; over > 0 {
		t.dropped += over
		t.b = append(t.b[:0], t.b[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dropped == 0 {
		return string(t.b)
	}
	b, dropped := t.b, t.dropped
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		b, dropped = b[i+1:], dropped+i+1
	}
	return fmt.Sprintf("（省略了前面 %d 字节的错误输出）\n%s", dropped, b)
}
