// Package credential_repo 密钥库（服务器登录用的密码与私钥）的数据访问
package credential_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/credential_entity"
)

// CredentialRepo 密钥仓储
//
//go:generate mockgen -source credential.go -destination mock_credential_repo/credential.go -package mock_credential_repo
type CredentialRepo interface {
	// Find 按 ID 查找，不存在返回 nil
	Find(ctx context.Context, id int64) (*credential_entity.Credential, error)
	// ListShared 列出全部共享密钥，按名称排序
	ListShared(ctx context.Context) ([]*credential_entity.Credential, error)
	// FindPrivate 取某台服务器的私有凭据：它引用的那行 shared = 0 时返回，否则返回 nil
	FindPrivate(ctx context.Context, serverID int64) (*credential_entity.Credential, error)
	Create(ctx context.Context, c *credential_entity.Credential) error
	Update(ctx context.Context, c *credential_entity.Credential) error
	Delete(ctx context.Context, id int64) error
	// CountServersByCredentialID 有多少台服务器引用这条凭据
	CountServersByCredentialID(ctx context.Context, id int64) (int64, error)
	// ServersByCredentialID 引用这条凭据的服务器（id 与名称），按 ID 升序
	ServersByCredentialID(ctx context.Context, id int64) ([]*ServerRef, error)
}

// ServerRef 引用凭据的服务器；地址与用户是明文信息，用于界面写「用户@地址」
type ServerRef struct {
	ID       int64  `gorm:"column:id"`
	Name     string `gorm:"column:name"`
	Host     string `gorm:"column:host"`
	Port     int    `gorm:"column:port"`
	Username string `gorm:"column:username"`
}

var defaultCredential CredentialRepo

// Credential 取得已注册的仓储
func Credential() CredentialRepo { return defaultCredential }

// RegisterCredential 注册仓储实现
func RegisterCredential(i CredentialRepo) { defaultCredential = i }

type credentialRepo struct{}

// NewCredential 创建基于 GORM 的实现
func NewCredential() CredentialRepo { return &credentialRepo{} }

func (r *credentialRepo) Find(ctx context.Context, id int64) (*credential_entity.Credential, error) {
	ret := &credential_entity.Credential{}
	if err := db.Ctx(ctx).Where("id = ?", id).First(ret).Error; err != nil {
		if db.RecordNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ret, nil
}

func (r *credentialRepo) ListShared(ctx context.Context) ([]*credential_entity.Credential, error) {
	var list []*credential_entity.Credential
	if err := db.Ctx(ctx).Where("shared = ?", true).Order("name ASC, id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *credentialRepo) FindPrivate(ctx context.Context, serverID int64) (*credential_entity.Credential, error) {
	ret := &credential_entity.Credential{}
	err := db.Ctx(ctx).
		Joins("JOIN servers ON servers.credential_id = credentials.id").
		Where("servers.id = ? AND credentials.shared = ?", serverID, false).
		First(ret).Error
	if err != nil {
		if db.RecordNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ret, nil
}

func (r *credentialRepo) Create(ctx context.Context, c *credential_entity.Credential) error {
	return db.Ctx(ctx).Create(c).Error
}

func (r *credentialRepo) Update(ctx context.Context, c *credential_entity.Credential) error {
	return db.Ctx(ctx).Select("*").Omit("id", "createtime").Where("id = ?", c.ID).Updates(c).Error
}

func (r *credentialRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Where("id = ?", id).Delete(&credential_entity.Credential{}).Error
}

func (r *credentialRepo) CountServersByCredentialID(ctx context.Context, id int64) (int64, error) {
	var n int64
	if err := db.Ctx(ctx).Table("servers").Where("credential_id = ?", id).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

func (r *credentialRepo) ServersByCredentialID(ctx context.Context, id int64) ([]*ServerRef, error) {
	var list []*ServerRef
	err := db.Ctx(ctx).Table("servers").
		Select("id, name, host, port, username").
		Where("credential_id = ?", id).
		Order("id ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}
