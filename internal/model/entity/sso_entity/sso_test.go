package sso_entity

import (
	"context"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func valid() *SSO {
	return &SSO{
		Issuer: "https://git.example.com/", ClientID: "cid", ButtonLabel: "Gitea",
		PanelURL: "https://opsnap.example.com",
	}
}

func TestSSO(t *testing.T) {
	ctx := context.Background()
	convey.Convey("单点登录配置", t, func() {
		convey.Convey("默认未配置、允许密码登录", func() {
			d := Default()
			assert.False(t, d.Configured())
			assert.True(t, d.PasswordLogin)
		})
		convey.Convey("回调地址由面板对外地址拼成，末尾的 / 不产生双斜杠", func() {
			s := valid()
			assert.Equal(t, "https://opsnap.example.com/api/v1/auth/oidc/callback", s.CallbackURL())
			s.PanelURL = "http://10.0.0.5:8080/"
			assert.Equal(t, "http://10.0.0.5:8080/api/v1/auth/oidc/callback", s.CallbackURL())
			s.PanelURL = ""
			assert.Equal(t, "", s.CallbackURL())
		})
		convey.Convey("合法配置通过，http 地址也允许", func() {
			s := valid()
			s.Issuer = "http://gitea.lan:3000/"
			assert.NoError(t, s.ValidateProvider(ctx))
		})
		convey.Convey("非法字段被拒绝", func() {
			cases := map[string]func(s *SSO){
				"Issuer 为空":      func(s *SSO) { s.Issuer = "" },
				"Issuer 不是 http": func(s *SSO) { s.Issuer = "ftp://git.example.com" },
				"Issuer 没有主机":    func(s *SSO) { s.Issuer = "https://" },
				"Client ID 为空":   func(s *SSO) { s.ClientID = "" },
				"按钮文字为空":         func(s *SSO) { s.ButtonLabel = "" },
				"按钮文字超过 32 个字符":  func(s *SSO) { s.ButtonLabel = strings.Repeat("字", 33) },
				"面板对外地址为空":       func(s *SSO) { s.PanelURL = "" },
				"面板对外地址是相对路径":    func(s *SSO) { s.PanelURL = "/opsnap" },
			}
			for name, mut := range cases {
				s := valid()
				mut(s)
				assert.Error(t, s.ValidateProvider(ctx), name)
			}
			s := valid()
			s.ButtonLabel = strings.Repeat("字", 32)
			assert.NoError(t, s.ValidateProvider(ctx))
		})
	})
}
