// Package admin_entity 定义管理员与会话实体。
package admin_entity

import "time"

// AdminID 唯一管理员的固定主键
const AdminID int64 = 1

// SessionTTL 会话连续不使用超过该时长即过期；每次使用顺延
const SessionTTL = 7 * 24 * time.Hour

type Admin struct {
	ID                 int64  `gorm:"column:id;primaryKey"`
	Username           string `gorm:"column:username"`
	PasswordHash       string `gorm:"column:password_hash"`
	PasswordUpdatetime int64  `gorm:"column:password_updatetime"`
	Createtime         int64  `gorm:"column:createtime"`
	Updatetime         int64  `gorm:"column:updatetime"`
}

func (Admin) TableName() string { return "admins" }

type Session struct {
	ID         int64  `gorm:"column:id;primaryKey"`
	TokenHash  string `gorm:"column:token_hash"`
	AdminID    int64  `gorm:"column:admin_id"`
	UserAgent  string `gorm:"column:user_agent"`
	IP         string `gorm:"column:ip"`
	Expiretime int64  `gorm:"column:expiretime"`
	Createtime int64  `gorm:"column:createtime"`
	Updatetime int64  `gorm:"column:updatetime"`
}

func (Session) TableName() string { return "sessions" }

// Expired 会话是否已过期
func (s *Session) Expired(now time.Time) bool {
	return now.Unix() >= s.Expiretime
}
