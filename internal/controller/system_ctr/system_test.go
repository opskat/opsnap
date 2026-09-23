package system_ctr

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/server/mux/muxtest"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/cago-frame/cago/configs"

	api "github.com/opskat/opsnap/internal/api/system"
	"github.com/opskat/opsnap/internal/repository/system_repo"
	mock_system_repo "github.com/opskat/opsnap/internal/repository/system_repo/mock"
)

func setupSystemTest(t *testing.T) (context.Context, *mock_system_repo.MockSystemRepo, *muxtest.TestMux) {
	mockCtrl := gomock.NewController(t)
	t.Cleanup(mockCtrl.Finish)
	mockSystemRepo := mock_system_repo.NewMockSystemRepo(mockCtrl)
	system_repo.RegisterSystem(mockSystemRepo)
	testMux := muxtest.NewTestMux()
	ctr := NewSystem()
	testMux.Group("/api/v1").Bind(ctr.Health)
	return context.Background(), mockSystemRepo, testMux
}

func TestSystemHealth(t *testing.T) {
	ctx, mockSystemRepo, testMux := setupSystemTest(t)
	convey.Convey("健康检查", t, func() {
		convey.Convey("元数据库可用时返回版本号与 ok", func() {
			mockSystemRepo.EXPECT().Ping(gomock.Any()).Return(nil)
			resp := &api.HealthResponse{}
			err := testMux.Do(ctx, &api.HealthRequest{}, resp)
			assert.NoError(t, err)
			assert.Equal(t, configs.Version, resp.Version)
			assert.Equal(t, api.DatabaseOK, resp.Database)
		})
		convey.Convey("元数据库不可用时仍返回响应，但状态为 error", func() {
			mockSystemRepo.EXPECT().Ping(gomock.Any()).Return(errors.New("database is locked"))
			resp := &api.HealthResponse{}
			err := testMux.Do(ctx, &api.HealthRequest{}, resp)
			assert.NoError(t, err)
			assert.Equal(t, api.DatabaseError, resp.Database)
		})
	})
}
