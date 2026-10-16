package sso_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/pkg/code"
)

// discoveryPath OIDC 发现文档相对 issuer 的路径
const discoveryPath = "/.well-known/openid-configuration"

// maxDiscoveryBytes 发现文档最多读取的字节数
const maxDiscoveryBytes = 1 << 20

// discovery 发现文档里用到的字段
type discovery struct {
	Issuer   string   `json:"issuer"`
	AuthURL  string   `json:"authorization_endpoint"`
	TokenURL string   `json:"token_endpoint"`
	JWKSURL  string   `json:"jwks_uri"`
	Algs     []string `json:"id_token_signing_alg_values_supported"`
}

// discoveryURL 按 OIDC 规范拼出发现文档地址：issuer 去掉结尾的 / 再加固定路径
func discoveryURL(issuer string) string {
	return strings.TrimRight(issuer, "/") + discoveryPath
}

// discoveryError 读取发现文档失败的原因；Declared 不为空表示文档声明的 issuer 与填写的不一致
type discoveryError struct {
	URL      string
	Reason   string
	Declared string
}

func (e *discoveryError) Error() string {
	if e.Declared != "" {
		return "提供方声明的 issuer 是 " + e.Declared
	}
	return e.Reason
}

// fetchDiscovery 读取并检查发现文档
func (s *ssoSvc) fetchDiscovery(ctx context.Context, issuer string) (*discovery, *discoveryError) {
	addr := discoveryURL(issuer)
	fail := func(reason string) *discoveryError { return &discoveryError{URL: addr, Reason: reason} }

	reqCtx, cancel := context.WithTimeout(ctx, s.discoveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, addr, nil)
	if err != nil {
		return nil, fail(err.Error())
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			return nil, fail(fmt.Sprintf("超时（%d 秒内没有响应）", int(s.discoveryTimeout.Seconds())))
		}
		return nil, fail(transportReason(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fail(fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDiscoveryBytes))
	if err != nil {
		return nil, fail(transportReason(err))
	}
	d := &discovery{}
	if err := json.Unmarshal(body, d); err != nil {
		return nil, fail("返回的内容不是合法的 OIDC 发现文档")
	}
	for _, f := range []struct{ name, val string }{
		{"issuer", d.Issuer},
		{"authorization_endpoint", d.AuthURL},
		{"token_endpoint", d.TokenURL},
		{"jwks_uri", d.JWKSURL},
	} {
		if f.val == "" {
			return nil, fail("返回的内容不是合法的 OIDC 发现文档，缺少 " + f.name)
		}
	}
	if d.Issuer != issuer {
		return nil, &discoveryError{URL: addr, Declared: d.Issuer}
	}
	return d, nil
}

// checkDiscovery 保存配置前的检查：失败时返回写明地址和原因的错误
func (s *ssoSvc) checkDiscovery(ctx context.Context, issuer string) error {
	_, de := s.fetchDiscovery(ctx, issuer)
	if de == nil {
		return nil
	}
	if de.Declared != "" {
		return i18n.NewError(ctx, code.SSOIssuerMismatch, de.URL, de.Declared)
	}
	return i18n.NewError(ctx, code.SSODiscoveryFailed, de.URL, de.Reason)
}

// transportReason 去掉 url.Error 里重复的方法和地址，只留底层原因（例如 connection refused）
func transportReason(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}
