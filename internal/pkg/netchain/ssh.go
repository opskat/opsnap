package netchain

import (
	"context"
	"fmt"
	"net"
	"strings"

	"golang.org/x/crypto/ssh"
)

// sshHandshake 在 conn 上完成 SSH 握手与认证。
// 主机密钥在密钥交换阶段校验，早于任何认证请求：未确认或不一致时直接中止，不发送凭据。
func sshHandshake(ctx context.Context, conn net.Conn, h Hop, signer ssh.Signer) (*ssh.Client, error) {
	auth := ssh.Password(h.Password)
	if signer != nil {
		auth = ssh.PublicKeys(signer)
	}
	var hostKeyErr *HostKeyError
	cfg := &ssh.ClientConfig{
		User: h.User,
		Auth: []ssh.AuthMethod{auth},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fp := ssh.FingerprintSHA256(key)
			switch {
			case h.HostKey == "":
				hostKeyErr = &HostKeyError{KeyType: key.Type(), Fingerprint: fp}
			case fp != h.HostKey:
				hostKeyErr = &HostKeyError{Changed: true, KeyType: key.Type(), Fingerprint: fp, Saved: h.HostKey}
			default:
				return nil
			}
			return hostKeyErr
		},
	}
	// 经 SSH 通道转发的连接不支持读写截止时间，ctx 结束时直接关闭连接来打断握手
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	sc, chans, reqs, err := ssh.NewClientConn(conn, h.addr(), cfg)
	if !stop() && err == nil {
		_ = sc.Close()
		err = ctx.Err()
	}
	switch {
	case err == nil:
		return ssh.NewClient(sc, chans, reqs), nil
	case hostKeyErr != nil:
		return nil, hostKeyErr
	case ctx.Err() != nil:
		return nil, ctx.Err()
	// x/crypto/ssh 不导出认证失败的错误类型，只能按消息识别
	case strings.Contains(err.Error(), "unable to authenticate"):
		return nil, fmt.Errorf("%w: %w", ErrAuthFailed, err)
	}
	return nil, fmt.Errorf("%w：SSH 握手失败: %w", ErrProtocol, err)
}
