package credential_entity

import (
	"context"
	"strings"
	"testing"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/code"
)

// codeOf 取出业务错误码
func codeOf(t *testing.T, err error) int {
	t.Helper()
	var he *httputils.Error
	require.ErrorAs(t, err, &he)
	return he.Code
}

func TestCheck(t *testing.T) {
	ctx := context.Background()
	var missing *Credential
	assert.Equal(t, code.CredentialNotFound, codeOf(t, missing.Check(ctx)))
	assert.NoError(t, (&Credential{}).Check(ctx))
}

func TestValidate(t *testing.T) {
	ctx := context.Background()
	valid := func() *Credential {
		return &Credential{Name: "生产 root", Kind: KindPassword, Password: "SECRET-pw", Shared: true}
	}
	require.NoError(t, valid().Validate(ctx))

	cases := map[string]struct {
		mut  func(c *Credential)
		want int
	}{
		"缺少名称":    {func(c *Credential) { c.Name = "  " }, code.CredentialFieldInvalid},
		"名称超长":    {func(c *Credential) { c.Name = strings.Repeat("名", 65) }, code.CredentialFieldInvalid},
		"类型不合法":   {func(c *Credential) { c.Kind = "token" }, code.CredentialKindInvalid},
		"密码类型缺密码": {func(c *Credential) { c.Password = "" }, code.CredentialEmpty},
		"私钥类型缺私钥": {func(c *Credential) { c.Kind, c.Password = KindSSHKey, "" }, code.CredentialEmpty},
		"用户超长":    {func(c *Credential) { c.Username = strings.Repeat("u", 65) }, code.CredentialFieldInvalid},
		"备注超长":    {func(c *Credential) { c.Note = strings.Repeat("注", 201) }, code.CredentialFieldInvalid},
	}
	for name, c := range cases {
		cred := valid()
		c.mut(cred)
		err := cred.Validate(ctx)
		require.Error(t, err, name)
		assert.Equal(t, c.want, codeOf(t, err), name)
		assert.NotContains(t, err.Error(), "SECRET-pw", "错误文案不带凭据内容")
	}

	t.Run("私有凭据可以没有名称", func(t *testing.T) {
		cred := &Credential{Kind: KindPassword, Password: "SECRET-pw"}
		assert.NoError(t, cred.Validate(ctx))
	})
}

func TestIsShared(t *testing.T) {
	assert.True(t, (&Credential{Shared: true, Name: "a"}).IsShared())
	assert.False(t, (&Credential{Shared: false, Name: "a"}).IsShared())
	// 名称只在共享时有意义；只要不共享就不是共享凭据
	assert.False(t, (&Credential{}).IsShared())
}
