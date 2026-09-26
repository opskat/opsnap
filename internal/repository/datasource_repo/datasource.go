// Package datasource_repo 读写数据源记录。
package datasource_repo

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
)

//go:generate mockgen -source datasource.go -destination mock/datasource.go

type DataSourceRepo interface {
	Create(ctx context.Context, d *datasource_entity.DataSource) error
	// Save 按 ID 更新全部字段；记录已被删除时返回 ErrNotFound，不会重新插入
	Save(ctx context.Context, d *datasource_entity.DataSource) error
	// SaveColumns 按 ID 只更新 columns 列，其余列保持库中的值（不覆盖并发修改）；记录已被删除时返回 ErrNotFound
	SaveColumns(ctx context.Context, d *datasource_entity.DataSource, columns ...string) error
	// List 按创建时间正序
	List(ctx context.Context) ([]*datasource_entity.DataSource, error)
	// Find 不存在时返回 nil, nil
	Find(ctx context.Context, id int64) (*datasource_entity.DataSource, error)
	// FindByName 不存在时返回 nil, nil
	FindByName(ctx context.Context, name string) (*datasource_entity.DataSource, error)
	Delete(ctx context.Context, id int64) error
}

// ErrNotFound 保存时记录已不存在（已被删除）
var ErrNotFound = errors.New("data source not found")

var defaultDataSource DataSourceRepo

func DataSource() DataSourceRepo {
	return defaultDataSource
}

func RegisterDataSource(i DataSourceRepo) {
	defaultDataSource = i
}

type dataSourceRepo struct{}

func NewDataSource() DataSourceRepo {
	return &dataSourceRepo{}
}

func (r *dataSourceRepo) Create(ctx context.Context, d *datasource_entity.DataSource) error {
	return db.Ctx(ctx).Create(d).Error
}

func (r *dataSourceRepo) Save(ctx context.Context, d *datasource_entity.DataSource) error {
	// 不用 gorm 的 Save：它在没有匹配行时会改为插入，把测试期间被删除的数据源重新写回
	return r.update(ctx, d, "*")
}

func (r *dataSourceRepo) SaveColumns(ctx context.Context, d *datasource_entity.DataSource, columns ...string) error {
	return r.update(ctx, d, columns...)
}

func (r *dataSourceRepo) update(ctx context.Context, d *datasource_entity.DataSource, columns ...string) error {
	cols := make([]any, 0, len(columns))
	for _, col := range columns[1:] {
		cols = append(cols, col)
	}
	res := db.Ctx(ctx).Model(d).Select(columns[0], cols...).Updates(d)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *dataSourceRepo) List(ctx context.Context) ([]*datasource_entity.DataSource, error) {
	var rows []*datasource_entity.DataSource
	err := db.Ctx(ctx).Order("createtime ASC, id ASC").Find(&rows).Error
	return rows, err
}

func (r *dataSourceRepo) first(ctx context.Context, query string, arg any) (*datasource_entity.DataSource, error) {
	var rows []*datasource_entity.DataSource
	if err := db.Ctx(ctx).Where(query, arg).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func (r *dataSourceRepo) Find(ctx context.Context, id int64) (*datasource_entity.DataSource, error) {
	return r.first(ctx, "id = ?", id)
}

func (r *dataSourceRepo) FindByName(ctx context.Context, name string) (*datasource_entity.DataSource, error) {
	return r.first(ctx, "name = ?", name)
}

func (r *dataSourceRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Delete(&datasource_entity.DataSource{}, id).Error
}
