package sshconn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/opskat/opsnap/internal/model/entity/credential_entity"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/credential_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
)

// ErrServerNotFound 服务器或代理链引用的跳板服务器不存在
var ErrServerNotFound = errors.New("服务器不存在")

// ErrCredentialNotFound 服务器引用的登录凭据不存在
var ErrCredentialNotFound = errors.New("服务器引用的凭据不存在")

// DialOption Dial 的可选参数
type DialOption func(*dialOptions)

type dialOptions struct {
	now     func() time.Time
	timeout time.Duration
}

// WithNow 指定记录连接状态用的时钟
func WithNow(now func() time.Time) DialOption {
	return func(o *dialOptions) { o.now = now }
}

// WithTimeout 指定每一跳的时限
func WithTimeout(d time.Duration) DialOption {
	return func(o *dialOptions) { o.timeout = d }
}

// Dial 连接已保存的服务器：从仓储读取服务器与代理链，用主密钥解密凭据，
// 逐跳校验 SSH 主机指纹（目标与跳板都必须与已核对的一致），
// 并把结果记为服务器的连接状态（正常 + 延迟，或从某时起连不上）。调用方取消时不记录。
func Dial(ctx context.Context, serverID int64, opts ...DialOption) (*Conn, []HopResult, error) {
	o := dialOptions{now: time.Now}
	for _, opt := range opts {
		opt(&o)
	}
	repo := server_repo.Server()
	srv, err := repo.Find(ctx, serverID)
	if err != nil {
		return nil, nil, err
	}
	if srv == nil {
		return nil, nil, fmt.Errorf("%w：id=%d", ErrServerNotFound, serverID)
	}
	cfg, err := ServerConfig(ctx, srv)
	if err != nil {
		return nil, nil, err
	}
	cfg.Timeout = o.timeout
	conn, results, err := DialConfig(ctx, cfg)

	var he *HopError
	if err == nil || (errors.As(err, &he) && he.Reason != ReasonCanceled) {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		srv.RecordCheck(err == nil, TotalLatency(results), msg, o.now())
		// 状态是附带记录，写入失败不影响本次连接结果
		_ = repo.UpdateStatus(context.WithoutCancel(ctx), srv)
	}
	return conn, results, err
}

// TotalLatency 各跳延迟之和
func TotalLatency(results []HopResult) time.Duration {
	var total time.Duration
	for _, r := range results {
		total += r.Latency
	}
	return total
}

// ServerConfig 把已保存的服务器转换成连接配置：按 CredentialID 读取并解密登录凭据，
// SSH 跳板按 ServerID 读取对应服务器的地址、凭据和已核对的指纹（跳板自己的代理链不参与）。
func ServerConfig(ctx context.Context, srv *server_entity.Server) (Config, error) {
	return ServerConfigWithCredential(ctx, srv, nil)
}

// ServerConfigWithCredential 同上，但目标的登录凭据用给出的这条（还未落库的待测试凭据）。
// pending 为 nil 时按 srv.CredentialID 从仓储读取。
func ServerConfigWithCredential(ctx context.Context, srv *server_entity.Server, pending *credential_entity.Credential) (Config, error) {
	sec := secret.Default()
	if sec == nil {
		return Config{}, errors.New("主密钥未加载，无法解密服务器凭据")
	}
	target, err := sshHopOf(ctx, sec, srv, pending)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{Target: target}
	for i, h := range srv.Hops {
		switch h.Type {
		case server_entity.HopSSH:
			jump, err := server_repo.Server().Find(ctx, h.ServerID)
			if err != nil {
				return Config{}, err
			}
			if jump == nil {
				return Config{}, fmt.Errorf("代理链第 %d 跳引用的%w", i+1, ErrServerNotFound)
			}
			hop, err := sshHopOf(ctx, sec, jump, nil)
			if err != nil {
				return Config{}, err
			}
			cfg.Hops = append(cfg.Hops, hop)
		case server_entity.HopSOCKS5, server_entity.HopHTTP:
			pw, err := decrypt(sec, h.Password)
			if err != nil {
				return Config{}, err
			}
			cfg.Hops = append(cfg.Hops, Hop{Kind: Kind(h.Type), Host: h.Host, Port: h.Port, User: h.Username, Password: pw})
		default:
			return Config{}, fmt.Errorf("代理链第 %d 跳的类型 %q 不受支持", i+1, h.Type)
		}
	}
	return cfg, nil
}

// sshHopOf 把一台服务器转换成 SSH 跳：地址与已核对的指纹来自服务器，凭据来自 pending（待测试的新凭据）
// 或它引用的那条凭据；用凭据类型决定走密码还是私钥。
func sshHopOf(ctx context.Context, sec secret.Secret, s *server_entity.Server, pending *credential_entity.Credential) (Hop, error) {
	hop := Hop{Kind: KindSSH, Name: s.Name, Host: s.Host, Port: s.Port, User: s.Username, Fingerprint: s.HostKeyFingerprint}
	cred := pending
	if cred == nil {
		var err error
		if cred, err = credential_repo.Credential().Find(ctx, s.CredentialID); err != nil {
			return hop, err
		}
		if cred == nil {
			return hop, fmt.Errorf("服务器 %s 引用的%w：id=%d", s.Name, ErrCredentialNotFound, s.CredentialID)
		}
	}
	return hopWithCredential(sec, hop, cred)
}

// hopWithCredential 按凭据的类型解密密码或私钥（及可选口令）
func hopWithCredential(sec secret.Secret, hop Hop, cred *credential_entity.Credential) (Hop, error) {
	var err error
	switch cred.Kind {
	case credential_entity.KindPassword:
		hop.Password, err = decrypt(sec, cred.Password)
	case credential_entity.KindSSHKey:
		if hop.PrivateKey, err = decrypt(sec, cred.PrivateKey); err == nil {
			hop.Passphrase, err = decrypt(sec, cred.Passphrase)
		}
	default:
		err = fmt.Errorf("服务器 %s 的凭据类型 %q 不受支持", hop.Name, cred.Kind)
	}
	return hop, err
}

func decrypt(sec secret.Secret, cipher string) (string, error) {
	if cipher == "" {
		return "", nil
	}
	return sec.Decrypt(cipher)
}
