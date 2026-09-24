package secret_svc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
)

func TestSecretInit(t *testing.T) {
	setting_repo.RegisterSetting(setting_repo.NewSetting())

	convey.Convey("主密钥初始化", t, func() {
		ctx := testdb.New(t)
		dir := t.TempDir()
		keyPath := filepath.Join(dir, secret.KeyFileName)

		convey.Convey("没有环境变量也没有密钥文件时，生成 0600 的 master.key 并可加解密", func() {
			svc := newSecret()
			res, err := svc.Init(ctx, InitOptions{DataDir: dir})
			require.NoError(t, err)
			assert.True(t, res.Generated)
			assert.Equal(t, SourceFile, res.Source)
			assert.Equal(t, keyPath, res.Path)
			info, err := os.Stat(keyPath)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

			ct, err := svc.Encrypt(ctx, "s3cret")
			require.NoError(t, err)
			pt, err := svc.Decrypt(ctx, ct)
			require.NoError(t, err)
			assert.Equal(t, "s3cret", pt)

			convey.Convey("再次启动读取同一个文件，之前的密文仍能解密", func() {
				again := newSecret()
				res, err := again.Init(ctx, InitOptions{DataDir: dir})
				require.NoError(t, err)
				assert.False(t, res.Generated)
				pt, err := again.Decrypt(ctx, ct)
				require.NoError(t, err)
				assert.Equal(t, "s3cret", pt)
			})

			convey.Convey("数据库已有加密数据而密钥文件丢失时，拒绝启动且不生成新密钥", func() {
				require.NoError(t, os.Remove(keyPath))
				_, err := newSecret().Init(ctx, InitOptions{DataDir: dir})
				assert.ErrorIs(t, err, ErrKeyMissing)
				assert.Contains(t, err.Error(), keyPath)
				_, statErr := os.Stat(keyPath)
				assert.ErrorIs(t, statErr, os.ErrNotExist)
			})

			convey.Convey("密钥文件被换成另一把密钥时，拒绝启动", func() {
				require.NoError(t, os.Remove(keyPath))
				other, _ := secret.GenerateKey()
				require.NoError(t, secret.WriteKeyFile(keyPath, other))
				_, err := newSecret().Init(ctx, InitOptions{DataDir: dir})
				assert.ErrorIs(t, err, ErrKeyMismatch)
				assert.Contains(t, err.Error(), keyPath)
			})

			convey.Convey("环境变量提供了不同的密钥时，拒绝启动并指明环境变量", func() {
				other, _ := secret.GenerateKey()
				_, err := newSecret().Init(ctx, InitOptions{DataDir: dir, EnvKey: secret.EncodeKey(other)})
				assert.ErrorIs(t, err, ErrKeyMismatch)
				assert.Contains(t, err.Error(), secret.EnvKey)
			})
		})

		convey.Convey("设置了环境变量时使用它，不读写 master.key", func() {
			key, _ := secret.GenerateKey()
			res, err := newSecret().Init(ctx, InitOptions{DataDir: dir, EnvKey: secret.EncodeKey(key)})
			require.NoError(t, err)
			assert.Equal(t, SourceEnv, res.Source)
			assert.False(t, res.Generated)
			_, statErr := os.Stat(keyPath)
			assert.ErrorIs(t, statErr, os.ErrNotExist)
		})

		convey.Convey("环境变量格式不对时拒绝启动", func() {
			_, err := newSecret().Init(ctx, InitOptions{DataDir: dir, EnvKey: "not-base64"})
			assert.ErrorIs(t, err, secret.ErrInvalidKey)
			assert.Contains(t, err.Error(), secret.EnvKey)
		})

		convey.Convey("未初始化时加解密返回错误", func() {
			_, err := newSecret().Encrypt(ctx, "x")
			assert.ErrorIs(t, err, ErrNotInitialized)
		})
	})
}
