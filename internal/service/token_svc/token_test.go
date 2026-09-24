package token_svc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/opskat/opsnap/internal/api/token"
	"github.com/opskat/opsnap/internal/model/entity/admin_entity"
	"github.com/opskat/opsnap/internal/model/entity/token_entity"
	"github.com/opskat/opsnap/internal/pkg/authctx"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/testdb"
	"github.com/opskat/opsnap/internal/repository/admin_repo"
	"github.com/opskat/opsnap/internal/repository/token_repo"
)

func errCode(err error) int {
	var e *httputils.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func errStatus(err error) int {
	var e *httputils.Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

func setupTokenTest(t *testing.T) (context.Context, *tokenSvc, *time.Time) {
	ctx := testdb.New(t)
	admin_repo.RegisterAdmin(admin_repo.NewAdmin())
	token_repo.RegisterToken(token_repo.NewToken())
	require.NoError(t, admin_repo.Admin().Create(ctx, &admin_entity.Admin{Username: "admin", PasswordHash: "x"}))
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	s := newToken()
	s.now = func() time.Time { return now }
	return ctx, s, &now
}

func TestTokenLifecycle(t *testing.T) {
	convey.Convey("API 令牌", t, func() {
		ctx, s, now := setupTokenTest(t)

		convey.Convey("生成：完整令牌以 onp_ 开头，只保存哈希，列表只显示前缀", func() {
			resp, err := s.Create(ctx, &api.CreateRequest{Name: "ci-deploy", ExpiresInDays: 90})
			require.NoError(t, err)
			assert.True(t, strings.HasPrefix(resp.Token, "onp_"))
			assert.Greater(t, len(resp.Token), 40)
			assert.Equal(t, now.Add(90*24*time.Hour).Unix(), resp.Item.ExpiresAt)
			assert.Equal(t, token_entity.StatusActive, resp.Item.Status)
			assert.Equal(t, resp.Token[:8], resp.Item.Prefix)

			var rows []token_entity.Token
			require.NoError(t, db.Ctx(ctx).Find(&rows).Error)
			require.Len(t, rows, 1)
			assert.NotContains(t, rows[0].TokenHash, resp.Token)
			assert.NotEqual(t, resp.Token, rows[0].TokenHash)

			list, err := s.List(ctx, &api.ListRequest{})
			require.NoError(t, err)
			require.Len(t, list.Items, 1)
			assert.Equal(t, "ci-deploy", list.Items[0].Name)
			assert.Equal(t, int64(0), list.Items[0].LastUsedAt)

			convey.Convey("有效令牌可以认证，最近使用时间精确到分钟", func() {
				p, err := s.Authenticate(ctx, resp.Token)
				require.NoError(t, err)
				assert.Equal(t, authctx.ViaToken, p.Via)
				assert.Equal(t, "admin", p.Username)
				assert.Equal(t, now.Unix(), lastUsed(t, s, ctx))

				minute := func(h, m, sec int) time.Time { return time.Date(2026, 9, 23, h, m, sec, 0, time.UTC) }
				for _, c := range []struct {
					at   time.Time
					want time.Time
					msg  string
				}{
					{minute(10, 0, 30), minute(10, 0, 0), "同一分钟内不重复更新"},
					{minute(10, 1, 10), minute(10, 1, 0), "进入新的一分钟后更新，按分钟取整"},
					{minute(10, 1, 50), minute(10, 1, 0), "同一分钟内不重复更新"},
					{minute(10, 2, 5), minute(10, 2, 0), "距上次不足 60 秒但已是新的一分钟，也要更新"},
				} {
					*now = c.at
					_, err = s.Authenticate(ctx, resp.Token)
					require.NoError(t, err)
					assert.Equal(t, c.want.Unix(), lastUsed(t, s, ctx), c.msg)
				}
			})

			convey.Convey("吊销后立即失效，并提示已吊销；列表中保留且状态为已吊销", func() {
				_, err := s.Revoke(ctx, &api.RevokeRequest{ID: resp.Item.ID})
				require.NoError(t, err)
				_, err = s.Authenticate(ctx, resp.Token)
				assert.Equal(t, code.TokenRevoked, errCode(err))
				assert.Equal(t, 401, errStatus(err))
				list, _ := s.List(ctx, &api.ListRequest{})
				assert.Equal(t, token_entity.StatusRevoked, list.Items[0].Status)

				convey.Convey("吊销后可以再用同一名称生成", func() {
					_, err := s.Create(ctx, &api.CreateRequest{Name: "ci-deploy", ExpiresInDays: 30})
					assert.NoError(t, err)
				})
			})

			convey.Convey("到期后失效，并提示已过期", func() {
				*now = now.Add(90 * 24 * time.Hour)
				_, err := s.Authenticate(ctx, resp.Token)
				assert.Equal(t, code.TokenExpired, errCode(err))
				list, _ := s.List(ctx, &api.ListRequest{})
				assert.Equal(t, token_entity.StatusExpired, list.Items[0].Status)

				convey.Convey("过期后可以再用同一名称生成", func() {
					_, err := s.Create(ctx, &api.CreateRequest{Name: "ci-deploy", ExpiresInDays: 30})
					assert.NoError(t, err)
				})
			})

			convey.Convey("有效令牌不能重名", func() {
				_, err := s.Create(ctx, &api.CreateRequest{Name: "ci-deploy", ExpiresInDays: 30})
				assert.Equal(t, code.TokenNameDuplicate, errCode(err))
			})
		})

		convey.Convey("永不过期的令牌 expires_at 为 0，远期仍可用", func() {
			resp, err := s.Create(ctx, &api.CreateRequest{Name: "forever", ExpiresInDays: 0})
			require.NoError(t, err)
			assert.Equal(t, int64(0), resp.Item.ExpiresAt)
			*now = now.Add(10 * 365 * 24 * time.Hour)
			_, err = s.Authenticate(ctx, resp.Token)
			assert.NoError(t, err)
		})

		convey.Convey("名称与有效期校验", func() {
			_, err := s.Create(ctx, &api.CreateRequest{Name: "   ", ExpiresInDays: 30})
			assert.Equal(t, code.TokenNameInvalid, errCode(err))
			_, err = s.Create(ctx, &api.CreateRequest{Name: strings.Repeat("a", 65), ExpiresInDays: 30})
			assert.Equal(t, code.TokenNameInvalid, errCode(err))
			_, err = s.Create(ctx, &api.CreateRequest{Name: strings.Repeat("名", 64), ExpiresInDays: 30})
			assert.NoError(t, err, "按字符计 64 个")
			_, err = s.Create(ctx, &api.CreateRequest{Name: "x", ExpiresInDays: 7})
			assert.Equal(t, code.TokenExpiryInvalid, errCode(err))
		})

		convey.Convey("不存在或格式不对的令牌提示无效", func() {
			for _, tok := range []string{"", "onp_nope", "garbage"} {
				_, err := s.Authenticate(ctx, tok)
				assert.Equal(t, code.TokenInvalid, errCode(err), tok)
			}
		})

		convey.Convey("吊销不存在的令牌返回 404", func() {
			_, err := s.Revoke(ctx, &api.RevokeRequest{ID: 999})
			assert.Equal(t, code.TokenNotFound, errCode(err))
			assert.Equal(t, 404, errStatus(err))
		})
	})
}

func lastUsed(t *testing.T, s *tokenSvc, ctx context.Context) int64 {
	t.Helper()
	list, err := s.List(ctx, &api.ListRequest{})
	require.NoError(t, err)
	return list.Items[0].LastUsedAt
}
