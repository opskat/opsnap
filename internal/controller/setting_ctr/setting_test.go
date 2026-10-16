package setting_ctr

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	settingapi "github.com/opskat/opsnap/internal/api/setting"
	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
	"github.com/opskat/opsnap/internal/model/entity/masterkey_entity"
	"github.com/opskat/opsnap/internal/model/entity/setting_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/admin_repo/mock_admin_repo"
	"github.com/opskat/opsnap/internal/repository/masterkey_repo"
	"github.com/opskat/opsnap/internal/repository/masterkey_repo/mock_masterkey_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo"
	"github.com/opskat/opsnap/internal/repository/setting_repo/mock_setting_repo"
	"github.com/opskat/opsnap/internal/service/setting_svc"
)

func setupSettingTest(t *testing.T) (context.Context, *mock_setting_repo.MockSettingRepo, *muxtest.TestMux) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_setting_repo.NewMockSettingRepo(ctrl)
	adminRepo := mock_admin_repo.NewMockAdminRepo(ctrl)
	adminRepo.EXPECT().Find(gomock.Any()).Return(&admin_entity.Admin{Username: "root"}, nil).AnyTimes()
	mkRepo := mock_masterkey_repo.NewMockMasterKeyRepo(ctrl)
	mkRepo.EXPECT().Get(gomock.Any()).Return(&masterkey_entity.MasterKey{RotatedAt: 1_700_000_000}, nil).AnyTimes()
	masterkey_repo.RegisterMasterKey(mkRepo)
	setting_repo.RegisterSetting(repo)
	admin_repo.RegisterAdmin(adminRepo)
	setting_svc.RegisterSetting(setting_svc.NewSetting(time.Now))
	testMux := muxtest.NewTestMux()
	ctr := NewSetting()
	testMux.Group("/api/v1").Bind(ctr.Get, ctr.Update, ctr.RotateMasterKey)
	return context.Background(), repo, testMux
}

func TestSettingController(t *testing.T) {
	convey.Convey("设置接口", t, func() {
		ctx, repo, testMux := setupSettingTest(t)

		convey.Convey("读取默认设置", func() {
			repo.EXPECT().Get(gomock.Any()).Return(nil, nil)
			resp := &settingapi.GetResponse{}
			require.NoError(t, testMux.Do(ctx, &settingapi.GetRequest{}, resp))
			assert.Equal(t, "Asia/Shanghai", resp.Timezone)
			assert.Equal(t, "root", resp.AdminUsername)
			assert.Equal(t, int64(1_700_000_000), resp.MasterKey.RotatedAt, "显示最近一次轮换主密钥的时间")
		})

		convey.Convey("轮换主密钥：没有填写新主密钥时拒绝", func() {
			err := testMux.Do(ctx, &settingapi.RotateMasterKeyRequest{NewKey: ""}, &settingapi.RotateMasterKeyResponse{})
			var he *httputils.Error
			require.ErrorAs(t, err, &he)
			assert.Equal(t, code.MasterKeyEmpty, he.Code)
		})

		convey.Convey("保存合法设置", func() {
			repo.EXPECT().Save(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, s *setting_entity.Setting) error {
					assert.Equal(t, 3, s.MaxConcurrentRuns)
					assert.Equal(t, 14, s.RetentionDays)
					assert.Equal(t, "Europe/London", s.Timezone)
					assert.True(t, s.ClearMirrorAfterBackup)
					return nil
				})
			repo.EXPECT().Get(gomock.Any()).Return(&setting_entity.Setting{
				MaxConcurrentRuns: 3, RetentionDays: 14, Timezone: "Europe/London", ClearMirrorAfterBackup: true,
			}, nil)
			resp := &settingapi.UpdateResponse{}
			require.NoError(t, testMux.Do(ctx, &settingapi.UpdateRequest{
				MaxConcurrentRuns: 3, RetentionDays: 14, Timezone: "Europe/London", ClearMirrorAfterBackup: true,
			}, resp))
			assert.Equal(t, "Europe/London", resp.Timezone)
		})

		convey.Convey("非法时区返回 400，不写库", func() {
			err := testMux.Do(ctx, &settingapi.UpdateRequest{MaxConcurrentRuns: 3, RetentionDays: 14, Timezone: "Mars/Base"}, &settingapi.UpdateResponse{})
			var he *httputils.Error
			require.ErrorAs(t, err, &he)
			assert.Equal(t, code.SettingTimezoneInvalid, he.Code)
			assert.Equal(t, 400, he.Status)
		})
	})
}
