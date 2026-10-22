package snapshot_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
)

func TestRestoreCommand(t *testing.T) {
	cases := []struct {
		name, source, file string
		gz                 bool
		want               []string
		notWant            []string
	}{
		{"mysql 压缩", "mysql", "shop.sql", true, []string{"gunzip < shop.sql.gz | mysql", "-p"}, nil},
		{"mysql 原样", "mysql", "shop.sql", false, []string{"mysql", "< shop.sql"}, []string{"gunzip"}},
		{"postgres", "postgres", "app.dump", false, []string{"pg_restore", "--dbname=", "app.dump"}, []string{"gunzip"}},
		{"postgres 压缩", "postgres", "app.dump", true, []string{"gunzip < app.dump.gz | pg_restore", "--dbname="}, nil},
		{"mongodb", "mongodb", "all.archive", false, []string{"mongorestore", "--archive=all.archive"}, []string{"--gzip"}},
		{"mongodb 压缩", "mongodb", "all.archive", true, []string{"gunzip < all.archive.gz | mongorestore"}, nil},
		{"redis", "redis", "dump.rdb", false, []string{"dump.rdb", "appendonly", "redis-server"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := restoreCommand(c.source, c.file, c.gz)
			for _, w := range c.want {
				assert.Contains(t, got, w)
			}
			for _, w := range c.notWant {
				assert.NotContains(t, got, w)
			}
			assert.NotContains(t, got, "PASSWORD", "示例里不放任何凭据")
		})
	}
}

func TestReasonsFromMatches(t *testing.T) {
	assert.Equal(t, []string{"last", "daily", "weekly", "monthly"},
		reasonsFromMatches([]string{"last snapshot", "daily snapshot", "weekly snapshot", "monthly snapshot", "yearly snapshot"}))
	assert.Equal(t, []string{"last"}, reasonsFromMatches([]string{"within 7d"}), "keep-within 归为最近")
	// restic ≥0.17 在快照数不足时用「oldest …」说明保留了最老的一份
	assert.Equal(t, []string{"daily", "monthly"}, reasonsFromMatches([]string{"oldest daily snapshot", "oldest monthly snapshot"}))
	assert.Empty(t, reasonsFromMatches(nil))
}

func TestFailureMasksOnlySecrets(t *testing.T) {
	cmd := cmdbuild.Cmd{Name: "restic", Env: []string{"RESTIC_REPOSITORY=/volume1/opsnap", "RESTIC_PASSWORD=SECRET-repo-key", "RCLONE_CONFIG_OPSNAP_PORT=22"}}
	err := failure(context.Background(), cmd, 1, nil, "Fatal: open /volume1/opsnap/config at 2026-10-22: wrong key SECRET-repo-key\n")
	msg := err.Error()
	assert.NotContains(t, msg, "SECRET-repo-key")
	assert.Contains(t, msg, "/volume1/opsnap/config", "仓库地址不是凭据，错误信息要保持可读")
	assert.Contains(t, msg, "2026-10-22")
}
