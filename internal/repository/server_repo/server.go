// Package server_repo 服务器的数据访问
package server_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/server_entity"
)

// statusColumns 连接状态列；Update 不写它们，UpdateStatus 只写它们
var statusColumns = []string{"last_check_at", "last_latency_ms", "fail_since", "last_error"}

// ServerRepo 服务器仓储
//
//go:generate mockgen -source server.go -destination mock_server_repo/server.go -package mock_server_repo
type ServerRepo interface {
	// Find 按 ID 查找，不存在返回 nil
	Find(ctx context.Context, id int64) (*server_entity.Server, error)
	// List 列出全部服务器，按 ID 升序
	List(ctx context.Context) ([]*server_entity.Server, error)
	Create(ctx context.Context, s *server_entity.Server) error
	// Update 保存配置，不改连接状态
	Update(ctx context.Context, s *server_entity.Server) error
	Delete(ctx context.Context, id int64) error
	// UpdateStatus 只保存连接状态
	UpdateStatus(ctx context.Context, s *server_entity.Server) error
}

var defaultServer ServerRepo

// Server 取得已注册的仓储
func Server() ServerRepo { return defaultServer }

// RegisterServer 注册仓储实现
func RegisterServer(i ServerRepo) { defaultServer = i }

type serverRepo struct{}

// NewServer 创建基于 GORM 的实现
func NewServer() ServerRepo { return &serverRepo{} }

func (r *serverRepo) Find(ctx context.Context, id int64) (*server_entity.Server, error) {
	ret := &server_entity.Server{}
	if err := db.Ctx(ctx).Where("id = ?", id).First(ret).Error; err != nil {
		if db.RecordNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ret, nil
}

func (r *serverRepo) List(ctx context.Context) ([]*server_entity.Server, error) {
	var list []*server_entity.Server
	if err := db.Ctx(ctx).Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *serverRepo) Create(ctx context.Context, s *server_entity.Server) error {
	return db.Ctx(ctx).Create(s).Error
}

func (r *serverRepo) Update(ctx context.Context, s *server_entity.Server) error {
	return db.Ctx(ctx).Select("*").Omit(append([]string{"id", "createtime"}, statusColumns...)...).
		Where("id = ?", s.ID).Updates(s).Error
}

func (r *serverRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Where("id = ?", id).Delete(&server_entity.Server{}).Error
}

func (r *serverRepo) UpdateStatus(ctx context.Context, s *server_entity.Server) error {
	return db.Ctx(ctx).Model(&server_entity.Server{}).Where("id = ?", s.ID).Updates(map[string]any{
		"last_check_at":   s.LastCheckAt,
		"last_latency_ms": s.LastLatencyMs,
		"fail_since":      s.FailSince,
		"last_error":      s.LastError,
	}).Error
}
