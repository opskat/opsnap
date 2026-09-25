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

// run 执行一条命令并返回去掉首尾空白的标准输出。
// 经 SSH 转发的连接不支持截止时间，ctx 结束时关闭客户端来打断等待。
func run(ctx context.Context, cl *ssh.Client, cmd string) (string, error) {
	sess, err := cl.NewSession()
	if err != nil {
		return "", err
	}
	defer func() { _ = sess.Close() }()
	var stdout, stderr bytes.Buffer
	sess.Stdout, sess.Stderr = &stdout, &stderr
	stop := context.AfterFunc(ctx, func() { _ = cl.Close() })
	err = sess.Run(cmd)
	if !stop() {
		return "", ctx.Err()
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return "", fmt.Errorf("执行 %s 失败（退出码 %d）: %s", cmd, exit.ExitStatus(), strings.TrimSpace(stderr.String()))
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}
