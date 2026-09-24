package kopiarepo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocationNormalize(t *testing.T) {
	t.Run("本地路径规范化，相对路径不合法", func(t *testing.T) {
		loc := Location{Kind: KindLocal, Path: "/data//backups/../repo/"}
		assert.NoError(t, loc.Normalize())
		assert.Equal(t, "/data/repo", loc.Path)

		rel := Location{Kind: KindLocal, Path: "data/repo"}
		assert.ErrorIs(t, rel.Normalize(), ErrRelativePath)
	})

	t.Run("S3 前缀去掉首尾斜杠后以斜杠结尾，Endpoint 不含协议", func(t *testing.T) {
		loc := Location{Kind: KindS3, Endpoint: "MinIO.lan:9000", Bucket: "b", Prefix: "/opsnap/prod/"}
		assert.NoError(t, loc.Normalize())
		assert.Equal(t, "opsnap/prod/", loc.Prefix)
		assert.Equal(t, "minio.lan:9000", loc.Endpoint)
		assert.Equal(t, "s3://b/opsnap/prod/", loc.String())

		root := Location{Kind: KindS3, Endpoint: "s3.amazonaws.com", Bucket: "b", Prefix: "/"}
		assert.NoError(t, root.Normalize())
		assert.Equal(t, "", root.Prefix)
		assert.Equal(t, "s3://b/", root.String())

		withScheme := Location{Kind: KindS3, Endpoint: "https://s3.amazonaws.com", Bucket: "b"}
		assert.ErrorIs(t, withScheme.Normalize(), ErrEndpointScheme)
	})

	t.Run("同一位置的 Key 相同，前缀不同则不同", func(t *testing.T) {
		a := Location{Kind: KindS3, Endpoint: "minio.lan:9000", Bucket: "b", Prefix: "x", AccessKey: "k1"}
		b := Location{Kind: KindS3, Endpoint: "MINIO.lan:9000", Bucket: "b", Prefix: "/x/", AccessKey: "k2", UseTLS: true}
		c := Location{Kind: KindS3, Endpoint: "minio.lan:9000", Bucket: "b", Prefix: "y"}
		for _, l := range []*Location{&a, &b, &c} {
			assert.NoError(t, l.Normalize())
		}
		assert.Equal(t, a.Key(), b.Key())
		assert.NotEqual(t, a.Key(), c.Key())

		l1 := Location{Kind: KindLocal, Path: "/data/repo/"}
		l2 := Location{Kind: KindLocal, Path: "/data/./repo"}
		_ = l1.Normalize()
		_ = l2.Normalize()
		assert.Equal(t, l1.Key(), l2.Key())
	})
}
