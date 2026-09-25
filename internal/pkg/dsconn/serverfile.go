package dsconn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/opskat/opsnap/internal/pkg/netchain"
)

// unameCmd 服务器文件连接测试执行的命令，读取系统与架构
const unameCmd = "uname -sm"

func (c Config) sshHop() netchain.Hop {
	return netchain.Hop{
		ID: c.ID, Name: c.Name, Kind: netchain.KindSSH, Host: c.Host, Port: c.Port,
		User: c.User, Password: c.Password, PrivateKey: c.PrivateKey, Passphrase: c.Passphrase, HostKey: c.HostKey,
	}
}

// openServerFile 经链路登录目标主机（主机密钥规则与 SSH 跳相同），执行 uname -sm
func openServerFile(ctx context.Context, d Dialer, c Config) (*Conn, error) {
	cl, err := d.DialSSH(ctx, c.sshHop())
	if err != nil {
		return nil, wrapError(ctx, err, c.Password, string(c.Passphrase))
	}
	system, err := run(ctx, cl, unameCmd)
	if err != nil {
		_ = cl.Close()
		return nil, wrapError(ctx, err, c.Password, string(c.Passphrase))
	}
	return &Conn{Info: Info{System: system}, SSH: cl}, nil
}

// run 执行一条命令并返回去掉首尾空白的标准输出；退出码非 0 时返回带标准错误的错误
func run(ctx context.Context, cl *ssh.Client, cmd string) (string, error) {
	stdout, stderr, code, err := Exec(ctx, cl, cmd)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("执行 %s 失败（退出码 %d）: %s", cmd, code, strings.TrimSpace(stderr))
	}
	return strings.TrimSpace(stdout), nil
}

// Exec 在 cl 上执行一条命令，返回标准输出、标准错误与退出码；命令能执行但退出码非 0 不算错误。
// 经链路转发的连接不支持读写截止时间（x/crypto/ssh 的限制），ctx 结束时关闭客户端来打断等待。
func Exec(ctx context.Context, cl *ssh.Client, cmd string) (stdout, stderr string, exitCode int, err error) {
	sess, err := cl.NewSession()
	if err != nil {
		return "", "", -1, err
	}
	defer func() { _ = sess.Close() }()
	var out, errBuf bytes.Buffer
	sess.Stdout, sess.Stderr = &out, &errBuf
	stop := context.AfterFunc(ctx, func() { _ = cl.Close() })
	runErr := sess.Run(cmd)
	if !stop() {
		return "", "", -1, ctx.Err()
	}
	var exit *ssh.ExitError
	switch {
	case errors.As(runErr, &exit):
		return out.String(), errBuf.String(), exit.ExitStatus(), nil
	case runErr != nil:
		return "", "", -1, runErr
	}
	return out.String(), errBuf.String(), 0, nil
}
