// Package testdb 为服务层与仓库层测试提供已执行迁移的临时 SQLite 数据库。
package testdb

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/opskat/opsnap/migrations"
)

// New 创建临时数据库、执行全部迁移并设为 cago 默认数据库；测试结束后关闭连接
func New(t *testing.T) context.Context {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开测试数据库: %v", err)
	}
	if err := migrations.RunMigrations(gdb); err != nil {
		t.Fatalf("执行迁移: %v", err)
	}
	db.SetDefault(gdb)
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return context.Background()
}
