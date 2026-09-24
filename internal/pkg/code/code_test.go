package code

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 两种语言的错误码集合必须一致，且文案非空
func TestLanguagesInSync(t *testing.T) {
	for k, v := range zhCN {
		assert.NotEmpty(t, v, "zh-cn %d", k)
		assert.NotEmpty(t, en[k], "en 缺少错误码 %d", k)
	}
	for k := range en {
		_, ok := zhCN[k]
		assert.True(t, ok, "zh-cn 缺少错误码 %d", k)
	}
}

// 前端 frontend/src/lib/auth.ts 的 ErrorCode 手写了一份错误码，这里检查每一项都与后端一致
func TestFrontendErrorCodesInSync(t *testing.T) {
	backend := map[string]int{
		"Unauthorized":             Unauthorized,
		"AlreadyInitialized":       AlreadyInitialized,
		"SetupCodeInvalid":         SetupCodeInvalid,
		"UsernameInvalid":          UsernameInvalid,
		"PasswordTooShort":         PasswordTooShort,
		"LoginFailed":              LoginFailed,
		"TooManyAttempts":          TooManyAttempts,
		"CurrentPasswordWrong":     CurrentPasswordWrong,
		"SessionRequired":          SessionRequired,
		"TokenNameInvalid":         TokenNameInvalid,
		"TokenNameDuplicate":       TokenNameDuplicate,
		"OIDCResetConfirmRequired": OIDCResetConfirmRequired,
		"StorageNameInvalid":       StorageNameInvalid,
		"StorageNameDuplicate":     StorageNameDuplicate,
		"StoragePathRelative":      StoragePathRelative,
		"StorageEndpointScheme":    StorageEndpointScheme,
		"StorageKeyInvalid":        StorageKeyInvalid,
	}
	src, err := os.ReadFile("../../../frontend/src/lib/auth.ts")
	require.NoError(t, err)
	block := regexp.MustCompile(`(?s)export const ErrorCode = \{(.*?)\}`).FindSubmatch(src)
	require.NotNil(t, block, "未找到 ErrorCode 定义")
	entries := regexp.MustCompile(`(\w+):\s*(\d+)`).FindAllSubmatch(block[1], -1)
	require.NotEmpty(t, entries)
	for _, e := range entries {
		name := string(e[1])
		want, ok := backend[name]
		if assert.True(t, ok, "前端错误码 %s 在后端对照表中不存在，请补充到本测试", name) {
			assert.Equal(t, strconv.Itoa(want), string(e[2]), "错误码 %s 前后端不一致", name)
		}
	}
}
