// Package admin_entity 单管理员账号
package admin_entity

import "time"

const (
	// MaxLoginFailures 连续登录失败多少次后锁定
	MaxLoginFailures = 5
	// LockDuration 锁定时长
	LockDuration = 10 * time.Minute
	// SingletonID 管理员只有一个，主键固定为 1
	SingletonID = 1
)

// Admin 管理员账号
type Admin struct {
	ID           int64  `gorm:"column:id;primaryKey;autoIncrement:false"`
	Username     string `gorm:"column:username;not null"`
	PasswordHash string `gorm:"column:password_hash;not null"`
	FailedCount  int    `gorm:"column:failed_count;not null"`
	LockedUntil  int64  `gorm:"column:locked_until;not null"`
	Createtime   int64  `gorm:"column:createtime"`
	Updatetime   int64  `gorm:"column:updatetime"`
}

func (Admin) TableName() string { return "admin" }

// IsLocked 在 now 时刻是否处于锁定中
func (a *Admin) IsLocked(now time.Time) bool {
	return a.LockedUntil > now.Unix()
}

// LockRemaining 距解锁的剩余时间，未锁定返回 0
func (a *Admin) LockRemaining(now time.Time) time.Duration {
	if !a.IsLocked(now) {
		return 0
	}
	return time.Unix(a.LockedUntil, 0).Sub(now)
}

// RecordFailure 记录一次登录失败，返回还剩几次机会以及是否因此被锁定。
// 达到上限时设置锁定截止时间，并把计数清零，解锁后重新计数。
func (a *Admin) RecordFailure(now time.Time) (remaining int, locked bool) {
	a.FailedCount++
	if a.FailedCount >= MaxLoginFailures {
		a.FailedCount = 0
		a.LockedUntil = now.Add(LockDuration).Unix()
		return 0, true
	}
	return MaxLoginFailures - a.FailedCount, false
}

// ResetFailures 登录成功后清除失败计数和锁定
func (a *Admin) ResetFailures() {
	a.FailedCount = 0
	a.LockedUntil = 0
}
