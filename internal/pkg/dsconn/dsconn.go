// Package dsconn 经网络通道链路连接数据源（MySQL、PostgreSQL、服务器文件）并读取基本信息：
// 数据库认证后读取服务端版本与 TLS 状态，服务器文件确认主机密钥、认证后执行 `uname -sm`。
// 连接过程只读，不修改数据源上的任何配置或数据；返回的错误不含任何秘密。
package dsconn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"

	"golang.org/x/crypto/ssh"

	"github.com/opskat/opsnap/internal/pkg/netchain"
)

// Type 数据源类型
type Type string

const (
	TypeMySQL      Type = "mysql"
	TypePostgreSQL Type = "postgres"
	TypeServerFile Type = "server_file"
)

// TLSMode MySQL / PostgreSQL 的 TLS 模式
type TLSMode string

const (
	// TLSDisable 不加密
	TLSDisable TLSMode = "disable"
	// TLSPrefer 优先加密（默认）：服务端支持就加密，不校验证书，不支持则不加密
	TLSPrefer TLSMode = "prefer"
	// TLSRequire 必须加密，不校验证书
	TLSRequire TLSMode = "require"
	// TLSVerifyCA 必须加密，校验证书链，不校验主机名
	TLSVerifyCA TLSMode = "verify_ca"
	// TLSVerifyFull 必须加密，校验证书链与主机名
	TLSVerifyFull TLSMode = "verify_full"
)

// DefaultPGDatabase PostgreSQL 未指定连接数据库时使用的库
const DefaultPGDatabase = "postgres"

// TLSConfig TLS 设置，证书与私钥均为 PEM
type TLSConfig struct {
	// Mode 为空时按 TLSPrefer
	Mode TLSMode
	// CA 可选；两种校验模式下为空时使用系统信任库
	CA []byte
	// ClientCert 与 ClientKey（mTLS）可选，但必须同时提供
	ClientCert []byte
	ClientKey  []byte
}

// Config 一个数据源的连接参数
type Config struct {
	// ID、Name 数据源的标识与名称，只用于错误定位（服务器文件的目标主机）
	ID   int64
	Name string

	Type Type
	Host string
	Port int
	User string
	// Password 数据库密码，或服务器文件的 SSH 密码
	Password string

	// Database PostgreSQL 的连接数据库，为空时为 DefaultPGDatabase
	Database string
	// TLS 仅用于 MySQL / PostgreSQL
	TLS TLSConfig

	// PrivateKey、Passphrase 服务器文件的 SSH 私钥与口令，与 Password 二选一
	PrivateKey []byte
	Passphrase []byte
	// HostKey 服务器文件已确认的主机密钥指纹（SHA256:...），为空表示尚未确认
	HostKey string
}

// String 只输出类型与地址，避免秘密被 %v 打进日志
func (c Config) String() string {
	return fmt.Sprintf("%s %s", c.Type, c.addr())
}

// GoString 同 String，%#v 也不输出秘密
func (c Config) GoString() string { return "dsconn.Config{" + c.String() + "}" }

func (c Config) addr() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

// ErrInvalidConfig 连接参数不完整或取值无效
var ErrInvalidConfig = errors.New("数据源配置无效")

// Info 连接测试的结果
type Info struct {
	// Version 数据库的服务端版本（MySQL / PostgreSQL）
	Version string
	// System 服务器文件的 `uname -sm` 输出，如 "Linux x86_64"
	System string
	// TLS 连接已加密时非空
	TLS *TLSInfo
}

// TLSInfo 已加密连接的 TLS 状态
type TLSInfo struct {
	// Version 服务端报告的 TLS 版本，如 "TLSv1.3"
	Version string
	// Verified 是否校验了服务端证书（校验 CA / 校验 CA 与主机名）
	Verified bool
}

// Dialer 已建立的网络链路，*netchain.Tunnel 实现了它
type Dialer interface {
	// Dial 经链路连接 addr；链路中某一跳失败时返回 *netchain.HopError
	Dial(ctx context.Context, network, addr string) (net.Conn, error)
	// DialSSH 经链路登录最终的 SSH 主机，主机密钥未确认或已变化时在认证前返回 *netchain.HopError
	DialSSH(ctx context.Context, target netchain.Hop) (*ssh.Client, error)
}

var _ Dialer = (*netchain.Tunnel)(nil)

// Conn 已打开的数据源连接，供能力探测使用，用完后 Close（在关闭链路之前）
type Conn struct {
	Info Info
	// DB MySQL / PostgreSQL 的连接池，新连接同样经由链路
	DB *sql.DB
	// SSH 服务器文件的 SSH 客户端
	SSH *ssh.Client
}

// Close 关闭连接池或 SSH 客户端，不关闭链路
func (c *Conn) Close() error {
	var errs []error
	if c.DB != nil {
		errs = append(errs, c.DB.Close())
	}
	if c.SSH != nil {
		if err := c.SSH.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Connector 连接数据源，便于上层替换为 mock
type Connector interface {
	// Test 连接、认证并读取基本信息后断开
	Test(ctx context.Context, d Dialer, cfg Config) (Info, error)
	// Open 连接、认证并读取基本信息，返回保持打开的连接
	Open(ctx context.Context, d Dialer, cfg Config) (*Conn, error)
}

// Default 真实的 Connector
var Default Connector = connector{}

type connector struct{}

func (connector) Test(ctx context.Context, d Dialer, cfg Config) (Info, error) {
	return Test(ctx, d, cfg)
}

func (connector) Open(ctx context.Context, d Dialer, cfg Config) (*Conn, error) {
	return Open(ctx, d, cfg)
}

// Test 连接、认证并读取基本信息后断开，见 Open
func Test(ctx context.Context, d Dialer, cfg Config) (Info, error) {
	c, err := Open(ctx, d, cfg)
	if err != nil {
		return Info{}, err
	}
	_ = c.Close()
	return c.Info, nil
}

// Open 经 d 连接数据源、认证并读取基本信息。整体受 ctx 约束，ctx 没有截止时间时为 netchain.DefaultTimeout（30 秒）。
// 失败时：链路中某一跳失败返回 *netchain.HopError（服务器文件的目标主机按链路之后的一跳编号），
// 配置问题返回 ErrInvalidConfig 或 *FieldError，其余返回 *Error。
func Open(ctx context.Context, d Dialer, cfg Config) (*Conn, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, netchain.DefaultTimeout)
		defer cancel()
	}
	if cfg.Type == TypeServerFile {
		return openServerFile(ctx, d, cfg)
	}
	return openDB(ctx, d, cfg)
}

// Validate 检查连接参数，不联网。字段内容无法使用（CA、客户端证书与私钥）时返回 *FieldError，
// 其余问题返回包装了 ErrInvalidConfig 的错误。服务器文件的私钥在连接时由 netchain 解析。
func (c Config) Validate() error {
	switch {
	case c.Type != TypeMySQL && c.Type != TypePostgreSQL && c.Type != TypeServerFile:
		return fmt.Errorf("%w：未知的类型 %q", ErrInvalidConfig, c.Type)
	case c.Host == "":
		return fmt.Errorf("%w：缺少主机", ErrInvalidConfig)
	case c.Port < 1 || c.Port > 65535:
		return fmt.Errorf("%w：端口 %d 超出 1–65535", ErrInvalidConfig, c.Port)
	case c.User == "":
		return fmt.Errorf("%w：缺少用户名", ErrInvalidConfig)
	case c.Type == TypeServerFile:
		if (c.Password == "") == (len(c.PrivateKey) == 0) {
			return fmt.Errorf("%w：SSH 认证需要密码或私钥之一", ErrInvalidConfig)
		}
		return nil
	}
	_, err := c.tlsConfig()
	return err
}
