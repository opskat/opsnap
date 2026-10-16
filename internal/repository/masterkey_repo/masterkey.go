// Package masterkey_repo 主密钥校验值与全部加密凭据的重新加密
package masterkey_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/model/entity/masterkey_entity"
	"github.com/opskat/opsnap/internal/model/entity/notify_entity"
	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
	"github.com/opskat/opsnap/internal/model/entity/server_entity"
)

// MasterKeyRepo 主密钥仓储
//
//go:generate mockgen -source masterkey.go -destination mock_masterkey_repo/masterkey.go -package mock_masterkey_repo
type MasterKeyRepo interface {
	// Get 读取校验值，从未写过时返回 nil
	Get(ctx context.Context) (*masterkey_entity.MasterKey, error)
	// Save 覆盖保存校验值
	Save(ctx context.Context, m *masterkey_entity.MasterKey) error
	// Rekey 在一个事务里把所有加密保存的凭据交给 fn 换成新密文，并保存新的校验值；
	// fn 对任何一项返回错误时整体回滚，数据库保持原状。空串表示没有该凭据，不交给 fn。
	Rekey(ctx context.Context, fn func(cipher string) (string, error), m *masterkey_entity.MasterKey) error
}

var defaultMasterKey MasterKeyRepo

// MasterKey 取得已注册的仓储
func MasterKey() MasterKeyRepo { return defaultMasterKey }

// RegisterMasterKey 注册仓储实现
func RegisterMasterKey(i MasterKeyRepo) { defaultMasterKey = i }

type masterKeyRepo struct{}

// NewMasterKey 创建基于 GORM 的实现
func NewMasterKey() MasterKeyRepo { return &masterKeyRepo{} }

func (r *masterKeyRepo) Get(ctx context.Context) (*masterkey_entity.MasterKey, error) {
	ret := &masterkey_entity.MasterKey{}
	if err := db.Ctx(ctx).Where("id = ?", masterkey_entity.SingletonID).First(ret).Error; err != nil {
		if db.RecordNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ret, nil
}

func (r *masterKeyRepo) Save(ctx context.Context, m *masterkey_entity.MasterKey) error {
	return save(db.Ctx(ctx), m)
}

func save(tx *gorm.DB, m *masterkey_entity.MasterKey) error {
	m.ID = masterkey_entity.SingletonID
	return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(m).Error
}

func (r *masterKeyRepo) Rekey(ctx context.Context, fn func(string) (string, error), m *masterkey_entity.MasterKey) error {
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		conv := func(fields ...*string) error {
			for _, f := range fields {
				if *f == "" {
					continue
				}
				v, err := fn(*f)
				if err != nil {
					return err
				}
				*f = v
			}
			return nil
		}

		var servers []*server_entity.Server
		if err := tx.Find(&servers).Error; err != nil {
			return err
		}
		for _, s := range servers {
			fields := []*string{&s.Password, &s.PrivateKey, &s.Passphrase}
			for i := range s.Hops {
				fields = append(fields, &s.Hops[i].Password)
			}
			if err := conv(fields...); err != nil {
				return err
			}
			if err := tx.Model(s).Select("password", "private_key", "passphrase", "hops").Updates(s).Error; err != nil {
				return err
			}
		}

		var repos []*repo_entity.Repo
		if err := tx.Find(&repos).Error; err != nil {
			return err
		}
		for _, rp := range repos {
			if err := conv(&rp.Credential, &rp.PrivateKey, &rp.OpsnapKey); err != nil {
				return err
			}
			if err := tx.Model(rp).Select("credential", "private_key", "opsnap_key").Updates(rp).Error; err != nil {
				return err
			}
		}

		var jobs []*job_entity.Job
		if err := tx.Find(&jobs).Error; err != nil {
			return err
		}
		for _, j := range jobs {
			if err := conv(&j.DBPassword); err != nil {
				return err
			}
			if err := tx.Model(j).Select("db_password").Updates(j).Error; err != nil {
				return err
			}
		}

		var notifies []*notify_entity.Notify
		if err := tx.Find(&notifies).Error; err != nil {
			return err
		}
		for _, n := range notifies {
			for name, v := range n.Headers {
				if err := conv(&v); err != nil {
					return err
				}
				n.Headers[name] = v
			}
			if err := tx.Model(n).Select("headers").Updates(n).Error; err != nil {
				return err
			}
		}
		return save(tx, m)
	})
}
