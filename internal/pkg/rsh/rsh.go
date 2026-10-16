// Package rsh 让 rsync 经 opsnap 已建立的 SSH 连接（含代理链与指纹校验）访问服务器，
// 凭据始终留在 opsnap 主进程里。
//
// 设计：运行引擎为一次运行启动 Bridge —— 在只有当前用户可访问（0700）的临时目录里监听 unix socket，
// 并生成一次性的随机令牌。rsync 以 `-e "<opsnap> rsh"` 启动本程序的隐藏子命令 rsh；
// 子命令从环境变量 OPSNAP_RSH_SOCKET / OPSNAP_RSH_TOKEN 读到地址和令牌，连上 socket 后把远端命令
// （rsync --server …）交给主进程。主进程核对令牌，在已经建立并校验过指纹的 SSH 连接上开会话执行命令，
// 再经 socket 转发标准输入输出并回传退出码。子进程拿不到服务器密码、私钥、口令或主密钥，
// 命令行参数里也没有任何凭据；令牌只在这次运行期间有效。
package rsh

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/gogo"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
)

const (
	// EnvSocket 主进程监听的 unix socket 路径
	EnvSocket = "OPSNAP_RSH_SOCKET"
	// EnvToken 本次运行的一次性令牌
	EnvToken = "OPSNAP_RSH_TOKEN" //nolint:gosec // 环境变量名，不是凭据
	// Subcommand opsnap 的隐藏子命令名
	Subcommand = "rsh"
	// Host rsync 源地址里的占位主机名；实际连接的服务器由 Bridge 决定
	Host = "opsnap"

	// exitFailure 无法建立转发等本地错误时的退出码，与 ssh 一致
	exitFailure = 255
	// maxHello 握手消息的上限
	maxHello = 64 << 10
	// helloTimeout 等待子进程发来握手消息的时限
	helloTimeout = 10 * time.Second
)

// 主进程发给子进程的帧类型：1 字节类型 + 4 字节大端长度 + 内容
const (
	frameStdout byte = 1
	frameExit   byte = 2
)

// hello 子进程连上后发送的第一行（JSON）
type hello struct {
	Token   string `json:"token"`
	Command string `json:"command"`
}

// Bridge 一次运行的转发端点
type Bridge struct {
	client *ssh.Client
	stderr io.Writer
	dir    string
	socket string
	token  string
	ln     net.Listener

	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]struct{}
}

// Serve 为已建立的 SSH 连接启动转发端点。stderr 接收服务器端命令的标准错误（运行日志），须可并发写入。
func Serve(client *ssh.Client, stderr io.Writer) (*Bridge, error) {
	if client == nil {
		return nil, errors.New("缺少 SSH 连接")
	}
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	// unix socket 路径长度有限（macOS 104 字节），放在系统临时目录下的短目录里
	dir, err := os.MkdirTemp("", "opsnap-rsh-")
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	b := &Bridge{
		client: client, stderr: stderr, dir: dir, socket: sock, token: hex.EncodeToString(tok), ln: ln,
		conns: map[net.Conn]struct{}{},
	}
	gogo.Go(func() error {
		b.accept()
		return nil
	})
	return b, nil
}

// Env 传给 rsync 的环境变量（rsync 会原样传给 rsh 子命令）
func (b *Bridge) Env() []string {
	return []string{EnvSocket + "=" + b.socket, EnvToken + "=" + b.token}
}

// Token 本次的令牌，调用方应把它加入日志遮盖
func (b *Bridge) Token() string { return b.token }

// Close 停止监听，断开进行中的转发并删除临时目录
func (b *Bridge) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	conns := b.conns
	b.conns = nil
	b.mu.Unlock()
	err := b.ln.Close()
	for c := range conns {
		_ = c.Close()
	}
	if rmErr := os.RemoveAll(b.dir); err == nil {
		err = rmErr
	}
	return err
}

func (b *Bridge) accept() {
	for {
		c, err := b.ln.Accept()
		if err != nil {
			return
		}
		if !b.track(c) {
			_ = c.Close()
			return
		}
		gogo.Go(func() error {
			defer b.untrack(c)
			b.handle(c)
			return nil
		})
	}
}

func (b *Bridge) track(c net.Conn) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return false
	}
	b.conns[c] = struct{}{}
	return true
}

func (b *Bridge) untrack(c net.Conn) {
	_ = c.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.conns, c)
}

// handle 核对令牌，在 SSH 连接上执行远端命令并转发数据
func (b *Bridge) handle(c net.Conn) {
	br := bufio.NewReaderSize(c, maxHello)
	_ = c.SetReadDeadline(time.Now().Add(helloTimeout))
	line, err := br.ReadSlice('\n')
	if err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	var h hello
	if err := json.Unmarshal(line, &h); err != nil {
		return
	}
	if subtle.ConstantTimeCompare([]byte(h.Token), []byte(b.token)) != 1 || strings.TrimSpace(h.Command) == "" {
		_ = writeFrame(c, frameExit, exitPayload(exitFailure))
		return
	}
	_ = writeFrame(c, frameExit, exitPayload(b.run(c, br, h.Command)))
}

// run 开会话执行命令，返回远端退出码
func (b *Bridge) run(c net.Conn, stdin io.Reader, command string) int {
	sess, err := b.client.NewSession()
	if err != nil {
		b.logf("opsnap rsh：无法在服务器上开启会话：%v\n", err)
		return exitFailure
	}
	defer func() { _ = sess.Close() }()
	in, err := sess.StdinPipe()
	if err != nil {
		return exitFailure
	}
	sess.Stdout = frameWriter{c}
	if b.stderr != nil {
		sess.Stderr = b.stderr
	} else {
		sess.Stderr = io.Discard
	}
	if err := sess.Start(command); err != nil {
		b.logf("opsnap rsh：无法执行远端命令：%v\n", err)
		return exitFailure
	}
	// 标准输入自己转发，不交给会话等待：远端命令结束时本地标准输入可能还没关
	gogo.Go(func() error {
		_, _ = io.Copy(in, stdin)
		_ = in.Close()
		return nil
	})
	err = sess.Wait()
	var ee *ssh.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.ExitStatus()
	default:
		b.logf("opsnap rsh：远端命令异常结束：%v\n", err)
		return exitFailure
	}
}

func (b *Bridge) logf(format string, args ...any) {
	if b.stderr != nil {
		_, _ = fmt.Fprintf(b.stderr, format, args...)
	}
}

// frameWriter 把远端标准输出封装成帧
type frameWriter struct{ w io.Writer }

func (f frameWriter) Write(p []byte) (int, error) {
	if err := writeFrame(f.w, frameStdout, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	frame := make([]byte, 5+len(payload))
	frame[0] = typ
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	_, err := w.Write(frame)
	return err
}

func exitPayload(code int) []byte {
	p := make([]byte, 4)
	binary.BigEndian.PutUint32(p, uint32(int32(code)))
	return p
}

// Main 是 `opsnap rsh` 子命令：rsync 以 `<opsnap> rsh [-l 用户] 主机 命令…` 调用它。
// 返回值作为进程退出码，即远端命令的退出码；无法转发时返回 255。
func Main(getenv func(string) string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	sock, token := getenv(EnvSocket), getenv(EnvToken)
	if sock == "" || token == "" {
		_, _ = fmt.Fprintln(stderr, "opsnap rsh：只能由 opsnap 运行引擎经 rsync 启动")
		return exitFailure
	}
	command, err := remoteCommand(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "opsnap rsh：%v\n", err)
		return exitFailure
	}
	c, err := net.DialTimeout("unix", sock, helloTimeout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "opsnap rsh：连不上 opsnap：%v\n", err)
		return exitFailure
	}
	defer func() { _ = c.Close() }()
	msg, err := json.Marshal(hello{Token: token, Command: command})
	if err != nil {
		return exitFailure
	}
	if _, err := c.Write(append(msg, '\n')); err != nil {
		_, _ = fmt.Fprintf(stderr, "opsnap rsh：%v\n", err)
		return exitFailure
	}
	gogo.Go(func() error {
		_, _ = io.Copy(c, stdin)
		if uc, ok := c.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
		return nil
	})
	return readFrames(c, stdout, stderr)
}

// readFrames 把标准输出帧写到 stdout，读到退出帧时返回退出码
func readFrames(r io.Reader, stdout, stderr io.Writer) int {
	br := bufio.NewReader(r)
	head := make([]byte, 5)
	for {
		if _, err := io.ReadFull(br, head); err != nil {
			_, _ = fmt.Fprintln(stderr, "opsnap rsh：连接在命令结束前断开")
			return exitFailure
		}
		n := binary.BigEndian.Uint32(head[1:])
		switch head[0] {
		case frameStdout:
			if _, err := io.CopyN(stdout, br, int64(n)); err != nil {
				return exitFailure
			}
		case frameExit:
			p := make([]byte, 4)
			if n != 4 {
				return exitFailure
			}
			if _, err := io.ReadFull(br, p); err != nil {
				return exitFailure
			}
			return int(int32(binary.BigEndian.Uint32(p)))
		default:
			return exitFailure
		}
	}
}

// remoteCommand 从 rsync 传来的参数中取出远端命令：跳过 -l 用户 等选项和主机名，余下的按 ssh 的做法用空格连接
func remoteCommand(args []string) (string, error) {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		switch args[i] {
		case "-l", "-p":
			i += 2
		default:
			i++
		}
	}
	if i >= len(args) {
		return "", errors.New("缺少主机名")
	}
	cmd := strings.Join(args[i+1:], " ")
	if strings.TrimSpace(cmd) == "" {
		return "", errors.New("缺少远端命令，不支持交互式登录")
	}
	return cmd, nil
}

// RsyncSpec 经桥接把服务器上的路径增量同步到本机镜像目录
type RsyncSpec struct {
	// Self opsnap 可执行文件的绝对路径，rsync 用它启动 rsh 子命令
	Self string
	// Paths 服务器上的绝对路径，按完整路径保留在镜像目录下
	Paths []string
	// Excludes 排除规则，写法同 rsync --exclude
	Excludes []string
	// Dest 本机镜像目录
	Dest string
	// BwLimitKiB 限速，0 表示不限
	BwLimitKiB int
	// ExtraArgs 任务「高级」里的附加参数
	ExtraArgs []string
}

// Rsync 生成经桥接的 rsync 命令。env 是 Bridge.Env()，只含 socket 路径与令牌；参数里没有凭据。
// 退出码 24（同步期间文件消失）由调用方记为「有警告」。
func Rsync(s RsyncSpec, env []string) (cmdbuild.Cmd, error) {
	if !filepath.IsAbs(s.Self) {
		return cmdbuild.Cmd{}, fmt.Errorf("opsnap 程序路径必须是绝对路径：%q", s.Self)
	}
	if strings.ContainsAny(s.Self, "'\"\\") {
		return cmdbuild.Cmd{}, fmt.Errorf("opsnap 程序路径不能含引号或反斜杠：%q", s.Self)
	}
	if len(s.Paths) == 0 {
		return cmdbuild.Cmd{}, errors.New("至少需要一个路径")
	}
	for _, p := range s.Paths {
		if !strings.HasPrefix(p, "/") {
			return cmdbuild.Cmd{}, fmt.Errorf("服务器路径必须是绝对路径：%q", p)
		}
	}
	if !strings.HasPrefix(s.Dest, "/") {
		return cmdbuild.Cmd{}, fmt.Errorf("镜像目录必须是绝对路径：%q", s.Dest)
	}
	self := s.Self
	if strings.ContainsAny(self, " \t") {
		self = "'" + self + "'"
	}
	args := []string{
		"--archive",
		"--relative",
		"--delete",
		"--delete-excluded",
		"--numeric-ids",
		// 整体进度（\r 分隔的行），供运行引擎读出已传输的字节数
		"--info=progress2",
		"-e", self + " " + Subcommand,
	}
	for _, e := range s.Excludes {
		args = append(args, "--exclude="+e)
	}
	if s.BwLimitKiB > 0 {
		args = append(args, "--bwlimit="+strconv.Itoa(s.BwLimitKiB))
	}
	args = append(args, s.ExtraArgs...)
	for _, p := range s.Paths {
		args = append(args, Host+":"+p)
	}
	args = append(args, strings.TrimRight(s.Dest, "/")+"/")
	return cmdbuild.Cmd{Name: "rsync", Args: args, Env: append([]string(nil), env...)}, nil
}
