package code

import (
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goCodes 用类型检查读出 code.go 里每个错误码常量的值（按 iota 展开）
func goCodes(t *testing.T) map[string]int64 {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "code.go", nil, 0)
	require.NoError(t, err)
	pkg, err := (&types.Config{}).Check("code", fset, []*ast.File{f}, nil)
	require.NoError(t, err)
	out := map[string]int64{}
	for _, name := range pkg.Scope().Names() {
		if c, ok := pkg.Scope().Lookup(name).(*types.Const); ok {
			v, exact := constant.Int64Val(c.Val())
			require.True(t, exact, name)
			out[name] = v
		}
	}
	return out
}

// 前端 frontend/src/lib/api.ts 的 ErrorCode 是这里的镜像：同名、同值，改了一边要改另一边
func TestFrontendErrorCodesMatch(t *testing.T) {
	codes := goCodes(t)
	src, err := os.ReadFile("../../../frontend/src/lib/api.ts")
	require.NoError(t, err)
	block := regexp.MustCompile(`(?s)export const ErrorCode = \{(.*?)\} as const`).FindSubmatch(src)
	require.NotNil(t, block, "api.ts 里找不到 ErrorCode")
	entries := regexp.MustCompile(`(\w+):\s*(\d+)`).FindAllSubmatch(block[1], -1)
	require.NotEmpty(t, entries)
	for _, e := range entries {
		name := string(e[1])
		value, err := strconv.ParseInt(string(e[2]), 10, 64)
		require.NoError(t, err)
		want, ok := codes[name]
		if assert.True(t, ok, "后端没有错误码 %s", name) {
			assert.Equal(t, want, value, name)
		}
	}
}

func TestEveryCodeHasMessage(t *testing.T) {
	for name, v := range goCodes(t) {
		_, ok := zhCN[int(v)]
		assert.True(t, ok, "错误码 %s 缺少中文文案", name)
	}
}
