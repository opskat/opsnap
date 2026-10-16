package apikey_ctr

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

	apikeyapi "github.com/opskat/opsnap/internal/api/apikey"
	"github.com/opskat/opsnap/internal/model/entity/apikey_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/apikey_repo"
	"github.com/opskat/opsnap/internal/repository/apikey_repo/mock_apikey_repo"
	"github.com/opskat/opsnap/internal/service/apikey_svc"
)

var now = time.Unix(1_800_000_000, 0)

func setupAPIKeyTest(t *testing.T) (context.Context, *mock_apikey_repo.MockAPIKeyRepo, *muxtest.TestMux) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_apikey_repo.NewMockAPIKeyRepo(ctrl)
	apikey_repo.RegisterAPIKey(repo)
	apikey_svc.RegisterAPIKey(apikey_svc.NewAPIKey(func() time.Time { return now }))
	testMux := muxtest.NewTestMux()
	ctr := NewAPIKey()
	testMux.Group("/api/v1").Bind(ctr.Create, ctr.List, ctr.Revoke)
	return context.Background(), repo, testMux
}

func TestAPIKeyController(t *testing.T) {
	convey.Convey("API 密钥接口", t, func() {
		ctx, repo, testMux := setupAPIKeyTest(t)

		convey.Convey("创建：返回一次完整密钥，仓储只收到哈希", func() {
			var storedHash string
			repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, k *apikey_entity.APIKey) error {
					k.ID = 9
					storedHash = k.KeyHash
					assert.Equal(t, "ci", k.Name)
					assert.Equal(t, now.Add(90*24*time.Hour).Unix(), k.ExpiresAt)
					assert.Len(t, k.KeyHash, 64)
					assert.NotContains(t, k.KeyHash, "osk_")
					return nil
				})
			resp := &apikeyapi.CreateResponse{}
			require.NoError(t, testMux.Do(ctx, &apikeyapi.CreateRequest{Name: "ci", ExpiresIn: "90d"}, resp))
			assert.Equal(t, int64(9), resp.ID)
			assert.Contains(t, resp.Key, "osk_")
			assert.Equal(t, apikey_entity.HashKey(resp.Key), storedHash)
		})

		convey.Convey("创建：过期选项不合法被拒绝", func() {
			err := testMux.Do(ctx, &apikeyapi.CreateRequest{Name: "ci", ExpiresIn: "1y"}, &apikeyapi.CreateResponse{})
			assert.Error(t, err)
		})

		convey.Convey("撤销不存在的密钥返回 404", func() {
			repo.EXPECT().Revoke(gomock.Any(), int64(5), now.Unix()).Return(false, nil)
			err := testMux.Do(ctx, &apikeyapi.RevokeRequest{ID: 5}, &apikeyapi.RevokeResponse{})
			var he *httputils.Error
			require.ErrorAs(t, err, &he)
			assert.Equal(t, code.APIKeyNotFound, he.Code)
			assert.Equal(t, 404, he.Status)
		})

		convey.Convey("列表", func() {
			repo.EXPECT().List(gomock.Any()).Return([]*apikey_entity.APIKey{{ID: 1, Name: "ci", Prefix: "osk_abcd1234", KeyHash: "secret-hash"}}, nil)
			resp := &apikeyapi.ListResponse{}
			require.NoError(t, testMux.Do(ctx, &apikeyapi.ListRequest{}, resp))
			require.Len(t, resp.List, 1)
			assert.Equal(t, "osk_abcd1234", resp.List[0].Prefix)
		})
	})
}
