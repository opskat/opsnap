package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opsnap/internal/model/entity/run_entity"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/migrations"
)

// envWith 返回只包含给定变量的 getenv 函数，避免读到本机真实环境
func envWith(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

// freeAddr 取一个本机空闲端口
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// writeConfig 在临时目录写一份指向临时数据目录的配置文件
func writeConfig(t *testing.T, dataDir, addr string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := fmt.Sprintf(`source: file
env: test
debug: false
dataDir: %q
db:
  driver: sqlite
  dsn: %q
http:
  address:
    - %q
logger:
  level: error
  disableConsole: true
  logFile:
    enable: false
`, dataDir, filepath.Join(dataDir, "opsnap.db"), addr)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestRunWithoutSecretKey(t *testing.T) {
	convey.Convey("缺少主密钥时拒绝启动", t, func() {
		dataDir := filepath.Join(t.TempDir(), "data")
		cfgFile := writeConfig(t, dataDir, freeAddr(t))

		err := run(context.Background(), envWith(nil), cfgFile)
		require.Error(t, err)
		assert.ErrorIs(t, err, secret.ErrMissingKey)
		assert.Contains(t, err.Error(), "OPSNAP_SECRET_KEY")

		convey.Convey("不会创建数据目录和数据库", func() {
			_, statErr := os.Stat(dataDir)
			assert.True(t, os.IsNotExist(statErr))
		})
	})
}

func TestRunWithSecretKey(t *testing.T) {
	// 服务只启动一次：cago 的组件注册有全局状态，不能放进会被 convey 重复执行的块里
	dataDir := filepath.Join(t.TempDir(), "data")
	addr := freeAddr(t)
	cfgFile := writeConfig(t, dataDir, addr)
	staleRunID := seedStaleRun(t, dataDir)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, envWith(map[string]string{secret.EnvKey: "test-master-key"}), cfgFile)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("服务未在超时内退出")
		}
	})

	base := "http://" + addr
	require.Eventually(t, func() bool {
		resp, err := http.Get(base + "/health")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 50*time.Millisecond)

	convey.Convey("有主密钥时完成迁移并提供 HTTP 服务", t, func() {
		convey.Convey("默认加解密器已就绪", func() {
			ct, err := secret.Default().Encrypt("x")
			require.NoError(t, err)
			plain, err := secret.Default().Decrypt(ct)
			require.NoError(t, err)
			assert.Equal(t, "x", plain)
		})

		convey.Convey("SQLite 已迁移", func() {
			db, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "opsnap.db")), &gorm.Config{})
			require.NoError(t, err)
			defer func() {
				if sqlDB, err := db.DB(); err == nil {
					_ = sqlDB.Close()
				}
			}()
			assert.True(t, db.Migrator().HasTable("meta"))
		})

		convey.Convey("上次进程留下的运行中记录在启动时改为失败", func() {
			db, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "opsnap.db")), &gorm.Config{})
			require.NoError(t, err)
			defer func() {
				if sqlDB, err := db.DB(); err == nil {
					_ = sqlDB.Close()
				}
			}()
			var got run_entity.Run
			require.NoError(t, db.Where("id = ?", staleRunID).First(&got).Error)
			assert.Equal(t, run_entity.StatusFailed, got.Status)
			assert.Contains(t, got.Reason, "中断")
		})

		convey.Convey("GET / 和 GET /api/v1/openapi.json 都返回 200", func() {
			for _, path := range []string{"/", "/api/v1/openapi.json"} {
				resp, err := http.Get(base + path)
				require.NoError(t, err)
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				assert.Equal(t, http.StatusOK, resp.StatusCode, path)
			}
		})
	})
}

// seedStaleRun 在启动前建好数据库，并留下一条「运行中」的记录，模拟上次进程在运行中退出
func seedStaleRun(t *testing.T, dataDir string) int64 {
	t.Helper()
	require.NoError(t, os.MkdirAll(dataDir, 0o700))
	db, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "opsnap.db")), &gorm.Config{})
	require.NoError(t, err)
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()
	require.NoError(t, migrations.RunMigrations(db))
	r := run_entity.New(1, "job", run_entity.TriggerSchedule, time.Now())
	require.NoError(t, db.Create(r).Error)
	return r.ID
}

func TestRshSubcommand(t *testing.T) {
	t.Run("opsnap rsh 交给桥接子命令；不是由运行引擎启动时拒绝", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code, ok := subcommand([]string{"opsnap", "rsh", "opsnap", "cat"}, envWith(nil), strings.NewReader(""), &out, &errOut)
		assert.True(t, ok)
		assert.Equal(t, 255, code)
		assert.Contains(t, errOut.String(), "opsnap rsh")
	})
	t.Run("其他参数照常启动服务", func(t *testing.T) {
		_, ok := subcommand([]string{"opsnap", "-config", "x.yaml"}, envWith(nil), nil, io.Discard, io.Discard)
		assert.False(t, ok)
		_, ok = subcommand([]string{"opsnap"}, envWith(nil), nil, io.Discard, io.Discard)
		assert.False(t, ok)
	})
}
