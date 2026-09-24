// Package fakeidp 是测试用的最小 OIDC 提供方：discovery、授权（自动同意）、令牌（校验 PKCE）、JWKS。
// 通过 Behavior 注入各种异常（用户取消、audience/nonce/签名错误、令牌已过期等），供 Go 测试与冒烟 e2e 使用。
package fakeidp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Behavior 下一次授权时的表现；零值为正常流程
type Behavior struct {
	// Subject 与 Email 为登录的身份
	Subject string
	Email   string
	// Error 非空时授权端点直接带着该错误回跳，例如 access_denied
	Error            string
	ErrorDescription string
	// WrongAudience、WrongNonce、Expired、WrongSignature 让签发的 ID Token 校验失败
	WrongAudience bool
	WrongNonce    bool
	Expired       bool
	// WrongSignature 用不在 JWKS 中的密钥签名
	WrongSignature bool
}

type grant struct {
	behavior  Behavior
	nonce     string
	challenge string
	clientID  string
}

// Server 假 IdP；Issuer 是它的根地址
type Server struct {
	Issuer       string
	ClientID     string
	ClientSecret string

	key *rsa.PrivateKey
	mu  sync.Mutex
	// next 下一次授权的表现，用后恢复为 Default
	next    *Behavior
	Default Behavior
	codes   map[string]grant
}

// New 创建假 IdP；issuer 为对外地址（与监听地址一致），调用方负责用 Handler 提供服务
func New(issuer, clientID, clientSecret string) (*Server, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return &Server{
		Issuer:       issuer,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		key:          key,
		Default:      Behavior{Subject: "user-1", Email: "ops@example.com"},
		codes:        map[string]grant{},
	}, nil
}

// SetNext 设置下一次授权的表现
func (s *Server) SetNext(b Behavior) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next = &b
}

func (s *Server) takeBehavior() Behavior {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next != nil {
		b := *s.next
		s.next = nil
		return b
	}
	return s.Default
}

func randomString() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", s.discovery)
	mux.HandleFunc("GET /authorize", s.authorize)
	mux.HandleFunc("POST /token", s.token)
	mux.HandleFunc("GET /jwks", s.jwks)
	// 控制接口：e2e 通过它设置下一次授权的表现
	mux.HandleFunc("POST /control/next", func(w http.ResponseWriter, r *http.Request) {
		var b Behavior
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.SetNext(b)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                s.Issuer,
		"authorization_endpoint":                s.Issuer + "/authorize",
		"token_endpoint":                        s.Issuer + "/token",
		"jwks_uri":                              s.Issuer + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.String() == "" {
		http.Error(w, "missing redirect_uri", http.StatusBadRequest)
		return
	}
	b := s.takeBehavior()
	back := redirect.Query()
	back.Set("state", q.Get("state"))
	if b.Error != "" {
		back.Set("error", b.Error)
		if b.ErrorDescription != "" {
			back.Set("error_description", b.ErrorDescription)
		}
	} else {
		c := randomString()
		s.mu.Lock()
		s.codes[c] = grant{behavior: b, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), clientID: q.Get("client_id")}
		s.mu.Unlock()
		back.Set("code", c)
	}
	redirect.RawQuery = back.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound) //nolint:gosec // 测试用假 IdP，按请求中的 redirect_uri 回跳是 OIDC 授权端点的本职
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != s.ClientID || secret != s.ClientSecret {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]string{"error": "invalid_client"})
		return
	}
	s.mu.Lock()
	g, found := s.codes[r.PostForm.Get("code")]
	delete(s.codes, r.PostForm.Get("code"))
	s.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !found || g.challenge == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}

	now := time.Now()
	claims := map[string]any{
		"iss":   s.Issuer,
		"sub":   g.behavior.Subject,
		"aud":   s.ClientID,
		"iat":   now.Unix(),
		"exp":   now.Add(5 * time.Minute).Unix(),
		"nonce": g.nonce,
	}
	if g.behavior.Email != "" {
		claims["email"] = g.behavior.Email
	}
	if g.behavior.WrongAudience {
		claims["aud"] = "someone-else"
	}
	if g.behavior.WrongNonce {
		claims["nonce"] = "forged-nonce"
	}
	if g.behavior.Expired {
		claims["iat"] = now.Add(-2 * time.Hour).Unix()
		claims["exp"] = now.Add(-time.Hour).Unix()
	}
	key := s.key
	if g.behavior.WrongSignature {
		other, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		key = other
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	idToken, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"access_token": randomString(),
		"token_type":   "Bearer",
		"expires_in":   300,
		"id_token":     idToken,
	})
}

func (s *Server) jwks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &s.key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"}}})
}
