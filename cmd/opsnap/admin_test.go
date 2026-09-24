package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/opskat/opsnap/internal/api/auth"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/service/auth_svc"
)

func prompts(answers ...string) func(string) (string, error) {
	return func(string) (string, error) {
		if len(answers) == 0 {
			return "", errors.New("no more input")
		}
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
}

func setupAdmin(t *testing.T) {
	t.Helper()
	ctx := testdb.New(t)
	registerRepositories()
	code, err := auth_svc.Auth().PrepareSetupCode(ctx)
	require.NoError(t, err)
	_, _, err = auth_svc.Auth().Setup(ctx, &api.SetupRequest{SetupCode: code, Username: "admin", Password: "old-password-123"}, auth_svc.ClientMeta{IP: "127.0.0.1"})
	require.NoError(t, err)
}

func canLogin(t *testing.T, pw string) bool {
	t.Helper()
	_, _, err := auth_svc.Auth().Login(t.Context(), &api.LoginRequest{Username: "admin", Password: pw}, auth_svc.ClientMeta{IP: "203.0.113.50"})
	return err == nil
}

func TestResetPassword(t *testing.T) {
	t.Run("密码登录此前关闭时重新开启，并在输出中说明", func(t *testing.T) {
		setupAdmin(t)
		require.NoError(t, auth_svc.Auth().SetPasswordLoginEnabled(t.Context(), false))
		var out bytes.Buffer
		err := resetPassword(t.Context(), resetIO{stdin: strings.NewReader("stdin-password-123\n"), out: &out}, true)
		require.NoError(t, err)
		assert.Contains(t, out.String(), "密码登录此前处于关闭状态，已重新开启")
		assert.True(t, canLogin(t, "stdin-password-123"))
	})

	t.Run("交互终端：输入两次一致的新密码后重置", func(t *testing.T) {
		setupAdmin(t)
		var out bytes.Buffer
		err := resetPassword(t.Context(), resetIO{tty: true, prompt: prompts("new-password-456", "new-password-456"), out: &out}, false)
		require.NoError(t, err)
		assert.Contains(t, out.String(), "已重置管理员密码，所有登录会话已失效")
		assert.True(t, canLogin(t, "new-password-456"))
		assert.False(t, canLogin(t, "old-password-123"))
	})

	t.Run("交互终端：两次输入不一致时不修改", func(t *testing.T) {
		setupAdmin(t)
		err := resetPassword(t.Context(), resetIO{tty: true, prompt: prompts("new-password-456", "new-password-789"), out: &bytes.Buffer{}}, false)
		assert.ErrorContains(t, err, "两次输入的密码不一致")
		assert.True(t, canLogin(t, "old-password-123"))
	})

	t.Run("--password-stdin 从标准输入读取一行", func(t *testing.T) {
		setupAdmin(t)
		var out bytes.Buffer
		err := resetPassword(t.Context(), resetIO{stdin: strings.NewReader("stdin-password-123\n"), out: &out}, true)
		require.NoError(t, err)
		assert.True(t, canLogin(t, "stdin-password-123"))
	})

	t.Run("非交互环境且未指定 --password-stdin 时报错", func(t *testing.T) {
		setupAdmin(t)
		err := resetPassword(t.Context(), resetIO{stdin: strings.NewReader("x\n"), out: &bytes.Buffer{}}, false)
		assert.ErrorContains(t, err, "--password-stdin")
	})

	t.Run("新密码太短时报错", func(t *testing.T) {
		setupAdmin(t)
		err := resetPassword(t.Context(), resetIO{stdin: strings.NewReader("short\n"), out: &bytes.Buffer{}}, true)
		assert.ErrorContains(t, err, "12")
	})

	t.Run("尚未创建管理员时报错", func(t *testing.T) {
		testdb.New(t)
		registerRepositories()
		err := resetPassword(t.Context(), resetIO{stdin: strings.NewReader("stdin-password-123\n"), out: &bytes.Buffer{}}, true)
		assert.ErrorContains(t, err, "尚未创建管理员，请通过网页完成首次设置")
	})
}
