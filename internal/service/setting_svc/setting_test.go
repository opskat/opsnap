package setting_svc

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/glebarez/sqlite"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	settingapi "github.com/opskat/opsnap/internal/api/setting"
	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/masterkey_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/migrations"
)

func setupSettingTest(t *testing.T) context.Context {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))
	setting_repo.RegisterSetting(setting_repo.NewSetting())
	masterkey_repo.RegisterMasterKey(masterkey_repo.NewMasterKey())
	admin_repo.RegisterAdmin(admin_repo.NewAdmin())
	ctx := db.WithContextDB(context.Background(), gdb)
	require.NoError(t, admin_repo.Admin().Create(ctx, &admin_entity.Admin{Username: "root", PasswordHash: "h"}))
	return ctx
}

func TestSettings(t *testing.T) {
	convey.Convey("设置", t, func() {
		ctx := setupSettingTest(t)
		svc := newSetting(func() time.Time { return time.Unix(1_800_000_000, 0) })
		convey.Convey("未保存过时返回默认值，并带管理员账号和主密钥状态", func() {
			s, err := svc.View(ctx)
			require.NoError(t, err)
			assert.Equal(t, "Asia/Shanghai", s.Timezone)
			assert.Equal(t, 2, s.MaxConcurrentRuns)
			assert.Equal(t, 90, s.RetentionDays)
			assert.False(t, s.ClearMirrorAfterBackup)
			assert.Equal(t, "root", s.AdminUsername)
			assert.Equal(t, "AES-256-GCM", s.MasterKey.Algorithm)
			assert.Equal(t, "/data/mirror", s.MirrorDir, "标出存放明文的文件镜像目录")
		})

		convey.Convey("主密钥已加载时状态为已配置", func() {
			sec, err := secret.New("0123456789abcdef0123456789abcdef")
			require.NoError(t, err)
			old := secret.Default()
			secret.SetDefault(sec)
			t.Cleanup(func() { secret.SetDefault(old) })
			s, err := svc.View(ctx)
			require.NoError(t, err)
			assert.True(t, s.MasterKey.Configured)
		})

		convey.Convey("保存后读取一致，Get 供其他任务使用", func() {
			_, err := svc.Update(ctx, &settingapi.UpdateRequest{
				MaxConcurrentRuns: 4, RetentionDays: 30, Timezone: "UTC", ClearMirrorAfterBackup: true,
			})
			require.NoError(t, err)
			got, err := svc.Get(ctx)
			require.NoError(t, err)
			assert.Equal(t, 4, got.MaxConcurrentRuns)
			assert.Equal(t, 30, got.RetentionDays)
			assert.Equal(t, "UTC", got.Timezone)
			assert.True(t, got.ClearMirrorAfterBackup)
		})

		convey.Convey("非法取值被拒绝且不改动已保存的设置", func() {
			_, err := svc.Update(ctx, &settingapi.UpdateRequest{MaxConcurrentRuns: 3, RetentionDays: 10, Timezone: "UTC"})
			require.NoError(t, err)
			bad := []struct {
				req  settingapi.UpdateRequest
				code int
			}{
				{settingapi.UpdateRequest{MaxConcurrentRuns: 0, RetentionDays: 10, Timezone: "UTC"}, code.SettingMaxConcurrentInvalid},
				{settingapi.UpdateRequest{MaxConcurrentRuns: 3, RetentionDays: 0, Timezone: "UTC"}, code.SettingRetentionInvalid},
				{settingapi.UpdateRequest{MaxConcurrentRuns: 3, RetentionDays: 10, Timezone: "Not/AZone"}, code.SettingTimezoneInvalid},
			}
			for _, b := range bad {
				req := b.req
				_, err := svc.Update(ctx, &req)
				var he *httputils.Error
				require.ErrorAs(t, err, &he)
				assert.Equal(t, b.code, he.Code)
			}
			got, _ := svc.Get(ctx)
			assert.Equal(t, 3, got.MaxConcurrentRuns)
			assert.Equal(t, 10, got.RetentionDays)
		})
	})
}
