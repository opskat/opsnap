package testenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func envOf(file string, vars map[string]string) *Env {
	return newEnv(parse(strings.NewReader(file)), func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	})
}

func TestParse(t *testing.T) {
	got := parse(strings.NewReader("# 注释\n\nTEST_ENV_HOST=192.0.2.1\n  OPSNAP_TEST_PASSWORD = p=w#1 \nnot a line\nQUOTED=\"x y\"\n"))
	assert.Equal(t, map[string]string{
		"TEST_ENV_HOST":        "192.0.2.1",
		"OPSNAP_TEST_PASSWORD": "p=w#1",
		"QUOTED":               "x y",
	}, got)
}

func TestEnvironmentOverridesFile(t *testing.T) {
	e := envOf("TEST_ENV_HOST=192.0.2.1\nTEST_ENV_MYSQL_PORT=13306\n", map[string]string{"TEST_ENV_HOST": "192.0.2.9"})
	assert.Equal(t, "192.0.2.9", e.Get("TEST_ENV_HOST"))
	assert.Equal(t, "13306", e.Get("TEST_ENV_MYSQL_PORT"))
	assert.Empty(t, e.Get("MISSING"))
}

func TestServices(t *testing.T) {
	full := "TEST_ENV_HOST=192.0.2.1\nOPSNAP_TEST_PASSWORD=pw\nTEST_ENV_MYSQL_PORT=13306\nTEST_ENV_PG_PORT=15432\n"

	t.Run("配置齐全", func(t *testing.T) {
		e := envOf(full, nil)
		m, err := e.MySQL()
		require.NoError(t, err)
		assert.Equal(t, Service{Host: "192.0.2.1", Port: 13306, User: "root", Password: "pw"}, m)
		assert.Equal(t, "192.0.2.1:13306", m.Addr())
		p, err := e.Postgres()
		require.NoError(t, err)
		assert.Equal(t, Service{Host: "192.0.2.1", Port: 15432, User: "postgres", Password: "pw"}, p)
	})

	t.Run("缺少的变量写进原因", func(t *testing.T) {
		e := envOf("TEST_ENV_HOST=192.0.2.1\nTEST_ENV_PG_PORT=15432\n", nil)
		_, err := e.MySQL()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "OPSNAP_TEST_PASSWORD")
		assert.Contains(t, err.Error(), "TEST_ENV_MYSQL_PORT")
		assert.NotContains(t, err.Error(), "TEST_ENV_HOST")
	})

	t.Run("端口无效", func(t *testing.T) {
		e := envOf(full, map[string]string{"TEST_ENV_PG_PORT": "abc"})
		_, err := e.Postgres()
		assert.ErrorContains(t, err, "TEST_ENV_PG_PORT")
	})

	t.Run("环境变量置空视为未配置", func(t *testing.T) {
		e := envOf(full, map[string]string{"OPSNAP_TEST_PASSWORD": ""})
		_, err := e.MySQL()
		assert.ErrorContains(t, err, "OPSNAP_TEST_PASSWORD")
	})
}

func TestLoadFindsRepoEnvFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "e2e"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "e2e", ".env"), []byte("TEST_ENV_PG_PORT=15432\n"), 0o600))
	sub := filepath.Join(root, "internal", "pkg", "x")
	require.NoError(t, os.MkdirAll(sub, 0o750))

	e := loadFrom(sub, func(string) (string, bool) { return "", false })
	assert.Equal(t, "15432", e.Get("TEST_ENV_PG_PORT"))

	// 没有 .env 时只使用环境变量
	require.NoError(t, os.Remove(filepath.Join(root, "e2e", ".env")))
	e = loadFrom(sub, func(k string) (string, bool) { return "from-env", k == "TEST_ENV_HOST" })
	assert.Equal(t, "from-env", e.Get("TEST_ENV_HOST"))
	assert.Empty(t, e.Get("TEST_ENV_PG_PORT"))
}
