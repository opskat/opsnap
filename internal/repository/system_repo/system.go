// Package system_repo 提供与业务表无关的系统级数据访问。
package system_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"
)

//go:generate mockgen -source system.go -destination mock/system.go

type SystemRepo interface {
	// Ping 检查元数据库连接是否可用
	Ping(ctx context.Context) error
}

var defaultSystem SystemRepo

func System() SystemRepo {
	return defaultSystem
}

func RegisterSystem(i SystemRepo) {
	defaultSystem = i
}

type systemRepo struct{}

func NewSystem() SystemRepo {
	return &systemRepo{}
}

func (r *systemRepo) Ping(ctx context.Context) error {
	sqlDB, err := db.Ctx(ctx).DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}
