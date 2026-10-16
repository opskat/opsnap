package apikey_svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/glebarez/sqlite"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	apikeyapi "github.com/opskat/opsnap/internal/api/apikey"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/repository/apikey_repo"
	"github.com/opskat/opsnap/migrations"
)

func setupAPIKeyTest(t *testing.T) (context.Context, *apiKeySvc, *time.Time, *gorm.DB) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, migrations.RunMigrations(gdb))
	apikey_repo.RegisterAPIKey(apikey_repo.NewAPIKey())
	now := time.Unix(1_800_000_000, 0)
	return db.WithContextDB(context.Background(), gdb), newAPIKey(func() time.Time { return now }), &now, gdb
}

func TestCreateAPIKey(t *testing.T) {
	convey.Convey("创建 API 密钥", t, func() {
		ctx, svc, now, gdb := setupAPIKeyTest(t)
		resp, err := svc.Create(ctx, &apikeyapi.CreateRequest{Name: "ci", ExpiresIn: apikeyapi.Expires30Days})
		require.NoError(t, err)

		convey.Convey("完整密钥带 osk_ 前缀，列表项只有前缀", func() {
			assert.True(t, strings.HasPrefix(resp.Key, "osk_"))
			assert.GreaterOrEqual(t, len(resp.Key), 40)
			assert.True(t, strings.HasPrefix(resp.Key, resp.Prefix))
			assert.Less(t, len(resp.Prefix), len(resp.Key))
			assert.Equal(t, now.Add(30*24*time.Hour).Unix(), resp.ExpiresAt)
		})

		convey.Convey("数据库任何一列都不含完整密钥，只有 sha256 哈希", func() {
			rows, err := gdb.Raw(`SELECT * FROM api_keys`).Rows()
			require.NoError(t, err)
			defer func() { _ = rows.Close() }()
			cols, _ := rows.Columns()
			require.True(t, rows.Next())
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			require.NoError(t, rows.Scan(ptrs...))
			sum := sha256.Sum256([]byte(resp.Key))
			foundHash := false
			for i, v := range vals {
				s, _ := v.(string)
				assert.NotContains(t, s, resp.Key, cols[i])
				if s == hex.EncodeToString(sum[:]) {
					foundHash = true
				}
			}
			assert.True(t, foundHash)
		})

		convey.Convey("列表不返回完整密钥或哈希", func() {
			list, err := svc.List(ctx)
			require.NoError(t, err)
			require.Len(t, list.List, 1)
			b, _ := json.Marshal(list)
			assert.NotContains(t, string(b), resp.Key)
			assert.NotContains(t, string(b), "hash")
			assert.Equal(t, "ci", list.List[0].Name)
			assert.Equal(t, resp.Prefix, list.List[0].Prefix)
		})

		convey.Convey("每次生成的密钥不同", func() {
			other, err := svc.Create(ctx, &apikeyapi.CreateRequest{Name: "ci2", ExpiresIn: apikeyapi.ExpiresNever})
			require.NoError(t, err)
			assert.NotEqual(t, resp.Key, other.Key)
			assert.Equal(t, int64(0), other.ExpiresAt, "永不过期记为 0")
		})

		convey.Convey("90 天", func() {
			r, err := svc.Create(ctx, &apikeyapi.CreateRequest{Name: "x", ExpiresIn: apikeyapi.Expires90Days})
			require.NoError(t, err)
			assert.Equal(t, now.Add(90*24*time.Hour).Unix(), r.ExpiresAt)
		})

		convey.Convey("撤销后从列表消失，重复撤销报不存在", func() {
			_, err := svc.Revoke(ctx, &apikeyapi.RevokeRequest{ID: resp.ID})
			require.NoError(t, err)
			list, _ := svc.List(ctx)
			assert.Empty(t, list.List)
			_, err = svc.Revoke(ctx, &apikeyapi.RevokeRequest{ID: resp.ID})
			var he *httputils.Error
			require.ErrorAs(t, err, &he)
			assert.Equal(t, code.APIKeyNotFound, he.Code)
		})
	})
}
