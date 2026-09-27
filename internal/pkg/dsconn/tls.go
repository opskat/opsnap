package dsconn

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

var (
	errCANotPEM       = errors.New("无法解析 CA 证书（需要 PEM 格式）")
	errCertNotPEM     = errors.New("无法解析客户端证书（需要 PEM 格式）")
	errKeyInvalid     = errors.New("无法解析客户端私钥（需要 PEM 格式）")
	errKeyMismatch    = errors.New("客户端私钥与证书不匹配")
	errCertMissing    = errors.New("提供了客户端私钥，还需要客户端证书")
	errKeyMissing     = errors.New("提供了客户端证书，还需要客户端私钥")
	errNoPeerCert     = errors.New("服务端没有出示证书")
	errInvalidTLSMode = fmt.Errorf("%w：未知的 TLS 模式", ErrInvalidConfig)
)

func (m TLSMode) valid() bool {
	switch m {
	case "", TLSDisable, TLSPrefer, TLSRequire, TLSVerifyCA, TLSVerifyFull:
		return true
	}
	return false
}

func (m TLSMode) verifies() bool { return m == TLSVerifyCA || m == TLSVerifyFull }

// material 解析 CA 与客户端证书；没有提供的返回零值
func (t TLSConfig) material() (*x509.CertPool, []tls.Certificate, error) {
	var roots *x509.CertPool
	if len(t.CA) > 0 {
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(t.CA) {
			return nil, nil, &FieldError{Field: "ca", Err: errCANotPEM}
		}
	}
	switch {
	case len(t.ClientCert) == 0 && len(t.ClientKey) == 0:
		return roots, nil, nil
	case len(t.ClientCert) == 0:
		return nil, nil, &FieldError{Field: "client_cert", Err: errCertMissing}
	case len(t.ClientKey) == 0:
		return nil, nil, &FieldError{Field: "client_key", Err: errKeyMissing}
	}
	if b, _ := pem.Decode(t.ClientCert); b == nil || b.Type != "CERTIFICATE" {
		return nil, nil, &FieldError{Field: "client_cert", Err: errCertNotPEM}
	} else if _, err := x509.ParseCertificate(b.Bytes); err != nil {
		return nil, nil, &FieldError{Field: "client_cert", Err: fmt.Errorf("%w: %w", errCertNotPEM, err)}
	}
	if b, _ := pem.Decode(t.ClientKey); b == nil || !parsesAsKey(b.Bytes) {
		return nil, nil, &FieldError{Field: "client_key", Err: errKeyInvalid}
	}
	pair, err := tls.X509KeyPair(t.ClientCert, t.ClientKey)
	if err != nil {
		// 证书与私钥各自都能解析，失败只能是两者不匹配
		return nil, nil, &FieldError{Field: "client_key", Err: fmt.Errorf("%w: %w", errKeyMismatch, err)}
	}
	return roots, []tls.Certificate{pair}, nil
}

// parsesAsKey der 是否为 crypto/tls 支持的私钥格式（PKCS#1、PKCS#8、SEC 1）
func parsesAsKey(der []byte) bool {
	if _, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return true
	}
	if _, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		return true
	}
	_, err := x509.ParseECPrivateKey(der)
	return err == nil
}

// tlsConfig 按 TLS 设置构造客户端配置；TLSDisable 返回 nil
func (c Config) tlsConfig() (*tls.Config, error) {
	if !c.TLS.Mode.valid() {
		return nil, errInvalidTLSMode
	}
	roots, certs, err := c.TLS.material()
	if err != nil {
		return nil, err
	}
	switch c.TLS.Mode {
	case TLSDisable:
		return nil, nil
	case TLSVerifyFull:
		// RootCAs 为 nil 时使用系统信任库
		return &tls.Config{ServerName: c.Host, RootCAs: roots, Certificates: certs, MinVersion: tls.VersionTLS12}, nil
	case TLSVerifyCA:
		return &tls.Config{
			ServerName: c.Host,
			//nolint:gosec // 校验 CA 模式不校验主机名，证书链由 VerifyConnection 按 CA 或系统信任库校验
			InsecureSkipVerify: true,
			VerifyConnection:   verifyChain(roots),
			Certificates:       certs,
			MinVersion:         tls.VersionTLS12,
		}, nil
	}
	// 优先加密与必须加密：只加密，按用户选择不校验证书
	//nolint:gosec // 用户选择的 TLS 模式不校验证书
	return &tls.Config{ServerName: c.Host, InsecureSkipVerify: true, Certificates: certs, MinVersion: tls.VersionTLS12}, nil
}

// verifyChain 按 roots（nil 为系统信任库）校验证书链，不校验主机名
func verifyChain(roots *x509.CertPool) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errNoPeerCert
		}
		inter := x509.NewCertPool()
		for _, c := range cs.PeerCertificates[1:] {
			inter.AddCert(c)
		}
		_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter})
		if err != nil {
			return &tls.CertificateVerificationError{UnverifiedCertificates: cs.PeerCertificates, Err: err}
		}
		return nil
	}
}
