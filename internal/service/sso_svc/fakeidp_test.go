package sso_svc

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/stretchr/testify/require"
)

// fakeIdP 进程内的假 OIDC 提供方：发现文档、JWKS 由 oidctest 提供，令牌端点在这里实现，
// 自己签发 id_token。授权页不需要浏览器：authorize 直接解析 opsnap 生成的授权地址并发放授权码。
type fakeIdP struct {
	t      *testing.T
	srv    *httptest.Server
	issuer string
	key    *rsa.PrivateKey
	now    func() time.Time

	clientID, clientSecret string

	mu    sync.Mutex
	codes map[string]grant

	// 下次签发的身份，以及对 claims 的修改
	sub, name string
	mutate    func(claims map[string]any)
	// signKey 不为空时用它签名（模拟签名错误）
	signKey *rsa.PrivateKey
	// tokenError 不为空时令牌端点返回这个 OAuth2 错误码
	tokenError string
	// tokenStatus 不为 0 时令牌端点返回这个 HTTP 状态码和一段 HTML（模拟提供方响应异常）
	tokenStatus int
	// jwksStatus 不为 0 时 JWKS 端点返回这个 HTTP 状态码（模拟读不到签名公钥）
	jwksStatus int
}

type grant struct {
	challenge, nonce, redirect string
}

func newFakeIdP(t *testing.T, now func() time.Time) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	f := &fakeIdP{
		t: t, key: key, now: now, clientID: "5f0c1e2a", clientSecret: "s3cret-client",
		codes: map[string]grant{}, sub: "3", name: "alice-ops",
	}
	disc := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{PublicKey: key.Public(), KeyID: "k1", Algorithm: "RS256"}}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			f.token(w, r)
			return
		}
		if r.URL.Path == "/keys" && f.jwksStatus != 0 {
			w.WriteHeader(f.jwksStatus)
			_, _ = w.Write([]byte("<html>provider says <b>down</b></html>"))
			return
		}
		disc.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	// Gitea 的 issuer 是 ROOT_URL，带结尾的 /
	f.issuer = f.srv.URL + "/"
	disc.SetIssuer(f.issuer)
	return f
}

// authorize 模拟用户在提供方同意授权：检查授权地址的参数，返回回调要带的 code 和 state
func (f *fakeIdP) authorize(authURL string) (code, state string) {
	f.t.Helper()
	u, err := url.Parse(authURL)
	require.NoError(f.t, err)
	q := u.Query()
	require.Equal(f.t, f.clientID, q.Get("client_id"))
	require.Equal(f.t, "code", q.Get("response_type"))
	require.Equal(f.t, "S256", q.Get("code_challenge_method"))
	require.Equal(f.t, "openid profile", q.Get("scope"))
	require.NotEmpty(f.t, q.Get("state"))
	require.NotEmpty(f.t, q.Get("nonce"))
	code = base64.RawURLEncoding.EncodeToString([]byte(q.Get("state")))[:16]
	f.mu.Lock()
	f.codes[code] = grant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirect: q.Get("redirect_uri")}
	f.mu.Unlock()
	return code, q.Get("state")
}

func (f *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	oauthErr := func(code string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
	}
	if err := r.ParseForm(); err != nil {
		oauthErr("invalid_request")
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != f.clientID || secret != f.clientSecret {
		oauthErr("invalid_client")
		return
	}
	if f.tokenStatus != 0 {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(f.tokenStatus)
		_, _ = w.Write([]byte("<html>provider says <b>oops</b></html>"))
		return
	}
	if f.tokenError != "" {
		oauthErr(f.tokenError)
		return
	}
	f.mu.Lock()
	g, found := f.codes[r.PostForm.Get("code")]
	delete(f.codes, r.PostForm.Get("code"))
	f.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !found || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge || r.PostForm.Get("redirect_uri") != g.redirect {
		oauthErr("invalid_grant")
		return
	}
	now := f.now()
	claims := map[string]any{
		"iss": f.issuer, "sub": f.sub, "aud": f.clientID, "nonce": g.nonce,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "preferred_username": f.name,
	}
	if f.mutate != nil {
		f.mutate(claims)
	}
	raw, err := json.Marshal(claims)
	require.NoError(f.t, err)
	key := f.key
	if f.signKey != nil {
		key = f.signKey
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "at-" + r.PostForm.Get("code"), "token_type": "Bearer", "expires_in": 3600,
		"id_token": oidctest.SignIDToken(key, "k1", "RS256", string(raw)),
	})
}
