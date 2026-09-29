package datasource_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
)

// ListDatabases 目前只支持 MySQL 与 PostgreSQL；其余类型（服务器文件）直接返回错误，
// 不触碰 conn.DB（server_file 的连接没有 *sql.DB，触碰会崩溃）
func TestListDatabasesUnsupportedType(t *testing.T) {
	dbs, err := ListDatabases(context.Background(), dsconn.TypeServerFile, &dsconn.Conn{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server_file")
	assert.Nil(t, dbs)
}
