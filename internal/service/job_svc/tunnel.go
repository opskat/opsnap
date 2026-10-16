package job_svc

import (
	"context"

	"github.com/opskat/opsnap/internal/pkg/sshconn"
)

// sshTunnel 经已保存的服务器（含代理链）连上目标 SSH 主机，并把本机一个随机端口转发到 addr
func sshTunnel(ctx context.Context, serverID int64, addr string) (string, func(), error) {
	conn, _, err := sshconn.Dial(ctx, serverID)
	if err != nil {
		return "", nil, err
	}
	if addr == "" {
		return "", func() { _ = conn.Close() }, nil
	}
	local, fwd, err := conn.Forward(addr)
	if err != nil {
		_ = conn.Close()
		return "", nil, err
	}
	return local, func() {
		_ = fwd.Close()
		_ = conn.Close()
	}, nil
}
