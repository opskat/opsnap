package oidc_ctr

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/stretchr/testify/assert"

	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/service/oidc_svc"
)

func TestBeginErrorKind(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		err  error
		want string
	}{
		{i18n.NewError(ctx, code.OIDCIssuerUnreachable, "https://sso.example.com", "timeout"), oidc_svc.ErrUnreachable},
		{i18n.NewError(ctx, code.OIDCDiscoveryInvalid, "bad"), oidc_svc.ErrUnreachable},
		{i18n.NewError(ctx, code.OIDCAlreadyBound), oidc_svc.ErrAlreadyBound},
		{i18n.NewError(ctx, code.OIDCNotConfigured), oidc_svc.ErrInvalid},
		{errors.New("其他错误"), oidc_svc.ErrInvalid},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, beginErrorKind(c.err), "%v", c.err)
	}
}
