package probe

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/dsconn"
)

func TestRunDispatchesByType(t *testing.T) {
	var gotConn *dsconn.Conn
	Register("test_type", func(_ context.Context, conn *dsconn.Conn) []Item {
		gotConn = conn
		return []Item{{Key: "test_type.ok", Tier: TierOK}}
	})

	conn := &dsconn.Conn{}
	items := Run(context.Background(), "test_type", conn)

	require.Len(t, items, 1)
	assert.Equal(t, "test_type.ok", items[0].Key)
	assert.Same(t, conn, gotConn)
}

func TestRunUnknownTypeReturnsNil(t *testing.T) {
	items := Run(context.Background(), "does_not_exist", &dsconn.Conn{})
	assert.Nil(t, items)
}

func TestRunRegistersBuiltinTypes(t *testing.T) {
	for _, typ := range []dsconn.Type{dsconn.TypeMySQL, dsconn.TypePostgreSQL, dsconn.TypeServerFile} {
		_, ok := registry[typ]
		assert.True(t, ok, "%s 应已注册探测项", typ)
	}
}
