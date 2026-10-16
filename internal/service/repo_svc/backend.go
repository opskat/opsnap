package repo_svc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/sshconn"
)

// probeTimeout 取回 SFTP 主机公钥的时限
const probeTimeout = 10 * time.Second

// keyOpTimeout 初始化仓库、更换密钥这类不能中途放弃的多步操作的总时限
const keyOpTimeout = 10 * time.Minute

// session 访问一个仓库所需的后端；SFTP 可能带着临时文件和经服务器的端口转发，用完必须 close
type session struct {
	backend cmdbuild.Backend
	closers []func()
}

func (ss *session) repo(password string) cmdbuild.Repo {
	return cmdbuild.Repo{Backend: ss.backend, Password: password}
}

func (ss *session) close() {
	for i := len(ss.closers) - 1; i >= 0; i-- {
		ss.closers[i]()
	}
	ss.closers = nil
}

// open 解密凭据并构造后端。WebDAV/SFTP 的密码先经 rclone obscure 处理；
// SFTP 的私钥与已知主机写入 0600 的临时文件，经服务器连接时建立本地端口转发。
func (s *repoSvc) open(ctx context.Context, ent *repo_entity.Repo) (*session, error) {
	p := ent.Params
	cred, err := decrypt(ent.Credential)
	if err != nil {
		return nil, err
	}
	switch ent.Type {
	case repo_entity.TypeLocal:
		return &session{backend: cmdbuild.LocalBackend{Path: p.Path}}, nil
	case repo_entity.TypeS3:
		return &session{backend: cmdbuild.S3Backend{
			Endpoint: p.Endpoint, Bucket: p.Bucket, Prefix: p.Prefix, Region: p.Region,
			AccessKeyID: p.AccessKeyID, SecretAccessKey: cred, PathStyle: p.PathStyle,
		}}, nil
	case repo_entity.TypeREST:
		return &session{backend: cmdbuild.RESTBackend{URL: p.URL, User: p.Username, Password: cred}}, nil
	case repo_entity.TypeWebDAV:
		obscured, err := s.obscure(ctx, cred)
		if err != nil {
			return nil, err
		}
		return &session{backend: cmdbuild.WebDAVBackend{
			URL: p.URL, Path: p.Path, User: p.Username, ObscuredPassword: obscured, Vendor: p.Vendor,
		}}, nil
	case repo_entity.TypeSFTP:
		return s.openSFTP(ctx, ent, cred)
	}
	return nil, i18n.NewError(ctx, code.RepoTypeInvalid)
}

// obscure 用 rclone obscure 处理密码（明文经标准输入传入）；空密码返回空
func (s *repoSvc) obscure(ctx context.Context, password string) (string, error) {
	if password == "" {
		return "", nil
	}
	res := s.exec(ctx)(cmdbuild.RcloneObscure(password))
	if !res.ok() {
		return "", i18n.NewError(ctx, code.RepoAccessFailed, "rclone obscure 失败："+res.reason())
	}
	return strings.TrimSpace(res.stdout), nil
}

func (s *repoSvc) openSFTP(ctx context.Context, ent *repo_entity.Repo, password string) (_ *session, err error) {
	p := ent.Params
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(p.HostKey))
	if err != nil {
		return nil, i18n.NewError(ctx, code.RepoHostKeyInvalid)
	}
	sess := &session{}
	defer func() {
		if err != nil {
			sess.close()
		}
	}()
	dir, err := os.MkdirTemp("", "opsnap-sftp-*")
	if err != nil {
		return nil, err
	}
	sess.closers = append(sess.closers, func() { _ = os.RemoveAll(dir) })

	host, port := p.Host, ent.SFTPPort()
	if p.ServerID != 0 {
		addr, closer, err := forwardVia(ctx, p.ServerID, ent.SFTPAddr())
		if err != nil {
			return nil, i18n.NewError(ctx, code.RepoAccessFailed, err.Error())
		}
		sess.closers = append(sess.closers, func() { _ = closer.Close() })
		h, ps, _ := net.SplitHostPort(addr)
		host, port = h, atoi(ps)
	}
	// rclone 按实际连接的地址校验主机公钥；经转发时就是本地转发端口
	known := knownhosts.Line([]string{knownhosts.Normalize(net.JoinHostPort(host, strconv.Itoa(port)))}, pub)
	knownFile := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownFile, []byte(known+"\n"), 0o600); err != nil {
		return nil, err
	}
	backend := cmdbuild.SFTPBackend{Host: host, Port: port, User: p.Username, Path: p.Path, KnownHostsFile: knownFile}
	if ent.PrivateKey != "" {
		key, err := decrypt(ent.PrivateKey)
		if err != nil {
			return nil, err
		}
		backend.KeyFile = filepath.Join(dir, "id")
		if err := os.WriteFile(backend.KeyFile, []byte(key), 0o600); err != nil {
			return nil, err
		}
	}
	if backend.ObscuredPassword, err = s.obscure(ctx, password); err != nil {
		return nil, err
	}
	sess.backend = backend
	return sess, nil
}

// connCloser 依次关闭转发与 SSH 连接
type connCloser struct {
	fwd  io.Closer
	conn *sshconn.Conn
}

func (c connCloser) Close() error {
	return errors.Join(c.fwd.Close(), c.conn.Close())
}

// forwardVia 经已保存的服务器（及其代理链）把 NAS 的 SFTP 端口转发到本地，返回本地地址
func forwardVia(ctx context.Context, serverID int64, addr string) (string, io.Closer, error) {
	conn, _, err := sshconn.Dial(ctx, serverID)
	if err != nil {
		return "", nil, fmt.Errorf("经服务器连接 NAS 失败：%w", err)
	}
	local, fwd, err := conn.Forward(addr)
	if err != nil {
		_ = conn.Close()
		return "", nil, fmt.Errorf("经服务器转发 %s 失败：%w", addr, err)
	}
	return local, connCloser{fwd: fwd, conn: conn}, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// errProbeDone 拿到主机公钥后中止握手
var errProbeDone = errors.New("已取回主机公钥")
