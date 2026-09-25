// Package channel_repo 读写网络通道记录。
package channel_repo

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/database/db"

	"github.com/opskat/opsnap/internal/model/entity/channel_entity"
)

//go:generate mockgen -source channel.go -destination mock/channel.go

type ChannelRepo interface {
	Create(ctx context.Context, c *channel_entity.Channel) error
	// Save 按 ID 更新全部字段；记录已被删除时返回 ErrNotFound，不会重新插入
	Save(ctx context.Context, c *channel_entity.Channel) error
	// List 按创建时间正序
	List(ctx context.Context) ([]*channel_entity.Channel, error)
	// Find 不存在时返回 nil, nil
	Find(ctx context.Context, id int64) (*channel_entity.Channel, error)
	// FindByName 不存在时返回 nil, nil
	FindByName(ctx context.Context, name string) (*channel_entity.Channel, error)
	Delete(ctx context.Context, id int64) error
}

// ErrNotFound 保存时记录已不存在（已被删除）
var ErrNotFound = errors.New("channel not found")

var defaultChannel ChannelRepo

func Channel() ChannelRepo {
	return defaultChannel
}

func RegisterChannel(i ChannelRepo) {
	defaultChannel = i
}

type channelRepo struct{}

func NewChannel() ChannelRepo {
	return &channelRepo{}
}

func (r *channelRepo) Create(ctx context.Context, c *channel_entity.Channel) error {
	return db.Ctx(ctx).Create(c).Error
}

func (r *channelRepo) Save(ctx context.Context, c *channel_entity.Channel) error {
	// 不用 gorm 的 Save：它在没有匹配行时会改为插入，把测试期间被删除的通道重新写回
	res := db.Ctx(ctx).Model(c).Select("*").Updates(c)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *channelRepo) List(ctx context.Context) ([]*channel_entity.Channel, error) {
	var rows []*channel_entity.Channel
	err := db.Ctx(ctx).Order("createtime ASC, id ASC").Find(&rows).Error
	return rows, err
}

func (r *channelRepo) first(ctx context.Context, query string, arg any) (*channel_entity.Channel, error) {
	var rows []*channel_entity.Channel
	if err := db.Ctx(ctx).Where(query, arg).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func (r *channelRepo) Find(ctx context.Context, id int64) (*channel_entity.Channel, error) {
	return r.first(ctx, "id = ?", id)
}

func (r *channelRepo) FindByName(ctx context.Context, name string) (*channel_entity.Channel, error) {
	return r.first(ctx, "name = ?", name)
}

func (r *channelRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Delete(&channel_entity.Channel{}, id).Error
}
